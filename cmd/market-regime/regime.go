package main

import (
	"fmt"
	"math"
	"time"

	"github.com/injoyai/strategy-tail/lib/extend"
	"github.com/injoyai/tdx/protocol"
)

// ============================================================================
// 大盘状态判定
// ============================================================================
// 给定指数 K 线，对每个交易日计算多维度的市场状态标签。
// 标签设计为互斥分组（同一维度内），便于分组统计对比。

// Regime 描述某个交易日的市场状态快照。
// 同一维度的字段互斥（如 MA5Dir 只会是 Up/Down/Flat 之一）。
type Regime struct {
	Date time.Time

	// —— 均线方向：收盘价相对均线 ——
	MA5Dir  string // AboveMA5 / BelowMA5
	MA20Dir string // AboveMA20 / BelowMA20
	MA60Dir string // AboveMA60 / BelowMA60

	// —— 均线排列 ——
	Alignment string // 多头排列 / 空头排列 / 纠缠

	// —— 均线斜率 ——
	MA20Slope string // MA20向上 / MA20向下 / MA20走平

	// —— 动量 ——
	Momentum5  string // 近5日涨 / 近5日跌 / 近5日持平
	Momentum20 string // 近20日涨 / 近20日跌 / 近20日持平

	// —— 波动 ——
	Volatility string // 高波动 / 中波动 / 低波动

	// —— 位置 ——
	Position60 string // 近60日高位 / 近60日中位 / 近60日低位

	// —— 突破 ——
	Breakout string // 突破20日新高 / 突破20日新低 / 区间内

	// —— 综合打分 ——
	Composite string // 强势 / 弱势 / 震荡
}

// RegimeLabels 返回所有维度及其可选标签，用于分组统计。
// 顺序决定报告展示顺序。
var RegimeLabels = []struct {
	Dimension string
	Labels    []string
}{
	{"MA5方向", []string{"AboveMA5", "BelowMA5"}},
	{"MA20方向", []string{"AboveMA20", "BelowMA20"}},
	{"MA60方向", []string{"AboveMA60", "BelowMA60"}},
	{"均线排列", []string{"多头排列", "空头排列", "纠缠"}},
	{"MA20斜率", []string{"MA20向上", "MA20向下", "MA20走平"}},
	{"5日动量", []string{"近5日涨", "近5日跌", "近5日持平"}},
	{"20日动量", []string{"近20日涨", "近20日跌", "近20日持平"}},
	{"波动率", []string{"高波动", "中波动", "低波动"}},
	{"60日位置", []string{"近60日高位", "近60日中位", "近60日低位"}},
	{"突破", []string{"突破20日新高", "突破20日新低", "区间内"}},
	{"综合", []string{"强势", "弱势", "震荡"}},
}

// ComputeRegimes 计算指数 K 线上每个交易日的市场状态。
// 要求 ks 长度 >= 120（MA60 + 足够历史），不足部分跳过。
func ComputeRegimes(ks extend.Klines) map[time.Time]*Regime {
	result := make(map[time.Time]*Regime, len(ks))
	if len(ks) < 120 {
		return result
	}

	// 预算 60 日波动率分位（用于高/中/低波动判定）
	// 取近 250 日的 20 日年化波动率分布
	volSeries := computeVolSeries(ks, 20, 250)
	volSorted := make([]float64, len(volSeries))
	copy(volSorted, volSeries)
	sortFloats(volSorted)
	volP33 := percentile(volSorted, 0.33)
	volP67 := percentile(volSorted, 0.67)

	for i := 119; i < len(ks); i++ {
		r := computeRegimeAt(ks, i, volSeries, volP33, volP67)
		result[ks[i].Time] = r
	}
	return result
}

// computeRegimeAt 计算 ks[i] 处的市场状态
func computeRegimeAt(ks extend.Klines, i int, volSeries []float64, volP33, volP67 float64) *Regime {
	today := ks[i]
	close := today.Close

	// 取截断 K 线用于算均线（用现有方法保持口径一致）
	sub := ks[:i+1]

	ma5 := sub.MA(5)
	ma20 := sub.MA(20)
	ma60 := sub.MA(60)
	ma10 := sub.MA(10)

	r := &Regime{Date: today.Time}

	// —— 均线方向 ——
	r.MA5Dir = priceVsMA(close, ma5, "AboveMA5", "BelowMA5")
	r.MA20Dir = priceVsMA(close, ma20, "AboveMA20", "BelowMA20")
	r.MA60Dir = priceVsMA(close, ma60, "AboveMA60", "BelowMA60")

	// —— 均线排列 ——
	if ma5 > 0 && ma10 > 0 && ma20 > 0 && ma60 > 0 {
		if close > ma5 && ma5 > ma10 && ma10 > ma20 && ma20 > ma60 {
			r.Alignment = "多头排列"
		} else if close < ma5 && ma5 < ma10 && ma10 < ma20 && ma20 < ma60 {
			r.Alignment = "空头排列"
		} else {
			r.Alignment = "纠缠"
		}
	} else {
		r.Alignment = "纠缠"
	}

	// —— MA20 斜率（5 日变化率）——
	if i >= 5 {
		ma20Prev := ks[:i-4].MA(20)
		if ma20 > 0 && ma20Prev > 0 {
			ratio := (ma20.Float64() - ma20Prev.Float64()) / ma20Prev.Float64()
			switch {
			case ratio > 0.002: // 日均 0.04% 以上
				r.MA20Slope = "MA20向上"
			case ratio < -0.002:
				r.MA20Slope = "MA20向下"
			default:
				r.MA20Slope = "MA20走平"
			}
		} else {
			r.MA20Slope = "MA20走平"
		}
	} else {
		r.MA20Slope = "MA20走平"
	}

	// —— 动量 ——
	r.Momentum5 = momentum(close, ks[i-5].Close, "近5日涨", "近5日跌", "近5日持平", 0.002)
	r.Momentum20 = momentum(close, ks[i-20].Close, "近20日涨", "近20日跌", "近20日持平", 0.005)

	// —— 波动率 ——
	// volSeries[k] 对应 ks[k+window] 处的波动率，window=20
	volIdx := i - 20
	if volIdx >= 0 && volIdx < len(volSeries) {
		v := volSeries[volIdx]
		switch {
		case v >= volP67:
			r.Volatility = "高波动"
		case v >= volP33:
			r.Volatility = "中波动"
		default:
			r.Volatility = "低波动"
		}
	} else {
		r.Volatility = "中波动"
	}

	// —— 60 日位置 ——
	hhv := sub.HHV(60)
	llv := sub.LLV(60)
	if hhv > llv && llv > 0 {
		pos := (close - llv).Float64() / (hhv - llv).Float64()
		switch {
		case pos >= 0.7:
			r.Position60 = "近60日高位"
		case pos <= 0.3:
			r.Position60 = "近60日低位"
		default:
			r.Position60 = "近60日中位"
		}
	} else {
		r.Position60 = "近60日中位"
	}

	// —— 突破 20 日新高/新低 ——
	if i >= 20 {
		prev20 := ks[i-20 : i]
		maxHigh := protocol.Price(0)
		minLow := protocol.Price(0)
		for j, k := range prev20 {
			if j == 0 {
				maxHigh = k.High
				minLow = k.Low
			} else {
				if k.High > maxHigh {
					maxHigh = k.High
				}
				if k.Low < minLow || minLow == 0 {
					minLow = k.Low
				}
			}
		}
		switch {
		case close >= maxHigh:
			r.Breakout = "突破20日新高"
		case close <= minLow && minLow > 0:
			r.Breakout = "突破20日新低"
		default:
			r.Breakout = "区间内"
		}
	} else {
		r.Breakout = "区间内"
	}

	// —— 综合打分 ——
	r.Composite = computeComposite(r)

	return r
}

// computeComposite 综合多个信号打分判断强势/弱势/震荡
func computeComposite(r *Regime) string {
	score := 0
	// 均线方向：站上 MA20 加分，跌破减分
	switch r.MA20Dir {
	case "AboveMA20":
		score += 2
	case "BelowMA20":
		score -= 2
	}
	// MA60 方向
	switch r.MA60Dir {
	case "AboveMA60":
		score += 1
	case "BelowMA60":
		score -= 1
	}
	// 排列
	switch r.Alignment {
	case "多头排列":
		score += 2
	case "空头排列":
		score -= 2
	}
	// 斜率
	switch r.MA20Slope {
	case "MA20向上":
		score += 1
	case "MA20向下":
		score -= 1
	}
	// 动量
	switch r.Momentum5 {
	case "近5日涨":
		score += 1
	case "近5日跌":
		score -= 1
	}
	switch r.Momentum20 {
	case "近20日涨":
		score += 1
	case "近20日跌":
		score -= 1
	}

	switch {
	case score >= 4:
		return "强势"
	case score <= -4:
		return "弱势"
	default:
		return "震荡"
	}
}

// priceVsMA 价格与均线比较
func priceVsMA(close, ma protocol.Price, above, below string) string {
	if close > ma {
		return above
	}
	return below
}

// momentum 动量判定
func momentum(now, prev protocol.Price, up, down, flat string, threshold float64) string {
	if prev <= 0 {
		return flat
	}
	ratio := (now.Float64() - prev.Float64()) / prev.Float64()
	switch {
	case ratio > threshold:
		return up
	case ratio < -threshold:
		return down
	default:
		return flat
	}
}

// computeVolSeries 计算 20 日年化波动率序列
// 返回每个时点的年化波动率（小数），长度 = len(ks) - window - period + 1（从有足够数据开始）
func computeVolSeries(ks extend.Klines, window, period int) []float64 {
	if len(ks) < window+period {
		return nil
	}
	// 计算每日收益率
	rets := make([]float64, len(ks)-1)
	for i := 1; i < len(ks); i++ {
		if ks[i-1].Close > 0 {
			rets[i-1] = (ks[i].Close.Float64() - ks[i-1].Close.Float64()) / ks[i-1].Close.Float64()
		}
	}

	// 滚动 window 日标准差，年化（×sqrt(250)）
	result := make([]float64, 0, len(rets)-window+1)
	for i := window; i <= len(rets); i++ {
		var sum float64
		var sqSum float64
		n := 0
		for j := i - window; j < i; j++ {
			sum += rets[j]
			sqSum += rets[j] * rets[j]
			n++
		}
		if n <= 0 {
			continue
		}
		mean := sum / float64(n)
		variance := sqSum/float64(n) - mean*mean
		if variance < 0 {
			variance = 0
		}
		annualVol := math.Sqrt(variance) * math.Sqrt(250)
		result = append(result, annualVol)
	}
	return result
}

// sortFloats 升序排序
func sortFloats(a []float64) {
	for i := 1; i < len(a); i++ {
		for j := i; j > 0 && a[j] < a[j-1]; j-- {
			a[j], a[j-1] = a[j-1], a[j]
		}
	}
}

// percentile 计算已排序切片的分位值
func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(float64(len(sorted)) * p)
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

// formatRegimeSummary 打印某日 regime 的简要说明（调试用）
func formatRegimeSummary(r *Regime) string {
	return fmt.Sprintf("%s | MA5:%s MA20:%s MA60:%s | %s | %s | %s | 综合:%s",
		r.Date.Format("2006-01-02"),
		r.MA5Dir, r.MA20Dir, r.MA60Dir,
		r.Alignment, r.MA20Slope, r.Momentum5,
		r.Composite,
	)
}
