package sell

import (
	"fmt"

	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/lib/extend"
)

// A止盈止损 是基于买入价的固定比例止盈止损卖出策略。
// TakeProfit 表示止盈比例，例如 0.05 表示盈利达到 5% 触发止盈。
// StopLoss 表示止损比例，例如 0.03 表示亏损达到 3% 触发止损。
//
// 与 SellTPSL 的关键区别：只从买入日之后开始判断，
// 避免遍历买入日之前的历史数据导致误触发。
// 回测引擎传入的 dks 包含买入日之前的历史数据，
// 本策略先定位买入日索引，只检查买入后的K线。
type A止盈止损 struct {
	TakeProfit float64
	StopLoss   float64
}

func (s A止盈止损) Name() string {
	switch {
	case s.TakeProfit > 0 && s.StopLoss > 0:
		return fmt.Sprintf("止盈%.0f%%/止损%.0f%%", s.TakeProfit*100, s.StopLoss*100)
	case s.TakeProfit > 0:
		return fmt.Sprintf("止盈%.0f%%", s.TakeProfit*100)
	case s.StopLoss > 0:
		return fmt.Sprintf("止损%.0f%%", s.StopLoss*100)
	default:
		return "止盈止损"
	}
}

func (s A止盈止损) Sell(code string, dks extend.Klines, buy core.Buy) bool {
	if len(dks) == 0 {
		return false
	}
	if s.TakeProfit <= 0 && s.StopLoss <= 0 {
		return false
	}
	buyPrice := buy.Price.Float64()
	if buyPrice <= 0 {
		return false
	}

	// 定位买入日在 dks 中的索引
	buyIdx := -1
	buyDate := buy.Time.Format("2006-01-02")
	for i, k := range dks {
		if k.Time.Format("2006-01-02") == buyDate {
			buyIdx = i
			break
		}
	}
	// 找不到买入日，保守不触发
	if buyIdx < 0 {
		return false
	}

	// 只检查买入日之后的K线（不含买入日当天）
	for i := buyIdx + 1; i < len(dks); i++ {
		closePrice := dks[i].Close.Float64()
		rate := (closePrice - buyPrice) / buyPrice

		if s.TakeProfit > 0 && rate >= s.TakeProfit {
			return true
		}
		if s.StopLoss > 0 && rate <= -s.StopLoss {
			return true
		}
	}

	return false
}
