package core

import (
	"math"
	"os"
	"path/filepath"
	"strings"
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

func TestAnalyzeDoesNotWriteArtifacts(t *testing.T) {
	chdirTemp(t)
	later := time.Date(2026, 1, 6, 15, 0, 0, 0, time.Local)
	earlier := later.AddDate(0, 0, -1)
	trades := []Trade{{BuyTime: later}, {BuyTime: earlier}}
	Analyze(2026, trades, nil, nil, Cost{}, PositionConfig{})
	if _, err := os.Stat("output"); !os.IsNotExist(err) {
		t.Fatalf("Analyze should be pure, output stat error = %v", err)
	}
	if !trades[0].BuyTime.Equal(later) {
		t.Fatal("Analyze should not reorder caller trades")
	}
}

func TestExportYearTradesCSVWritesExplicitArtifact(t *testing.T) {
	chdirTemp(t)
	trade := Trade{
		Code:       "sh600000",
		BuyTime:    time.Date(2026, 1, 5, 15, 0, 0, 0, time.Local),
		SellTime:   time.Date(2026, 1, 6, 15, 0, 0, 0, time.Local),
		BuyPrice:   protocol.Yuan(10),
		SellPrice:  protocol.Yuan(10.5),
		BuyCost:    1005,
		SellIncome: 1030,
		Quantity:   100,
	}

	path, err := ExportYearTradesCSV(2026, []Trade{trade})
	if err != nil {
		t.Fatal(err)
	}
	if path != filepath.Join("output", "backtest", "2026.csv") {
		t.Fatalf("unexpected path: %s", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "净盈亏") {
		t.Fatalf("CSV missing net-profit header: %s", data)
	}
}

func TestExportTradeVisualHTMLRejectsMissingLoader(t *testing.T) {
	err := ExportTradeVisualHTML([]int{2026}, map[int][]Trade{2026: nil}, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "缺少日线数据源") {
		t.Fatalf("ExportTradeVisualHTML() error = %v", err)
	}
}
