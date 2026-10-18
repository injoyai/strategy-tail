package sell

import (
	"fmt"

	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/lib/extend"
	"github.com/injoyai/strategy-tail/strategies/rule"
)

// A持仓N天 买入后满 N 个交易日强制卖出。
// Days 表示持仓天数上限（不含买入日），默认 5。
// 触发条件：当前交易日（dks 最后一天）距买入日达到 Days 个交易日。
//
// 按交易日索引计算，不按日历日，避免周末/节假日错位。
// 用于确保短线策略的持仓周期可控，资金不沉淀在横盘股上。
type A持仓N天 struct {
	Days int
}

func (s A持仓N天) Name() string {
	days := s.Days
	if days == 0 {
		days = 5
	}
	return fmt.Sprintf("%d天强制平仓", days)
}

func (s A持仓N天) Sell(code string, dks extend.Klines, buy core.Buy) bool {
	days := s.Days
	if days == 0 {
		days = 5
	}
	// 判断委托共用内核：已持仓交易日数 >= days（恰等触发）。定位口径
	// 固化在内核 PositionHoldingDays：顺序首个不早于买入时刻（与旧
	// 一致）；持仓不可用、空前缀或买入日在前缀之后均无效不触发，
	// 等价旧 buyIdx<0 预检查。
	c, err := rule.NewCompare(rule.PositionHoldingDays(), rule.Ge, rule.Literal(rule.FloatValue(float64(days))))
	if err != nil {
		return false
	}
	return c.Eval(rule.NewInput(code, dks, nil, &buy))
}
