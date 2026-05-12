package strategies

import (
	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/tdx/extend"
	"github.com/injoyai/tdx/protocol"
)

type BuyMACD struct {
	Fast     int
	Slow     int
	Signal   int
	Lookback int
	MinDiff  float64
}

func (s BuyMACD) Name() string {
	return "MACD买入"
}

func (s BuyMACD) Buy(code string, dks extend.Klines, mk protocol.Klines) *core.Buy {
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
		return nil
	}

	hist := macdHistogram(dks, s.Fast, s.Slow, s.Signal)
	if len(hist) != n {
		return nil
	}

	yesterday := hist[n-2]
	today := hist[n-1]
	if !(today > yesterday+s.MinDiff) {
		return nil
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
		return nil
	}

	k := dks[n-1]
	return &core.Buy{
		Code:  code,
		Time:  k.Time,
		Price: k.Close,
	}
}

type SellMACD struct {
	Fast     int
	Slow     int
	Signal   int
	Lookback int
	MinDiff  float64
}

func (s SellMACD) Name() string {
	return "MACD卖出"
}

func (s SellMACD) Sell(code string, history, future extend.Klines, getMinklines func(after int) core.Klines, buy core.Buy) *core.Sell {
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

	for i := 1; i < len(future); i++ {
		series := append(history, future[:i+1]...)
		hist := macdHistogram(series, s.Fast, s.Slow, s.Signal)
		if len(hist) != len(series) {
			continue
		}

		n := len(hist)
		yesterday := hist[n-2]
		today := hist[n-1]
		if !(today < yesterday-s.MinDiff) {
			continue
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
			continue
		}

		return &core.Sell{
			Code:  code,
			Time:  future[i].Time,
			Price: future[i].Open,
		}
	}

	return nil
}

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
