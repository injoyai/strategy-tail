package buy

import (
	"fmt"
	"math"

	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/lib/extend"
)

// A因子过滤 按因子值闭区间过滤买入：Min <= 因子值 <= Max。
// 单边过滤时另一侧用 ±1e9（不用 0 表示无限制，0 是合法因子值）。
// Min > Max（配反区间）恒 false。Factor 为 nil、空数据或因子返回 NaN 时恒 false。
type A因子过滤 struct {
	Factor core.Factor
	Min    float64
	Max    float64
}

func (b A因子过滤) Name() string {
	if b.Factor == nil {
		return "因子过滤"
	}
	return fmt.Sprintf("%s∈[%.4g,%.4g]", b.Factor.Name(), b.Min, b.Max)
}

func (b A因子过滤) Buy(code string, dks extend.Klines) bool {
	if b.Factor == nil || len(dks) == 0 {
		return false
	}
	v := b.Factor.Value(code, dks)
	if math.IsNaN(v) {
		return false
	}
	return b.Min <= v && v <= b.Max
}
