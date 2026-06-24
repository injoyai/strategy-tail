package sell

import (
	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/lib/extend"
)

// LongMADown 是 250 日或 60 日均线不再同时向上时卖出的策略。
// Lookback 表示连续向上比较的交易日数量，默认 5。
// 当 250 日均线或 60 日均线在最近 Lookback 个交易日中出现走平或走弱时返回卖出信号。
// 该策略与 buy.LongMAUp 对应，用于在中长期趋势过滤条件失效时退出持仓。
type LongMADown struct {
	Lookback int
}

func (s LongMADown) Name() string {
	return "250/60日均线转弱"
}

func (s LongMADown) Sell(code string, dks extend.Klines, buy core.Buy) bool {
	if s.Lookback == 0 {
		s.Lookback = 5
	}
	return !maUp(dks, 250, s.Lookback) || !maUp(dks, 60, s.Lookback)
}

func maUp(dks extend.Klines, period, lookback int) bool {
	if period <= 0 || lookback <= 0 || len(dks) < period+lookback {
		return false
	}

	n := len(dks)
	for x := 0; x < lookback; x++ {
		maNow := core.MA(dks[:n-x], period)
		maPrev := core.MA(dks[:n-x-1], period)
		if maNow <= maPrev {
			return false
		}
	}
	return true
}
