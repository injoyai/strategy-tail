package lab

import (
	"math"
	"slices"
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
	if want := []int{1, 0, 0, 0, 0, 0, 0, 2}; !slices.Equal(got.ChangeDistribution[:], want) {
		t.Fatalf("unexpected distribution: %v, want %v", got.ChangeDistribution, want)
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
	for _, check := range []struct {
		name  string
		ranks []dashboardStockQuote
		want  []string
	}{
		{"amount ascending", got.Rankings.AmountAsc, []string{"bj920001", "sz000002", "sh600001", "sz000001"}},
		{"change ascending", got.Rankings.ChangeAsc, []string{"bj920001", "sh600002", "sh600001", "sz000001"}},
		{"change descending", got.Rankings.ChangeDesc, []string{"sh600001", "sz000001", "sh600002", "bj920001"}},
		{"price ascending", got.Rankings.CloseAsc, []string{"bj920001", "sh600002", "sh600001", "sz000001", "sz000002"}},
		{"price descending", got.Rankings.CloseDesc, []string{"sz000002", "sh600001", "sz000001", "sh600002", "bj920001"}},
	} {
		codes := make([]string, 0, len(check.ranks))
		for _, row := range check.ranks {
			codes = append(codes, row.Code)
		}
		if !slices.Equal(codes, check.want) {
			t.Errorf("%s: got %v, want %v", check.name, codes, check.want)
		}
	}
}

func TestMarketDashboardChangeDistributionBoundaries(t *testing.T) {
	// Decimal prices exercise the rounding noise around exact cutoffs.
	closes := []float64{9.49, 9.50, 9.70, 9.71, 9.90, 9.91, 10, 10.09, 10.10, 10.29, 10.30, 10.50, 10.51}
	rows := make([]marketstore.DashboardStockDay, 0, len(closes))
	for _, close := range closes {
		rows = append(rows, marketstore.DashboardStockDay{Code: "sh600001", Close: close, Previous: 10})
	}
	got := summarizeDashboardStocks(rows)
	if want := []int{1, 2, 2, 1, 1, 2, 2, 1}; !slices.Equal(got.ChangeDistribution[:], want) {
		t.Fatalf("unexpected distribution: %v, want %v", got.ChangeDistribution, want)
	}
	if got.Up != 6 || got.Flat != 1 || got.Down != 6 {
		t.Fatalf("unexpected breadth: %+v", got.dashboardBreadth)
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
	if got.ChangeDistribution[7] != len(rows) {
		t.Fatalf("distribution should include stocks beyond the ranking limit: %v", got.ChangeDistribution)
	}
	if asc := got.Rankings.AmountAsc; len(asc) != 50 || asc[0].Amount != 1 || asc[49].Amount != 50 {
		t.Fatalf("ascending ranking must use the full market snapshot: %+v", asc)
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
