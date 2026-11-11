// Package testmarket provides in-memory bar fixtures for application tests.
package testmarket

import (
	"errors"
	"slices"
	"sync"
	"time"

	"github.com/injoyai/tdx/v2/extend/model"
	"github.com/injoyai/tdx/v2/protocol"
)

type Store struct {
	mu     sync.RWMutex
	days   map[model.Code][]*model.KlineDay
	equity map[string]map[string]protocol.Equity
	closed bool
}

func New() *Store {
	return &Store{
		days:   make(map[model.Code][]*model.KlineDay),
		equity: make(map[string]map[string]protocol.Equity),
	}
}

func (s *Store) Identity() string { return "fixture" }

func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	return nil
}

func (s *Store) SeedDay(code model.Code, bars []*model.KlineDay) {
	code.Name = ""
	s.mu.Lock()
	defer s.mu.Unlock()
	rows := slices.Clone(bars)
	slices.SortFunc(rows, func(a, b *model.KlineDay) int {
		if a.Time < b.Time {
			return -1
		}
		if a.Time > b.Time {
			return 1
		}
		return 0
	})
	s.days[code] = rows
}

func (s *Store) SeedEquity(code, date string, floatStock, totalStock int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.equity[code] == nil {
		s.equity[code] = make(map[string]protocol.Equity)
	}
	s.equity[code][date] = protocol.Equity{Float: floatStock, Total: totalStock}
}

func (s *Store) Equity(code string, at time.Time) *protocol.Equity {
	s.mu.RLock()
	defer s.mu.RUnlock()
	record, ok := s.equity[code][at.Format(time.DateOnly)]
	if !ok {
		return nil
	}
	return &record
}

func (s *Store) QueryDay(code model.Code, start, end string) ([]*model.KlineDay, error) {
	code.Name = ""
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return nil, errors.New("fixture closed")
	}
	var out []*model.KlineDay
	for _, row := range s.days[code] {
		if (start == "" || row.Time >= start) && (end == "" || row.Time <= end) {
			copy := *row
			out = append(out, &copy)
		}
	}
	return out, nil
}

func (s *Store) QueryMinute(model.Code, string, string) ([]*model.KlineMinute, error) {
	return nil, nil
}

func (s *Store) DayCodes(market protocol.Market) ([]model.Code, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []model.Code
	for code, rows := range s.days {
		if code.Market == market && len(rows) != 0 {
			out = append(out, code)
		}
	}
	return out, nil
}

func (s *Store) LastDayTime(code model.Code) (string, error) {
	rows, err := s.QueryDay(code, "", "")
	if err != nil || len(rows) == 0 {
		return "", err
	}
	return rows[len(rows)-1].Time, nil
}
