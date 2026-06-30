package buy

import (
	"fmt"

	"github.com/injoyai/strategy-tail/lib/extend"
	"github.com/injoyai/strategy-tail/strategies/util"
)

// MACD反转 是 MACD 低位拐头买入策略。
// Fast 表示快线 EMA 周期，默认 12。
// Slow 表示慢线 EMA 周期，默认 26。
// Signal 表示 DEA EMA 周期，默认 9。
// Lookback 表示向前比较的窗口长度，默认 20。
// MinDiff 表示今天 MACD 柱子必须比昨天至少大多少，默认 0。
// 触发条件：昨天 MACD 柱子是近期 Lookback 窗口内最低值，并且今天 MACD 柱子比昨天变大。
type MACD反转 struct {
	Fast     int
	Slow     int
	Signal   int
	Lookback int
	MinDiff  float64
}

func (s MACD反转) Name() string {
	return fmt.Sprintf("%d日MACD最低点后", s.Lookback)
}

func (s MACD反转) Buy(code string, dks extend.Klines) bool {
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

// MACD连涨 是 MACD 柱子连续 N 天上升的买入条件。
// Fast 表示快线 EMA 周期，默认 12。
// Slow 表示慢线 EMA 周期，默认 26。
// Signal 表示 DEA EMA 周期，默认 9。
// MinDays 表示 MACD 柱子连续上升的最少天数（含今天），默认 1。
// MaxDays 表示 MACD 柱子连续上升的最多天数（含今天），默认 0 表示不限。
//   - 配置连涨范围：MinDays=3, MaxDays=5 表示连涨 3~5 天才触发。
//   - 若 MaxDays < MinDays 或 MaxDays == 0，则上限不生效，只校验下限 MinDays。
//
// Lookback 表示连涨起点之前的回看窗口长度，默认 0（不校验）。
// 触发条件：
//  1. 从最新交易日向前连续若干天，每天的 MACD 柱子都大于前一天；
//  2. 连涨天数落在 [MinDays, MaxDays] 区间内（连涨段在 MaxDays+1 天前必须断开）；
//  3. 当 Lookback > 0 时，连涨的起点（即连涨段第一根柱子的前一根）必须是
//     向前回看 Lookback 个交易日窗口内的最低值。
//
// 该策略适合作为 BuyAll 的过滤条件，用来限制买点处于 MACD 低位拐头放大区域。
type MACD连涨 struct {
	Fast     int
	Slow     int
	Signal   int
	MinDays  int
	MaxDays  int
	Lookback int
}

func (s MACD连涨) Name() string {
	minDays := s.MinDays
	if minDays == 0 {
		minDays = 1
	}
	if s.MaxDays > 0 && s.MaxDays >= minDays {
		if s.Lookback > 0 {
			return fmt.Sprintf("MACD%d日最低点后连涨%d-%d天", s.Lookback, minDays, s.MaxDays)
		}
		return fmt.Sprintf("MACD连涨%d-%d天", minDays, s.MaxDays)
	}
	if s.Lookback > 0 {
		return fmt.Sprintf("MACD%d日最低点后连涨%d天", s.Lookback, minDays)
	}
	return fmt.Sprintf("MACD连涨%d天", minDays)
}

func (s MACD连涨) Buy(code string, dks extend.Klines) bool {
	if s.Fast == 0 {
		s.Fast = 12
	}
	if s.Slow == 0 {
		s.Slow = 26
	}
	if s.Signal == 0 {
		s.Signal = 9
	}
	if s.MinDays == 0 {
		s.MinDays = 1
	}
	// MaxDays 仅在大于等于 MinDays 时才生效
	maxDays := s.MaxDays
	if maxDays > 0 && maxDays < s.MinDays {
		maxDays = 0
	}

	n := len(dks)
	if n < s.Slow+s.Signal || n < s.MinDays+1 {
		return false
	}

	hist := util.MACDHistogram(dks, s.Fast, s.Slow, s.Signal)
	if len(hist) != n {
		return false
	}

	// 从最新交易日向前数连续上涨天数
	// streakEnd: 连涨段最后一根的索引（含今天，即 n-1）
	// streakStart: 连涨段第一根的索引
	streakEnd := n - 1
	if hist[streakEnd] <= hist[streakEnd-1] {
		// 今天没有比昨天大，连涨 0 天
		return false
	}
	streakStart := streakEnd
	for streakStart > 0 && hist[streakStart] > hist[streakStart-1] {
		streakStart--
	}
	streakDays := streakEnd - streakStart + 1

	// 校验连涨天数下限
	if streakDays < s.MinDays {
		return false
	}
	// 校验连涨天数上限（连涨段在 MaxDays+1 天前必须断开）
	if maxDays > 0 && streakDays > maxDays {
		return false
	}

	// 校验连涨起点是否为近 Lookback 天的最低点
	if s.Lookback > 0 {
		// 连涨段第一根柱子的前一根，它本身不参与连涨
		startIdx := streakStart - 1
		if startIdx < 0 {
			return false
		}
		windowStart := startIdx - s.Lookback
		if windowStart < 0 {
			windowStart = 0
		}
		for i := windowStart; i < startIdx; i++ {
			if hist[i] < hist[startIdx] {
				return false
			}
		}
	}

	return true
}
