package rule

import (
	"fmt"
	"math"

	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/lib/extend"
)

// NewFactorRange 把旧因子过滤合同编译为闭区间规则，不改写 ±1e9 边界。
// Go 调用可传无穷边界；仅该兼容分支保留 IEEE 比较，普通区间使用 Range。
func NewFactorRange(factor core.Factor, min, max float64) (Rule, error) {
	if factor == nil {
		return nil, fmt.Errorf("因子不能为空")
	}
	if math.IsNaN(min) || math.IsNaN(max) || min > max {
		return nil, fmt.Errorf("因子区间无效")
	}
	if math.IsInf(min, 0) || math.IsInf(max, 0) {
		return NewRuleFunc("因子闭区间（无穷边界兼容）", func(in Input) bool {
			return EvalFactorRange(factor, min, max, in)
		})
	}
	lo, hi := FloatValue(min), FloatValue(max)
	return NewRange(floatOperand("因子值", func(code string, dks extend.Klines) float64 {
		if len(dks) == 0 {
			return math.NaN()
		}
		return factor.Value(code, dks)
	}), &lo, &hi, true, true)
}

// EvalFactorRange 直接求值旧因子过滤，复用 Range 的边界判断，省去逐根构造规则的分配。
func EvalFactorRange(factor core.Factor, min, max float64, in Input) bool {
	if factor == nil || len(in.Klines) == 0 || math.IsNaN(min) || math.IsNaN(max) || min > max {
		return false
	}
	v := factor.Value(in.Code, in.Klines)
	if math.IsInf(min, 0) || math.IsInf(max, 0) {
		return min <= v && v <= max
	}
	lo, hi := FloatValue(min), FloatValue(max)
	r := Range{Min: &lo, Max: &hi, MinInclusive: true, MaxInclusive: true}
	return r.contains(FloatValue(v))
}
