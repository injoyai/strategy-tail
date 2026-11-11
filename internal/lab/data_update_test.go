package lab

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	common "github.com/injoyai/strategy-tail"
)

func TestMarketDataStatusReadsStoredBenchmarkDate(t *testing.T) {
	dir := t.TempDir()
	old := common.Pull
	common.Pull = fixturePull(t, dir)
	t.Cleanup(func() { common.Pull = old })
	writeFakeDayDB(t, dir, "sh000001", 2, time.Date(2026, 10, 12, 15, 0, 0, 0, time.UTC))
	s := NewServer()
	read := func() marketDataStatus {
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/data-update/status", nil))
		var status marketDataStatus
		if err := json.Unmarshal(w.Body.Bytes(), &status); w.Code != http.StatusOK || err != nil {
			t.Fatalf("status %d, parse error %v", w.Code, err)
		}
		return status
	}
	if got := read(); got.BenchmarkDate != "2026-10-13" || got.Storage != common.Pull.StorageIdentity() || got.ReadError != "" {
		t.Fatalf("unexpected status %+v", got)
	}
	if err := fixtureMarket(t, dir).Close(); err != nil {
		t.Fatal(err)
	}
	if got := read(); got.ReadError == "" || got.BenchmarkDate != "" {
		t.Fatalf("unexpected failure status %+v", got)
	}
}

func TestLabHasNoMarketWriteEndpoints(t *testing.T) {
	s := NewServer()
	for _, req := range []*http.Request{
		httptest.NewRequest(http.MethodPost, "/api/data-update/run", nil),
		httptest.NewRequest(http.MethodPut, "/api/data-update/config", nil),
	} {
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, req)
		if w.Code != http.StatusNotFound && w.Code != http.StatusMethodNotAllowed {
			t.Fatalf("%s %s returned %d", req.Method, req.URL.Path, w.Code)
		}
	}
}
