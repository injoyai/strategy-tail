package sell

import (
	"fmt"

	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/strategies/util"
	"github.com/injoyai/tdx/extend"
)

// MACD 是 MACD 高位拐头卖出策略。
// Fast 表示快线 EMA 周期，默认 12。
// Slow 表示慢线 EMA 周期，默认 26。
// Signal 表示 DEA EMA 周期，默认 9。
// Lookback 表示向前比较的窗口长度，默认 20。
// MinDiff 表示今天 MACD 柱子必须比昨天至少小多少，默认 0。
// 卖出时会逐日遍历 future，找到第一次满足“昨天为近期高点且今天回落”的日期。
type MACD struct {
	Fast     int
	Slow     int
	Signal   int
	Lookback int
	MinDiff  float64
}

func (s MACD) Name() string {
	return fmt.Sprintf("%d日MACD最高点后", s.Lookback)
}

func (s MACD) Sell(code string, dks extend.Klines, buy core.Buy) bool {
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

	hist := util.MACDHistogram(dks, s.Fast, s.Slow, s.Signal)
	if len(hist) != len(dks) {
		return false
	}

	n := len(hist)
	if n < 2 {
		return false
	}
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
