package core

import (
	"math"
	"testing"

	"github.com/injoyai/tdx/protocol"
)

// 构造一批收益率有差异的交易用于蒙特卡洛测试。
// 与项目回测口径一致：收益率 = (SellPrice - BuyPrice) / BuyPrice。
func mcTestTrades() []Trade {
	return []Trade{
		{BuyPrice: protocol.Yuan(100), SellPrice: protocol.Yuan(110)},  // +10%
		{BuyPrice: protocol.Yuan(100), SellPrice: protocol.Yuan(104)},  // +4%
		{BuyPrice: protocol.Yuan(100), SellPrice: protocol.Yuan(102)},  // +2%
		{BuyPrice: protocol.Yuan(100), SellPrice: protocol.Yuan(101)},  // +1%
		{BuyPrice: protocol.Yuan(100), SellPrice: protocol.Yuan(95)},   // -5%
		{BuyPrice: protocol.Yuan(100), SellPrice: protocol.Yuan(92)},   // -8%
		{BuyPrice: protocol.Yuan(100), SellPrice: protocol.Yuan(98)},   // -2%
		{BuyPrice: protocol.Yuan(100), SellPrice: protocol.Yuan(99)},   // -1%
	}
}

// TestMonteCarlo收益百分位有区分度
// 回归测试：旧实现用"无放回 shuffle"复利，由于复利乘法可交换，
// 最终收益率与顺序无关，会导致 ReturnP5 == ReturnP95 == 单一复利值。
// 正确实现应使用"有放回重采样"，让 P5 < P50 < P95 存在真实区分度。
func TestMonteCarlo收益百分位有区分度(t *testing.T) {
	trades := mcTestTrades()
	res := MonteCarlo(trades, 2000, 100000)

	if res.ReturnP5 >= res.ReturnP50 {
		t.Fatalf("expected ReturnP5(%v) < ReturnP50(%v)", res.ReturnP5, res.ReturnP50)
	}
	if res.ReturnP50 >= res.ReturnP95 {
		t.Fatalf("expected ReturnP50(%v) < ReturnP95(%v)", res.ReturnP50, res.ReturnP95)
	}
	// P5 与 P95 之间应有实质差距（本组样本收益离散，bootstrap 展开明显）
	if math.Abs(res.ReturnP95-res.ReturnP5) < 5.0 {
		t.Fatalf("expected P95-P5 spread >= 5%%, got %.2f%% (P5=%.2f P95=%.2f)",
			res.ReturnP95-res.ReturnP5, res.ReturnP5, res.ReturnP95)
	}
}

// TestMonteCarlo中位数接近确定性复利
// 8 笔收益率的全组合期望收益约等于确定性复利：
// Π(1+r) = 1.10*1.04*1.02*1.01*0.95*0.92*0.98*0.99。
// bootstrap 中位数应接近该值，P50 允许在 ±10pp 内。
func TestMonteCarlo中位数接近确定性复利(t *testing.T) {
	trades := mcTestTrades()
	det := 100000.0
	for _, t := range trades {
		r := (t.SellPrice.Float64() - t.BuyPrice.Float64()) / t.BuyPrice.Float64()
		det *= 1 + r
	}
	detReturn := (det - 100000) / 100000 * 100

	res := MonteCarlo(trades, 2000, 100000)
	if math.Abs(res.ReturnP50-detReturn) > 10.0 {
		t.Fatalf("expected P50≈%.1f%%, got %.1f%%", detReturn, res.ReturnP50)
	}
}

// TestMonteCarlo边界条件
func TestMonteCarlo边界条件(t *testing.T) {
	// 空交易列表：返回零值
	empty := MonteCarlo(nil, 100, 100000)
	if empty.ReturnP50 != 0 || empty.ProbProfit != 0 {
		t.Fatalf("expected zero result for empty trades, got %+v", empty)
	}

	// 非法本金：返回零值
	bad := MonteCarlo(mcTestTrades(), 100, 0)
	if bad.ReturnP50 != 0 {
		t.Fatalf("expected zero result for invalid capital, got %+v", bad)
	}
}

// TestMonteCarlo可复现性
// 固定种子 42 应保证两次调用结果完全一致。
func TestMonteCarlo可复现性(t *testing.T) {
	trades := mcTestTrades()
	a := MonteCarlo(trades, 500, 100000)
	b := MonteCarlo(trades, 500, 100000)
	if a.ReturnP50 != b.ReturnP50 || a.ProbProfit != b.ProbProfit {
		t.Fatalf("expected reproducible results, got %+v vs %+v", a, b)
	}
}
