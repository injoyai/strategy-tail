package sell

import (
	"time"

	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/lib/extend"
	"github.com/injoyai/strategy-tail/strategies/rule"
)

// A买入次日未高开 是买入次日未高开的卖出条件。
// 触发条件：今日是买入日的下一个交易日，且今日开盘价 <= 昨日收盘价（即没有高开）。
// 注：dks 只包含交易日 K 线，按 dks[n-2] 定位"昨天"，避免周末/节假日错位。
type A买入次日未高开 struct{}

func (s A买入次日未高开) Name() string {
	return "买入次日未高开"
}

func (s A买入次日未高开) Sell(code string, dks extend.Klines, buy core.Buy) bool {
	// 判断委托共用内核 And 组合：昨为买入日 且 今开(厘) <= 昨收(厘)。
	// 昨日定位经规则级逃逸口表达，沿用旧口径日期串比较（计划第 4 节
	// 不统一各组件定位算法）；价格比较为 protocol.Price 厘级 int64
	// 精确比较，与旧 today.Open <= yesterday.Close 一致；n<2 或持仓
	// 不可用不触发（昨收不足 2 根同样无效），等价旧 n<2 预检查。
	isBuyDay, err := rule.NewRuleFunc("昨为买入日", func(in rule.Input) bool {
		n := len(in.Klines)
		if n < 2 {
			return false
		}
		pos, ok := in.Position()
		if !ok {
			return false
		}
		return in.Klines[n-2].Time.Format(time.DateOnly) == pos.Time.Format(time.DateOnly)
	})
	if err != nil {
		return false
	}
	c, err := rule.NewCompare(rule.OpenPrice(), rule.Le, rule.PrevClosePrice())
	if err != nil {
		return false
	}
	r, err := rule.NewAnd(isBuyDay, c)
	if err != nil {
		return false
	}
	return r.Eval(rule.NewInput(code, dks, nil, &buy))
}
