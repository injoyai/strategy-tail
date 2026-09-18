package factor

import (
	"math"
	"testing"
	"time"

	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/researchdata"
)

func dataTime(day int) time.Time {
	return time.Date(2025, 1, day, 15, 0, 0, 0, time.Local)
}

func dataStore(t *testing.T) *researchdata.Store {
	t.Helper()
	store := researchdata.NewStore()
	for _, spec := range []researchdata.DatasetSpec{
		{ID: "fundamental.daily", Kind: researchdata.KindFundamental, Source: "test", Version: "1"},
		{ID: "financial.indicator", Kind: researchdata.KindFinancial, Source: "test", Version: "1"},
		{ID: "announcement.events", Kind: researchdata.KindAnnouncement, Source: "test", Version: "1"},
	} {
		if err := store.RegisterDataset(spec); err != nil {
			t.Fatal(err)
		}
	}
	return store
}

func TestLatestFieldRespectsAvailableAt(t *testing.T) {
	store := dataStore(t)
	if err := store.Ingest([]researchdata.Record{{
		Dataset: "fundamental.daily", Code: "sh600000", Key: "d1", Source: "test",
		EventAt: dataTime(1), AvailableAt: dataTime(3), Values: map[string]float64{"pe_ttm": 12.5},
	}}); err != nil {
		t.Fatal(err)
	}
	factor := 最新字段{Dataset: "fundamental.daily", Field: "pe_ttm", Label: "市盈率TTM"}
	if got := factor.ValueAt(core.FactorContext{Code: "sh600000", AsOf: dataTime(2), Data: store}); !math.IsNaN(got) {
		t.Fatalf("value before availability = %v, want NaN", got)
	}
	if got := factor.ValueAt(core.FactorContext{Code: "sh600000", AsOf: dataTime(3), Data: store}); got != 12.5 {
		t.Fatalf("value after availability = %v, want 12.5", got)
	}
}

func TestFieldChangeUsesVisibleBusinessPeriods(t *testing.T) {
	store := dataStore(t)
	if err := store.Ingest([]researchdata.Record{
		{Dataset: "financial.indicator", Code: "sh600000", Key: "2024Q3", Source: "test", EventAt: dataTime(1), AvailableAt: dataTime(2), Values: map[string]float64{"roe": 0.10}},
		{Dataset: "financial.indicator", Code: "sh600000", Key: "2024Q4", Source: "test", EventAt: dataTime(5), AvailableAt: dataTime(7), Values: map[string]float64{"roe": 0.12}},
	}); err != nil {
		t.Fatal(err)
	}
	factor := 字段变化率{Dataset: "financial.indicator", Field: "roe", Periods: 1}
	if got := factor.ValueAt(core.FactorContext{Code: "sh600000", AsOf: dataTime(6), Data: store}); !math.IsNaN(got) {
		t.Fatalf("value before second publication = %v, want NaN", got)
	}
	if got := factor.ValueAt(core.FactorContext{Code: "sh600000", AsOf: dataTime(8), Data: store}); math.Abs(got-0.2) > 1e-12 {
		t.Fatalf("growth = %v, want 0.2", got)
	}
}

func TestEventCountFiltersAnnouncementType(t *testing.T) {
	store := dataStore(t)
	if err := store.Ingest([]researchdata.Record{
		{Dataset: "announcement.events", Code: "sh600000", Key: "a1", Source: "test", EventAt: dataTime(3), AvailableAt: dataTime(3), Attributes: map[string]string{"type": "forecast"}},
		{Dataset: "announcement.events", Code: "sh600000", Key: "a2", Source: "test", EventAt: dataTime(4), AvailableAt: dataTime(4), Attributes: map[string]string{"type": "risk"}},
		{Dataset: "announcement.events", Code: "sh600000", Key: "a3", Source: "test", EventAt: dataTime(5), AvailableAt: dataTime(6), Attributes: map[string]string{"type": "forecast"}},
	}); err != nil {
		t.Fatal(err)
	}
	factor := 事件计数{Dataset: "announcement.events", Days: 5, Attribute: "type", Equals: "forecast"}
	if got := factor.ValueAt(core.FactorContext{Code: "sh600000", AsOf: dataTime(5), Data: store}); got != 1 {
		t.Fatalf("count before delayed event publication = %v, want 1", got)
	}
	if got := factor.ValueAt(core.FactorContext{Code: "sh600000", AsOf: dataTime(6), Data: store}); got != 2 {
		t.Fatalf("count after delayed event publication = %v, want 2", got)
	}
}
