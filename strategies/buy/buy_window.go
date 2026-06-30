package buy

import (
	"fmt"

	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/lib/extend"
)

// A近N天符合 是组合型买入条件。
// 当且仅当被包裹的 Buyer 在最近 Days 个交易日（含今天）的任意一天评估返回 true 时触发。
// Days 默认 10。
// 这是个通用包装器，例如：
//
//	buy.A近N天买入过{Days: 10, Buyer: buy.A倍量{}}
//
// 表示"近 10 天内出现过倍量买点"。
type A近N天符合 struct {
	Days  int
	Buyer core.Buyer
}

func (b A近N天符合) Name() string {
	days := b.Days
	if days == 0 {
		days = 10
	}
	inner := "Null"
	if b.Buyer != nil {
		inner = b.Buyer.Name()
	}
	return fmt.Sprintf("近%d天符合(%s)", days, inner)
}

func (b A近N天符合) Buy(code string, dks extend.Klines) bool {
	if b.Buyer == nil {
		return false
	}
	days := b.Days
	if days == 0 {
		days = 10
	}

	n := len(dks)
	if n == 0 {
		return false
	}
	start := n - days
	if start < 1 {
		start = 1
	}
	for i := start; i <= n; i++ {
		if b.Buyer.Buy(code, dks[:i]) {
			return true
		}
	}
	return false
}
