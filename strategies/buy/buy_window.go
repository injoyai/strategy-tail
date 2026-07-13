package buy

import (
	"fmt"

	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/lib/extend"
)

func A近N天符合(days int, buy core.Buyer) core.Buyer {
	return 近N天符合{
		Days:  days,
		Buyer: buy,
	}
}

// A近N天符合 是组合型买入条件。
// 当且仅当被包裹的 Buyer 在最近 Days 个交易日（含今天）的任意一天评估返回 true 时触发。
// Days 默认 10。
// 这是个通用包装器，例如：
//
//	buy.A近N天买入过{Days: 10, Buyer: buy.A倍量{}}
//
// 表示"近 10 天内出现过倍量买点"。
type 近N天符合 struct {
	Days  int
	Buyer core.Buyer
}

func (b 近N天符合) Name() string {
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

func (b 近N天符合) Buy(code string, dks extend.Klines) bool {
	if b.Buyer == nil {
		return false
	}
	days := b.Days
	if days == 0 {
		days = 10
	}
	for i := len(dks); i >= len(dks)-days && i >= 1; i-- {
		if b.Buyer.Buy(code, dks[:i]) {
			return true
		}
	}
	return false
}

// N天前符合 是组合型买入条件。
// 当且仅当被包裹的 Buyer 在 [From, To] 个交易日前（含两端）的任意一天评估返回 true 时触发。
// From 默认 5；To 默认 0 表示上界不限（一直回溯到数据起点）。
// 当 To > From 时，扫描范围为 [len-To, len-From]；To <= From 时退化为无上界约束。
// 例如 buy.N天前符合{From: 10, To: 20, Buyer: buy.A倍量{}} 表示"10~20 个交易日前出现过倍量买点"。
type N天前符合 struct {
	From  int // 起点：最近第几天前（含），默认 5
	To    int // 终点：最远第几天前（含），0 表示不限
	Buyer core.Buyer
}

func (b N天前符合) Name() string {
	from := b.From
	if from == 0 {
		from = 5
	}
	inner := "Null"
	if b.Buyer != nil {
		inner = b.Buyer.Name()
	}
	if b.To > from {
		return fmt.Sprintf("%d-%d天前符合(%s)", from, b.To, inner)
	}
	return fmt.Sprintf("%d天前符合(%s)", from, inner)
}

func (b N天前符合) Buy(code string, dks extend.Klines) bool {
	if b.Buyer == nil {
		return false
	}
	from := b.From
	if from == 0 {
		from = 5
	}
	n := len(dks)
	end := n - from
	if end < 1 {
		return false
	}
	// 计算下界（最远的扫描位置，向旧方向）
	low := 0
	if b.To > from && n-b.To > low {
		low = n - b.To
	}
	for i := end; i >= low+1; i-- {
		if b.Buyer.Buy(code, dks[:i]) {
			return true
		}
	}
	return false
}
