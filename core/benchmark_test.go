package core

import (
	"math"
	"testing"
	"time"

	"github.com/injoyai/strategy-tail/lib/extend"
	"github.com/injoyai/tdx/protocol"
)

// ========== BenchmarkReturn 基准收益率 ==========

func TestBenchmarkReturn区间收益(t *testing.T) {
	// 构造3根日线: close 10 -> 11 -> 12
	// 区间收益 = 12/10 - 1 = 20%
	t0 := time.Date(2024, 1, 2, 15, 0, 0, 0, time.Local)
	dks := extend.Klines{
		&extend.Kline{Unix: t0.Unix(), Kline: &protocol.Kline{Time: t0, Close: protocol.Yuan(10)}},
		&extend.Kline{Unix: t0.AddDate(0, 0, 1).Unix(), Kline: &protocol.Kline{Time: t0.AddDate(0, 0, 1), Close: protocol.Yuan(11)}},
		&extend.Kline{Unix: t0.AddDate(0, 0, 2).Unix(), Kline: &protocol.Kline{Time: t0.AddDate(0, 0, 2), Close: protocol.Yuan(12)}},
	}
	r := BenchmarkReturn(dks)
	if math.Abs(r-0.20) > 1e-6 {
		t.Fatalf("基准收益期望 0.20, 实际 %v", r)
	}
}

func TestBenchmarkReturn数据不足返回0(t *testing.T) {
	t0 := time.Date(2024, 1, 2, 15, 0, 0, 0, time.Local)
	single := extend.Klines{
		&extend.Kline{Unix: t0.Unix(), Kline: &protocol.Kline{Time: t0, Close: protocol.Yuan(10)}},
	}
	if r := BenchmarkReturn(single); r != 0 {
		t.Fatalf("数据不足期望 0, 实际 %v", r)
	}
	if r := BenchmarkReturn(nil); r != 0 {
		t.Fatalf("空数据期望 0, 实际 %v", r)
	}
}

// ========== AlphaBeta ==========

func TestAlphaBeta完全正相关(t *testing.T) {
	// 策略收益与基准收益完全正相关，beta=1, alpha=0
	bench := []float64{0.01, 0.02, -0.01, 0.03}
	strat := []float64{0.01, 0.02, -0.01, 0.03}
	alpha, beta := AlphaBeta(strat, bench)
	if math.Abs(beta-1.0) > 1e-6 {
		t.Fatalf("完全正相关 beta 期望 1.0, 实际 %v", beta)
	}
	if math.Abs(alpha) > 1e-6 {
		t.Fatalf("完全正相关 alpha 期望 0, 实际 %v", alpha)
	}
}

func TestAlphaBeta两倍杠杆(t *testing.T) {
	// 策略收益 = 2 * 基准收益 -> beta=2, alpha=0
	bench := []float64{0.01, 0.02, -0.01, 0.03}
	strat := []float64{0.02, 0.04, -0.02, 0.06}
	alpha, beta := AlphaBeta(strat, bench)
	if math.Abs(beta-2.0) > 1e-6 {
		t.Fatalf("两倍杠杆 beta 期望 2.0, 实际 %v", beta)
	}
	if math.Abs(alpha) > 1e-6 {
		t.Fatalf("两倍杠杆 alpha 期望 0, 实际 %v", alpha)
	}
}

func TestAlphaBeta有超额收益(t *testing.T) {
	// 策略收益 = 基准收益 + 0.005 -> beta=1, alpha=0.005
	bench := []float64{0.01, 0.02, -0.01, 0.03}
	strat := []float64{0.015, 0.025, -0.005, 0.035}
	alpha, beta := AlphaBeta(strat, bench)
	if math.Abs(beta-1.0) > 1e-6 {
		t.Fatalf("超额收益 beta 期望 1.0, 实际 %v", beta)
	}
	if math.Abs(alpha-0.005) > 1e-6 {
		t.Fatalf("超额收益 alpha 期望 0.005, 实际 %v", alpha)
	}
}

func TestAlphaBeta无波动返回0(t *testing.T) {
	// 基准无波动 -> beta=0, alpha=0
	alpha, beta := AlphaBeta([]float64{0.01, 0.01}, []float64{0.02, 0.02})
	if alpha != 0 || beta != 0 {
		t.Fatalf("无波动期望 alpha=0 beta=0, 实际 alpha=%v beta=%v", alpha, beta)
	}
}

func TestComputeTradeAlphaBeta使用净收益(t *testing.T) {
	d1 := time.Date(2024, 1, 2, 15, 0, 0, 0, time.Local)
	d2 := d1.AddDate(0, 0, 1)
	d3 := d1.AddDate(0, 0, 2)
	d4 := d1.AddDate(0, 0, 3)
	trades := []Trade{
		{BuyTime: d1, SellTime: d2, BuyPrice: protocol.Yuan(10), SellPrice: protocol.Yuan(11), BuyCost: 1000, SellIncome: 1050},
		{BuyTime: d3, SellTime: d4, BuyPrice: protocol.Yuan(10), SellPrice: protocol.Yuan(11), BuyCost: 1000, SellIncome: 1150},
	}
	benchmark := extend.Klines{
		&extend.Kline{Unix: d1.Unix(), Kline: &protocol.Kline{Time: d1, Close: protocol.Yuan(100)}},
		&extend.Kline{Unix: d2.Unix(), Kline: &protocol.Kline{Time: d2, Close: protocol.Yuan(110)}},
		&extend.Kline{Unix: d3.Unix(), Kline: &protocol.Kline{Time: d3, Close: protocol.Yuan(100)}},
		&extend.Kline{Unix: d4.Unix(), Kline: &protocol.Kline{Time: d4, Close: protocol.Yuan(120)}},
	}

	alpha, beta := computeTradeAlphaBeta(trades, benchmark)
	if math.Abs(alpha-(-0.05)) > 1e-6 || math.Abs(beta-1) > 1e-6 {
		t.Fatalf("expected net alpha=-0.05 beta=1, got alpha=%v beta=%v", alpha, beta)
	}
}

func TestAlphaBeta长度不一致返回0(t *testing.T) {
	alpha, beta := AlphaBeta([]float64{0.01, 0.02}, []float64{0.01})
	if alpha != 0 || beta != 0 {
		t.Fatalf("长度不一致期望 alpha=0 beta=0, 实际 alpha=%v beta=%v", alpha, beta)
	}
}
