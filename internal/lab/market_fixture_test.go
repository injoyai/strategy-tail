package lab

import (
	"sync"
	"testing"

	"github.com/injoyai/strategy-tail/internal/marketstore"
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
	s := testmarket.Open(t, dir)
	testMarkets.Store(dir, s)
	t.Cleanup(func() { testMarkets.Delete(dir) })
	return s
}

func fixturePull(t *testing.T, dir string) *extend.PullKline {
	t.Helper()
	return &extend.PullKline{Config: extend.PullKlineConfig{Dir: dir, Store: fixtureMarket(t, dir)}}
}

func seedTestDays(t *testing.T, dir, name string, bars extend.Klines) {
	t.Helper()
	code, err := pull.ParseCode(name)
	if err != nil {
		t.Fatal(err)
	}
	rows := make([]*marketstore.DayRecord, 0, len(bars))
	for _, bar := range bars {
		volume := bar.Volume
		if code.Market != protocol.MarketIndex && code.Market != protocol.MarketBlock {
			volume = protocol.ToShares(volume)
		}
		rows = append(rows, &marketstore.DayRecord{
			KlineDay: model.KlineDay{Time: bar.Time.Format("2006-01-02"), Open: bar.Open, High: bar.High, Low: bar.Low, Close: bar.Close, Volume: volume, Amount: &bar.Amount},
			Turnover: bar.Turnover, FloatStock: float64(bar.FloatStock), TotalStock: float64(bar.TotalStock),
		})
	}
	if err := fixtureMarket(t, dir).SeedDay(code, rows); err != nil {
		t.Fatal(err)
	}
}
