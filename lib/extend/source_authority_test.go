package extend

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/injoyai/tdx/v2/extend/model"
	"github.com/injoyai/tdx/v2/extend/pull"
	"github.com/injoyai/tdx/v2/extend/storage/sqlite"
)

func TestInjectedStoreIsSoleReadAndUpdateTarget(t *testing.T) {
	store, err := sqlite.NewPull(t.TempDir(), 4)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	root := t.TempDir()
	for _, name := range []string{"update.db", "day-kline/sh000001.db", "min-kline/sh000001/sh000001-2026.db"} {
		file := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(file), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte("old database must not be opened"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	p, err := NewPullKline(PullKlineConfig{Store: store, Dir: root, Codes: []string{"sh000001"}, Types: []string{Day}})
	if err != nil {
		t.Fatal(err)
	}
	client := &sourceClient{}
	if err := p.UpdateWithProgress(&MarketData{ClientPool: sourcePool{client: client}}, true, nil); err != nil {
		t.Fatal(err)
	}
	if client.calls != 1 {
		t.Fatal("update did not use SDK client")
	}
	if unix, has, err := store.LoadUpdate("pull"); err != nil || !has || unix <= 0 {
		t.Fatalf("store completion marker missing: %d %v %v", unix, has, err)
	}
	if at, err := p.LastSuccessfulUpdate(); err != nil || at.IsZero() {
		t.Fatalf("store update time missing: %v %v", at, err)
	}
	rows, err := p.DayKlinesAll("sh000001")
	if err != nil || len(rows) != 1 || rows[0].Close != 12.345678 {
		t.Fatalf("read used legacy database: %v %v", rows, err)
	}
	if rows, err := p.DayKlinesAll("sh600000"); err != nil || len(rows) != 0 {
		t.Fatal("empty store read fell back to local data", err)
	}
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.Local)
	if rows, err := p.MinKlines("sh000001", start, start); err != nil || len(rows) != 0 {
		t.Fatal("minute read opened corrupt legacy data", err)
	}
	code, err := pull.ParseCode("sh000001")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveMinute(code, 2026, "", []*model.KlineMinute{{Time: "2026-01-02 09:31", Close: 10, Volume: 100}}, nil); err != nil {
		t.Fatal(err)
	}
	minutes, err := p.MinKlines("sh000001", start, start)
	if err != nil || len(minutes) != 1 || minutes[0].Volume != 100 {
		t.Fatal("source minute conversion failed", minutes, err)
	}
	if _, err := os.Stat(filepath.Join(root, "source")); !os.IsNotExist(err) {
		t.Fatal("injected store created local source storage")
	}
	if raw, err := os.ReadFile(filepath.Join(root, "update.db")); err != nil || string(raw) != "old database must not be opened" {
		t.Fatal("update modified local marker", err)
	}
}
