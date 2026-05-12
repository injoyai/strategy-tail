package strategies

import (
	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/tdx/extend"
)

type SellRSI struct {
	Period    int
	Threshold float64
}

func (s SellRSI) Name() string {
	return "RSI卖出"
}

// Sell
// @history 是之前的日线数据
// @ future 是未来的日线数据,判断哪一天卖出
// @ getMinklines 是未来的某一天的分钟K线数据,用于精细化具体卖出点
func (s SellRSI) Sell(code string, history, future extend.Klines, getMinklines func(after int) core.Klines, buy core.Buy) *core.Sell {
	if s.Period == 0 {
		s.Period = 14
	}
	if s.Threshold == 0 {
		s.Threshold = 50
	}

	//遍历未来的每一天
	for i := range future {
		if len(history)+i+1 < s.Period+1 {
			//数据量不足,过滤,一般不会出现
			continue
		}

		//计算未来某一天的rsi
		rsi := calcRSI(append(history, future[:i+1]...), s.Period)

		if rsi <= s.Threshold {
			continue
		}

		return &core.Sell{
			Code:  code,
			Time:  future[i].Time,
			Price: future[i].Open,
		}

	}

	//遍历完所有未来的数据,没有符合的卖点
	return nil
}
