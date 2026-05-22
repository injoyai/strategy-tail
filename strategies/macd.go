package strategies

import (
	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/tdx/extend"
)

// BuyMACD 是 MACD 低位拐头买入策略。
// Fast 表示快线 EMA 周期，默认 12。
// Slow 表示慢线 EMA 周期，默认 26。
// Signal 表示 DEA EMA 周期，默认 9。
// Lookback 表示向前比较的窗口长度，默认 20。
// MinDiff 表示今天 MACD 柱子必须比昨天至少大多少，默认 0。
// 触发条件：昨天 MACD 柱子是近期 Lookback 窗口内最低值，并且今天 MACD 柱子比昨天变大。
type BuyMACD struct {
	Fast     int
	Slow     int
	Signal   int
	Lookback int
	MinDiff  float64
}

func (s BuyMACD) Name() string {
	return "MACD"
}

func (s BuyMACD) Buy(code string, dks extend.Klines) bool {
	if s.Fast == 0 {
		s.Fast = 12
	}
	if s.Slow == 0 {
		s.Slow = 26
	}
	if s.Signal == 0 {
		s.Signal = 9
	}
	if s.Lookback == 0 {
		s.Lookback = 20
	}

	n := len(dks)
	if n < 2 || n < s.Slow+s.Signal {
		return false
	}

	hist := macdHistogram(dks, s.Fast, s.Slow, s.Signal)
	if len(hist) != n {
		return false
	}

	yesterday := hist[n-2]
	today := hist[n-1]
	if !(today > yesterday+s.MinDiff) {
		return false
	}

	windowStart := n - 1 - s.Lookback
	if windowStart < 0 {
		windowStart = 0
	}
	minV := hist[windowStart]
	for i := windowStart + 1; i <= n-2; i++ {
		if hist[i] < minV {
			minV = hist[i]
		}
	}
	if yesterday != minV {
		return false
	}

	return true
}

// BuyMACDNegative 是 MACD 连续负数买入条件。
// Fast 表示快线 EMA 周期，默认 12。
// Slow 表示慢线 EMA 周期，默认 26。
// Signal 表示 DEA EMA 周期，默认 9。
// Days 表示要求最近连续多少天 MACD 柱子为负数，默认 1。
// 该策略适合作为 BuyAll 的过滤条件，用来限制买点处于 MACD 零轴下方区域。
type BuyMACDNegative struct {
	Fast   int
	Slow   int
	Signal int
	Days   int
}

func (s BuyMACDNegative) Name() string {
	return "MACD负数"
}

func (s BuyMACDNegative) Buy(code string, dks extend.Klines) bool {
	if s.Fast == 0 {
		s.Fast = 12
	}
	if s.Slow == 0 {
		s.Slow = 26
	}
	if s.Signal == 0 {
		s.Signal = 9
	}
	if s.Days == 0 {
		s.Days = 1
	}

	n := len(dks)
	if n < s.Slow+s.Signal || n < s.Days {
		return false
	}

	hist := macdHistogram(dks, s.Fast, s.Slow, s.Signal)
	if len(hist) != n {
		return false
	}

	for i := n - s.Days; i < n; i++ {
		if hist[i] >= 0 {
			return false
		}
	}

	return true
}

// SellMACD 是 MACD 高位拐头卖出策略。
// Fast 表示快线 EMA 周期，默认 12。
// Slow 表示慢线 EMA 周期，默认 26。
// Signal 表示 DEA EMA 周期，默认 9。
// Lookback 表示向前比较的窗口长度，默认 20。
// MinDiff 表示今天 MACD 柱子必须比昨天至少小多少，默认 0。
// 卖出时会逐日遍历 future，找到第一次满足“昨天为近期高点且今天回落”的日期。
type SellMACD struct {
	Fast     int
	Slow     int
	Signal   int
	Lookback int
	MinDiff  float64
}

func (s SellMACD) Name() string {
	return "MACD"
}

func (s SellMACD) Sell(code string, dks extend.Klines, buy core.Buy) bool {
	if s.Fast == 0 {
		s.Fast = 12
	}
	if s.Slow == 0 {
		s.Slow = 26
	}
	if s.Signal == 0 {
		s.Signal = 9
	}
	if s.Lookback == 0 {
		s.Lookback = 20
	}

	hist := macdHistogram(dks, s.Fast, s.Slow, s.Signal)
	if len(hist) != len(dks) {
		return false
	}

	n := len(hist)
	yesterday := hist[n-2]
	today := hist[n-1]
	if !(today < yesterday-s.MinDiff) {
		return false
	}

	windowStart := n - 1 - s.Lookback
	if windowStart < 0 {
		windowStart = 0
	}
	maxV := hist[windowStart]
	for j := windowStart + 1; j <= n-2; j++ {
		if hist[j] > maxV {
			maxV = hist[j]
		}
	}
	if yesterday != maxV {
		return false
	}

	return true
}

// macdHistogram 计算 MACD 柱子序列。
// 返回值为 DIF - DEA，没有乘以 2。
// 所有 MACD 策略都基于同一套计算，避免买卖条件之间口径不一致。
func macdHistogram(dks extend.Klines, fast, slow, signal int) []float64 {
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
