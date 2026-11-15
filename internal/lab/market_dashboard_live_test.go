package lab

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	tdx "github.com/injoyai/tdx/v2"
	"github.com/injoyai/tdx/v2/protocol"
)

func dashboardTestTime(t *testing.T, value string) time.Time {
	t.Helper()
	at, err := time.ParseInLocation(time.DateTime, value, protocol.Location())
	if err != nil {
		t.Fatal(err)
	}
	return at
}

func TestMarketDashboardSchedule(t *testing.T) {
	for _, test := range []struct{ at, phase, next string }{
		{"2026-11-13 09:29:59", "closed", "2026-11-13 09:30:00"},
		{"2026-11-13 09:30:00", "trading", "2026-11-13 11:30:00"},
		{"2026-11-13 11:30:00", "break", "2026-11-13 13:00:00"},
		{"2026-11-13 13:00:00", "trading", "2026-11-13 15:00:00"},
		{"2026-11-13 15:00:00", "closed", "2026-11-16 09:30:00"},
		{"2026-11-15 09:30:00", "closed", "2026-11-16 09:30:00"},
	} {
		at := dashboardTestTime(t, test.at).UTC()
		phase, next := dashboardSchedule(at)
		if phase != test.phase || next.Format(time.DateTime) != test.next {
			t.Errorf("%s: got %s %s", test.at, phase, next)
		}
	}
}

type dashboardQuoteFixture struct {
	minute string
	quotes map[string]tdx.Quote
	failed string
	calls  int
}

func (f *dashboardQuoteFixture) GetKlineMinute(string, uint16, uint16) (*tdx.KlineResp, error) {
	return &tdx.KlineResp{List: []tdx.Kline{{Time: f.minute, Close: 3000}}}, nil
}

func (f *dashboardQuoteFixture) GetQuote(codes ...string) ([]tdx.Quote, error) {
	f.calls++
	if slices.Contains(codes, f.failed) {
		return nil, errors.New("quote unavailable")
	}
	var out []tdx.Quote
	for _, code := range codes {
		if q, ok := f.quotes[code]; ok {
			out = append(out, q)
		}
	}
	return out, nil
}

func dashboardFixtureQuote(t *testing.T, code string, price, previous, amount float64, volume int64) tdx.Quote {
	t.Helper()
	instrument, err := protocol.ParseInstrument(code)
	if err != nil {
		t.Fatal(err)
	}
	return tdx.Quote{Instrument: instrument, Price: price, PreClose: previous, Volume: volume,
		Standard: &protocol.Quote{Kline: &protocol.Kline{Amount: amount}}}
}

func TestMarketDashboardQuotesUseCurrentSessionAndCountCoverage(t *testing.T) {
	at := dashboardTestTime(t, "2026-11-13 13:01:40")
	feed := &dashboardQuoteFixture{
		minute: "2026-11-13 13:02",
		failed: "sh600002",
		quotes: map[string]tdx.Quote{
			"sh000001": dashboardFixtureQuote(t, "sh000001", 3300, 3000, 900, 100),
			"bj899050": dashboardFixtureQuote(t, "bj899050", 900, 1000, 100, 20),
			"sz000001": dashboardFixtureQuote(t, "sz000001", 12, 10, 300, 100),
			"sz000002": dashboardFixtureQuote(t, "sz000002", 10, 10, 0, 0),
			"bj920001": dashboardFixtureQuote(t, "bj920001", 8, 10, 200, 50),
		},
	}
	stocks := []string{"sh600001", "sh600002", "sz000001", "sz000002", "bj920001"}
	view, err := collectDashboardQuotes(context.Background(), feed, stocks, at)
	if err != nil {
		t.Fatal(err)
	}
	if view.Date != "2026-11-13" || view.Source != "tdx" || view.StockQuotes != 3 || view.StockUniverse != 5 || view.Stocks.Total != 2 {
		t.Fatalf("unexpected live snapshot: %+v", view)
	}
	if ranks := view.Stocks.Rankings.Amount; len(ranks) != 2 || ranks[0].Code != "sz000001" || *ranks[0].Change != 20 || view.Stocks.Amount != 500 {
		t.Fatalf("unexpected ranking: %+v", view.Stocks)
	}
	if view.Indexes[0].Date != view.Date || *view.Indexes[0].Change != 10 || view.Indexes[7].Close != 900 || view.Indexes[1].Date != "" {
		t.Fatalf("unexpected indices: %+v", view.Indexes)
	}

	for _, minute := range []string{"2026-11-12 15:00", "2026-11-13 11:30", "2026-11-13 13:05"} {
		feed.minute, feed.calls = minute, 0
		if _, err := collectDashboardQuotes(context.Background(), feed, stocks, at); !errors.Is(err, errDashboardNoRecentMinute) || feed.calls != 0 {
			t.Errorf("%s: should decline quote sweep without a current index minute, got %v (%d quotes)", minute, err, feed.calls)
		}
	}
}

func TestMarketDashboardKeepsLiveSnapshotUntilDailyIsReady(t *testing.T) {
	at := dashboardTestTime(t, "2026-11-13 10:00:00")
	dailyDate := "2026-11-12"
	daily := func(context.Context, string) (marketDashboard, error) {
		return marketDashboard{Date: dailyDate, Source: "daily"}, nil
	}
	calls := 0
	live := func(context.Context, time.Time) (marketDashboard, error) {
		calls++
		if calls > 1 {
			return marketDashboard{}, errDashboardNoRecentMinute
		}
		return marketDashboard{Date: "2026-11-13", Source: "tdx", CapturedAt: "2026-11-13T10:00:00+08:00"}, nil
	}
	var cache dashboardLiveCache
	check := func(at time.Time, source, phase string, warning bool) marketDashboard {
		t.Helper()
		view, err := cache.viewAt(context.Background(), at, live, daily)
		if err != nil || view.Source != source || view.Phase != phase || (view.Notice != "") != warning || view.RefreshAfter <= 0 {
			t.Fatalf("%s: got %+v, %v", at, view, err)
		}
		return view
	}
	check(at, "tdx", "trading", false)
	check(at.Add(5*time.Second), "tdx", "trading", false)
	if calls != 1 {
		t.Fatalf("same refresh interval made %d upstream calls", calls)
	}
	stale := check(at.Add(dashboardRefresh), "tdx", "trading", true)
	if stale.CapturedAt != "2026-11-13T10:00:00+08:00" {
		t.Fatal("failed refresh changed the capture time")
	}
	check(dashboardTestTime(t, "2026-11-13 11:36:00"), "tdx", "break", false)
	check(dashboardTestTime(t, "2026-11-13 15:00:00"), "tdx", "closed", true)
	if calls != 2 {
		t.Fatalf("closed market made upstream calls: %d", calls)
	}
	dailyDate = "2026-11-13"
	check(dashboardTestTime(t, "2026-11-13 16:00:00"), "daily", "closed", false)
	check(dashboardTestTime(t, "2026-11-16 09:30:00"), "daily", "trading", true)
}

func TestMarketDashboardCanceledViewerDoesNotCancelSharedRefresh(t *testing.T) {
	at := dashboardTestTime(t, "2026-11-13 10:00:00")
	view := marketDashboard{Date: "2026-11-13", Source: "tdx"}
	var cache dashboardLiveCache
	started := make(chan struct{})
	release := make(chan struct{})
	read := func(ctx context.Context, _ time.Time) (marketDashboard, error) {
		close(started)
		select {
		case <-release:
			return view, nil
		case <-ctx.Done():
			return marketDashboard{}, ctx.Err()
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	first := make(chan error, 1)
	go func() {
		_, err := cache.load(ctx, at, read)
		first <- err
	}()
	<-started
	cancel()
	if err := <-first; !errors.Is(err, context.Canceled) {
		t.Fatalf("first viewer did not cancel: %v", err)
	}
	close(release)
	second, stop := context.WithTimeout(context.Background(), time.Second)
	defer stop()
	got, err := cache.load(second, at, read)
	if err != nil || got.Date != view.Date {
		t.Fatalf("shared refresh did not finish for second viewer: %+v %v", got, err)
	}
}
