package lab

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestScriptCheckAcceptRemoteOriginStopRejects /api/script/check 为只读校验
// 接口，暂维持跨站豁免（豁免范围应另行评估）；/api/stop 有副作用，跨站
// 来源必须拒绝，同源放行由 TestServerStopSameOrigin 覆盖。
func TestScriptCheckAcceptRemoteOriginStopRejects(t *testing.T) {
	h := NewServer().Handler()
	r := httptest.NewRequest(http.MethodPost, "http://192.168.1.10:8765/api/script/check", strings.NewReader(`{"content":"package main"}`))
	r.Header.Set("Origin", "http://192.168.1.20:8765")
	r.Header.Set("Sec-Fetch-Site", "cross-site")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("/api/script/check: status=%d, body=%s", w.Code, w.Body.String())
	}
	stop := httptest.NewRequest(http.MethodPost, "http://192.168.1.10:8765/api/stop", nil)
	stop.Header.Set("Origin", "http://192.168.1.20:8765")
	stop.Header.Set("Sec-Fetch-Site", "cross-site")
	w2 := httptest.NewRecorder()
	h.ServeHTTP(w2, stop)
	if w2.Code != http.StatusForbidden {
		t.Fatalf("/api/stop: status=%d, body=%s", w2.Code, w2.Body.String())
	}
}

// TestWriteEndpointsRejectRemoteOrigin 非 GET 写端点必须拒绝跨站 Origin
// （同源校验在业务逻辑前拦截，不会触碰存储）；只读豁免端点由
// TestScriptCheckAcceptRemoteOriginStopRejects 单独覆盖。
func TestWriteEndpointsRejectRemoteOrigin(t *testing.T) {
	h := NewServer().Handler()
	endpoints := []struct{ method, path string }{
		{http.MethodPut, "/api/script"},
		{http.MethodPost, "/api/run"},
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

func TestScriptCheckAcceptsLargeBody(t *testing.T) {
	h := NewServer().Handler()
	r := httptest.NewRequest(http.MethodPost, "/api/script/check", strings.NewReader(`{"content":"package main`+strings.Repeat(" ", 1<<16)+`"}`))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"ok":true`) {
		t.Fatalf("status=%d, body=%s", w.Code, w.Body.String())
	}
}
