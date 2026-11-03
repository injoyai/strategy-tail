package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/injoyai/tdx/v2/lib/update"
	"github.com/robfig/cron/v3"
	"sort"
	"strings"
	"sync"
	"time"

	_ "github.com/glebarez/go-sqlite"
	"github.com/injoyai/bar"
	"github.com/injoyai/frame/fbr" // Web框架
	"github.com/injoyai/goutil/times"
	"github.com/injoyai/logs" // 日志库
	common "github.com/injoyai/strategy-tail"
	"github.com/injoyai/strategy-tail/core"       // 选股核心模块
	"github.com/injoyai/strategy-tail/lib/extend" // 通达信扩展功能
	"github.com/injoyai/strategy-tail/strategies/buy"
	"github.com/injoyai/tdx/v2" // 通达信SDK
	"github.com/injoyai/tdx/v2/lib/xorms"
	"github.com/injoyai/tdx/v2/protocol"

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
	historyDayKlines  map[string]extend.Klines   //历史日线数据缓存
	realtimeDayKlines map[string]extend.Klines   //实时数据缓存,只在今天是交易日时更新
	lastPrices        map[string]*protocol.Kline //最新价格,非交易日也获取,用于填充历史买点的现价
	subscribers       map[*fbr.Websocket]bool

	update *update.Tracker //

	lastBuys   []*Trade // 最新买点快照(含策略/标签)
	lastSells  []*Trade // 最新卖点快照(交易列表)
	lastTrades []*Trade // 最新交易快照

}

// update 更新实时数据
func (this *ScreenService) updateRealtime(first bool) error {
	realKlines, err := this.getRealtimeKlines()
	if err != nil {
		return err
	}

	this.mu.Lock()
	this.lastPrices = realKlines
	this.mu.Unlock()

	if !common.IsTradingTime() && !first {
		return nil
	}

	realtimeKlinesMap := map[string]extend.Klines{}
	// historyDayKlines 由写侧在锁内原地更新,读侧必须持读锁遍历,避免并发读写 map 竞态。
	this.mu.RLock()
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
	this.mu.RUnlock()

	this.mu.Lock()
	this.realtimeDayKlines = realtimeKlinesMap
	this.mu.Unlock()

	return nil
}

// snapshot - 获取当前快照供新连接订阅时推送
func (s *ScreenService) snapshot() ([]*Trade, []*Trade, []*Trade) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lastBuys, s.lastSells, s.lastTrades
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

// broadcast - 向所有订阅者广播消息，自动转换为前端期望的格式
func (s *ScreenService) broadcast(_type string, payload any) {
	msg := s.marshal(_type, payload)
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
func (s *ScreenService) sendTo(ws *fbr.Websocket, _type string, payload any) {
	msg := s.marshal(_type, payload)
	if msg == "" {
		return
	}
	ws.WriteText(msg)
}

// marshal - 将内部数据转换为前端期望的JSON格式(WS推送用)
func (s *ScreenService) marshal(_type string, payload any) string {

	var resp any
	switch _type {
	case TypeBuy, TypeSell, TypeHistory:
		ts, ok := payload.([]*Trade)
		if ok {
			resp = s.response(_type, ts)
		}

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

// buildHistoryResponse 构建历史买卖点响应
func (s *ScreenService) response(_type string, ts []*Trade) Response {
	ts = cloneTrades(ts)
	s.mu.RLock()
	for i := range ts {
		k := s.lastPrices[ts[i].Code]
		ts[i].Realtime(k)
	}
	s.mu.RUnlock()
	sort.Slice(ts, func(i, j int) bool {
		return ts[i].BuyTime > ts[j].BuyTime
	})
	return Response{
		Type:    _type,
		Count:   len(ts),
		Results: ts,
	}
}

func cloneTrades(ts []*Trade) []*Trade {
	if ts == nil {
		return nil
	}
	cloned := make([]*Trade, len(ts))
	for i, trade := range ts {
		copy := *trade
		copy.Tags = append([]string(nil), trade.Tags...)
		cloned[i] = &copy
	}
	return cloned
}

func (this *ScreenService) init() error {

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

	if this.update == nil {
		// 复用 this.DB,避免同一 trade.db 被两个独立 engine 同时打开并发访问(各自连接池可能互相锁库)。
		update, err := update.New(this.DB, 15, 31)
		if err != nil {
			return err
		}
		this.update = update
	}

	//===================================================//

	return this.DB.Sync2(new(Trade))
}

func (this *ScreenService) _update() {
	//更新最新数据到本地数据库
	if err := common.Update(); err != nil {
		logs.Err(err)
		return
	}
	//加载本地日线数据到缓存
	if err := this.getHistoryDayKlines(); err != nil {
		logs.Err(err)
		return
	}
	if update, err := this.update.IsUpdated("history"); err != nil || !update {
		if err := this.updateHistoryTrade(); err != nil {
			logs.Err(err)
			return
		}
		if _, err := this.loadingTrades(); err != nil {
			logs.Err(err)
			return
		}
		logs.PrintErr(this.update.Mark("history"))
	}
}

func (this *ScreenService) Run() error {

	err := this.init()
	if err != nil {
		return err
	}

	//增量更新历史交易
	this._update()

	cr := cron.New(cron.WithSeconds())
	cr.AddFunc("0 32 15 * * *", this._update)
	cr.Start()

	first := true

	for range time.NewTicker(this.Interval).C {

		//判断是否是交易日和交易时间
		if first || (common.Manage.Workday.TodayIs() && common.IsTradingTime()) {

			//更新实时数据
			err := this.updateRealtime(first)
			logs.PrintErr(err)

			//计算实时卖点
			err = this.realtimeShells()
			logs.PrintErr(err)

			//开始计算实时买点
			this.realtimeBuys()

			first = false
		}
	}

	return nil
}

// realtimeShells 计算实时卖点
func (this *ScreenService) realtimeShells() error {
	todayDate := time.Now().Format(time.DateOnly)

	//从本地加载历史成交
	trades, err := this.loadingTrades()
	if err != nil {
		return err
	}

	//计算实时卖点
	sells := []*Trade(nil)
	for _, t := range trades {
		if t.Sold {
			if strings.HasPrefix(t.SellTime, todayDate) {
				sells = append(sells, t)
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
		for _, s := range this.Strategies {
			if s.Key == t.Strategy {
				if s := core.GetSell(s.Seller, ks, b, nil); s != nil {
					t.Sell(s)
					sells = append(sells, t)
					//更新到数据库
					_, err := this.DB.Where("ID=?", t.ID).Cols("Sold,SellTime,SellPrice,Income").Update(t)
					logs.PrintErr(err)
				}
			}
		}
	}
	this.mu.Lock()
	this.lastTrades = cloneTrades(trades)
	this.lastSells = sells
	this.mu.Unlock()
	//推送实时卖点数据
	this.broadcast(TypeSell, sells)
	return nil
}

// realtimeBuys 实时计算的买点
func (this *ScreenService) realtimeBuys() {
	items := []*Trade(nil)
	today := time.Now().Format(time.DateOnly)
	for _, code := range this.Codes {
		this.mu.RLock()
		ks := this.realtimeDayKlines[code]
		this.mu.RUnlock()
		if len(ks) == 0 {
			continue
		}
		//只评估当日K线的买点,避免盘前用昨日数据生成stale信号
		last := ks[len(ks)-1]
		if last.Time.Format(time.DateOnly) != today {
			continue
		}
		for _, s := range this.Strategies {
			if s.Buyer.Buy(code, ks) {
				t := &Trade{
					Code:     code,
					Name:     common.Manage.Codes.GetName(code),
					BuyTime:  last.Time.Format(time.DateTime),
					BuyPrice: last.Close,
					BuyRise:  last.RiseRate(),
					Strategy: s.Key,
					Tags:     s.checkTags(code, ks),
				}
				t.Realtime(last.Kline)
				items = append(items, t)
			}
		}
	}
	//处理买点,推送到前端
	this.mu.Lock()
	this.lastBuys = items
	this.mu.Unlock()
	this.broadcast(TypeBuy, items)
}

// Diagnose 诊断指定股票在指定策略下的匹配情况
func (s *ScreenService) Diagnose(code, strategyKey string) (*DiagnoseResponse, error) {
	code = protocol.AddPrefix(code)

	//选择策略
	var buyer core.Buyer
	var strategyName string
	if strategyKey == "" || strategyKey == "all" {
		ss := make([]core.Buyer, 0, len(s.Strategies))
		for _, st := range s.Strategies {
			if st.Buyer != nil {
				ss = append(ss, buy.Strategy(st.Name, st.Buyer))
			}
		}
		buyer = buy.Strategy("全部", buy.Or(ss))
		strategyName = "全部策略"
	} else {
		for _, st := range s.Strategies {
			if st.Key == strategyKey {
				buyer = st.Buyer
				strategyName = st.Name
				break
			}
		}
		if strategyName == "" {
			return nil, fmt.Errorf("策略不存在: %s", strategyKey)
		}
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

	//查询该股票的历史成交记录(从数据库),作为K线图买卖点标注的权威数据源
	trades := []*Trade(nil)
	if err := s.DB.Where("Code=?", code).Asc("BuyTime").Find(&trades); err != nil {
		logs.Errf("[诊断] 查询交易记录失败: %v", err)
	}

	//组装K线数据
	klines := make([]ChartKline, 0, len(ks))
	for _, k := range ks {
		klines = append(klines, ChartKline{
			Time:   k.Time.Format("2006-01-02"),
			Open:   k.Open,
			High:   k.High,
			Low:    k.Low,
			Close:  k.Close,
			Volume: k.Volume,
		})
	}

	//将买卖点追加到标注中(直接使用 trades 表的买卖时间,与成交记录一致)
	for _, t := range trades {
		//选了特定策略时,只展示该策略的标注
		if strategyKey != "" && strategyKey != "all" && t.Strategy != strategyKey {
			continue
		}
		//买点标注
		if buyTime, err := time.Parse(time.DateTime, t.BuyTime); err == nil {
			anns = append(anns, core.Annotation{
				Time:  buyTime,
				Price: t.BuyPrice,
				Label: "买",
				Color: "#ef4444", // A股: 红色买入
				Note:  fmt.Sprintf("买入 %.2f @ %s", t.BuyPrice, buyTime.Format("2006-01-02")),
			})
		}
		//卖点标注(仅已卖出的)
		if t.Sold {
			if sellTime, err := time.Parse(time.DateTime, t.SellTime); err == nil {
				anns = append(anns, core.Annotation{
					Time:  sellTime,
					Price: t.SellPrice,
					Label: "卖",
					Color: "#22c55e", // A股: 绿色卖出
					Note:  fmt.Sprintf("卖出 %.2f @ %s", t.SellPrice, sellTime.Format("2006-01-02")),
				})
			}
		}
	}
	diagTrades := make([]DiagnoseTrade, 0, len(trades))
	for _, t := range trades {
		//选了特定策略时,只展示该策略的成交记录
		if strategyKey != "" && strategyKey != "all" && t.Strategy != strategyKey {
			continue
		}
		t.Realtime(nil)
		dt := DiagnoseTrade{
			BuyTime:    t.BuyTime,
			BuyPrice:   t.BuyPrice,
			SellTime:   t.SellTime,
			SellPrice:  t.SellPrice,
			ProfitRate: t.Income,
			Sold:       t.Sold,
		}
		if t.Sold {
			dt.CurrPrice = t.SellPrice
		} else {
			//持仓中:用最新收盘价作为现价,计算实时收益率
			if len(ks) > 0 && ks[len(ks)-1] != nil {
				dt.CurrPrice = ks[len(ks)-1].Close
				if t.BuyPrice > 0 {
					dt.ProfitRate = (dt.CurrPrice - t.BuyPrice) / t.BuyPrice * 100
				}
			}
		}
		diagTrades = append(diagTrades, dt)
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
		Trades:      diagTrades,
	}, nil
}

// reloadHistoryKlines 重新加载历史日线缓存(用于跨天刷新)
func (this *ScreenService) getHistoryDayKlines() error {
	b := bar.NewCoroutine(
		len(this.Codes),
		this.Goroutines,
		bar.WithPrefix("[加载日线][xx000000]"),
	)
	defer b.Close()
	var mu sync.Mutex
	var failures []error
	for i := range this.Codes {
		code := this.Codes[i]
		b.Go(func() {
			b.SetPrefix(fmt.Sprintf("[加载日线][%s]", code))
			b.Flush()
			ks, err := common.Pull.DayKlines(code, time.Now().AddDate(-1, -6, 0), time.Now())
			if err != nil {
				b.Logf("[错误][%s] %v", code, err)
				b.Flush()
				mu.Lock()
				failures = append(failures, fmt.Errorf("%s 日线: %w", code, err))
				mu.Unlock()
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
	return errors.Join(failures...)
}

func (this *ScreenService) loadingTrades() ([]*Trade, error) {
	trades := []*Trade(nil)
	if err := this.DB.Find(&trades); err != nil {
		return nil, err
	}
	//缓存历史交易数据(供HTTP读取)
	this.mu.Lock()
	this.lastTrades = cloneTrades(trades)
	this.mu.Unlock()
	return trades, nil
}

// UpdateHistoryTrade 更新历史买卖点数据
func (this *ScreenService) updateHistoryTrade() error {

	//查询最新一条交易的买入时间
	var lastTrade Trade
	has, err := this.DB.Desc("BuyTime").Get(&lastTrade)
	if err != nil {
		return err
	}

	var days int
	var latest time.Time
	if has {
		//从最新买入时间开始计算需要扫描的天数
		latest, err = time.Parse(time.DateTime, lastTrade.BuyTime)
		if err != nil {
			return err
		}

		latest = latest.AddDate(0, 0, -1)
		latest = times.IntegerDay(latest)

		days = int(time.Since(latest)/(time.Hour*24)) + 1

		logs.Debug("重新历史节点:", latest.Format(time.DateTime), days)

	} else {
		//数据库为空,全量计算
		days = this.LookbackDays
	}

	//计算新交易
	ts, err := this.getHistoryTrade(days)
	if err != nil {
		return err
	}
	return this.replaceHistoryTrades(latest, ts)
}

// 完整计算成功后才替换区间，删除与插入共用事务，失败保留旧记录。
func (this *ScreenService) replaceHistoryTrades(latest time.Time, ts []*Trade) error {
	return this.DB.SessionFunc(func(session *xorm.Session) error {
		if !latest.IsZero() {
			if _, err := session.Where("BuyTime >= ?", latest.Format(time.DateTime)).Delete(&Trade{}); err != nil {
				return err
			}
		}
		for _, t := range ts {
			if t.BuyTime < latest.Format(time.DateTime) {
				continue
			}
			if _, err := session.Insert(t); err != nil {
				return err
			}
		}
		return nil
	})
}

// getHistoryTrade 计算历史买点
func (this *ScreenService) getHistoryTrade(days int) ([]*Trade, error) {
	if len(this.Codes) == 0 {
		return nil, fmt.Errorf("历史重算股票样本为空")
	}
	if common.Manage == nil {
		return nil, fmt.Errorf("行情运行时尚未初始化")
	}
	b := bar.NewCoroutine(
		len(this.Codes),
		this.Goroutines,
		bar.WithPrefix("[历史成交][xx000000]"),
	)
	defer b.Close()

	mu := sync.Mutex{}
	ts := []*Trade(nil)
	var failures []error
	for i := range this.Codes {
		code := this.Codes[i]

		this.mu.RLock()
		ks := this.historyDayKlines[code]
		this.mu.RUnlock()

		b.Go(func() {

			b.SetPrefix(fmt.Sprintf("[历史成交][%s]", code))
			b.Flush()
			if len(ks) == 0 {
				mu.Lock()
				failures = append(failures, fmt.Errorf("%s 缺少历史日线", code))
				mu.Unlock()
				return
			}

			//获取历史买点
			mbs := map[string][]*core.Buy{}
			for _, v := range this.Strategies {
				mbs[v.Key] = core.GetBuys(v.Buyer, code, ks, days)
			}
			if len(mbs) == 0 {
				return
			}

			//实时获取历史分钟数据
			var mks protocol.Klines
			err := common.Manage.Do(func(c tdx.Client) error {
				resp, err := extend.FetchStandardMinute(c, code, func(k *protocol.Kline) bool {
					return k.Time.Before(time.Now().AddDate(0, 0, -days-3))
				}, false)
				if err != nil {
					return err
				}
				mks = resp
				return nil
			})

			//本地获取历史分钟数据
			//mks, err := common.Pull.MinKlines(code, time.Now().AddDate(0, 0, -days*2), time.Now())

			if err != nil {
				b.Logf("[错误][%s] %v", code, err)
				b.Flush()
				mu.Lock()
				failures = append(failures, fmt.Errorf("%s 分钟行情: %w", code, err))
				mu.Unlock()
				return
			}

			mmks := map[string]protocol.Klines{}
			for _, v := range mks {
				key := v.Time.Format(time.DateOnly)
				mmks[key] = append(mmks[key], v)
			}

			for k, bs := range mbs {

				for _, b := range bs {
					if b == nil {
						continue
					}

					//用截止买入当日的 K 线切片评估 Tags,避免未来函数
					hisKs := extend.Klines{}
					for _, kk := range ks {
						hisKs = append(hisKs, kk)
						if kk.Time.Equal(b.Time) {
							break
						}
					}

					for _, st := range this.Strategies {
						if k == st.Key {
							//用完整 ks + 分钟数据查找未来卖点(未找到则 Sold=false)
							s := core.GetSell(st.Seller, ks, *b, mmks)
							t := &Trade{
								Code:     code,
								Name:     common.Manage.Codes.GetName(code),
								BuyTime:  b.Time.Format(time.DateTime),
								BuyPrice: b.Price,
								BuyRise:  b.Rise,
								Strategy: st.Key,
								Tags:     st.checkTags(code, hisKs),
							}
							mu.Lock()
							ts = append(ts, t.Sell(s))
							mu.Unlock()
						}
					}
				}
			}

		})
	}

	b.Wait()
	return ts, errors.Join(failures...)
}

// getRealtimeQuotes - 批量获取实时行情
// 通达信 API 每次最多支持 80 个代码，需分批拉取
func (this *ScreenService) getRealtimeKlines() (map[string]*protocol.Kline, error) {
	codes := this.Codes
	quoteKline := make(map[string]*protocol.Kline, len(codes))
	batchSize := 80

	b := bar.New(
		bar.WithTotal(int64(len(codes))),
		bar.WithPrefix("[实时行情]"),
		bar.WithFlush(),
	)
	defer b.Close()

	for i := 0; i < len(codes); i += batchSize {
		end := i + batchSize
		if end > len(codes) {
			end = len(codes)
		}
		if err := common.Manage.Do(func(c tdx.Client) error {
			quotes, err := c.GetQuote(codes[i:end]...)
			if err != nil {
				return err
			}
			for _, q := range quotes {
				if q.Standard == nil {
					return fmt.Errorf("非标准行情无法用于当前选股模型: %s", q.Instrument.String())
				}
				quoteKline[q.Instrument.Exchange.String()+q.Instrument.Code] = q.Standard.Kline
			}
			return nil
		}); err != nil {
			logs.Errf("[行情] 批量获取失败(%d-%d): %v", i, end, err)
		}
		b.Add(int64(end - i)).Flush()
	}

	return quoteKline, nil
}
