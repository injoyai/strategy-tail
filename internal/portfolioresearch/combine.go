// combine.go 多因子合成与滚动 IC 权重（v2 设计 §7）。
//
// 提供两条合成路径：
//   - 等权秩基线（§7.1）：score(i,t) = Σ_j w_j * transformedFactor(i,t,j)，w_j = 1/n。
//   - 滚动 IC 权重（§7.2）：训练器只接收训练视图，权重快照保存训练起止日、
//     样本数、原始估计、收缩值、最终权重与 fallback 原因。
//
// 审计边界：
//   - 合成输入统一为方向统一后的 Final（与 Transform 输出同源，越大越好）；
//   - 滚动 IC 权重训练器只读取训练视图，测试窗数据在类型上无法进入；
//   - 方向冻结：训练器不因短期反向自动翻转方向（负估计给零权重）；
//   - 数据不足按协议预先选择回退等权或现金，不得临时择优。
package portfolioresearch

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"sort"
)

// ---- 常量 ----

const (
	// tradingDaysPerYear 训练窗截取用的年交易日数（A 股约 250 个交易日/年）。
	tradingDaysPerYear = 250
	// minICDays 滚动 IC 权重的有效 IC 样本日数下限，不足按协议 fallback。
	minICDays = 5
	// minPairsPerDay 单日截面 IC 的最小配对股票数（少于无统计意义）。
	minPairsPerDay = 3
	// weightTolerance 权重和/上限校验容差。
	weightTolerance = 1e-9
)

// ---- 输入类型 ----

// FactorSeries 单因子变换后的逐日截面（合成与训练共用输入）。
// Days 值为方向统一后的 Final（越大越好），与 Transform 输出同源。
type FactorSeries struct {
	Key       string                      `json:"key"`       // 因子身份 key（审计引用）
	Direction string                      `json:"direction"` // 冻结方向，训练器不自动翻转
	Horizon   int                         `json:"horizon"`   // 主预测周期（交易日），用于滚动 IC 的未来收益
	Days      map[string][]TransformedRow `json:"days"`      // 信号日 → 变换行
}

// TrainingView 训练视图：只含训练期数据，训练器只接受该类型（测试窗无法进入）。
// Dates 为升序交易日；Price 为 date → code → 收盘价（用于从视图自身计算未来主周期收益）。
type TrainingView struct {
	Dates []string                      `json:"dates"`
	Price map[string]map[string]float64 `json:"price"`
}

// Validate 校验训练视图：日期非空、严格升序、无重复；每日期价格齐全且有限。
func (v TrainingView) Validate() error {
	if len(v.Dates) == 0 {
		return fmt.Errorf("训练视图日期不能为空")
	}
	for i, d := range v.Dates {
		if d == "" {
			return fmt.Errorf("训练视图 Dates[%d] 为空", i)
		}
		if i > 0 && v.Dates[i] <= v.Dates[i-1] {
			return fmt.Errorf("训练视图日期必须严格升序: %s <= %s", v.Dates[i], v.Dates[i-1])
		}
		pm, ok := v.Price[d]
		if !ok {
			return fmt.Errorf("训练视图缺 %s 的价格", d)
		}
		for code, p := range pm {
			if math.IsNaN(p) || math.IsInf(p, 0) {
				return fmt.Errorf("训练视图 %s/%s 价格非有限", d, code)
			}
		}
	}
	return nil
}

// ---- 合成输出类型 ----

// CombineResult 单日合成结果。
type CombineResult struct {
	Date string `json:"date"`
	// Scores 代码 → 合成分数（缺失策略下无分数的股票不出现）。
	Scores map[string]float64 `json:"scores"`
	// Weights 因子 key → 当日实际权重：exclude/median 恒等于名义权重；
	// renormalize_available 为跨股票的平均实际权重（每代码可用因子不同）。
	Weights map[string]float64 `json:"weights"`
	// WeightDrift 重归一化导致的权重漂移 = Σ_j |平均实际权重 - 名义权重| / n（仅 renormalize 非 0）。
	WeightDrift float64 `json:"weightDrift"`
}

// FactorCoverage 单因子覆盖诊断（方向、有效日数、平均有效股票数单独报告）。
type FactorCoverage struct {
	Key       string  `json:"key"`
	Direction string  `json:"direction"`
	ValidDays int     `json:"validDays"`
	AvgStocks float64 `json:"avgStocks"`
}

// CombineReport 多因子合成报告（每日结果 + 覆盖诊断汇总）。
type CombineReport struct {
	Results     []CombineResult  `json:"results"`
	Coverage    []FactorCoverage `json:"coverage"`
	EqualWeight float64          `json:"equalWeight"`
	Warnings    []string         `json:"warnings,omitempty"`
}

// ---- 权重快照 ----

// WeightSnapshot 权重形成快照（设计 §7.2）。时间上只影响其后的重平衡周期；
// 字段完整记录权重来源：训练起止日、样本数、数据 hash、原始估计、收缩、
// 最终权重与 fallback 原因（空 = 正常）。
type WeightSnapshot struct {
	TrainStart     string             `json:"trainStart"`
	TrainEnd       string             `json:"trainEnd"`
	SampleCount    int                `json:"sampleCount"`
	RawEstimates   map[string]float64 `json:"rawEstimates,omitempty"`
	Shrinkage      float64            `json:"shrinkage"`
	ShrunkValues   map[string]float64 `json:"shrunkValues,omitempty"`
	FinalWeights   map[string]float64 `json:"finalWeights"`
	Directions     map[string]string  `json:"directions"`
	DataHash       string             `json:"dataHash"`
	FallbackReason string             `json:"fallbackReason,omitempty"`
	Method         string             `json:"method"`
	// MaxAbsWeight 权重形成时的单因子绝对上限（冗余诊断据此统计触顶次数）。
	MaxAbsWeight float64 `json:"maxAbsWeight"`
}

// weightFormationMethod 权重形成方法的稳定描述（写入快照 Method）。
const weightFormationMethod = "mean_ic -> shrink_to_equal_weight -> non_negative+cap -> simplex_box"

// ---- 等权辅助 ----

// EqualWeights 构造等权权重 map（1/n）。
func EqualWeights(keys []string) map[string]float64 {
	w := make(map[string]float64, len(keys))
	if len(keys) == 0 {
		return w
	}
	v := 1.0 / float64(len(keys))
	for _, k := range keys {
		w[k] = v
	}
	return w
}

// ---- 合成 ----

// Combine 多因子合成（设计 §7.1）。factors 为方向统一后的逐日截面；
// weights 为每因子权重（nil 表示等权 1/n，供滚动 IC 权重模型传入快照权重）。
// missing 复用 Task 1 枚举：exclude / cross_section_median / renormalize_available。
func Combine(factors []FactorSeries, missing string, weights map[string]float64) (CombineReport, error) {
	switch missing {
	case TransformMissingExclude, TransformMissingCrossSectionMedian, TransformMissingRenormalizeAvailable:
	default:
		return CombineReport{}, fmt.Errorf("missing 非法: %q（应为 exclude | cross_section_median | renormalize_available）", missing)
	}
	if err := validateFactors(factors); err != nil {
		return CombineReport{}, err
	}
	keys := factorKeys(factors)
	resolved, err := resolveWeights(keys, weights)
	if err != nil {
		return CombineReport{}, err
	}

	dates := factorDateUnion(factors)
	report := CombineReport{
		EqualWeight: 1.0 / float64(len(keys)),
		Coverage:    factorCoverage(factors),
	}
	for _, d := range dates {
		dfs, err := buildDayFactors(factors, d)
		if err != nil {
			return CombineReport{}, err
		}
		res, err := combineDay(d, dfs, missing, resolved)
		if err != nil {
			return CombineReport{}, err
		}
		if len(res.Scores) == 0 {
			report.Warnings = append(report.Warnings, fmt.Sprintf("日期 %s 无可合成股票（缺失策略 %s）", d, missing))
		}
		report.Results = append(report.Results, res)
	}
	return report, nil
}

// factorKeys 按输入顺序取因子 key。
func factorKeys(factors []FactorSeries) []string {
	keys := make([]string, len(factors))
	for i, f := range factors {
		keys[i] = f.Key
	}
	return keys
}

// resolveWeights nil → 等权；非 nil → 校验覆盖全部因子且有限非负。
func resolveWeights(keys []string, weights map[string]float64) (map[string]float64, error) {
	if weights == nil {
		return EqualWeights(keys), nil
	}
	for _, k := range keys {
		w, ok := weights[k]
		if !ok {
			return nil, fmt.Errorf("权重缺少因子 %q", k)
		}
		if math.IsNaN(w) || math.IsInf(w, 0) || w < 0 {
			return nil, fmt.Errorf("因子 %q 权重非法: %v（应为有限非负）", k, w)
		}
	}
	return weights, nil
}

// validateFactors 校验因子列表：非空、key 唯一、方向合法、主周期 >=1、有截面。
func validateFactors(factors []FactorSeries) error {
	if len(factors) == 0 {
		return fmt.Errorf("因子列表不能为空")
	}
	seen := make(map[string]bool, len(factors))
	for i, f := range factors {
		if f.Key == "" {
			return fmt.Errorf("factors[%d].key 不能为空", i)
		}
		if seen[f.Key] {
			return fmt.Errorf("因子 key 重复: %q", f.Key)
		}
		seen[f.Key] = true
		switch f.Direction {
		case DirectionHigherIsBetter, DirectionLowerIsBetter:
		default:
			return fmt.Errorf("factors[%d] direction 非法: %q（应为 higher_is_better | lower_is_better）", i, f.Direction)
		}
		if f.Horizon < 1 {
			return fmt.Errorf("factors[%d] horizon 无效: %d（应为 >=1）", i, f.Horizon)
		}
		if len(f.Days) == 0 {
			return fmt.Errorf("factors[%d] 无截面数据", i)
		}
	}
	return nil
}

// factorDateUnion 全部因子日期并集（升序，YYYY-MM-DD 字典序 = 时间序）。
func factorDateUnion(factors []FactorSeries) []string {
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

// dayFactor 单日单因子的对齐输入（行按代码索引 + 当日有效截面中位数）。
type dayFactor struct {
	key       string
	rows      map[string]TransformedRow
	median    float64
	hasMedian bool
}

// buildDayFactors 把某日的因子截面构建为对齐输入。某因子该日无截面时 rows 为空。
func buildDayFactors(factors []FactorSeries, date string) ([]dayFactor, error) {
	out := make([]dayFactor, len(factors))
	for i, f := range factors {
		df := dayFactor{key: f.Key, rows: map[string]TransformedRow{}}
		for _, r := range f.Days[date] {
			if r.Code == "" {
				return nil, fmt.Errorf("因子 %q 日期 %s 存在空代码行", f.Key, date)
			}
			if _, dup := df.rows[r.Code]; dup {
				return nil, fmt.Errorf("因子 %q 日期 %s 代码重复: %q", f.Key, date, r.Code)
			}
			df.rows[r.Code] = r
		}
		df.median, df.hasMedian = dayMedian(df.rows)
		out[i] = df
	}
	return out, nil
}

// dayMedian 当日有效 Final 的中位数（仅 cross_section_median 用）。
func dayMedian(rows map[string]TransformedRow) (float64, bool) {
	var vals []float64
	for _, r := range rows {
		if finalFinite(r) {
			vals = append(vals, r.Final)
		}
	}
	if len(vals) == 0 {
		return 0, false
	}
	return medianOf(vals), true
}

// finalFinite 判断变换行是否携带可合成的最终值（NaN/±Inf 视为缺失）。
func finalFinite(r TransformedRow) bool {
	return !math.IsNaN(r.Final) && !math.IsInf(r.Final, 0)
}

// combineDay 单日合成（纯函数，三种缺失策略）。
// exclude：任一因子该股缺失则不产出分数；
// cross_section_median：缺失值用该因子当日截面中位数填充（无有效截面则不可合成）；
// renormalize_available：按该股可用因子把名义权重重归一化，输出实际权重分布与漂移。
func combineDay(date string, factors []dayFactor, missing string, weights map[string]float64) (CombineResult, error) {
	codeSet := make(map[string]bool)
	for _, f := range factors {
		for c := range f.rows {
			codeSet[c] = true
		}
	}
	codes := make([]string, 0, len(codeSet))
	for c := range codeSet {
		codes = append(codes, c)
	}
	sort.Strings(codes)

	scores := make(map[string]float64)
	actualSum := make(map[string]float64, len(factors))
	actualCount := 0

	switch missing {
	case TransformMissingExclude:
		for _, c := range codes {
			sc, ok := 0.0, true
			for _, f := range factors {
				r, has := f.rows[c]
				if !has || !finalFinite(r) {
					ok = false
					break
				}
				sc += weights[f.key] * r.Final
			}
			if ok {
				scores[c] = sc
			}
		}
	case TransformMissingCrossSectionMedian:
		for _, c := range codes {
			sc, ok := 0.0, true
			for _, f := range factors {
				r, has := f.rows[c]
				var v float64
				if has && finalFinite(r) {
					v = r.Final
				} else if f.hasMedian {
					v = f.median
				} else {
					ok = false
					break
				}
				sc += weights[f.key] * v
			}
			if ok {
				scores[c] = sc
			}
		}
	case TransformMissingRenormalizeAvailable:
		for _, c := range codes {
			var avail []dayFactor
			var wsum float64
			for _, f := range factors {
				r, has := f.rows[c]
				if has && finalFinite(r) {
					avail = append(avail, f)
					wsum += weights[f.key]
				}
			}
			if len(avail) == 0 || wsum <= 0 {
				continue
			}
			sc := 0.0
			for _, f := range avail {
				w := weights[f.key] / wsum
				sc += w * f.rows[c].Final
				actualSum[f.key] += w
			}
			scores[c] = sc
			actualCount++
		}
	default:
		return CombineResult{}, fmt.Errorf("missing 非法: %q", missing)
	}

	res := CombineResult{Date: date, Scores: scores, Weights: map[string]float64{}}
	for _, f := range factors {
		if missing == TransformMissingRenormalizeAvailable {
			if actualCount > 0 {
				res.Weights[f.key] = actualSum[f.key] / float64(actualCount)
			} else {
				res.Weights[f.key] = 0
			}
			res.WeightDrift += math.Abs(res.Weights[f.key] - weights[f.key])
		} else {
			res.Weights[f.key] = weights[f.key]
		}
	}
	if missing == TransformMissingRenormalizeAvailable && len(factors) > 0 {
		res.WeightDrift /= float64(len(factors))
	}
	return res, nil
}

// factorCoverage 每因子覆盖汇总（有效日数与平均有效股票数）。
func factorCoverage(factors []FactorSeries) []FactorCoverage {
	out := make([]FactorCoverage, len(factors))
	for i, f := range factors {
		c := FactorCoverage{Key: f.Key, Direction: f.Direction}
		total := 0.0
		for _, rows := range f.Days {
			cnt := 0
			for _, r := range rows {
				if finalFinite(r) {
					cnt++
				}
			}
			if cnt > 0 {
				c.ValidDays++
				total += float64(cnt)
			}
		}
		if c.ValidDays > 0 {
			c.AvgStocks = total / float64(c.ValidDays)
		}
		out[i] = c
	}
	return out
}

// ---- 滚动 IC 权重训练器 ----

// TrainRollingIC 滚动 IC 权重训练（设计 §7.2）。view 只含训练期数据（训练器
// 不接受测试视图，类型隔离防泄漏）。返回的权重快照时间上只影响其后的重平衡周期。
//
// 权重形成方法（记录于快照 Method）：
//  1. 训练窗内按每因子主周期计算截面 IC（Spearman：Final 与未来 H 日收益），
//     原始估计 = IC 均值；
//  2. 向等权收缩：s_j = (1-λ)*e_j + λ*(1/n)，λ = spec.Shrinkage；
//  3. 非负 + 绝对上限截断：v_j = clamp(max(s_j,0), 0, maxAbsWeight)；
//     负估计不给负权重（不悄悄翻转冻结方向）；
//  4. 水填充投影到 {Σw=1, 0≤w≤maxAbsWeight}（无解时返回错误）；
//  5. 有效 IC 样本日数不足 minICDays 或估计全零时，按协议预选 fallback
//     （equal_weight → 等权；cash → 全 0），不得临时择优。
func TrainRollingIC(spec RollingICSpec, view TrainingView, factors []FactorSeries) (WeightSnapshot, error) {
	if err := (CombinationSpec{Method: CombinationRollingICWeight, RollingIC: &spec}).Validate(); err != nil {
		return WeightSnapshot{}, err
	}
	if err := validateFactors(factors); err != nil {
		return WeightSnapshot{}, err
	}
	if err := view.Validate(); err != nil {
		return WeightSnapshot{}, err
	}
	keys := factorKeys(factors)
	n := len(factors)

	windowDates := trainingWindow(view.Dates, spec.WindowYears)
	snap := WeightSnapshot{
		TrainStart:   windowDates[0],
		TrainEnd:     windowDates[len(windowDates)-1],
		Shrinkage:    spec.Shrinkage,
		Directions:   factorDirections(factors),
		Method:       weightFormationMethod,
		MaxAbsWeight: spec.MaxAbsWeight,
	}
	hash, err := trainingViewHash(view)
	if err != nil {
		return WeightSnapshot{}, err
	}
	snap.DataHash = hash

	// 逐因子 IC 序列与最小有效日数。
	ics := make([][]float64, n)
	minValid := len(windowDates)
	for i, f := range factors {
		seq := factorICSeries(f, view, windowDates)
		ics[i] = seq
		if len(seq) < minValid {
			minValid = len(seq)
		}
	}
	snap.SampleCount = minValid

	if minValid < minICDays {
		return applyFallback(snap, spec.Fallback, fmt.Sprintf("有效 IC 样本日数 %d 小于最小要求 %d", minValid, minICDays), keys), nil
	}

	// 原始估计 = IC 均值；向等权收缩；非负 + 上限截断。
	raw := make(map[string]float64, n)
	shrunk := make(map[string]float64, n)
	vec := make([]float64, n)
	for i, f := range factors {
		m := meanOf(ics[i])
		raw[f.Key] = m
		s := (1-spec.Shrinkage)*m + spec.Shrinkage*(1.0/float64(n))
		shrunk[f.Key] = s
		vec[i] = math.Min(math.Max(s, 0), spec.MaxAbsWeight)
	}
	snap.RawEstimates = raw
	snap.ShrunkValues = shrunk

	sum := 0.0
	for _, v := range vec {
		sum += v
	}
	if sum <= 0 {
		return applyFallback(snap, spec.Fallback, "权重估计全零（无正 IC 信号）", keys), nil
	}

	w, err := projectSimplexBox(vec, spec.MaxAbsWeight)
	if err != nil {
		return WeightSnapshot{}, err
	}
	final := make(map[string]float64, n)
	for i, f := range factors {
		final[f.Key] = w[i]
	}
	snap.FinalWeights = final
	return snap, nil
}

// factorDirections 因子 key → 冻结方向（审计）。
func factorDirections(factors []FactorSeries) map[string]string {
	d := make(map[string]string, len(factors))
	for _, f := range factors {
		d[f.Key] = f.Direction
	}
	return d
}

// trainingWindow 固定滚动训练窗 = 视图日期最后 windowYears*250 个交易日。
func trainingWindow(dates []string, years int) []string {
	max := years * tradingDaysPerYear
	if len(dates) <= max {
		return dates
	}
	return dates[len(dates)-max:]
}

// factorICSeries 训练窗内某因子的截面 IC 序列（Spearman：Final 与未来 H 日收益）。
// 收益从训练视图自身计算（price[t+H]/price[t]-1），无未来收益或配对不足的日期剔除。
func factorICSeries(f FactorSeries, view TrainingView, windowDates []string) []float64 {
	pos := make(map[string]int, len(view.Dates))
	for i, d := range view.Dates {
		pos[d] = i
	}
	H := f.Horizon
	var ics []float64
	for _, d := range windowDates {
		i, ok := pos[d]
		if !ok {
			continue
		}
		fwd := i + H
		if fwd >= len(view.Dates) {
			continue // 训练视图内无未来收益
		}
		fwdDate := view.Dates[fwd]
		var xs, ys []float64
		for _, r := range f.Days[d] {
			if !finalFinite(r) {
				continue
			}
			p0, ok0 := view.Price[d][r.Code]
			p1, ok1 := view.Price[fwdDate][r.Code]
			if !ok0 || !ok1 || p0 <= 0 || p1 <= 0 {
				continue
			}
			xs = append(xs, r.Final)
			ys = append(ys, p1/p0-1)
		}
		if len(xs) >= minPairsPerDay {
			if ic := spearman(xs, ys); !math.IsNaN(ic) && !math.IsInf(ic, 0) {
				ics = append(ics, ic)
			}
		}
	}
	return ics
}

// applyFallback 按协议预选策略回退（等权或现金），记录原因并清空估计值。
func applyFallback(snap WeightSnapshot, fallback, reason string, keys []string) WeightSnapshot {
	snap.FallbackReason = reason
	snap.RawEstimates = nil
	snap.ShrunkValues = nil
	switch fallback {
	case CombinationFallbackEqualWeight:
		snap.FinalWeights = EqualWeights(keys)
	case CombinationFallbackCash:
		snap.FinalWeights = make(map[string]float64, len(keys))
		for _, k := range keys {
			snap.FinalWeights[k] = 0
		}
	default:
		// spec.Validate 已保证合法。
		snap.FinalWeights = EqualWeights(keys)
	}
	return snap
}

// projectSimplexBox 把非负权重投影到 {Σw=1, 0≤w≤cap}（水填充，单调收敛）。
// 无解（cap*因子数 < 1）时返回错误，不静默放宽约束。
func projectSimplexBox(v []float64, cap float64) ([]float64, error) {
	sum := 0.0
	for _, x := range v {
		sum += x
	}
	if math.IsNaN(sum) || math.IsInf(sum, 0) || sum <= 0 {
		return nil, fmt.Errorf("权重估计总和非法: %v", sum)
	}
	w := make([]float64, len(v))
	for i := range v {
		w[i] = v[i] / sum
	}
	for iter := 0; iter < 100; iter++ {
		clipped := 0.0
		unclipSum := 0.0
		nUnclip := 0
		for i := range w {
			if w[i] > cap+1e-12 {
				clipped += w[i] - cap
				w[i] = cap
			} else {
				unclipSum += w[i]
				nUnclip++
			}
		}
		if clipped <= 1e-12 || nUnclip == 0 || unclipSum == 0 {
			break
		}
		for i := range w {
			if w[i] < cap {
				w[i] += clipped * (w[i] / unclipSum)
			}
		}
	}
	s := 0.0
	for _, x := range w {
		s += x
	}
	if math.Abs(s-1) > weightTolerance {
		return nil, fmt.Errorf("权重上限与因子数冲突（Σ=%.4f，cap*%d < 1）", s, len(w))
	}
	for _, x := range w {
		if x > cap+1e-9 {
			return nil, fmt.Errorf("权重 %v 超过上限 %v", x, cap)
		}
	}
	return w, nil
}

// trainingViewHash 训练视图的数据 hash（日期 + 价格规范化序列化；json 对
// map 键排序，输出确定）。
func trainingViewHash(v TrainingView) (string, error) {
	type viewJSON struct {
		Dates []string                      `json:"dates"`
		Price map[string]map[string]float64 `json:"price"`
	}
	buf, err := json.Marshal(viewJSON{Dates: v.Dates, Price: v.Price})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(buf)
	return hex.EncodeToString(sum[:]), nil
}

// ---- 统计辅助 ----

// meanOf 均值（空切片返回 0）。
func meanOf(vals []float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	s := 0.0
	for _, v := range vals {
		s += v
	}
	return s / float64(len(vals))
}

// pearson 皮尔逊相关；样本 < 2 或任一侧零方差时返回 0（无离差相关无定义）。
func pearson(xs, ys []float64) float64 {
	n := len(xs)
	if n < 2 {
		return 0
	}
	mx, my := meanOf(xs), meanOf(ys)
	var sxx, syy, sxy float64
	for i := range xs {
		dx := xs[i] - mx
		dy := ys[i] - my
		sxx += dx * dx
		syy += dy * dy
		sxy += dx * dy
	}
	if sxx == 0 || syy == 0 {
		return 0
	}
	return sxy / math.Sqrt(sxx*syy)
}

// spearman Spearman 秩相关 = 平均秩映射后的 Pearson（并列共享平均秩）。
// 并列判定用容差（withinTie）：OLS 残差等浮点运算会产生 -0.5 与
// -0.5000000000000004 这类噪声差异，精确相等会破坏真实并列（MEMORY 坑点：
// 并列秩必须共享平均秩，否则 Spearman 失真）。
func spearman(xs, ys []float64) float64 {
	return pearson(rankValues(xs), rankValues(ys))
}

// rankValues 平均秩映射 [-1,1]：score = 2*r/(n+1) - 1，r 为 1 起始平均秩。
// 与 transform.go 的 rankScores 口径一致，仅并列判定改为容差感知：
// |a-b| <= 1e-12*max(1,|a|,|b|) 视为并列（防御浮点噪声破坏真实并列）。
func rankValues(vals []float64) []float64 {
	n := len(vals)
	order := make([]int, n)
	for i := range order {
		order[i] = i
	}
	sort.Slice(order, func(a, b int) bool { return vals[order[a]] < vals[order[b]] })
	ranks := make([]float64, n)
	for i := 0; i < n; {
		j := i + 1
		for j < n && withinTie(vals[order[j]], vals[order[i]]) {
			j++
		}
		avg := float64(i+1+j) / 2 // 并列块平均秩（1 起始）
		for k := i; k < j; k++ {
			ranks[order[k]] = avg
		}
		i = j
	}
	scores := make([]float64, n)
	for i := 0; i < n; i++ {
		scores[i] = 2*ranks[i]/float64(n+1) - 1
	}
	return scores
}

// withinTie 容差并列判定：相对 1e-12 视为并列（仅吸收浮点噪声，不吞真实差异）。
func withinTie(a, b float64) bool {
	return math.Abs(a-b) <= 1e-12*math.Max(1, math.Max(math.Abs(a), math.Abs(b)))
}
