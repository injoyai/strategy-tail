package util

import "github.com/injoyai/strategy-tail/lib/extend"

// MACDHistogram 计算 MACD 柱子序列。
// 返回值为 DIF - DEA，没有乘以 2。
// 所有 MACD 策略都基于同一套计算，避免买卖条件之间口径不一致。
func MACDHistogram(dks extend.Klines, fast, slow, signal int) []float64 {
	n := len(dks)
	if n == 0 {
		return nil
	}
	closes := make([]float64, n)
	for i := range dks {
		closes[i] = dks[i].Close.Float64()
	}

	emaFast := emaSeries(closes, fast)
	emaSlow := emaSeries(closes, slow)

	dif := make([]float64, n)
	for i := 0; i < n; i++ {
		dif[i] = emaFast[i] - emaSlow[i]
	}

	dea := emaSeries(dif, signal)
	hist := make([]float64, n)
	for i := 0; i < n; i++ {
		hist[i] = dif[i] - dea[i]
	}
	return hist
}

// emaSeries 计算 EMA 序列。
// 第一个值直接使用原始序列第一个值作为初始 EMA。
// period 小于等于 1 时直接返回原始序列副本。
func emaSeries(values []float64, period int) []float64 {
	n := len(values)
	out := make([]float64, n)
	if n == 0 {
		return out
	}
	if period <= 1 {
		copy(out, values)
		return out
	}

	alpha := 2.0 / (float64(period) + 1.0)
	out[0] = values[0]
	for i := 1; i < n; i++ {
		out[i] = out[i-1] + alpha*(values[i]-out[i-1])
	}
	return out
}
