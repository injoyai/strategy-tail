package rule

import (
	"fmt"
	"strings"
)

// Rule 规则谓词：给定一次求值输入，返回是否成立。
// 实现必须构造期校验、构造后不可变，Eval 无副作用、无跨调用缓存；
// 无效输入（缺数据、值域不匹配）一律返回 false，不 panic。
type Rule interface {
	Name() string
	Eval(in Input) bool
}

// Op 比较操作符。
type Op uint8

const (
	Lt Op = iota // left <  right
	Le           // left <= right
	Gt           // left >  right
	Ge           // left >= right
	Eq           // left == right（同值域精确相等；浮点阈值边界一般用 Ge/Le）
)

func (op Op) String() string {
	switch op {
	case Lt:
		return "<"
	case Le:
		return "<="
	case Gt:
		return ">"
	case Ge:
		return ">="
	case Eq:
		return "=="
	}
	return "?"
}

// validOp 报告 op 是否为已定义操作符（拦截字面量直构的非法 Op）。
func (op Op) validOp() bool { return op <= Eq }

// Compare 单次比较谓词：left (op) right。
// 两侧操作数任一无效或值域不同时不触发（返回 false），
// 不发生跨值域转换；阈值相等边界按操作符语义严格判定
// （如 Ge 对 value==threshold 返回 true，Gt 返回 false）。
type Compare struct {
	Left  Operand
	Op    Op
	Right Operand
}

// NewCompare 构造比较谓词，构造期校验操作数与操作符。
func NewCompare(left Operand, op Op, right Operand) (Compare, error) {
	if left == nil || right == nil {
		return Compare{}, fmt.Errorf("比较规则的操作数不能为空")
	}
	if !op.validOp() {
		return Compare{}, fmt.Errorf("比较操作符无效: %d", op)
	}
	return Compare{Left: left, Op: op, Right: right}, nil
}

func (c Compare) Name() string {
	left, right := "?", "?"
	if c.Left != nil {
		left = c.Left.Name()
	}
	if c.Right != nil {
		right = c.Right.Name()
	}
	return left + " " + c.Op.String() + " " + right
}

func (c Compare) Eval(in Input) bool {
	if c.Left == nil || c.Right == nil || !c.Op.validOp() {
		return false
	}
	return EvalCompare(c.Left.Value(in), c.Op, c.Right.Value(in))
}

// EvalCompare 比较已求得的值，供标量热路径复用同一边界语义，避免构造操作数。
func EvalCompare(left Value, op Op, right Value) bool {
	cmp, ok := compareValues(left, right)
	if !ok {
		return false
	}
	switch op {
	case Lt:
		return cmp < 0
	case Le:
		return cmp <= 0
	case Gt:
		return cmp > 0
	case Ge:
		return cmp >= 0
	case Eq:
		return cmp == 0
	}
	return false
}

// Range 区间谓词：操作数值落在 [min, max] 内，两侧边界包含性分别可配。
// Min/Max 为 nil 表示该侧结构性无界——这是表达旧组件"单边界限 ±1e9"与
// "Max=0 上限不生效"退化语义的正式能力（由 T2 门面负责把旧参数映射为
// 结构性无界，内核不内置哨兵值与默认值替换）。
// 无效操作数值不触发；操作数值域与边界值域不同时不触发（无有损转换）。
type Range struct {
	Value Operand
	// Min 下限，nil 表示无下界；必须为有效值（非 NaN/Inf）。
	Min *Value
	// Max 上限，nil 表示无上界；必须为有效值（非 NaN/Inf）。
	Max          *Value
	MinInclusive bool // true: value >= min（含下边界）；false: value > min
	MaxInclusive bool // true: value <= max（含上边界）；false: value < max
}

// NewRange 构造区间谓词，构造期校验：
//   - 操作数与两侧边界不得为 nil；
//   - 两侧均无界没有判定意义，拒绝构造（恒真请用 Const(true)）；
//   - 边界必须为有效值（KindInvalid/NaN/Inf 拒绝）；
//   - min 与 max 值域必须一致，且 min <= max。
//     旧组件 Max=0 或 Max<Min 的"上限不生效"退化语义由门面在构造前
//     映射为 Max=nil（结构性无界），内核不做隐式退化，避免掩盖门面错误。
func NewRange(value Operand, min, max *Value, minInclusive, maxInclusive bool) (Range, error) {
	if value == nil {
		return Range{}, fmt.Errorf("区间规则的操作数不能为空")
	}
	if min == nil && max == nil {
		return Range{}, fmt.Errorf("区间两侧均无界，拒绝构造（恒真请用常量谓词）")
	}
	for _, b := range []struct {
		name  string
		bound *Value
	}{{"下限", min}, {"上限", max}} {
		if b.bound == nil {
			continue
		}
		if !b.bound.Valid() {
			return Range{}, fmt.Errorf("区间%s为无效值（NaN/Inf 不能作边界）", b.name)
		}
	}
	if min != nil && max != nil {
		if min.kind != max.kind {
			return Range{}, fmt.Errorf("区间上下限值域不一致: %s(%s) vs %s(%s)",
				min, min.kind, max, max.kind)
		}
		if cmp, ok := compareValues(*min, *max); !ok || cmp > 0 {
			return Range{}, fmt.Errorf("区间下限 %s 不能大于上限 %s", min, max)
		}
	}
	return Range{
		Value:        value,
		Min:          min,
		Max:          max,
		MinInclusive: minInclusive,
		MaxInclusive: maxInclusive,
	}, nil
}

func (r Range) Name() string {
	val := "?"
	if r.Value != nil {
		val = r.Value.Name()
	}
	left, leftBracket := "-∞", "("
	if r.Min != nil {
		left = r.Min.String()
	}
	if r.MinInclusive {
		leftBracket = "["
	}
	right, rightBracket := "+∞", ")"
	if r.Max != nil {
		right = r.Max.String()
	}
	if r.MaxInclusive {
		rightBracket = "]"
	}
	return val + "∈" + leftBracket + left + "," + right + rightBracket
}

func (r Range) Eval(in Input) bool {
	if r.Value == nil {
		return false
	}
	return r.contains(r.Value.Value(in))
}

// contains 供已取得因子值的热路径复用区间判断，避免构造操作数及闭包。
func (r Range) contains(v Value) bool {
	if !v.Valid() {
		return false
	}
	if r.Min != nil {
		cmp, ok := compareValues(v, *r.Min)
		if !ok {
			return false
		}
		if r.MinInclusive && cmp < 0 {
			return false
		}
		if !r.MinInclusive && cmp <= 0 {
			return false
		}
	}
	if r.Max != nil {
		cmp, ok := compareValues(v, *r.Max)
		if !ok {
			return false
		}
		if r.MaxInclusive && cmp > 0 {
			return false
		}
		if !r.MaxInclusive && cmp >= 0 {
			return false
		}
	}
	return true
}

// And 全部子规则成立才成立。短路求值，保持左到右顺序：
// 首个不成立的子规则之后的子规则不再求值（顺序可由测试观测）。
// 新内核构造期拒绝空组与 nil 子项（计划 3.4）；旧 buy.And 的
// 空/nil 跳过语义保留在旧门面，不由内核模仿。
// 零值直构（空 And）求值恒 false，仅作为防御，正常代码应走 NewAnd。
type And []Rule

// NewAnd 构造 And 组，空组与 nil 子项拒绝构造。
func NewAnd(rules ...Rule) (And, error) {
	if len(rules) == 0 {
		return nil, fmt.Errorf("And 组不能为空")
	}
	for i, r := range rules {
		if r == nil {
			return nil, fmt.Errorf("And 子项 %d 为空", i)
		}
	}
	return And(rules), nil
}

func (a And) Name() string {
	return joinRuleNames(a, " & ")
}

func (a And) Eval(in Input) bool {
	seen := false
	for _, r := range a {
		if r == nil {
			continue
		}
		seen = true
		if !r.Eval(in) {
			return false
		}
	}
	return seen
}

// Or 任一子规则成立即成立。短路求值，保持左到右顺序：
// 首个成立的子规则之后的子规则不再求值。
// 单个叶子的无效值只让该叶子不触发，不影响其它叶子
// （计划 3.4：不存在"任一叶缺数据整棵树失败"的全局规则）。
type Or []Rule

// NewOr 构造 Or 组，空组与 nil 子项拒绝构造。
func NewOr(rules ...Rule) (Or, error) {
	if len(rules) == 0 {
		return nil, fmt.Errorf("Or 组不能为空")
	}
	for i, r := range rules {
		if r == nil {
			return nil, fmt.Errorf("Or 子项 %d 为空", i)
		}
	}
	return Or(rules), nil
}

func (o Or) Name() string {
	return joinRuleNames(o, " | ")
}

func (o Or) Eval(in Input) bool {
	for _, r := range o {
		if r == nil {
			continue
		}
		if r.Eval(in) {
			return true
		}
	}
	return false
}

// constRule 显式常量谓词：恒真/恒假。用于"显式全部买入"等
// 原生谓词的兜底表达与测试。
type constRule bool

// Const 构造显式常量谓词。
func Const(v bool) Rule { return constRule(v) }

func (c constRule) Name() string {
	if c {
		return "true"
	}
	return "false"
}

func (c constRule) Eval(Input) bool { return bool(c) }

// joinRuleNames 以分隔符拼接子规则名称（组合规则 Name 共用）。
func joinRuleNames(rules []Rule, sep string) string {
	names := make([]string, 0, len(rules))
	for _, r := range rules {
		if r == nil {
			continue
		}
		names = append(names, r.Name())
	}
	return "[" + strings.Join(names, sep) + "]"
}

// 编译期断言：内核原语满足 Rule 契约。
var (
	_ Rule = Compare{}
	_ Rule = Range{}
	_ Rule = And{}
	_ Rule = Or{}
	_ Rule = constRule(true)
)
