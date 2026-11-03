package buy

import (
	"fmt"

	"github.com/injoyai/strategy-tail/lib/extend"
	"github.com/injoyai/strategy-tail/strategies/rule"
)

// A回踩N日均线 判断股价回踩指定均线后收回的形态。
// Period 表示回踩的均线周期，默认 5（MA5）。
// SupportPeriod 表示下方支撑均线周期，默认 10（MA10），0 表示不校验支撑。
// MinTouchDays 表示近几天内至少有 1 天最低价触及/跌破 MA(Period)，默认 3。
// 触发条件：
//  1. 昨天收盘价 ≤ MA(Period)（昨天还在均线上或下方，确保今天才是反弹确认）
//  2. 今天收盘价 > MA(Period) 且 > 昨天收盘价（今天收回均线且收阳）
//  3. 近 MinTouchDays 天内，至少有 1 天最低价 ≤ MA(Period)（确实发生了回踩）
//  4. 若 SupportPeriod > 0：回踩期间最低价不跌破 MA(SupportPeriod)
//
// 关键改进：条件1确保买入日是"回踩后反弹的第一天"，
// 避免在已经反弹几天后的高点买入。
type A回踩N日均线 struct {
	Period        int
	SupportPeriod int
	MinTouchDays  int
}

func (s A回踩N日均线) Name() string {
	period := s.Period
	if period == 0 {
		period = 5
	}
	touchDays := s.MinTouchDays
	if touchDays == 0 {
		touchDays = 3
	}
	if s.SupportPeriod > 0 {
		return fmt.Sprintf("回踩MA%d不破MA%d·%d天窗口", period, s.SupportPeriod, touchDays)
	}
	return fmt.Sprintf("回踩MA%d·%d天窗口", period, touchDays)
}

func (s A回踩N日均线) Buy(code string, dks extend.Klines) bool {
	period := s.Period
	if period == 0 {
		period = 5
	}
	supportPeriod := s.SupportPeriod
	touchDays := s.MinTouchDays
	if touchDays == 0 {
		touchDays = 3
	}

	// 判断委托共用内核，四条件与旧实现逐条同构（台账风险 §13.14）：
	// 预热门槛沿用旧式 max(period,support)+touchDays+1；条件2 今收>当日
	// MA 与 今收>昨收（价格比较）两条比较表达；条件1 昨收≤昨日 MA
	// 用 CountWindow 窗口 1 天不含今日表达（谓词按昨日截断前缀求值）；
	// 条件3 触及判定用 CountWindow(touchDays,1) 逐日按截断前缀重算均线，
	// 与旧循环 f.A均线(period).Value(code, dks[:i+1]) 口径一致；
	// 条件4 支撑钳制用窗口最低价 >= touchDays+1 日前的支撑均线表达，
	// 预热门槛保证旧 supportIdx 钳制分支不可达，RefOperand 恰等。
	minLen := period + touchDays + 1 // +1 因为需要昨天的数据
	if supportPeriod > 0 && supportPeriod+touchDays+1 > minLen {
		minLen = supportPeriod + touchDays + 1
	}
	gate, err := rule.NewMinKlines(minLen)
	if err != nil {
		return false
	}
	above, err := rule.NewCompare(rule.CloseFloat(), rule.Gt, rule.MAValue(period))
	if err != nil {
		return false
	}
	yang, err := rule.NewCompare(rule.ClosePrice(), rule.Gt, rule.PrevClosePrice())
	if err != nil {
		return false
	}
	belowYesterday, err := rule.NewCompare(rule.CloseFloat(), rule.Le, rule.MAValue(period))
	if err != nil {
		return false
	}
	pullback, err := rule.NewCountWindow(1, 1, belowYesterday, false)
	if err != nil {
		return false
	}
	touched, err := rule.NewCompare(rule.LowFloat(), rule.Le, rule.MAValue(period))
	if err != nil {
		return false
	}
	touchInWindow, err := rule.NewCountWindow(touchDays, 1, touched, false)
	if err != nil {
		return false
	}
	conds := []rule.Rule{gate, above, yang, pullback, touchInWindow}
	if supportPeriod > 0 {
		windowLow, err := rule.WindowMin(touchDays, rule.LowFloat(), false)
		if err != nil {
			return false
		}
		maSupportRef, err := rule.RefOperand(touchDays+1, rule.MAValue(supportPeriod))
		if err != nil {
			return false
		}
		held, err := rule.NewCompare(windowLow, rule.Ge, maSupportRef)
		if err != nil {
			return false
		}
		conds = append(conds, held)
	}
	and, err := rule.NewAnd(conds...)
	if err != nil {
		return false
	}
	return and.Eval(rule.NewInput(code, dks, nil, nil))
}
