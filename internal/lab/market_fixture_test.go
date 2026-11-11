package lab

import (
	"sync"
	"testing"

	"github.com/injoyai/strategy-tail/internal/testmarket"
	"github.com/injoyai/strategy-tail/lib/extend"
	"github.com/injoyai/tdx/v2/extend/model"
	"github.com/injoyai/tdx/v2/extend/pull"
	"github.com/injoyai/tdx/v2/protocol"
)

var testMarkets sync.Map

func fixtureMarket(t *testing.T, dir string) *testmarket.Store {
	t.Helper()
	if existing, ok := testMarkets.Load(dir); ok {
		return existing.(*testmarket.Store)
	}
	s := testmarket.New()
	t.Cleanup(func() { _ = s.Close() })
	testMarkets.Store(dir, s)
	t.Cleanup(func() { testMarkets.Delete(dir) })
	return s
}

func fixturePull(t *testing.T, dir string) *extend.MarketReader {
	t.Helper()
	store := fixtureMarket(t, dir)
	return extend.NewMarketReader(store, store.Equity)
}

func seedTestDays(t *testing.T, dir, name string, bars extend.Klines) {
	t.Helper()
	code, err := pull.ParseCode(name)
	if err != nil {
		t.Fatal(err)
	}
	store := fixtureMarket(t, dir)
	rows := make([]*model.KlineDay, 0, len(bars))
	for _, bar := range bars {
		volume := bar.Volume
		if code.Market != protocol.MarketIndex && code.Market != protocol.MarketBlock {
			volume = protocol.ToShares(volume)
		}
		date := bar.Time.Format("2006-01-02")
		rows = append(rows, &model.KlineDay{Time: date, Open: bar.Open, High: bar.High, Low: bar.Low, Close: bar.Close, Volume: volume, Amount: &bar.Amount})
		if bar.FloatStock > 0 || bar.TotalStock > 0 {
			store.SeedEquity(code.Code, date, bar.FloatStock, bar.TotalStock)
		}
	}
	store.SeedDay(code, rows)
}
