package buy

import (
	"fmt"

	"github.com/injoyai/strategy-tail/lib/extend"
	f "github.com/injoyai/strategy-tail/strategies/factor"
	"github.com/injoyai/strategy-tail/strategies/rule"
)

// A收盘高于均线 是收盘价高于指定均线的买入条件。
// Period 表示均线周期，默认 20。
// 与 BuyCloseAboveMA 等价，采用 A+中文 命名风格。
type A收盘高于均线 struct {
	Period int
}

func (b A收盘高于均线) Name() string {
	if b.Period == 0 {
		b.Period = 20
	}
	return fmt.Sprintf("收盘高于%d日均线", b.Period)
}

func (b A收盘高于均线) Buy(code string, dks extend.Klines) bool {
	if b.Period == 0 {
		b.Period = 20
	}
	// 今收(浮点元) > N日均线（extend 整数厘均线）；均线窗口不足时为无效值，
	// 比较不触发（与旧实现 len(dks)<period 预检行为等价）。
	return mustCompare(rule.CloseFloat(), rule.Gt, rule.MAValue(b.Period)).
		Eval(rule.NewInput(code, dks, nil, nil))
}

// A均线向上 是指定均线方向向上的买入条件。
// Period 表示均线周期，默认 20。
// Lookback 表示连续向上的天数，默认 1。
// MinSlope 表示每一步的最小涨速，默认 0。
type A均线向上 struct {
	Period   int
	Lookback int
	MinSlope float64
}

func (b A均线向上) Name() string {
	if b.Period == 0 {
		b.Period = 20
	}
	return fmt.Sprintf("%d日均线向上", b.Period)
}

func (b A均线向上) Buy(code string, dks extend.Klines) bool {
	period := b.Period
	if period == 0 {
		period = 20
	}
	lookback := b.Lookback
	if lookback == 0 {
		lookback = 1
	}
	return maUp(dks, period, lookback, b.MinSlope)
}

// A成交量放大 是当日成交量相对前N日均量放大的买入条件。
// Period 表示对比的均量周期，默认 5。
// Ratio  表示放大倍数，默认 1.5。
// 例如 Period=5, Ratio=1.5 表示当日成交量需大于近5日均量的1.5倍。
type A成交量放大 struct {
	Period int
	Ratio  float64
}

func (b A成交量放大) Name() string {
	if b.Period == 0 {
		b.Period = 5
	}
	if b.Ratio == 0 {
		b.Ratio = 1.5
	}
	return fmt.Sprintf("成交量放大%.1f倍(%d日)", b.Ratio, b.Period)
}

func (b A成交量放大) Buy(code string, dks extend.Klines) bool {
	period := b.Period
	if period == 0 {
		period = 5
	}
	ratio := b.Ratio
	if ratio == 0 {
		ratio = 1.5
	}
	// 今日量(手) > 前 period 日均量(不含今日)×ratio，严格大于；基准均量 <=0
	// 或窗口不足由 PrevAvgVolumeScaled 归为无效值，比较不触发
	//（与旧实现 len<period+1、avg<=0 守卫行为等价）。
	return mustCompare(rule.Volume(), rule.Gt, rule.PrevAvgVolumeScaled(period, ratio)).
		Eval(rule.NewInput(code, dks, nil, nil))
}

// RSI区间 是 RSI 在指定区间内的买入条件。
// Period 表示 RSI 计算周期，默认 14。
// Min 表示最低 RSI 值，默认 30。
// Max 表示最高 RSI 值，默认 50。
// 当 RSI 在 [Min, Max] 之间时返回买入信号。
type RSI区间 struct {
	Period int
	Min    float64
	Max    float64
}

func (b RSI区间) Name() string {
	min := b.Min
	if min == 0 {
		min = 30
	}
	max := b.Max
	if max == 0 {
		max = 50
	}
	return fmt.Sprintf("RSI在[%.0f,%.0f]", min, max)
}

func (b RSI区间) Buy(code string, dks extend.Klines) bool {
	period := b.Period
	if period == 0 {
		period = 14
	}
	min := b.Min
	if min == 0 {
		min = 30
	}
	max := b.Max
	if max == 0 {
		max = 50
	}
	// 浮点 RSI ∈ [min,max] 含两端边界；RSI 窗口不足为无效值不触发；
	// min>max 时区间不可满足，内核拒绝构造，按旧实现恒 false 处理。
	lowVal, highVal := rule.FloatValue(min), rule.FloatValue(max)
	r, err := rule.NewRange(rule.RSIFloat(period), &lowVal, &highVal, true, true)
	if err != nil {
		return false
	}
	return r.Eval(rule.NewInput(code, dks, nil, nil))
}

// RSI超卖回升 是 RSI 从超卖区回升的买入条件。
// Period 表示 RSI 计算周期，默认 14。
// Threshold 表示超卖阈值，默认 30。
// 当昨日 RSI < Threshold 且今日 RSI >= Threshold 时返回买入信号。
type RSI超卖回升 struct {
	Period    int
	Threshold float64
}

func (b RSI超卖回升) Name() string {
	return "RSI超卖回升"
}

func (b RSI超卖回升) Buy(code string, dks extend.Klines) bool {
	period := b.Period
	if period == 0 {
		period = 14
	}
	threshold := b.Threshold
	if threshold == 0 {
		threshold = 30
	}
	// 旧长度预检 n<period+2（昨日侧 RSI 需 period+1 根前缀）须显式拦截。
	mk, err := rule.NewMinKlines(period + 2)
	if err != nil {
		return false
	}
	// 昨 RSI < threshold 且 今 RSI >= threshold（恰等在今日侧），
	// 经 CrossUp 今日含等口径表达；RSI 窗口不足为无效值不触发。
	cross, err := rule.CrossUp(rule.RSIFloat(period), rule.Literal(rule.FloatValue(threshold)), true)
	if err != nil {
		return false
	}
	and, err := rule.NewAnd(mk, cross)
	if err != nil {
		return false
	}
	return and.Eval(rule.NewInput(code, dks, nil, nil))
}

// MACD金叉 是 MACD 柱子由负转正的买入条件。
// Fast/Slow/Signal 为 MACD 参数，默认 12/26/9。
// 当昨日柱子 <=0 且今日柱子 >0 时返回买入信号。
type MACD金叉 struct {
	Fast   int
	Slow   int
	Signal int
}

func (b MACD金叉) Name() string {
	return "MACD金叉"
}

func (b MACD金叉) Buy(code string, dks extend.Klines) bool {
	fast, slow, signal := defaultMACDParams(b.Fast, b.Slow, b.Signal)
	// MACDHist 对短前缀仍给出有限值，旧 n<slow+signal 预热门槛须显式拦截。
	mk, err := rule.NewMinKlines(slow + signal)
	if err != nil {
		return false
	}
	// 昨柱<=0 且 今柱>0（跨零即金叉，不看 DIF；恰等在昨日侧）。
	cross, err := rule.CrossUp(rule.MACDHist(fast, slow, signal), rule.Literal(rule.FloatValue(0)), false)
	if err != nil {
		return false
	}
	and, err := rule.NewAnd(mk, cross)
	if err != nil {
		return false
	}
	return and.Eval(rule.NewInput(code, dks, nil, nil))
}

// MACD零轴上方 是 MACD 柱子位于零轴上方的买入条件。
// Fast/Slow/Signal 为 MACD 参数，默认 12/26/9。
// 当今日柱子 >0 时返回买入信号。
type MACD零轴上方 struct {
	Fast   int
	Slow   int
	Signal int
}

func (b MACD零轴上方) Name() string {
	return "MACD零轴上方"
}

func (b MACD零轴上方) Buy(code string, dks extend.Klines) bool {
	fast, slow, signal := defaultMACDParams(b.Fast, b.Slow, b.Signal)
	// MACDHist 对短前缀仍给出有限值，旧 n<slow+signal 预热门槛须显式拦截。
	mk, err := rule.NewMinKlines(slow + signal)
	if err != nil {
		return false
	}
	// 今柱 > 0 严格（恰等 0 不触发）。
	gt, err := rule.NewCompare(rule.MACDHist(fast, slow, signal), rule.Gt, rule.Literal(rule.FloatValue(0)))
	if err != nil {
		return false
	}
	and, err := rule.NewAnd(mk, gt)
	if err != nil {
		return false
	}
	return and.Eval(rule.NewInput(code, dks, nil, nil))
}

// A乖离率小于 是收盘价相对均线乖离率小于指定值的买入条件。
// Period 表示均线周期，默认 20。
// Max 表示最大允许乖离率（%），默认 15。
// 乖离率 = (收盘价 - 均线) / 均线 * 100。
type A乖离率小于 struct {
	Period int
	Max    float64
}

func (b A乖离率小于) Name() string {
	period := b.Period
	if period == 0 {
		period = 20
	}
	max := b.Max
	if max == 0 {
		max = 15
	}
	return fmt.Sprintf("乖离率(%d日)<%.0f%%", period, max)
}

func (b A乖离率小于) Buy(code string, dks extend.Klines) bool {
	period := b.Period
	if period == 0 {
		period = 20
	}
	max := b.Max
	if max == 0 {
		max = 15
	}
	if len(dks) < period {
		return false
	}
	ma := f.A均线(period).Value(code, dks)
	if ma <= 0 {
		return false
	}
	closePrice := dks[len(dks)-1].Close.Float64()
	bias := (closePrice - ma) / ma * 100
	return bias < max
}

// A成交额大于 是日成交额大于指定值的买入条件。
// Min 表示最小成交额（元），默认 50000000（5000万）。
// 用于过滤流动性较差的股票。
type A成交额大于 float64

func (b A成交额大于) Name() string {
	min := b
	if min == 0 {
		min = 50000000
	}
	return fmt.Sprintf("成交额>%.0f万", min/10000)
}

func (b A成交额大于) Buy(code string, dks extend.Klines) bool {
	min := float64(b)
	if min == 0 {
		min = 50000000
	}
	// 当日成交额(元) >= min；空前缀为无效值不触发。
	return mustCompare(rule.Turnover(), rule.Ge, rule.Literal(rule.FloatValue(min))).
		Eval(rule.NewInput(code, dks, nil, nil))
}

// defaultMACDParams 返回默认的 MACD 参数（12, 26, 9）
func defaultMACDParams(fast, slow, signal int) (int, int, int) {
	if fast == 0 {
		fast = 12
	}
	if slow == 0 {
		slow = 26
	}
	if signal == 0 {
		signal = 9
	}
	return fast, slow, signal
}

// atr占比操作数 ATR/今收×100（%）：数值委托 factor.ATR值 与 K 线今收，
// 仅做比值换算（内核操作数无算术组合 DSL）；今收 <=0 或 ATR 窗口不足
// 为无效值，比较不触发（与旧实现 close<=0 守卫行为等价）。
func atr占比操作数(period int) rule.Operand {
	fa := f.ATR值(period)
	return rule.OperandFunc(fa.Name()+"÷今收×100", func(in rule.Input) rule.Value {
		if len(in.Klines) == 0 {
			return rule.InvalidValue()
		}
		closePrice := in.Klines[len(in.Klines)-1].Close.Float64()
		if closePrice <= 0 {
			return rule.InvalidValue()
		}
		return rule.FloatValue(fa.Value(in.Code, in.Klines) / closePrice * 100)
	})
}

// ATR波动率范围 是过滤极端波动股票的买入条件。
// Period 表示 ATR 计算周期，默认 14。
// MinPct/MaxPct 表示 ATR/Close 的百分比区间，默认 [0.5, 5.0]。
// 过滤掉波动太小（死水）和波动太大（妖股）的标的。
type ATR波动率范围 struct {
	Period int
	MinPct float64
	MaxPct float64
}

func (b ATR波动率范围) Name() string {
	return fmt.Sprintf("ATR波动率[%.1f%%,%.1f%%]", b.MinPct, b.MaxPct)
}

func (b ATR波动率范围) Buy(code string, dks extend.Klines) bool {
	period := b.Period
	if period == 0 {
		period = 14
	}
	minPct := b.MinPct
	if minPct == 0 {
		minPct = 0.5
	}
	maxPct := b.MaxPct
	if maxPct == 0 {
		maxPct = 5.0
	}
	// ATR占比 ∈ [minPct,maxPct] 含两端边界；max<min 时区间不可满足，
	// 内核拒绝构造，按旧实现恒 false 处理。
	lowVal, highVal := rule.FloatValue(minPct), rule.FloatValue(maxPct)
	r, err := rule.NewRange(atr占比操作数(period), &lowVal, &highVal, true, true)
	if err != nil {
		return false
	}
	return r.Eval(rule.NewInput(code, dks, nil, nil))
}

// A突破N日高点 是收盘价突破前N日最高价的买入条件（不含当日）。
// Period 表示统计窗口，默认 20。
// 真实突破信号，比单纯"站上均线"更可靠。
type A突破N日高点 struct {
	Period int
}

func (b A突破N日高点) Name() string {
	period := b.Period
	if period == 0 {
		period = 20
	}
	return fmt.Sprintf("突破%d日新高", period)
}

func (b A突破N日高点) Buy(code string, dks extend.Klines) bool {
	period := b.Period
	if period == 0 {
		period = 20
	}
	// 今收(浮点元) > 前 period 日最高价（不含今日，取 High 字段）；
	// 窗口不足为无效值不触发（与旧实现 len<period+1 预检行为等价）。
	return mustCompare(rule.CloseFloat(), rule.Gt, rule.HighestValue(period, false)).
		Eval(rule.NewInput(code, dks, nil, nil))
}

// A均线多头排列 是短中长均线呈多头排列的买入条件。
// Periods 表示从短到长的均线周期，默认 [5, 10, 20]。
// 要求 MA[0] > MA[1] > MA[2]，体现明确的上升趋势。
type A均线多头排列 []int

func (b A均线多头排列) Name() string {
	periods := b
	if len(periods) == 0 {
		periods = []int{5, 10, 20}
	}
	return fmt.Sprintf("MA%v多头排列", periods)
}

func (b A均线多头排列) Buy(code string, dks extend.Klines) bool {
	periods := b
	if len(periods) == 0 {
		periods = []int{5, 10, 20}
	}
	// 单周期退化：旧实现无相邻比较，仅剩"窗口足即触发"的预热门槛，且
	// p<=0 时（len<maxP 永不成立）空前缀也返回 true；该有效性检查无对应
	// 内核比较原语，保留在门面。
	if len(periods) == 1 {
		return periods[0] <= 0 || len(dks) >= periods[0]
	}
	// 相邻均线严格递减 MA(p0)>MA(p1)>…：逐对比较经 And 组合；任一均线
	// 窗口不足为无效值，比较不触发（与旧实现 len<max(periods) 预检等价）。
	rules := make([]rule.Rule, 0, len(periods)-1)
	for i := 0; i < len(periods)-1; i++ {
		rules = append(rules, mustCompare(rule.MAValue(periods[i]), rule.Gt, rule.MAValue(periods[i+1])))
	}
	and, err := rule.NewAnd(rules...)
	if err != nil {
		return false
	}
	return and.Eval(rule.NewInput(code, dks, nil, nil))
}

// 实体阳线收阳规则 今收 > 今开（厘级整数严格比较），与已迁移的实体阴线
// 收阴规则（今开 > 今收）对称；旧实现为浮点元比较，可表示价格范围内两者
// 等价，且与阴线侧已验收口径一致。
var 实体阳线收阳规则 = mustCompare(rule.ClosePrice(), rule.Gt, rule.OpenPrice())

// 实体阳线占比操作数 实体占振幅比例：数值委托 factor.A实体振幅占比
// （振幅 <=0 时因子返回 NaN，经 FloatValue 归为无效值，比较不触发）。
func 实体阳线占比操作数() rule.Operand {
	fa := f.A实体振幅占比{}
	return rule.OperandFunc(fa.Name(), func(in rule.Input) rule.Value {
		return rule.FloatValue(fa.Value(in.Code, in.Klines))
	})
}

// A实体阳线 是当日为实体明显的阳线买入条件。
// MinBodyRatio 表示实体长度占总振幅的最小比例，默认 0.5。
// 用于过滤"假突破"和"十字星犹豫线"。
type A实体阳线 struct {
	MinBodyRatio float64
}

func (b A实体阳线) Name() string {
	return "实体阳线"
}

func (b A实体阳线) Buy(code string, dks extend.Klines) bool {
	minRatio := b.MinBodyRatio
	if minRatio == 0 {
		minRatio = 0.5
	}
	// 先收阳（今收 > 今开），再判实体振幅占比 >= minRatio（含边界）。
	in := rule.NewInput(code, dks, nil, nil)
	if !实体阳线收阳规则.Eval(in) {
		return false
	}
	return mustCompare(实体阳线占比操作数(), rule.Ge, rule.Literal(rule.FloatValue(minRatio))).Eval(in)
}
