package rule

import (
	"math"
	"strconv"

	"github.com/injoyai/strategy-tail/lib/extend"
	f "github.com/injoyai/strategy-tail/strategies/factor"
)

// 因子操作数胶水（T2 前置）：把 strategies/factor 的现有数值实现包装成
// 内核 Operand，供买卖两侧组件迁移共用，落实"不在买入与卖出两侧重复写公式"。
//
// 约定：
//   - 只委托因子现有实现，不复制任何计算公式；窗口长度、零值默认与
//     NaN 语义全部以因子为准（含 days<=0 时因子自身的默认替换）；
//   - 无效值归一集中在此处：浮点结果一律经 FloatValue（NaN/±Inf → 无效值），
//     不补 0；价格域因子缺数据返回 0，与真实 0 价无法区分，故 priceOperand
//     以同因子的浮点口径（NaN=缺数据）判定有效性后再取价格值；
//   - 组件合同守卫不放胶水："MA<=0 不触发"这类守卫由门面用
//     NewCompare(MAValue(p), Gt, Literal(FloatValue(0))) 与主比较 NewAnd
//     组合表达——无效值同样让守卫不成立，语义等价且在规则名中可见；
//   - Name() 仅为诊断用的稳定中文名，不进任何持久化合同。

// OperandFunc 函数操作数逃逸口：为暂无专用构造器的取值（如 T3/T4 的
// 历史引用、持仓指标）提供兜底入口。name 为稳定中文诊断名；fn 在每次
// 求值时调用，必须只读 Input、无副作用、无跨调用缓存。fn 为 nil 时
// 求值返回无效值（仅防御直构，正常代码不传入）。
func OperandFunc(name string, fn func(Input) Value) Operand {
	return operandFunc{name: name, fn: fn}
}

type operandFunc struct {
	name string
	fn   func(Input) Value
}

func (o operandFunc) Name() string { return o.name }

func (o operandFunc) Value(in Input) Value {
	if o.fn == nil {
		return InvalidValue()
	}
	return o.fn(in)
}

// factorOperand 因子操作数统一载体：eval 返回的 Value 已完成无效值归一。
type factorOperand struct {
	name string
	eval func(in Input) Value
}

func (o factorOperand) Name() string { return o.name }

func (o factorOperand) Value(in Input) Value { return o.eval(in) }

// floatOperand 包装因子浮点取值：NaN/±Inf 经 FloatValue 统一归为无效值。
func floatOperand(name string, eval func(code string, dks extend.Klines) float64) Operand {
	return factorOperand{name: name, eval: func(in Input) Value {
		return FloatValue(eval(in.Code, in.Klines))
	}}
}

// priceOperand 包装因子价格域取值：价格域因子缺数据返回 0，与真实 0 价
// 无法区分，因此以同因子的浮点口径（NaN=缺数据）判定有效性，有效时才
// 取价格值——窗口与默认值语义仍由因子单方提供，胶水不复制窗口判断。
func priceOperand(
	name string,
	floatEval func(code string, dks extend.Klines) float64,
	priceEval func(code string, dks extend.Klines) float64,
) Operand {
	return factorOperand{name: name, eval: func(in Input) Value {
		if math.IsNaN(floatEval(in.Code, in.Klines)) {
			return InvalidValue()
		}
		return PriceValue(priceEval(in.Code, in.Klines))
	}}
}

// prior 返回因子求值前缀：含今日原样返回；不含今日去掉末根
// （空前缀原样返回，由因子自身的窗口检查产生无效值）。
func prior(dks extend.Klines, includeToday bool) extend.Klines {
	if includeToday || len(dks) == 0 {
		return dks
	}
	return dks[:len(dks)-1]
}

// windowName 为历史极值窗口生成可区分的诊断名（买卖两侧含/不含今日
// 口径相反，台账风险 §5.5，必须在名称中可见）。
func windowName(includeToday bool, factorName string) string {
	if includeToday {
		return "含今日·" + factorName
	}
	return "不含今日·" + factorName
}

// MAValue N 日简单均线（浮点元）。委托 factor.A均线，0 默认 20 日。
func MAValue(days int) Operand {
	ma := f.A均线(days)
	return floatOperand(ma.Name(), ma.Value)
}

// MAPrice N 日简单均线（浮点元，extend 口径 dks.MA(n)）。供价格域比较使用。
func MAPrice(days int) Operand {
	ma := f.A均线(days)
	return priceOperand(ma.Name(), ma.Value, func(_ string, dks extend.Klines) float64 {
		return ma.Price(dks)
	})
}

// MarketCap 流通市值（亿元，浮点）。委托 factor.A流通市值，
// 缺失股本产生的 0 不改写为估算值。
func MarketCap() Operand {
	fc := f.A流通市值{}
	return floatOperand(fc.Name(), fc.Value)
}

// RSIFloat 浮点 RSI（0-100，util.CalcRSI 口径）。买入侧组件使用；
// 与 RSIInt 不同源（台账风险 §5.4），不得混用。
func RSIFloat(days int) Operand {
	fa := f.RSI值(days)
	return floatOperand(fa.Name(), fa.Value)
}

// RSIInt 整数 RSI（extend 0-100 整数口径，尾数截断）。卖出侧
// "RSI恢复"组件使用；与 RSIFloat 数值可差整数个单位。
func RSIInt(days int) Operand {
	fa := f.A整数RSI值(days)
	return floatOperand(fa.Name(), fa.Value)
}

// PrevAvgVolume 前N日均量（不含今日，浮点手数）。委托 factor.A前N日均量。
func PrevAvgVolume(days int) Operand {
	fa := f.A前N日均量(days)
	return floatOperand(fa.Name(), fa.Value)
}

// VolumeRatio 沿用旧倍量条件的浮点除法顺序，避免改成乘法后翻转恰等边界。
func VolumeRatio(days int) Operand {
	base := f.A前N日均量(days)
	return floatOperand("量比", func(code string, dks extend.Klines) float64 {
		avg := base.Value(code, dks)
		if len(dks) == 0 || math.IsNaN(avg) || avg <= 0 {
			return math.NaN()
		}
		return float64(dks[len(dks)-1].Volume) / avg
	})
}

// PrevAvgVolumeScaled 前N日均量 × ratio 的派生值，表达量比阈值类比较的
// 右侧操作数（如 今日量 > 前5日均量×1.5），避免内核引入算术 DSL。
// 基准 <= 0 时返回无效值：零量或缺数据没有可比基准，比较不应触发；
// ratio == 0 不在此吞掉——0 乘出 0 是合法浮点值（FloatValue(0)），
// 是否把 0 比例视为"阈值不生效"由门面决定，胶水不代答。
func PrevAvgVolumeScaled(days int, ratio float64) Operand {
	fa := f.A前N日均量(days)
	return factorOperand{
		name: fa.Name() + "×" + strconv.FormatFloat(ratio, 'g', -1, 64),
		eval: func(in Input) Value {
			base := fa.Value(in.Code, in.Klines)
			if math.IsNaN(base) || base <= 0 {
				return InvalidValue()
			}
			return FloatValue(base * ratio)
		},
	}
}

// HighestValue 近 N 日最高价（浮点元）。includeToday 显式声明窗口是否
// 含今日：买卖两侧历史极值口径相反（台账风险 §5.5），胶水不内置单一口径。
func HighestValue(days int, includeToday bool) Operand {
	fa := f.A最高价(days)
	return floatOperand(windowName(includeToday, fa.Name()), func(code string, dks extend.Klines) float64 {
		return fa.Value(code, prior(dks, includeToday))
	})
}

// HighestPrice 近 N 日最高价（浮点元），有效性由浮点口径判定（见 priceOperand）。
func HighestPrice(days int, includeToday bool) Operand {
	fa := f.A最高价(days)
	return priceOperand(windowName(includeToday, fa.Name()),
		func(code string, dks extend.Klines) float64 {
			return fa.Value(code, prior(dks, includeToday))
		},
		func(_ string, dks extend.Klines) float64 {
			return fa.Price(prior(dks, includeToday))
		})
}

// LowestValue 近 N 日最低价（浮点元），窗口开关同 HighestValue。
func LowestValue(days int, includeToday bool) Operand {
	fa := f.A最低价(days)
	return floatOperand(windowName(includeToday, fa.Name()), func(code string, dks extend.Klines) float64 {
		return fa.Value(code, prior(dks, includeToday))
	})
}

// LowestPrice 近 N 日最低价（浮点元），窗口开关同 HighestPrice。
func LowestPrice(days int, includeToday bool) Operand {
	fa := f.A最低价(days)
	return priceOperand(windowName(includeToday, fa.Name()),
		func(code string, dks extend.Klines) float64 {
			return fa.Value(code, prior(dks, includeToday))
		},
		func(_ string, dks extend.Klines) float64 {
			return fa.Price(prior(dks, includeToday))
		})
}

// RiseRate 单日涨幅（%）。委托 factor.A单日涨幅（行情 RiseRate 口径）。
func RiseRate() Operand {
	fa := f.A单日涨幅{}
	return floatOperand(fa.Name(), fa.Value)
}

// ATR 近 N 个真实波幅的简单均值（浮点元），预热需 N+1 根（首个波幅需
// 前一日收盘）。委托 factor.ATR值，窗口口径以因子实现为准。
func ATR(days int) Operand {
	fa := f.ATR值(days)
	return floatOperand(fa.Name(), fa.Value)
}

// BollLower 布林下轨 MA(N)-std×σ（浮点元）。委托 factor.A布林下轨，
// Days/StdTimes 的零值默认替换在因子内完成。
func BollLower(days int, std float64) Operand {
	fa := f.A布林下轨{Days: days, StdTimes: std}
	return floatOperand(fa.Name(), fa.Value)
}

// BiasPercent 乖离率（%），沿用旧组件合同公式 (Close-MA)/MA×100 的原始
// 运算次序（买卖两侧一致），保证与旧门面逐位一致：factor 注册表 "ma_bias"
// 为数学等价的 (Close/MA-1)×100，但浮点末位可差 1-ULP，恰等阈值会翻转
// 比较结果（台账 §2.1 乖离 1-ULP 分歧，T2 对拍量化 23679/99320 组合），
// 迁移期以旧公式为准；注册表因子保持自身口径不动。均线零值语义委托
// factor.A均线（缺数据/空前缀 → NaN → 无效值），MA==0 除法归一为无效值；
// 'MA<=0 不触发'守卫由门面用 NewCompare(MAValue(p), Gt, Literal(FloatValue(0)))
// 表达，胶水不代答。
func BiasPercent(days int) Operand {
	ma := f.A均线(days)
	return floatOperand("乖离率%", func(code string, dks extend.Klines) float64 {
		m := ma.Value(code, dks)
		if math.IsNaN(m) || m == 0 {
			return math.NaN()
		}
		return (dks[len(dks)-1].Close - m) / m * 100
	})
}

// ZScore 收盘价偏离 N 日均值的标准差倍数（浮点）。委托 factor.ZScore值，
// 零方差 → NaN → 无效值。
func ZScore(days int) Operand {
	fa := f.ZScore值(days)
	return floatOperand(fa.Name(), fa.Value)
}
