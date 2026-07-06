package core

import (
	"math"
	"testing"
)

// ========== CAGR 复合年化收益率 ==========

func TestCAGR单年等于简单收益率(t *testing.T) {
	// 单年时 CAGR 应等于该年收益率
	// 年收益率 30% -> CAGR = 30%
	r := CAGR([]float64{0.30})
	if math.Abs(r-0.30) > 1e-6 {
		t.Fatalf("单年 CAGR 期望 0.30, 实际 %v", r)
	}
}

func TestCAGR多年复合(t *testing.T) {
	// 3年收益率: 10%, 20%, -10%
	// 总复合 = 1.1 * 1.2 * 0.9 = 1.188
	// CAGR = 1.188^(1/3) - 1 ≈ 5.91%
	r := CAGR([]float64{0.10, 0.20, -0.10})
	expected := math.Pow(1.1*1.2*0.9, 1.0/3.0) - 1
	if math.Abs(r-expected) > 1e-6 {
		t.Fatalf("多年 CAGR 期望 %v, 实际 %v", expected, r)
	}
}

func TestCAGR空切片返回0(t *testing.T) {
	if r := CAGR(nil); r != 0 {
		t.Fatalf("空切片 CAGR 期望 0, 实际 %v", r)
	}
}

// ========== Sharpe 夏普比率 ==========

func TestSharpe比率计算(t *testing.T) {
	// returns: 1%, 2%, 3%, 4% (per-trade)
	// mean = 2.5%, std = sqrt(((1-2.5)^2+(2-2.5)^2+(3-2.5)^2+(4-2.5)^2)/4) = sqrt(1.25) ≈ 1.118%
	// Sharpe = mean/std * sqrt(periodsPerYear)
	// periodsPerYear = 4 -> annualization = sqrt(4) = 2
	// Sharpe = 2.5/1.118 * 2 ≈ 4.472
	returns := []float64{0.01, 0.02, 0.03, 0.04}
	r := SharpeRatio(returns, 4)
	mean := 0.025
	std := math.Sqrt(((0.01-mean)*(0.01-mean) + (0.02-mean)*(0.02-mean) + (0.03-mean)*(0.03-mean) + (0.04-mean)*(0.04-mean)) / 4)
	expected := mean / std * math.Sqrt(4)
	if math.Abs(r-expected) > 1e-6 {
		t.Fatalf("Sharpe 期望 %v, 实际 %v", expected, r)
	}
}

func TestSharpe无波动返回0(t *testing.T) {
	// 所有收益率相同 -> std=0 -> Sharpe=0（避免除零）
	r := SharpeRatio([]float64{0.01, 0.01, 0.01}, 12)
	if r != 0 {
		t.Fatalf("无波动 Sharpe 期望 0, 实际 %v", r)
	}
}

func TestSharpe空切片返回0(t *testing.T) {
	if r := SharpeRatio(nil, 12); r != 0 {
		t.Fatalf("空切片 Sharpe 期望 0, 实际 %v", r)
	}
}

// ========== Sortino 索提诺比率 ==========

func TestSortino仅用下行波动(t *testing.T) {
	// returns: 5%, -3%, 5%, -3%
	// mean = 1%
	// downside returns (负收益): -3%, -3% (相对0的偏差)
	// downside std = sqrt((0.03^2 + 0.03^2)/4) = sqrt(0.0009/2) ≈ 0.02121
	// Sortino = mean / downsideStd * sqrt(periodsPerYear)
	// periodsPerYear = 12 -> annualization = sqrt(12)
	returns := []float64{0.05, -0.03, 0.05, -0.03}
	r := SortinoRatio(returns, 12)
	mean := 0.01
	downsideSqSum := 0.03*0.03 + 0.03*0.03
	downsideStd := math.Sqrt(downsideSqSum / 4)
	expected := mean / downsideStd * math.Sqrt(12)
	if math.Abs(r-expected) > 1e-6 {
		t.Fatalf("Sortino 期望 %v, 实际 %v", expected, r)
	}
}

func TestSortino无下行风险返回0(t *testing.T) {
	// 所有收益为正 -> 无下行风险 -> 返回 0（避免除零）
	r := SortinoRatio([]float64{0.01, 0.02, 0.03}, 12)
	if r != 0 {
		t.Fatalf("无下行风险 Sortino 期望 0, 实际 %v", r)
	}
}

func TestSortino空切片返回0(t *testing.T) {
	if r := SortinoRatio(nil, 12); r != 0 {
		t.Fatalf("空切片 Sortino 期望 0, 实际 %v", r)
	}
}

// ========== Calmar 卡玛比率 ==========

func TestCalmar比率计算(t *testing.T) {
	// 年化收益 20%, 最大回撤 10%
	// Calmar = 0.20 / 0.10 = 2.0
	r := CalmarRatio(0.20, 0.10)
	if math.Abs(r-2.0) > 1e-6 {
		t.Fatalf("Calmar 期望 2.0, 实际 %v", r)
	}
}

func TestCalmar零回撤返回0(t *testing.T) {
	if r := CalmarRatio(0.20, 0); r != 0 {
		t.Fatalf("零回撤 Calmar 期望 0, 实际 %v", r)
	}
}
