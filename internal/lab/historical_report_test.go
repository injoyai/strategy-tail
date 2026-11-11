package lab

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestHistoricalMACDReportRetainsSnapshot(t *testing.T) {
	t.Chdir(t.TempDir())
	raw := []byte(`{"source":"macd","config":{"scriptName":"MACD参数回测","macd":{"enabled":{"price":true},"maxPrice":120,"sellLookback":10}},"startedAt":"2026-10-13T15:00:00+08:00","finishedAt":"2026-10-13T15:00:01+08:00","variants":[]}`)
	var report Report
	if err := json.Unmarshal(raw, &report); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join("output", "trades", report.RunID())
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "report.json"), raw, 0644); err != nil {
		t.Fatal(err)
	}
	body := doReq(t, NewServer().Handler(), http.MethodGet, "/api/report/"+report.RunID(), nil, http.StatusOK)
	var restored Report
	if err := json.Unmarshal(body, &restored); err != nil {
		t.Fatal(err)
	}
	if restored.Source != "macd" || string(restored.Config.MACD) != string(report.Config.MACD) {
		t.Fatalf("历史 MACD 快照丢失: %s", body)
	}
}

func TestRetiredLabRoutes(t *testing.T) {
	handler := NewServer().Handler()
	for _, route := range []struct{ method, path string }{
		{http.MethodGet, "/api/script"},
		{http.MethodPut, "/api/script"},
		{http.MethodPost, "/api/script/check"},
		{http.MethodPost, "/api/run"},
		{http.MethodGet, "/api/macd/config"},
		{http.MethodPost, "/api/macd/run"},
		{http.MethodGet, "/api/builder/templates"},
		{http.MethodPost, "/api/builder/strategies/st_20990101T000000000Z_00000000/copy"},
	} {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(route.method, route.path, nil))
		if w.Code != http.StatusNotFound && w.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s %s returned %d", route.method, route.path, w.Code)
		}
	}
}
