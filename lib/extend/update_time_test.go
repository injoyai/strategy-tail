package extend

import (
	"errors"
	"testing"
	"time"

	"github.com/injoyai/tdx/v2/extend/storage"
)

type markerStore struct {
	storage.BarStore
	timestamp int64
	found     bool
	err       error
}

func (s markerStore) LoadUpdate(key string) (int64, bool, error) { return s.timestamp, s.found, s.err }

func TestLastSuccessfulUpdate(t *testing.T) {
	p := &PullKline{Config: PullKlineConfig{Store: markerStore{}}}
	cause := errors.New("store unavailable")
	for _, s := range []markerStore{{}, {timestamp: -1, found: true}, {timestamp: time.Now().Unix(), found: true}, {err: cause}} {
		p.Config.Store = s
		at, err := p.LastSuccessfulUpdate()
		if !errors.Is(err, s.err) || (s.timestamp <= 0 && !at.IsZero()) || (s.timestamp > 0 && at.Unix() != s.timestamp) {
			t.Fatalf("marker=%+v at=%v err=%v", s, at, err)
		}
	}
}
