package main

import (
	"time"

	"github.com/injoyai/bar"
	"github.com/injoyai/conv"
)

type Screen struct {
	Buyer
}

func (this Screen) Run(codes []string, at ...time.Time) ([]*Buy, error) {

	now := conv.Default(time.Now(), at...)
	start := now.AddDate(0, -4, 0)
	end := time.Date(now.Year(), now.Month(), now.Day(), 15, 1, 0, 0, time.Local)

	b := bar.NewCoroutine(
		len(codes),
		10,
		bar.WithPrefix("[选股][xx000000]"),
	)

	result := make([]*Buy, 0)
	for _, code := range codes {
		b.Go(func() {
			b.SetPrefix("[选股][" + code + "]")
			dks, err := getDayKlines(code, start, end)
			if err != nil {
				b.Logf("[错误] %s", err)
				b.Flush()
				return
			}
			//mks, err := getMinKlines(code, start, end)
			//if err != nil {
			//	b.Logf("[错误] %s", err)
			//	b.Flush()
			//	return
			//}
			buy := this.Buyer.Buy(code, dks, nil)
			if buy != nil {
				result = append(result, buy)
			}
		})

	}
	b.Wait()
	return result, nil
}
