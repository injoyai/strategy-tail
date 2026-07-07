package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	_ "github.com/glebarez/go-sqlite"
	"github.com/injoyai/bar"
	"github.com/injoyai/frame/fbr" // Web框架
	"github.com/injoyai/logs"      // 日志库
	"github.com/injoyai/strategy-tail"
	"github.com/injoyai/strategy-tail/core"       // 选股核心模块
	"github.com/injoyai/strategy-tail/lib/extend" // 通达信扩展功能
	"github.com/injoyai/strategy-tail/strategies/buy"
	"github.com/injoyai/tdx" // 通达信SDK
	"github.com/injoyai/tdx/lib/xorms"
	"github.com/injoyai/tdx/protocol"
	"xorm.io/xorm"
)

// =========================================================
// 服务核心
// =========================================================

// ScreenService - 选股服务，管理买点历史、卖点判定和 WebSocket 推送
type ScreenService struct {
	DB           *xorms.Engine //数据库引擎
	LookbackDays int           //历史交割单天数
	Interval     time.Duration //间隔时间
	Goroutines   int           //协程数量
	Codes        []string      //计算的代码
	Strategies   []Strategy    //可切换策略列表

	mu                sync.RWMutex
	historyDayKlines  map[string]extend.Klines //历史数据缓存
	realtimeDayKlines map[string]extend.Klines //实时数据缓存

	lastBuys    []*core.Buy  // 最新买点快照
	lastSells   []*core.Sell // 最新卖点快照
	lastTrades  []*Trade     // 最新交易快照
	subscribers map[*fbr.Websocket]bool
}

// update 更新实时数据
func (this *ScreenService) updateRealtime() error {
	realKlines, err := this.getRealtimeKlines()
	if err != nil {
		return err
	}
	realtimeKlinesMap := map[string]extend.Klines{}
	for _, code := range this.Codes {
		ks := extend.Klines{}
		for _, v := range this.historyDayKlines[code] {
			ks = append(ks, v)
		}

		if len(ks) < 1 {
			continue
		}

		last := ks[len(ks)-1]

		realKline := realKlines[code]
		if realKline != nil {
			switch {
			case realKline.Time.Format(time.DateOnly) > last.Time.Format(time.DateOnly):
				ks = append(ks, &extend.Kline{
					Unix:       realKline.Time.Unix(),
					Kline:      realKline,
					FloatStock: last.FloatStock,
					TotalStock: last.TotalStock,
				})
			case realKline.Time.Format(time.DateOnly) == last.Time.Format(time.DateOnly):
				ks[len(ks)-1].Kline = realKline
			}
		}
		realtimeKlinesMap[code] = ks
	}

	this.mu.Lock()
	this.realtimeDayKlines = realtimeKlinesMap
	this.mu.Unlock()

	return nil
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
func (s *ScreenService) snapshot() ([]*core.Buy, []*core.Sell, []*Trade) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lastBuys, s.lastSells, s.lastTrades
}

// broadcast - 向所有订阅者广播消息，自动转换为前端期望的格式
func (s *ScreenService) broadcast(payload any) {
	msg := s.marshal(payload)
	if msg == "" {
		return
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for ws := range s.subscribers {
		ws.WriteText(msg)
	}
}

// sendTo - 向单个连接推送消息，自动转换为前端期望的格式
func (s *ScreenService) sendTo(ws *fbr.Websocket, payload any) {
	msg := s.marshal(payload)
	if msg == "" {
		return
	}
	ws.WriteText(msg)
}

// marshal - 将内部数据转换为前端期望的JSON格式(WS推送用)
func (s *ScreenService) marshal(payload any) string {
	now := time.Now().Format(time.DateTime)

	var resp any
	switch v := payload.(type) {
	case []*core.Buy:
		resp = s.buildBuyResponse(v, now)

	case []*core.Sell:
		resp = s.buildSellResponse(v, now)

	case []*Trade:
		// history 不再通过 WS 推送，但保留兼容
		resp = s.buildHistoryResponse(v, now)

	default:
		resp = payload
	}

	data, err := json.Marshal(resp)
	if err != nil {
		logs.Errf("[广播] 序列化失败: %v", err)
		return ""
	}
	return string(data)
}

// buildBuyResponse 构建买点响应
func (s *ScreenService) buildBuyResponse(v []*core.Buy, now string) BuyResponse {
	items := make([]BuyItem, len(v))
	for i, b := range v {
		s.mu.RLock()
		ks := s.realtimeDayKlines[b.Code]
		s.mu.RUnlock()
		var rise float64
		if len(ks) >= 2 && ks[len(ks)-2] != nil && ks[len(ks)-2].Close > 0 {
			rise = (b.Price.Float64() - ks[len(ks)-2].Close.Float64()) / ks[len(ks)-2].Close.Float64() * 100
		}
		matched := s.evalStrategies(b.Code, ks)
		items[i] = BuyItem{
			Code:       b.Code,
			Name:       common.Manage.Codes.GetName(b.Code),
			Time:       b.Time.Format(time.DateTime),
			Price:      b.Price.Float64(),
			Rise:       rise,
			Strategies: matched,
			Tags:       s.evalTags(b.Code, ks, matched),
		}
	}
	return BuyResponse{Type: "buy", Count: len(items), Time: now, Results: items}
}

// buildSellResponse 构建卖点响应
func (s *ScreenService) buildSellResponse(v []*core.Sell, now string) SellResponse {
	items := make([]Trade, len(v))
	for i, se := range v {
		s.mu.RLock()
		for _, t := range s.lastTrades {
			if t.Code == se.Code {
				items[i] = *t
				break
			}
		}
		s.mu.RUnlock()
	}
	return SellResponse{Type: "sell", Count: len(items), Time: now, Results: items}
}

// buildHistoryResponse 构建历史买卖点响应
func (s *ScreenService) buildHistoryResponse(v []*Trade, now string) HistoryResponse {
	items := make([]BuyItem, len(v))
	for i, t := range v {
		item := BuyItem{
			Code:       t.Code,
			Name:       t.Name,
			Date:       t.BuyTime[:10],
			Time:       t.BuyTime,
			Price:      t.BuyPrice,
			Sold:       t.Sold,
			SellPrice:  t.SellPrice,
			SellTime:   t.SellTime,
			IncomeRate: t.ProfitRate,
			Strategies: t.Strategies,
			Tags:       t.Tags,
		}
		if t.Sold {
			item.CurrPrice = t.SellPrice
		} else {
			s.mu.RLock()
			ks := s.realtimeDayKlines[t.Code]
			s.mu.RUnlock()
			if len(ks) > 0 && ks[len(ks)-1] != nil {
				item.CurrPrice = ks[len(ks)-1].Close.Float64()
				if t.BuyPrice > 0 {
					item.IncomeRate = (item.CurrPrice - t.BuyPrice) / t.BuyPrice * 100
				}
			}
		}
		items[i] = item
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].Time > items[j].Time
	})
	return HistoryResponse{Type: "history", Time: now, Total: len(items), Results: items}
}

func (this *ScreenService) Run() {

	first := true

	for range time.NewTicker(this.Interval).C {

		now := time.Now()

		//判断是否是交易日和交易时间
		if first || (common.Manage.Workday.TodayIs() && common.IsTradingTime()) {

			//更新实时数据
			err := this.updateRealtime()
			logs.PrintErr(err)

			//读取历史未卖出交易数据
			trades := []*Trade(nil)
			if err := this.DB.Find(&trades); err != nil {
				logs.Err(err)
			}

			//缓存历史交易数据(供HTTP读取)
			this.mu.Lock()
			this.lastTrades = trades
			this.mu.Unlock()

			//计算实时卖点
			sells := []*core.Sell(nil)
			for _, t := range trades {
				if t.Sold {
					if strings.HasPrefix(t.SellTime, now.Format(time.DateOnly)) {
						sell, err := t.ToSell()
						if err != nil {
							logs.Err(err)
							continue
						}
						sells = append(sells, sell)
					}
					continue
				}
				b, err := t.ToBuy()
				if err != nil {
					logs.Err(err)
					continue
				}
				this.mu.RLock()
				ks := this.realtimeDayKlines[t.Code]
				this.mu.RUnlock()
				//实时计算卖点(按买入时命中的策略选择卖出条件)
				seller := this.sellerFor(t.Strategies)
				if seller != nil {
					if s := core.GetSell(seller, ks, b, nil); s != nil {
						sells = append(sells, s)
						//更新到数据库
						_, err := this.DB.Where("ID=?", t.ID).Cols("Sold,SellTime,SellPrice,ProfitRate").Update(t.Sell(s))
						logs.PrintErr(err)
					}
				}
			}

			//缓存实时卖点数据
			this.mu.Lock()
			this.lastSells = sells
			this.mu.Unlock()
			//推送实时卖点数据
			this.broadcast(sells)

			//开始计算实时买点
			buys := this.realtimeBuys()
			//处理买点,推送到前端
			this.mu.Lock()
			this.lastBuys = buys
			this.mu.Unlock()
			this.broadcast(buys)

			first = false
		}
	}
}

// realtimeBuys 实时计算的买点
func (this *ScreenService) realtimeBuys() []*core.Buy {
	bs := []*core.Buy(nil)
	buyer := this.combinedBuyer()
	for _, code := range this.Codes {
		this.mu.RLock()
		ks := this.realtimeDayKlines[code]
		this.mu.RUnlock()
		if buyer.Buy(code, ks) {
			k := ks[len(ks)-1]
			bs = append(bs, &core.Buy{
				Code:  code,
				Time:  k.Time,
				Price: k.Close,
			})
		}
	}
	return bs
}

// combinedBuyer 联合所有策略的买入条件(Or),用于扫描
func (this *ScreenService) combinedBuyer() core.Buyer {
	union := make([]core.Buyer, 0, len(this.Strategies))
	for _, st := range this.Strategies {
		if st.Buyer != nil {
			union = append(union, buy.Strategy(st.Name, st.Buyer))
		}
	}
	return buy.Strategy("全部", buy.Or(union))
}

// sellerFor 根据命中的策略 key 找到对应的卖出策略(取第一个命中的)
func (this *ScreenService) sellerFor(keys []string) core.Seller {
	for _, key := range keys {
		for _, st := range this.Strategies {
			if st.Key == key && st.Seller != nil {
				return st.Seller
			}
		}
	}
	return nil
}

// evalStrategies 用 Strategies 中的策略对 ks 进行判断,返回命中的 key 列表
func (this *ScreenService) evalStrategies(code string, ks extend.Klines) []string {
	if len(this.Strategies) == 0 {
		return nil
	}
	keys := []string(nil)
	for _, st := range this.Strategies {
		if st.Buyer == nil {
			continue
		}
		if st.Buyer.Buy(code, ks) {
			keys = append(keys, st.Key)
		}
	}
	return keys
}

// findStrategy 按 key 查找策略
func (this *ScreenService) findStrategy(key string) (Strategy, bool) {
	for _, st := range this.Strategies {
		if st.Key == key {
			return st, true
		}
	}
	return Strategy{}, false
}

// Diagnose 诊断指定股票在指定策略下的匹配情况
func (s *ScreenService) Diagnose(code, strategyKey string) (*DiagnoseResponse, error) {
	code = protocol.AddPrefix(code)

	//选择策略
	var buyer core.Buyer
	var fixedSeller core.Seller //特定策略时直接使用; 全部策略时为 nil,按买点动态匹配
	var strategyName string
	if strategyKey == "" || strategyKey == "all" {
		buyer = s.combinedBuyer()
		strategyName = "全部策略"
	} else {
		st, ok := s.findStrategy(strategyKey)
		if !ok {
			return nil, fmt.Errorf("策略不存在: %s", strategyKey)
		}
		buyer = st.Buyer
		fixedSeller = st.Seller
		strategyName = st.Name
	}

	//优先用缓存K线,回退拉取
	s.mu.RLock()
	ks := s.realtimeDayKlines[code]
	s.mu.RUnlock()
	if len(ks) == 0 {
		var err error
		ks, err = common.Pull.DayKlines(code, time.Now().AddDate(-1, 0, 0), time.Now())
		if err != nil {
			return nil, fmt.Errorf("拉取数据失败: %v", err)
		}
	}
	if len(ks) == 0 {
		return nil, fmt.Errorf("无K线数据: %s", code)
	}

	//判断是否命中
	matched := buyer.Buy(code, ks)

	//获取标注点
	var anns []core.Annotation
	if v, ok := buyer.(core.Visualizer); ok {
		anns = v.Annotate(code, ks)
	}

	//获取逐步判定原因
	var explain []core.ExplainStep
	if e, ok := buyer.(core.Explainer); ok {
		explain = e.Explain(code, ks)
	}

	//获取诊断树
	_, diagnosis := core.Diagnose(buyer, code, ks)

	//计算历史买卖点,用于在K线图上标注
	buyPoints := core.GetBuys(buyer, code, ks, len(ks))
	type sellPoint struct {
		BuyIdx  int
		SellIdx int
	}
	sellPoints := []sellPoint(nil)
	for i, b := range buyPoints {
		var sel *core.Sell
		if fixedSeller != nil {
			//特定策略: 使用该策略的卖出条件
			sel = core.GetSell(fixedSeller, ks, *b, nil)
		} else {
			//全部策略: 按买点命中的策略动态选择卖出条件
			buyIdx := -1
			for j := range ks {
				if ks[j].Time.Equal(b.Time) {
					buyIdx = j
					break
				}
			}
			if buyIdx >= 0 {
				if seller := s.sellerFor(s.evalStrategies(b.Code, ks[:buyIdx+1])); seller != nil {
					sel = core.GetSell(seller, ks, *b, nil)
				}
			}
		}
		if sel != nil {
			//找到卖出K线在 ks 中的索引
			for j := range ks {
				if ks[j].Time.Equal(sel.Time) {
					sellPoints = append(sellPoints, sellPoint{BuyIdx: i, SellIdx: j})
					break
				}
			}
		}
	}

	//组装K线数据
	klines := make([]ChartKline, 0, len(ks))
	for _, k := range ks {
		klines = append(klines, ChartKline{
			Time:   k.Time.Format("2006-01-02"),
			Open:   k.Open.Float64(),
			High:   k.High.Float64(),
			Low:    k.Low.Float64(),
			Close:  k.Close.Float64(),
			Volume: k.Volume,
		})
	}

	//将买卖点追加到标注中
	for i, b := range buyPoints {
		anns = append(anns, core.Annotation{
			Time:  b.Time,
			Price: b.Price.Float64(),
			Label: "买",
			Color: "#ef4444", // A股: 红色买入
			Note:  fmt.Sprintf("买入 %.2f @ %s", b.Price.Float64(), b.Time.Format("2006-01-02")),
		})
		_ = i
	}
	for _, sp := range sellPoints {
		k := ks[sp.SellIdx]
		anns = append(anns, core.Annotation{
			Time:  k.Time,
			Price: k.Close.Float64(),
			Label: "卖",
			Color: "#22c55e", // A股: 绿色卖出
			Note:  fmt.Sprintf("卖出 %.2f @ %s", k.Close.Float64(), k.Time.Format("2006-01-02")),
		})
	}

	return &DiagnoseResponse{
		Code:        code,
		Name:        common.Manage.Codes.GetName(code),
		Strategy:    strategyName,
		Matched:     matched,
		Klines:      klines,
		Annotations: anns,
		Explain:     explain,
		Diagnosis:   diagnosis,
	}, nil
}

// evalTags 只评估命中策略的 Tags,返回命中的标签(去重)
func (this *ScreenService) evalTags(code string, ks extend.Klines, matchedKeys []string) []string {
	seen := map[string]bool{}
	tags := []string(nil)
	for _, st := range this.Strategies {
		if !containsKey(matchedKeys, st.Key) {
			continue
		}
		for name, b := range st.Tags {
			if b == nil || seen[name] {
				continue
			}
			if b.Buy(code, ks) {
				seen[name] = true
				tags = append(tags, name)
			}
		}
	}
	return tags
}

func containsKey(keys []string, key string) bool {
	for _, k := range keys {
		if k == key {
			return true
		}
	}
	return false
}

func (this *ScreenService) Init() error {

	if this.subscribers == nil {
		this.subscribers = map[*fbr.Websocket]bool{}
	}
	if this.LookbackDays <= 0 {
		this.LookbackDays = 10
	}
	if this.Goroutines < 1 {
		this.Goroutines = common.DefaultGoroutines
	}
	if len(this.Codes) == 0 {
		this.Codes = common.GetAllCodes()
	}

	if this.historyDayKlines == nil {
		this.historyDayKlines = map[string]extend.Klines{}
	}
	if this.realtimeDayKlines == nil {
		this.realtimeDayKlines = map[string]extend.Klines{}
	}

	//===================================================//

	this.DB.Sync2(new(Trade))

	//加载历史数据到缓存
	b := bar.NewCoroutine(
		len(this.Codes),
		this.Goroutines,
		bar.WithPrefix("[加载日线][xx000000]"),
	)
	defer b.Close()
	for i := range this.Codes {
		code := this.Codes[i]
		b.Go(func() {
			b.SetPrefix(fmt.Sprintf("[加载日线][%s]", code))
			b.Flush()
			ks, err := common.Pull.DayKlines(code, time.Now().AddDate(-1, -6, 0), time.Now())
			if err != nil {
				b.Logf("[错误][%s] %v", code, err)
				b.Flush()
				return
			}
			if len(ks) > 300 {
				ks = append(extend.Klines{}, ks[len(ks)-300:]...)
			}
			this.mu.Lock()
			defer this.mu.Unlock()
			this.historyDayKlines[code] = ks
		})
	}
	b.Wait()

	//更新历史买卖点,判断数据库是否有历史买卖点数据
	update, err := tdx.NewUpdated(this.DB, 0, 1)
	if err != nil {
		return err
	}
	//升级 update key,强制重算历史交易(策略结构变更后 key 变化)
	updated, err := update.Updated("history-trade-v3")
	if err != nil {
		return err
	}
	if !updated {
		//更新历史交易数据
		if err := this.updateHistoryTrade(); err != nil {
			return err
		}
		if err = update.Update("history-trade-v3"); err != nil {
			return err
		}
	}

	return nil
}

func (this *ScreenService) updateHistoryTrade() error {
	ts := this.getHistoryTrade()
	return this.DB.SessionFunc(func(session *xorm.Session) error {
		if _, err := session.Where("ID>0").Delete(new(Trade)); err != nil {
			return err
		}
		for _, t := range ts {
			if _, err := session.Insert(t); err != nil {
				return err
			}
		}
		return nil
	})
}

// getHistoryBuys 计算历史买点
func (this *ScreenService) getHistoryTrade() []*Trade {
	b := bar.NewCoroutine(
		len(this.Codes),
		this.Goroutines,
		bar.WithPrefix("[历史成交][xx000000]"),
	)
	defer b.Close()

	mu := sync.Mutex{}
	ts := []*Trade(nil)
	for i := range this.Codes {
		code := this.Codes[i]

		this.mu.RLock()
		ks := this.historyDayKlines[code]
		this.mu.RUnlock()
		b.Go(func() {

			b.SetPrefix(fmt.Sprintf("[历史成交][%s]", code))
			b.Flush()

			bs := core.GetBuys(this.combinedBuyer(), code, ks, this.LookbackDays)
			if len(bs) == 0 {
				return
			}

			//获取历史分钟数据
			var mks protocol.Klines
			err := common.Manage.Do(func(c *tdx.Client) error {
				resp, err := c.GetKlineMinuteUntil(code, func(k *protocol.Kline) bool {
					return k.Time.Before(time.Now().AddDate(0, 0, -this.LookbackDays*2))
				})
				if err != nil {
					return err
				}
				mks = resp.List
				return nil
			})
			if err != nil {
				b.Logf("[错误][%s] %v", code, err)
				b.Flush()
				return
			}

			mmks := map[string]protocol.Klines{}
			for _, v := range mks {
				key := v.Time.Format(time.DateOnly)
				mmks[key] = append(mmks[key], v)
			}

			for _, b := range bs {
				//用截止买入当日的 K 线切片评估 Tags 和 Strategies,避免未来函数
				hisKs := extend.Klines{}
				for _, k := range ks {
					hisKs = append(hisKs, k)
					if k.Time.Equal(b.Time) {
						break
					}
				}
				matched := this.evalStrategies(code, hisKs)
				t := &Trade{
					Code:       code,
					Name:       common.Manage.Codes.GetName(code),
					BuyTime:    b.Time.Format(time.DateTime),
					BuyPrice:   b.Price.Float64(),
					Strategies: matched,
					Tags:       this.evalTags(code, hisKs, matched),
				}
				//按命中的策略选择卖出条件
				var s *core.Sell
				if seller := this.sellerFor(matched); seller != nil {
					s = core.GetSell(seller, ks, *b, mmks)
				}
				mu.Lock()
				ts = append(ts, t.Sell(s))
				mu.Unlock()
			}

		})
	}

	b.Wait()
	return ts
}

// getRealtimeQuotes - 批量获取实时行情
// 通达信 API 每次最多支持 80 个代码，需分批拉取
func (this *ScreenService) getRealtimeKlines() (map[string]*protocol.Kline, error) {
	codes := this.Codes

	quoteKline := make(map[string]*protocol.Kline, len(codes))
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
				quoteKline[protocol.AddPrefix(q.Code)] = q.Kline
			}
			return nil
		}); err != nil {
			logs.Errf("[行情] 批量获取失败(%d-%d): %v", i, end, err)
		}
	}

	return quoteKline, nil
}
