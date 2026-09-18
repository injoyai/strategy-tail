package lab

import (
	"math"
	"testing"
)

// factor_stats_extended_test.go：HAC/Newey-West、ExtendedICStats 与衰减曲线的
// 手算锁定。基准序列 xs=[1,2,3,4]：mean=2.5，偏差 d=[-1.5,-0.5,0.5,1.5]，
// γ0=1.25、γ1=0.3125、γ2=-0.375、γ3=-0.5625（γl = Σ d_t·d_{t-l} / n，n=4）。

func TestHACMeanTStatLagZeroEqualsNaiveStandardError(t *testing.T) {
	gotT, gotSE := hacMeanTStat([]float64{1, 2, 3, 4}, 0)
	if gotT == nil || gotSE == nil {
		t.Fatalf("lag=0 应可得，得到 (%v, %v)", gotT, gotSE)
	}
	// LRV=γ0=1.25 → SE=sqrt(1.25/4)，与朴素标准误（总体 std/√n）一致
	wantSE := math.Sqrt(1.25 / 4)
	if !nearlyEq(*gotSE, wantSE) {
		t.Fatalf("SE = %v, want %v", *gotSE, wantSE)
	}
	if !nearlyEq(*gotT, 2.5/wantSE) {
		t.Fatalf("t = %v, want %v", *gotT, 2.5/wantSE)
	}
}

func TestHACMeanTStatLagOneHandComputed(t *testing.T) {
	gotT, gotSE := hacMeanTStat([]float64{1, 2, 3, 4}, 1)
	if gotT == nil || gotSE == nil {
		t.Fatalf("应可得，得到 (%v, %v)", gotT, gotSE)
	}
	// LRV = γ0 + 2·(1-1/2)·γ1 = 1.25 + 0.3125 = 1.5625
	wantSE := math.Sqrt(1.5625 / 4)
	if !nearlyEq(*gotSE, wantSE) || !nearlyEq(*gotSE, 0.625) {
		t.Fatalf("SE = %v, want 0.625", *gotSE)
	}
	if !nearlyEq(*gotT, 4.0) {
		t.Fatalf("t = %v, want 4.0", *gotT)
	}
}

func TestHACMeanTStatLagTwoHandComputed(t *testing.T) {
	gotT, gotSE := hacMeanTStat([]float64{1, 2, 3, 4}, 2)
	if gotT == nil || gotSE == nil {
		t.Fatalf("应可得，得到 (%v, %v)", gotT, gotSE)
	}
	// LRV = 1.25 + 2·(2/3)·0.3125 + 2·(1/3)·(-0.375) = 17/12
	wantSE := math.Sqrt(17.0 / 48.0)
	if !nearlyEq(*gotSE, wantSE) {
		t.Fatalf("SE = %v, want %v", *gotSE, wantSE)
	}
	if !nearlyEq(*gotT, 2.5/wantSE) {
		t.Fatalf("t = %v, want %v", *gotT, 2.5/wantSE)
	}
}

func TestHACMeanTStatPositiveAutocorrelationInflatesSE(t *testing.T) {
	_, se0 := hacMeanTStat([]float64{1, 2, 3, 4}, 0)
	_, se1 := hacMeanTStat([]float64{1, 2, 3, 4}, 1)
	if se0 == nil || se1 == nil || *se1 <= *se0 {
		t.Fatalf("γ1>0 时 lag=1 的 SE 应大于 lag=0: se0=%v se1=%v", *se0, *se1)
	}
	trend := []float64{1, 1.2, 1.5, 1.9, 2.4, 3.0, 3.7, 4.5}
	_, tse0 := hacMeanTStat(trend, 0)
	_, tse1 := hacMeanTStat(trend, 1)
	if tse0 == nil || tse1 == nil || *tse1 <= *tse0 {
		t.Fatalf("趋势序列 lag=1 的 SE 应大于 lag=0: se0=%v se1=%v", *tse0, *tse1)
	}
}

func TestHACMeanTStatLagBeyondSeriesHandComputed(t *testing.T) {
	// l>=n 的 γl 无重叠项恒为 0，但 Bartlett 权重仍按原 lag 计算：
	// LRV = γ0 + 2·(10/11)·γ1 + 2·(9/11)·γ2 + 2·(8/11)·γ3 = 17/44
	gotT, gotSE := hacMeanTStat([]float64{1, 2, 3, 4}, 10)
	if gotT == nil || gotSE == nil {
		t.Fatalf("应可得，得到 (%v, %v)", gotT, gotSE)
	}
	wantSE := math.Sqrt(17.0 / 44.0 / 4.0)
	if !nearlyEq(*gotSE, wantSE) {
		t.Fatalf("SE = %v, want %v", *gotSE, wantSE)
	}
	if !nearlyEq(*gotT, 2.5/wantSE) {
		t.Fatalf("t = %v, want %v", *gotT, 2.5/wantSE)
	}
}

func TestHACMeanTStatRejectsDegenerateInputs(t *testing.T) {
	cases := []struct {
		name string
		xs   []float64
		lag  int
	}{
		{"empty", nil, 0},
		{"single sample", []float64{3.14}, 0},
		{"constant", []float64{5, 5, 5, 5}, 0},
		{"NaN", []float64{1, math.NaN(), 3}, 0},
		{"Inf", []float64{1, math.Inf(1), 3}, 1},
		{"negative lag", []float64{1, 2, 3}, -1},
	}
	for _, c := range cases {
		if gotT, gotSE := hacMeanTStat(c.xs, c.lag); gotT != nil || gotSE != nil {
			t.Errorf("%s: 期望 (nil, nil)，得到 (%v, %v)", c.name, gotT, gotSE)
		}
	}
}

// extICs 手算基准：sum=0.85、mean=0.10625、Σd²=0.0471875、std=sqrt(0.0471875/8)、
// 正值 7 个、负值 1 个（n=8）。
var extICs = []float64{0.1, 0.2, 0.05, 0.15, 0.1, -0.05, 0.2, 0.1}

func TestExtendedICStatsHandComputedPositive(t *testing.T) {
	got := extendedICStats(extICs, "positive", 1)
	if got.Pairs != 8 {
		t.Fatalf("Pairs = %d, want 8", got.Pairs)
	}
	if got.Mean == nil || !nearlyEq(*got.Mean, 0.10625) {
		t.Fatalf("Mean = %v, want 0.10625", got.Mean)
	}
	wantStd := math.Sqrt(0.0471875 / 8)
	if got.Std == nil || !nearlyEq(*got.Std, wantStd) {
		t.Fatalf("Std = %v, want %v", got.Std, wantStd)
	}
	if got.ICIR == nil || !nearlyEq(*got.ICIR, *got.Mean / *got.Std) {
		t.Fatalf("ICIR = %v, want Mean/Std = %v", got.ICIR, *got.Mean / *got.Std)
	}
	// 年化 = sqrt(252) × ICIR（日频固定系数）
	if got.AnnualizedICIR == nil || !nearlyEq(*got.AnnualizedICIR / *got.ICIR, math.Sqrt(252)) {
		t.Fatalf("AnnualizedICIR/ICIR = %v, want sqrt(252)", got.AnnualizedICIR)
	}
	if got.PositiveRate == nil || !nearlyEq(*got.PositiveRate, 0.875) {
		t.Fatalf("PositiveRate = %v, want 0.875", got.PositiveRate)
	}
	if got.DirectionConsistentRate == nil || !nearlyEq(*got.DirectionConsistentRate, 0.875) {
		t.Fatalf("DirectionConsistentRate = %v, want 0.875", got.DirectionConsistentRate)
	}
	// 朴素 t = mean/(std/√n) = ICIR·√n
	if got.NaiveTStat == nil || !nearlyEq(*got.NaiveTStat, *got.ICIR*math.Sqrt(8)) {
		t.Fatalf("NaiveTStat = %v, want ICIR·√8", got.NaiveTStat)
	}
	// HAC t 复用 hacMeanTStat（其正确性由手算测试锁定），lag 原样记录
	wantT, _ := hacMeanTStat(extICs, 1)
	if wantT == nil {
		t.Fatal("hacMeanTStat 应可得")
	}
	if got.HACTStat == nil || !nearlyEq(*got.HACTStat, *wantT) {
		t.Fatalf("HACTStat = %v, want %v", got.HACTStat, *wantT)
	}
	if got.HACLag != 1 {
		t.Fatalf("HACLag = %d, want 1", got.HACLag)
	}
	// PValue：双侧正态近似 p = erfc(|t|/√2)
	wantP := math.Erfc(math.Abs(*got.HACTStat) / math.Sqrt(2))
	if got.PValue == nil || !nearlyEq(*got.PValue, wantP) {
		t.Fatalf("PValue = %v, want %v", got.PValue, wantP)
	}
}

func TestExtendedICStatsDirectionConsistentRate(t *testing.T) {
	neg := extendedICStats(extICs, "negative", 0)
	if neg.DirectionConsistentRate == nil || !nearlyEq(*neg.DirectionConsistentRate, 0.125) {
		t.Fatalf("negative 方向一致率 = %v, want 0.125", neg.DirectionConsistentRate)
	}
	// 正向率与方向无关，始终是 IC>0 的比例
	if neg.PositiveRate == nil || !nearlyEq(*neg.PositiveRate, 0.875) {
		t.Fatalf("PositiveRate = %v, want 0.875", neg.PositiveRate)
	}
	for _, dir := range []string{"two_sided", "anything_else"} {
		got := extendedICStats(extICs, dir, 0)
		if got.DirectionConsistentRate != nil {
			t.Fatalf("%s 方向一致率应为 null，得到 %v", dir, got.DirectionConsistentRate)
		}
	}
}

func TestExtendedICStatsDegenerateInputs(t *testing.T) {
	// 空输入：Pairs=0，全部统计 null（不以 0 冒充）
	empty := extendedICStats(nil, "positive", 0)
	if empty.Pairs != 0 {
		t.Fatalf("Pairs = %d, want 0", empty.Pairs)
	}
	assertAllNull(t, &empty, "empty")

	// NaN/Inf 全部过滤后等同空输入
	allBad := extendedICStats([]float64{math.NaN(), math.Inf(-1), math.NaN()}, "positive", 2)
	if allBad.Pairs != 0 {
		t.Fatalf("Pairs = %d, want 0", allBad.Pairs)
	}
	assertAllNull(t, &allBad, "allNaN")

	// 单样本：Mean/PositiveRate 可得，Std=0 使推断统计为 null
	single := extendedICStats([]float64{0.3}, "positive", 0)
	if single.Pairs != 1 || single.Mean == nil || *single.Mean != 0.3 {
		t.Fatalf("单样本 Mean = %v (Pairs=%d), want 0.3 (1)", single.Mean, single.Pairs)
	}
	if single.Std != nil || single.ICIR != nil || single.AnnualizedICIR != nil ||
		single.NaiveTStat != nil || single.HACTStat != nil || single.PValue != nil {
		t.Fatalf("单样本推断统计应为 null: %+v", single)
	}
	if single.PositiveRate == nil || *single.PositiveRate != 1 {
		t.Fatalf("单样本 PositiveRate = %v, want 1", single.PositiveRate)
	}

	// NaN/Inf 混入：只对有限值统计
	mixed := extendedICStats([]float64{math.NaN(), 0.1, math.Inf(1), 0.3}, "positive", 0)
	if mixed.Pairs != 2 || mixed.Mean == nil || !nearlyEq(*mixed.Mean, 0.2) {
		t.Fatalf("混入 NaN 的 Mean = %v (Pairs=%d), want 0.2 (2)", mixed.Mean, mixed.Pairs)
	}

	// 常数序列：Std=0 是真实结果（非 null），除以它的推断统计为 null
	constant := extendedICStats([]float64{0.2, 0.2, 0.2}, "positive", 0)
	if constant.Mean == nil || !nearlyEq(*constant.Mean, 0.2) {
		t.Fatalf("常数序列 Mean = %v, want 0.2", constant.Mean)
	}
	if constant.Std == nil || *constant.Std != 0 {
		t.Fatalf("常数序列 Std = %v, want 0", constant.Std)
	}
	if constant.ICIR != nil || constant.AnnualizedICIR != nil ||
		constant.NaiveTStat != nil || constant.HACTStat != nil || constant.PValue != nil {
		t.Fatalf("常数序列推断统计应为 null: %+v", constant)
	}
}

// assertAllNull 断言 ExtendedICStats 的全部统计指针字段均为 nil。
func assertAllNull(t *testing.T, s *ExtendedICStats, name string) {
	t.Helper()
	for name, v := range map[string]*float64{
		"Mean":                    s.Mean,
		"Std":                     s.Std,
		"ICIR":                    s.ICIR,
		"AnnualizedICIR":          s.AnnualizedICIR,
		"PositiveRate":            s.PositiveRate,
		"NaiveTStat":              s.NaiveTStat,
		"HACTStat":                s.HACTStat,
		"PValue":                  s.PValue,
		"DirectionConsistentRate": s.DirectionConsistentRate,
	} {
		if v != nil {
			t.Fatalf("%s: %s 应为 null，得到 %v", name, name, *v)
		}
	}
}

func TestExtendedICStatsNegativeLagHasNoHAC(t *testing.T) {
	got := extendedICStats(extICs, "positive", -1)
	if got.HACTStat != nil || got.PValue != nil {
		t.Fatalf("负 lag 不应产生 HAC t/p: %v/%v", got.HACTStat, got.PValue)
	}
	if got.HACLag != 0 {
		t.Fatalf("HACLag = %d, want 0", got.HACLag)
	}
	// 其余统计不受 lag 影响
	if got.Mean == nil || !nearlyEq(*got.Mean, 0.10625) {
		t.Fatalf("Mean = %v, want 0.10625", got.Mean)
	}
}

func TestBuildDecayCurveSortedAndNullSemantics(t *testing.T) {
	stats := map[int]ExtendedICStats{
		1:  {Mean: f64p(0.10), HACTStat: f64p(3.2)},
		5:  {Mean: f64p(0.06)}, // HAC 无效（如样本不足）为 null
		10: {},                 // 完全无有效结果
	}
	spreads := map[int]*float64{1: f64p(0.42), 5: nil}

	got := buildDecayCurve([]int{5, 10, 1}, stats, spreads)
	if len(got) != 3 {
		t.Fatalf("点数 = %d, want 3", len(got))
	}
	for i, want := range []int{1, 5, 10} {
		if got[i].Horizon != want {
			t.Fatalf("got[%d].Horizon = %d, want %d（必须升序）", i, got[i].Horizon, want)
		}
	}
	if got[0].MeanIC == nil || *got[0].MeanIC != 0.10 ||
		got[0].HACT == nil || *got[0].HACT != 3.2 ||
		got[0].Spread == nil || *got[0].Spread != 0.42 {
		t.Fatalf("h=1 点错误: %+v", got[0])
	}
	if got[1].MeanIC == nil || *got[1].MeanIC != 0.06 || got[1].HACT != nil || got[1].Spread != nil {
		t.Fatalf("h=5 点错误: %+v", got[1])
	}
	if got[2].MeanIC != nil || got[2].HACT != nil || got[2].Spread != nil {
		t.Fatalf("h=10 点应全 null: %+v", got[2])
	}

	if got := buildDecayCurve(nil, nil, nil); got != nil {
		t.Fatalf("空输入应返回 nil，得到 %v", got)
	}
}
