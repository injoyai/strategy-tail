package core

import (
	"math"
	"testing"
	"time"

	"github.com/injoyai/strategy-tail/lib/extend"
	"github.com/injoyai/tdx/protocol"
)

func TestAnalyzeUsesNetProfitAmount(t *testing.T) {
	chdirTemp(t)
	buyTime := time.Date(2026, 1, 5, 15, 0, 0, 0, time.Local)
	trade := Trade{
		Code:       "sh600000",
		BuyTime:    buyTime,
		SellTime:   buyTime.AddDate(0, 0, 1),
		BuyPrice:   protocol.Yuan(10),
		SellPrice:  protocol.Yuan(10.5),
		BuyCost:    1005,
		SellIncome: 1030,
		Quantity:   100,
	}
	getDayKlines := func(string, time.Time, time.Time) (extend.Klines, error) {
		return nil, nil
	}

	result := Analyze(2026, []Trade{trade}, getDayKlines, nil, Cost{}, PositionConfig{})
	if math.Abs(result.TotalProfit-25) > 1e-6 {
		t.Fatalf("expected net TotalProfit=25, got %v", result.TotalProfit)
	}
	wantRate := (1030.0 - 1005.0) / 1005.0 * 100
	if math.Abs(result.AvgProfit-wantRate) > 1e-6 {
		t.Fatalf("expected net AvgProfit=%v, got %v", wantRate, result.AvgProfit)
	}
}
