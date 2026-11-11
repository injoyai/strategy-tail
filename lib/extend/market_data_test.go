package extend

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/injoyai/tdx/v2"
	"github.com/injoyai/tdx/v2/extend/pull"
	"github.com/injoyai/tdx/v2/extend/storage/sqlite"
	"github.com/injoyai/tdx/v2/protocol"
)

type calendarFixture struct {
	tdx.Client
	calls int
}

func (c *calendarFixture) GetKlineDayAll(code string) (*tdx.KlineResp, error) {
	c.calls++
	return &tdx.KlineResp{Count: 2, List: []tdx.Kline{{Time: "2026-11-10"}, {Time: "2026-11-12"}}}, nil
}

func (c *calendarFixture) DoContext(_ context.Context, f func(tdx.Client) error) error {
	return f(c)
}

func TestMarketCalendarDoesNotCertifyUnobservedWeekdays(t *testing.T) {
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "metadata.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	cache, err := sqlite.NewMetadata(db)
	if err != nil {
		t.Fatal(err)
	}
	client := &calendarFixture{}
	service, err := pull.New(&pull.Config{Pool: client})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close() })
	m := &MarketData{service: service, cache: cache}
	for _, tc := range []struct {
		day            int
		trading, known bool
	}{
		{12, true, true}, {11, false, true}, {13, false, false}, {14, false, true},
	} {
		at := time.Date(2026, 11, tc.day, 16, 0, 0, 0, protocol.Location())
		trading, known, err := m.CheckTradingDay(at)
		if err != nil || trading != tc.trading || known != tc.known {
			t.Fatalf("%v: trading=%v known=%v err=%v", at, trading, known, err)
		}
	}
	if client.calls != 1 {
		t.Fatalf("calendar refetched %d times", client.calls)
	}
}
