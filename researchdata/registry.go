package researchdata

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// Registry owns provider discovery and dataset routing. A dataset ID has one
// authoritative provider; source merging must be explicit in a separate
// normalized dataset rather than hidden behind registration order.
type Registry struct {
	mu        sync.RWMutex
	providers map[string]Provider
	datasets  map[string]DatasetSpec
	routes    map[string]string
}

func NewRegistry() *Registry {
	return &Registry{
		providers: make(map[string]Provider),
		datasets:  make(map[string]DatasetSpec),
		routes:    make(map[string]string),
	}
}

func (r *Registry) Register(provider Provider) error {
	if provider == nil {
		return fmt.Errorf("researchdata: provider is nil")
	}
	id := strings.TrimSpace(provider.ID())
	if id == "" {
		return fmt.Errorf("researchdata: provider id is empty")
	}
	catalog := provider.Catalog()
	if len(catalog) == 0 {
		return fmt.Errorf("researchdata: provider %q catalog is empty", id)
	}
	for _, spec := range catalog {
		if err := spec.Validate(); err != nil {
			return err
		}
		if spec.Source != id {
			return fmt.Errorf("researchdata: dataset %q source %q does not match provider %q", spec.ID, spec.Source, id)
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.providers[id]; ok {
		return fmt.Errorf("researchdata: provider %q already registered", id)
	}
	for _, spec := range catalog {
		if owner, ok := r.routes[spec.ID]; ok {
			return fmt.Errorf("researchdata: dataset %q already registered by provider %q", spec.ID, owner)
		}
	}
	r.providers[id] = provider
	for _, spec := range catalog {
		spec.Fields = append([]FieldSpec(nil), spec.Fields...)
		r.datasets[spec.ID] = spec
		r.routes[spec.ID] = id
	}
	return nil
}

func (r *Registry) Catalog() []DatasetSpec {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]DatasetSpec, 0, len(r.datasets))
	for _, spec := range r.datasets {
		spec.Fields = append([]FieldSpec(nil), spec.Fields...)
		out = append(out, spec)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (r *Registry) Fetch(ctx context.Context, query Query) ([]Record, error) {
	if err := query.Validate(); err != nil {
		return nil, err
	}
	r.mu.RLock()
	providerID, ok := r.routes[query.Dataset]
	provider := r.providers[providerID]
	r.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("researchdata: dataset %q is not registered", query.Dataset)
	}
	records, err := provider.Fetch(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("researchdata: provider %q fetch %q: %w", providerID, query.Dataset, err)
	}
	for _, record := range records {
		if err := record.Validate(); err != nil {
			return nil, err
		}
		if record.Dataset != query.Dataset {
			return nil, fmt.Errorf("researchdata: provider %q returned dataset %q for query %q", providerID, record.Dataset, query.Dataset)
		}
		if record.Source != providerID {
			return nil, fmt.Errorf("researchdata: provider %q returned record source %q", providerID, record.Source)
		}
		if record.AvailableAt.After(query.AsOf) {
			return nil, fmt.Errorf("researchdata: provider %q returned future revision %q", providerID, record.Key)
		}
		if record.EventAt.After(query.AsOf) {
			return nil, fmt.Errorf("researchdata: provider %q returned future event %q", providerID, record.Key)
		}
		if !query.Start.IsZero() && record.EventAt.Before(query.Start) {
			return nil, fmt.Errorf("researchdata: provider %q returned record %q before query start", providerID, record.Key)
		}
		if !query.End.IsZero() && record.EventAt.After(query.End) {
			return nil, fmt.Errorf("researchdata: provider %q returned record %q after query end", providerID, record.Key)
		}
		if len(query.Codes) > 0 && !contains(query.Codes, record.Code) {
			return nil, fmt.Errorf("researchdata: provider %q returned unrequested code %q", providerID, record.Code)
		}
	}
	return records, nil
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

// Load fetches one dataset and atomically ingests the validated batch.
func (r *Registry) Load(ctx context.Context, store *Store, query Query) error {
	if store == nil {
		return fmt.Errorf("researchdata: store is nil")
	}
	records, err := r.Fetch(ctx, query)
	if err != nil {
		return err
	}
	return store.Ingest(records)
}

// Hub combines provider routing and the bounded in-memory View for callers that
// do not need a custom persistence layer.
type Hub struct {
	registry *Registry
	store    *Store
}

func NewHub() *Hub {
	return &Hub{registry: NewRegistry(), store: NewStore()}
}

func (h *Hub) Register(provider Provider) error {
	if err := h.registry.Register(provider); err != nil {
		return err
	}
	for _, spec := range provider.Catalog() {
		if err := h.store.RegisterDataset(spec); err != nil {
			return err
		}
	}
	return nil
}

func (h *Hub) Load(ctx context.Context, query Query) error {
	return h.registry.Load(ctx, h.store, query)
}

func (h *Hub) Catalog() []DatasetSpec {
	return h.registry.Catalog()
}

func (h *Hub) Latest(dataset, code string, asOf time.Time) (Record, bool) {
	return h.store.Latest(dataset, code, asOf)
}

func (h *Hub) Range(dataset, code string, start, end, asOf time.Time) []Record {
	return h.store.Range(dataset, code, start, end, asOf)
}
