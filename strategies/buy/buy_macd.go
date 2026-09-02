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

// MACD顺滑 是 MACD 量柱曲线光滑的买入条件。
// 对原始 MACD 量柱序列再做一次 EMA 平滑，然后检查最近 Days 天平滑后量柱的
// 方向反转次数。与旧版不同，本版按"正数段/负数段"分别统计反转次数：
// 量柱穿越零轴时不计入反转（零轴穿越是正常反转），但同号段（连续正数或连续负数）
// 内的方向变化才算拐头。
//
// Fast 表示快线 EMA 周期，默认 12。
// Slow 表示慢线 EMA 周期，默认 26。
// Signal 表示 DEA EMA 周期，默认 9。
// Smooth 表示对量柱做 EMA 平滑的周期，默认 5。值越大曲线越光滑但滞后越大。
// Days 表示回看最近多少个交易日检查曲线光滑度，默认 10。
// MaxReversals 表示最近 Days 天内，每个同号段（连续正数或连续负数）内允许的
// 量柱方向反转次数，默认 1。
//   - MaxReversals=0 要求每个同号段内量柱严格单调。
//   - MaxReversals=1 允许每个同号段内 1 次方向反转（如先降后升的 V 形）。
//   - 零轴穿越（正→负或负→正）不计入反转，但会开启一个新的同号段，
//     段内反转计数随之归零。
//
// 触发条件：最近 Days 天内，任一同号段（连续正数段或连续负数段）内的量柱方向
// 反转次数均 <= MaxReversals。即"连续正数时每个正数段最多 1 次拐头、连续负数
// 时每个负数段最多 1 次拐头"。该策略适合作为 buy.And 的过滤条件，排除量柱忽上
// 忽下、走势锯齿的股票。
type MACD顺滑 struct {
	Fast         int
	Slow         int
	Signal       int
	Smooth       int
	Days         int
	MaxReversals int
}

func (s MACD顺滑) Name() string {
	smooth := s.Smooth
	if smooth == 0 {
		smooth = 5
	}
	days := s.Days
	if days == 0 {
		days = 10
	}
	maxRev := s.MaxReversals
	return fmt.Sprintf("MACD量柱EMA%d平滑%d日(同号段反转≤%d)", smooth, days, maxRev)
}

func (s MACD顺滑) Buy(code string, dks extend.Klines) bool {
	if s.Fast == 0 {
		s.Fast = 12
	}
	if s.Slow == 0 {
		s.Slow = 26
	}
	if s.Signal == 0 {
		s.Signal = 9
	}
	if s.Smooth == 0 {
		s.Smooth = 5
	}
	if s.Days == 0 {
		s.Days = 10
	}

	n := len(dks)
	if n < s.Slow+s.Signal || n < s.Days+1 {
		return false
	}

	smoothed := util.SmoothedMACDHistogram(dks, s.Fast, s.Slow, s.Signal, s.Smooth)
	if len(smoothed) != n {
		return false
	}

	// 按同号段（连续正数 / 连续负数）分段统计方向反转次数：
	// 零轴穿越开启新段，段内反转计数归零；任一段内反转数 > MaxReversals 即拒绝。
	maxRev := s.MaxReversals
	prevDir := 0  // 0:未知, 1:上升, -1:下降
	prevSign := 0 // 0:未知, 1:正, -1:负
	segRev := 0   // 当前同号段内反转次数
	for i := n - s.Days; i < n; i++ {
		if i <= 0 {
			continue
		}
		curr := smoothed[i]
		prev := smoothed[i-1]

		// 确定当前量柱符号
		sign := 0
		if curr > 0 {
			sign = 1
		} else if curr < 0 {
			sign = -1
		}

		// 符号变化（零轴穿越）→ 开启新段，重置方向与段内反转计数
		if prevSign != 0 && sign != 0 && sign != prevSign {
			prevDir = 0
			segRev = 0
		}
		if sign != 0 {
			prevSign = sign
		}

		// 统计同号段内方向反转
		diff := curr - prev
		dir := 0
		if diff > 0 {
			dir = 1
		} else if diff < 0 {
			dir = -1
		}
		if dir == 0 {
			continue
		}
		if prevDir != 0 && dir != prevDir {
			segRev++
			if segRev > maxRev {
				return false
			}
		}
		if dir != 0 {
			prevDir = dir
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
	// 连涨天数 = 上涨步数（今天>昨天算1步），等于连涨段除基础柱外的步数
	streakDays := streakEnd - streakStart

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

// MACD负柱缩短 是 MACD 绿柱（负柱）逐渐缩短的买入条件。
//
// 社区经典"绿柱缩短"抄底逻辑：下跌趋势中，绿柱（负柱）由长变短代表
// 空头动能衰竭，是底部反转的早期信号。
// 本条件要求：昨日为负柱，且今日负柱比昨日更接近零轴（hist[n-1] > hist[n-2]），
// 并且近 Lookback 根柱子内负柱总体在收窄（最近 MinDays 根中递增步数 >= MinDays）。
//
// Fast 表示快线 EMA 周期，默认 12。
// Slow 表示慢线 EMA 周期，默认 26。
// Signal 表示 DEA EMA 周期，默认 9。
// MinDays 表示最近连续"负柱变短"的最少天数，默认 1。
// MaxDays 表示最近连续"负柱变短"的最多天数，默认 0 表示不限。
//   - 配置范围：MinDays=2, MaxDays=3 表示最近 2~3 天负柱都在变短。
//   - 若 MaxDays < MinDays 或 MaxDays == 0，则上限不生效，只校验下限 MinDays。
//
// Lookback 表示负柱缩短前的回看窗口，默认 0（不校验）。
// 当 Lookback > 0 时，要求近 Lookback 根柱子内存在"负柱缩短"的起点，
// 即窗口内较早位置曾出现连续负柱（体现"大量负柱"前提）。
//
// 该策略常与 MACD负数 / A企稳信号 / MACD转红 组合使用：
//
//	buy.And{ buy.MACD负数{MinDays:5}, buy.MACD负柱缩短{MinDays:2}, buy.A企稳信号{}, buy.MACD转红{} }
type MACD负柱缩短 struct {
	Fast     int
	Slow     int
	Signal   int
	MinDays  int
	MaxDays  int
	Lookback int
}

func (s MACD负柱缩短) Name() string {
	minDays := s.MinDays
	if minDays == 0 {
		minDays = 1
	}
	if s.MaxDays > 0 && s.MaxDays >= minDays {
		return fmt.Sprintf("MACD负柱缩短%d-%d天", minDays, s.MaxDays)
	}
	return fmt.Sprintf("MACD负柱缩短%d天", minDays)
}

func (s MACD负柱缩短) Buy(code string, dks extend.Klines) bool {
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
	if n < 2 || n < s.Slow+s.Signal || n < s.MinDays+1 {
		return false
	}

	hist := util.MACDHistogram(dks, s.Fast, s.Slow, s.Signal)
	if len(hist) != n {
		return false
	}

	// 昨天必须是负柱（今天转红由 MACD转红 负责）
	yesterday := hist[n-2]
	if yesterday >= 0 {
		return false
	}

	// 从最新交易日向前数连续"负柱变短"天数
	// 负柱变短 = 今天比昨天更接近零轴（更大），且两者都为负
	streakEnd := n - 1
	if !(hist[streakEnd] > hist[streakEnd-1]) {
		return false
	}
	streakStart := streakEnd
	for streakStart > 0 && hist[streakStart] > hist[streakStart-1] && hist[streakStart-1] < 0 {
		streakStart--
	}
	streakDays := streakEnd - streakStart

	if streakDays < s.MinDays {
		return false
	}
	if maxDays > 0 && streakDays > maxDays {
		return false
	}

	// 校验回看窗口内曾出现连续负柱（"大量负柱"前提）
	if s.Lookback > 0 {
		windowStart := n - 1 - s.Lookback
		if windowStart < 0 {
			windowStart = 0
		}
		hasNegative := false
		for i := windowStart; i < n; i++ {
			if hist[i] < 0 {
				hasNegative = true
				break
			}
		}
		if !hasNegative {
			return false
		}
	}

	return true
}

// MACD转红 是 MACD 量柱由负转正（零轴金叉）的买入条件。
// Fast 表示快线 EMA 周期，默认 12。
// Slow 表示慢线 EMA 周期，默认 26。
// Signal 表示 DEA EMA 周期，默认 9。
// 触发条件：今天 MACD 量柱 > 0（变红），昨天 MACD 量柱 <= 0（此前为绿/非红），
// 即量柱从零轴下方穿越到上方。
//
// 常与 MACD连涨 组合使用，表达“此前量柱为负、连续上涨数日后今天转红”的买点：
//
//	buy.And{ buy.MACD转红{}, buy.MACD连涨{MinDays: 3, MaxDays: 5} }
//
// 其中 MACD连涨{MinDays: N} 已隐含“此前至少 N-1 天量柱为负”（因昨天 <= 0 且连涨使得更早的柱子更负）。
type MACD转红 struct {
	Fast   int
	Slow   int
	Signal int
}

func (s MACD转红) Name() string {
	return "MACD量柱转红"
}

func (s MACD转红) Buy(code string, dks extend.Klines) bool {
	if s.Fast == 0 {
		s.Fast = 12
	}
	if s.Slow == 0 {
		s.Slow = 26
	}
	if s.Signal == 0 {
		s.Signal = 9
	}

	n := len(dks)
	if n < 2 || n < s.Slow+s.Signal {
		return false
	}

	hist := util.MACDHistogram(dks, s.Fast, s.Slow, s.Signal)
	if len(hist) != n {
		return false
	}

	today := hist[n-1]
	yesterday := hist[n-2]
	// 今天变红（>0），昨天非红（<=0），确认零轴穿越
	return today > 0 && yesterday <= 0
}
