package lab

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/injoyai/strategy-tail/internal/lab/builder"
	"github.com/injoyai/strategy-tail/internal/screener"
	"github.com/injoyai/strategy-tail/lib/extend"
	"github.com/injoyai/tdx/v2/protocol"
)

type screenFixture struct {
	at      time.Time
	daily   bool
	wait    <-chan struct{}
	started chan<- struct{}
}

func (f screenFixture) Codes() ([]string, error) {
	return []string{"sh600002", "sh600000", "sh600001", "sh600000"}, nil
}

func (f screenFixture) Name(code string) string { return "股票 " + code }

func (f screenFixture) LatestDayDate(string) (string, error) {
	if f.daily {
		return f.at.Format(time.DateOnly), nil
	}
	return f.at.AddDate(0, 0, -1).Format(time.DateOnly), nil
}

func (f screenFixture) StockSessionBars([]string, time.Time) (map[string]*protocol.Kline, error) {
	if f.started != nil {
		select {
		case f.started <- struct{}{}:
		default:
		}
		<-f.wait
	}
	return map[string]*protocol.Kline{
		"sh600000": {Time: f.at.Add(-time.Minute), Open: 10, Close: 11, High: 11, Low: 10},
		"sh600001": {Time: f.at.Add(-time.Minute), Open: 10, Close: 10, High: 11, Low: 10},
	}, nil
}

func (f screenFixture) DayKlines(code string, start, end time.Time) (extend.Klines, error) {
	if code == "sh600001" {
		return nil, errors.New("fixture unavailable")
	}
	bars := extend.Klines{{Kline: &protocol.Kline{Time: f.at.AddDate(0, 0, -1), Open: 10, Close: 10, High: 10, Low: 10}}}
	if f.daily && code == "sh600000" && !end.Before(f.at) {
		bars = append(bars, &extend.Kline{Kline: &protocol.Kline{Time: f.at, Last: 10, Open: 10, Close: 12, High: 12, Low: 10}})
	}
	return bars, nil
}

func (f screenFixture) EquityAt(string, time.Time) *protocol.Equity { return nil }

func TestScreenConfigAndScan(t *testing.T) {
	at := time.Date(2026, 11, 13, 10, 0, 0, 0, protocol.Location())
	root := t.TempDir()
	s := newServerWithStores(NewAnalysisStore(root), NewCandidateStore(root))
	s.screen.market = screenFixture{at: at}
	s.screen.now = func() time.Time { return at }
	handler := s.Handler()

	request := func(method, path string, body any, expected int) screenState {
		t.Helper()
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(method, path, bytes.NewReader(data)))
		if w.Code != expected {
			t.Fatalf("%s %s: status %d, want %d (%s)", method, path, w.Code, expected, w.Body)
		}
		var state screenState
		if expected == http.StatusOK {
			if err := json.Unmarshal(w.Body.Bytes(), &state); err != nil {
				t.Fatal(err)
			}
		}
		return state
	}
	initial := request("GET", "/api/screen", nil, http.StatusOK)
	if len(initial.Config.Strategies) != 4 || hasEnabledScreenStrategy(initial.Config) || initial.Running {
		t.Fatalf("unexpected default config: %+v", initial)
	}
	request("POST", "/api/screen/run", nil, http.StatusBadRequest)
	config := initial.Config
	config.Strategies = append(config.Strategies, screenStrategy{Name: "价格组合", Enabled: true})
	request("PUT", "/api/screen", config, http.StatusBadRequest)
	config.Strategies[4].Buy = &builder.BuilderNode{Type: "group", Op: "and", Children: []*builder.BuilderNode{
		{Type: "condition", ComponentID: "buy_price", Params: map[string]any{"min": 10.5}},
		{Type: "group", Op: "or", Children: []*builder.BuilderNode{
			{Type: "condition", ComponentID: "buy_price", Params: map[string]any{"max": 8.0}},
			{Type: "condition", ComponentID: "buy_price", Params: map[string]any{"max": 12.0}},
		}},
	}}
	config.Strategies = append(config.Strategies,
		screenStrategy{Name: "第二策略", Enabled: true, Buy: &builder.BuilderNode{Type: "condition", ComponentID: "buy_all"}},
		screenStrategy{Name: "关闭策略", Buy: &builder.BuilderNode{Type: "condition", ComponentID: "buy_all"}})
	origin := httptest.NewRequest("PUT", "/api/screen", nil)
	origin.Header.Set("Origin", "https://other.example")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, origin)
	if w.Code != http.StatusForbidden {
		t.Fatalf("cross-origin save status = %d", w.Code)
	}
	saved := request("PUT", "/api/screen", config, http.StatusOK)
	if !saved.Running || saved.Preview == nil || saved.Config.Version != 2 || saved.Config.Strategies[4].ID == "" {
		t.Fatalf("saved config = %+v", saved)
	}
	request("PUT", "/api/screen", config, http.StatusConflict)
	finished := waitForScreen(t, s.screen)
	if finished.Preview != nil {
		t.Fatalf("finished scan still has preview progress: %+v", finished.Preview)
	}
	result := finished.Result
	if result == nil || result.Universe != 3 || result.Evaluated != 1 || result.Failed != 1 || result.NoSessionBars != 1 {
		t.Fatalf("unexpected coverage: %+v", result)
	}
	if len(result.Matches) != 1 {
		t.Fatalf("want one combined row, got %+v", result.Matches)
	}
	if run := request("POST", "/api/screen/run", nil, http.StatusOK); !run.Running || run.Preview != nil {
		t.Fatalf("manual scan state = %+v", run)
	}
	status := waitStatus(t, handler)
	if status["state"] != "done" || status["task"] != "screen_scan" || status["phase"] != "scan" ||
		status["done"] != float64(3) || status["total"] != float64(3) {
		t.Fatalf("manual scan progress = %v", status)
	}
	row := result.Matches[0]
	if row.Code != "sh600000" || row.Price != 11 || len(row.Hits) != 2 ||
		row.Hits[0].ID != saved.Config.Strategies[4].ID || row.Hits[1].ID != saved.Config.Strategies[5].ID {
		t.Fatalf("unexpected match: %+v", row)
	}
	reopened := newScreenService(filepath.Join(root, "screen.json"))
	persisted, err := reopened.load()
	if err != nil || persisted.Version != 2 || !persisted.Strategies[4].Enabled || persisted.Strategies[6].Enabled {
		t.Fatalf("config did not persist: %+v, %v", persisted, err)
	}
	config = saved.Config
	config.Strategies = config.Strategies[:4]
	cleared := request("PUT", "/api/screen", config, http.StatusOK)
	if cleared.Running || cleared.Result != nil {
		t.Fatalf("removed strategy results still visible: %+v", cleared)
	}
}

func TestScreenSupersededScanAndClosePrice(t *testing.T) {
	at := time.Date(2026, 11, 13, 15, 0, 0, 0, protocol.Location())
	root := t.TempDir()
	s := newServerWithStores(NewAnalysisStore(root), NewCandidateStore(root))
	release, started := make(chan struct{}), make(chan struct{}, 1)
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	s.screen.market = screenFixture{at: at, daily: true, wait: release, started: started}
	s.screen.now = func() time.Time { return at }

	save := func(config screenConfig) screenConfig {
		t.Helper()
		data, err := json.Marshal(config)
		if err != nil {
			t.Fatal(err)
		}
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, httptest.NewRequest("PUT", "/api/screen", bytes.NewReader(data)))
		if w.Code != http.StatusOK {
			t.Fatalf("save: %d %s", w.Code, w.Body)
		}
		var state screenState
		if err := json.Unmarshal(w.Body.Bytes(), &state); err != nil {
			t.Fatal(err)
		}
		if state.Result != nil {
			t.Fatalf("superseded result visible: %+v", state.Result)
		}
		return state.Config
	}
	config, err := s.screen.load()
	if err != nil {
		t.Fatal(err)
	}
	config.Strategies = append(config.Strategies, screenStrategy{Name: "扫描一", Enabled: true,
		Buy: &builder.BuilderNode{Type: "condition", ComponentID: "buy_all"}})
	config = save(config)
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("scan did not start")
	}
	oldID := config.Strategies[4].ID
	config.Strategies[4] = screenStrategy{Name: "扫描二", Enabled: true, Buy: &builder.BuilderNode{Type: "condition", ComponentID: "buy_all"}}
	config = save(config)
	close(release)
	final := waitForScreen(t, s.screen)
	result := final.Result
	if result == nil || !result.DailyAvailable || len(result.Matches) != 1 {
		t.Fatalf("expected official daily result: %+v", final)
	}
	hit := result.Matches[0]
	if hit.Price != 12 || len(hit.Hits) != 1 || hit.Hits[0].ID == oldID || hit.Hits[0].ID != config.Strategies[4].ID {
		t.Fatalf("obsolete rules or intraday price used: %+v", hit)
	}
}

func waitForScreen(t *testing.T, screen *screenService) screenState {
	t.Helper()
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); {
		screen.mu.Lock()
		config, err := screen.load()
		state := screen.state(config)
		screen.mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		if !state.Running {
			return state
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("scan did not finish")
	return screenState{}
}

var _ screener.Market = screenFixture{}
