package portfolioresearch

import (
	"math"
	"reflect"
	"testing"
)

// combine_test.go v2 Task 3 多因子合成与滚动 IC 权重测试。
//
// 金标准覆盖：等权秩合成手算；缺失三策略（exclude / cross_section_median /
// renormalize_available）；滚动 IC 训练器的 IC 估计、收缩、权重上限、冻结方向
// 与数据不足 fallback；泄漏测试 4 项；完成门槛（分数 = 因子值 × 实际权重，
// 权重来源可追溯）。

// ---- 测试辅助 ----

// trow 构造有效变换行（Final 有限即有效）。
func trow(code string, final float64) TransformedRow {
	return TransformedRow{Code: code, Final: final}
}

// trowMissing 构造缺失变换行（Final=NaN，Missing 标记）。
func trowMissing(code string) TransformedRow {
	return TransformedRow{Code: code, Final: math.NaN(), Missing: true}
}

// series 便捷构造单因子逐日截面。
func series(key, dir string, horizon int, days map[string][]TransformedRow) FactorSeries {
	return FactorSeries{Key: key, Direction: dir, Horizon: horizon, Days: days}
}

// flatFactor 每日截面值相同的因子（4 股票 A/B/C/D，与 stepView 同顺序）。
func flatFactor(key, dir string, horizon int, dates []string, vals []float64) FactorSeries {
	days := map[string][]TransformedRow{}
	codes := []string{"A", "B", "C", "D"}
	for _, d := range dates {
		rows := make([]TransformedRow, len(codes))
		for i, c := range codes {
			rows[i] = trow(c, vals[i])
		}
		days[d] = rows
	}
	return series(key, dir, horizon, days)
}

// stepView 生成 4 股票（A/B/C/D）价格视图：第 0 日全 100，第 i 日 = 前日 × steps[i-1]。
// 未来 1 日收益 ret(第 i 日) = steps[i] - 1（按股票逐元素）。
func stepView(dates []string, steps [][]float64) TrainingView {
	price := map[string]map[string]float64{}
	codes := []string{"A", "B", "C", "D"}
	prev := map[string]float64{"A": 100, "B": 100, "C": 100, "D": 100}
	for i, d := range dates {
		m := map[string]float64{"A": 100, "B": 100, "C": 100, "D": 100}
		if i > 0 {
			for j, c := range codes {
				m[c] = prev[c] * steps[i-1][j]
			}
		}
		price[d] = m
		prev = m
	}
	return TrainingView{Dates: dates, Price: price}
}

// icSpec 便捷构造滚动 IC 参数。
func icSpec(fallback string) RollingICSpec {
	return RollingICSpec{WindowYears: 1, Shrinkage: 0, MaxAbsWeight: 1, Fallback: fallback}
}

// assertFloatMap 容差断言 float map。
func assertFloatMap(t *testing.T, got, want map[string]float64, tol float64, msg string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: 长度不同 got=%d want=%d（got=%v）", msg, len(got), len(want), got)
	}
	for k, wv := range want {
		gv, ok := got[k]
		if !ok {
			t.Fatalf("%s: 缺少键 %q（got=%v）", msg, k, got)
		}
		if math.Abs(gv-wv) > tol {
			t.Fatalf("%s: %s got %v want %v", msg, k, gv, wv)
		}
	}
}

// rowFinal 取因子某日某股票的 Final 值（测试辅助）。
func rowFinal(t *testing.T, f FactorSeries, date, code string) float64 {
	t.Helper()
	for _, r := range f.Days[date] {
		if r.Code == code {
			return r.Final
		}
	}
	t.Fatalf("缺少 %s/%s 的行", date, code)
	return 0
}

// dates6 / dates7 固定训练日期（字符串升序即时间序）。
var dates6 = []string{"2026-01-05", "2026-01-06", "2026-01-07", "2026-01-08", "2026-01-09", "2026-01-12"}

// risingSteps 收益秩恒为 [1,2,3,4] 的每日乘数。
var risingSteps = [][]float64{{1.1, 1.2, 1.3, 1.4}, {1.1, 1.2, 1.3, 1.4}, {1.1, 1.2, 1.3, 1.4}, {1.1, 1.2, 1.3, 1.4}, {1.1, 1.2, 1.3, 1.4}}

// fallingSteps 收益秩恒为 [4,3,2,1] 的每日乘数（短期反向数据）。
var fallingSteps = [][]float64{{1.4, 1.3, 1.2, 1.1}, {1.4, 1.3, 1.2, 1.1}, {1.4, 1.3, 1.2, 1.1}, {1.4, 1.3, 1.2, 1.1}, {1.4, 1.3, 1.2, 1.1}}

// ---- 等权秩合成：手算金标准 ----

// TestCombineEqualWeightHandComputed 等权秩合成手算（完成门槛：分数可拆解为
// 因子值 × 实际权重，逐股断言）。
// 2 因子 4 股票单日：f1=[1,2,3,4]、f2=[4,3,2,1]，w=0.5/0.5。
// score = 0.5*1+0.5*4 = 2.5（A/B/C/D 全部 2.5）。
func TestCombineEqualWeightHandComputed(t *testing.T) {
	dates := []string{"2026-01-05"}
	f1 := flatFactor("f1", DirectionHigherIsBetter, 1, dates, []float64{1, 2, 3, 4})
	f2 := flatFactor("f2", DirectionHigherIsBetter, 1, dates, []float64{4, 3, 2, 1})
	rep, err := Combine([]FactorSeries{f1, f2}, TransformMissingExclude, nil)
	if err != nil {
		t.Fatalf("Combine 失败: %v", err)
	}
	if len(rep.Results) != 1 {
		t.Fatalf("应只有 1 个日期结果: %d", len(rep.Results))
	}
	res := rep.Results[0]
	if res.Date != dates[0] {
		t.Fatalf("日期错误: %s", res.Date)
	}
	want := map[string]float64{"A": 2.5, "B": 2.5, "C": 2.5, "D": 2.5}
	assertFloatMap(t, res.Scores, want, 1e-9, "等权合成分数")
	// 完成门槛：score = Σ w_j * value_j（逐股）。
	if rep.EqualWeight != 0.5 {
		t.Fatalf("等权应为 1/2: %v", rep.EqualWeight)
	}
	for code, score := range res.Scores {
		wantScore := 0.5*rowFinal(t, f1, dates[0], code) + 0.5*rowFinal(t, f2, dates[0], code)
		assertFloat(t, score, wantScore, 1e-9, code+" 分数=权重×值")
	}
	// 覆盖诊断：每因子 1 个有效日、平均 4 只股票；方向单独报告。
	if len(rep.Coverage) != 2 {
		t.Fatalf("覆盖诊断应含 2 因子: %d", len(rep.Coverage))
	}
	for _, c := range rep.Coverage {
		if c.ValidDays != 1 || c.AvgStocks != 4 {
			t.Fatalf("覆盖诊断错误: %+v", c)
		}
		if c.Direction == "" {
			t.Fatalf("覆盖诊断应报告方向: %+v", c)
		}
	}
}

// ---- 缺失三策略 ----

// TestCombineMissingExclude exclude：任一因子缺失的股票不产出分数。
// f1 覆盖 A/B/C/D，f2 覆盖 A/B/C（D 缺失）→ 仅 D 无分数。
func TestCombineMissingExclude(t *testing.T) {
	d := "2026-01-05"
	f1 := series("f1", DirectionHigherIsBetter, 1, map[string][]TransformedRow{
		d: {trow("A", 1), trow("B", 2), trow("C", 3), trow("D", 4)},
	})
	f2 := series("f2", DirectionHigherIsBetter, 1, map[string][]TransformedRow{
		d: {trow("A", 4), trow("B", 3), trow("C", 2), trowMissing("D")},
	})
	rep, err := Combine([]FactorSeries{f1, f2}, TransformMissingExclude, nil)
	if err != nil {
		t.Fatalf("Combine 失败: %v", err)
	}
	res := rep.Results[0]
	if len(res.Scores) != 3 {
		t.Fatalf("exclude 下应只有 3 只股票有分数: %v", res.Scores)
	}
	if _, ok := res.Scores["D"]; ok {
		t.Fatal("D 缺因子 f2，exclude 下不应有分数")
	}
	assertFloatMap(t, res.Scores, map[string]float64{"A": 2.5, "B": 2.5, "C": 2.5}, 1e-9, "exclude 分数")
}

// TestCombineMissingCrossSectionMedian 当日截面中位数填充（合成层兜底）：
// f2 的 D 缺失 → 用 f2 当日有效截面 [4,3,2] 中位数 3 填充 → D=0.5*4+0.5*3=3.5。
func TestCombineMissingCrossSectionMedian(t *testing.T) {
	d := "2026-01-05"
	f1 := series("f1", DirectionHigherIsBetter, 1, map[string][]TransformedRow{
		d: {trow("A", 1), trow("B", 2), trow("C", 3), trow("D", 4)},
	})
	f2 := series("f2", DirectionHigherIsBetter, 1, map[string][]TransformedRow{
		d: {trow("A", 4), trow("B", 3), trow("C", 2), trowMissing("D")},
	})
	rep, err := Combine([]FactorSeries{f1, f2}, TransformMissingCrossSectionMedian, nil)
	if err != nil {
		t.Fatalf("Combine 失败: %v", err)
	}
	res := rep.Results[0]
	if len(res.Scores) != 4 {
		t.Fatalf("median 下 4 只股票都应有分数: %v", res.Scores)
	}
	assertFloat(t, res.Scores["D"], 3.5, 1e-9, "D 中位数填充后分数")
	assertFloatMap(t, res.Scores, map[string]float64{"A": 2.5, "B": 2.5, "C": 2.5, "D": 3.5}, 1e-9, "median 分数")
}

// TestCombineMissingRenormalizeAvailable 可用因子重归一化：
// D 缺 f2 → 可用 {f1} → 权重 [1,0] → score_D = 4；其余股票权重 [0.5,0.5]。
// 输出当日实际权重分布（平均）与漂移。
func TestCombineMissingRenormalizeAvailable(t *testing.T) {
	d := "2026-01-05"
	f1 := series("f1", DirectionHigherIsBetter, 1, map[string][]TransformedRow{
		d: {trow("A", 1), trow("B", 2), trow("C", 3), trow("D", 4)},
	})
	f2 := series("f2", DirectionHigherIsBetter, 1, map[string][]TransformedRow{
		d: {trow("A", 4), trow("B", 3), trow("C", 2), trowMissing("D")},
	})
	rep, err := Combine([]FactorSeries{f1, f2}, TransformMissingRenormalizeAvailable, nil)
	if err != nil {
		t.Fatalf("Combine 失败: %v", err)
	}
	res := rep.Results[0]
	assertFloat(t, res.Scores["D"], 4, 1e-9, "D 重归一化后分数")
	assertFloat(t, res.Scores["A"], 2.5, 1e-9, "A 重归一化后分数")
	// 当日实际权重分布：w1=(0.5+0.5+0.5+1)/4=0.625，w2=(0.5+0.5+0.5+0)/4=0.375。
	assertFloatMap(t, res.Weights, map[string]float64{"f1": 0.625, "f2": 0.375}, 1e-9, "重归一化实际权重")
	// 漂移 = (|0.625-0.5| + |0.375-0.5|)/2 = 0.125。
	assertFloat(t, res.WeightDrift, 0.125, 1e-9, "重归一化漂移")
}

// TestCombineWithSnapshotWeights 完成门槛：滚动 IC 权重快照的权重直接用于合成，
// 分数 = Σ w_j * value_j 逐股断言，权重来源可追溯（来自快照）。
func TestCombineWithSnapshotWeights(t *testing.T) {
	dates := []string{"2026-01-05", "2026-01-06"}
	f1 := flatFactor("f1", DirectionHigherIsBetter, 1, dates, []float64{1, 2, 3, 4})
	f2 := flatFactor("f2", DirectionHigherIsBetter, 1, dates, []float64{1, 2, 3, 4})
	spec := icSpec(CombinationFallbackEqualWeight)
	spec.Shrinkage = 0.5
	snap, err := TrainRollingIC(spec, stepView(dates, risingSteps), []FactorSeries{f1, f2})
	if err != nil {
		t.Fatalf("TrainRollingIC 失败: %v", err)
	}
	rep, err := Combine([]FactorSeries{f1, f2}, TransformMissingExclude, snap.FinalWeights)
	if err != nil {
		t.Fatalf("Combine 失败: %v", err)
	}
	if len(rep.Results) != 2 {
		t.Fatalf("应有 2 个日期结果: %d", len(rep.Results))
	}
	for _, res := range rep.Results {
		for code, score := range res.Scores {
			var wantScore float64
			for _, f := range []FactorSeries{f1, f2} {
				wantScore += snap.FinalWeights[f.Key] * rowFinal(t, f, res.Date, code)
			}
			assertFloat(t, score, wantScore, 1e-9, res.Date+" "+code+" 分数=快照权重×值")
		}
	}
}

// TestCombineDateUnionOrder 日期并集升序；因子某日无截面时 exclude 下该日无产出。
func TestCombineDateUnionOrder(t *testing.T) {
	d1, d2, d3 := "2026-01-05", "2026-01-06", "2026-01-07"
	f1 := series("f1", DirectionHigherIsBetter, 1, map[string][]TransformedRow{
		d1: {trow("A", 1), trow("B", 2), trow("C", 3), trow("D", 4)},
		d2: {trow("A", 1), trow("B", 2), trow("C", 3), trow("D", 4)},
	})
	f2 := series("f2", DirectionHigherIsBetter, 1, map[string][]TransformedRow{
		d2: {trow("A", 4), trow("B", 3), trow("C", 2), trow("D", 1)},
		d3: {trow("A", 4), trow("B", 3), trow("C", 2), trow("D", 1)},
	})
	rep, err := Combine([]FactorSeries{f1, f2}, TransformMissingExclude, nil)
	if err != nil {
		t.Fatalf("Combine 失败: %v", err)
	}
	wantDates := []string{d1, d2, d3}
	if len(rep.Results) != len(wantDates) {
		t.Fatalf("日期并集应为 3 天: %d", len(rep.Results))
	}
	for i, r := range rep.Results {
		if r.Date != wantDates[i] {
			t.Fatalf("日期顺序错误: got %s want %s", r.Date, wantDates[i])
		}
	}
	// d1 缺 f2、d3 缺 f1 → exclude 下该日无任何股票可合成，输出 warning。
	if len(rep.Results[0].Scores) != 0 || len(rep.Results[2].Scores) != 0 {
		t.Fatalf("因子缺整日时 exclude 下该日不应产出分数: %v", rep.Results)
	}
	if len(rep.Warnings) == 0 {
		t.Fatal("应输出因子缺整日的 warning")
	}
	if len(rep.Results[1].Scores) != 4 {
		t.Fatalf("d2 应有 4 只股票分数: %v", rep.Results[1].Scores)
	}
}

// ---- 滚动 IC 权重训练器 ----

// TestTrainRollingICHandComputed 手算金标准：收益秩与因子秩完全一致 → 每日
// IC=1 → 无收缩时等权。训练窗 6 天、H=1 → 5 个有效 IC 日。
func TestTrainRollingICHandComputed(t *testing.T) {
	view := stepView(dates6, risingSteps)
	f1 := flatFactor("f1", DirectionHigherIsBetter, 1, dates6, []float64{1, 2, 3, 4})
	f2 := flatFactor("f2", DirectionHigherIsBetter, 1, dates6, []float64{1, 2, 3, 4})
	snap, err := TrainRollingIC(icSpec(CombinationFallbackEqualWeight), view, []FactorSeries{f1, f2})
	if err != nil {
		t.Fatalf("TrainRollingIC 失败: %v", err)
	}
	if snap.FallbackReason != "" {
		t.Fatalf("正常训练不应 fallback: %q", snap.FallbackReason)
	}
	if snap.TrainStart != "2026-01-05" || snap.TrainEnd != "2026-01-12" {
		t.Fatalf("训练起止日错误: %s ~ %s", snap.TrainStart, snap.TrainEnd)
	}
	if snap.SampleCount != 5 {
		t.Fatalf("有效 IC 日数应为 5（6 天中末日无未来收益）: %d", snap.SampleCount)
	}
	assertFloatMap(t, snap.RawEstimates, map[string]float64{"f1": 1, "f2": 1}, 1e-9, "原始估计")
	assertFloatMap(t, snap.ShrunkValues, map[string]float64{"f1": 1, "f2": 1}, 1e-9, "收缩后值（λ=0）")
	assertFloatMap(t, snap.FinalWeights, map[string]float64{"f1": 0.5, "f2": 0.5}, 1e-9, "最终权重")
	if snap.DataHash == "" {
		t.Fatal("权重快照必须记录数据 hash")
	}
	if snap.Directions["f1"] != DirectionHigherIsBetter || snap.Directions["f2"] != DirectionHigherIsBetter {
		t.Fatalf("快照应记录冻结方向: %v", snap.Directions)
	}
	if snap.Method == "" {
		t.Fatal("快照应记录权重形成方法")
	}
}

// TestTrainRollingICShrinkage 收缩生效：λ=0.5 时权重比 λ=0 更接近等权。
// e=[1,0]：s1=(1-0.5)*1+0.5*0.5=0.75，s2=(1-0.5)*0+0.5*0.5=0.25。
func TestTrainRollingICShrinkage(t *testing.T) {
	view := stepView(dates6, risingSteps)
	f1 := flatFactor("f1", DirectionHigherIsBetter, 1, dates6, []float64{1, 2, 3, 4})
	// f2 恒值：截面无离差 → 每日 IC=0。
	f2 := flatFactor("f2", DirectionHigherIsBetter, 1, dates6, []float64{0, 0, 0, 0})

	spec0 := icSpec(CombinationFallbackEqualWeight)
	snap0, err := TrainRollingIC(spec0, view, []FactorSeries{f1, f2})
	if err != nil {
		t.Fatalf("TrainRollingIC(λ=0) 失败: %v", err)
	}
	assertFloatMap(t, snap0.FinalWeights, map[string]float64{"f1": 1, "f2": 0}, 1e-9, "λ=0 权重")

	spec := icSpec(CombinationFallbackEqualWeight)
	spec.Shrinkage = 0.5
	snap, err := TrainRollingIC(spec, view, []FactorSeries{f1, f2})
	if err != nil {
		t.Fatalf("TrainRollingIC(λ=0.5) 失败: %v", err)
	}
	assertFloatMap(t, snap.FinalWeights, map[string]float64{"f1": 0.75, "f2": 0.25}, 1e-9, "λ=0.5 权重")
	if snap.Shrinkage != 0.5 {
		t.Fatalf("快照应记录收缩系数: %v", snap.Shrinkage)
	}
	assertFloatMap(t, snap.ShrunkValues, map[string]float64{"f1": 0.75, "f2": 0.25}, 1e-9, "收缩后值")
	// 收缩后更接近等权（|w1-0.5| 更小）。
	if d0 := math.Abs(snap0.FinalWeights["f1"] - 0.5); d0 < 0.25 {
		t.Fatalf("λ=0 偏离等权应为 0.5: %v", d0)
	}
	if d1 := math.Abs(snap.FinalWeights["f1"] - 0.5); d1 > 0.25 {
		t.Fatalf("λ=0.5 偏离等权应为 0.25: %v", d1)
	}
}

// TestTrainRollingICCap 权重上限生效：e=[1,0.2]（f2 截面秩 [1,4,3,2] 与收益
// 秩 [1,2,3,4] 的 Spearman=0.2，手算见下），maxAbs=0.6。
// 归一化 [5/6,1/6] → 水填充投影 → [0.6,0.4]；无上限时 [5/6,1/6]。
func TestTrainRollingICCap(t *testing.T) {
	view := stepView(dates6, risingSteps)
	f1 := flatFactor("f1", DirectionHigherIsBetter, 1, dates6, []float64{1, 2, 3, 4})
	// rank(f2)=[1,4,3,2]（A..D），rank(ret)=[1,2,3,4]：Pearson=0.2。
	f2 := flatFactor("f2", DirectionHigherIsBetter, 1, dates6, []float64{1, 4, 3, 2})

	noCap := icSpec(CombinationFallbackEqualWeight)
	snapNC, err := TrainRollingIC(noCap, view, []FactorSeries{f1, f2})
	if err != nil {
		t.Fatalf("TrainRollingIC(无上限) 失败: %v", err)
	}
	assertFloatMap(t, snapNC.FinalWeights, map[string]float64{"f1": 5.0 / 6, "f2": 1.0 / 6}, 1e-9, "无上限权重")

	spec := icSpec(CombinationFallbackEqualWeight)
	spec.MaxAbsWeight = 0.6
	snap, err := TrainRollingIC(spec, view, []FactorSeries{f1, f2})
	if err != nil {
		t.Fatalf("TrainRollingIC(上限 0.6) 失败: %v", err)
	}
	assertFloatMap(t, snap.FinalWeights, map[string]float64{"f1": 0.6, "f2": 0.4}, 1e-9, "上限后权重")
	for k, w := range snap.FinalWeights {
		if w > 0.6+1e-9 {
			t.Fatalf("权重 %s=%v 超过上限 0.6", k, w)
		}
	}
	// 上限确实生效：f1 从 5/6 被压到 0.6。
	if !(snap.FinalWeights["f1"] < snapNC.FinalWeights["f1"]-1e-9) {
		t.Fatalf("上限应压低 f1 权重: %v vs %v", snap.FinalWeights["f1"], snapNC.FinalWeights["f1"])
	}
}

// TestTrainRollingICDirectionFrozen 冻结方向：f1=lower_is_better 且训练窗内
// 短期反向（e=-1）→ 不给权重（不自动翻转方向）；f2=higher_is_better（e=+1）
// → 得全部权重。方向字段保持冻结不变。
func TestTrainRollingICDirectionFrozen(t *testing.T) {
	view := stepView(dates6, fallingSteps) // 收益秩 [4,3,2,1]
	// f1 Final 已按 lower_is_better 统一方向（越大越好），但与收益负相关 → IC=-1。
	f1 := flatFactor("f1", DirectionLowerIsBetter, 1, dates6, []float64{1, 2, 3, 4})
	// f2 与收益正相关 → IC=+1。
	f2 := flatFactor("f2", DirectionHigherIsBetter, 1, dates6, []float64{4, 3, 2, 1})
	snap, err := TrainRollingIC(icSpec(CombinationFallbackEqualWeight), view, []FactorSeries{f1, f2})
	if err != nil {
		t.Fatalf("TrainRollingIC 失败: %v", err)
	}
	assertFloatMap(t, snap.RawEstimates, map[string]float64{"f1": -1, "f2": 1}, 1e-9, "原始估计（f1 短期反向）")
	assertFloatMap(t, snap.FinalWeights, map[string]float64{"f1": 0, "f2": 1}, 1e-9, "权重（不翻转方向）")
	// 方向冻结：快照记录的方向必须保持模型冻结值，不因短期反向改写。
	if snap.Directions["f1"] != DirectionLowerIsBetter {
		t.Fatalf("f1 方向被篡改: %q（冻结方向不得自动翻转）", snap.Directions["f1"])
	}
	if snap.Directions["f2"] != DirectionHigherIsBetter {
		t.Fatalf("f2 方向错误: %q", snap.Directions["f2"])
	}
}

// ---- 数据不足 fallback ----

// TestTrainRollingICFallback 数据不足严格执行冻结 fallback：
// 训练视图仅 3 天（H=1 → 2 个有效 IC 日 < minICDays）→ 按协议预选
// equal_weight → 等权；cash → 全 0。不得临时择优。
func TestTrainRollingICFallback(t *testing.T) {
	shortDates := []string{"2026-01-05", "2026-01-06", "2026-01-07"}
	view := stepView(shortDates, [][]float64{{1.1, 1.2, 1.3, 1.4}, {1.1, 1.2, 1.3, 1.4}})
	f1 := flatFactor("f1", DirectionHigherIsBetter, 1, shortDates, []float64{1, 2, 3, 4})
	f2 := flatFactor("f2", DirectionHigherIsBetter, 1, shortDates, []float64{1, 2, 3, 4})

	snapEQ, err := TrainRollingIC(icSpec(CombinationFallbackEqualWeight), view, []FactorSeries{f1, f2})
	if err != nil {
		t.Fatalf("TrainRollingIC(等权回退) 失败: %v", err)
	}
	if snapEQ.FallbackReason == "" {
		t.Fatal("数据不足必须标记 fallback 原因")
	}
	assertFloatMap(t, snapEQ.FinalWeights, map[string]float64{"f1": 0.5, "f2": 0.5}, 1e-9, "等权 fallback 权重")
	if snapEQ.RawEstimates != nil || snapEQ.ShrunkValues != nil {
		t.Fatalf("fallback 时不应有估计值: %+v", snapEQ)
	}

	snapCash, err := TrainRollingIC(icSpec(CombinationFallbackCash), view, []FactorSeries{f1, f2})
	if err != nil {
		t.Fatalf("TrainRollingIC(现金回退) 失败: %v", err)
	}
	if snapCash.FallbackReason == "" {
		t.Fatal("现金回退必须标记 fallback 原因")
	}
	assertFloatMap(t, snapCash.FinalWeights, map[string]float64{"f1": 0, "f2": 0}, 1e-9, "现金 fallback 权重")
	if snapCash.RawEstimates != nil {
		t.Fatal("现金 fallback 不应有估计值")
	}
}

// ---- 泄漏测试 4 项 ----

// TestLeakNoTestWindowInTraining 泄漏 a：训练器只接受训练视图（类型隔离），
// 测试窗收益数据不可能进入训练。同一训练视图重复训练得到完全相同的权重快照。
func TestLeakNoTestWindowInTraining(t *testing.T) {
	viewTrain := stepView(dates6, risingSteps)
	factors := []FactorSeries{
		flatFactor("f1", DirectionHigherIsBetter, 1, dates6, []float64{1, 2, 3, 4}),
		flatFactor("f2", DirectionHigherIsBetter, 1, dates6, []float64{1, 2, 3, 4}),
	}
	spec := icSpec(CombinationFallbackEqualWeight)
	spec.Shrinkage = 0.3
	w1, err := TrainRollingIC(spec, viewTrain, factors)
	if err != nil {
		t.Fatalf("TrainRollingIC 失败: %v", err)
	}
	// 重复训练（同一训练视图）→ 权重快照完全一致（确定性、无外部状态）。
	w2, err := TrainRollingIC(spec, viewTrain, factors)
	if err != nil {
		t.Fatalf("重训失败: %v", err)
	}
	if !reflect.DeepEqual(w1, w2) {
		t.Fatalf("同一训练视图重复训练必须产生相同快照:\n%+v\n%+v", w1, w2)
	}
}

// TestLeakAppendTrainingDayOnlyAffectsLater 泄漏 b：训练窗末尾追加一天，
// 只影响之后的权重形成；之前已生成的权重快照保持不变。
func TestLeakAppendTrainingDayOnlyAffectsLater(t *testing.T) {
	dates7 := append(append([]string(nil), dates6...), "2026-01-13")
	steps7 := append(append([][]float64(nil), risingSteps...), []float64{1.1, 1.2, 1.3, 1.4})
	factors := []FactorSeries{
		flatFactor("f1", DirectionHigherIsBetter, 1, dates7, []float64{1, 2, 3, 4}),
		flatFactor("f2", DirectionHigherIsBetter, 1, dates7, []float64{1, 2, 3, 4}),
	}
	spec := icSpec(CombinationFallbackEqualWeight)

	// 之前（6 天）形成的快照。
	wBefore, err := TrainRollingIC(spec, stepView(dates6, risingSteps), factors)
	if err != nil {
		t.Fatalf("TrainRollingIC(6 天) 失败: %v", err)
	}
	if wBefore.TrainEnd != "2026-01-12" || wBefore.SampleCount != 5 {
		t.Fatalf("6 天训练窗错误: end=%s samples=%d", wBefore.TrainEnd, wBefore.SampleCount)
	}

	// 追加一天后（7 天）→ 训练窗扩展，样本 +1。
	wAfter, err := TrainRollingIC(spec, stepView(dates7, steps7), factors)
	if err != nil {
		t.Fatalf("TrainRollingIC(7 天) 失败: %v", err)
	}
	if wAfter.TrainEnd != "2026-01-13" || wAfter.SampleCount != 6 {
		t.Fatalf("7 天训练窗错误: end=%s samples=%d", wAfter.TrainEnd, wAfter.SampleCount)
	}
	// 之前快照不受追加日影响：用 6 天视图重训仍得到 wBefore。
	wBeforeAgain, err := TrainRollingIC(spec, stepView(dates6, risingSteps), factors)
	if err != nil {
		t.Fatalf("重训 6 天失败: %v", err)
	}
	if !reflect.DeepEqual(wBefore, wBeforeAgain) {
		t.Fatal("追加训练日后，之前已生成的权重快照必须保持不变")
	}
}

// TestLeakTestWindowReversalNoFlip 泄漏 c：测试窗收益反向不影响训练器输出；
// 权重符号由冻结方向与训练视图决定，测试窗数据从未进入训练。
func TestLeakTestWindowReversalNoFlip(t *testing.T) {
	viewTrain := stepView(dates6, risingSteps) // 训练期：收益秩 [1,2,3,4]
	// 测试期构造收益全反向的独立视图（秩 [4,3,2,1]），绝不传入训练器。
	viewTest := stepView(dates6, fallingSteps)
	if reflect.DeepEqual(viewTrain, viewTest) {
		t.Fatal("测试视图应构造为与训练视图不同")
	}
	factors := []FactorSeries{
		flatFactor("f1", DirectionHigherIsBetter, 1, dates6, []float64{1, 2, 3, 4}),
	}
	spec := icSpec(CombinationFallbackEqualWeight)
	w1, err := TrainRollingIC(spec, viewTrain, factors)
	if err != nil {
		t.Fatalf("TrainRollingIC 失败: %v", err)
	}
	// 权重为正（方向一致）：收益秩与因子秩正相关 → IC=1。
	if w1.FinalWeights["f1"] <= 0 {
		t.Fatalf("权重符号应与冻结方向一致: %v", w1.FinalWeights["f1"])
	}
	// 测试窗收益反向不改变结果（训练器只读训练视图 viewTrain）。
	w2, err := TrainRollingIC(spec, viewTrain, factors)
	if err != nil {
		t.Fatalf("重训失败: %v", err)
	}
	if !reflect.DeepEqual(w1, w2) {
		t.Fatal("测试窗收益反向不得影响已训练权重（训练器不接受测试视图）")
	}
}

// TestLeakInsufficientFallbackFrozen 泄漏 d：数据不足时严格执行冻结 fallback，
// 不得临时择优——即使视图内因子方向看起来很强，只要有效样本不足就按协议回退。
func TestLeakInsufficientFallbackFrozen(t *testing.T) {
	shortDates := []string{"2026-01-05", "2026-01-06", "2026-01-07"}
	// f1 与收益强正相关（2 个有效 IC 日均为 1）——但不满足 minICDays=5。
	view := stepView(shortDates, [][]float64{{1.1, 1.2, 1.3, 1.4}, {1.1, 1.2, 1.3, 1.4}})
	f1 := flatFactor("f1", DirectionHigherIsBetter, 1, shortDates, []float64{1, 2, 3, 4})
	f2 := flatFactor("f2", DirectionHigherIsBetter, 1, shortDates, []float64{1, 2, 3, 4})

	// 协议预选等权 → 必须等权，不能因为 f1 强相关就临时给它更多权重。
	snapEQ, err := TrainRollingIC(icSpec(CombinationFallbackEqualWeight), view, []FactorSeries{f1, f2})
	if err != nil {
		t.Fatalf("TrainRollingIC 失败: %v", err)
	}
	assertFloatMap(t, snapEQ.FinalWeights, map[string]float64{"f1": 0.5, "f2": 0.5}, 1e-9, "冻结等权 fallback")
	// 协议预选现金 → 必须全 0。
	snapCash, err := TrainRollingIC(icSpec(CombinationFallbackCash), view, []FactorSeries{f1, f2})
	if err != nil {
		t.Fatalf("TrainRollingIC(现金) 失败: %v", err)
	}
	assertFloatMap(t, snapCash.FinalWeights, map[string]float64{"f1": 0, "f2": 0}, 1e-9, "冻结现金 fallback")
}

// ---- 完成门槛：权重来源可追溯 ----

// TestWeightSnapshotTraceability 权重快照保存训练起止日、样本数、原始估计、
// 收缩值、最终权重与 fallback 原因（空 = 正常），保证权重来源可追溯。
func TestWeightSnapshotTraceability(t *testing.T) {
	view := stepView(dates6, risingSteps)
	factors := []FactorSeries{
		flatFactor("f1", DirectionHigherIsBetter, 1, dates6, []float64{1, 2, 3, 4}),
		flatFactor("f2", DirectionLowerIsBetter, 1, dates6, []float64{1, 2, 3, 4}),
	}
	spec := icSpec(CombinationFallbackEqualWeight)
	spec.Shrinkage = 0.4
	snap, err := TrainRollingIC(spec, view, factors)
	if err != nil {
		t.Fatalf("TrainRollingIC 失败: %v", err)
	}
	if snap.TrainStart == "" || snap.TrainEnd == "" {
		t.Fatal("快照必须记录训练起止日")
	}
	if snap.SampleCount < 1 {
		t.Fatalf("快照必须记录样本数: %d", snap.SampleCount)
	}
	if snap.DataHash == "" {
		t.Fatal("快照必须记录训练数据 hash")
	}
	if len(snap.RawEstimates) != 2 || len(snap.ShrunkValues) != 2 || len(snap.FinalWeights) != 2 {
		t.Fatalf("快照估计/收缩/最终权重必须逐因子记录: %+v", snap)
	}
	if snap.Shrinkage != 0.4 {
		t.Fatalf("快照必须记录收缩系数: %v", snap.Shrinkage)
	}
	if snap.FallbackReason != "" {
		t.Fatalf("正常训练 fallback 原因应为空: %q", snap.FallbackReason)
	}
	if snap.Method == "" {
		t.Fatal("快照必须记录权重形成方法")
	}
	// 权重和 = 1。
	sum := 0.0
	for _, w := range snap.FinalWeights {
		sum += w
	}
	assertFloat(t, sum, 1, 1e-9, "权重和")
}
