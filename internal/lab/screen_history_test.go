package lab

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/internal/lab/builder"
	"github.com/injoyai/strategy-tail/internal/screener"
	"github.com/injoyai/strategy-tail/lib/extend"
	"github.com/injoyai/tdx/v2/protocol"
)

type screenHistoryFixture struct {
	at             time.Time
	daily          bool
	missingMinutes bool
}

func (f screenHistoryFixture) Codes() ([]string, error) { return []string{"sh600000"}, nil }
func (f screenHistoryFixture) Name(string) string       { return "测试股票" }
func (f screenHistoryFixture) LatestDayDate(string) (string, error) {
	if f.daily {
		return f.at.Format(time.DateOnly), nil
	}
	return f.at.AddDate(0, 0, -1).Format(time.DateOnly), nil
}
func (f screenHistoryFixture) DayKlines(_ string, _, end time.Time) (extend.Klines, error) {
	bars := extend.Klines{}
	for i := 2; i > 0; i-- {
		day := f.at.AddDate(0, 0, -i).Add(5 * time.Hour)
		bars = append(bars, &extend.Kline{Kline: &protocol.Kline{Time: day, Last: 10, Open: 10, High: 11, Low: 10, Close: 10}})
	}
	closeAt := core.DayOf(f.at).Add(15 * time.Hour)
	if f.daily && !end.Before(closeAt) {
		bars = append(bars, &extend.Kline{Kline: &protocol.Kline{Time: closeAt, Last: 10, Open: 10, High: 12, Low: 10, Close: 12}})
	}
	return bars, nil
}
func (f screenHistoryFixture) StockSessionBars([]string, time.Time) (map[string]*protocol.Kline, error) {
	return map[string]*protocol.Kline{"sh600000": {Time: f.at, Open: 10, High: 12, Low: 10, Close: 12}}, nil
}
func (f screenHistoryFixture) EquityAt(string, time.Time) *protocol.Equity { return nil }
func (f screenHistoryFixture) MinuteKlines(_ string, start, end time.Time) (protocol.Klines, error) {
	if f.missingMinutes {
		return nil, nil
	}
	return protocol.Klines{{Time: f.at.Add(-time.Minute), Open: 10, High: 11, Low: 10, Close: 11}, {Time: f.at, Open: 11, High: 12, Low: 11, Close: 12}}, nil
}

func TestScreenHistoryAndDiagnosis(t *testing.T) {
	at := time.Date(2026, 11, 13, 15, 0, 0, 0, protocol.Location())
	root := t.TempDir()
	s := newServerWithStores(NewAnalysisStore(root), NewCandidateStore(root))
	s.screen.now = func() time.Time { return at }
	s.screen.market = screenHistoryFixture{at: at, daily: true}
	s.screen.lookback = 1
	config, err := s.screen.load()
	if err != nil {
		t.Fatal(err)
	}
	config.Strategies = append(config.Strategies, screenStrategy{Name: "T+1", Enabled: true,
		Buy:   &builder.BuilderNode{Type: "condition", ComponentID: "buy_all"},
		Risks: []*builder.BuilderNode{{Type: "condition", ComponentID: "risk_holding_days", Params: map[string]any{"days": 1.0}}}})
	handler := s.Handler()
	get := func(path string, dst any) {
		t.Helper()
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 200 {
			t.Fatalf("GET %s: %d %s", path, w.Code, w.Body)
		}
		if err := json.Unmarshal(w.Body.Bytes(), dst); err != nil {
			t.Fatal(err)
		}
	}
	body, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodPut, "/api/screen", bytes.NewReader(body)))
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	waitForScreen(t, s.screen)
	var history screenHistoryPage
	get("/api/screen/history", &history)
	if history.Running || history.Total != 0 || history.Through != "" {
		t.Fatalf("buy scan started history replay: %+v", history)
	}
	savedConfig, err := s.screen.load()
	if err != nil {
		t.Fatal(err)
	}
	strategy := savedConfig.Strategies[len(savedConfig.Strategies)-1]
	db, err := openScreenLedger(s.screen.ledgerPath())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	buy := screener.Signal{Tracked: true, Strategy: strategy.ID, Code: "sh600000",
		BuyTime: at.AddDate(0, 0, -1).Format(time.DateTime), BuyPrice: 9}
	if err := insertScreenEntries(db, []screenPosition{{Signal: buy, Strategy: strategy}}); err != nil {
		t.Fatal(err)
	}
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/screen/history", nil))
	if w.Code != http.StatusOK {
		t.Fatal(w.Code, w.Body.String())
	}
	status := waitStatus(t, handler)
	if status["state"] != "done" || status["task"] != "screen_history" || status["unit"] != "stocks" ||
		status["done"] != float64(1) || status["total"] != float64(1) || status["currentCode"] != "sh600000" {
		t.Fatalf("history progress = %v", status)
	}
	get("/api/screen/history", &history)
	if history.Total != 2 || len(history.Rows) != 2 || history.Stats.Win != 1 || history.AvgHoldingDays != 1 {
		t.Fatalf("history = %+v", history)
	}
	var today screenHistoryPage
	get("/api/screen/history?mode=sell", &today)
	signal := history.Rows[1]
	if today.Total != 1 || today.Rows[0].Key() != signal.Key() ||
		signal.BuyPrice != 9 || signal.SellTime != at.Add(-time.Minute).Format(time.DateTime) ||
		signal.SellPrice != 11 || signal.SellSource != "minute" {
		t.Fatalf("signal = %+v, today = %+v", signal, today)
	}
	open, err := screenEntries(db, at.AddDate(0, 0, 1).Format(time.DateOnly))
	if err != nil || len(open) != 1 || open[0].Signal.BuyTime != at.Format(time.DateTime) {
		t.Fatalf("positions pending live exit = %+v, %v", open, err)
	}
	if events, err := screenEvents(db, 0, at.Format(time.DateOnly)); err != nil || len(events) != 0 {
		t.Fatalf("backfill produced live events = %+v, %v", events, err)
	}
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/screen/history", nil))
	if w.Code != http.StatusOK || waitStatus(t, handler)["state"] != "done" {
		t.Fatalf("retry failed: %d %s", w.Code, w.Body.String())
	}
	restarted := newServerWithStores(NewAnalysisStore(root), NewCandidateStore(root))
	restarted.screen.market = screenHistoryFixture{at: at, daily: true}
	restarted.screen.now = func() time.Time { return at }
	handler = restarted.Handler()
	var saved screenHistoryPage
	get("/api/screen/history", &saved)
	if saved.Total != 2 || saved.Rows[1].Key() != signal.Key() || saved.Rows[1].SellTime != signal.SellTime ||
		saved.Lookback != 1 || saved.Through != history.Through || saved.Running || saved.ArchiveError != "" {
		t.Fatalf("history after restart = %+v", saved)
	}
	handler = s.Handler()
	var diagnosis screenDiagnosis
	get("/api/screen/diagnose/sh600000?strategy="+url.QueryEscape(signal.Strategy), &diagnosis)
	if len(diagnosis.Klines) != 3 || len(diagnosis.Signals) != 2 || len(diagnosis.Rules) != 1 || !diagnosis.Rules[0].Matched {
		t.Fatalf("diagnosis = %+v", diagnosis)
	}
	if diagnosis.Klines[2].Close != 12 || diagnosis.Klines[1].Close != 10 {
		t.Fatal("diagnosis did not keep the original day bar and session close")
	}
}

func TestScreenHistoryLeavesUnverifiedExitPending(t *testing.T) {
	at := time.Date(2026, 11, 13, 15, 0, 0, 0, protocol.Location())
	root := t.TempDir()
	s := newServerWithStores(NewAnalysisStore(root), NewCandidateStore(root))
	market := screenHistoryFixture{at: at, daily: true, missingMinutes: true}
	config, err := s.screen.load()
	if err != nil {
		t.Fatal(err)
	}
	strategy := screenStrategy{ID: "test", Name: "T+1",
		Risks: []*builder.BuilderNode{{Type: "condition", ComponentID: "risk_holding_days", Params: map[string]any{"days": 1.0}}}}
	db, err := openScreenLedger(s.screen.ledgerPath())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	buy := screener.Signal{Tracked: true, Strategy: strategy.ID, Code: "sh600000",
		BuyTime: at.AddDate(0, 0, -1).Format(time.DateTime), BuyPrice: 9}
	if err := insertScreenEntries(db, []screenPosition{{Signal: buy, Strategy: strategy}}); err != nil {
		t.Fatal(err)
	}
	if err := s.screen.runHistory(context.Background(), market, config, at, 1, nil); err != nil {
		t.Fatal(err)
	}
	rows, err := screenEntries(db, at.Format(time.DateOnly))
	if err != nil || len(rows) != 1 || !rows[0].Signal.Incomplete || rows[0].Signal.SellTime != "" {
		t.Fatalf("unverified exit = %+v, %v", rows, err)
	}
}

func TestScreenReplaySnapshotKeepsLastSuccess(t *testing.T) {
	path := filepath.Join(t.TempDir(), "screen-trades.db")
	first := screener.History{Through: "2026-11-12"}
	if err := saveScreenReplay(context.Background(), path, screenConfig{}, first, 60, nil); err != nil {
		t.Fatal(err)
	}
	second := screener.History{Through: "2026-11-13"}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := saveScreenReplay(ctx, path, screenConfig{}, second, 30, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled save: %v", err)
	}
	if got, err := readScreenReplay(path); err != nil || got == nil || got.Through != first.Through || got.Lookback != 60 {
		t.Fatalf("previous snapshot = %+v, %v", got, err)
	}
	if err := saveScreenReplay(context.Background(), path, screenConfig{}, second, 30, nil); err != nil {
		t.Fatal(err)
	}
	if got, err := readScreenReplay(path); err != nil || got == nil || got.Through != second.Through || got.Lookback != 30 {
		t.Fatalf("replacement snapshot = %+v, %v", got, err)
	}
}

func TestScreenReadsLegacySQLite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trade.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`CREATE TABLE Trade(Code TEXT, Name TEXT, Strategy TEXT, BuyTime TEXT, BuyPrice REAL,
	Sold INTEGER, SellTime TEXT, SellPrice REAL, Tags TEXT);
	INSERT INTO Trade VALUES ('sh600000','浦发银行','macd-premium','2026-11-11 15:00:00',10,1,'2026-11-12 09:32:00',11,'["MACD"]');`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	db.Close()
	rows, err := readLegacySignals(path)
	if err != nil || len(rows) != 1 || !rows[0].Legacy || rows[0].Rate != 10 || len(rows[0].Tags) != 1 {
		t.Fatalf("archive = %+v, %v", rows, err)
	}
}
