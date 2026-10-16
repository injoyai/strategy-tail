package lab

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func writeHistoryReport(t *testing.T, at string) *Report {
	t.Helper()
	rep := &Report{Config: RunConfig{ScriptName: "builder", StartYear: 2025, EndYear: 2025,
		Builder: &BuilderSpec{Name: "历史策略"}}, FinishedAt: at, Source: sourceBuilder,
		Variants: []VariantReport{{Name: "策略/一"}}}
	dir := filepath.Join("output", "trades", rep.RunID())
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(rep)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "report.json"), data, 0644); err != nil {
		t.Fatal(err)
	}
	exports := filepath.Join("output", "trades", "builder")
	if err := os.MkdirAll(exports, 0755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{rep.RunID() + "_策略_一.csv", rep.RunID() + "_summary.html"} {
		if err := os.WriteFile(filepath.Join(exports, name), []byte("export"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	return rep
}

func TestReportHistorySurvivesRestartAndDeletesOnlySelectedRun(t *testing.T) {
	t.Chdir(t.TempDir())
	older := writeHistoryReport(t, "2026-10-14T15:30:00+08:00")
	newer := writeHistoryReport(t, "2026-10-15T15:30:00+08:00")
	s := NewServer()
	h := s.Handler()
	var items []reportItem
	if err := json.Unmarshal(doReq(t, h, http.MethodGet, "/api/reports", nil, http.StatusOK), &items); err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[0].ID != newer.RunID() || items[0].Name != "历史策略" {
		t.Fatalf("history = %+v", items)
	}
	var got Report
	if err := json.Unmarshal(doReq(t, h, http.MethodGet, "/api/report/latest", nil, http.StatusOK), &got); err != nil || got.FinishedAt != newer.FinishedAt {
		t.Fatalf("latest after restart = %+v, err=%v", got, err)
	}
	s.runner.lastReport.Store(newer)
	task, done := "backtest", "done"
	s.runner.task.Store(&task)
	s.runner.state.Store(&done)
	doReq(t, h, http.MethodDelete, "/api/report/"+newer.RunID(), nil, http.StatusOK)
	if s.runner.LatestReport() != nil || s.runner.Status()["state"] != "idle" {
		t.Fatal("deleted report remains in memory/task state")
	}
	for _, name := range []string{newer.RunID(), filepath.Join("builder", newer.RunID()+"_策略_一.csv"), filepath.Join("builder", newer.RunID()+"_summary.html")} {
		if _, err := os.Stat(filepath.Join("output", "trades", name)); !os.IsNotExist(err) {
			t.Fatalf("deleted artifact %q still exists: %v", name, err)
		}
	}
	for _, name := range []string{filepath.Join(older.RunID(), "report.json"), filepath.Join("builder", older.RunID()+"_策略_一.csv"), filepath.Join("builder", older.RunID()+"_summary.html")} {
		if _, err := os.Stat(filepath.Join("output", "trades", name)); err != nil {
			t.Fatalf("other run changed: %s: %v", name, err)
		}
	}
	doReq(t, h, http.MethodGet, "/api/report/"+newer.RunID(), nil, http.StatusNotFound)
	if err := json.Unmarshal(doReq(t, NewServer().Handler(), http.MethodGet, "/api/report/latest", nil, http.StatusOK), &got); err != nil || got.FinishedAt != older.FinishedAt {
		t.Fatalf("remaining latest = %+v, err=%v", got, err)
	}
	doReq(t, h, http.MethodDelete, "/api/report/"+older.RunID(), nil, http.StatusOK)
	if data := doReq(t, h, http.MethodGet, "/api/reports", nil, http.StatusOK); string(data) != "[]\n" {
		t.Fatalf("empty history = %s", data)
	}
	doReq(t, h, http.MethodGet, "/api/report/latest", nil, http.StatusNotFound)
}

func TestReportHistoryDeleteGuards(t *testing.T) {
	t.Chdir(t.TempDir())
	rep := writeHistoryReport(t, "2026-10-15T15:30:00+08:00")
	s := NewServer()
	h := s.Handler()
	request := httptest.NewRequest(http.MethodDelete, "/api/report/"+rep.RunID(), nil)
	request.Header.Set("Origin", "https://other.example")
	response := httptest.NewRecorder()
	h.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("cross-origin delete = %d", response.Code)
	}
	s.runner.mu.Lock()
	doReq(t, h, http.MethodDelete, "/api/report/"+rep.RunID(), nil, http.StatusConflict)
	s.runner.mu.Unlock()
	for _, id := range []string{"..%2Fbuilder", ".hidden", "latest", "foo%5Cbar"} {
		doReq(t, h, http.MethodDelete, "/api/report/"+id, nil, http.StatusBadRequest)
	}
	doReq(t, h, http.MethodDelete, "/api/report/missing", nil, http.StatusNotFound)
	doReq(t, h, http.MethodGet, "/api/report/"+rep.RunID(), nil, http.StatusOK)
	if err := os.MkdirAll(filepath.Join("output", "trades", "not-a-run"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join("output", "trades", "not-a-run", "report.json"), []byte(`{}`), 0644); err != nil {
		t.Fatal(err)
	}
	doReq(t, h, http.MethodDelete, "/api/report/not-a-run", nil, http.StatusBadRequest)
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "report.json"), []byte(`{}`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join("output", "trades", "linked")); err != nil {
		t.Fatal(err)
	}
	doReq(t, h, http.MethodDelete, "/api/report/linked", nil, http.StatusInternalServerError)
	if _, err := os.Stat(filepath.Join(outside, "report.json")); err != nil {
		t.Fatal("delete escaped reports directory")
	}
}
