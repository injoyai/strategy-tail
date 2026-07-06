package core

import (
	"math"
	"math/rand"
	"sort"
	"time"
)

// ============================================================================
// 蒙特卡洛模拟与滚动分析（阶段二：专业量化框架升级）
// 本文件提供蒙特卡洛重采样、滚动夏普/回撤/胜率、月度收益矩阵等
// 进阶绩效分析函数，用于评估策略在不同交易顺序下的稳健性。
// ============================================================================

// MonteCarloResult 蒙特卡洛模拟结果。
// 通过对历史交易顺序进行多次重采样，得到收益与回撤的经验分布，
// 用于评估策略对交易顺序的敏感度与尾部风险。
type MonteCarloResult struct {
	ReturnP5       float64 // 最终收益率第 5 百分位（%，悲观情景）
	ReturnP25      float64 // 最终收益率第 25 百分位（%）
	ReturnP50      float64 // 最终收益率第 50 百分位（%，中位数）
	ReturnP75      float64 // 最终收益率第 75 百分位（%）
	ReturnP95      float64 // 最终收益率第 95 百分位（%，乐观情景）
	MaxDrawdownP50 float64 // 最大回撤第 50 百分位（%）
	MaxDrawdownP95 float64 // 最大回撤第 95 百分位（%，悲观情景）
	ProbProfit     float64 // 盈利概率（0-1）
	ProbRuin       float64 // 破产概率（0-1），定义为最终亏损超过 20%
}

// percentile 计算已排序切片的百分位数值。
// p 取值范围 0-100，采用线性插值法（与 numpy 默认一致）。
func percentile(sorted []float64, p float64) float64 {
	n := len(sorted)
	if n == 0 {
		return 0
	}
	if n == 1 {
		return sorted[0]
	}
	rank := p / 100 * float64(n-1)
	lower := int(math.Floor(rank))
	upper := int(math.Ceil(rank))
	if lower < 0 {
		lower = 0
	}
	if upper >= n {
		upper = n - 1
	}
	if lower == upper {
		return sorted[lower]
	}
	frac := rank - float64(lower)
	return sorted[lower] + frac*(sorted[upper]-sorted[lower])
}

// MonteCarlo 蒙特卡洛模拟。
//
// 对历史交易顺序进行 iterations 次随机重采样（shuffle），
// 每次将打乱后的逐笔收益率（tradeReturnRate/100）复利累计，
// 得到最终收益率与最大回撤的经验分布。
//
// 返回收益率的百分位带（P5/P25/P50/P75/P95）、回撤分布（P50/P95）、
// 盈利概率与破产概率（最终亏损 > 20%）。
//
// 使用固定种子 42 保证结果可复现。iterations <= 0 时默认 1000 次。
func MonteCarlo(trades []Trade, iterations int, initialCapital float64) MonteCarloResult {
	// 默认 1000 次模拟
	if iterations <= 0 {
		iterations = 1000
	}

	result := MonteCarloResult{}

	// 边界检查：无交易或本金非法时返回空结果
	if len(trades) == 0 || initialCapital <= 0 {
		return result
	}

	// 提取每笔交易的收益率（小数，如 0.05 表示 5%）
	returns := make([]float64, len(trades))
	for i, t := range trades {
		returns[i] = tradeReturnRate(t) / 100
	}

	// 固定种子的随机数生成器，保证可复现
	rng := rand.New(rand.NewSource(42))

	finalReturns := make([]float64, 0, iterations)
	maxDrawdowns := make([]float64, 0, iterations)
	profitCount := 0
	ruinCount := 0
	const ruinThreshold = -20.0 // 破产阈值：最终亏损超过 20%

	for it := 0; it < iterations; it++ {
		// 复制收益序列并随机打乱顺序
		shuffled := make([]float64, len(returns))
		copy(shuffled, returns)
		rng.Shuffle(len(shuffled), func(i, j int) {
			shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
		})

		// 复利累计资金曲线，同时跟踪最大回撤
		equity := initialCapital
		peak := initialCapital
		maxDD := 0.0
		for _, ret := range shuffled {
			equity *= (1 + ret)
			if equity > peak {
				peak = equity
			}
			if peak > 0 {
				dd := (peak - equity) / peak * 100
				if dd > maxDD {
					maxDD = dd
				}
			}
		}

		// 计算最终收益率（%）
		finalReturn := (equity - initialCapital) / initialCapital * 100
		finalReturns = append(finalReturns, finalReturn)
		maxDrawdowns = append(maxDrawdowns, maxDD)

		if finalReturn > 0 {
			profitCount++
		}
		if finalReturn < ruinThreshold {
			ruinCount++
		}
	}

	// 排序后计算百分位
	sort.Float64s(finalReturns)
	sort.Float64s(maxDrawdowns)

	total := float64(len(finalReturns))
	result.ReturnP5 = percentile(finalReturns, 5)
	result.ReturnP25 = percentile(finalReturns, 25)
	result.ReturnP50 = percentile(finalReturns, 50)
	result.ReturnP75 = percentile(finalReturns, 75)
	result.ReturnP95 = percentile(finalReturns, 95)
	result.MaxDrawdownP50 = percentile(maxDrawdowns, 50)
	result.MaxDrawdownP95 = percentile(maxDrawdowns, 95)
	result.ProbProfit = float64(profitCount) / total
	result.ProbRuin = float64(ruinCount) / total

	return result
}

// RollingSharpe 计算滚动夏普比率。
//
// 在长度为 window 的滑动窗口内逐期计算夏普比率，
// 年化系数为 periodsPerYear（如日频取 252）。
// 返回与输入等长的切片，前 window-1 个元素为 0。
func RollingSharpe(returns []float64, window int, periodsPerYear int) []float64 {
	result := make([]float64, len(returns))
	if window <= 0 || periodsPerYear <= 0 || len(returns) < window {
		return result
	}
	for i := window - 1; i < len(returns); i++ {
		w := returns[i-window+1 : i+1]
		result[i] = SharpeRatio(w, periodsPerYear)
	}
	return result
}

// RollingDrawdown 计算滚动最大回撤。
//
// 在长度为 window 的滑动窗口内计算资金曲线的最大回撤百分比。
// 回撤 = (窗口内峰值 - 当前值) / 窗口内峰值 × 100。
// 返回与输入等长的切片，前 window-1 个元素为 0。
func RollingDrawdown(equityCurve []float64, window int) []float64 {
	result := make([]float64, len(equityCurve))
	if window <= 0 || len(equityCurve) < window {
		return result
	}
	for i := window - 1; i < len(equityCurve); i++ {
		segment := equityCurve[i-window+1 : i+1]
		peak := segment[0]
		maxDD := 0.0
		for _, eq := range segment {
			if eq > peak {
				peak = eq
			}
			if peak > 0 {
				dd := (peak - eq) / peak * 100
				if dd > maxDD {
					maxDD = dd
				}
			}
		}
		result[i] = maxDD
	}
	return result
}

// RollingWinRate 计算滚动胜率。
//
// 在长度为 window 的滑动窗口内统计盈利交易（收益率 > 0）占比（%）。
// 返回与输入等长的切片，前 window-1 个元素为 0。
func RollingWinRate(trades []Trade, window int) []float64 {
	result := make([]float64, len(trades))
	if window <= 0 || len(trades) < window {
		return result
	}
	for i := window - 1; i < len(trades); i++ {
		wins := 0
		for j := i - window + 1; j <= i; j++ {
			if tradeReturnRate(trades[j]) > 0 {
				wins++
			}
		}
		result[i] = float64(wins) / float64(window) * 100
	}
	return result
}

// MonthlyReturns 按月汇总交易收益率。
//
// 以每笔交易的买入时间（BuyTime）所属的 "YYYY-MM" 为键，
// 累加该月所有交易的收益率（%）。
// 返回 map["2024-01"] = 3.5 表示 2024 年 1 月合计收益 3.5%。
func MonthlyReturns(trades []Trade) map[string]float64 {
	result := make(map[string]float64)
	for _, t := range trades {
		key := t.BuyTime.Format("2006-01")
		result[key] += tradeReturnRate(t)
	}
	return result
}

// MonthlyReturnMatrix 构建月度收益矩阵。
//
// 返回 12×N 的二维切片（行 = 月份 1-12，列 = years 中的各年），
// 每个元素为该月所有交易收益率之和（%）。
// years 指定需要统计的年份及其列顺序；不在 years 中的月份将被忽略。
func MonthlyReturnMatrix(trades []Trade, years []int) [][]float64 {
	n := len(years)
	// 12 行（月份 1-12），n 列（年份）
	matrix := make([][]float64, 12)
	for i := range matrix {
		matrix[i] = make([]float64, n)
	}

	// 年份到列索引的映射
	yearIdx := make(map[int]int)
	for i, y := range years {
		yearIdx[y] = i
	}

	// 复用 MonthlyReturns，解析 "YYYY-MM" 键填入矩阵
	monthly := MonthlyReturns(trades)
	for key, ret := range monthly {
		t, err := time.Parse("2006-01", key)
		if err != nil {
			continue
		}
		year := t.Year()
		month := int(t.Month())
		if col, ok := yearIdx[year]; ok && month >= 1 && month <= 12 {
			matrix[month-1][col] += ret
		}
	}

	return matrix
}
