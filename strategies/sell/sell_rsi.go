package sell

import (
	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/strategies/util"
	"github.com/injoyai/tdx/extend"
)

// SellRSI 是 RSI 强弱恢复卖出策略。
// Period 表示 RSI 计算周期，默认 14。
// Threshold 表示卖出阈值，默认 50。
// 策略会逐日遍历 future，并且每次只使用 history + 截止当前 future 日期的数据计算 RSI，避免直接用完整未来数据产生未来函数。
// 当某一天 RSI 大于 Threshold 时，使用该日开盘价卖出。
type SellRSI struct {
	Period    int
	Threshold float64
}

func (s SellRSI) Name() string {
	return "RSI卖出"
}

func (s SellRSI) Sell(code string, history, future extend.Klines, getMinklines func(after int) core.Klines, buy core.Buy) *core.Sell {
	if s.Period == 0 {
		s.Period = 14
	}
	if s.Threshold == 0 {
		s.Threshold = 50
	}

	for i := range future {
		if len(history)+i+1 < s.Period+1 {
			continue
		}

		rsi := util.CalcRSI(append(history, future[:i+1]...), s.Period)

		if rsi <= s.Threshold {
			continue
		}

		return &core.Sell{
			Code:  code,
			Time:  future[i].Time,
			Price: future[i].Open,
		}

	}

	return nil
}
