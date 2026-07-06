package buy

import (
	"fmt"

	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/lib/extend"
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

	n := len(dks)
	minLen := period + touchDays + 1 // +1 因为需要昨天的数据
	if supportPeriod > 0 && supportPeriod+touchDays+1 > minLen {
		minLen = supportPeriod + touchDays + 1
	}
	if n < minLen {
		return false
	}

	today := dks[n-1]
	yesterday := dks[n-2]
	maPeriodToday := core.MA(dks, period)
	maPeriodYesterday := core.MA(dks[:n-1], period)

	// 条件2：今天收盘价 > MA(Period) 且 > 昨天收盘价（收回均线且收阳）
	if today.Close.Float64() <= maPeriodToday {
		return false
	}
	if today.Close.Float64() <= yesterday.Close.Float64() {
		return false
	}

	// 条件1：昨天收盘价 ≤ MA(Period)（昨天还在均线上或下方）
	// 这确保今天才是"收回均线"的第一天，避免在反弹高点买入
	if yesterday.Close.Float64() > maPeriodYesterday {
		return false
	}

	// 条件3：近 MinTouchDays 天内（不含今天），至少有1天最低价 ≤ MA(Period)
	touched := false
	windowStart := n - touchDays - 1 // 不含今天
	if windowStart < 0 {
		windowStart = 0
	}
	lowestLow := dks[windowStart].Low.Float64()
	for i := windowStart; i < n-1; i++ { // 不含今天
		maAtDay := core.MA(dks[:i+1], period)
		if dks[i].Low.Float64() <= maAtDay {
			touched = true
		}
		if dks[i].Low.Float64() < lowestLow {
			lowestLow = dks[i].Low.Float64()
		}
	}
	if !touched {
		return false
	}

	// 条件4：若 SupportPeriod > 0，回踩期间最低价不跌破 MA(SupportPeriod)
	if supportPeriod > 0 {
		supportIdx := windowStart - 1
		if supportIdx < supportPeriod {
			supportIdx = supportPeriod - 1
		}
		maSupport := core.MA(dks[:supportIdx+1], supportPeriod)
		if lowestLow < maSupport {
			return false
		}
	}

	return true
}
