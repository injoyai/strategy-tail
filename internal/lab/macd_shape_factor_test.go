package lab

import (
	"encoding/json"
	"net/http"
	"reflect"
	"testing"

	"github.com/injoyai/strategy-tail/core"
	sb "github.com/injoyai/strategy-tail/strategies/buy"
	f "github.com/injoyai/strategy-tail/strategies/factor"
)

func TestMACDShapeFactorsInResearchAndBuilder(t *testing.T) {
	t.Chdir(t.TempDir())
	h := NewServer().Handler()
	var catalog []f.CatalogEntry
	if err := json.Unmarshal(doReq(t, h, http.MethodGet, "/api/factors", nil, http.StatusOK), &catalog); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		kind   string
		days   int
		max    float64
		legacy core.Buyer
	}{
		{"macd_negative_shrinking_streak", 1, 3, sb.MACD负柱缩短{MinDays: 1, MaxDays: 3}},
		{"macd_same_sign_reversals", 4, 0, sb.MACD顺滑{Days: 4, MaxReversals: 0}},
		{"macd_same_sign_reversals", 10, 2, sb.MACD顺滑{Days: 10, MaxReversals: 2}},
		{"macd_same_sign_max_change", 5, 0.5, sb.MACD平滑{Days: 5, MaxRatio: 0.5}},
	} {
		t.Run(tt.kind, func(t *testing.T) {
			found := false
			for _, entry := range catalog {
				if entry.Kind == tt.kind {
					found = true
					if entry.ImplementationVersion != 1 {
						t.Fatal("new factor version must be 1")
					}
				}
			}
			if !found {
				t.Fatal("missing catalog factor")
			}
			cfg := AnalyzeConfig{RunConfig: RunConfig{StartYear: 2025, EndYear: 2025, SampleMode: "all"}, Kind: tt.kind, Days: tt.days, Window: 5}
			if err := cfg.Validate(); err != nil {
				t.Fatal(err)
			}
			ft := FactorFilterSpec{Kind: tt.kind, Days: tt.days, FactorVersion: 1, Operator: "lte", Max: fptr(tt.max)}
			if tt.kind == "macd_negative_shrinking_streak" {
				ft.Operator = "between"
				ft.Min = fptr(1)
			}
			spec := BuilderSpec{Version: 1, Name: "MACD形态", Buy: factorNode(ft), SellSignal: factorNode(ft)}
			if err := spec.validate("run"); err != nil {
				t.Fatal(err)
			}
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
				t.Fatal("factor spec changed on reload")
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
			for _, flat := range []bool{false, true} {
				bars := conditionBaselineBars(flat)
				for end := 0; end <= len(bars); end++ {
					ks := bars[:end]
					want := tt.legacy.Buy("sh600000", ks)
					if buyer.Buy("sh600000", ks) != want || seller.Sell("sh600000", ks, core.Buy{}) != want || variants[1].Buyer.Buy("sh600000", ks) != want {
						t.Fatalf("prefix %d: factor and legacy signals differ", end)
					}
				}
			}
		})
	}
}

func TestMACDReversalFromFactorConditions(t *testing.T) {
	spec := BuilderSpec{Version: 1, Name: "因子反转", Buy: bGroup("and",
		factorNode(FactorFilterSpec{Kind: "macd_trough_position", Days: 20, FactorVersion: 1, Operator: "between", Min: fptr(0), Max: fptr(0)}),
		factorNode(FactorFilterSpec{Kind: "macd_rising_streak", Days: 1, FactorVersion: 1, Operator: "gte", Min: fptr(1)})), HoldToEnd: true}
	buyer, err := spec.Buyer()
	if err != nil {
		t.Fatal(err)
	}
	for _, flat := range []bool{false, true} {
		bars := conditionBaselineBars(flat)
		for end := 0; end <= len(bars); end++ {
			if got, want := buyer.Buy("sh600000", bars[:end]), (sb.MACD反转{}).Buy("sh600000", bars[:end]); got != want {
				t.Fatalf("prefix %d: reversal = %v, want %v", end, got, want)
			}
		}
	}
}
