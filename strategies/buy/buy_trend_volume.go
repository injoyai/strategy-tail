package buy

import (
	"fmt"

	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/strategies/util"
	"github.com/injoyai/strategy-tail/lib/extend"
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
	if len(dks) < b.Period {
		return false
	}
	today := dks[len(dks)-1]
	return today.Close.Float64() > core.MA(dks, b.Period)
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
	n := len(dks)
	if n < period+1 {
		return false
	}
	today := dks[n-1]
	avg := core.AverageVolume(dks[n-1-period : n-1])
	if avg <= 0 {
		return false
	}
	return float64(today.Volume) > avg*ratio
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
	if len(dks) < period+1 {
		return false
	}
	rsi := util.CalcRSI(dks, period)
	return rsi >= min && rsi <= max
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
	n := len(dks)
	if n < period+2 {
		return false
	}
	prevRSI := util.CalcRSI(dks[:n-1], period)
	rsi := util.CalcRSI(dks, period)
	return prevRSI < threshold && rsi >= threshold
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
	n := len(dks)
	if n < slow+signal {
		return false
	}
	hist := util.MACDHistogram(dks, fast, slow, signal)
	if len(hist) != n {
		return false
	}
	return hist[n-2] <= 0 && hist[n-1] > 0
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
	n := len(dks)
	if n < slow+signal {
		return false
	}
	hist := util.MACDHistogram(dks, fast, slow, signal)
	if len(hist) != n {
		return false
	}
	return hist[n-1] > 0
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
	ma := core.MA(dks, period)
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
	if len(dks) == 0 {
		return false
	}
	return dks[len(dks)-1].Amount.Float64() >= min
}

// riseRateNDays 计算最近N日累计涨幅百分比
func riseRateNDays(dks extend.Klines, days int) float64 {
	n := len(dks)
	if n < days+1 {
		return 0
	}
	startPrice := dks[n-1-days].Close.Float64()
	endPrice := dks[n-1].Close.Float64()
	if startPrice <= 0 {
		return 0
	}
	return (endPrice - startPrice) / startPrice * 100
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
	n := len(dks)
	if n < period+1 {
		return false
	}
	// 计算ATR (True Range 平均)
	trSum := 0.0
	for i := n - period; i < n; i++ {
		high := dks[i].High.Float64()
		low := dks[i].Low.Float64()
		prevClose := dks[i-1].Close.Float64()
		tr := high - low
		if d := high - prevClose; d > tr {
			tr = d
		}
		if d := prevClose - low; d > tr {
			tr = d
		}
		trSum += tr
	}
	atr := trSum / float64(period)
	closePrice := dks[n-1].Close.Float64()
	if closePrice <= 0 {
		return false
	}
	pct := atr / closePrice * 100
	return pct >= minPct && pct <= maxPct
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
	n := len(dks)
	if n < period+1 {
		return false
	}
	prevHigh := dks[n-1-period : n-1].HHV(period).Float64()
	return dks[n-1].Close.Float64() > prevHigh
}

// A均线多头排列 是短中长均线呈多头排列的买入条件。
// Periods 表示从短到长的均线周期，默认 [5, 10, 20]。
// 要求 MA[0] > MA[1] > MA[2]，体现明确的上升趋势。
type A均线多头排列 struct {
	Periods []int
}

func (b A均线多头排列) Name() string {
	periods := b.Periods
	if len(periods) == 0 {
		periods = []int{5, 10, 20}
	}
	return fmt.Sprintf("MA%v多头排列", periods)
}

func (b A均线多头排列) Buy(code string, dks extend.Klines) bool {
	periods := b.Periods
	if len(periods) == 0 {
		periods = []int{5, 10, 20}
	}
	maxP := periods[0]
	for _, p := range periods {
		if p > maxP {
			maxP = p
		}
	}
	if len(dks) < maxP {
		return false
	}
	for i := 0; i < len(periods)-1; i++ {
		short := core.MA(dks, periods[i])
		long := core.MA(dks, periods[i+1])
		if short <= long {
			return false
		}
	}
	return true
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
	if len(dks) == 0 {
		return false
	}
	today := dks[len(dks)-1]
	open := today.Open.Float64()
	close := today.Close.Float64()
	high := today.High.Float64()
	low := today.Low.Float64()
	if close <= open {
		return false
	}
	rangeVal := high - low
	if rangeVal <= 0 {
		return false
	}
	body := close - open
	return body/rangeVal >= minRatio
}
