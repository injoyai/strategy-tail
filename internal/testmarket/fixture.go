// Package testmarket provides fixture storage for tests without a MySQL server.
// Application binaries do not import this package.
package testmarket

import (
	"path/filepath"
	"sync"
	"testing"

	"github.com/injoyai/strategy-tail/internal/marketstore"
	"github.com/injoyai/tdx/v2/extend/model"
	"github.com/injoyai/tdx/v2/extend/storage/sqlite"
	"github.com/injoyai/tdx/v2/protocol"
)

type Store struct {
	*sqlite.PullStore
	mu     sync.RWMutex
	extras map[model.Code]map[string]marketstore.DayRecord
}

func Open(t testing.TB, dir string) *Store {
	t.Helper()
	s, err := sqlite.NewPull(filepath.Join(dir, "fixture-bars"), 4)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return &Store{PullStore: s, extras: make(map[model.Code]map[string]marketstore.DayRecord)}
}

func (s *Store) SeedDay(code model.Code, rows []*marketstore.DayRecord) error {
	raw := make([]*model.KlineDay, 0, len(rows))
	extras := make(map[string]marketstore.DayRecord, len(rows))
	for _, row := range rows {
		raw = append(raw, &row.KlineDay)
		extras[row.Time] = *row
	}
	if err := s.SaveDay(code, "", raw); err != nil {
		return err
	}
	code.Name = ""
	s.mu.Lock()
	s.extras[code] = extras
	s.mu.Unlock()
	return nil
}

func (s *Store) QueryDayRecords(code model.Code, start, end string) ([]*marketstore.DayRecord, error) {
	bars, err := s.QueryDay(code, start, end)
	if err != nil {
		return nil, err
	}
	code.Name = ""
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*marketstore.DayRecord, 0, len(bars))
	for _, bar := range bars {
		record := s.extras[code][bar.Time]
		record.KlineDay = *bar
		out = append(out, &record)
	}
	return out, nil
}

func (s *Store) DayCodes(market protocol.Market) ([]model.Code, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []model.Code
	for code, rows := range s.extras {
		if code.Market == market && len(rows) != 0 {
			out = append(out, code)
		}
	}
	return out, nil
}
