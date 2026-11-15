package lab

import (
	"encoding/json"
	"math"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/lib/extend"
	f "github.com/injoyai/strategy-tail/strategies/factor"
	"github.com/injoyai/tdx/v2/protocol"
)

func TestTurnoverPerPriceMoveInResearchAndBuilder(t *testing.T) {
	const kind = "turnover_per_price_move"
	const code = "sh600001"
	dir := t.TempDir()
	setupAnalysisData(t, dir)
	var catalog []f.CatalogEntry
	if err := json.Unmarshal(doReq(t, NewServer().Handler(), http.MethodGet, "/api/factors", nil, http.StatusOK), &catalog); err != nil {
		t.Fatal(err)
	}
	entry := catalog[len(catalog)-1]
	if entry.Kind != kind || entry.Unit != "yi_yuan_per_pct_point" || entry.DefaultDays != 1 || entry.ImplementationVersion != 1 {
		t.Fatalf("目录项 = %+v", entry)
	}

	base := time.Date(2024, 12, 31, 15, 0, 0, 0, protocol.Location())
	var bars extend.Klines
	for i, c := range []float64{100, 125, 100, 100} {
		at := base.AddDate(0, 0, i)
		bars = append(bars, &extend.Kline{Unix: at.Unix(), Kline: &protocol.Kline{
			Time: at, Open: c, High: c, Low: c, Close: c, Volume: 1000, Amount: 200_000_000,
		}})
	}
	bars[1].Amount = 500_000_000
	seedTestDays(t, dir, code, bars)
	cfg := AnalyzeConfig{
		RunConfig: RunConfig{StartYear: 2025, EndYear: 2025, SampleMode: "codes", SampleCodes: []string{code}},
		Kind:      kind, Days: 1, Window: 1,
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	report, err := (&Runner{}).runAnalysis("", cfg, make(chan struct{}))
	if err != nil {
		t.Fatal(err)
	}
	if report.Factor.Unit != entry.Unit || report.Factor.ImplementationVersion != entry.ImplementationVersion {
		t.Fatalf("报告因子快照 = %+v", report.Factor)
	}
	var observations int
	var minimum, maximum = math.Inf(1), math.Inf(-1)
	for _, group := range report.Groups {
		observations += group.Observations
		if group.FactorMin != nil && group.FactorMax != nil {
			minimum = math.Min(minimum, *group.FactorMin)
			maximum = math.Max(maximum, *group.FactorMax)
		}
	}
	if observations != 2 || math.Abs(minimum-0.1) > 1e-12 || math.Abs(maximum-0.2) > 1e-12 {
		t.Fatalf("观测数 = %d，值域 = [%v,%v]，预期 2 个样本 [0.1,0.2]", observations, minimum, maximum)
	}

	ft := FactorFilterSpec{Kind: kind, Days: 2, FactorVersion: 1, Operator: "lte", Max: fptr(0.16)}
	spec := BuilderSpec{Version: 1, Name: "每1%涨跌成交额", Buy: factorNode(ft), SellSignal: factorNode(ft)}
	store := NewBuilderStore(t.TempDir())
	saved, err := store.Create(spec)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Get(saved.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(loaded.Spec, spec) {
		t.Fatalf("重载后因子条件变化: %+v", loaded.Spec)
	}
	buyer, err := loaded.Spec.Buyer()
	if err != nil {
		t.Fatal(err)
	}
	seller, err := loaded.Spec.Seller()
	if err != nil {
		t.Fatal(err)
	}
	variants, err := (StrategySpec{Version: 1, FactorFilters: []FactorFilterSpec{ft}}).Variants()
	if err != nil {
		t.Fatal(err)
	}
	for i := range bars {
		prefix, want := bars[:i+1], i == 2
		if buyer.Buy(code, prefix) != want || seller.Sell(code, prefix, core.Buy{}) != want || variants[1].Buyer.Buy(code, prefix) != want {
			t.Fatalf("第 %d 根 K 线，买入、卖出与因子组合应为 %v", i+1, want)
		}
	}
}
