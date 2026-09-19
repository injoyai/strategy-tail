// redundancy.go 冗余诊断（v2 设计 §7.3）。
//
// 输出至少包含：因子两两日截面 Spearman 相关的时间均值/分位数/有效日数、
// 有效股票覆盖交集并集重叠比例、单因子 IC 与合成分数 IC、控制其他因子后的
// 边际/残差 IC、leave-one-factor-out 的 IC 变化、滚动权重稳定度与触顶次数。
//
// 审计边界：
//   - 相关门槛只生成 warning，不自动删除因子；删除/合并必须形成新模型 revision；
//   - 边际/残差 IC 方法：对每因子用其余因子做当日截面 OLS（含截距），残差与
//     主周期收益的 Spearman（复用 transform.go 的 olsFit，矩阵奇异当日跳过并记录）；
//   - 合成 IC 使用等权秩基线（与 §7.1 默认基线一致）。
package portfolioresearch

import (
	"fmt"
	"math"
	"sort"
)

// ReturnsView 逐日截面主周期收益视图（date → code → 未来主周期收益，有限数）。
type ReturnsView map[string]map[string]float64

// RedundancyConfig 冗余诊断配置。
type RedundancyConfig struct {
	Missing string `json:"missing"` // 合成缺失策略（复用于合成分数）
	// CorrThreshold 相关门槛（0 = 不启用）：|相关均值| 超过门槛只生成 warning。
	CorrThreshold float64 `json:"corrThreshold,omitempty"`
}

// FactorPairCorr 两两因子日截面 Spearman 相关的时间聚合。
type FactorPairCorr struct {
	FactorA   string  `json:"factorA"`
	FactorB   string  `json:"factorB"`
	Mean      float64 `json:"mean"`
	P25       float64 `json:"p25"`
	P50       float64 `json:"p50"`
	P75       float64 `json:"p75"`
	ValidDays int     `json:"validDays"`
}

// CoverageStats 有效股票覆盖交集/并集/重叠比例（跨共同日期时间平均）。
type CoverageStats struct {
	FactorA          string  `json:"factorA"`
	FactorB          string  `json:"factorB"`
	MeanIntersection float64 `json:"meanIntersection"`
	MeanUnion        float64 `json:"meanUnion"`
	MeanOverlap      float64 `json:"meanOverlap"`
	ValidDays        int     `json:"validDays"`
}

// FactorIC 单因子/边际 IC 的时间聚合。
type FactorIC struct {
	Factor    string  `json:"factor"`
	Mean      float64 `json:"mean"`
	ValidDays int     `json:"validDays"`
}

// CompositeIC 等权合成分数 IC 的时间聚合。
type CompositeIC struct {
	Mean      float64 `json:"mean"`
	ValidDays int     `json:"validDays"`
}

// LeaveOneOut 留一因子编排（本 Task 实现 IC 维度；收益/换手/回撤由 Task 6 接入）。
type LeaveOneOut struct {
	RemovedFactor string  `json:"removedFactor"`
	FullIC        float64 `json:"fullIc"`   // 全因子合成分数 IC（与 LOO 相同有效日）
	LOOIC         float64 `json:"looIc"`    // 移除后合成分数 IC
	ICChange      float64 `json:"icChange"` // LOOIC - FullIC（正 = 移除后提升）
	ValidDays     int     `json:"validDays"`
}

// WeightStability 因子在滚动权重中的稳定度与触顶次数。
type WeightStability struct {
	Factor     string  `json:"factor"`
	MeanWeight float64 `json:"meanWeight"`
	StdWeight  float64 `json:"stdWeight"` // 跨快照总体标准差（单快照 = 0）
	CapHits    int     `json:"capHits"`   // |权重| 达到快照上限的次数
	Snapshots  int     `json:"snapshots"`
}

// RedundancyReport 冗余诊断报告。
type RedundancyReport struct {
	Pairs           []FactorPairCorr  `json:"pairs"`
	Coverage        []CoverageStats   `json:"coverage"`
	SingleIC        []FactorIC        `json:"singleIc"`
	Composite       CompositeIC       `json:"compositeIc"`
	MarginalIC      []FactorIC        `json:"marginalIc"`
	LeaveOneOut     []LeaveOneOut     `json:"leaveOneOut"`
	WeightStability []WeightStability `json:"weightStability"`
	Warnings        []string          `json:"warnings,omitempty"`
}

// RedundancyDiagnostics 冗余诊断编排（设计 §7.3）。rets 为主周期收益视图
// （date → code → 未来主周期收益）；snapshots 为滚动权重快照序列（可空）。
func RedundancyDiagnostics(factors []FactorSeries, rets ReturnsView, snapshots []WeightSnapshot, cfg RedundancyConfig) (RedundancyReport, error) {
	switch cfg.Missing {
	case TransformMissingExclude, TransformMissingCrossSectionMedian, TransformMissingRenormalizeAvailable:
	default:
		return RedundancyReport{}, fmt.Errorf("missing 非法: %q（应为 exclude | cross_section_median | renormalize_available）", cfg.Missing)
	}
	if cfg.CorrThreshold < 0 {
		return RedundancyReport{}, fmt.Errorf("corrThreshold 不能为负: %v", cfg.CorrThreshold)
	}
	if err := validateFactors(factors); err != nil {
		return RedundancyReport{}, err
	}

	report := RedundancyReport{
		Pairs:       pairCorrelations(factors),
		Coverage:    coverageStats(factors),
		SingleIC:    singleICs(factors, rets),
		Composite:   compositeIC(factors, rets, cfg.Missing),
		LeaveOneOut: leaveOneOut(factors, rets, cfg.Missing),
	}
	marg, skipped := marginalICs(factors, rets)
	report.MarginalIC = marg
	if skipped > 0 {
		report.Warnings = append(report.Warnings, fmt.Sprintf("边际 IC 计算跳过 %d 个奇异截面日（控制因子共线）", skipped))
	}
	report.WeightStability = weightStability(factors, snapshots)

	// 相关门槛：只提示，不自动删除（删除/合并必须形成新模型 revision）。
	if cfg.CorrThreshold > 0 {
		for _, p := range report.Pairs {
			if math.Abs(p.Mean) > cfg.CorrThreshold {
				report.Warnings = append(report.Warnings, fmt.Sprintf(
					"因子 %s 与 %s 日截面 Spearman 相关均值 %.3f 超过门槛 %.2f：仅提示，不自动删除（删除/合并须新模型 revision）",
					p.FactorA, p.FactorB, p.Mean, cfg.CorrThreshold))
			}
		}
	}
	return report, nil
}

// ---- 两两相关 ----

// pairCorrelations 两两因子共同日期的日截面 Spearman 相关时间聚合。
func pairCorrelations(factors []FactorSeries) []FactorPairCorr {
	var out []FactorPairCorr
	for i := 0; i < len(factors); i++ {
		for j := i + 1; j < len(factors); j++ {
			a, b := factors[i], factors[j]
			var daily []float64
			for _, d := range sortedDateUnion(a, b) {
				xs, ys, ok := pairedValues(a, b, d)
				if !ok {
					continue
				}
				ic := spearman(xs, ys)
				if math.IsNaN(ic) || math.IsInf(ic, 0) {
					continue
				}
				daily = append(daily, ic)
			}
			if len(daily) == 0 {
				continue
			}
			sort.Float64s(daily)
			out = append(out, FactorPairCorr{
				FactorA:   a.Key,
				FactorB:   b.Key,
				Mean:      meanOf(daily),
				P25:       quantile(daily, 0.25),
				P50:       quantile(daily, 0.5),
				P75:       quantile(daily, 0.75),
				ValidDays: len(daily),
			})
		}
	}
	return out
}

// sortedDateUnion 两因子日期并集（升序）。
func sortedDateUnion(a, b FactorSeries) []string {
	set := make(map[string]bool)
	for d := range a.Days {
		set[d] = true
	}
	for d := range b.Days {
		set[d] = true
	}
	dates := make([]string, 0, len(set))
	for d := range set {
		dates = append(dates, d)
	}
	sort.Strings(dates)
	return dates
}

// pairedValues 两因子某日的配对有效值（两因子该股都有有限 Final）。
func pairedValues(a, b FactorSeries, date string) (xs, ys []float64, ok bool) {
	rowsB := rowsByCode(b.Days[date])
	var xv, yv []float64
	for _, r := range a.Days[date] {
		if !finalFinite(r) {
			continue
		}
		rb, has := rowsB[r.Code]
		if !has || !finalFinite(rb) {
			continue
		}
		xv = append(xv, r.Final)
		yv = append(yv, rb.Final)
	}
	if len(xv) < minPairsPerDay {
		return nil, nil, false
	}
	return xv, yv, true
}

// rowsByCode 变换行按代码索引（行已按代码升序，map 供 O(1) 查找）。
func rowsByCode(rows []TransformedRow) map[string]TransformedRow {
	m := make(map[string]TransformedRow, len(rows))
	for _, r := range rows {
		m[r.Code] = r
	}
	return m
}

// ---- 覆盖交并 ----

// coverageStats 两两因子有效股票覆盖的交集/并集/重叠比例（跨共同日期平均）。
func coverageStats(factors []FactorSeries) []CoverageStats {
	var out []CoverageStats
	for i := 0; i < len(factors); i++ {
		for j := i + 1; j < len(factors); j++ {
			a, b := factors[i], factors[j]
			var inters, unions, overlaps []float64
			for _, d := range sortedDateUnion(a, b) {
				setA := validCodes(a, d)
				setB := validCodes(b, d)
				if len(setA) == 0 || len(setB) == 0 {
					continue
				}
				inter := 0
				for c := range setA {
					if setB[c] {
						inter++
					}
				}
				union := len(setA) + len(setB) - inter
				inters = append(inters, float64(inter))
				unions = append(unions, float64(union))
				overlaps = append(overlaps, float64(inter)/float64(union))
			}
			if len(inters) == 0 {
				continue
			}
			out = append(out, CoverageStats{
				FactorA:          a.Key,
				FactorB:          b.Key,
				MeanIntersection: meanOf(inters),
				MeanUnion:        meanOf(unions),
				MeanOverlap:      meanOf(overlaps),
				ValidDays:        len(inters),
			})
		}
	}
	return out
}

// validCodes 某因子某日的有效代码集（Final 有限）。
func validCodes(f FactorSeries, date string) map[string]bool {
	set := make(map[string]bool)
	for _, r := range f.Days[date] {
		if finalFinite(r) {
			set[r.Code] = true
		}
	}
	return set
}

// ---- 单因子 / 合成分数 IC ----

// singleICs 每因子与主周期收益的日截面 IC 时间聚合。
func singleICs(factors []FactorSeries, rets ReturnsView) []FactorIC {
	out := make([]FactorIC, 0, len(factors))
	for _, f := range factors {
		var daily []float64
		for _, d := range sortedFactorDates(f) {
			var xs, ys []float64
			for _, r := range f.Days[d] {
				if !finalFinite(r) {
					continue
				}
				ret, ok := rets[d][r.Code]
				if !ok || math.IsNaN(ret) || math.IsInf(ret, 0) {
					continue
				}
				xs = append(xs, r.Final)
				ys = append(ys, ret)
			}
			if ic, ok := spearmanIfEnough(xs, ys); ok {
				daily = append(daily, ic)
			}
		}
		out = append(out, FactorIC{Factor: f.Key, Mean: meanOf(daily), ValidDays: len(daily)})
	}
	return out
}

// sortedFactorDates 因子日期（升序）。
func sortedFactorDates(f FactorSeries) []string {
	dates := make([]string, 0, len(f.Days))
	for d := range f.Days {
		dates = append(dates, d)
	}
	sort.Strings(dates)
	return dates
}

// spearmanIfEnough 配对充足时计算 Spearman（不足或无定义返回 false）。
func spearmanIfEnough(xs, ys []float64) (float64, bool) {
	if len(xs) < minPairsPerDay {
		return 0, false
	}
	ic := spearman(xs, ys)
	if math.IsNaN(ic) || math.IsInf(ic, 0) {
		return 0, false
	}
	return ic, true
}

// compositeIC 等权合成分数与主周期收益的日截面 IC 时间聚合。
func compositeIC(factors []FactorSeries, rets ReturnsView, missing string) CompositeIC {
	daily := compositeICByDate(factors, rets, missing)
	var vals []float64
	for _, ic := range daily {
		vals = append(vals, ic)
	}
	return CompositeIC{Mean: meanOf(vals), ValidDays: len(vals)}
}

// compositeICByDate 等权合成分数逐日 IC（date → IC），Combine 失败时返回空 map。
func compositeICByDate(factors []FactorSeries, rets ReturnsView, missing string) map[string]float64 {
	out := map[string]float64{}
	rep, err := Combine(factors, missing, nil)
	if err != nil {
		return out
	}
	for _, r := range rep.Results {
		var xs, ys []float64
		for code, sc := range r.Scores {
			ret, ok := rets[r.Date][code]
			if !ok || math.IsNaN(ret) || math.IsInf(ret, 0) {
				continue
			}
			xs = append(xs, sc)
			ys = append(ys, ret)
		}
		if ic, ok := spearmanIfEnough(xs, ys); ok {
			out[r.Date] = ic
		}
	}
	return out
}

// ---- 边际/残差 IC ----

// marginalICs 每因子控制其他因子后的边际/残差 IC：
// 对每日截面用其余因子（含截距）OLS 回归该因子，残差与主周期收益的 Spearman。
// 矩阵奇异（控制因子共线）当日跳过并累计计数（不失败），由调用方记录 warning。
func marginalICs(factors []FactorSeries, rets ReturnsView) ([]FactorIC, int) {
	n := len(factors)
	dates := allFactorDates(factors)
	out := make([]FactorIC, 0, n)
	totalSkipped := 0
	for j := range factors {
		var daily []float64
		skipped := 0
		for _, d := range dates {
			rows := make([]rowsByCodeMap, n)
			for i := range factors {
				rows[i] = rowsByCode(factors[i].Days[d])
			}
			codes := commonCodesWithRets(rows, rets[d])
			if len(codes) < minPairsPerDay {
				continue
			}
			X := make([][]float64, len(codes))
			y := make([]float64, len(codes))
			for k, code := range codes {
				row := make([]float64, n) // 1 + (n-1) 个控制列
				row[0] = 1
				col := 1
				for i := range factors {
					r := rows[i][code]
					if i == j {
						y[k] = r.Final
						continue
					}
					row[col] = r.Final
					col++
				}
				X[k] = row
			}
			beta, _, err := olsFit(X, y)
			if err != nil {
				skipped++
				continue
			}
			resid := make([]float64, len(codes))
			var retsVals []float64
			for k, code := range codes {
				fitted := 0.0
				for c, b := range beta {
					fitted += X[k][c] * b
				}
				resid[k] = y[k] - fitted
				retsVals = append(retsVals, rets[d][code])
			}
			if ic, ok := spearmanIfEnough(resid, retsVals); ok {
				daily = append(daily, ic)
			}
		}
		totalSkipped += skipped
		out = append(out, FactorIC{Factor: factors[j].Key, Mean: meanOf(daily), ValidDays: len(daily)})
	}
	return out, totalSkipped
}

// rowsByCodeMap 变换行按代码索引（局部别名避免与签名冲突）。
type rowsByCodeMap map[string]TransformedRow

// allFactorDates 全部因子日期并集（升序）。
func allFactorDates(factors []FactorSeries) []string {
	set := make(map[string]bool)
	for _, f := range factors {
		for d := range f.Days {
			set[d] = true
		}
	}
	dates := make([]string, 0, len(set))
	for d := range set {
		dates = append(dates, d)
	}
	sort.Strings(dates)
	return dates
}

// commonCodesWithRets 所有因子该日都有有效值且当日收益有限（有限）的代码。
func commonCodesWithRets(rows []rowsByCodeMap, dayRets map[string]float64) []string {
	if len(rows) == 0 {
		return nil
	}
	var codes []string
	for code := range rows[0] {
		r0 := rows[0][code]
		if !finalFinite(r0) {
			continue
		}
		all := true
		for _, m := range rows[1:] {
			r, has := m[code]
			if !has || !finalFinite(r) {
				all = false
				break
			}
		}
		if !all {
			continue
		}
		ret, ok := dayRets[code]
		if !ok || math.IsNaN(ret) || math.IsInf(ret, 0) {
			continue
		}
		codes = append(codes, code)
	}
	sort.Strings(codes)
	return codes
}

// ---- leave-one-factor-out ----

// leaveOneOut 对每因子：全因子与移除后的等权合成分数 IC 在相同有效日上对比。
func leaveOneOut(factors []FactorSeries, rets ReturnsView, missing string) []LeaveOneOut {
	fullDaily := compositeICByDate(factors, rets, missing)
	out := make([]LeaveOneOut, 0, len(factors))
	for j := range factors {
		remaining := make([]FactorSeries, 0, len(factors)-1)
		for i := range factors {
			if i != j {
				remaining = append(remaining, factors[i])
			}
		}
		looDaily := compositeICByDate(remaining, rets, missing)
		var fullVals, looVals []float64
		for d, ic := range looDaily {
			full, ok := fullDaily[d]
			if !ok {
				continue
			}
			fullVals = append(fullVals, full)
			looVals = append(looVals, ic)
		}
		if len(fullVals) == 0 {
			continue
		}
		fullMean := meanOf(fullVals)
		looMean := meanOf(looVals)
		out = append(out, LeaveOneOut{
			RemovedFactor: factors[j].Key,
			FullIC:        fullMean,
			LOOIC:         looMean,
			ICChange:      looMean - fullMean,
			ValidDays:     len(fullVals),
		})
	}
	return out
}

// ---- 滚动权重稳定度 ----

// weightStability 因子在滚动权重快照序列中的均值/标准差与触顶次数。
func weightStability(factors []FactorSeries, snapshots []WeightSnapshot) []WeightStability {
	if len(snapshots) == 0 {
		return nil
	}
	out := make([]WeightStability, 0, len(factors))
	for _, f := range factors {
		var ws []float64
		capHits := 0
		for _, s := range snapshots {
			w, ok := s.FinalWeights[f.Key]
			if !ok {
				continue
			}
			ws = append(ws, w)
			if s.MaxAbsWeight > 0 && math.Abs(w) >= s.MaxAbsWeight-1e-9 {
				capHits++
			}
		}
		if len(ws) == 0 {
			continue
		}
		mean := meanOf(ws)
		std := 0.0
		if len(ws) > 1 {
			v := 0.0
			for _, w := range ws {
				v += (w - mean) * (w - mean)
			}
			std = math.Sqrt(v / float64(len(ws)))
		}
		out = append(out, WeightStability{
			Factor:     f.Key,
			MeanWeight: mean,
			StdWeight:  std,
			CapHits:    capHits,
			Snapshots:  len(snapshots),
		})
	}
	return out
}
