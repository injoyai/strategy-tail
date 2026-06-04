package buy

import (
	"fmt"

	"github.com/injoyai/strategy-tail/strategies/util"
	"github.com/injoyai/tdx/extend"
)

// MACD 是 MACD 低位拐头买入策略。
// Fast 表示快线 EMA 周期，默认 12。
// Slow 表示慢线 EMA 周期，默认 26。
// Signal 表示 DEA EMA 周期，默认 9。
// Lookback 表示向前比较的窗口长度，默认 20。
// MinDiff 表示今天 MACD 柱子必须比昨天至少大多少，默认 0。
// 触发条件：昨天 MACD 柱子是近期 Lookback 窗口内最低值，并且今天 MACD 柱子比昨天变大。
type MACD struct {
	Fast     int
	Slow     int
	Signal   int
	Lookback int
	MinDiff  float64
}

func (s MACD) Name() string {
	return fmt.Sprintf("%d日MACD最低点后", s.Lookback)
}

func (s MACD) Buy(code string, dks extend.Klines) bool {
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

	hist := util.MACDHistogram(dks, s.Fast, s.Slow, s.Signal)
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

// MACD正数缓降最低点 是 MACD 正值区缓慢降低后的买入条件。
// Fast 表示快线 EMA 周期，默认 12。
// Slow 表示慢线 EMA 周期，默认 26。
// Signal 表示 DEA EMA 周期，默认 9。
// Lookback 表示向前比较的窗口长度，默认 20。
// MinDiff 表示昨天与今天 MACD 柱子的最小差值，默认 0。
// 触发条件：最近 Lookback 窗口内 MACD 柱子始终为正，昨天是窗口内最低点，并且今天开始回升。
type MACD正数缓降最低点 struct {
	Fast     int
	Slow     int
	Signal   int
	Lookback int
	MinDiff  float64
}

func (s MACD正数缓降最低点) Name() string {
	return fmt.Sprintf("%d日MACD正数缓降最低点", s.Lookback)
}

func (s MACD正数缓降最低点) Buy(code string, dks extend.Klines) bool {
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

	hist := util.MACDHistogram(dks, s.Fast, s.Slow, s.Signal)
	if len(hist) != n {
		return false
	}

	yesterday := hist[n-2]
	today := hist[n-1]
	if yesterday <= 0 {
		return false
	}
	if !(today > yesterday+s.MinDiff) {
		return false
	}

	windowStart := n - 1 - s.Lookback
	if windowStart < 0 {
		windowStart = 0
	}
	minV := hist[windowStart]
	for i := windowStart; i <= n-2; i++ {
		if hist[i] <= 0 {
			return false
		}
		if hist[i] < minV {
			minV = hist[i]
		}
	}
	if yesterday != minV {
		return false
	}

	return true
}

// MACD负数 是 MACD 连续负数买入条件。
// Fast 表示快线 EMA 周期，默认 12。
// Slow 表示慢线 EMA 周期，默认 26。
// Signal 表示 DEA EMA 周期，默认 9。
// Days 表示最近连续 MACD 柱子为负数的最少天数，默认 1。
// 当从最新交易日往前连续统计的负数天数大于等于 Days 时返回买入信号。
// 该策略适合作为 BuyAll 的过滤条件，用来限制买点处于 MACD 零轴下方区域。
type MACD负数 struct {
	Fast   int
	Slow   int
	Signal int
	Days   int
}

func (s MACD负数) Name() string {
	return "MACD负数"
}

func (s MACD负数) Buy(code string, dks extend.Klines) bool {
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

	hist := util.MACDHistogram(dks, s.Fast, s.Slow, s.Signal)
	if len(hist) != n {
		return false
	}

	count := 0
	for i := n - 1; i >= 0; i-- {
		if hist[i] >= 0 {
			break
		}
		count++
	}

	return count >= s.Days
}

// MACD正数 是 MACD 连续负数买入条件。
// Fast 表示快线 EMA 周期，默认 12。
// Slow 表示慢线 EMA 周期，默认 26。
// Signal 表示 DEA EMA 周期，默认 9。
// Days 表示要求最近连续多少天 MACD 柱子为负数，默认 1。
// 该策略适合作为 BuyAll 的过滤条件，用来限制买点处于 MACD 零轴下方区域。
type MACD正数 struct {
	Fast   int
	Slow   int
	Signal int
	Days   int
}

func (s MACD正数) Name() string {
	return "MACD正数"
}

func (s MACD正数) Buy(code string, dks extend.Klines) bool {
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

	hist := util.MACDHistogram(dks, s.Fast, s.Slow, s.Signal)
	if len(hist) != n {
		return false
	}

	for i := n - s.Days; i < n; i++ {
		if hist[i] <= 0 {
			return false
		}
	}

	return true
}
