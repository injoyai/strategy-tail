package buy

import (
	"fmt"

	"github.com/injoyai/strategy-tail/lib/extend"
	"github.com/injoyai/strategy-tail/strategies/rule"
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

	// 判断委托共用内核，与旧实现同构：先预热拦截（n<period+days，与
	// MA上方连续天数自身的"均线无效即断"拦截相比更严，必须显式
	// MinKlines 表达），再最近 Days 天每天今收>当日均线（含今天，
	// 逐日按截断前缀求值，与因子逐日重算口径一致）。旧比较为
	// count>=days 无上限，Consecutive 上限传 0（上限不限），内核统计到
	// MinDays 即停，判定等价。
	gate, err := rule.NewMinKlines(period + days)
	if err != nil {
		return false
	}
	above, err := rule.NewCompare(rule.CloseFloat(), rule.Gt, rule.MAValue(period))
	if err != nil {
		return false
	}
	streak, err := rule.NewConsecutive(days, 0, above, true)
	if err != nil {
		return false
	}
	and, err := rule.NewAnd(gate, streak)
	if err != nil {
		return false
	}
	return and.Eval(rule.NewInput(code, dks, nil, nil))
}
