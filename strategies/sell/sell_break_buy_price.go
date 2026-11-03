package sell

import (
	"time"

	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/lib/extend"
	"github.com/injoyai/strategy-tail/strategies/rule"
)

// A买入次日跌破买入价 是买入次日跌破买入价的卖出条件。
// 触发条件：当前交易日是买入日的下一个交易日，且今日收盘价 < 买入价。
// 注：dks 只包含交易日 K 线，按日期比较判定"次日"。
type A买入次日跌破买入价 struct{}

func (s A买入次日跌破买入价) Name() string {
	return "买入次日跌破买入价"
}

func (s A买入次日跌破买入价) Sell(code string, dks extend.Klines, buy core.Buy) bool {
	// 判断委托共用内核 And 组合：昨为买入日 且 今收(元) < 买入价(元)。
	// 昨日定位经规则级逃逸口表达，沿用旧口径日期串比较（计划第 4 节
	// 不统一各组件定位算法），n<2 或持仓不可用不触发；价格比较为
	// float64 原币种 精确比较，与旧 today.Close < buy.Price
	// 一致；买入价不做非正守卫（旧实现无）。
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
	c, err := rule.NewCompare(rule.ClosePrice(), rule.Lt, rule.PositionBuyPrice())
	if err != nil {
		return false
	}
	r, err := rule.NewAnd(isBuyDay, c)
	if err != nil {
		return false
	}
	return r.Eval(rule.NewInput(code, dks, nil, &buy))
}
