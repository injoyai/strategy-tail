package buy

import (
	"fmt"

	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/tdx/extend"
)

// A长期均线多头 是长期均线呈多头排列的买入条件（牛股筛选器）。
// 默认要求 MA20 > MA60 > MA120，体现长期上升趋势。
// 是"龙头股回调企稳"策略的核心前置过滤条件，确保只选择长期向上的好股票。
type A长期均线多头 struct {
	Short  int // 默认 20
	Medium int // 默认 60
	Long   int // 默认 120
}

func (b A长期均线多头) Name() string {
	s, m, l := b.params()
	return fmt.Sprintf("MA%d>MA%d>MA%d", s, m, l)
}

func (b A长期均线多头) params() (int, int, int) {
	s, m, l := b.Short, b.Medium, b.Long
	if s == 0 {
		s = 20
	}
	if m == 0 {
		m = 60
	}
	if l == 0 {
		l = 120
	}
	return s, m, l
}

func (b A长期均线多头) Buy(code string, dks extend.Klines) bool {
	s, m, l := b.params()
	if len(dks) < l {
		return false
	}
	ma1 := core.MA(dks, s)
	ma2 := core.MA(dks, m)
	ma3 := core.MA(dks, l)
	return ma1 > ma2 && ma2 > ma3
}

// A收盘价站上长期均线 是收盘价站上长期均线（如120/250日）的买入条件。
// Period 表示均线周期，默认 120（半年线）。
// Margin 表示需要高于均线的最小百分比，默认 0（即只要在上方即可）。
// 用于过滤长期下跌或筑底中的股票。
type A收盘价站上长期均线 struct {
	Period int
	Margin float64 // 百分比，例如 2 表示收盘价需高于均线 2%
}

func (b A收盘价站上长期均线) Name() string {
	period := b.Period
	if period == 0 {
		period = 120
	}
	return fmt.Sprintf("站上%d日均线", period)
}

func (b A收盘价站上长期均线) Buy(code string, dks extend.Klines) bool {
	period := b.Period
	if period == 0 {
		period = 120
	}
	if len(dks) < period {
		return false
	}
	ma := core.MA(dks, period)
	if ma <= 0 {
		return false
	}
	closePrice := dks[len(dks)-1].Close.Float64()
	threshold := ma * (1 + b.Margin/100)
	return closePrice > threshold
}

// A浅回调至均线 是股价从近期高点回调至指定均线附近的买入条件（高胜率核心）。
// MAPeriod 表示参照均线，默认 20。
// HighLookback 表示统计近期高点的窗口，默认 20。
// MinPullback 表示最小回调幅度（%），默认 2.0（避免选还在上涨的）。
// MaxPullback 表示最大回调幅度（%），默认 8.0（避免选趋势反转的）。
// MAProximity 表示与均线的接近度（%），默认 3.0（收盘价在均线上下3%范围内）。
type A浅回调至均线 struct {
	MAPeriod     int
	HighLookback int
	MinPullback  float64
	MaxPullback  float64
	MAProximity  float64
}

func (b A浅回调至均线) Name() string {
	maPeriod := b.MAPeriod
	if maPeriod == 0 {
		maPeriod = 20
	}
	return fmt.Sprintf("浅回调至MA%d", maPeriod)
}

func (b A浅回调至均线) Buy(code string, dks extend.Klines) bool {
	maPeriod := b.MAPeriod
	if maPeriod == 0 {
		maPeriod = 20
	}
	highLookback := b.HighLookback
	if highLookback == 0 {
		highLookback = 20
	}
	minPullback := b.MinPullback
	if minPullback == 0 {
		minPullback = 2.0
	}
	maxPullback := b.MaxPullback
	if maxPullback == 0 {
		maxPullback = 8.0
	}
	proximity := b.MAProximity
	if proximity == 0 {
		proximity = 3.0
	}

	n := len(dks)
	if n < maPeriod || n < highLookback {
		return false
	}

	today := dks[n-1]
	closePrice := today.Close.Float64()

	// 统计近期高点
	highest := dks[n-highLookback : n].HHV(highLookback).Float64()
	if highest <= 0 {
		return false
	}

	// 当前回调幅度
	pullback := (highest - closePrice) / highest * 100
	if pullback < minPullback || pullback > maxPullback {
		return false
	}

	// 价格在均线附近（上下 proximity% 之内）
	ma := core.MA(dks, maPeriod)
	if ma <= 0 {
		return false
	}
	dist := (closePrice - ma) / ma * 100
	if dist < -proximity || dist > proximity {
		return false
	}

	return true
}

// A企稳信号 是回调后企稳的右侧买入信号。
// 要求：今日为阳线 + 今日收盘价 > 昨日收盘价 + 今日最低价 > 前N日最低价。
// LowLookback 表示前低统计窗口，默认 3。
// 用于过滤"下跌途中的反弹"，确保是真正的企稳。
type A企稳信号 struct {
	LowLookback int
}

func (b A企稳信号) Name() string {
	return "企稳信号"
}

func (b A企稳信号) Buy(code string, dks extend.Klines) bool {
	lookback := b.LowLookback
	if lookback == 0 {
		lookback = 3
	}
	n := len(dks)
	if n < lookback+1 {
		return false
	}
	today := dks[n-1]
	yesterday := dks[n-2]

	// 今日为阳线
	if today.Close <= today.Open {
		return false
	}

	// 今日收盘价 > 昨日收盘价
	if today.Close <= yesterday.Close {
		return false
	}

	// 今日最低价 >= 前N日最低价（不创新低）
	prevLow := dks[n-1-lookback : n-1].LLV(lookback)
	if today.Low < prevLow {
		return false
	}

	return true
}

// A缩量企稳 是回调过程中成交量萎缩的企稳信号。
// Period 表示对比的均量周期，默认 5。
// MaxRatio 表示今日成交量相对均量的最大比例，默认 1.2（即不放量）。
// 缩量回调说明抛压减弱，是右侧买点的重要确认。
type A缩量企稳 struct {
	Period   int
	MaxRatio float64
}

func (b A缩量企稳) Name() string {
	return "缩量企稳"
}

func (b A缩量企稳) Buy(code string, dks extend.Klines) bool {
	period := b.Period
	if period == 0 {
		period = 5
	}
	maxRatio := b.MaxRatio
	if maxRatio == 0 {
		maxRatio = 1.2
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
	return float64(today.Volume) <= avg*maxRatio
}

// A年线向上 是250日均线（年线）方向向上的买入条件（长牛过滤器）。
// Lookback 表示对比的天数，默认 20（即20日前年线与今日比较）。
// MinSlope 表示最小斜率（小数），默认 0（只要向上即可）。
// 年线向上代表长期处于牛市/上涨阶段，是高胜率的根本保证。
type A年线向上 struct {
	Lookback int
	MinSlope float64
}

func (b A年线向上) Name() string {
	return "年线向上"
}

func (b A年线向上) Buy(code string, dks extend.Klines) bool {
	lookback := b.Lookback
	if lookback == 0 {
		lookback = 20
	}
	const period = 250
	n := len(dks)
	if n < period+lookback {
		return false
	}
	maNow := core.MA(dks, period)
	maPrev := core.MA(dks[:n-lookback], period)
	if maNow <= maPrev || maPrev <= 0 {
		return false
	}
	if b.MinSlope > 0 {
		slope := (maNow - maPrev) / maPrev
		if slope < b.MinSlope {
			return false
		}
	}
	return true
}

// A非连续下跌 是要求最近N日中下跌天数不超过阈值的买入条件。
// Lookback 表示统计窗口，默认 10。
// MaxDownDays 表示允许的最大下跌天数，默认 6（即上涨天数至少4天）。
// 用于过滤趋势已经反转的股票。
type A非连续下跌 struct {
	Lookback    int
	MaxDownDays int
}

func (b A非连续下跌) Name() string {
	return "非连续下跌"
}

func (b A非连续下跌) Buy(code string, dks extend.Klines) bool {
	lookback := b.Lookback
	if lookback == 0 {
		lookback = 10
	}
	maxDown := b.MaxDownDays
	if maxDown == 0 {
		maxDown = 6
	}
	n := len(dks)
	if n < lookback+1 {
		return false
	}
	downDays := 0
	for i := n - lookback; i < n; i++ {
		if dks[i].Close < dks[i-1].Close {
			downDays++
		}
	}
	return downDays <= maxDown
}
