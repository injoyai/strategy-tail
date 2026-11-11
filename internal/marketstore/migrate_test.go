package marketstore

import (
	"context"
	"reflect"
	"testing"

	"github.com/injoyai/tdx/v2/extend/model"
	"github.com/injoyai/tdx/v2/extend/storage"
	"github.com/injoyai/tdx/v2/extend/storage/sqlite"
	"github.com/injoyai/tdx/v2/protocol"
)

func TestSQLiteStoreMigrateTo(t *testing.T) {
	src, err := sqlite.NewPull(t.TempDir(), 8)
	if err != nil {
		t.Fatalf("open source: %v", err)
	}
	t.Cleanup(func() { _ = src.Close() })
	dst, err := sqlite.NewPull(t.TempDir(), 8)
	if err != nil {
		t.Fatalf("open destination: %v", err)
	}
	t.Cleanup(func() { _ = dst.Close() })

	code := model.Code{Market: protocol.MarketAStock, Code: "sh600000"}
	days := []*model.KlineDay{
		{Time: "2024-01-02", Open: 10, High: 11, Low: 9.5, Close: 10.5, Volume: 1000, Amount: testAmount(10500)},
		{Time: "2024-01-03", Open: 10.5, High: 12, Low: 10, Close: 11.5, Volume: 1200, Amount: testAmount(13800)},
	}
	if err := src.SaveDay(code, days[0].Time, days); err != nil {
		t.Fatalf("save day: %v", err)
	}
	minutes2024 := []*model.KlineMinute{
		{Time: "2024-01-02 09:31", Open: 10, High: 10.2, Low: 9.9, Close: 10.1, Volume: 100, Amount: testAmount(1010)},
		{Time: "2024-01-02 09:32", Open: 10.1, High: 10.3, Low: 10, Close: 10.2, Volume: 120, Amount: testAmount(1224)},
	}
	if err := src.SaveMinute(code, 2024, minutes2024[0].Time, minutes2024, &model.MinuteCoverage{From: "2024-01-01 00:00"}); err != nil {
		t.Fatalf("save minute 2024: %v", err)
	}
	minutes2025 := []*model.KlineMinute{
		{Time: "2025-01-02 09:31", Open: 11, High: 11.2, Low: 10.8, Close: 11.1, Volume: 90, Amount: testAmount(999), Source: model.MinuteSourceTrade},
	}
	if err := src.SaveMinute(code, 2025, minutes2025[0].Time, minutes2025, nil); err != nil {
		t.Fatalf("save minute 2025: %v", err)
	}
	hk := model.Code{Market: protocol.MarketHK, Code: "00700"}
	if err := src.SaveDay(hk, "2024-01-02", []*model.KlineDay{{Time: "2024-01-02", Close: 300}}); err != nil {
		t.Fatalf("save hk day: %v", err)
	}

	archive, err := OpenSQLiteSource(src.Identity())
	if err != nil {
		t.Fatal(err)
	}
	stats, err := MigrateSQLite(context.Background(), archive, dst, MigrateOptions{Workers: 2})
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	want := MigrateStats{Codes: 2, DayRows: 3, MinuteRows: 3, Coverages: 1}
	if stats != want {
		t.Fatalf("stats = %+v, want %+v", stats, want)
	}
	if got := mustQueryDay(t, dst, code); !reflect.DeepEqual(got, days) {
		t.Fatalf("day rows = %+v, want %+v", got, days)
	}
	gotMinutes := append(append([]*model.KlineMinute{}, minutes2024...), minutes2025...)
	if got := mustQueryMinute(t, dst, code); !reflect.DeepEqual(got, gotMinutes) {
		t.Fatalf("minute rows = %+v, want %+v", got, gotMinutes)
	}
	coverage, err := dst.QueryMinuteCoverage(code, 2024)
	if err != nil {
		t.Fatalf("query coverage: %v", err)
	}
	if coverage == nil || coverage.From != "2024-01-01 00:00" || coverage.TradeFallback {
		t.Fatalf("coverage = %+v", coverage)
	}
	if got := mustQueryDay(t, dst, hk); len(got) != 1 || got[0].Time != "2024-01-02" {
		t.Fatalf("hk day rows = %+v", got)
	}

	// 重复执行保持幂等，不改变行数。
	stats, err = MigrateSQLite(context.Background(), archive, dst, MigrateOptions{Workers: 2})
	if err != nil {
		t.Fatalf("migrate again: %v", err)
	}
	if stats != want {
		t.Fatalf("second stats = %+v, want %+v", stats, want)
	}
	if got := mustQueryDay(t, dst, code); !reflect.DeepEqual(got, days) {
		t.Fatalf("day rows after rerun = %+v", got)
	}

	// 目标端已有更晚的日线时跳过该代码，不能回退目标端数据。
	newer := []*model.KlineDay{{Time: "2024-02-01", Close: 12}}
	if err := dst.SaveDay(code, newer[0].Time, newer); err != nil {
		t.Fatalf("save newer day: %v", err)
	}
	stats, err = MigrateSQLite(context.Background(), archive, dst, MigrateOptions{})
	if err != nil {
		t.Fatalf("migrate with newer target: %v", err)
	}
	if stats.DayRows != 1 || stats.Skipped != 1 {
		t.Fatalf("stats with newer target = %+v", stats)
	}
	got := mustQueryDay(t, dst, code)
	if len(got) != 3 || got[2].Time != newer[0].Time {
		t.Fatalf("day rows with newer target = %+v", got)
	}
}

func TestMigrateItemsLayout(t *testing.T) {
	dir := t.TempDir()
	store, err := sqlite.NewPull(dir, 8)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	items := []struct {
		code model.Code
		year int
	}{
		{model.Code{Market: protocol.MarketFuture, Code: "cff/IF2609"}, 0},
		{model.Code{Market: protocol.MarketFuture, Code: "cff/IF2609"}, 2024},
		{model.Code{Market: protocol.MarketIndex, Code: "sh000001"}, 2025},
		{model.Code{Market: protocol.MarketAStock, Code: "sh600000"}, 0},
		{model.Code{Market: protocol.MarketAStock, Code: "sh600000"}, 2024},
	}
	for _, item := range items {
		if item.year == 0 {
			if err := store.SaveDay(item.code, "2024-01-02", []*model.KlineDay{{Time: "2024-01-02", Close: 1}}); err != nil {
				t.Fatalf("save day %s: %v", item.code.Key(), err)
			}
			continue
		}
		time := yearToString(item.year) + "-01-02 09:31"
		if err := store.SaveMinute(item.code, item.year, time, []*model.KlineMinute{{Time: time, Close: 1}}, nil); err != nil {
			t.Fatalf("save minute %s %d: %v", item.code.Key(), item.year, err)
		}
	}

	source, err := OpenSQLiteSource(store.Identity())
	if err != nil {
		t.Fatal(err)
	}
	got, codes, err := migrateItems(source)
	if err != nil {
		t.Fatalf("migrate items: %v", err)
	}
	if codes != 3 {
		t.Fatalf("codes = %d, want 3", codes)
	}
	if len(got) != len(items) {
		t.Fatalf("items = %d, want %d: %+v", len(got), len(items), got)
	}
	for i, want := range items {
		if got[i].code != want.code || got[i].year != want.year {
			t.Fatalf("item %d = %+v, want %+v", i, got[i], want)
		}
	}
}

func mustQueryDay(t *testing.T, store storage.BarStore, code model.Code) []*model.KlineDay {
	t.Helper()
	rows, err := store.QueryDay(code, "", "")
	if err != nil {
		t.Fatalf("query day: %v", err)
	}
	return rows
}

func mustQueryMinute(t *testing.T, store storage.BarStore, code model.Code) []*model.KlineMinute {
	t.Helper()
	rows, err := store.QueryMinute(code, "", "")
	if err != nil {
		t.Fatalf("query minute: %v", err)
	}
	return rows
}

func TestSQLiteMigrateToMySQLPreservesExisting(t *testing.T) {
	dst := newMySQLStore(t)
	src, err := sqlite.NewPull(t.TempDir(), 4)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	code := uniqueMySQLCode()
	days := []*model.KlineDay{{Time: "2026-01-02", Close: 1}, {Time: "2026-01-05", Close: 2}}
	if err := src.SaveDay(code, "", days); err != nil {
		t.Fatal(err)
	}
	originalDay := []*model.KlineDay{{Time: "2026-01-05", Close: 99}, {Time: "2026-01-06", Close: 100}}
	if err := dst.SaveDay(code, "", originalDay); err != nil {
		t.Fatal(err)
	}
	minutes := []*model.KlineMinute{{Time: "2094-01-02 09:31", Close: 1, Source: model.MinuteSourceTrade}, {Time: "2094-01-02 09:32", Close: 2, Source: model.MinuteSourceTrade}, {Time: "2094-01-02 09:33", Close: 3}}
	mark := &model.MinuteCoverage{From: "2094-01-01 00:00"}
	if err := src.SaveMinute(code, 2094, "", minutes, mark); err != nil {
		t.Fatal(err)
	}
	originalMinute := []*model.KlineMinute{{Time: minutes[0].Time, Close: 99}, {Time: minutes[2].Time, Close: 98}, {Time: "2094-01-03 09:31", Close: 97}}
	if err := dst.SaveMinute(code, 2094, "", originalMinute, nil); err != nil {
		t.Fatal(err)
	}
	archive, err := OpenSQLiteSource(src.Identity())
	if err != nil {
		t.Fatal(err)
	}
	stats, err := MigrateSQLite(context.Background(), archive, dst, MigrateOptions{Workers: 2})
	if err != nil || stats.DayRows != 1 || stats.MinuteRows != 1 || stats.Coverages != 1 {
		t.Fatalf("migration=%+v %v", stats, err)
	}
	gotDay := mustQueryDay(t, dst, code)
	if want := append([]*model.KlineDay{days[0]}, originalDay...); !reflect.DeepEqual(gotDay, want) {
		t.Fatalf("day changed: %+v", gotDay)
	}
	gotMinute := mustQueryMinute(t, dst, code)
	wantMinute := []*model.KlineMinute{originalMinute[0], minutes[1], originalMinute[1], originalMinute[2]}
	if !reflect.DeepEqual(gotMinute, wantMinute) {
		t.Fatalf("minute changed: %+v", gotMinute)
	}
	stats, err = MigrateSQLite(context.Background(), archive, dst, MigrateOptions{Workers: 2})
	if err != nil || stats.DayRows != 0 || stats.MinuteRows != 0 || stats.Skipped != 2 || stats.Coverages != 0 {
		t.Fatalf("rerun=%+v %v", stats, err)
	}
}

func testAmount(v float64) *float64 { return &v }
