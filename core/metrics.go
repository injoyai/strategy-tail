package core

import "math"

// CAGR 计算复合年化收益率。
// yearlyReturns 为各年的收益率（小数，如 0.10 表示 10%）。
// 公式：∏(1+r_i)^(1/n) - 1，n 为年数。
// 空切片返回 0。
func CAGR(yearlyReturns []float64) float64 {
	if len(yearlyReturns) == 0 {
		return 0
	}
	prod := 1.0
	for _, r := range yearlyReturns {
		prod *= (1 + r)
	}
	return math.Pow(prod, 1.0/float64(len(yearlyReturns))) - 1
}

// SharpeRatio 计算年化夏普比率。
// returns 为各期（如每笔交易）的收益率（小数）。
// periodsPerYear 为一年内的期数，用于年化（乘以 sqrt(periodsPerYear)）。
// 无波动或空切片返回 0。
func SharpeRatio(returns []float64, periodsPerYear int) float64 {
	n := len(returns)
	if n == 0 || periodsPerYear <= 0 {
		return 0
	}
	sum := 0.0
	for _, r := range returns {
		sum += r
	}
	mean := sum / float64(n)
	var sqSum float64
	for _, r := range returns {
		d := r - mean
		sqSum += d * d
	}
	std := math.Sqrt(sqSum / float64(n))
	if std == 0 {
		return 0
	}
	return mean / std * math.Sqrt(float64(periodsPerYear))
}

// SortinoRatio 计算年化索提诺比率。
// 仅用负收益计算下行波动，比 Sharpe 更适合不对称收益分布。
// returns 为各期收益率（小数），periodsPerYear 用于年化。
// 无下行风险或空切片返回 0。
func SortinoRatio(returns []float64, periodsPerYear int) float64 {
	n := len(returns)
	if n == 0 || periodsPerYear <= 0 {
		return 0
	}
	sum := 0.0
	for _, r := range returns {
		sum += r
	}
	mean := sum / float64(n)
	var downsideSqSum float64
	for _, r := range returns {
		if r < 0 {
			downsideSqSum += r * r
		}
	}
	downsideStd := math.Sqrt(downsideSqSum / float64(n))
	if downsideStd == 0 {
		return 0
	}
	return mean / downsideStd * math.Sqrt(float64(periodsPerYear))
}

// CalmarRatio 计算卡玛比率 = 年化收益率 / 最大回撤。
// annualReturn 为年化收益率（小数），maxDrawdown 为最大回撤（正数，小数）。
// 最大回撤为 0 时返回 0（避免除零）。
func CalmarRatio(annualReturn, maxDrawdown float64) float64 {
	if maxDrawdown == 0 {
		return 0
	}
	return annualReturn / maxDrawdown
}
