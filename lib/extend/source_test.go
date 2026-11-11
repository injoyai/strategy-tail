package extend

import (
	"testing"
	"time"

	"github.com/injoyai/strategy-tail/internal/testmarket"
	"github.com/injoyai/tdx/v2/extend/metadata"
	"github.com/injoyai/tdx/v2/extend/model"
	"github.com/injoyai/tdx/v2/protocol"
)

func TestReadDayUsesStoredSourceDatesAndHistoricalEquity(t *testing.T) {
	store := testmarket.New()
	code := model.Code{Market: protocol.MarketAStock, Code: "sh600000"}
	store.SeedDay(code, []*model.KlineDay{
		{Time: "2026-01-02", Close: 10, Volume: 100},
		{Time: "2026-01-05", Close: 11, Volume: 200},
	})
	equity, err := metadata.NewGbbq([]*model.Gbbq{
		{Code: code.Code, Category: 5, Time: time.Date(2026, 1, 5, 15, 0, 0, 0, protocol.Location()), C3: 1000, C4: 2000},
		{Code: code.Code, Category: 5, Time: time.Date(2026, 1, 6, 15, 0, 0, 0, protocol.Location()), C3: 9999, C4: 9999},
	})
	if err != nil {
		t.Fatal(err)
	}
	reader := NewMarketReader(store, equity.GetEquity)
	for _, zone := range []*time.Location{time.UTC, time.FixedZone("west", -5*3600), protocol.Location()} {
		date := time.Date(2026, 1, 5, 0, 0, 0, 0, zone)
		got, err := reader.DayKlines(code.Code, date, date)
		if err != nil || len(got) != 1 || got[0].Last != 10 || got[0].Volume != 2 ||
			got[0].Time.Format(time.DateTime) != "2026-01-05 15:00:00" || got[0].FloatStock != 1000 || got[0].Turnover != 20 {
			t.Fatal(zone, got, err)
		}
	}
}
