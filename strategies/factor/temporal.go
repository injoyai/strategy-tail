package factor

import (
	"fmt"
	"math"

	"github.com/injoyai/strategy-tail/lib/extend"
)

// NegativeStreak 返回序列末尾连续小于零的数量，零或缺失值中断连续段。
func NegativeStreak(values []float64) int {
	count := 0
	for i := len(values) - 1; i >= 0 && values[i] < 0; i-- {
		count++
	}
	return count
}

// PositiveStreak 返回序列末尾连续大于零的数量，零或缺失值中断连续段。
func PositiveStreak(values []float64) int {
	count := 0
	for i := len(values) - 1; i >= 0 && values[i] > 0; i-- {
		count++
	}
	return count
}

// RisingStreak 返回末尾严格递增的步数，今天首次高于昨天记为一步。
func RisingStreak(values []float64) int {
	count := 0
	for i := len(values) - 1; i > 0 && values[i] > values[i-1]; i-- {
		count++
	}
	return count
}

// FallingStreak 返回末尾严格递减的步数，相等或缺失值中断连续段。
func FallingStreak(values []float64) int {
	count := 0
	for i := len(values) - 1; i > 0 && values[i] < values[i-1]; i-- {
		count++
	}
	return count
}

// MA最弱日斜率 返回最近 Lookback 步均线相对涨速的最小值。
// Period=0 默认 20，Lookback=0 默认 5；前值非正或数据不足返回 NaN。
type MA最弱日斜率 struct{ Period, Lookback int }

func (f MA最弱日斜率) Name() string {
	return fmt.Sprintf("均线最弱日斜率(%d,%d)", daysOr(f.Period, 20), daysOr(f.Lookback, 5))
}

func (f MA最弱日斜率) Value(code string, dks extend.Klines) float64 {
	return weakestMAChange(code, dks, f.Period, f.Lookback, true)
}

// MA最弱日增量 返回最近 Lookback 步均线绝对增量的最小值，允许非正前值。
// Period=0 默认 20，Lookback=0 默认 5；数据不足返回 NaN。
type MA最弱日增量 struct{ Period, Lookback int }

func (f MA最弱日增量) Name() string {
	return fmt.Sprintf("均线最弱日增量(%d,%d)", daysOr(f.Period, 20), daysOr(f.Lookback, 5))
}

func (f MA最弱日增量) Value(code string, dks extend.Klines) float64 {
	return weakestMAChange(code, dks, f.Period, f.Lookback, false)
}

func weakestMAChange(code string, dks extend.Klines, period, lookback int, relative bool) float64 {
	if period < 0 || lookback < 0 {
		return math.NaN()
	}
	period, lookback = daysOr(period, 20), daysOr(lookback, 5)
	if len(dks) < period+lookback {
		return math.NaN()
	}
	weakest := math.Inf(1)
	for offset := 0; offset < lookback; offset++ {
		end := len(dks) - offset
		now := A均线(period).Value(code, dks[:end])
		prev := A均线(period).Value(code, dks[:end-1])
		change := now - prev
		if relative {
			if prev <= 0 {
				return math.NaN()
			}
			change /= prev
		}
		weakest = math.Min(weakest, change)
	}
	return weakest
}

// MA上方连续天数 返回末尾收盘价严格高于均线的天数，最多统计 Days 天。
// Period=0 默认 20，Days=0 默认 5；至少需要一个完整均线窗口。
type MA上方连续天数 struct{ Period, Days int }

func (f MA上方连续天数) Name() string {
	return fmt.Sprintf("均线上方连续天数(%d,%d)", daysOr(f.Period, 20), daysOr(f.Days, 5))
}

func (f MA上方连续天数) Value(code string, dks extend.Klines) float64 {
	return maPositionStreak(code, dks, daysOr(f.Period, 20), daysOr(f.Days, 5), true)
}

// MA下方连续天数 沿用卖出条件的包含相等口径：收盘价 <= 当日均线。
// Period=0 默认 20，Days=0 默认 5；最多统计 Days 天。
type MA下方连续天数 struct{ Period, Days int }

func (f MA下方连续天数) Name() string {
	return fmt.Sprintf("均线下方连续天数(含相等,%d,%d)", daysOr(f.Period, 20), daysOr(f.Days, 5))
}

func (f MA下方连续天数) Value(code string, dks extend.Klines) float64 {
	return maPositionStreak(code, dks, daysOr(f.Period, 20), daysOr(f.Days, 5), false)
}

func maPositionStreak(code string, dks extend.Klines, period, days int, above bool) float64 {
	if period <= 0 || len(dks) < period {
		return math.NaN()
	}
	count := 0
	for end := len(dks); end >= period && count < days; end-- {
		ma := A均线(period).Value(code, dks[:end])
		close := dks[end-1].Close
		if above && close <= ma || !above && close > ma {
			break
		}
		count++
	}
	return float64(count)
}
