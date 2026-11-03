package rule

import (
	"math"
	"strconv"
)

// Kind 值的值域。比较只在同值域内进行：跨域不触发、不转换，
// 价格与百分比等数值均以 float64 保存，但保留语义域，防止单位混用。
type Kind uint8

const (
	// KindInvalid 无效值：缺数据、NaN、±Inf。不参与任何比较、不补 0。
	KindInvalid Kind = iota
	// KindPrice 行情价格，单位为证券原币种值。
	KindPrice
	// KindFloat 浮点值：元、比率、百分数、计数等，按 float64 比较。
	KindFloat
)

// String 返回值域的可读名称，用于构造期错误信息。
func (k Kind) String() string {
	switch k {
	case KindInvalid:
		return "invalid"
	case KindPrice:
		return "price"
	case KindFloat:
		return "float"
	}
	return "unknown"
}

// Value 规则比较中的单个值。零值即 KindInvalid（不触发），无需单独构造。
// 字段不导出：值只能经 PriceValue/FloatValue/InvalidValue 构造，
// 保证 NaN/±Inf 一律落为 KindInvalid，比较侧不必重复防御。
type Value struct {
	kind  Kind
	price float64
	num   float64
}

// PriceValue 以浮点元价格构造值，不与百分比等数值隐式转换。
func PriceValue(p float64) Value {
	if math.IsNaN(p) || math.IsInf(p, 0) {
		return InvalidValue()
	}
	return Value{kind: KindPrice, price: p}
}

// FloatValue 以浮点构造值；NaN 或 ±Inf 归为无效值（不触发比较、不补 0）。
func FloatValue(f float64) Value {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return Value{kind: KindInvalid}
	}
	return Value{kind: KindFloat, num: f}
}

// InvalidValue 返回无效值，用于缺数据等不可求值场合。
func InvalidValue() Value {
	return Value{kind: KindInvalid}
}

// Kind 返回值域。
func (v Value) Kind() Kind { return v.kind }

// Valid 报告值是否可参与比较。
func (v Value) Valid() bool { return v.kind != KindInvalid }

// Price 返回浮点元价格；非 KindPrice 时返回 0。
func (v Value) Price() float64 { return v.price }

// Float 返回浮点值；非 KindFloat 时返回 NaN。
func (v Value) Float() float64 { return v.num }

// String 返回可读形式，用于构造期错误信息与规则名称。
func (v Value) String() string {
	switch v.kind {
	case KindPrice:
		return strconv.FormatFloat(v.price, 'g', -1, 64)
	case KindFloat:
		return strconv.FormatFloat(v.num, 'g', -1, 64)
	default:
		return "invalid"
	}
}

// compareValues 比较两个值：返回 -1/0/1 与是否可比。
// 不可比（任一侧无效、值域不同、浮点非有限）一律返回 false，
// 调用方必须按"不触发"处理，不得回退为任何有损比较。
func compareValues(a, b Value) (int, bool) {
	if !a.Valid() || !b.Valid() || a.kind != b.kind {
		return 0, false
	}
	switch a.kind {
	case KindPrice:
		switch {
		case a.price < b.price:
			return -1, true
		case a.price > b.price:
			return 1, true
		}
		return 0, true
	case KindFloat:
		// 构造入口已拦截 NaN/Inf，这里兜底直构 Value 的误用。
		if math.IsNaN(a.num) || math.IsInf(a.num, 0) ||
			math.IsNaN(b.num) || math.IsInf(b.num, 0) {
			return 0, false
		}
		switch {
		case a.num < b.num:
			return -1, true
		case a.num > b.num:
			return 1, true
		}
		return 0, true
	}
	return 0, false
}
