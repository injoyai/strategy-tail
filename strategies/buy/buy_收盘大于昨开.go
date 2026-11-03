package buy

import (
	"github.com/injoyai/strategy-tail/lib/extend"
	"github.com/injoyai/strategy-tail/strategies/rule"
)

// 收盘大于昨开规则 今收 > 昨开：两侧均取浮点元（float64）精确比较；
// 昨开不足 2 根时为无效值，规则不触发。
var 收盘大于昨开规则 = mustCompare(rule.ClosePrice(), rule.Gt, rule.PrevOpenPrice())

// A收盘大于昨开 是"当日收盘价大于昨日开盘价"买入条件。
//
// 用途：作为附加过滤条件，要求今天收盘站上昨日开盘价之上，
// 表达"多头回补了昨日跳空/实体区间"的偏多确认。常与其他条件组合：
//
//	buy.And{ buy.MACD连涨{MinDays: 2}, buy.A收盘大于昨开{} }
//
// 触发条件：当日收盘价严格大于昨日开盘价（相等不触发）。
// 判断委托共用规则内核（strategies/rule），本门面不再自持实现。
type A收盘大于昨开 struct{}

func (s A收盘大于昨开) Name() string { return "收盘>昨开" }

func (s A收盘大于昨开) Buy(code string, dks extend.Klines) bool {
	return 收盘大于昨开规则.Eval(rule.NewInput(code, dks, nil, nil))
}
