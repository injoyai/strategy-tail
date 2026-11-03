package buy

import (
	"fmt"

	"github.com/injoyai/strategy-tail/lib/extend"
	f "github.com/injoyai/strategy-tail/strategies/factor"
	"github.com/injoyai/strategy-tail/strategies/rule"
	"github.com/injoyai/tdx/v2/protocol"
)

func A价格小于(f float64) A价格 {
	return A价格{Max: f}
}

func A价格大于(f float64) A价格 {
	return A价格{Min: f}
}

type A现价 = A价格

// A价格 是价格区间过滤条件。
// Min 表示允许买入的最低价格，单位为元；Min 为 0 时不限制最低价格。
// Max 表示允许买入的最高价格，单位为元；Max 为 0 时不限制最高价格。
// 判断对象是最新交易日的收盘价，满足价格区间后返回买入信号。
// 适合作为 BuyAll 中的价格过滤条件，与其它形态、均线、成交量条件组合使用。
type A价格 struct {
	Min float64
	Max float64
}

func (b A价格) Name() string {
	switch {
	case b.Min > 0 && b.Max > 0:
		return fmt.Sprintf("价格%.1f-%.1f元", b.Min, b.Max)
	case b.Min > 0:
		return fmt.Sprintf("价格大于%.1f元", b.Min)
	case b.Max > 0:
		return fmt.Sprintf("价格小于%.1f元", b.Max)
	default:
		return "价格范围买入"
	}
}

func (b A价格) Buy(code string, dks extend.Klines) bool {
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
	// 主体判断经共用内核：今收（浮点元）落在 [Min,Max]（含端点）。
	rng, err := rule.NewRange(rule.CloseFloat(), minV, maxV, true, true)
	if err != nil {
		return false // Max<Min 的历史退化为恒不触发
	}
	return rng.Eval(rule.NewInput(code, dks, nil, nil))
}

// A过滤涨停 是过滤涨停买入条件。
// 当最新交易日涨幅达到或超过 MaxRiseRate 时返回 false，避免回测买入实际无法成交的涨停股票。
// 当前主程序只筛选 sh60 和 sz00，默认值按常见 10% 涨停制度设置；如需适配 ST 或 20cm 股票，可按需调整 MaxRiseRate。
type A过滤涨停 struct{}

func (b A过滤涨停) Name() string {
	return "过滤涨停"
}

func (b A过滤涨停) Buy(code string, dks extend.Klines) bool {
	if len(dks) == 0 {
		return false
	}

	code = protocol.AddPrefix(code)

	if len(code) != 8 {
		return false
	}

	threshold, ok := p2涨停阈值(code)
	if !ok {
		return false
	}
	// 涨幅百分比比较经共用内核：单日涨幅 < 板块阈值。
	c, err := rule.NewCompare(rule.ClosePrice(), rule.Lt, p2涨停价格(threshold))
	if err != nil {
		return false
	}
	return c.Eval(rule.NewInput(code, dks, nil, nil))
}

// p2涨停阈值 按代码板块返回涨停判定阈值（%）：主板 9.8，创业/科创/北证 19.8；
// 其余代码不属于可判定板块，ok 为 false。代码前缀分类是原生谓词，保留在门面。
func p2涨停阈值(code string) (float64, bool) {
	switch code[:4] {
	case "sh60", "sz00":
		return 9.8, true
	case "sh68", "sz30", "bj92":
		return 19.8, true
	default:
		return 0, false
	}
}

// A涨停 是过滤涨停买入条件。
// 当最新交易日涨幅达到或超过 MaxRiseRate 时返回 false，避免回测买入实际无法成交的涨停股票。
// 当前主程序只筛选 sh60 和 sz00，默认值按常见 10% 涨停制度设置；如需适配 ST 或 20cm 股票，可按需调整 MaxRiseRate。
type A涨停 struct{}

func (b A涨停) Name() string {
	return "过滤涨停"
}

func (b A涨停) Buy(code string, dks extend.Klines) bool {
	if len(dks) == 0 {
		return false
	}

	code = protocol.AddPrefix(code)

	if len(code) != 8 {
		return false
	}

	threshold, ok := p2涨停阈值(code)
	if !ok {
		return false
	}
	// 涨幅百分比比较经共用内核：单日涨幅 >= 板块阈值。
	c, err := rule.NewCompare(rule.ClosePrice(), rule.Ge, p2涨停价格(threshold))
	if err != nil {
		return false
	}
	return c.Eval(rule.NewInput(code, dks, nil, nil))
}

// A单日涨幅小于 是当日涨幅小于指定值的买入条件。
// Max 表示最大允许涨幅（%），默认 9.5。
// 用于过滤接近涨停的股票。
type A单日涨幅小于 float64

func (b A单日涨幅小于) Name() string {
	max := b
	if max == 0 {
		max = 9.5
	}
	return fmt.Sprintf("涨幅小于%.1f%%", max)
}

func (b A单日涨幅小于) Buy(code string, dks extend.Klines) bool {
	max := float64(b)
	if max == 0 {
		max = 9.5
	}
	if len(dks) == 0 {
		return false
	}
	return (f.A单日涨幅{}).Value(code, dks) < max
}

// A近N日涨幅小于 是近N日累计涨幅小于指定值的买入条件。
// Days 表示统计天数，默认 5。
// Max 表示最大允许累计涨幅（%），默认 15。
// 用于过滤短期暴涨追高风险。
type A近N日涨幅小于 struct {
	Days int
	Max  float64
}

func (b A近N日涨幅小于) Name() string {
	days := b.Days
	if days == 0 {
		days = 5
	}
	max := b.Max
	if max == 0 {
		max = 15
	}
	return fmt.Sprintf("近%d日涨幅<%.0f%%", days, max)
}

func (b A近N日涨幅小于) Buy(code string, dks extend.Klines) bool {
	days := b.Days
	if days == 0 {
		days = 5
	}
	max := b.Max
	if max == 0 {
		max = 15
	}
	// 判断委托共用内核：GainPercent 与旧 riseRateNDays 公式逐位一致
	// （(今收-起点收盘)/起点收盘*100，起点取 len-1-days 位置收盘），
	// 数据不足或起点收盘非正时无效值不触发，与旧 ok=false 保守口径
	// 一致；差异注记：旧 days<=-2 会索引越界 panic，此处构造期校验
	// 返回 false。
	gain, err := rule.GainPercent(days)
	if err != nil {
		return false
	}
	cond, err := rule.NewCompare(gain, rule.Lt, rule.Literal(rule.FloatValue(max)))
	if err != nil {
		return false
	}
	return cond.Eval(rule.NewInput(code, dks, nil, nil))
}

type A单日涨幅范围 struct {
	Min float64
	Max float64
}

func (b A单日涨幅范围) Name() string {
	switch {
	case b.Min != 0 && b.Max != 0:
		return fmt.Sprintf("涨幅%.1f%%-%.1f%%", b.Min, b.Max)
	case b.Min != 0:
		return fmt.Sprintf("涨幅大于%.1f%%", b.Min)
	case b.Max != 0:
		return fmt.Sprintf("涨幅小于%.1f%%", b.Max)
	default:
		return "涨幅范围买入"
	}
}

func (b A单日涨幅范围) Buy(code string, dks extend.Klines) bool {
	// 参数归一（唯一来源）：Min/Max 为 0 表示该侧不限制（`!=0` 才判，
	// 负下限/上限均生效），映射为结构性无界 ±1e9 等价。
	var minV, maxV *rule.Value
	if b.Min != 0 {
		v := rule.FloatValue(b.Min)
		minV = &v
	}
	if b.Max != 0 {
		v := rule.FloatValue(b.Max)
		maxV = &v
	}
	if minV == nil && maxV == nil {
		// 旧退化语义：两侧均不限时，有行情即通过（显式常量谓词）。
		return rule.Const(len(dks) > 0).Eval(rule.NewInput(code, dks, nil, nil))
	}
	// 主体判断经共用内核：当日涨幅（f.A单日涨幅 同源）落在 [Min,Max]
	//（含端点）。计划 §3.4 非有限值不触发：Last=0 且 Open=0 的畸形
	// 数据下涨幅为 NaN/+Inf，旧实现回 true，迁移后为 false。
	rng, err := rule.NewRange(rule.RiseRate(), minV, maxV, true, true)
	if err != nil {
		return false // Max<Min 的历史退化为恒不触发
	}
	return rng.Eval(rule.NewInput(code, dks, nil, nil))
}

// A突破N天新高 是当日最高价创近 N 个交易日新高的买入条件。
// N 表示回看窗口长度（含今天），默认 20。
// 触发条件：dks[n-1].High == HHV(High, N)，即今日最高价等于近 N 天最高价。
// 适合捕捉突破形态，作为趋势确认/突破入场过滤条件使用。
type A突破N天新高 int

func (b A突破N天新高) Name() string {
	days := int(b)
	if days == 0 {
		days = 20
	}
	return fmt.Sprintf("创近%d天新高", days)
}

func (b A突破N天新高) Buy(code string, dks extend.Klines) bool {
	days := int(b)
	if days == 0 {
		days = 20
	}
	if len(dks) < days {
		return false
	}
	today := dks[len(dks)-1]
	return today.High >= f.A最高价(days).Price(dks)
}

// A近N天最高价 是当前收盘价等于近 N 个交易日最高价的买入条件。
// N 表示回看窗口长度（含今天），默认 20。
// 触发条件：dks[n-1].Close >= HHV(High, N)，即今日收盘价达到近 N 天的最高价。
// 与 A突破N天新高 的区别：A突破N天新高 用当日最高价判断，A近N天最高价 用收盘价判断，信号更保守。
type A近N天最高价 int

func (b A近N天最高价) Name() string {
	days := int(b)
	if days == 0 {
		days = 20
	}
	return fmt.Sprintf("收盘价创近%d天最高", days)
}

func (b A近N天最高价) Buy(code string, dks extend.Klines) bool {
	days := int(b)
	if days == 0 {
		days = 20
	}
	if len(dks) < days {
		return false
	}
	today := dks[len(dks)-1]
	return today.Close >= f.A最高价(days).Price(dks)
}

func p2涨停价格(threshold float64) rule.Operand {
	return rule.OperandFunc("涨停判定价格", func(in rule.Input) rule.Value {
		ks := in.Klines
		if len(ks) == 0 {
			return rule.InvalidValue()
		}
		base := ks[len(ks)-1].Last
		if base == 0 {
			base = ks[len(ks)-1].Open
		}
		if base == 0 {
			return rule.InvalidValue()
		}
		return rule.PriceValue(base * (1 + threshold/100))
	})
}
