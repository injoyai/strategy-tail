package core

import (
	"io"
	"sync"
	"time"

	"github.com/injoyai/bar"
	"github.com/injoyai/conv"
	"github.com/injoyai/strategy-tail/lib/extend"
	"github.com/injoyai/tdx/v2/protocol"
)

type Screen struct {
	Buyer
	Codes        []string
	Goroutines   int
	GetDayKlines func(code string, start, end time.Time) (extend.Klines, error)
	ShowBar      bool
}

func (this Screen) Run(at ...time.Time) ([]*Buy, error) {
	now := conv.Default(time.Now(), at...)
	start := now.AddDate(0, -4, 0)
	end := time.Date(now.Year(), now.Month(), now.Day(), 15, 1, 0, 0, time.Local)

	p := bar.NewCoroutine(
		len(this.Codes),
		this.Goroutines,
		bar.WithPrefix("[选股][xx000000]"),
		func(b *bar.Bar) {
			if !this.ShowBar {
				b.SetWriter(io.Discard)
			}
		},
	)

	var mu sync.Mutex
	result := make([]*Buy, 0)
	for _, code := range this.Codes {
		code := code
		p.Go(func() {
			p.SetPrefix("[选股][" + code + "]")
			dks, err := this.GetDayKlines(code, start, end)
			if err != nil {
				p.Logf("[错误] %s", err)
				p.Flush()
				return
			}
			if len(dks) == 0 {
				return
			}
			today := dks[len(dks)-1]
			if this.Buyer.Buy(code, dks) {
				rise, _ := today.RisePercent()
				mu.Lock()
				result = append(result, &Buy{
					Code:  code,
					Time:  today.Time,
					Price: today.Close,
					Rise:  rise,
				})
				mu.Unlock()
			}
		})
	}
	p.Wait()

	return result, nil
}

// GetBuys 获取历史买点
func GetBuys(b Buyer, code string, ks extend.Klines, days int) []*Buy {
	ls := []*Buy(nil)
	for i := 0; i < days; i++ {
		if len(ks) > i {
			if b.Buy(code, ks[:len(ks)-i]) {
				k := ks[len(ks)-i-1]
				rise, _ := k.RisePercent()
				ls = append(ls, &Buy{
					Code:  code,
					Time:  k.Time,
					Price: k.Close,
					Rise:  rise,
				})
			}
		}
	}
	return ls
}

// GetSell finds the first eligible sell signal on a later trading day.
// Minute snapshots use the preceding close and never replace the caller's daily bars.
func GetSell(s Seller, ks extend.Klines, buy Buy, minKs map[string]protocol.Klines) *Sell {
	return GetSellSince(s, ks, buy, time.Time{}, minKs)
}

// GetSellSince restricts sell evaluation to bars on or after since.
func GetSellSince(s Seller, ks extend.Klines, buy Buy, since time.Time, minKs map[string]protocol.Klines) *Sell {
	if s == nil {
		return nil
	}
	buyDate := DayOf(buy.Time.In(protocol.Location()))
	input := append(extend.Klines(nil), ks...)
	for i, day := range ks {
		if day.Time.Before(since) || !DayOf(day.Time.In(protocol.Location())).After(buyDate) {
			continue
		}
		minutes := minKs[day.Time.Format(time.DateOnly)]
		if len(minutes) == 0 {
			if s.Sell(buy.Code, input[:i+1], buy) {
				return &Sell{Code: buy.Code, Time: day.Time, Price: day.Close}
			}
			continue
		}

		snapshot := *day
		var aggregate protocol.Kline
		snapshot.Kline = &aggregate
		input[i] = &snapshot
		started := false
		for _, minute := range minutes {
			if minute.Close <= 0 {
				continue
			}
			if !started {
				aggregate = *minute
				aggregate.Last = day.Last
				started = true
			} else {
				if aggregate.Open == 0 {
					aggregate.Open = minute.Open
				}
				aggregate.Time, aggregate.Close = minute.Time, minute.Close
				aggregate.High, aggregate.Low = max(aggregate.High, minute.High), min(aggregate.Low, minute.Low)
				aggregate.Volume += minute.Volume
				aggregate.Amount += minute.Amount
				aggregate.Order += minute.Order
				aggregate.UpCount, aggregate.DownCount = minute.UpCount, minute.DownCount
			}
			snapshot.Unix = minute.Time.Unix()
			if snapshot.FloatStock > 0 {
				equity := protocol.Equity{Float: snapshot.FloatStock}
				snapshot.Turnover = equity.Turnover(protocol.ToShares(aggregate.Volume))
			}
			if s.Sell(buy.Code, input[:i+1], buy) {
				return &Sell{Code: buy.Code, Time: aggregate.Time, Price: aggregate.Close}
			}
		}
		input[i] = day
	}
	return nil
}
