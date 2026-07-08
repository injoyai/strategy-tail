package sell

import (
	"fmt"

	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/lib/extend"
)

// A盈利 是当前盈利达到 N 即卖出的基础止盈条件。
// Pct 表示止盈百分比（小数），默认 0.05（5%）。
// 触发条件：(当前收盘价 - 买入价) / 买入价 >= Pct
type A盈利 float64

func (s A盈利) Name() string {
	pct := float64(s)
	if pct == 0 {
		pct = 0.05
	}
	return fmt.Sprintf("盈利%.1f%%", pct*100)
}

func (s A盈利) Sell(code string, dks extend.Klines, b core.Buy) bool {
	pct := float64(s)
	if pct == 0 {
		pct = 0.05
	}
	if len(dks) == 0 || b.Price.Float64() <= 0 {
		return false
	}
	profit := (dks[len(dks)-1].Close.Float64() - b.Price.Float64()) / b.Price.Float64()
	return profit >= pct
}
