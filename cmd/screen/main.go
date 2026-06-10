// main.go - 实时选股屏幕服务
//
// 该服务实现以下功能：
// 1. 从通达信获取实时行情数据
// 2. 基于预设的MACD反转策略筛选买点
// 3. 基于最近 N 个交易日的买点，使用卖出策略实时判定卖点
// 4. 通过 WebSocket 单一连接（/ws）推送两类消息：
//    - {"type":"buy", ...}  最新买点列表
//    - {"type":"sell", ...} 最新卖点列表
//
// 交易时间：
// - 上午：9:30 - 11:30
// - 下午：13:00 - 15:00
//
// 非交易时间不执行自动刷新

package main

import (
	"encoding/json"
	"sort"
	"sync"
	"time"

	"github.com/injoyai/conv/cfg"
	"github.com/injoyai/frame/fbr" // Web框架
	"github.com/injoyai/logs"      // 日志库
	"github.com/injoyai/strategy-tail"
	"github.com/injoyai/strategy-tail/core" // 选股核心模块
	"github.com/injoyai/tdx"                // 通达信SDK
	"github.com/injoyai/tdx/extend"         // 通达信扩展功能
	"github.com/injoyai/tdx/protocol"       // 通达信协议定义
)

// =========================================================
// 实时行情 / K线 工具
// =========================================================

// getRealtimeQuotes - 批量获取实时行情
// 通达信 API 每次最多支持 80 个代码，需分批拉取
func getRealtimeQuotes(codes []string) (map[string]*protocol.Quote, error) {
	if len(codes) == 0 {
		return nil, nil
	}

	quoteMap := make(map[string]*protocol.Quote, len(codes))
	batchSize := 80

	for i := 0; i < len(codes); i += batchSize {
		end := i + batchSize
		if end > len(codes) {
			end = len(codes)
		}

		if err := common.Manage.Do(func(c *tdx.Client) error {
			quotes, err := c.GetQuote(codes[i:end]...)
			if err != nil {
				return err
			}
			for _, q := range quotes {
				quoteMap[protocol.AddPrefix(q.Code)] = q
			}
			return nil
		}); err != nil {
			logs.Errf("[行情] 批量获取失败(%d-%d): %v", i, end, err)
		}
	}

	return quoteMap, nil
}

// quoteToKline - 将实时行情转换为 K 线（流通股/总股本继承上一日）
func quoteToKline(quote *protocol.Quote, prevKline *extend.Kline) *extend.Kline {
	now := time.Now()
	return &extend.Kline{
		Unix: now.Unix(),
		Kline: &protocol.Kline{
			Last:   quote.K.Last,
			Open:   quote.K.Open,
			High:   quote.K.High,
			Low:    quote.K.Low,
			Close:  quote.K.Close,
			Volume: int64(quote.TotalHand) * 100,
			Amount: protocol.Yuan(quote.Amount),
			Time:   now,
		},
		FloatStock: prevKline.FloatStock,
		TotalStock: prevKline.TotalStock,
	}
}

// makeIntradayGetDayKlines - 构造盘中版 K 线获取函数
// 1. 读取本地历史日线
// 2. 按 [start, end] 过滤
// 3. 如有实时行情，拼接/替换今日 K 线
func makeIntradayGetDayKlines(quoteMap map[string]*protocol.Quote) func(code string, start, end time.Time) (extend.Klines, error) {
	return func(code string, start, end time.Time) (extend.Klines, error) {
		ks, err := common.Pull.DayKlines(code)
		if err != nil {
			return nil, err
		}

		var ls extend.Klines
		for _, k := range ks {
			if !k.Time.Before(start) && !k.Time.After(end) {
				ls = append(ls, k)
			}
		}

		if len(ls) == 0 {
			return ls, nil
		}

		if quote, ok := quoteMap[code]; ok {
			last := ls[len(ls)-1]
			todayKline := quoteToKline(quote, last)

			nowDate := time.Now().Format(time.DateOnly)
			if last.Time.Format(time.DateOnly) == nowDate {
				ls[len(ls)-1] = todayKline
			} else {
				ls = append(ls, todayKline)
			}
		}

		return ls, nil
	}
}

// =========================================================
// 响应数据结构
// =========================================================

// BuyItem - 买入信号条目
type BuyItem struct {
	Code  string  `json:"code"`  // 股票代码（带前缀）
	Time  string  `json:"time"`  // 信号产生时间
	Price float64 `json:"price"` // 当时收盘/现价
	Rise  float64 `json:"rise"`  // 涨幅百分比
}

// BuyResponse - 买点响应
type BuyResponse struct {
	Type    string    `json:"type"`    // 固定 "buy"
	Count   int       `json:"count"`   // 数量
	Time    string    `json:"time"`    // 刷新时间
	Results []BuyItem `json:"results"` // 列表
}

// SellItem - 卖出信号条目
type SellItem struct {
	Code       string  `json:"code"`        // 股票代码
	BuyTime    string  `json:"buy_time"`    // 买入时间
	BuyPrice   float64 `json:"buy_price"`   // 买入价
	SellTime   string  `json:"sell_time"`   // 卖出时间（当前时间）
	SellPrice  float64 `json:"sell_price"`  // 卖出价（当前价）
	ProfitRate float64 `json:"profit_rate"` // 收益率百分比
}

// SellResponse - 卖点响应
type SellResponse struct {
	Type    string     `json:"type"`    // 固定 "sell"
	Count   int        `json:"count"`   // 数量
	Time    string     `json:"time"`    // 刷新时间
	Results []SellItem `json:"results"` // 列表
}

// =========================================================
// 服务核心
// =========================================================

// ScreenService - 选股服务，管理买点历史、卖点判定和 WebSocket 推送
type ScreenService struct {
	mu               sync.RWMutex
	sellLookbackDays int                     // 卖点回看天数
	buyHistory       map[string][]core.Buy   // 按日期(YYYY-MM-DD) 聚合的买点
	lastBuys         *BuyResponse            // 最新买点快照
	lastSells        *SellResponse           // 最新卖点快照
	subscribers      map[*fbr.Websocket]bool // WebSocket 订阅者
}

func newScreenService(sellLookbackDays int) *ScreenService {
	return &ScreenService{
		sellLookbackDays: sellLookbackDays,
		buyHistory:       make(map[string][]core.Buy),
		subscribers:      make(map[*fbr.Websocket]bool),
	}
}

// addSubscriber - 添加订阅者
func (s *ScreenService) addSubscriber(ws *fbr.Websocket) {
	s.mu.Lock()
	s.subscribers[ws] = true
	s.mu.Unlock()
}

// removeSubscriber - 移除订阅者
func (s *ScreenService) removeSubscriber(ws *fbr.Websocket) {
	s.mu.Lock()
	delete(s.subscribers, ws)
	s.mu.Unlock()
}

// snapshot - 获取当前快照供新连接订阅时推送
func (s *ScreenService) snapshot() (*BuyResponse, *SellResponse) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lastBuys, s.lastSells
}

// broadcast - 向所有订阅者广播消息
func (s *ScreenService) broadcast(payload any) {
	data, err := json.Marshal(payload)
	if err != nil {
		logs.Errf("[广播] 序列化失败: %v", err)
		return
	}
	msg := string(data)
	s.mu.RLock()
	defer s.mu.RUnlock()
	for ws := range s.subscribers {
		ws.WriteText(msg)
	}
}

// recentDates - 返回最近 N 个交易日（含今日，若今日为交易日）日期的字符串集合
func (s *ScreenService) recentDates(now time.Time) []string {
	n := s.sellLookbackDays
	if n <= 0 {
		n = 10
	}
	// 倒序遍历，往前推 N+5 天以确保能找到 N 个交易日
	start := now.AddDate(0, 0, -(n + 30))
	end := time.Date(now.Year(), now.Month(), now.Day(), 23, 59, 59, 0, time.Local)

	dates := make([]string, 0, n)
	for t := range common.Manage.Workday.Iter(start, end, true) {
		dates = append(dates, t.Format(time.DateOnly))
		if len(dates) >= n {
			break
		}
	}
	return dates
}

// backfillHistory - 启动时回填最近 N 个交易日的买点
// 注意：不使用实时行情，使用本地历史日线（每日的当天 K 线即为收盘数据）
func (s *ScreenService) backfillHistory() {
	now := time.Now()
	dates := s.recentDates(now)
	if len(dates) == 0 {
		return
	}
	logs.Infof("[回填] 开始回填最近 %d 个交易日的买点\n", len(dates))

	codes := common.GetNoPriceLimitCodes()
	for _, date := range dates {
		// 跳过今日：今日会在首次 doScreenBuys 时计算
		if date == now.Format(time.DateOnly) {
			continue
		}
		day, err := time.ParseInLocation(time.DateOnly, date, time.Local)
		if err != nil {
			logs.Errf("[回填] 解析日期 %s 失败: %v", date, err)
			continue
		}
		at := time.Date(day.Year(), day.Month(), day.Day(), 15, 0, 0, 0, time.Local)

		scr := core.Screen{
			Buyer:        common.MACDBuyer,
			Codes:        codes,
			Goroutines:   10,
			GetDayKlines: common.GetDayKlines,
		}
		buys, err := scr.Run(codes, at)
		if err != nil {
			logs.Errf("[回填] %s 选股失败: %v", date, err)
			continue
		}

		s.mu.Lock()
		s.buyHistory[date] = toCoreBuys(buys)
		s.mu.Unlock()
		logs.Infof("[回填] %s 选出 %d 只\n", date, len(buys))
	}
	logs.Infof("[回填] 完成\n")
}

// toCoreBuys - 转换 *core.Buy -> core.Buy
func toCoreBuys(ls []*core.Buy) []core.Buy {
	out := make([]core.Buy, 0, len(ls))
	for _, b := range ls {
		if b != nil {
			out = append(out, *b)
		}
	}
	return out
}

// doScreenBuys - 执行一次选股，更新今日买点、广播 {type:"buy"}
func (s *ScreenService) doScreenBuys() {
	codes := common.GetNoPriceLimitCodes()
	now := time.Now()

	logs.Infof("[选股] 拉取实时行情，共 %d 只股票\n", len(codes))
	quoteMap, err := getRealtimeQuotes(codes)
	if err != nil {
		logs.Errf("[选股] 拉取行情失败: %v\n", err)
		return
	}

	scr := core.Screen{
		Buyer:        common.MACDBuyer,
		Codes:        codes,
		Goroutines:   10,
		GetDayKlines: makeIntradayGetDayKlines(quoteMap),
	}

	buys, err := scr.Run(codes, now)
	if err != nil {
		logs.Errf("[选股] 执行失败: %v", err)
		return
	}

	// 计算涨幅
	items := make([]BuyItem, 0, len(buys))
	for _, b := range buys {
		riseRate := 0.0
		if b.Price > 0 {
			if quote := quoteMap[b.Code]; quote != nil && quote.K.Last > 0 {
				riseRate = (b.Price.Float64() - quote.K.Last.Float64()) / quote.K.Last.Float64() * 100
			}
		}
		items = append(items, BuyItem{
			Code:  b.Code,
			Time:  b.Time.Format(time.DateTime),
			Price: b.Price.Float64(),
			Rise:  riseRate,
		})
	}

	resp := &BuyResponse{
		Type:    "buy",
		Count:   len(items),
		Time:    now.Format(time.DateTime),
		Results: items,
	}

	// 更新今日买点到历史，并清理超出回看窗口的旧日期
	today := now.Format(time.DateOnly)
	coreBuys := toCoreBuys(buys)
	validDates := make(map[string]struct{}, s.sellLookbackDays)
	for _, d := range s.recentDates(now) {
		validDates[d] = struct{}{}
	}
	s.mu.Lock()
	s.buyHistory[today] = coreBuys
	for d := range s.buyHistory {
		if _, ok := validDates[d]; !ok {
			delete(s.buyHistory, d)
		}
	}
	s.lastBuys = resp
	s.mu.Unlock()

	logs.Infof("[选股] 选出 %d 只股票\n", len(items))

	s.broadcast(resp)

	// 买点更新后，紧接着重算卖点（共享同一份实时行情）
	s.doScreenSells(quoteMap)
}

// doScreenSells - 基于最近 N 天买点重算卖点，更新并广播 {type:"sell"}
// quoteMap 由调用方传入；如果为 nil 则内部重新拉取
func (s *ScreenService) doScreenSells(quoteMap map[string]*protocol.Quote) {
	now := time.Now()

	// 收集最近 N 天的所有买点，不去重，每个买点独立判定卖点
	s.mu.RLock()
	dates := s.recentDates(now)
	var candidates []core.Buy
	for _, date := range dates {
		candidates = append(candidates, s.buyHistory[date]...)
	}
	s.mu.RUnlock()

	if len(candidates) == 0 {
		// 即使为空也广播，让客户端知道当前无卖点
		resp := &SellResponse{Type: "sell", Count: 0, Time: now.Format(time.DateTime), Results: []SellItem{}}
		s.mu.Lock()
		s.lastSells = resp
		s.mu.Unlock()
		s.broadcast(resp)
		return
	}

	// 收集需要拉取行情的代码（去重）
	codeSet := make(map[string]struct{}, len(candidates))
	for _, b := range candidates {
		codeSet[b.Code] = struct{}{}
	}

	// 若 quoteMap 为 nil（独立调用卖点判定），拉取一次行情
	if quoteMap == nil {
		codes := make([]string, 0, len(codeSet))
		for code := range codeSet {
			codes = append(codes, code)
		}
		var err error
		quoteMap, err = getRealtimeQuotes(codes)
		if err != nil {
			logs.Errf("[卖点] 拉取行情失败: %v\n", err)
			return
		}
	}

	getKlines := makeIntradayGetDayKlines(quoteMap)
	start := now.AddDate(0, -4, 0)
	end := time.Date(now.Year(), now.Month(), now.Day(), 15, 1, 0, 0, time.Local)

	sells := make([]SellItem, 0)
	for _, b := range candidates {
		dks, err := getKlines(b.Code, start, end)
		if err != nil || len(dks) == 0 {
			continue
		}
		if !common.MACDSeller.Sell(b.Code, dks, b) {
			continue
		}
		today := dks[len(dks)-1]
		sellPrice := today.Close.Float64()
		profitRate := 0.0
		if b.Price > 0 {
			profitRate = (sellPrice - b.Price.Float64()) / b.Price.Float64() * 100
		}
		sells = append(sells, SellItem{
			Code:       b.Code,
			BuyTime:    b.Time.Format(time.DateTime),
			BuyPrice:   b.Price.Float64(),
			SellTime:   today.Time.Format(time.DateTime),
			SellPrice:  sellPrice,
			ProfitRate: profitRate,
		})
	}

	// 按代码+买入时间排序，便于客户端展示稳定
	sort.Slice(sells, func(i, j int) bool {
		if sells[i].Code != sells[j].Code {
			return sells[i].Code < sells[j].Code
		}
		return sells[i].BuyTime < sells[j].BuyTime
	})

	resp := &SellResponse{
		Type:    "sell",
		Count:   len(sells),
		Time:    now.Format(time.DateTime),
		Results: sells,
	}

	s.mu.Lock()
	s.lastSells = resp
	s.mu.Unlock()

	logs.Infof("[卖点] 检出 %d 只\n", len(sells))
	s.broadcast(resp)
}

// startBackground - 启动后台定时任务
// - 每天 08:00 刷新历史买点（清理旧数据，重新回填）
// - 交易时间内按 interval 周期执行选股
func (s *ScreenService) startBackground(interval time.Duration) {

	// 每日 00:00 或者启动的时候 刷新历史买点
	go s.scheduleDailyBackfill()

	// 启动时立刻跑一次，确保有数据快照
	s.doScreenBuys()

	logs.Infof("[服务] 后台选股任务启动，间隔 %v\n", interval)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for range ticker.C {
		if !common.Manage.Workday.TodayIs() || !isTradingTime() {
			continue
		}
		s.doScreenBuys()
	}
}

// scheduleDailyBackfill - 每个交易日 00:00 刷新历史买点
func (s *ScreenService) scheduleDailyBackfill() {
	logs.Infof("开始读取近N天的买点\n")
	s.backfillHistory()
	for {
		now := time.Now()
		// 计算下一个 00:00
		next := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
		if !next.After(now) {
			next = next.AddDate(0, 0, 1)
		}
		timer := time.NewTimer(next.Sub(now))
		<-timer.C

		// 只在交易日执行
		if !common.Manage.Workday.TodayIs() {
			continue
		}

		logs.Infof("[定时回填] 交易日 08:00，刷新历史买点\n")
		s.backfillHistory()
	}
}

// =========================================================
// 交易时间判断
// =========================================================

// isTradingTime - 判断是否处于交易时间段
// 交易时间：上午 09:30 - 11:30，下午 13:00 - 15:00
func isTradingTime() bool {
	now := time.Now()
	hour, minute := now.Hour(), now.Minute()

	if hour >= 9 && hour <= 11 {
		if hour == 9 && minute >= 30 {
			return true
		}
		if hour == 10 {
			return true
		}
		if hour == 11 && minute <= 30 {
			return true
		}
	}

	return hour >= 13 && hour < 15
}

// =========================================================
// 入口
// =========================================================

func main() {
	port := cfg.GetInt("port", 9090)
	interval := cfg.GetDuration("interval", time.Minute)
	sellLookbackDays := cfg.GetInt("sell_lookback_days", 10)

	svc := newScreenService(sellLookbackDays)

	//实时计算今天的买点
	go svc.startBackground(interval)

	s := fbr.Default(
		fbr.WithPort(port),
		fbr.WithALL("/ws", func(c fbr.Ctx) {
			c.Websocket(func(ws *fbr.Websocket) {
				svc.addSubscriber(ws)
				defer svc.removeSubscriber(ws)

				// 新连接立即推送当前快照
				if buys, sells := svc.snapshot(); buys != nil || sells != nil {
					if buys != nil {
						if data, err := json.Marshal(buys); err == nil {
							ws.WriteText(string(data))
						}
					}
					if sells != nil {
						if data, err := json.Marshal(sells); err == nil {
							ws.WriteText(string(data))
						}
					}
				}

				// 保持连接（阻塞等待）
				ws.DiscardRead()
			})
		}),
	)

	s.Run()
}
