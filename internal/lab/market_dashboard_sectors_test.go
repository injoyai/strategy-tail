package lab

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/injoyai/strategy-tail/internal/marketstore"
	tdx "github.com/injoyai/tdx/v2"
	"github.com/injoyai/tdx/v2/protocol"
)

type dashboardSectorFixture struct {
	dashboardQuoteFixture
	catalog []*protocol.TdxZs
}

func (f *dashboardSectorFixture) GetTdxZs() ([]*protocol.TdxZs, error) {
	return f.catalog, nil
}

func TestDashboardSectorsUseNamedIndustryAndConceptQuotes(t *testing.T) {
	feed := &dashboardSectorFixture{
		dashboardQuoteFixture: dashboardQuoteFixture{
			minute: "2026-11-13 15:00",
			quotes: map[string]tdx.Quote{
				"sh880301": dashboardFixtureQuote(t, "sh880301", 13, 10, 100, 20),
				"sh880302": dashboardFixtureQuote(t, "sh880302", 13, 10, 60, 20),
				"sh880305": dashboardFixtureQuote(t, "sh880305", 11, 10, 50, 20),
				"sh880310": dashboardFixtureQuote(t, "sh880310", 9, 10, 50, 20),
				"sh880505": dashboardFixtureQuote(t, "sh880505", 12, 10, 40, 20),
				"sh880506": dashboardFixtureQuote(t, "sh880506", 14, 0, 40, 20),
			},
		},
		catalog: []*protocol.TdxZs{
			{Code: "880302", Name: "煤炭开采", Type: 2, SubType: 1},
			{Code: "880301", Name: "煤炭", Type: 2, SubType: 1},
			{Code: "880305", Name: "电力", Type: 2, SubType: 1},
			{Code: "880310", Name: "石油", Type: 2, SubType: 1},
			{Code: "880505", Name: "稀缺资源", Type: 4, SubType: 2},
			{Code: "880506", Name: "5G概念", Type: 4, SubType: 2},
			{Code: "880201", Name: "黑龙江", Type: 3, SubType: 1},
			{Code: "880501", Name: "含H股", Type: 4, SubType: 1},
			{Code: "000001", Name: "上证指数", Type: 2, SubType: 1},
			{Code: "880505", Name: "稀缺资源重复记录", Type: 4, SubType: 2},
		},
	}

	var service dashboardSectorService
	view, err := service.read(context.Background(), feed, dashboardTestTime(t, "2026-11-15 10:00:00"))
	if err != nil {
		t.Fatal(err)
	}
	if view.Date != "2026-11-13" || view.Expected != 6 || view.Received != 5 {
		t.Fatalf("unexpected sector coverage or date: %+v", view)
	}
	if len(view.Industries) != 4 || view.Industries[0].Name != "煤炭" || view.Industries[1].Code != "sh880302" ||
		view.Industries[3].Name != "石油" || view.Industries[3].Change >= 0 ||
		len(view.Concepts) != 1 || view.Concepts[0].Name != "稀缺资源" || view.Concepts[0].Change != 20 {
		t.Fatalf("unexpected sector rankings: %+v", view)
	}

	if _, err := service.read(context.Background(), feed, dashboardTestTime(t, "2026-11-16 09:45:00")); !errors.Is(err, errDashboardNoRecentMinute) {
		t.Fatalf("previous-session data must not be treated as fresh intraday quotes: %v", err)
	}
}

func TestSectorFiveDayRanksIncludeTodayAndSkipMissing(t *testing.T) {
	dates := []string{"1", "2", "3", "4", "5"}
	catalog := map[string]dashboardSector{"a": {industry: true}, "b": {industry: true}, "c": {industry: true}, "x": {industry: false}}
	var rows []marketstore.SectorDayClose
	for _, date := range dates {
		for _, v := range []struct {
			code  string
			close float64
		}{{"a", 100}, {"b", 100}, {"c", 100}, {"x", 100}} {
			if date == "3" && v.code == "c" {
				continue
			}
			rows = append(rows, marketstore.SectorDayClose{Date: date, Code: v.code, Close: v.close})
		}
	}
	view := dashboardSectorSnapshot{Industries: []dashboardSectorQuote{{Code: "b"}, {Code: "a"}, {Code: "c"}}, Concepts: []dashboardSectorQuote{{Code: "x"}}}
	applySectorFiveDay(&view, catalog, dates, rows)
	if *view.Industries[0].FiveDaySum != 9 || *view.Industries[0].FiveDayRank != 2 || *view.Industries[1].FiveDaySum != 6 || *view.Industries[1].FiveDayRank != 1 {
		t.Fatalf("unexpected rankings: %+v", view.Industries)
	}
	if view.Industries[2].FiveDayRank != nil {
		t.Fatal("missing day must not produce a rank")
	}
	if *view.Concepts[0].FiveDayRank != 1 || *view.Concepts[0].FiveDaySum != 5 {
		t.Fatalf("categories must be independent: %+v", view.Concepts)
	}
	if view.Industries[0].Code != "b" {
		t.Fatal("today's sort order changed")
	}
}

func TestSectorHistoryRefreshesAfterSameDayBackfill(t *testing.T) {
	dates := []string{"1", "2", "3", "4", "5"}
	var rows []marketstore.SectorDayClose
	for _, d := range dates[:4] {
		rows = append(rows, marketstore.SectorDayClose{Date: d, Code: "a", Close: 100})
	}
	calls := 0
	read := func(_ context.Context, _ string) ([]string, []marketstore.SectorDayClose, error) {
		calls++
		return dates, append([]marketstore.SectorDayClose(nil), rows...), nil
	}
	at := dashboardTestTime(t, "2026-11-17 16:00:00")
	var service dashboardSectorService
	catalog := map[string]dashboardSector{"a": {industry: true}}
	check := func(now time.Time) *int {
		t.Helper()
		if err := service.refreshSectorHistory(context.Background(), "2026-11-17", now, read); err != nil {
			t.Fatal(err)
		}
		view := dashboardSectorSnapshot{Industries: []dashboardSectorQuote{{Code: "a"}}}
		applySectorFiveDay(&view, catalog, service.historyDates, service.historyRows)
		return view.Industries[0].FiveDayRank
	}
	if check(at) != nil {
		t.Fatal("missing day must not produce a rank")
	}
	rows = append(rows, marketstore.SectorDayClose{Date: "5", Code: "a", Close: 100})
	if check(at.Add(4*time.Minute)) != nil || calls != 1 {
		t.Fatal("cache should remain valid before expiry")
	}
	rank := check(at.Add(5 * time.Minute))
	if rank == nil || *rank != 1 || calls != 2 {
		t.Fatal("backfilled data must become visible after expiry")
	}
}
