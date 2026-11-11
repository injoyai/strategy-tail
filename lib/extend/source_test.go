package extend

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/injoyai/strategy-tail/internal/testmarket"
	"github.com/injoyai/tdx/v2"
	"github.com/injoyai/tdx/v2/extend/metadata"
	"github.com/injoyai/tdx/v2/extend/model"
	"github.com/injoyai/tdx/v2/extend/pull"
	"github.com/injoyai/tdx/v2/protocol"
)

func saveSourceMinutes(p *PullKline, code string, year int, bars protocol.Klines) error {
	store := p.Config.Store
	s, err := pull.New(&pull.Config{Store: store, Codes: []string{code}, Minute: true})
	if err != nil {
		return err
	}
	defer s.Close()
	c, err := pull.ParseCode(code)
	if err != nil {
		return err
	}
	rows := make([]*model.KlineMinute, 0, len(bars))
	from := ""
	for _, b := range bars {
		if b.Time.Second() != 0 || b.Time.Nanosecond() != 0 {
			return fmt.Errorf("源分钟时间不能丢失秒或小数秒")
		}
		value := b.Time.Format(protocol.TimeLayout)
		if from == "" || value < from {
			from = value
		}
		rows = append(rows, &model.KlineMinute{Time: value, Open: b.Open, High: b.High, Low: b.Low, Close: b.Close, Volume: b.Volume * 100, Amount: &b.Amount})
	}
	return s.SaveMinute(c, year, from, rows)
}

func TestSourceStoragePreservesMarketClockWithoutTimezone(t *testing.T) {
	p := fixturePull(t, PullKlineConfig{})
	store := p.Config.Store
	s, err := pull.New(&pull.Config{Store: store, Codes: []string{"sh600000", "US.AAPL", "HK00700"}, Minute: true})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, name := range []string{"sh600000", "US.AAPL", "HK00700"} {
		c, e := pull.ParseCode(name)
		if e != nil {
			t.Fatal(e)
		}
		rows := []*model.KlineMinute{{Time: "2026-11-03 09:30", Close: 12.345678, Volume: 100}}
		if e = s.SaveMinute(c, 2026, rows[0].Time, rows); e != nil {
			t.Fatal(e)
		}
		got, e := s.QueryMinute(c, "", "")
		if e != nil || len(got) != 1 || *got[0] != *rows[0] {
			t.Fatal(name, got, e)
		}
	}
	// Raw extended data stays available; the existing A-share adapter must refuse to guess its zone.
	if _, err = p.DayKlinesAll("US.AAPL"); err == nil {
		t.Fatal("extended source was interpreted by standard backtest adapter")
	}
}

func TestSourceDailyAdapterUsesDatesAndPreviousClose(t *testing.T) {
	p := fixturePull(t, PullKlineConfig{})
	equity, err := metadata.NewGbbq([]*model.Gbbq{
		{Code: "sh600000", Category: 5, Time: time.Date(2026, 1, 5, 15, 0, 0, 0, protocol.Location()), C3: 1000, C4: 2000},
		{Code: "sh600000", Category: 5, Time: time.Date(2026, 1, 6, 15, 0, 0, 0, protocol.Location()), C3: 9999, C4: 9999},
	})
	if err != nil {
		t.Fatal(err)
	}
	p.Config.Equity = func() *metadata.Gbbq { return equity }
	store := p.Config.Store
	c, e := pull.ParseCode("sh600000")
	if e != nil {
		t.Fatal(e)
	}
	if e = store.SaveDay(c, "2026-01-02", []*model.KlineDay{{Time: "2026-01-02", Close: 10, Volume: 100}, {Time: "2026-01-05", Close: 11, Volume: 200}}); e != nil {
		t.Fatal(e)
	}
	for _, zone := range []*time.Location{time.UTC, time.FixedZone("west", -5*3600), protocol.Location()} {
		got, e := p.DayKlines("sh600000", time.Date(2026, 1, 5, 0, 0, 0, 0, zone), time.Date(2026, 1, 5, 0, 0, 0, 0, zone))
		if e != nil || len(got) != 1 || got[0].Last != 10 || got[0].Volume != 2 || got[0].Time.Format(time.DateTime) != "2026-01-05 15:00:00" || got[0].FloatStock != 1000 {
			t.Fatal(zone, got, e)
		}
	}
}

func TestMissingStoreDoesNotOpenArchivedSQLite(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "update.db"), []byte("archived"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewPullKline(PullKlineConfig{Dir: dir}); !errors.Is(err, ErrMarketStoreRequired) {
		t.Fatalf("expected missing MySQL configuration: %v", err)
	}
	p := &PullKline{Config: PullKlineConfig{Dir: dir}}
	if _, err := p.DayKlinesAll("sh600000"); !errors.Is(err, ErrMarketStoreRequired) {
		t.Fatal(err)
	}
	if _, err := p.LastSuccessfulUpdate(); !errors.Is(err, ErrMarketStoreRequired) {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "update.db"))
	if err != nil || string(raw) != "archived" {
		t.Fatal("archived marker changed", err)
	}
}

type sourceClient struct {
	tdx.Client
	calls int
}

func (c *sourceClient) GetKlineUntil(category uint8, code string, stop func(tdx.Kline) bool) (*tdx.KlineResp, error) {
	c.calls++
	return &tdx.KlineResp{List: []tdx.Kline{{Time: time.Now().In(protocol.Location()).Format(time.DateOnly), Open: 10, Close: 12.345678, Volume: 100}}}, nil
}

type sourcePool struct {
	tdx.ClientPool
	client *sourceClient
}

func (p sourcePool) DoContext(_ context.Context, f func(tdx.Client) error) error { return f(p.client) }

func TestSourceUpdateUsesSDKAndIsolatesOldCompletionMarker(t *testing.T) {
	p := fixturePull(t, PullKlineConfig{Codes: []string{"sh000001"}, Types: []string{Day}})
	var e error
	if e = p.Updated.Mark("pull"); e != nil {
		t.Fatal(e)
	} // Legacy completion cannot suppress the new dataset.
	client := &sourceClient{}
	m := &MarketData{ClientPool: sourcePool{client: client}}
	if e = p.UpdateWithProgress(m, true, nil); e != nil {
		t.Fatal(e)
	}
	if client.calls != 1 {
		t.Fatal(client.calls)
	}
	store := p.Config.Store
	code, e := pull.ParseCode("sh000001")
	if e != nil {
		t.Fatal(e)
	}
	rows, e := store.QueryDay(code, "", "")
	if e != nil || len(rows) != 1 || rows[0].Time != time.Now().In(protocol.Location()).Format(time.DateOnly) || rows[0].Close != 12.345678 {
		t.Fatal(rows, e)
	}
	if e = p.UpdateWithProgress(m, true, nil); e != nil || client.calls != 1 {
		t.Fatal("fresh dataset downloaded again", client.calls, e)
	}
	db, e := sql.Open("sqlite", store.(*testmarket.Store).DayFile(code))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	columns, e := db.Query("PRAGMA table_info(KlineDay)")
	if e != nil {
		t.Fatal(e)
	}
	defer columns.Close()
	count := 0
	for columns.Next() {
		var id, nn, pk int
		var name, kind string
		var def sql.NullString
		if e = columns.Scan(&id, &name, &kind, &nn, &def, &pk); e != nil {
			t.Fatal(e)
		}
		if name == "Unix" || name == "TimeZone" || name == "TradingDate" {
			t.Fatal("unexpected source metadata", name)
		}
		count++
	}
	if e = columns.Err(); e != nil || count != 7 {
		t.Fatal(count, e)
	}
}

func TestSourceSDKRejectsTimezoneAndInvalidDate(t *testing.T) {
	p := fixturePull(t, PullKlineConfig{})
	store := p.Config.Store
	s, e := pull.New(&pull.Config{Store: store, Codes: []string{"US.AAPL"}, Minute: true})
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	code, e := pull.ParseCode("US.AAPL")
	if e != nil {
		t.Fatal(e)
	}
	for _, value := range []string{"2026-11-03T09:30:00+08:00", "2026-11-03 09:30+08:00", "2026-02-30 09:30"} {
		if e = s.SaveMinute(code, 2026, "", []*model.KlineMinute{{Time: value, Close: 10}}); e == nil {
			t.Fatal("invalid source accepted", value)
		}
	}
	rows, e := s.QueryMinute(code, "", "")
	if e != nil || len(rows) != 0 {
		t.Fatal(rows, e)
	}
}

func fixturePull(t *testing.T, cfg PullKlineConfig) *PullKline {
	t.Helper()
	if cfg.Dir == "" {
		cfg.Dir = t.TempDir()
	}
	cfg.Store = testmarket.Open(t, cfg.Dir)
	p, err := NewPullKline(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return p
}
