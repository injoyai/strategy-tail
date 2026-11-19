package lab

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
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
	config.Strategies = append(config.Strategies, screenStrategy{Name: "T+1",
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
	if history.Running || history.Total != 0 || history.Through != "" || history.DefaultLookback != 1 {
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
	strategyQuery := "?strategy=" + url.QueryEscape(strategy.ID)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/screen/history"+strategyQuery, nil))
	if w.Code != http.StatusOK {
		t.Fatal(w.Code, w.Body.String())
	}
	status := waitStatus(t, handler)
	if status["state"] != "done" || status["task"] != "screen_history" || status["unit"] != "stocks" ||
		status["done"] != float64(1) || status["total"] != float64(1) || status["currentCode"] != "sh600000" {
		t.Fatalf("history progress = %v", status)
	}
	get("/api/screen/history"+strategyQuery, &history)
	if history.Total != 2 || len(history.Rows) != 2 || history.Stats.Win != 1 ||
		history.Stats.Closed != 1 || history.Stats.Marked != 1 || history.Stats.WinRate != 50 ||
		history.Rows[0].LastPrice != 12 || history.Rows[0].Rate != 0 || history.AvgHoldingDays != 1 || history.Lookback != 1 {
		t.Fatalf("history = %+v", history)
	}
	var screen screenState
	get("/api/screen", &screen)
	stats := screen.StrategyStats[strategy.ID]
	if stats.Total != history.Stats.Total || stats.WinRate != history.Stats.WinRate ||
		stats.ProfitFactor != nil || screen.StrategyStats[config.Strategies[0].ID].Total != 0 || screen.StrategyStatsError != "" {
		t.Fatalf("strategy stats = %+v, error = %q", screen.StrategyStats, screen.StrategyStatsError)
	}
	var today screenHistoryPage
	get("/api/screen/history?mode=sell", &today)
	signal := history.Rows[1]
	if today.Total != 1 || today.Rows[0].Key() != signal.Key() ||
		signal.BuyPrice != 9 || signal.SellTime != at.Add(-time.Minute).Format(time.DateTime) ||
		signal.SellPrice != 11 || signal.SellSource != "minute" {
		t.Fatalf("signal = %+v, today = %+v", signal, today)
	}
	open, err := screenEntries(db, at.AddDate(0, 0, 1).Format(time.DateOnly), "")
	if err != nil || len(open) != 1 || open[0].Signal.BuyTime != at.Format(time.DateTime) {
		t.Fatalf("positions pending live exit = %+v, %v", open, err)
	}
	if events, err := screenEvents(db, 0, at.Format(time.DateOnly)); err != nil || len(events) != 0 {
		t.Fatalf("backfill produced live events = %+v, %v", events, err)
	}
	for _, value := range []string{"", "0", "-1", "241", "1.5"} {
		w = httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/screen/history"+strategyQuery+"&lookback="+value, nil))
		if w.Code != http.StatusBadRequest {
			t.Fatalf("lookback=%q: %d %s", value, w.Code, w.Body.String())
		}
	}
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/screen/history"+strategyQuery+"&lookback=2", nil))
	if w.Code != http.StatusOK || waitStatus(t, handler)["state"] != "done" {
		t.Fatalf("retry failed: %d %s", w.Code, w.Body.String())
	}
	get("/api/screen/history"+strategyQuery, &history)
	if history.Lookback != 2 || history.DefaultLookback != 1 {
		t.Fatalf("selected lookback = %d, default = %d", history.Lookback, history.DefaultLookback)
	}
	restarted := newServerWithStores(NewAnalysisStore(root), NewCandidateStore(root))
	restarted.screen.market = screenHistoryFixture{at: at, daily: true}
	restarted.screen.now = func() time.Time { return at }
	handler = restarted.Handler()
	var saved screenHistoryPage
	get("/api/screen/history"+strategyQuery, &saved)
	if saved.Total != 2 || saved.Rows[1].Key() != signal.Key() || saved.Rows[1].SellTime != signal.SellTime ||
		saved.Lookback != 2 || saved.Through != history.Through || saved.Running || saved.ArchiveError != "" {
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

func TestScreenHistoryReturnsAllFilteredRows(t *testing.T) {
	at := time.Date(2026, 11, 13, 15, 0, 0, 0, protocol.Location())
	root := t.TempDir()
	s := newServerWithStores(NewAnalysisStore(root), NewCandidateStore(root))
	s.screen.now = func() time.Time { return at }
	s.screen.market = screenHistoryFixture{at: at}
	config, err := s.screen.load()
	if err != nil {
		t.Fatal(err)
	}
	strategy := config.Strategies[0]
	history := screener.History{Through: at.Format(time.DateOnly)}
	for i := range 36 {
		tags := []string{"匹配"}
		if i == 35 {
			tags = nil
		}
		history.Rows = append(history.Rows, screener.Signal{
			Strategy: strategy.ID, Code: fmt.Sprintf("sh%06d", 600000+i), Name: "测试股票",
			BuyTime: "2026-11-12 15:00:00", BuyPrice: 10, SellTime: "2026-11-13 14:00:00", SellPrice: 11, Tags: tags,
		})
	}
	if err := saveScreenReplay(context.Background(), s.screen.ledgerPath(), strategy, history, 60, nil); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		query                  string
		total, rows, pageCount int
	}{
		{"mode=history&status=sold&tag=" + url.QueryEscape("匹配"), 35, 35, 1},
		{"mode=history&page=2", 36, 6, 2},
		{"mode=sell", 36, 30, 2},
	} {
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/screen/history?"+tc.query, nil))
		var got screenHistoryPage
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if w.Code != http.StatusOK || got.Total != tc.total || len(got.Rows) != tc.rows ||
			got.Pages != tc.pageCount || got.Stats.Closed != tc.total {
			t.Fatalf("%s: status %d, total %d, rows %d, pages %d, closed %d",
				tc.query, w.Code, got.Total, len(got.Rows), got.Pages, got.Stats.Closed)
		}
	}
}

func TestScreenHistoryOnlyReplaysSelectedStrategy(t *testing.T) {
	at := time.Date(2026, 11, 13, 15, 0, 0, 0, protocol.Location())
	root := t.TempDir()
	s := newServerWithStores(NewAnalysisStore(root), NewCandidateStore(root))
	s.screen.now = func() time.Time { return at }
	s.screen.market = screenHistoryFixture{at: at, daily: true}
	const legacyConfig = `{"version":34,"strategies":[
		{"id":"macd-premium","name":"MACD精选","enabled":false},
		{"id":"macd-base","name":"MACD基础(测试)","enabled":false},
		{"id":"boll-rsi","name":"布林+RSI(测试)","enabled":false},
		{"id":"macd-premium2","name":"MACD精选2","enabled":false},
		{"id":"st_20261113T070000000Z_00000001","name":"策略一","enabled":true,"buy":{"type":"condition","componentId":"buy_all"}},
		{"id":"st_20261113T070000000Z_00000002","name":"策略二","enabled":false,"buy":{"type":"condition","componentId":"buy_all"}}
	]}`
	if err := os.WriteFile(s.screen.path, []byte(legacyConfig), 0600); err != nil {
		t.Fatal(err)
	}
	config, err := s.screen.load()
	if err != nil {
		t.Fatal(err)
	}
	other, selected := config.Strategies[4], config.Strategies[5]
	other.Risks = []*builder.BuilderNode{{Type: "condition", ComponentID: "risk_holding_days", Params: map[string]any{"days": 1.0}}}
	db, err := openScreenLedger(s.screen.ledgerPath())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	buy := screener.Signal{Tracked: true, Strategy: other.ID, Code: "sh600000",
		BuyTime: at.AddDate(0, 0, -1).Format(time.DateTime), BuyPrice: 9}
	if err := insertScreenEntries(db, []screenPosition{{Signal: buy, Strategy: other}}); err != nil {
		t.Fatal(err)
	}
	if err := saveScreenReplay(context.Background(), s.screen.ledgerPath(), other, screener.History{
		Through: "2026-11-12", FailedCodes: []string{"sh600001"},
	}, 30, nil); err != nil {
		t.Fatal(err)
	}
	handler := s.Handler()
	for _, query := range []string{"", "?strategy=", "?strategy=missing", "?strategy=" + selected.ID + "&strategy=" + other.ID} {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/screen/history"+query, nil))
		if w.Code != http.StatusBadRequest {
			t.Fatalf("invalid strategy %q: %d %s", query, w.Code, w.Body)
		}
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/screen/history?lookback=1&strategy="+selected.ID, nil))
	if w.Code != http.StatusOK || waitStatus(t, handler)["state"] != "done" {
		t.Fatalf("history replay: %d %s", w.Code, w.Body)
	}
	for _, tc := range []struct {
		strategy screenStrategy
		buyTime  string
		through  string
		lookback int
		failures int
	}{
		{selected, at.Format(time.DateTime), at.Format(time.DateOnly), 1, 0},
		{other, buy.BuyTime, "2026-11-12", 30, 1},
	} {
		w = httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/screen/history?mode=history&strategy="+tc.strategy.ID, nil))
		var history screenHistoryPage
		if err := json.Unmarshal(w.Body.Bytes(), &history); err != nil {
			t.Fatal(err)
		}
		if w.Code != http.StatusOK || len(history.Rows) != 1 ||
			history.Through != tc.through || history.Lookback != tc.lookback || len(history.FailedCodes) != tc.failures {
			t.Fatalf("strategy %s: %d %+v", tc.strategy.ID, w.Code, history)
		}
		if row := history.Rows[0]; row.Strategy != tc.strategy.ID || row.BuyTime != tc.buyTime || row.SellTime != "" {
			t.Fatalf("strategy %s: trade %+v", tc.strategy.ID, row)
		}
	}
	if got, err := readScreenReplay(s.screen.ledgerPath(), config.Strategies[0].ID); err != nil || got != nil {
		t.Fatalf("unselected strategy snapshot = %+v, %v", got, err)
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
	strategy := screenStrategy{ID: "st_20261113T070000000Z_00000001", Name: "T+1",
		Buy:   &builder.BuilderNode{Type: "condition", ComponentID: "buy_all"},
		Risks: []*builder.BuilderNode{{Type: "condition", ComponentID: "risk_holding_days", Params: map[string]any{"days": 1.0}}}}
	config.Strategies = append(config.Strategies, strategy)
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
	if err := s.screen.runHistory(context.Background(), market, config, strategy, at, 1, nil); err != nil {
		t.Fatal(err)
	}
	rows, err := screenEntries(db, at.Format(time.DateOnly), "")
	if err != nil || len(rows) != 1 || !rows[0].Signal.Incomplete || rows[0].Signal.SellTime != "" {
		t.Fatalf("unverified exit = %+v, %v", rows, err)
	}
}

func TestScreenReplaySnapshotKeepsLastSuccess(t *testing.T) {
	path := filepath.Join(t.TempDir(), "screen-trades.db")
	strategy := screenStrategy{ID: "macd-premium"}
	first := screener.History{Through: "2026-11-12"}
	if err := saveScreenReplay(context.Background(), path, strategy, first, 60, nil); err != nil {
		t.Fatal(err)
	}
	second := screener.History{Through: "2026-11-13"}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := saveScreenReplay(ctx, path, strategy, second, 30, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled save: %v", err)
	}
	if got, err := readScreenReplay(path, strategy.ID); err != nil || got == nil || got.Through != first.Through || got.Lookback != 60 {
		t.Fatalf("previous snapshot = %+v, %v", got, err)
	}
	if err := saveScreenReplay(context.Background(), path, strategy, second, 30, nil); err != nil {
		t.Fatal(err)
	}
	if got, err := readScreenReplay(path, strategy.ID); err != nil || got == nil || got.Through != second.Through || got.Lookback != 30 {
		t.Fatalf("replacement snapshot = %+v, %v", got, err)
	}
}

func TestScreenReplayKeepsLegacySnapshotUnscoped(t *testing.T) {
	path := filepath.Join(t.TempDir(), "screen-trades.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`
		CREATE TABLE screen_replay_status (id INTEGER PRIMARY KEY CHECK (id = 1), payload BLOB NOT NULL);
		INSERT INTO screen_replay_status VALUES (1, '{"Through":"2026-11-12","Lookback":240}');
	`); err != nil {
		t.Fatal(err)
	}
	strategy := screenStrategy{ID: "macd-premium"}
	if got, err := readScreenReplay(path, strategy.ID); err != nil || got != nil {
		t.Fatalf("legacy snapshot attributed to strategy: %+v, %v", got, err)
	}
	history := screener.History{Through: "2026-11-13"}
	if err := saveScreenReplay(context.Background(), path, strategy, history, 60, nil); err != nil {
		t.Fatal(err)
	}
	if got, err := readScreenReplay(path, ""); err != nil || got == nil || got.Lookback != 240 {
		t.Fatalf("legacy snapshot = %+v, %v", got, err)
	}
	if got, err := readScreenReplay(path, strategy.ID); err != nil || got == nil || got.Through != history.Through || got.Lookback != 60 {
		t.Fatalf("strategy snapshot = %+v, %v", got, err)
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

func TestScreenHistoryReadsRelativeLedgerAfterReplay(t *testing.T) {
	t.Chdir(t.TempDir())
	root := filepath.Join("data", "选股 #1")
	s := newServerWithStores(NewAnalysisStore(root), NewCandidateStore(root))
	config, err := s.screen.load()
	if err != nil {
		t.Fatal(err)
	}
	strategy := config.Strategies[0]
	history := screener.History{Through: "2026-11-13", Rows: []screener.Signal{{
		Tracked: true, Strategy: strategy.ID, Code: "sh600000", Name: "浦发银行",
		BuyTime: "2026-11-13 15:00:00", BuyPrice: 10,
	}}}
	if err := saveScreenReplay(context.Background(), s.screen.ledgerPath(), strategy, history, 60, nil); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/screen/history?strategy="+strategy.ID, nil))
	var page screenHistoryPage
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if w.Code != http.StatusOK || page.Total != 1 || page.Through != history.Through || page.ArchiveError != "" {
		t.Fatalf("relative ledger response: %d %s", w.Code, w.Body.String())
	}
}

func TestScreenHistoryReportsUnreadableLedger(t *testing.T) {
	root := t.TempDir()
	s := newServerWithStores(NewAnalysisStore(root), NewCandidateStore(root))
	if err := os.WriteFile(s.screen.ledgerPath(), []byte("invalid SQLite"), 0600); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/screen/history", nil))
	if w.Code != http.StatusInternalServerError || !bytes.Contains(w.Body.Bytes(), []byte(errScreenLedgerRead.Error())) {
		t.Fatalf("unreadable ledger response: %d %s", w.Code, w.Body.String())
	}
}

func TestScreenHistoryUsesLatestPriceForOpenPositions(t *testing.T) {
	at := time.Date(2026, 11, 13, 10, 0, 0, 0, protocol.Location())
	root := t.TempDir()
	s := newServerWithStores(NewAnalysisStore(root), NewCandidateStore(root))
	s.screen.now = func() time.Time { return at }
	s.screen.market = screenHistoryFixture{at: at}
	config, err := s.screen.load()
	if err != nil {
		t.Fatal(err)
	}
	strategy := config.Strategies[0]
	db, err := openScreenLedger(s.screen.ledgerPath())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	buyTime := "2026-11-12 15:00:00"
	first := screener.Signal{Code: "sh600000", Strategy: strategy.ID, BuyTime: buyTime, BuyPrice: 10, Incomplete: true}
	second := screener.Signal{Code: "sh600001", Strategy: strategy.ID, BuyTime: "2026-11-13 15:00:00", BuyPrice: 10}
	if err := insertScreenEntries(db, []screenPosition{{Signal: first, Strategy: strategy}, {Signal: second, Strategy: strategy}}); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/screen/history?status=open", nil))
	var page screenHistoryPage
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if w.Code != http.StatusOK || page.Total != 2 || page.Stats.Closed != 0 || page.Stats.Marked != 1 || page.Stats.Unpriced != 1 ||
		page.Stats.WinRate != 100 || page.Rows[1].LastPrice != 12 || page.Rows[1].Rate != 20 || page.Rows[1].SellTime != "" {
		t.Fatalf("open position performance: %d %s", w.Code, w.Body.String())
	}
	stored, err := screenEntries(db, "", "")
	if err != nil || len(stored) != 2 || stored[0].Signal.LastPrice != 0 || stored[0].Signal.SellTime != "" {
		t.Fatalf("mark changed the ledger: %+v, %v", stored, err)
	}
}
