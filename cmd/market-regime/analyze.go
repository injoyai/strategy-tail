package main

import (
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/injoyai/strategy-tail/core"
)

// ============================================================================
// 分组统计
// ============================================================================

// TaggedTrade 给 Trade 打上买入当天的大盘状态标签
type TaggedTrade struct {
	Trade  core.Trade
	Regime *Regime // 买入当天的大盘状态，nil 表示该日无 regime 数据
	Year   int
	Return float64 // 单笔收益率（%）
}

// GroupStat 单个分组的统计
type GroupStat struct {
	Dimension    string  `json:"dimension"`
	Label        string  `json:"label"`
	Count        int     `json:"count"`
	Win          int     `json:"win"`
	WinRate      float64 `json:"winRate"`
	AvgProfit    float64 `json:"avgProfit"`
	ProfitFactor float64 `json:"profitFactor"`
	MaxProfit    float64 `json:"maxProfit"`
	MaxLoss      float64 `json:"maxLoss"`
	TotalProfit  float64 `json:"totalProfit"` // Σ 收益率（%），非金额
}

// DimensionResult 一个维度的分组结果
type DimensionResult struct {
	Dimension string     `json:"dimension"`
	Groups    []GroupStat `json:"groups"`
}

// AnalysisResult 完整分析结果
type AnalysisResult struct {
	StrategyName    string
	Benchmark       string
	Years           []int
	TotalTrades     int
	MatchedTrades   int // 成功匹配到 regime 的交易数
	TaggedTrades    []TaggedTrade
	DimensionResults []DimensionResult
	// 按年份×综合状态的交叉统计
	YearlyComposite map[int]map[string]GroupStat `json:"-"`
	// 综合状态下的月度表现
	MonthlyComposite map[string]map[int]float64 `json:"-"` // label -> month(1-12) -> avgReturn
}

// TagTrades 给每笔交易打上买入日的大盘状态标签
func TagTrades(trades []core.Trade, regimes map[time.Time]*Regime) []TaggedTrade {
	result := make([]TaggedTrade, 0, len(trades))
	for _, t := range trades {
		tt := TaggedTrade{
			Trade:  t,
			Year:   t.BuyTime.Year(),
			Return: t.Profit(),
		}
		// 用买入日精确匹配 regime（regime key 是 K 线 Time，即当日 15:00 左右）
		// 这里做一次日期对齐：regime 的 key 是 K 线时间，可能带时分秒
		// 尝试精确匹配 + 日期匹配两种
		if r, ok := regimes[t.BuyTime]; ok {
			tt.Regime = r
		} else {
			// 退化：按日期匹配（忽略时分秒）
			for k, v := range regimes {
				if sameDay(k, t.BuyTime) {
					tt.Regime = v
					break
				}
			}
		}
		result = append(result, tt)
	}
	return result
}

// sameDay 判断两个时间是否同一天
func sameDay(a, b time.Time) bool {
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	return ay == by && am == bm && ad == bd
}

// Analyze 执行全部分组统计
func Analyze(tagged []TaggedTrade) *AnalysisResult {
	r := &AnalysisResult{
		TotalTrades:   len(tagged),
		TaggedTrades:  tagged,
		Years:         collectYears(tagged),
	}
	for _, tt := range tagged {
		if tt.Regime != nil {
			r.MatchedTrades++
		}
	}

	// 各维度分组
	r.DimensionResults = make([]DimensionResult, 0, len(RegimeLabels))
	for _, dim := range RegimeLabels {
		r.DimensionResults = append(r.DimensionResults, analyzeByDimension(tagged, dim.Dimension, dim.Labels))
	}

	// 年度 × 综合状态 交叉统计
	r.YearlyComposite = computeYearlyComposite(tagged)
	// 综合状态 × 月度 平均收益
	r.MonthlyComposite = computeMonthlyComposite(tagged)

	return r
}

// analyzeByDimension 按指定维度分组统计
func analyzeByDimension(tagged []TaggedTrade, dimension string, labels []string) DimensionResult {
	dr := DimensionResult{Dimension: dimension}
	// 按标签收集交易
	groups := make(map[string][]TaggedTrade)
	for _, tt := range tagged {
		if tt.Regime == nil {
			continue
		}
		label := getLabelByDimension(tt.Regime, dimension)
		if label == "" {
			continue
		}
		groups[label] = append(groups[label], tt)
	}
	// 按 labels 顺序输出
	for _, label := range labels {
		ts := groups[label]
		dr.Groups = append(dr.Groups, statGroup(dimension, label, ts))
	}
	// 补充未匹配到的（regime=nil）
	missed := 0
	for _, tt := range tagged {
		if tt.Regime == nil {
			missed++
		}
	}
	if missed > 0 {
		dr.Groups = append(dr.Groups, GroupStat{
			Dimension: dimension,
			Label:     "(无数据)",
			Count:     missed,
		})
	}
	return dr
}

// getLabelByDimension 根据维度名取 regime 对应字段的值
func getLabelByDimension(r *Regime, dimension string) string {
	switch dimension {
	case "MA5方向":
		return r.MA5Dir
	case "MA20方向":
		return r.MA20Dir
	case "MA60方向":
		return r.MA60Dir
	case "均线排列":
		return r.Alignment
	case "MA20斜率":
		return r.MA20Slope
	case "5日动量":
		return r.Momentum5
	case "20日动量":
		return r.Momentum20
	case "波动率":
		return r.Volatility
	case "60日位置":
		return r.Position60
	case "突破":
		return r.Breakout
	case "综合":
		return r.Composite
	}
	return ""
}

// statGroup 统计一组交易
func statGroup(dimension, label string, ts []TaggedTrade) GroupStat {
	g := GroupStat{
		Dimension: dimension,
		Label:     label,
		Count:     len(ts),
	}
	if len(ts) == 0 {
		return g
	}
	var winSum, lossSum, profitSum float64
	for _, tt := range ts {
		r := tt.Return
		profitSum += r
		if r > g.MaxProfit {
			g.MaxProfit = r
		}
		if r < g.MaxLoss {
			g.MaxLoss = r
		}
		switch {
		case r > 0:
			g.Win++
			winSum += r
		case r < 0:
			lossSum += -r
		}
	}
	g.WinRate = float64(g.Win) / float64(g.Count) * 100
	g.AvgProfit = profitSum / float64(g.Count)
	g.TotalProfit = profitSum
	switch {
	case lossSum > 0:
		g.ProfitFactor = winSum / lossSum
	case winSum > 0:
		g.ProfitFactor = math.Inf(1)
	}
	return g
}

// computeYearlyComposite 年度×综合状态交叉统计
func computeYearlyComposite(tagged []TaggedTrade) map[int]map[string]GroupStat {
	result := make(map[int]map[string]GroupStat)
	type key struct {
		year  int
		label string
	}
	bucket := make(map[key][]TaggedTrade)
	for _, tt := range tagged {
		if tt.Regime == nil {
			continue
		}
		k := key{tt.Year, tt.Regime.Composite}
		bucket[k] = append(bucket[k], tt)
	}
	for k, ts := range bucket {
		if result[k.year] == nil {
			result[k.year] = make(map[string]GroupStat)
		}
		result[k.year][k.label] = statGroup("综合", k.label, ts)
	}
	return result
}

// computeMonthlyComposite 综合状态×月份 平均收益
func computeMonthlyComposite(tagged []TaggedTrade) map[string]map[int]float64 {
	result := make(map[string]map[int]float64)
	type key struct {
		label string
		month int
	}
	sums := make(map[key]float64)
	counts := make(map[key]int)
	for _, tt := range tagged {
		if tt.Regime == nil {
			continue
		}
		k := key{tt.Regime.Composite, int(tt.Trade.BuyTime.Month())}
		sums[k] += tt.Return
		counts[k]++
	}
	for k, s := range sums {
		if result[k.label] == nil {
			result[k.label] = make(map[int]float64)
		}
		if counts[k] > 0 {
			result[k.label][k.month] = s / float64(counts[k])
		}
	}
	return result
}

// collectYears 收集所有年份
func collectYears(tagged []TaggedTrade) []int {
	set := make(map[int]bool)
	for _, tt := range tagged {
		set[tt.Year] = true
	}
	years := make([]int, 0, len(set))
	for y := range set {
		years = append(years, y)
	}
	sort.Ints(years)
	return years
}

// FindBestWorst 找出最佳和最差的市场环境
func FindBestWorst(r *AnalysisResult) (best, worst GroupStat) {
	bestAvg := math.Inf(-1)
	worstAvg := math.Inf(1)
	for _, dr := range r.DimensionResults {
		for _, g := range dr.Groups {
			if g.Count < 30 { // 样本不足跳过
				continue
			}
			if g.AvgProfit > bestAvg {
				bestAvg = g.AvgProfit
				best = g
			}
			if g.AvgProfit < worstAvg {
				worstAvg = g.AvgProfit
				worst = g
			}
		}
	}
	return
}

// PrintSummary 打印控制台汇总
func PrintSummary(r *AnalysisResult) {
	fmt.Printf("\n%s 在不同大盘状态下的表现（%s, %d-%d）\n",
		r.StrategyName, r.Benchmark, r.Years[0], r.Years[len(r.Years)-1])
	fmt.Printf("总交易笔数: %d, 匹配到大盘数据: %d (%.1f%%)\n\n",
		r.TotalTrades, r.MatchedTrades,
		safeDiv(r.MatchedTrades*100, r.TotalTrades))

	for _, dr := range r.DimensionResults {
		fmt.Printf("=== %s ===\n", dr.Dimension)
		fmt.Printf("%-16s %6s %8s %10s %10s %10s %10s\n",
			"标签", "笔数", "胜率%", "平均收益", "盈亏比", "最大收益", "最大亏损")
		for _, g := range dr.Groups {
			pf := fmt.Sprintf("%.2f", g.ProfitFactor)
			if math.IsInf(g.ProfitFactor, 1) {
				pf = "∞"
			}
			fmt.Printf("%-16s %6d %8.1f %10.2f%% %10s %10.2f%% %10.2f%%\n",
				g.Label, g.Count, g.WinRate, g.AvgProfit, pf, g.MaxProfit, g.MaxLoss)
		}
		fmt.Println()
	}

	best, worst := FindBestWorst(r)
	fmt.Println("=== 关键发现 ===")
	if best.Count > 0 {
		fmt.Printf("最佳环境: [%s/%s] 笔数=%d 胜率=%.1f%% 平均收益=%.2f%%\n",
			best.Dimension, best.Label, best.Count, best.WinRate, best.AvgProfit)
	}
	if worst.Count > 0 {
		fmt.Printf("最差环境: [%s/%s] 笔数=%d 胜率=%.1f%% 平均收益=%.2f%%\n",
			worst.Dimension, worst.Label, worst.Count, worst.WinRate, worst.AvgProfit)
	}
}

func safeDiv(a, b int) float64 {
	if b == 0 {
		return 0
	}
	return float64(a) / float64(b)
}
