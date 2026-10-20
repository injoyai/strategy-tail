package lab

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLabServesLocalKlineRuntime(t *testing.T) {
	s := &Server{mux: http.NewServeMux()}
	s.registerRoutes()
	for _, name := range []string{"vendor/klinecharts-10.1.0.min.js", "kline.js"} {
		response := httptest.NewRecorder()
		s.Handler().ServeHTTP(response, httptest.NewRequest("GET", "/assets/kline/"+name, nil))
		if response.Code != http.StatusOK || !strings.Contains(response.Header().Get("Content-Type"), "javascript") || response.Body.Len() == 0 {
			t.Fatalf("local chart asset %s: %d %s", name, response.Code, response.Header().Get("Content-Type"))
		}
	}
	response := httptest.NewRecorder()
	s.Handler().ServeHTTP(response, httptest.NewRequest("GET", "/assets/kline/missing.js", nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("missing chart asset: %d", response.Code)
	}
}
