package researchdata

import (
	"context"
	"testing"
	"time"
)

type fakeProvider struct {
	id      string
	catalog []DatasetSpec
	records []Record
}

func (p fakeProvider) ID() string             { return p.id }
func (p fakeProvider) Catalog() []DatasetSpec { return p.catalog }
func (p fakeProvider) Fetch(context.Context, Query) ([]Record, error) {
	return p.records, nil
}

func TestRegistryRoutesAndLoadsDataset(t *testing.T) {
	spec := DatasetSpec{ID: "fundamental.daily", Kind: KindFundamental, Source: "vendor-a", Version: "1"}
	record := Record{
		Dataset: spec.ID, Code: "sh600000", Key: "2025-01-01", Source: spec.Source,
		EventAt: testTime(1), AvailableAt: testTime(2), Values: map[string]float64{"pe_ttm": 10},
	}
	registry := NewRegistry()
	if err := registry.Register(fakeProvider{id: spec.Source, catalog: []DatasetSpec{spec}, records: []Record{record}}); err != nil {
		t.Fatal(err)
	}
	store := NewStore()
	if err := store.RegisterDataset(spec); err != nil {
		t.Fatal(err)
	}
	if err := registry.Load(context.Background(), store, Query{Dataset: spec.ID, AsOf: testTime(3)}); err != nil {
		t.Fatal(err)
	}
	if got, ok := store.Latest(spec.ID, "sh600000", testTime(3)); !ok || got.Values["pe_ttm"] != 10 {
		t.Fatalf("Latest() = %#v, %v; want pe_ttm=10", got, ok)
	}
}

func TestRegistryRejectsDatasetCollision(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register(fakeProvider{id: "a", catalog: []DatasetSpec{{ID: "shared", Kind: KindAlternative, Source: "a", Version: "1"}}}); err != nil {
		t.Fatal(err)
	}
	err := registry.Register(fakeProvider{id: "b", catalog: []DatasetSpec{{ID: "shared", Kind: KindAlternative, Source: "b", Version: "1"}}})
	if err == nil {
		t.Fatal("Register() error = nil, want dataset collision")
	}
}

func TestRegistryRejectsFutureRevision(t *testing.T) {
	spec := DatasetSpec{ID: "financial.income", Kind: KindFinancial, Source: "vendor", Version: "1"}
	registry := NewRegistry()
	if err := registry.Register(fakeProvider{
		id: "vendor", catalog: []DatasetSpec{spec}, records: []Record{{
			Dataset: spec.ID, Code: "sh600000", Key: "2024FY", Source: "vendor",
			EventAt: testTime(1), AvailableAt: testTime(5), Values: map[string]float64{"revenue": 1},
		}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Fetch(context.Background(), Query{Dataset: spec.ID, AsOf: testTime(4)}); err == nil {
		t.Fatal("Fetch() error = nil, want future revision error")
	}
}

func TestHubRegistersLoadsAndServesView(t *testing.T) {
	spec := DatasetSpec{ID: "announcement.events", Kind: KindAnnouncement, Source: "vendor", Version: "1"}
	hub := NewHub()
	if err := hub.Register(fakeProvider{
		id: "vendor", catalog: []DatasetSpec{spec}, records: []Record{{
			Dataset: spec.ID, Code: "sh600000", Key: "a1", Source: "vendor",
			EventAt: testTime(1), AvailableAt: testTime(2), Attributes: map[string]string{"type": "risk"},
		}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := hub.Load(context.Background(), Query{Dataset: spec.ID, AsOf: testTime(3)}); err != nil {
		t.Fatal(err)
	}
	if got := hub.Range(spec.ID, "sh600000", time.Time{}, testTime(3), testTime(3)); len(got) != 1 {
		t.Fatalf("Range() len = %d, want 1", len(got))
	}
}
