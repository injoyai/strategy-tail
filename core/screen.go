package core

import (
	"time"

	"github.com/injoyai/bar"
	"github.com/injoyai/conv"
	"github.com/injoyai/tdx/extend"
)

type Screen struct {
	Buyer
	GetDayKlines func(code string, start, end time.Time) (extend.Klines, error)
}

func (s Screen) Run(codes []string, at ...time.Time) ([]*Buy, error) {
	now := conv.Default(time.Now(), at...)
	start := now.AddDate(0, -4, 0)
	end := time.Date(now.Year(), now.Month(), now.Day(), 15, 1, 0, 0, time.Local)

	p := bar.NewCoroutine(
		len(codes),
		10,
		bar.WithPrefix("[选股][xx000000]"),
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
			buy := s.Buyer.Buy(code, dks, nil)
			if buy != nil {
				result = append(result, buy)
			}
		})
	}
	p.Wait()
	return result, nil
}
