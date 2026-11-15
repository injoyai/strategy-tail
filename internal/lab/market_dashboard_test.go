package lab

import (
	"math"
	"testing"

	"github.com/injoyai/strategy-tail/internal/marketstore"
)

func TestMarketDashboardStockSummary(t *testing.T) {
	rows := []marketstore.DashboardStockDay{
		{Code: "sh600001", Close: 11, Previous: 10, Amount: 300},
		{Code: "sz000001", Close: 11, Previous: 10, Amount: 300},
		{Code: "bj920001", Close: 8, Previous: 10, Amount: 100},
		{Code: "sh600002", Close: 10, Previous: 10, Amount: math.NaN()},
		{Code: "sz000002", Close: 25, Amount: 200},
		{Code: "sh510300", Close: 5, Previous: 5, Amount: 10000},
	}
	got := summarizeDashboardStocks(rows)
	if got.Total != 5 || got.Up != 2 || got.Flat != 1 || got.Down != 1 || got.Unclassified != 1 {
		t.Fatalf("unexpected breadth: %+v", got.dashboardBreadth)
	}
	if got.Amount != 900 || got.WithAmount != 4 || got.Exchanges[0].Total != 2 || got.Exchanges[1].Amount != 500 || got.Exchanges[2].Amount != 100 {
		t.Fatalf("unexpected amount or exchange coverage: %+v", got)
	}
	if ranks := got.Rankings.Amount; len(ranks) != 4 || ranks[0].Code != "sh600001" || ranks[1].Code != "sz000001" || ranks[2].Change != nil {
		t.Fatalf("unexpected amount ranking: %+v", ranks)
	}
	if ranks := got.Rankings.Up; len(ranks) != 2 || ranks[0].Change == nil || *ranks[0].Change != 10 {
		t.Fatalf("unexpected gainers: %+v", ranks)
	}
	if ranks := got.Rankings.Down; len(ranks) != 1 || ranks[0].Change == nil || *ranks[0].Change != -20 {
		t.Fatalf("unexpected decliners: %+v", ranks)
	}
}

func TestMarketDashboardRankingsAreBoundedAndKeepFullMarketTotals(t *testing.T) {
	rows := make([]marketstore.DashboardStockDay, 51)
	for i := range rows {
		rows[i] = marketstore.DashboardStockDay{Code: "sh600001", Close: 11, Previous: 10, Amount: float64(i + 1)}
	}
	got := summarizeDashboardStocks(rows)
	if got.Total != len(rows) || got.Amount != 1326 || len(got.Rankings.Amount) != 50 || got.Rankings.Amount[0].Amount != 51 || len(got.Rankings.Up) != 50 {
		t.Fatalf("unexpected ranking bounds or totals: %+v", got)
	}
}

func TestMarketDashboardIndexUsesLatestValidPair(t *testing.T) {
	quotes := dashboardIndexQuotes(map[string][]marketstore.DashboardIndexDay{
		"sh000001": {{Date: "2026-11-13", Close: 3000}, {Date: "2026-11-12", Close: 2500}},
		"sz399001": {{Date: "2026-11-12", Close: 13000}},
	})
	if quotes[0].Date != "2026-11-13" || quotes[0].Change == nil || *quotes[0].Change != 20 {
		t.Fatalf("unexpected Shanghai quote: %+v", quotes[0])
	}
	if quotes[1].Date != "2026-11-12" || quotes[1].Close != 13000 || quotes[1].Change != nil || quotes[2].Date != "" {
		t.Fatalf("unexpected missing-date handling: %+v", quotes)
	}
}
