package core

import (
	"fmt"
	"sync"
	"time"

	"github.com/injoyai/bar"
	"github.com/injoyai/logs"
	"github.com/injoyai/tdx/extend"
	"github.com/injoyai/tdx/protocol"
)

type Backtest struct {
	Strategy                                                                      //策略
	Codes        []string                                                         //股票代码
	Years        []int                                                            //回测年
	GetDayKlines func(code string, start, end time.Time) (extend.Klines, error)   //获取日线数据函数
	GetMinKlines func(code string, start, end time.Time) (protocol.Klines, error) //获取分钟数据函数
}

func (this Backtest) Run() {
	logs.Info(this.Strategy)

	for _, year := range this.Years {
		start := time.Date(year, 1, 1, 0, 0, 0, 0, time.Local)
		end := time.Date(year, 12, 31, 23, 0, 0, 0, time.Local)

		ls, err := this._backtest(this.Codes, start, end)
		logs.PanicErr(err)

		fmt.Printf("回测年份: %d\n", year)
		Analyze(ls, func(code string) (extend.Klines, error) {
			return this.GetDayKlines(code, time.Time{}, time.Now())
		})
	}
}

func (this Backtest) _backtest(codes []string, start, end time.Time) ([]Trade, error) {
	result := []Trade(nil)
	mu := sync.Mutex{}
	b := bar.NewCoroutine(
		len(codes),
		10,
		bar.WithPrefix("[回测][xx000000]"),
	)
	defer b.Close()
	for _, code := range codes {
		b.Go(func() {
			b.SetPrefix("[回测][" + code + "]")
			dks, err := this.GetDayKlines(code, start, end)
			if err != nil {
				b.Logf("[错误] %s", err)
				b.Flush()
				return
			}
			var mks protocol.Klines
			mks, err = this.GetMinKlines(code, start, end)
			if err != nil {
				b.Logf("[错误] %s", err)
				b.Flush()
				return
			}
			ts := this.Do(code, dks, mks)
			mu.Lock()
			defer mu.Unlock()
			result = append(result, ts...)
		})

	}
	b.Wait()
	return result, nil
}

func (this Backtest) Do(code string, dks extend.Klines, mks protocol.Klines) []Trade {

	m := map[string]protocol.Klines{}
	for _, mk := range mks {
		key := mk.Time.Format(time.DateOnly)
		m[key] = append(m[key], mk)
	}

	ts := []Trade(nil)

	var currentBuy *Buy
	for i := 0; i < len(dks); i++ {

		dk := dks[i]
		minKlines := GetMinKlines{
			today: dk.Time,
			m:     map[string]protocol.Klines{},
		}

		if currentBuy == nil {
			buy := this.Buy(code, dks[:i+1], minKlines.Get(0))
			if buy != nil {
				currentBuy = buy
			}
		} else {
			sell := this.Sell(code, dks[:i], dks[i+1:], minKlines.Get, *currentBuy)
			if sell != nil {
				tr := Trade{
					Code:      code,
					BuyTime:   currentBuy.Time,
					SellTime:  sell.Time,
					BuyPrice:  currentBuy.Price + protocol.Yuan(0.01),
					SellPrice: sell.Price - protocol.Yuan(0.01),
				}
				ts = append(ts, tr)
				currentBuy = nil
			}
		}
	}
	return ts
}
