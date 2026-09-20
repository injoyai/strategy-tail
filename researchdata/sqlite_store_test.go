package researchdata

import (
	"path/filepath"
	"testing"
	"time"
)

func TestSQLiteStorePersistsPointInTimeRecords(t *testing.T) {
	path := filepath.Join(t.TempDir(), "research.db")
	store, err := OpenSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	spec := DatasetSpec{
		ID: "valuation.daily", Kind: KindFundamental, Source: "test", Version: "1",
		Fields: []FieldSpec{{Name: "pe_ttm", Unit: "multiple"}},
	}
	if err := store.RegisterDataset(spec); err != nil {
		t.Fatal(err)
	}
	event := time.Date(2025, 1, 2, 15, 0, 0, 0, time.FixedZone("CST", 8*60*60))
	records := []Record{
		{Dataset: spec.ID, Code: "sh600000", Key: "2025-01-02", Source: "test", Revision: "r1", EventAt: event, AvailableAt: event, Values: map[string]float64{"pe_ttm": 10}},
		{Dataset: spec.ID, Code: "sh600000", Key: "2025-01-02", Source: "test", Revision: "r2", EventAt: event, AvailableAt: event.Add(time.Hour), Values: map[string]float64{"pe_ttm": 11}},
	}
	if err := store.Ingest(records); err != nil {
		t.Fatal(err)
	}
	if got, ok := store.Latest(spec.ID, "sh600000", event); !ok || got.Values["pe_ttm"] != 10 {
		t.Fatalf("close-time record = %+v, %v", got, ok)
	}
	if got, ok := store.Latest(spec.ID, "sh600000", event.Add(time.Hour)); !ok || got.Values["pe_ttm"] != 11 {
		t.Fatalf("revised record = %+v, %v", got, ok)
	}
	if got := store.Range(spec.ID, "sh600000", event, event, event.Add(time.Hour)); len(got) != 1 || got[0].Revision != "r2" {
		t.Fatalf("range = %+v", got)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := OpenSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if got, ok := reopened.Latest(spec.ID, "sh600000", event.Add(time.Hour)); !ok || got.Values["pe_ttm"] != 11 {
		t.Fatalf("reopened record = %+v, %v", got, ok)
	}
}

func TestSQLiteStoreRejectsDatasetDriftAndUnknownFields(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "research.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	spec := DatasetSpec{ID: "valuation.daily", Kind: KindFundamental, Source: "test", Version: "1", Fields: []FieldSpec{{Name: "pe_ttm"}}}
	if err := store.RegisterDataset(spec); err != nil {
		t.Fatal(err)
	}
	if err := store.RegisterDataset(spec); err != nil {
		t.Fatalf("same contract should be idempotent: %v", err)
	}
	drifted := spec
	drifted.Version = "2"
	if err := store.RegisterDataset(drifted); err == nil {
		t.Fatal("different contract should be rejected")
	}
	now := time.Now()
	err = store.Ingest([]Record{{
		Dataset: spec.ID, Code: "sh600000", Key: "k", Source: "test",
		EventAt: now, AvailableAt: now, Values: map[string]float64{"pb": 1},
	}})
	if err == nil {
		t.Fatal("undeclared field should be rejected")
	}
}
