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
// MinLookback 表示向前比较的最小窗口长度，默认 20。即昨天至少是近 MinLookback 天的最低点。
// MaxLookback 表示向前比较的最大窗口长度，默认 0 表示不限。
//   - 配置回看范围：MinLookback=4, MaxLookback=20 表示昨天是近 4~20 天内某个窗口的最低点。
//   - 若 MaxLookback < MinLookback 或 MaxLookback == 0，则上限不生效，只校验下限 MinLookback。
//
// MinDiff 表示今天 MACD 柱子必须比昨天至少大多少，默认 0。
// 触发条件：今天 MACD 柱子比昨天变大，并且昨天是近 [MinLookback, MaxLookback] 天窗口内的最低值。
type MACD反转 struct {
	Fast        int
	Slow        int
	Signal      int
	MinLookback int
	MaxLookback int
	MinDiff     float64
}

func (s MACD反转) Name() string {
	minLb := s.MinLookback
	if minLb == 0 {
		minLb = 20
	}
	if s.MaxLookback > 0 && s.MaxLookback >= minLb {
		return fmt.Sprintf("%d-%d日MACD最低点后", minLb, s.MaxLookback)
	}
	return fmt.Sprintf("%d日MACD最低点后", minLb)
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
	if s.MinLookback == 0 {
		s.MinLookback = 20
	}
	// MaxLookback 仅在大于等于 MinLookback 时才生效
	maxLb := s.MaxLookback
	if maxLb > 0 && maxLb < s.MinLookback {
		maxLb = 0
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

	// 昨天是近 [MinLookback, MaxLookback] 天窗口内的最低点
	// 存在某个窗口大小 k 使昨天是近 k 天最低即可
	end := maxLb
	if end == 0 {
		end = s.MinLookback
	}
	for k := s.MinLookback; k <= end; k++ {
		windowStart := n - 1 - k
		if windowStart < 0 {
			windowStart = 0
		}
		isMin := true
		for i := windowStart; i <= n-2; i++ {
			if hist[i] < yesterday {
				isMin = false
				break
			}
		}
		if isMin {
			return true
		}
	}

	return false
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

// MACD平滑 是 MACD 量柱走势光滑的买入条件。
// Fast 表示快线 EMA 周期，默认 12。
// Slow 表示慢线 EMA 周期，默认 26。
// Signal 表示 DEA EMA 周期，默认 9。
// Days 表示回看最近多少个交易日的量柱走势，默认 5。
// MaxRatio 表示相邻两天量柱变化的最大允许比值，默认 3.0。
//
// 触发条件：最近 Days 天内，相邻交易日的 MACD 量柱变化比值都不超过 MaxRatio。
// 变化比值 = |今天量柱 - 昨天量柱| / |昨天量柱|（昨天量柱为0时按1处理）。
// 该策略适合作为 BuyAnd 的过滤条件，排除量柱忽高忽低的股票。
type MACD平滑 struct {
	Fast     int
	Slow     int
	Signal   int
	Days     int
	MaxRatio float64
}

func (s MACD平滑) Name() string {
	days := s.Days
	if days == 0 {
		days = 5
	}
	ratio := s.MaxRatio
	if ratio == 0 {
		ratio = 3.0
	}
	return fmt.Sprintf("MACD量柱%d日平滑(≤%.1f)", days, ratio)
}

func (s MACD平滑) Buy(code string, dks extend.Klines) bool {
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
		s.Days = 5
	}
	if s.MaxRatio == 0 {
		s.MaxRatio = 3.0
	}

	n := len(dks)
	if n < s.Slow+s.Signal || n < s.Days+1 {
		return false
	}

	hist := util.MACDHistogram(dks, s.Fast, s.Slow, s.Signal)
	if len(hist) != n {
		return false
	}

	// 检查最近 Days 天内相邻量柱的变化比值
	for i := n - s.Days; i < n; i++ {
		if i <= 0 {
			continue
		}
		prev := hist[i-1]
		curr := hist[i]
		diff := curr - prev

		// 量柱同号（同正或同负）才校验平滑度
		// 异号（穿越零轴）属于正常反转，不视为突变
		if prev == 0 || curr == 0 {
			continue
		}
		if (prev > 0) != (curr > 0) {
			continue // 穿越零轴，跳过
		}

		// 变化比值 = |变化量| / |昨天量柱|
		ratio := diff / prev
		if ratio < 0 {
			ratio = -ratio
		}

		if ratio > s.MaxRatio {
			return false
		}
	}

	return true
}

// MACD负数 是 MACD 连续负数买入条件。
// Fast 表示快线 EMA 周期，默认 12。
// Slow 表示慢线 EMA 周期，默认 26。
// Signal 表示 DEA EMA 周期，默认 9。
// MinDays 表示最近连续 MACD 柱子为负数的最少天数，默认 1。
// MaxDays 表示最近连续 MACD 柱子为负数的最多天数，默认 0 表示不限。
//   - 配置范围：MinDays=3, MaxDays=5 表示连续负数 3~5 天才触发。
//   - 若 MaxDays < MinDays 或 MaxDays == 0，则上限不生效，只校验下限 MinDays。
//
// 该策略适合作为 BuyAll 的过滤条件，用来限制买点处于 MACD 零轴下方区域。
type MACD负数 struct {
	Fast    int
	Slow    int
	Signal  int
	MinDays int
	MaxDays int
}

func (s MACD负数) Name() string {
	if s.MaxDays > 0 && s.MaxDays >= s.MinDays {
		return fmt.Sprintf("MACD负数%d~%d日", s.MinDays, s.MaxDays)
	}
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
	if s.MinDays == 0 {
		s.MinDays = 1
	}
	maxDays := s.MaxDays
	if maxDays > 0 && maxDays < s.MinDays {
		maxDays = 0
	}

	n := len(dks)
	if n < s.Slow+s.Signal || n < s.MinDays {
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

	if count < s.MinDays {
		return false
	}
	if maxDays > 0 && count > maxDays {
		return false
	}
	return true
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
