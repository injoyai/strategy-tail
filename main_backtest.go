package main

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
	IStrategy
}

func (this Backtest) Run(codes []string, years []int) {
	logs.Info(this.IStrategy)

	for _, year := range years {
		start := time.Date(year, 1, 1, 0, 0, 0, 0, time.Local)
		end := time.Date(year, 12, 31, 23, 0, 0, 0, time.Local)

		ls, err := this._backtest(codes, start, end)
		logs.PanicErr(err)

		fmt.Printf("回测年份: %d\n", year)
		Analyze(ls)
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
			dks, err := getDayKlines(code, start, end)
			if err != nil {
				b.Logf("[错误] %s", err)
				b.Flush()
				return
			}
			var mks protocol.Klines
			mks, err = getMinKlines(code, start, end)
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
	mmks := map[string]protocol.Klines{}
	for _, mk := range mks {
		key := mk.Time.Format(time.DateOnly)
		mmks[key] = append(mmks[key], mk)
	}
	ts := []Trade(nil)

	var currentBuy *Buy

	for i := 0; i < len(dks); i++ {
		dk := dks[i]
		mk := protocol.Klines(nil)
		if v, ok := mmks[dk.Time.Format(time.DateOnly)]; ok {
			mk = v
		}

		if currentBuy == nil {
			buy := this.Buy(code, dks[:i+1], mk)
			if buy != nil {
				currentBuy = buy
			}
		} else {
			sell := this.Sell(code, dks[:i+1], mk, *currentBuy)
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
