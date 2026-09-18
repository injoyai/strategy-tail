// Package researchdata defines provider-neutral, point-in-time-safe research
// data contracts. It intentionally does not depend on a concrete vendor or
// storage engine so financial, fundamental, announcement and alternative data
// can share the same factor input boundary.
package researchdata

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

// Kind describes the broad semantics of a dataset. Dataset IDs remain the
// stable machine-readable identifiers used by factors and providers.
type Kind string

const (
	KindFundamental  Kind = "fundamental"
	KindFinancial    Kind = "financial"
	KindAnnouncement Kind = "announcement"
	KindReference    Kind = "reference"
	KindAlternative  Kind = "alternative"
)

// FieldSpec documents one normalized field exposed by a dataset.
type FieldSpec struct {
	Name        string `json:"name"`
	Unit        string `json:"unit,omitempty"`
	Description string `json:"description,omitempty"`
}

// DatasetSpec is the catalog contract owned by a provider. Version must change
// when field meaning, normalization or point-in-time semantics change.
type DatasetSpec struct {
	ID          string      `json:"id"`
	Kind        Kind        `json:"kind"`
	Source      string      `json:"source"`
	Version     string      `json:"version"`
	Description string      `json:"description,omitempty"`
	Fields      []FieldSpec `json:"fields,omitempty"`
}

// Validate rejects underspecified datasets before they enter a catalog.
func (s DatasetSpec) Validate() error {
	if strings.TrimSpace(s.ID) == "" {
		return fmt.Errorf("researchdata: dataset id is empty")
	}
	if s.Kind == "" {
		return fmt.Errorf("researchdata: dataset %q kind is empty", s.ID)
	}
	if strings.TrimSpace(s.Source) == "" {
		return fmt.Errorf("researchdata: dataset %q source is empty", s.ID)
	}
	if strings.TrimSpace(s.Version) == "" {
		return fmt.Errorf("researchdata: dataset %q version is empty", s.ID)
	}
	seen := make(map[string]struct{}, len(s.Fields))
	for _, field := range s.Fields {
		name := strings.TrimSpace(field.Name)
		if name == "" {
			return fmt.Errorf("researchdata: dataset %q has an empty field name", s.ID)
		}
		if _, ok := seen[name]; ok {
			return fmt.Errorf("researchdata: dataset %q has duplicate field %q", s.ID, name)
		}
		seen[name] = struct{}{}
	}
	return nil
}

// Record is one normalized, revision-aware observation.
//
// EventAt is when the business fact applies (for example a fiscal period end
// or announcement event time). AvailableAt is the earliest time the value was
// knowable to the strategy. Key is stable across revisions of the same fact.
// Point-in-time reads require both EventAt <= asOf and AvailableAt <= asOf.
type Record struct {
	Dataset     string             `json:"dataset"`
	Code        string             `json:"code"`
	Key         string             `json:"key"`
	Source      string             `json:"source"`
	Revision    string             `json:"revision,omitempty"`
	EventAt     time.Time          `json:"eventAt"`
	AvailableAt time.Time          `json:"availableAt"`
	Values      map[string]float64 `json:"values,omitempty"`
	Attributes  map[string]string  `json:"attributes,omitempty"`
}

// Validate rejects records that cannot be queried without look-ahead or cannot
// be deterministically de-duplicated across revisions.
func (r Record) Validate() error {
	if strings.TrimSpace(r.Dataset) == "" {
		return fmt.Errorf("researchdata: record dataset is empty")
	}
	if strings.TrimSpace(r.Code) == "" {
		return fmt.Errorf("researchdata: record code is empty")
	}
	if strings.TrimSpace(r.Key) == "" {
		return fmt.Errorf("researchdata: record key is empty")
	}
	if strings.TrimSpace(r.Source) == "" {
		return fmt.Errorf("researchdata: record source is empty")
	}
	if r.EventAt.IsZero() {
		return fmt.Errorf("researchdata: record %q event time is empty", r.Key)
	}
	if r.AvailableAt.IsZero() {
		return fmt.Errorf("researchdata: record %q available time is empty", r.Key)
	}
	if len(r.Values) == 0 && len(r.Attributes) == 0 {
		return fmt.Errorf("researchdata: record %q has no values or attributes", r.Key)
	}
	for field, value := range r.Values {
		if strings.TrimSpace(field) == "" {
			return fmt.Errorf("researchdata: record %q has an empty value field", r.Key)
		}
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return fmt.Errorf("researchdata: record %q field %q is not finite", r.Key, field)
		}
	}
	for field := range r.Attributes {
		if strings.TrimSpace(field) == "" {
			return fmt.Errorf("researchdata: record %q has an empty attribute field", r.Key)
		}
	}
	return nil
}

// Query describes a provider fetch. Start and End apply to EventAt. AsOf is a
// mandatory availability cutoff; providers must not knowingly return revisions
// that were unavailable at that time.
type Query struct {
	Dataset string
	Codes   []string
	Start   time.Time
	End     time.Time
	AsOf    time.Time
}

func (q Query) Validate() error {
	if strings.TrimSpace(q.Dataset) == "" {
		return fmt.Errorf("researchdata: query dataset is empty")
	}
	if q.AsOf.IsZero() {
		return fmt.Errorf("researchdata: query as-of time is empty")
	}
	if !q.Start.IsZero() && !q.End.IsZero() && q.End.Before(q.Start) {
		return fmt.Errorf("researchdata: query end precedes start")
	}
	return nil
}

// Provider supplies one or more normalized datasets. Fetch should return raw
// revisions; Store applies the final point-in-time filter and de-duplication.
type Provider interface {
	ID() string
	Catalog() []DatasetSpec
	Fetch(context.Context, Query) ([]Record, error)
}

// View is the minimal read contract consumed by context-aware factors. A
// database-backed implementation can replace Store without changing factors.
type View interface {
	Latest(dataset, code string, asOf time.Time) (Record, bool)
	Range(dataset, code string, start, end, asOf time.Time) []Record
}

func cloneRecord(r Record) Record {
	r.Values = cloneMap(r.Values)
	r.Attributes = cloneMap(r.Attributes)
	return r
}

func cloneMap[K comparable, V any](in map[K]V) map[K]V {
	if in == nil {
		return nil
	}
	out := make(map[K]V, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func sortRecords(records []Record) {
	sort.Slice(records, func(i, j int) bool {
		if !records[i].EventAt.Equal(records[j].EventAt) {
			return records[i].EventAt.Before(records[j].EventAt)
		}
		if !records[i].AvailableAt.Equal(records[j].AvailableAt) {
			return records[i].AvailableAt.Before(records[j].AvailableAt)
		}
		return records[i].Key < records[j].Key
	})
}
