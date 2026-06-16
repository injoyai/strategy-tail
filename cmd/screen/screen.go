package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"time"

	_ "github.com/glebarez/go-sqlite"
	"github.com/injoyai/bar"
	"github.com/injoyai/frame/fbr" // Web框架
	"github.com/injoyai/logs"      // 日志库
	"github.com/injoyai/strategy-tail"
	"github.com/injoyai/strategy-tail/core" // 选股核心模块
	"github.com/injoyai/tdx"                // 通达信SDK
	"github.com/injoyai/tdx/extend"         // 通达信扩展功能
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
	core.Buyer                 //买入策略
	core.Seller                //卖出策略

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
		if realKline != nil && realKline.Time.Format(time.DateOnly) > last.Time.Format(time.DateOnly) {
			ks = append(ks, &extend.Kline{
				Unix:       realKline.Time.Unix(),
				Kline:      realKline,
				FloatStock: last.FloatStock,
				TotalStock: last.TotalStock,
			})
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

// marshal - 将内部数据转换为前端期望的JSON格式
func (s *ScreenService) marshal(payload any) string {
	now := time.Now().Format(time.DateTime)

	var resp any
	switch v := payload.(type) {
	case []*core.Buy:
		items := make([]BuyItem, len(v))
		for i, b := range v {
			s.mu.RLock()
			ks := s.realtimeDayKlines[b.Code]
			s.mu.RUnlock()
			var rise float64
			if len(ks) >= 2 && ks[len(ks)-2] != nil && ks[len(ks)-2].Close > 0 {
				rise = (b.Price.Float64() - ks[len(ks)-2].Close.Float64()) / ks[len(ks)-2].Close.Float64() * 100
			}
			items[i] = BuyItem{
				Code:  b.Code,
				Name:  common.Manage.Codes.GetName(b.Code),
				Time:  b.Time.Format(time.DateTime),
				Price: b.Price.Float64(),
				Rise:  rise,
			}
		}
		resp = BuyResponse{Type: "buy", Count: len(items), Time: now, Results: items}

	case []*core.Sell:
		items := make([]Trade, len(v))
		for i, se := range v {
			//从lastTrades中匹配对应的交易
			s.mu.RLock()
			for _, t := range s.lastTrades {
				if t.Code == se.Code {
					items[i] = *t
					break
				}
			}
			s.mu.RUnlock()
		}
		resp = SellResponse{Type: "sell", Count: len(items), Time: now, Results: items}

	case []*Trade:
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
		resp = HistoryResponse{Type: "history", Time: now, Total: len(items), Results: items}

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

func (this *ScreenService) Run() {

	first := true

	for range time.NewTicker(this.Interval).C {

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

			//推送历史成交数据
			this.mu.Lock()
			this.lastTrades = trades
			this.mu.Unlock()
			this.broadcast(trades)

			//计算实时卖点
			sells := []*core.Sell(nil)
			for _, t := range trades {
				if t.Sold {
					continue
				}
				b, err := t.Buy()
				if err != nil {
					logs.Err(err)
					continue
				}
				this.mu.RLock()
				ks := this.realtimeDayKlines[t.Code]
				this.mu.RUnlock()
				//实时计算卖点
				if s := core.GetSell(this.Seller, ks, b, nil); s != nil {
					sells = append(sells, s)
					//更新到数据库
					_, err := this.DB.Where("ID=?", t.ID).Update(t.Sell(s))
					logs.PrintErr(err)
				}
			}

			//推送历史成交数据
			//this.mu.Lock()
			//this.lastSells = sells
			//this.mu.Unlock()
			this.broadcast(sells)
			this.broadcast(trades)

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
	for _, code := range this.Codes {
		this.mu.RLock()
		ks := this.realtimeDayKlines[code]
		this.mu.RUnlock()
		if this.Buyer.Buy(code, ks) {
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
		this.Codes = common.GetNoPriceLimitCodes()
	}
	if this.Buyer == nil {
		this.Buyer = common.MACDBuyer
	}
	if this.Seller == nil {
		this.Seller = common.MACDSeller
	}

	if this.historyDayKlines == nil {
		this.historyDayKlines = map[string]extend.Klines{}
	}
	if this.realtimeDayKlines == nil {
		this.realtimeDayKlines = map[string]extend.Klines{}
	}

	//===================================================//

	this.DB.Sync2(new(Trade))

	//更新历史数据
	if err := common.Pull.Update(common.Manage); err != nil {
		return err
	}

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
			ks, err := common.Pull.DayKlines(code)
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
	updated, err := update.Updated("history-trade")
	if err != nil {
		return err
	}
	if !updated {
		//更新历史交易数据
		if err := this.updateHistoryTrade(); err != nil {
			return err
		}
		if err = update.Update("history-trade"); err != nil {
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

			bs := core.GetBuys(this.Buyer, code, ks, this.LookbackDays)
			if len(bs) == 0 {
				return
			}

			//获取历史分钟数据
			var mks protocol.Klines
			err := common.Manage.Do(func(c *tdx.Client) error {
				resp, err := c.GetKlineMinuteUntil(code, func(k *protocol.Kline) bool {
					return k.Time.Before(time.Now().AddDate(0, 0, -30))
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
				t := &Trade{
					Code:     code,
					Name:     common.Manage.Codes.GetName(code),
					BuyTime:  b.Time.Format(time.DateTime),
					BuyPrice: b.Price.Float64(),
				}
				s := core.GetSell(this.Seller, ks, *b, mmks)
				mu.Lock()
				ts = append(ts, t.Sell(s))
				mu.Unlock()
			}

		})
	}

	b.Wait()
	return ts
}
