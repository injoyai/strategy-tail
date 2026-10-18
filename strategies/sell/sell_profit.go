package sell

import (
	"fmt"

	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/lib/extend"
	"github.com/injoyai/strategy-tail/strategies/rule"
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
	// 判断委托共用内核：持仓收益率 >= 阈值（恰等触发）。旧 len==0 与
	// buy<=0 预检查由收益率无效值等价表达：持仓不可用、买入价非正或
	// 空前缀均无效不触发；公式次序 (今收-买入价)/买入价 与旧实现一致。
	in := rule.Input{Code: code, Klines: dks, Buy: &b}
	return rule.EvalCompare(rule.PositionProfitPercent().Value(in), rule.Ge, rule.FloatValue(pct))
}
