package researchdata

import (
	"math"
	"testing"
	"time"
)

func testTime(day int) time.Time {
	return time.Date(2025, 1, day, 9, 0, 0, 0, time.UTC)
}

func newTestStore(t *testing.T) *Store {
	t.Helper()
	store := NewStore()
	for _, spec := range []DatasetSpec{
		{ID: "financial.income", Kind: KindFinancial, Source: "test", Version: "1"},
		{ID: "announcement.events", Kind: KindAnnouncement, Source: "test", Version: "1"},
	} {
		if err := store.RegisterDataset(spec); err != nil {
			t.Fatal(err)
		}
	}
	return store
}

func TestStorePointInTimeRevisionVisibility(t *testing.T) {
	store := newTestStore(t)
	records := []Record{
		{
			Dataset: "financial.income", Code: "sh600000", Key: "2024FY", Source: "test",
			Revision: "original", EventAt: testTime(1), AvailableAt: testTime(5),
			Values: map[string]float64{"net_profit": 100},
		},
		{
			Dataset: "financial.income", Code: "sh600000", Key: "2024FY", Source: "test",
			Revision: "restated", EventAt: testTime(1), AvailableAt: testTime(10),
			Values: map[string]float64{"net_profit": 80},
		},
	}
	if err := store.Ingest(records); err != nil {
		t.Fatal(err)
	}
	if _, ok := store.Latest("financial.income", "sh600000", testTime(4)); ok {
		t.Fatal("record was visible before its first publication")
	}
	original, ok := store.Latest("financial.income", "sh600000", testTime(7))
	if !ok || original.Values["net_profit"] != 100 || original.Revision != "original" {
		t.Fatalf("original as-of view = %#v, want original value 100", original)
	}
	restated, ok := store.Latest("financial.income", "sh600000", testTime(12))
	if !ok || restated.Values["net_profit"] != 80 || restated.Revision != "restated" {
		t.Fatalf("restated as-of view = %#v, want restated value 80", restated)
	}
}

func TestStoreRangeDeduplicatesRevisions(t *testing.T) {
	store := newTestStore(t)
	records := []Record{
		{Dataset: "announcement.events", Code: "sh600000", Key: "a1", Source: "test", EventAt: testTime(2), AvailableAt: testTime(2), Attributes: map[string]string{"type": "forecast"}},
		{Dataset: "announcement.events", Code: "sh600000", Key: "a1", Source: "test", Revision: "fixed", EventAt: testTime(2), AvailableAt: testTime(3), Attributes: map[string]string{"type": "correction"}},
		{Dataset: "announcement.events", Code: "sh600000", Key: "a2", Source: "test", EventAt: testTime(4), AvailableAt: testTime(4), Attributes: map[string]string{"type": "forecast"}},
	}
	if err := store.Ingest(records); err != nil {
		t.Fatal(err)
	}
	got := store.Range("announcement.events", "sh600000", testTime(1), testTime(5), testTime(5))
	if len(got) != 2 {
		t.Fatalf("Range() len = %d, want 2", len(got))
	}
	if got[0].Attributes["type"] != "correction" {
		t.Fatalf("Range()[0] revision = %#v, want corrected revision", got[0])
	}
}

func TestStoreRejectsMissingAvailabilityWithoutPartialWrite(t *testing.T) {
	store := newTestStore(t)
	err := store.Ingest([]Record{
		{Dataset: "financial.income", Code: "sh600000", Key: "valid", Source: "test", EventAt: testTime(1), AvailableAt: testTime(2), Values: map[string]float64{"revenue": 1}},
		{Dataset: "financial.income", Code: "sh600000", Key: "invalid", Source: "test", EventAt: testTime(1), Values: map[string]float64{"revenue": 2}},
	})
	if err == nil {
		t.Fatal("Ingest() error = nil, want missing available time error")
	}
	if _, ok := store.Latest("financial.income", "sh600000", testTime(10)); ok {
		t.Fatal("valid prefix was written even though the batch was rejected")
	}
}

func TestDatasetSpecRejectsDuplicateFields(t *testing.T) {
	err := (DatasetSpec{
		ID: "fundamental.daily", Kind: KindFundamental, Source: "test", Version: "1",
		Fields: []FieldSpec{{Name: "pe"}, {Name: "pe"}},
	}).Validate()
	if err == nil {
		t.Fatal("Validate() error = nil, want duplicate field error")
	}
}

func TestRecordRejectsNonFiniteValue(t *testing.T) {
	err := (Record{
		Dataset: "fundamental.daily", Code: "sh600000", Key: "d1", Source: "test",
		EventAt: testTime(1), AvailableAt: testTime(2), Values: map[string]float64{"pe": math.NaN()},
	}).Validate()
	if err == nil {
		t.Fatal("Validate() error = nil, want non-finite value error")
	}
}

func TestStoreRejectsUndeclaredNumericField(t *testing.T) {
	store := NewStore()
	if err := store.RegisterDataset(DatasetSpec{
		ID: "fundamental.daily", Kind: KindFundamental, Source: "test", Version: "1",
		Fields: []FieldSpec{{Name: "pe_ttm"}},
	}); err != nil {
		t.Fatal(err)
	}
	err := store.Ingest([]Record{{
		Dataset: "fundamental.daily", Code: "sh600000", Key: "d1", Source: "test",
		EventAt: testTime(1), AvailableAt: testTime(2), Values: map[string]float64{"pb": 1.2},
	}})
	if err == nil {
		t.Fatal("Ingest() error = nil, want undeclared field error")
	}
}
