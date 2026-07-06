package buy

import (
	"fmt"

	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/lib/extend"
)

// A站上N日均线 判断收盘价连续 N 天站在指定均线上方。
// Period 表示均线周期，默认 20。
// Days 表示收盘价需在均线上方运行的连续天数（含今天），默认 5。
// 触发条件：最近 Days 天，每天的收盘价都大于当天的 MA(Period)。
//
// 适合作为趋势确立的过滤条件，确保价格已经在均线上方稳定运行。
// 常与 A均线多头排列 组合使用，先确认多头排列，再确认价格站稳。
type A站上N日均线 struct {
	Period int
	Days   int
}

func (s A站上N日均线) Name() string {
	period := s.Period
	if period == 0 {
		period = 20
	}
	days := s.Days
	if days == 0 {
		days = 5
	}
	return fmt.Sprintf("站上MA%d_%d天", period, days)
}

func (s A站上N日均线) Buy(code string, dks extend.Klines) bool {
	period := s.Period
	if period == 0 {
		period = 20
	}
	days := s.Days
	if days == 0 {
		days = 5
	}

	n := len(dks)
	minLen := period + days
	if n < minLen {
		return false
	}

	// 最近 Days 天，每天的收盘价都大于当天的 MA(Period)
	for i := 0; i < days; i++ {
		idx := n - 1 - i
		if idx < 0 {
			return false
		}
		maAtDay := core.MA(dks[:idx+1], period)
		closePrice := dks[idx].Close.Float64()
		if closePrice <= maAtDay {
			return false
		}
	}

	return true
}
