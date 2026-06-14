package main

import (
	"encoding/json"
	"fmt"
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

	lastBuys    *BuyResponse     // 最新买点快照
	lastSells   *SellResponse    // 最新卖点快照
	lastHistory *HistoryResponse // 最新历史买点快照
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

		var last *extend.Kline
		if len(ks) > 0 {
			last = ks[len(ks)-1]
		}

		realKline, ok := realKlines[code]
		if ok && realKline != nil {
			k := &extend.Kline{
				Unix:  realKline.Time.Unix(),
				Kline: realKline,
			}
			if last != nil {
				k.FloatStock = last.FloatStock
				k.TotalStock = last.TotalStock
				k.Last = last.Close
			}
			ks = append(ks, k)
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
func (s *ScreenService) snapshot() (*BuyResponse, *SellResponse, *HistoryResponse) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lastBuys, s.lastSells, s.lastHistory
}

// broadcast - 向所有订阅者广播消息
func (s *ScreenService) broadcast(payload any) {

	switch payload.(type) {
	case []*Trade:
	case []*core.Sell:
	case []*core.Buy:
	}

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

func (this *ScreenService) Run() {
	for range time.NewTicker(time.Second * 5).C {
		//判断是否是交易日和交易时间
		if true || common.Manage.Workday.TodayIs() && common.IsTradingTime() {
			//更新实时数据
			err := this.updateRealtime()
			logs.PrintErr(err)

			//读取历史未卖出交易数据
			trades := []*Trade(nil)
			if err := this.DB.Where("Sold=?", false).Find(&trades); err != nil {
				logs.Err(err)
			}

			//推送历史成交数据
			this.broadcast(trades)

			//计算实时卖点
			sells := []*core.Sell(nil)
			for _, t := range trades {
				b, err := t.Buy()
				if err != nil {
					logs.Err(err)
					continue
				}
				this.mu.RLock()
				ks := this.realtimeDayKlines[t.Code]
				this.mu.RUnlock()
				//实时计算卖点
				if s := core.GetSell(this.Seller, ks, b); s != nil {
					sells = append(sells, s)
					//更新到数据库
					_, err := this.DB.Where("ID=?", t.ID).Update(t.Sell(s))
					logs.PrintErr(err)
				}
			}

			//推送历史成交数据
			_ = sells
			_ = trades
			this.broadcast(sells)
			this.broadcast(trades)

			//开始计算实时买点/卖点
			buys := this.realtimeBuys()
			//处理买点,推送到前端
			_ = buys
			this.broadcast(buys)

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
			this.mu.Lock()
			defer this.mu.Unlock()
			this.historyDayKlines[code] = ks
		})
	}
	b.Wait()
	logs.Info("加载历史日线成功...")

	go func() {
		//更新历史买卖点,判断数据库是否有历史买卖点数据
		update, err := tdx.NewUpdated(this.DB, 0, 0)
		if err != nil {
			logs.Err(err)
			return
		}
		updated, err := update.Updated("history-tade")
		if err != nil {
			logs.Err(err)
			return
		}
		if !updated {
			//更新历史交易数据
			if err := this.updateHistoryTrade(); err != nil {
				logs.Err(err)
				return
			}
			update.Update("history-tade")
		}
	}()

	return nil
}

func (this *ScreenService) updateHistoryTrade() error {
	logs.Info("开始计算历史交割单...")
	ts := this.getHistoryTrade()
	return this.DB.SessionFunc(func(session *xorm.Session) error {
		if _, err := this.DB.Where("ID>0").Delete(new(Trade)); err != nil {
			return err
		}
		for _, t := range ts {
			if _, err := this.DB.Insert(t); err != nil {
				return err
			}
		}
		return nil
	})
}

// getHistoryBuys 获取历史买点
func (this *ScreenService) getHistoryTrade() []*Trade {
	b := bar.NewCoroutine(
		len(this.Codes),
		10,
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
			for _, b := range bs {
				t := &Trade{
					Code:       code,
					Name:       common.Manage.Codes.GetName(code),
					BuyTime:    b.Time.Format(time.DateTime),
					BuyPrice:   b.Price.Float64(),
					SellTime:   "",
					SellPrice:  0,
					ProfitRate: 0,
				}
				s := core.GetSell(this.Seller, ks, *b)
				if s != nil {
					t.SellTime = s.Time.Format(time.DateTime)
					t.SellPrice = s.Price.Float64()
					if b.Price > 0 {
						t.ProfitRate = (s.Price.Float64() - b.Price.Float64()) / b.Price.Float64()
					}
				}
				mu.Lock()
				ts = append(ts, t)
				mu.Unlock()
			}

		})
	}

	b.Wait()
	return ts
}
