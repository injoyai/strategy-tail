package factor

import (
	"fmt"
	"math"

	"github.com/injoyai/strategy-tail/lib/extend"
)

// SeriesPosition 返回末值在序列高低区间中的位置；平价为 0，空序列为 NaN。
func SeriesPosition(values []float64) float64 {
	if len(values) == 0 {
		return math.NaN()
	}
	lo, hi := values[0], values[0]
	for _, value := range values[1:] {
		lo = math.Min(lo, value)
		hi = math.Max(hi, value)
	}
	if hi == lo {
		return 0
	}
	return (values[len(values)-1] - lo) / (hi - lo)
}

// NegativeShorteningStreak 沿用原条件：连续上涨且前一柱为负，含首次到达或越过零轴的一步。
func NegativeShorteningStreak(values []float64) int {
	count := 0
	for i := len(values) - 1; i > 0 && values[i] > values[i-1] && values[i-1] < 0; i-- {
		count++
	}
	return count
}

// SameSignMaxReversals 返回最近 days 步中，同号段各自方向反转次数的最大值。
// 平段不改变方向；只在非零符号切换时重置，接触零轴不清除上一非零符号。
func SameSignMaxReversals(values []float64, days int) int {
	prevDir, prevSign, segment, highest := 0, 0, 0, 0
	for i := max(len(values)-days, 1); i < len(values); i++ {
		curr, prev := values[i], values[i-1]
		sign := 0
		if curr > 0 {
			sign = 1
		} else if curr < 0 {
			sign = -1
		}
		if prevSign != 0 && sign != 0 && sign != prevSign {
			prevDir, segment = 0, 0
		}
		if sign != 0 {
			prevSign = sign
		}
		dir := 0
		if diff := curr - prev; diff > 0 {
			dir = 1
		} else if diff < 0 {
			dir = -1
		}
		if dir == 0 {
			continue
		}
		if prevDir != 0 && dir != prevDir {
			segment++
			highest = max(highest, segment)
		}
		prevDir = dir
	}
	return highest
}

// MaxSameSignChangeRatio 返回最近 days 步同号非零柱的最大 |(curr-prev)/prev|。
// 越过零轴、任一柱为零均不参与；空比较集返回 -Inf，保留旧条件的免校验语义。
func MaxSameSignChangeRatio(values []float64, days int) float64 {
	highest := math.Inf(-1)
	for i := max(len(values)-days, 1); i < len(values); i++ {
		curr, prev := values[i], values[i-1]
		if prev == 0 || curr == 0 || (prev > 0) != (curr > 0) {
			continue
		}
		ratio := math.Abs((curr - prev) / prev)
		if ratio > highest {
			highest = ratio
		}
	}
	return highest
}

// MACD负柱缩短天数 固定使用 12/26/9，至少预热 35 根；含首次穿越零轴的一步。
type MACD负柱缩短天数 struct{}

func (MACD负柱缩短天数) Name() string { return "MACD负柱缩短天数" }
func (MACD负柱缩短天数) Value(code string, dks extend.Klines) float64 {
	hist, ok := standardMACD(dks)
	if !ok {
		return math.NaN()
	}
	return float64(NegativeShorteningStreak(hist))
}

// MACD同号段最大反转次数 固定使用 12/26/9 再 EMA5 平滑，Days<=0 默认 10。
// 需要至少 35 根及 Days+1 根数据；返回各同号段反转次数的最大值。
type MACD同号段最大反转次数 struct{ Days int }

func (f MACD同号段最大反转次数) Name() string {
	return fmt.Sprintf("MACD同号段最大反转次数(%d)", daysOr(f.Days, 10))
}
func (f MACD同号段最大反转次数) Value(code string, dks extend.Klines) float64 {
	days := daysOr(f.Days, 10)
	if len(dks) < macdMinSamples || days >= len(dks) {
		return math.NaN()
	}
	smoothed := (MACD平滑柱值{}).Series(dks)
	return float64(SameSignMaxReversals(smoothed, days))
}

// MACD同号最大变化比 固定使用 12/26/9，Days<=0 默认 5，不额外平滑。
// 需要至少 35 根及 Days+1 根数据；0.5 表示同号柱最大变化比为 50%。
type MACD同号最大变化比 struct{ Days int }

func (f MACD同号最大变化比) Name() string {
	return fmt.Sprintf("MACD同号最大变化比(%d)", daysOr(f.Days, 5))
}
func (f MACD同号最大变化比) Value(code string, dks extend.Klines) float64 {
	days := daysOr(f.Days, 5)
	hist, ok := standardMACD(dks)
	if !ok || days >= len(hist) {
		return math.NaN()
	}
	return math.Max(0, MaxSameSignChangeRatio(hist, days))
}
