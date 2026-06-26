package core

import (
	"math"
	"testing"

	"github.com/injoyai/tdx/protocol"
)

func TestProfitFactor百分比口径(t *testing.T) {
	// 等手数买入下，应该按收益率累计盈亏，而不是按绝对金额。
	// A: 10 -> 11   收益率 +10%
	// B: 100 -> 99  收益率 -1%
	// 期望 ProfitFactor = 10 / 1 = 10
	trades := []Trade{
		{BuyPrice: protocol.Yuan(10), SellPrice: protocol.Yuan(11)},
		{BuyPrice: protocol.Yuan(100), SellPrice: protocol.Yuan(99)},
	}

	stats := Stats(trades)

	if math.Abs(stats.ProfitFactor-10) > 1e-6 {
		t.Fatalf("expected ProfitFactor=10, got %v", stats.ProfitFactor)
	}
}

func TestStats胜率和数量(t *testing.T) {
	trades := []Trade{
		{BuyPrice: protocol.Yuan(10), SellPrice: protocol.Yuan(11)},
		{BuyPrice: protocol.Yuan(10), SellPrice: protocol.Yuan(9)},
		{BuyPrice: protocol.Yuan(10), SellPrice: protocol.Yuan(10)},
	}

	stats := Stats(trades)

	if stats.Total != 3 {
		t.Fatalf("expected Total=3, got %d", stats.Total)
	}
	if stats.Win != 1 {
		t.Fatalf("expected Win=1, got %d", stats.Win)
	}
	if stats.Loss != 1 {
		t.Fatalf("expected Loss=1, got %d", stats.Loss)
	}
	if math.Abs(stats.WinRate-(1.0/3.0*100)) > 1e-6 {
		t.Fatalf("expected WinRate=33.33..., got %v", stats.WinRate)
	}
}

func TestStats无亏损时ProfitFactor为正无穷(t *testing.T) {
	trades := []Trade{
		{BuyPrice: protocol.Yuan(10), SellPrice: protocol.Yuan(11)},
	}

	stats := Stats(trades)

	if !math.IsInf(stats.ProfitFactor, 1) {
		t.Fatalf("expected ProfitFactor=+Inf, got %v", stats.ProfitFactor)
	}
}

func TestStats空交易返回零值(t *testing.T) {
	stats := Stats(nil)

	if stats.Total != 0 || stats.ProfitFactor != 0 {
		t.Fatalf("expected zero stats, got %+v", stats)
	}
}
