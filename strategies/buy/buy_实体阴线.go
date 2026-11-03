package buy

import (
	"fmt"

	"github.com/injoyai/strategy-tail/lib/extend"
	f "github.com/injoyai/strategy-tail/strategies/factor"
	"github.com/injoyai/strategy-tail/strategies/rule"
)

// 实体阴线收阴规则 今开 > 今收（浮点价格严格比较），即当天收阴。
var 实体阴线收阴规则 = mustCompare(rule.OpenPrice(), rule.Gt, rule.ClosePrice())

// 实体阴线占比操作数 实体占振幅比例：数值计算委托 factor.A实体振幅占比，
// 不复制公式；NaN/±Inf 经 FloatValue 归为无效值，比较不触发（不补 0）。
func 实体阴线占比操作数() rule.Operand {
	fa := f.A实体振幅占比{}
	return rule.OperandFunc(fa.Name(), func(in rule.Input) rule.Value {
		return rule.FloatValue(fa.Value(in.Code, in.Klines))
	})
}

// A实体阴线 是"有实体的阴线"K 线形态买入条件。
//
// 形态：当天收阴（收盘价 < 开盘价），且阴线实体占当天振幅的比例达到
// MinBodyRatio，用于过滤上下影线过长的十字星式阴线。
// 常与 MACD连涨 组合，表达"量柱趋势向上时出现实体阴线洗盘"的博弈买点：
//
//	buy.And{ buy.MACD连涨{MinDays: 2}, buy.A实体阴线{MinBodyRatio: 0.5} }
//
// 触发条件：
//  1. 当天收阴：收盘价 < 开盘价；
//  2. 实体比例：(开盘价 - 收盘价) / (最高价 - 最低价) ≥ MinBodyRatio。
//     MinBodyRatio <= 0 表示只要收阴即可（不校验实体比例）。
//
// 判断委托共用规则内核（strategies/rule）：收阴为浮点价格比较；
// 实体占比为 factor 数值经操作数与阈值（Ge）比较。"MinBodyRatio<=0
// 只要收阴"的退化语义保留在本门面，不经内核代答。
type A实体阴线 struct {
	MinBodyRatio float64 // 阴线实体占振幅最小比例，<=0 表示只要收阴即可
}

func (s A实体阴线) Name() string {
	if s.MinBodyRatio > 0 {
		return fmt.Sprintf("实体阴线(实体≥%.0f%%)", s.MinBodyRatio*100)
	}
	return "实体阴线"
}

func (s A实体阴线) Buy(code string, dks extend.Klines) bool {
	// 条件1：当天收阴（今开 > 今收，严格比较；空 K 线时为无效值不触发）
	if !实体阴线收阴规则.Eval(rule.NewInput(code, dks, nil, nil)) {
		return false
	}

	// 条件2：实体比例过滤十字星；<=0 退化为只要收阴即可
	if s.MinBodyRatio <= 0 {
		return true
	}
	占比, err := rule.NewCompare(实体阴线占比操作数(), rule.Ge, rule.Literal(rule.FloatValue(s.MinBodyRatio)))
	if err != nil {
		return false
	}
	return 占比.Eval(rule.NewInput(code, dks, nil, nil))
}
