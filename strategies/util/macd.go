package util

import "github.com/injoyai/strategy-tail/lib/extend"

// MACDHistogram 计算 MACD 柱子序列。
// 返回值为 DIF - DEA，没有乘以 2。
// 所有 MACD 策略都基于同一套计算，避免买卖条件之间口径不一致。
func MACDHistogram(dks extend.Klines, fast, slow, signal int) []float64 {
	dif := MACDDIF(dks, fast, slow)
	if len(dif) == 0 {
		return nil
	}
	// DIF 缓冲区由本次调用独占，直接复用为柱值，避免额外分配 DEA 和 Hist。
	dea := dif[0]
	alpha := 2.0 / (float64(signal) + 1.0)
	for i, value := range dif {
		if i > 0 {
			if signal <= 1 {
				dea = value
			} else {
				dea = dea + alpha*(value-dea)
			}
		}
		dif[i] = value - dea
	}
	return dif
}

// MACDDIF 返回快慢 EMA 的差值，供柱值与 DIF 因子共用同一计算口径。
func MACDDIF(dks extend.Klines, fast, slow int) []float64 {
	n := len(dks)
	if n == 0 {
		return nil
	}
	dif := make([]float64, n)
	emaFast, emaSlow := dks[0].Close.Float64(), dks[0].Close.Float64()
	fastAlpha := 2.0 / (float64(fast) + 1.0)
	slowAlpha := 2.0 / (float64(slow) + 1.0)
	for i := 0; i < n; i++ {
		if i > 0 {
			close := dks[i].Close.Float64()
			if fast <= 1 {
				emaFast = close
			} else {
				emaFast = emaFast + fastAlpha*(close-emaFast)
			}
			if slow <= 1 {
				emaSlow = close
			} else {
				emaSlow = emaSlow + slowAlpha*(close-emaSlow)
			}
		}
		dif[i] = emaFast - emaSlow
	}

	return dif
}

// SmoothedMACDHistogram 返回对原始 MACD 量柱再做一次 EMA 平滑后的序列。
// 平滑后的量柱曲线更圆润，消除毛刺和锯齿。
// smoothPeriod 为平滑用的 EMA 周期，一般取 3~10，值越大曲线越光滑但滞后越大。
func SmoothedMACDHistogram(dks extend.Klines, fast, slow, signal, smoothPeriod int) []float64 {
	hist := MACDHistogram(dks, fast, slow, signal)
	if hist == nil {
		return nil
	}
	return emaSeries(hist, smoothPeriod)
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
