package lab

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestWriteEndpointsRejectRemoteOrigin 写端点在进入业务逻辑前拒绝跨站 Origin。
func TestWriteEndpointsRejectRemoteOrigin(t *testing.T) {
	h := NewServer().Handler()
	endpoints := []struct{ method, path string }{
		{http.MethodPost, "/api/stop"},
		{http.MethodPost, "/api/analyze"},
		{http.MethodPost, "/api/strategy/run"},
		{http.MethodPost, "/api/factor-candidates"},
		{http.MethodPut, "/api/factor-candidates/fc_x"},
		{http.MethodPost, "/api/factor-models"},
		{http.MethodPost, "/api/portfolio-experiments"},
		{http.MethodPost, "/api/portfolio-validations"},
		{http.MethodPost, "/api/portfolio-runs/pe_x/start"},
		{http.MethodPost, "/api/portfolio-runs/pe_x/stop"},
	}
	for _, e := range endpoints {
		r := httptest.NewRequest(e.method, "http://192.168.1.10:8765"+e.path, strings.NewReader(`{}`))
		r.Header.Set("Origin", "http://192.168.1.20:8765")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusForbidden {
			t.Fatalf("%s %s: status=%d, want 403, body=%s", e.method, e.path, w.Code, w.Body.String())
		}
	}
}

// TestWriteEndpointsAcceptSameOrigin 同源 Origin 不触发 403（请求到达业务
// 层，无效请求体按业务校验返回非 403）。
func TestWriteEndpointsAcceptSameOrigin(t *testing.T) {
	h := NewServer().Handler()
	r := httptest.NewRequest(http.MethodPost, "http://192.168.1.10:8765/api/analyze", strings.NewReader(`{}`))
	r.Header.Set("Origin", "http://192.168.1.10:8765")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code == http.StatusForbidden {
		t.Fatalf("同源请求不应被拒: status=%d, body=%s", w.Code, w.Body.String())
	}
}
