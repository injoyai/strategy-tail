package core

import (
	"math"
	"testing"
	"time"

	"github.com/injoyai/strategy-tail/lib/extend"
	"github.com/injoyai/strategy-tail/researchdata"
	"github.com/injoyai/tdx/protocol"
)

type testLegacyFactor struct{}

func (testLegacyFactor) Name() string { return "legacy" }
func (testLegacyFactor) Value(_ string, dks extend.Klines) float64 {
	return float64(len(dks))
}

type testContextFactor struct{}

func (testContextFactor) Name() string { return "context" }
func (testContextFactor) ValueAt(input FactorContext) float64 {
	record, ok := input.Data.Latest("fundamental.daily", input.Code, input.AsOf)
	if !ok {
		return math.NaN()
	}
	return record.Values["pe_ttm"]
}

func TestContextualPreservesLegacyFactor(t *testing.T) {
	d := time.Date(2025, 1, 2, 15, 0, 0, 0, time.Local)
	dks := extend.Klines{{Kline: &protocol.Kline{Time: d}}}
	got := Contextual(testLegacyFactor{}).ValueAt(FactorContext{Code: "sh600000", AsOf: d, Klines: dks})
	if got != 1 {
		t.Fatalf("ValueAt() = %v, want 1", got)
	}
}

func TestBindContextFactorUsesKlineTimeAsAsOf(t *testing.T) {
	store := researchdata.NewStore()
	if err := store.RegisterDataset(researchdata.DatasetSpec{
		ID: "fundamental.daily", Kind: researchdata.KindFundamental,
		Source: "test", Version: "1",
	}); err != nil {
		t.Fatal(err)
	}
	event := time.Date(2025, 1, 1, 0, 0, 0, 0, time.Local)
	available := time.Date(2025, 1, 3, 9, 0, 0, 0, time.Local)
	if err := store.Ingest([]researchdata.Record{{
		Dataset: "fundamental.daily", Code: "sh600000", Key: "2025-01-01",
		Source: "test", EventAt: event, AvailableAt: available,
		Values: map[string]float64{"pe_ttm": 12.5},
	}}); err != nil {
		t.Fatal(err)
	}
	factor := BindContextFactor(testContextFactor{}, store)
	before := extend.Klines{{Kline: &protocol.Kline{Time: available.Add(-time.Minute)}}}
	if got := factor.Value("sh600000", before); !math.IsNaN(got) {
		t.Fatalf("value before publication = %v, want NaN", got)
	}
	after := extend.Klines{{Kline: &protocol.Kline{Time: available}}}
	if got := factor.Value("sh600000", after); got != 12.5 {
		t.Fatalf("value after publication = %v, want 12.5", got)
	}
}

func TestBindContextFactorEmptyKlines(t *testing.T) {
	if got := BindContextFactor(testContextFactor{}, researchdata.NewStore()).Value("sh600000", nil); !math.IsNaN(got) {
		t.Fatalf("empty K-lines value = %v, want NaN", got)
	}
}
