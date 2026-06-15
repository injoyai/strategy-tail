package core

import (
	"io"
	"sync"
	"time"

	"github.com/injoyai/bar"
	"github.com/injoyai/conv"
	"github.com/injoyai/tdx/extend"
	"github.com/injoyai/tdx/protocol"
)

type Screen struct {
	Buyer
	Codes        []string
	Goroutines   int
	GetDayKlines func(code string, start, end time.Time) (extend.Klines, error)
	ShowBar      bool
}

func (s Screen) Run(codes []string, at ...time.Time) ([]*Buy, error) {
	now := conv.Default(time.Now(), at...)
	start := now.AddDate(0, -4, 0)
	end := time.Date(now.Year(), now.Month(), now.Day(), 15, 1, 0, 0, time.Local)

	p := bar.NewCoroutine(
		len(codes),
		s.Goroutines,
		bar.WithPrefix("[选股][xx000000]"),
		func(b *bar.Bar) {
			if !s.ShowBar {
				b.SetWriter(io.Discard)
			}
		},
	)

	var mu sync.Mutex
	result := make([]*Buy, 0)
	for _, code := range codes {
		code := code
		p.Go(func() {
			p.SetPrefix("[选股][" + code + "]")
			dks, err := s.GetDayKlines(code, start, end)
			if err != nil {
				p.Logf("[错误] %s", err)
				p.Flush()
				return
			}
			if len(dks) == 0 {
				return
			}
			today := dks[len(dks)-1]
			if s.Buyer.Buy(code, dks) {
				mu.Lock()
				result = append(result, &Buy{
					Code:  code,
					Time:  today.Time,
					Price: today.Close,
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
				ls = append(ls, &Buy{
					Code:  code,
					Time:  k.Time,
					Price: k.Close,
				})
			}
		}
	}
	return ls
}

func GetSell(s Seller, ks extend.Klines, buy Buy, minKs map[string]protocol.Klines) *Sell {

	for i := range ks {
		k := ks[i]
		if k.Time.After(buy.Time) {

			mks := minKs[k.Time.Format(time.DateOnly)]
			if len(mks) == 0 {
				mks = protocol.Klines{k.Kline}
			}

			his := ks[:i]

			for ii := range mks {
				minuteKlines := mks[:ii+1]
				lastMinuteKline := mks[ii]
				k.Kline = minuteKlines.Kline(lastMinuteKline.Time, lastMinuteKline.Open)

				if s.Sell(buy.Code, append(his, k), buy) {
					return &Sell{
						Code:  buy.Code,
						Time:  k.Time,
						Price: k.Close,
					}
				}
			}

		}
	}
	return nil
}
