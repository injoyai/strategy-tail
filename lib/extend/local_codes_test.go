package extend

import (
	"errors"
	"slices"
	"testing"

	"github.com/injoyai/tdx/v2/extend/model"
	"github.com/injoyai/tdx/v2/protocol"
)

type catalogStore struct {
	MarketSource
	codes []model.Code
	err   error
}

func (s catalogStore) Identity() string { return "test-market" }

func (s catalogStore) DayCodes(protocol.Market) ([]model.Code, error) { return s.codes, s.err }

func TestLocalStockCodesFromStore(t *testing.T) {
	reader := NewMarketReader(catalogStore{codes: []model.Code{{Code: "sh600000"}, {Code: "sz000003"}, {Code: "sh000001"}, {Code: "sh600000"}}}, nil)
	got, err := reader.LocalStockCodes()
	if err != nil || !slices.Equal(got, []string{"sh600000", "sz000003"}) {
		t.Fatalf("store codes=%v, %v", got, err)
	}
	cause := errors.New("catalog unavailable")
	if _, err := NewMarketReader(catalogStore{err: cause}, nil).LocalStockCodes(); !errors.Is(err, cause) {
		t.Fatalf("catalog error hidden: %v", err)
	}
	if _, err := NewMarketReader(catalogStore{codes: []model.Code{{Code: "sh000001"}}}, nil).LocalStockCodes(); err == nil {
		t.Fatal("index-only store should not provide stock universe")
	}
}
