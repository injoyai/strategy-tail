package factor

import (
	"fmt"
	"math"

	"github.com/injoyai/strategy-tail/lib/extend"
)

// daysOr 统一因子周期的零值语义：Days <= 0 时使用默认值。
func daysOr(days, fallback int) int {
	if days <= 0 {
		return fallback
	}
	return days
}

// N日动量 是近 N 日涨跌幅：close / close[-N] - 1。正值表示动量较强。
// 数据不足或基准收盘价为零时返回 NaN。
type N日动量 struct {
	Days int
}

func (f N日动量) Name() string {
	return fmt.Sprintf("N日动量(%d)", daysOr(f.Days, 20))
}

func (f N日动量) Value(code string, dks extend.Klines) float64 {
	days := daysOr(f.Days, 20)
	if len(dks) < days+1 {
		return math.NaN()
	}
	base := dks[len(dks)-days-1].Close.Float64()
	if base == 0 {
		return math.NaN()
	}
	return dks[len(dks)-1].Close.Float64()/base - 1
}

// 均线偏离 是收盘价相对 N 日均线的偏离率：(close - MA(N)) / MA(N)。
// 数据不足或均线为零时返回 NaN。
type 均线偏离 struct {
	Days int
}

func (f 均线偏离) Name() string {
	return fmt.Sprintf("均线偏离(%d)", daysOr(f.Days, 20))
}

func (f 均线偏离) Value(code string, dks extend.Klines) float64 {
	days := daysOr(f.Days, 20)
	if len(dks) < days {
		return math.NaN()
	}
	ma := dks.MA(days).Float64()
	if ma == 0 {
		return math.NaN()
	}
	return dks[len(dks)-1].Close.Float64()/ma - 1
}

// N日斜率 是近 N 日收盘价线性回归斜率的相对值：slope / mean(close)。
// 数据不足、回归分母为零或均价为零时返回 NaN。
type N日斜率 struct {
	Days int
}

func (f N日斜率) Name() string {
	return fmt.Sprintf("N日斜率(%d)", daysOr(f.Days, 20))
}

func (f N日斜率) Value(code string, dks extend.Klines) float64 {
	days := daysOr(f.Days, 20)
	if days < 2 || len(dks) < days {
		return math.NaN()
	}

	window := dks[len(dks)-days:]
	var sumX, sumY, sumXX, sumXY float64
	for i, kline := range window {
		x := float64(i)
		y := kline.Close.Float64()
		sumX += x
		sumY += y
		sumXX += x * x
		sumXY += x * y
	}

	n := float64(days)
	denominator := n*sumXX - sumX*sumX
	if denominator == 0 {
		return math.NaN()
	}
	mean := sumY / n
	if mean == 0 {
		return math.NaN()
	}
	slope := (n*sumXY - sumX*sumY) / denominator
	return slope / mean
}
