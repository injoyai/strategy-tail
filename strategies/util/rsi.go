package util

import "github.com/injoyai/tdx/extend"

// CalcRSI 根据日线收盘价计算最近 period 个交易日的 RSI。
// 这里使用简单平均涨跌幅计算，返回值范围通常为 0 到 100。
// 返回 50 表示最近周期内没有明显涨跌变化。
func CalcRSI(dks extend.Klines, period int) float64 {
	if period <= 0 || len(dks) < period+1 {
		return 0
	}

	start := len(dks) - period
	if start < 1 {
		start = 1
	}

	gainSum := 0.0
	lossSum := 0.0
	for i := start; i < len(dks); i++ {
		change := dks[i].Close.Float64() - dks[i-1].Close.Float64()
		if change > 0 {
			gainSum += change
		} else if change < 0 {
			lossSum += -change
		}
	}

	avgGain := gainSum / float64(period)
	avgLoss := lossSum / float64(period)

	if avgLoss == 0 {
		if avgGain == 0 {
			return 50
		}
		return 100
	}
	rs := avgGain / avgLoss
	return 100 - (100 / (1 + rs))
}
