package buy

import (
	"fmt"

	"github.com/injoyai/strategy-tail/lib/extend"
	"github.com/injoyai/strategy-tail/strategies/rule"
)

func A流通市值小于(f float64) A流通市值 {
	return A流通市值{Max: f}
}

type A流通市值 struct {
	Min float64 //亿元
	Max float64 //亿元
}

func (b A流通市值) Name() string {
	switch {
	case b.Min > 0 && b.Max > 0:
		return fmt.Sprintf("流通市值[%.f,%.f]亿", b.Min, b.Max)
	case b.Min > 0:
		return fmt.Sprintf("流通市值[%.f,]亿", b.Min)
	case b.Max > 0:
		return fmt.Sprintf("流通市值[,%.f]亿", b.Max)
	default:
		return "Null"
	}
}

func (b A流通市值) Buy(code string, dks extend.Klines) bool {
	// 参数归一（唯一来源）：Min/Max<=0 表示该侧不限制，映射为结构性无界。
	var minV, maxV *rule.Value
	if b.Min > 0 {
		v := rule.FloatValue(b.Min)
		minV = &v
	}
	if b.Max > 0 {
		v := rule.FloatValue(b.Max)
		maxV = &v
	}
	if minV == nil && maxV == nil {
		// 旧退化语义：两侧均不限时，有行情即通过（显式常量谓词）。
		return rule.Const(len(dks) > 0).Eval(rule.NewInput(code, dks, nil, nil))
	}
	// 主体判断经共用内核：流通市值（亿元，经 factor）落在 [Min,Max]（含端点）。
	rng, err := rule.NewRange(rule.MarketCap(), minV, maxV, true, true)
	if err != nil {
		return false // Max<Min 的历史退化为恒不触发
	}
	return rng.Eval(rule.NewInput(code, dks, nil, nil))
}
