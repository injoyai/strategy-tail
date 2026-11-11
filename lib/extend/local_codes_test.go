package extend

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/injoyai/tdx/v2/extend/model"
	"github.com/injoyai/tdx/v2/extend/storage"
	"github.com/injoyai/tdx/v2/protocol"
)

func TestLocalStockCodesMissingOrEmpty(t *testing.T) {
	p := &PullKline{Config: PullKlineConfig{Dir: t.TempDir()}}
	if _, err := p.LocalStockCodes(); !errors.Is(err, ErrMarketStoreRequired) {
		t.Fatal(err)
	}
	p.Config.Store = catalogStore{codes: []model.Code{{Code: "sh000001"}}}
	if _, err := p.LocalStockCodes(); err == nil {
		t.Fatal("index-only store should not provide stock universe")
	}
}

type catalogStore struct {
	storage.BarStore
	codes []model.Code
	err   error
}

func (s catalogStore) Identity() string { return "test-market" }

func (s catalogStore) DayCodes(protocol.Market) ([]model.Code, error) { return s.codes, s.err }

func TestLocalStockCodesFromStore(t *testing.T) {
	store := catalogStore{codes: []model.Code{{Code: "sh600000"}, {Code: "sz000003"}, {Code: "sh000001"}, {Code: "sh600000"}}}
	p := &PullKline{Config: PullKlineConfig{Dir: t.TempDir(), Store: store}}
	got, err := p.LocalStockCodes()
	if err != nil || !slices.Equal(got, []string{"sh600000", "sz000003"}) {
		t.Fatalf("store codes=%v, %v", got, err)
	}
	dir := filepath.Join(p.Config.Dir, DirDay)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sz000001.db"), nil, 0644); err != nil {
		t.Fatal(err)
	}
	got, err = p.LocalStockCodes()
	if err != nil || !slices.Equal(got, []string{"sh600000", "sz000003"}) {
		t.Fatalf("explicit store was mixed with local codes: %v, %v", got, err)
	}
	cause := errors.New("catalog unavailable")
	p.Config.Store = catalogStore{err: cause}
	if _, err := p.LocalStockCodes(); !errors.Is(err, cause) {
		t.Fatalf("catalog error hidden: %v", err)
	}
}
