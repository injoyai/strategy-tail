package core

import (
	"io"
	"time"

	"github.com/injoyai/bar"
	"github.com/injoyai/conv"
	"github.com/injoyai/tdx/extend"
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
				result = append(result, &Buy{
					Code:  code,
					Time:  today.Time,
					Price: today.Close,
				})
			}
		})
	}
	p.Wait()

	return result, nil
}
