package core

import (
	"testing"
	"time"

	"github.com/injoyai/strategy-tail/lib/extend"
	"github.com/injoyai/tdx/v2/protocol"
)

type screenTestSeller func(extend.Klines, Buy) bool

func (s screenTestSeller) Name() string                                { return "test" }
func (s screenTestSeller) Sell(_ string, ks extend.Klines, b Buy) bool { return s(ks, b) }

func TestScreenGetSellKeepsDailyBarsAndUsesTradingDate(t *testing.T) {
	entry := time.Date(2026, 11, 12, 10, 0, 0, 0, protocol.Location())
	day := time.Date(2026, 11, 13, 15, 0, 0, 0, protocol.Location())
	prior := &protocol.Kline{Time: entry.Add(5 * time.Hour), Close: 10}
	daily := &protocol.Kline{Time: day, Last: 10, Open: 10, Close: 13}
	bars := extend.Klines{{Kline: prior}, {Kline: daily, FloatStock: 10000}}
	first := day.Add(-5 * time.Hour)
	minutes := map[string]protocol.Klines{day.Format(time.DateOnly): {
		{Time: first, Open: 10, High: 11, Low: 10, Close: 11, Volume: 1},
		{Time: first.Add(time.Minute), Open: 11, High: 12, Low: 9, Close: 12, Volume: 2},
	}}
	seller := screenTestSeller(func(ks extend.Klines, b Buy) bool {
		if len(ks) == 1 {
			t.Fatal("same-day sale was evaluated")
		}
		if ks[0].Kline != prior || b.Time != entry || ks[1].Last != 10 {
			t.Fatalf("unexpected history or prior close: %+v", ks[1].Kline)
		}
		if ks[1].Close == 12 && (ks[1].Volume != 3 || ks[1].Turnover != 3 || ks[1].Low != 9) {
			t.Fatalf("unexpected aggregated bar: %+v", ks[1])
		}
		return ks[1].Close == 12
	})
	got := GetSell(seller, bars, Buy{Code: "sh600000", Time: entry, Price: 10}, minutes)
	if got == nil || got.Time != first.Add(time.Minute) || got.Price != 12 || bars[1].Kline != daily || bars[1].Close != 13 {
		t.Fatalf("sale or input changed: %+v, %+v", got, bars[1])
	}
}
