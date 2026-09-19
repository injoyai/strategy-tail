package portfolioresearch

import (
	"math"
	"strings"
	"testing"
)

// redundancy_test.go v2 Task 3 冗余诊断测试（设计 §7.3）。
//
// 金标准覆盖：两两因子日截面 Spearman 相关手算（含并列秩）、覆盖交集/并集/
// 重叠比例、单因子 IC 与合成分数 IC、边际/残差 IC 手算（OLS 残差）、
// leave-one-factor-out IC 变化、滚动权重稳定度与触顶次数；相关门槛只生成
// warning 不自动删除因子。

// ---- 测试辅助 ----

func findPair(t *testing.T, rep RedundancyReport, a, b string) FactorPairCorr {
	t.Helper()
	for _, p := range rep.Pairs {
		if (p.FactorA == a && p.FactorB == b) || (p.FactorA == b && p.FactorB == a) {
			return p
		}
	}
	t.Fatalf("缺少因子对 %s-%s（%+v）", a, b, rep.Pairs)
	return FactorPairCorr{}
}

func findCoverage(t *testing.T, rep RedundancyReport, a, b string) CoverageStats {
	t.Helper()
	for _, c := range rep.Coverage {
		if (c.FactorA == a && c.FactorB == b) || (c.FactorA == b && c.FactorB == a) {
			return c
		}
	}
	t.Fatalf("缺少覆盖统计 %s-%s", a, b)
	return CoverageStats{}
}

func findSingleIC(t *testing.T, rep RedundancyReport, factor string) FactorIC {
	t.Helper()
	for _, ic := range rep.SingleIC {
		if ic.Factor == factor {
			return ic
		}
	}
	t.Fatalf("缺少单因子 IC %s", factor)
	return FactorIC{}
}

func findMarginalIC(t *testing.T, rep RedundancyReport, factor string) FactorIC {
	t.Helper()
	for _, ic := range rep.MarginalIC {
		if ic.Factor == factor {
			return ic
		}
	}
	t.Fatalf("缺少边际 IC %s", factor)
	return FactorIC{}
}

func findLOO(t *testing.T, rep RedundancyReport, factor string) LeaveOneOut {
	t.Helper()
	for _, l := range rep.LeaveOneOut {
		if l.RemovedFactor == factor {
			return l
		}
	}
	t.Fatalf("缺少留一法 %s", factor)
	return LeaveOneOut{}
}

// ---- 两两因子相关（手算金标准） ----

// TestRedundancyPairCorrHandComputed 单日 5 因子两两 Spearman 手算：
// 秩相同 → 1；秩反向 → -1；并列秩 [1,2,2,3] vs [0.1,0.3,0.2,0.4] = 3/√10 ≈ 0.9487。
func TestRedundancyPairCorrHandComputed(t *testing.T) {
	d := "2026-01-05"
	f1 := flatFactor("f1", DirectionHigherIsBetter, 1, []string{d}, []float64{1, 2, 3, 4})
	f2 := flatFactor("f2", DirectionHigherIsBetter, 1, []string{d}, []float64{2, 4, 6, 8})
	f3 := flatFactor("f3", DirectionHigherIsBetter, 1, []string{d}, []float64{4, 3, 2, 1})
	f4 := flatFactor("f4", DirectionHigherIsBetter, 1, []string{d}, []float64{1, 2, 2, 3})
	f5 := flatFactor("f5", DirectionHigherIsBetter, 1, []string{d}, []float64{0.1, 0.3, 0.2, 0.4})
	rep, err := RedundancyDiagnostics([]FactorSeries{f1, f2, f3, f4, f5}, ReturnsView{}, nil, RedundancyConfig{Missing: TransformMissingExclude})
	if err != nil {
		t.Fatalf("RedundancyDiagnostics 失败: %v", err)
	}
	if len(rep.Pairs) != 10 {
		t.Fatalf("5 因子应有 10 对相关: %d", len(rep.Pairs))
	}
	p12 := findPair(t, rep, "f1", "f2")
	if p12.ValidDays != 1 || math.Abs(p12.Mean-1) > 1e-9 {
		t.Fatalf("f1-f2 相关应为 1: %+v", p12)
	}
	p13 := findPair(t, rep, "f1", "f3")
	if math.Abs(p13.Mean+1) > 1e-9 {
		t.Fatalf("f1-f3 相关应为 -1: %+v", p13)
	}
	// 并列秩金标准：3/√10。
	p45 := findPair(t, rep, "f4", "f5")
	want := 3.0 / math.Sqrt(10)
	if math.Abs(p45.Mean-want) > 1e-9 {
		t.Fatalf("f4-f5 并列秩相关应为 3/√10=%.6f，实际 %.6f", want, p45.Mean)
	}
	// 单日 → 分位数 = 该值。
	for _, p := range []FactorPairCorr{p12, p45} {
		if p.P25 != p.Mean || p.P50 != p.Mean || p.P75 != p.Mean {
			t.Fatalf("单日相关分位数应等于均值: %+v", p)
		}
	}
}

// ---- 覆盖交并 ----

// TestRedundancyCoverageHandComputed 覆盖交集/并集/重叠比例手算：
// f1 覆盖 {A,B,C,D}，f2 覆盖 {B,C,D,E} → 交集 3、并集 5、重叠 0.6。
func TestRedundancyCoverageHandComputed(t *testing.T) {
	d := "2026-01-05"
	f1 := series("f1", DirectionHigherIsBetter, 1, map[string][]TransformedRow{
		d: {trow("A", 1), trow("B", 2), trow("C", 3), trow("D", 4)},
	})
	f2 := series("f2", DirectionHigherIsBetter, 1, map[string][]TransformedRow{
		d: {trow("B", 4), trow("C", 3), trow("D", 2), trow("E", 1)},
	})
	rep, err := RedundancyDiagnostics([]FactorSeries{f1, f2}, ReturnsView{}, nil, RedundancyConfig{Missing: TransformMissingExclude})
	if err != nil {
		t.Fatalf("RedundancyDiagnostics 失败: %v", err)
	}
	c := findCoverage(t, rep, "f1", "f2")
	if c.ValidDays != 1 || c.MeanIntersection != 3 || c.MeanUnion != 5 {
		t.Fatalf("覆盖交并错误: %+v", c)
	}
	assertFloat(t, c.MeanOverlap, 0.6, 1e-9, "重叠比例")
}

// ---- 单因子 IC 与合成分数 IC ----

// TestRedundancySingleAndCompositeIC 单因子与等权合成分数的 IC：
// f1/f2 秩均与收益秩一致 → 单因子 IC=1；等权合成秩不变 → 合成分数 IC=1。
func TestRedundancySingleAndCompositeIC(t *testing.T) {
	d := "2026-01-05"
	f1 := flatFactor("f1", DirectionHigherIsBetter, 1, []string{d}, []float64{1, 2, 3, 4})
	f2 := flatFactor("f2", DirectionHigherIsBetter, 1, []string{d}, []float64{10, 20, 30, 40})
	rets := ReturnsView{d: {"A": 0.1, "B": 0.2, "C": 0.3, "D": 0.4}}
	rep, err := RedundancyDiagnostics([]FactorSeries{f1, f2}, rets, nil, RedundancyConfig{Missing: TransformMissingExclude})
	if err != nil {
		t.Fatalf("RedundancyDiagnostics 失败: %v", err)
	}
	ic1 := findSingleIC(t, rep, "f1")
	if ic1.ValidDays != 1 || math.Abs(ic1.Mean-1) > 1e-9 {
		t.Fatalf("f1 单因子 IC 应为 1: %+v", ic1)
	}
	ic2 := findSingleIC(t, rep, "f2")
	if math.Abs(ic2.Mean-1) > 1e-9 {
		t.Fatalf("f2 单因子 IC 应为 1: %+v", ic2)
	}
	// 合成分数 = [5.5,11,16.5,22] → 秩 [1,2,3,4] → IC=1。
	if rep.Composite.ValidDays != 1 || math.Abs(rep.Composite.Mean-1) > 1e-9 {
		t.Fatalf("合成分数 IC 应为 1: %+v", rep.Composite)
	}
}

// ---- 边际/残差 IC（手算金标准） ----

// TestRedundancyMarginalICHandComputed 边际 IC 手算：
// f1=[1,2,3,4] 对 f2=[1,1,4,4] 回归后残差 [-0.5,0.5,-0.5,0.5]（β=[5/6,2/3]），
// 与收益 [0.1,0.2,0.3,0.4] 的 Spearman = 1/√5；
// f2 对 f1 回归残差 [0.3,-0.9,0.9,-0.3] 的秩 [3,1,4,2] 与收益秩正交 → 0。
func TestRedundancyMarginalICHandComputed(t *testing.T) {
	d := "2026-01-05"
	f1 := flatFactor("f1", DirectionHigherIsBetter, 1, []string{d}, []float64{1, 2, 3, 4})
	f2 := flatFactor("f2", DirectionHigherIsBetter, 1, []string{d}, []float64{1, 1, 4, 4})
	rets := ReturnsView{d: {"A": 0.1, "B": 0.2, "C": 0.3, "D": 0.4}}
	rep, err := RedundancyDiagnostics([]FactorSeries{f1, f2}, rets, nil, RedundancyConfig{Missing: TransformMissingExclude})
	if err != nil {
		t.Fatalf("RedundancyDiagnostics 失败: %v", err)
	}
	m1 := findMarginalIC(t, rep, "f1")
	if m1.ValidDays != 1 {
		t.Fatalf("f1 边际 IC 有效日应为 1: %+v", m1)
	}
	assertFloat(t, m1.Mean, 1/math.Sqrt(5), 1e-9, "f1 边际 IC = 1/√5")
	m2 := findMarginalIC(t, rep, "f2")
	assertFloat(t, m2.Mean, 0, 1e-9, "f2 边际 IC = 0")
}

// ---- leave-one-factor-out ----

// TestRedundancyLeaveOneOutHandComputed 留一法手算：
// f1=[1,2,3,4]（IC=1）、f2=[4,3,2,1]（IC=-1）→ 等权合成 [2.5,2.5,2.5,2.5] IC=0；
// 移除 f1 后仅 f2 → IC=-1（变化 -1）；移除 f2 后仅 f1 → IC=+1（变化 +1）。
func TestRedundancyLeaveOneOutHandComputed(t *testing.T) {
	d := "2026-01-05"
	f1 := flatFactor("f1", DirectionHigherIsBetter, 1, []string{d}, []float64{1, 2, 3, 4})
	f2 := flatFactor("f2", DirectionHigherIsBetter, 1, []string{d}, []float64{4, 3, 2, 1})
	rets := ReturnsView{d: {"A": 0.1, "B": 0.2, "C": 0.3, "D": 0.4}}
	rep, err := RedundancyDiagnostics([]FactorSeries{f1, f2}, rets, nil, RedundancyConfig{Missing: TransformMissingExclude})
	if err != nil {
		t.Fatalf("RedundancyDiagnostics 失败: %v", err)
	}
	lo1 := findLOO(t, rep, "f1")
	assertFloat(t, lo1.FullIC, 0, 1e-9, "全因子 IC")
	assertFloat(t, lo1.LOOIC, -1, 1e-9, "移除 f1 后 IC")
	assertFloat(t, lo1.ICChange, -1, 1e-9, "移除 f1 的 IC 变化")
	if lo1.ValidDays != 1 {
		t.Fatalf("留一法有效日应为 1: %d", lo1.ValidDays)
	}
	lo2 := findLOO(t, rep, "f2")
	assertFloat(t, lo2.LOOIC, 1, 1e-9, "移除 f2 后 IC")
	assertFloat(t, lo2.ICChange, 1, 1e-9, "移除 f2 的 IC 变化")
}

// ---- 相关门槛：只提示不删除 ----

// TestRedundancyCorrThresholdWarningOnly 相关超过门槛只生成 warning，不删除因子。
func TestRedundancyCorrThresholdWarningOnly(t *testing.T) {
	d := "2026-01-05"
	f1 := flatFactor("f1", DirectionHigherIsBetter, 1, []string{d}, []float64{1, 2, 3, 4})
	f2 := flatFactor("f2", DirectionHigherIsBetter, 1, []string{d}, []float64{2, 4, 6, 8}) // 相关 1
	f3 := flatFactor("f3", DirectionHigherIsBetter, 1, []string{d}, []float64{4, 3, 2, 1}) // 与 f1 相关 -1
	rep, err := RedundancyDiagnostics([]FactorSeries{f1, f2, f3}, ReturnsView{}, nil,
		RedundancyConfig{Missing: TransformMissingExclude, CorrThreshold: 0.8})
	if err != nil {
		t.Fatalf("RedundancyDiagnostics 失败: %v", err)
	}
	if len(rep.Warnings) == 0 {
		t.Fatal("相关超门槛应生成 warning")
	}
	joined := strings.Join(rep.Warnings, " ")
	if !strings.Contains(joined, "f1") || !strings.Contains(joined, "f2") {
		t.Fatalf("warning 应指向超门槛因子对: %v", rep.Warnings)
	}
	// 不删除因子：相关对仍保留在报告中（删除/合并必须走新模型 revision）。
	if len(rep.Pairs) != 3 {
		t.Fatalf("相关对不应被删除（应仍为 3 对）: %d", len(rep.Pairs))
	}
}

// ---- 滚动权重稳定度 ----

// TestRedundancyWeightStability 滚动权重稳定度：2 个快照权重 [0.6,0.4]/[0.4,0.6]，
// 上限 0.6 → 均值 0.5、总体标准差 0.1、各触顶 1 次。
func TestRedundancyWeightStability(t *testing.T) {
	factors := []FactorSeries{
		flatFactor("f1", DirectionHigherIsBetter, 1, dates6, []float64{1, 2, 3, 4}),
		flatFactor("f2", DirectionHigherIsBetter, 1, dates6, []float64{1, 2, 3, 4}),
	}
	snaps := []WeightSnapshot{
		{FinalWeights: map[string]float64{"f1": 0.6, "f2": 0.4}, MaxAbsWeight: 0.6},
		{FinalWeights: map[string]float64{"f1": 0.4, "f2": 0.6}, MaxAbsWeight: 0.6},
	}
	rep, err := RedundancyDiagnostics(factors, ReturnsView{}, snaps, RedundancyConfig{Missing: TransformMissingExclude})
	if err != nil {
		t.Fatalf("RedundancyDiagnostics 失败: %v", err)
	}
	if len(rep.WeightStability) != 2 {
		t.Fatalf("应有 2 个因子的稳定度: %+v", rep.WeightStability)
	}
	byKey := map[string]WeightStability{}
	for _, ws := range rep.WeightStability {
		byKey[ws.Factor] = ws
	}
	for _, k := range []string{"f1", "f2"} {
		ws := byKey[k]
		if ws.Snapshots != 2 {
			t.Fatalf("%s 快照数应为 2: %d", k, ws.Snapshots)
		}
		assertFloat(t, ws.MeanWeight, 0.5, 1e-9, k+" 权重均值")
		assertFloat(t, ws.StdWeight, 0.1, 1e-9, k+" 权重标准差（总体，n=2）")
		if ws.CapHits != 1 {
			t.Fatalf("%s 触顶次数应为 1（权重 0.6 = 上限 0.6）: %d", k, ws.CapHits)
		}
	}
}

// ---- 输入校验 ----

// TestRedundancyInputValidation 非法合成策略/非法门槛应报错。
func TestRedundancyInputValidation(t *testing.T) {
	f := flatFactor("f1", DirectionHigherIsBetter, 1, []string{"2026-01-05"}, []float64{1, 2, 3, 4})
	if _, err := RedundancyDiagnostics([]FactorSeries{f}, ReturnsView{}, nil, RedundancyConfig{Missing: "bogus"}); err == nil {
		t.Fatal("非法合成策略应报错")
	}
	if _, err := RedundancyDiagnostics([]FactorSeries{f}, ReturnsView{}, nil, RedundancyConfig{Missing: TransformMissingExclude, CorrThreshold: -0.1}); err == nil {
		t.Fatal("负相关门槛应报错")
	}
	if _, err := RedundancyDiagnostics(nil, ReturnsView{}, nil, RedundancyConfig{Missing: TransformMissingExclude}); err == nil {
		t.Fatal("空因子列表应报错")
	}
}
