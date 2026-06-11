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
	"os"
	"path/filepath"
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
	Code       string  `json:"code"`        // 股票代码（带前缀）
	Name       string  `json:"name"`        // 股票名称
	Date       string  `json:"date"`        // 日期 YYYY-MM-DD
	Time       string  `json:"time"`        // 信号产生时间
	Price      float64 `json:"price"`       // 买入价
	Rise       float64 `json:"rise"`        // 盘中涨幅百分比（仅当日买点有效）
	CurrPrice  float64 `json:"curr_price"`  // 现价（未卖出时有效）
	IncomeRate float64 `json:"income_rate"` // 收益率百分比
	Sold       bool    `json:"sold"`        // 是否已卖出
	SellPrice  float64 `json:"sell_price"`  // 卖出价（已卖出时有效）
	SellTime   string  `json:"sell_time"`   // 卖出时间（已卖出时有效）
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
	Name       string  `json:"name"`        // 股票名称
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

// HistoryResponse - 历史买点响应（扁平结构，按时间倒序）
type HistoryResponse struct {
	Type    string    `json:"type"`    // 固定 "history"
	Time    string    `json:"time"`    // 刷新时间
	Total   int       `json:"total"`   // 总买点数量
	Results []BuyItem `json:"results"` // 所有历史买点，按时间倒序
}

// =========================================================
// 服务核心
// =========================================================

// ScreenService - 选股服务，管理买点历史、卖点判定和 WebSocket 推送
type ScreenService struct {
	mu               sync.RWMutex
	sellLookbackDays int                     // 卖点回看天数
	buyHistory       map[string][]core.Buy   // 按日期(YYYY-MM-DD) 聚合的买点
	soldBuys         map[string]*SellItem    // 已卖出的买点 key=code|buyTime
	lastBuys         *BuyResponse            // 最新买点快照
	lastSells        *SellResponse           // 最新卖点快照
	lastHistory      *HistoryResponse        // 最新历史买点快照
	subscribers      map[*fbr.Websocket]bool // WebSocket 订阅者
}

func newScreenService(sellLookbackDays int) *ScreenService {
	return &ScreenService{
		sellLookbackDays: sellLookbackDays,
		buyHistory:       make(map[string][]core.Buy),
		soldBuys:         make(map[string]*SellItem),
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
func (s *ScreenService) snapshot() (*BuyResponse, *SellResponse, *HistoryResponse) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lastBuys, s.lastSells, s.lastHistory
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

// backfillHistory - 启动时回填最近 N 个交易日的买点（不含今日）
// 使用本地历史日线（收盘数据），今日买点由 doScreenBuys 用实时行情计算
func (s *ScreenService) backfillHistory() {
	now := time.Now()
	today := now.Format(time.DateOnly)

	// 1. 先从文件加载已有历史
	fileHistory, err := loadHistoryFile()
	if err != nil {
		logs.Warnf("[回填] 加载历史文件失败，将全量计算: %v\n", err)
		fileHistory = map[string][]core.Buy{}
	}

	// 2. 合并到内存
	s.mu.Lock()
	for date, buys := range fileHistory {
		if date != today { // 不覆盖今日盘中数据
			s.buyHistory[date] = buys
		}
	}
	// 加载已卖出记录
	s.soldBuys = loadSoldHistory()
	s.mu.Unlock()

	// 3. 找出缺失的日期（文件中没有的）
	dates := s.recentDates(now)
	var missing []string
	s.mu.RLock()
	for _, date := range dates {
		if date == today {
			continue
		}
		if _, ok := s.buyHistory[date]; !ok {
			missing = append(missing, date)
		}
	}
	s.mu.RUnlock()

	if len(missing) == 0 {
		logs.Infof("[回填] 历史文件完整，无需重新计算\n")
		return
	}

	logs.Infof("[回填] 需补齐 %d 个交易日: %v\n", len(missing), missing)

	codes := common.GetNoPriceLimitCodes()
	for _, date := range missing {
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

		coreBuys := toCoreBuys(buys)
		s.mu.Lock()
		s.buyHistory[date] = coreBuys
		s.mu.Unlock()
		logs.Infof("[回填] %s 选出 %d 只\n", date, len(buys))

		// 每算完一天就持久化一次，避免中断丢失
		s.persistHistory()
	}
	logs.Infof("[回填] 完成\n")
}

// refreshYesterdayBuys - 用收盘数据刷新昨天的买点
// 跨天时调用：昨天盘中用实时行情计算的买点可能与收盘数据有差异，需用收盘数据重算
func (s *ScreenService) refreshYesterdayBuys() {
	now := time.Now()
	dates := s.recentDates(now)
	if len(dates) < 2 {
		return
	}

	today := now.Format(time.DateOnly)
	// 找到最近的非今日交易日（即昨天或上一个交易日）
	var yesterday string
	for _, d := range dates {
		if d != today {
			yesterday = d
			break
		}
	}
	if yesterday == "" {
		return
	}

	day, err := time.ParseInLocation(time.DateOnly, yesterday, time.Local)
	if err != nil {
		logs.Errf("[跨天刷新] 解析日期 %s 失败: %v", yesterday, err)
		return
	}
	at := time.Date(day.Year(), day.Month(), day.Day(), 15, 0, 0, 0, time.Local)

	codes := common.GetNoPriceLimitCodes()
	scr := core.Screen{
		Buyer:        common.MACDBuyer,
		Codes:        codes,
		Goroutines:   10,
		GetDayKlines: common.GetDayKlines,
	}
	buys, err := scr.Run(codes, at)
	if err != nil {
		logs.Errf("[跨天刷新] %s 选股失败: %v", yesterday, err)
		return
	}

	s.mu.Lock()
	s.buyHistory[yesterday] = toCoreBuys(buys)
	s.mu.Unlock()
	logs.Infof("[跨天刷新] %s 用收盘数据刷新，选出 %d 只\n", yesterday, len(buys))
	s.persistHistory()
}

// =========================================================
// 买点历史持久化
// =========================================================

// historyFilePath - 买点历史 JSON 文件路径
const historyFilePath = "./data/buy_history.json"

// soldFilePath - 已卖出记录 JSON 文件路径
const soldFilePath = "./data/sold_history.json"

// persistedBuy - 持久化用的买点结构（避免依赖 protocol.Price 序列化细节）
type persistedBuy struct {
	Code  string    `json:"code"`
	Time  time.Time `json:"time"`
	Price float64   `json:"price"`
}

// loadHistoryFile - 从文件加载历史买点，返回 {date: [buys]}
func loadHistoryFile() (map[string][]core.Buy, error) {
	data, err := os.ReadFile(historyFilePath)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string][]core.Buy{}, nil
		}
		return nil, err
	}
	raw := map[string][]persistedBuy{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	out := make(map[string][]core.Buy, len(raw))
	for date, ls := range raw {
		buys := make([]core.Buy, 0, len(ls))
		for _, p := range ls {
			buys = append(buys, core.Buy{
				Code:  p.Code,
				Time:  p.Time,
				Price: protocol.Yuan(p.Price),
			})
		}
		out[date] = buys
	}
	return out, nil
}

// saveHistoryFile - 将 {date: [buys]} 持久化到文件
func saveHistoryFile(buyHistory map[string][]core.Buy) error {
	raw := make(map[string][]persistedBuy, len(buyHistory))
	for date, buys := range buyHistory {
		ls := make([]persistedBuy, 0, len(buys))
		for _, b := range buys {
			ls = append(ls, persistedBuy{
				Code:  b.Code,
				Time:  b.Time,
				Price: b.Price.Float64(),
			})
		}
		raw[date] = ls
	}
	data, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(historyFilePath), 0755); err != nil {
		return err
	}
	// 原子写：先写临时文件再重命名
	tmp := historyFilePath + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, historyFilePath)
}

// persistHistory - 持久化当前 buyHistory（含今日），失败仅打印日志
func (s *ScreenService) persistHistory() {
	s.mu.RLock()
	snapshot := make(map[string][]core.Buy, len(s.buyHistory))
	for k, v := range s.buyHistory {
		snapshot[k] = v
	}
	s.mu.RUnlock()
	if err := saveHistoryFile(snapshot); err != nil {
		logs.Errf("[持久化] 写入失败: %v\n", err)
	}
}

// loadSoldHistory - 从文件加载已卖出记录
func loadSoldHistory() map[string]*SellItem {
	data, err := os.ReadFile(soldFilePath)
	if err != nil {
		return make(map[string]*SellItem)
	}
	var list []SellItem
	if err := json.Unmarshal(data, &list); err != nil {
		logs.Errf("[持久化] 解析卖出记录失败: %v\n", err)
		return make(map[string]*SellItem)
	}
	m := make(map[string]*SellItem, len(list))
	for i := range list {
		m[list[i].Code+"|"+list[i].BuyTime] = &list[i]
	}
	return m
}

// persistSoldHistory - 持久化已卖出记录
func (s *ScreenService) persistSoldHistory() {
	s.mu.RLock()
	list := make([]SellItem, 0, len(s.soldBuys))
	for _, v := range s.soldBuys {
		list = append(list, *v)
	}
	s.mu.RUnlock()

	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		logs.Errf("[持久化] 序列化卖出记录失败: %v\n", err)
		return
	}
	if err := os.MkdirAll(filepath.Dir(soldFilePath), 0755); err != nil {
		logs.Errf("[持久化] 创建目录失败: %v\n", err)
		return
	}
	tmp := soldFilePath + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		logs.Errf("[持久化] 写入卖出记录失败: %v\n", err)
		return
	}
	if err := os.Rename(tmp, soldFilePath); err != nil {
		logs.Errf("[持久化] 重命名卖出记录失败: %v\n", err)
	}
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

// buyToItem - 将 core.Buy 转为 BuyItem，可选传入实时行情计算现价和收益率
func buyToItem(b core.Buy, quoteMap map[string]*protocol.Quote) BuyItem {
	item := BuyItem{
		Code:  b.Code,
		Name:  common.Manage.Codes.GetName(b.Code),
		Date:  b.Time.Format(time.DateOnly),
		Time:  b.Time.Format(time.DateTime),
		Price: b.Price.Float64(),
		Rise:  0,
	}
	if quoteMap != nil {
		if q, ok := quoteMap[b.Code]; ok && q.K.Close > 0 {
			item.CurrPrice = q.K.Close.Float64()
			if item.Price > 0 {
				item.IncomeRate = (item.CurrPrice - item.Price) / item.Price * 100
			}
		}
	}
	return item
}

// doScreenHistory - 汇总历史买点（不含今日）并广播 {type:"history"}
func (s *ScreenService) doScreenHistory() {
	now := time.Now()
	today := now.Format(time.DateOnly)

	s.mu.RLock()
	dates := s.recentDates(now)
	all := make([]BuyItem, 0)
	codeSet := make(map[string]struct{})
	for _, date := range dates {
		if date == today {
			continue
		}
		for _, b := range s.buyHistory[date] {
			codeSet[b.Code] = struct{}{}
			all = append(all, buyToItem(b, nil))
		}
	}

	// 用持久化的已卖出记录匹配
	soldMap := make(map[string]*SellItem, len(s.soldBuys))
	for k, v := range s.soldBuys {
		soldMap[k] = v
	}
	s.mu.RUnlock()

	// 拉取实时行情，补全现价和收益率（仅未卖出的需要）
	if len(codeSet) > 0 {
		codes := make([]string, 0, len(codeSet))
		for code := range codeSet {
			codes = append(codes, code)
		}
		quoteMap, err := getRealtimeQuotes(codes)
		if err != nil {
			logs.Errf("[历史] 拉取实时行情失败: %v\n", err)
		} else {
			for i := range all {
				b := all[i]
				// 已卖出的用卖出价计算收益率
				if si, ok := soldMap[b.Code+"|"+b.Time]; ok {
					all[i].Sold = true
					all[i].SellPrice = si.SellPrice
					all[i].SellTime = si.SellTime
					if all[i].Price > 0 {
						all[i].IncomeRate = (si.SellPrice - all[i].Price) / all[i].Price * 100
					}
					continue
				}
				// 未卖出的用现价计算浮动收益率
				if q, ok := quoteMap[b.Code]; ok && q.K.Close > 0 {
					all[i].CurrPrice = q.K.Close.Float64()
					if all[i].Price > 0 {
						all[i].IncomeRate = (all[i].CurrPrice - all[i].Price) / all[i].Price * 100
					}
				}
			}
		}
	}

	// 按时间倒序（最新在前）
	sort.Slice(all, func(i, j int) bool {
		return all[i].Time > all[j].Time
	})

	resp := &HistoryResponse{
		Type:    "history",
		Time:    now.Format(time.DateTime),
		Total:   len(all),
		Results: all,
	}

	s.mu.Lock()
	s.lastHistory = resp
	s.mu.Unlock()

	s.broadcast(resp)
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
			Name:  common.Manage.Codes.GetName(b.Code),
			Date:  b.Time.Format(time.DateOnly),
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
			Name:       common.Manage.Codes.GetName(b.Code),
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
	// 追加到已卖出记录（不会覆盖已有的，保留首次卖出信息）
	newSold := false
	for i := range sells {
		key := sells[i].Code + "|" + sells[i].BuyTime
		if _, exists := s.soldBuys[key]; !exists {
			s.soldBuys[key] = &sells[i]
			newSold = true
		}
	}
	s.mu.Unlock()

	// 有新卖出记录时持久化
	if newSold {
		s.persistSoldHistory()
	}

	logs.Infof("[卖点] 检出 %d 只\n", len(sells))
	s.broadcast(resp)
}

// startBackground - 启动后台定时任务
// 1. 立即执行一次选股（确保快速出数据）
// 2. 异步回填历史买点（不阻塞选股，回填完成后自动重算卖点）
// 3. 每个交易日 00:00 用收盘数据刷新昨天的买点（跨天处理）
// 4. 交易时间内按 interval 周期执行选股
func (s *ScreenService) startBackground(interval time.Duration) {
	// 立即选股，确保客户端尽快拿到数据
	s.doScreenBuys()

	// 异步回填历史买点，回填完成后重算卖点
	go func() {
		s.backfillHistory()
		// 回填完成后推送历史买点和卖点（此时买点历史已完整）
		s.doScreenHistory()
		s.doScreenSells(nil)
	}()

	// 启动跨天刷新协程
	go s.scheduleDailyRefresh()

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

// scheduleDailyRefresh - 每个交易日 00:00 用收盘数据刷新上一交易日的买点
// 解决跨天问题：盘中用实时行情计算的买点可能与收盘数据有差异
func (s *ScreenService) scheduleDailyRefresh() {
	for {
		now := time.Now()
		// 计算下一个 00:00
		next := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
		if !next.After(now) {
			next = next.AddDate(0, 0, 1)
		}
		timer := time.NewTimer(next.Sub(now))
		<-timer.C

		// 只在交易日执行刷新
		if !common.Manage.Workday.TodayIs() {
			continue
		}

		logs.Infof("[定时刷新] 交易日 00:00，用收盘数据刷新上一交易日买点\n")
		s.refreshYesterdayBuys()
		// 跨天后同步推送最新历史买点
		s.doScreenHistory()
	}
}

// =========================================================
// 交易时间判断
// =========================================================

// isTradingTime - 判断是否处于交易时间段
// 交易时间：上午 09:30 - 11:30，下午 13:00 - 15:00
func isTradingTime() bool {
	now := time.Now()
	h, m := now.Hour(), now.Minute()

	// 上午 09:30 - 11:30
	if h == 9 && m >= 30 {
		return true
	}
	if h == 10 {
		return true
	}
	if h == 11 && m <= 30 {
		return true
	}

	// 下午 13:00 - 15:00
	if h == 13 || h == 14 {
		return true
	}

	return false
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
				buys, sells, history := svc.snapshot()
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
				if history != nil {
					if data, err := json.Marshal(history); err == nil {
						ws.WriteText(string(data))
					}
				}

				// 保持连接（阻塞等待）
				ws.DiscardRead()
			})
		}),
	)

	s.Run()
}
