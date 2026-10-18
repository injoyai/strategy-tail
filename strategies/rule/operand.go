package rule

// Operand 规则比较的取值来源：每次求值从 Input 中取一个 Value
// （因子值、K 线字段、持仓指标或常量）。
// 实现必须无状态、只读输入，不得缓存跨调用结果，不得修改 Input。
type Operand interface {
	Name() string
	Value(in Input) Value
}

// literal 显式常量操作数：阈值与测试固定值。
type literal Value

// Literal 以常量值构造操作数。值在构造时确定，求值时原样返回。
func Literal(v Value) Operand {
	return literal(v)
}

func (l literal) Name() string { return Value(l).String() }

func (l literal) Value(Input) Value { return Value(l) }
