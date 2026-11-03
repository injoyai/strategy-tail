package buy

import (
	"github.com/injoyai/strategy-tail/lib/extend"
	"github.com/injoyai/strategy-tail/strategies/rule"
)

// mustCompare 构造静态比较规则（P6 批次共用）。操作数与操作符均为编译期
// 常量，失败只可能是编程错误，包初始化阶段 panic 暴露，不静默降级
// （迁移计划第 8 节：已迁移规则构造失败须在启动时报错）。
func mustCompare(left rule.Operand, op rule.Op, right rule.Operand) rule.Compare {
	c, err := rule.NewCompare(left, op, right)
	if err != nil {
		panic(err)
	}
	return c
}

// 收盘大于昨收规则 今收 > 昨收：两侧均取浮点元（float64）精确比较，
// 保留厘级边界，不转浮点；昨收不足 2 根时为无效值，规则不触发。
var 收盘大于昨收规则 = mustCompare(rule.ClosePrice(), rule.Gt, rule.PrevClosePrice())

// A收盘大于昨收 是"当日收盘价大于昨日收盘价"买入条件。
//
// 用途：作为附加过滤条件，要求今天收盘仍高于昨天收盘，
// 表达"阴线回调没有吞掉昨日阳线成果"的强势整理确认。常与其他条件组合：
//
//	buy.And{ buy.MACD连涨{MinDays: 2}, buy.A收盘大于昨收{} }
//
// 触发条件：当日收盘价严格大于昨日收盘价（相等不触发）。
// 判断委托共用规则内核（strategies/rule），本门面不再自持实现。
type A收盘大于昨收 struct{}

func (s A收盘大于昨收) Name() string { return "收盘>昨收" }

func (s A收盘大于昨收) Buy(code string, dks extend.Klines) bool {
	return 收盘大于昨收规则.Eval(rule.NewInput(code, dks, nil, nil))
}
