package lab

import (
	"math"
	"testing"
)

func nearlyEq(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

// TestAvgRanks 升序名次、并列取均值。
func TestAvgRanks(t *testing.T) {
	got := avgRanks([]float64{1, 2, 2, 3})
	want := []float64{1, 2.5, 2.5, 4}
	for i := range want {
		if !nearlyEq(got[i], want[i]) {
			t.Fatalf("avgRanks[%d] = %v, want %v", i, got[i], want[i])
		}
	}
}

// TestSpearmanIC 完全单调 ±1、并列衰减、弱相关确定性小值。
func TestSpearmanIC(t *testing.T) {
	vals := []float64{1, 2, 3, 4, 5}
	if ic := spearmanIC(vals, []float64{0.1, 0.2, 0.3, 0.4, 0.5}); !nearlyEq(ic, 1) {
		t.Fatalf("同向 IC = %v, want 1", ic)
	}
	if ic := spearmanIC(vals, []float64{0.5, 0.4, 0.3, 0.2, 0.1}); !nearlyEq(ic, -1) {
		t.Fatalf("反向 IC = %v, want -1", ic)
	}
	// 并列：因子 [1,2,2,3]（平均名次 [1,2.5,2.5,4]）对 [0.1,0.3,0.2,0.4]（名次 [1,3,2,4]）
	// 的 Pearson = 4.5/√22.5 = 3/√10 ≈ 0.9487
	if ic := spearmanIC([]float64{1, 2, 2, 3}, []float64{0.1, 0.3, 0.2, 0.4}); !nearlyEq(ic, 3/math.Sqrt(10)) {
		t.Fatalf("并列 IC = %v, want %v", ic, 3/math.Sqrt(10))
	}
	// 弱相关（排名 [3,5,1,2,4]）：手算 Pearson = -0.1
	if ic := spearmanIC([]float64{3, 5, 1, 2, 4}, vals); !nearlyEq(ic, -0.1) {
		t.Fatalf("弱相关 IC = %v, want -0.1", ic)
	}
}

// TestQuintileMeans 按因子值升序等频五分位，收益严格单调。
func TestQuintileMeans(t *testing.T) {
	vals := make([]float64, 20)
	rets := make([]float64, 20)
	for i := range vals {
		vals[i] = float64(i + 1)
		rets[i] = 0.01 * float64(i+1)
	}
	qs := quintileMeans(vals, rets)
	if len(qs) != 5 {
		t.Fatalf("组数 = %d", len(qs))
	}
	for i := 1; i < 5; i++ {
		if qs[i] <= qs[i-1] {
			t.Fatalf("分位收益应严格递增: %v", qs)
		}
	}
	// n=20 等频每组 4 个：Q1=(0.01+0.02+0.03+0.04)/4=0.025，Q5=0.185
	if !nearlyEq(qs[0], 0.025) || !nearlyEq(qs[4], 0.185) {
		t.Fatalf("Q1/Q5 = %v/%v, want 0.025/0.185", qs[0], qs[4])
	}
	if quintileMeans([]float64{1, 2, 3, 4}, []float64{0, 0, 0, 0}) != nil {
		t.Fatal("n<5 应返回 nil")
	}
	// n=9 组大小 [2,2,2,2,1]（最不对称的等频切分），各组仍非空且递增
	qs = quintileMeans([]float64{1, 2, 3, 4, 5, 6, 7, 8, 9},
		[]float64{0.01, 0.02, 0.03, 0.04, 0.05, 0.06, 0.07, 0.08, 0.09})
	if len(qs) != 5 || !nearlyEq(qs[0], 0.015) || !nearlyEq(qs[4], 0.09) {
		t.Fatalf("n=9 分位 = %v, want Q1=0.015 Q5=0.09", qs)
	}
	for i := 1; i < 5; i++ {
		if qs[i] <= qs[i-1] {
			t.Fatalf("n=9 分位收益应严格递增: %v", qs)
		}
	}
}

// TestICStats 均值/总体标准差/t 统计量；样本不足全 0；NaN 剔除。
func TestICStats(t *testing.T) {
	ics := make([]float64, 10)
	for i := range ics {
		if i%2 == 0 {
			ics[i] = 0.1
		} else {
			ics[i] = 0.3
		}
	}
	s := icStats(ics)
	if s.Pairs != 10 || !nearlyEq(s.Mean, 0.2) || !nearlyEq(s.Std, 0.1) {
		t.Fatalf("icStats = %+v", s)
	}
	// t = 0.2/(0.1/√10) = 2√10
	if math.Abs(s.TStat-2*math.Sqrt(10)) > 1e-9 {
		t.Fatalf("TStat = %v", s.TStat)
	}

	// 有效样本 9 < minPairs → 全 0
	short := append([]float64{0.5}, make([]float64, 8)...)
	if s = icStats(short); s.Pairs != 9 || s.Mean != 0 || s.Std != 0 || s.TStat != 0 {
		t.Fatalf("样本不足应全 0: %+v", s)
	}

	// NaN 剔除后仍达 minPairs
	s = icStats([]float64{0.1, 0.3, math.NaN(), 0.1, 0.3, 0.1, 0.3, 0.1, 0.3, 0.1, 0.3})
	if s.Pairs != 10 || !nearlyEq(s.Mean, 0.2) {
		t.Fatalf("NaN 未剔除: %+v", s)
	}
}
