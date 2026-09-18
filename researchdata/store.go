package researchdata

import (
	"fmt"
	"sync"
	"time"
)

// Store is an in-memory reference implementation of View. It is suitable for
// bounded research runs and tests; larger datasets can provide their own View.
type Store struct {
	mu       sync.RWMutex
	datasets map[string]DatasetSpec
	records  map[string]map[string][]Record // dataset -> code -> revisions
}

func NewStore() *Store {
	return &Store{
		datasets: make(map[string]DatasetSpec),
		records:  make(map[string]map[string][]Record),
	}
}

// RegisterDataset adds one immutable dataset contract.
func (s *Store) RegisterDataset(spec DatasetSpec) error {
	if err := spec.Validate(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if old, ok := s.datasets[spec.ID]; ok {
		return fmt.Errorf("researchdata: dataset %q already registered by %q", spec.ID, old.Source)
	}
	spec.Fields = append([]FieldSpec(nil), spec.Fields...)
	s.datasets[spec.ID] = spec
	return nil
}

// Ingest validates the whole batch before mutating the store.
func (s *Store) Ingest(records []Record) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, record := range records {
		if err := record.Validate(); err != nil {
			return err
		}
		spec, ok := s.datasets[record.Dataset]
		if !ok {
			return fmt.Errorf("researchdata: dataset %q is not registered", record.Dataset)
		}
		if record.Source != spec.Source {
			return fmt.Errorf("researchdata: dataset %q source %q does not match catalog source %q", record.Dataset, record.Source, spec.Source)
		}
		if len(spec.Fields) > 0 {
			allowed := make(map[string]struct{}, len(spec.Fields))
			for _, field := range spec.Fields {
				allowed[field.Name] = struct{}{}
			}
			for field := range record.Values {
				if _, ok := allowed[field]; !ok {
					return fmt.Errorf("researchdata: dataset %q does not declare field %q", record.Dataset, field)
				}
			}
		}
	}
	for _, record := range records {
		byCode := s.records[record.Dataset]
		if byCode == nil {
			byCode = make(map[string][]Record)
			s.records[record.Dataset] = byCode
		}
		byCode[record.Code] = append(byCode[record.Code], cloneRecord(record))
		sortRecords(byCode[record.Code])
	}
	return nil
}

// Latest returns the newest business fact visible at asOf. If that fact has
// revisions, the newest revision available at asOf wins.
func (s *Store) Latest(dataset, code string, asOf time.Time) (Record, bool) {
	if asOf.IsZero() {
		return Record{}, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	var best Record
	found := false
	for _, record := range s.records[dataset][code] {
		if record.EventAt.After(asOf) || record.AvailableAt.After(asOf) {
			continue
		}
		if !found || record.EventAt.After(best.EventAt) ||
			(record.EventAt.Equal(best.EventAt) && record.AvailableAt.After(best.AvailableAt)) {
			best = record
			found = true
		}
	}
	return cloneRecord(best), found
}

// Range returns one visible revision per stable record key, ordered by EventAt.
// Start/End are inclusive; a zero boundary is open.
func (s *Store) Range(dataset, code string, start, end, asOf time.Time) []Record {
	if asOf.IsZero() {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	latest := make(map[string]Record)
	for _, record := range s.records[dataset][code] {
		if record.EventAt.After(asOf) || record.AvailableAt.After(asOf) {
			continue
		}
		if !start.IsZero() && record.EventAt.Before(start) {
			continue
		}
		if !end.IsZero() && record.EventAt.After(end) {
			continue
		}
		old, ok := latest[record.Key]
		if !ok || record.AvailableAt.After(old.AvailableAt) {
			latest[record.Key] = record
		}
	}
	out := make([]Record, 0, len(latest))
	for _, record := range latest {
		out = append(out, cloneRecord(record))
	}
	sortRecords(out)
	return out
}
