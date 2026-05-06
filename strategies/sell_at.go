package strategies

import (
	"time"

	"github.com/injoyai/conv"
	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/tdx/extend"
)

type SellAt struct {
	After int //后续第N天卖出
	Time  string
}

func (s SellAt) Name() string {
	return conv.String(s.After+1) + "天后卖出"
}

func (s SellAt) Sell(code string, history, future extend.Klines, getMinklines func(after int) core.Klines, buy core.Buy) *core.Sell {
	if len(future) <= s.After {
		return nil
	}

	sellTime := s.Time
	if len(sellTime) == 0 {
		sellTime = "10:00:00"
	}

	for _, v := range getMinklines(s.After) {
		if v.Time.Format(time.TimeOnly) >= sellTime {
			return &core.Sell{
				Code:  code,
				Time:  v.Time,
				Price: v.Close,
			}
		}
	}

	return &core.Sell{
		Code:  code,
		Time:  future[s.After].Time,
		Price: future[s.After].Open,
	}
}
