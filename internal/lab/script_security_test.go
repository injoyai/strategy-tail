package lab

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckScriptDoesNotExecuteInitializers(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "executed")
	src := fmt.Sprintf(`package main
import "os"
var initialized = func() int { os.WriteFile(%q, []byte("global"), 0600); return 1 }()
func init() { os.WriteFile(%q, []byte("init"), 0600) }
`, marker, marker)
	if err := CheckScript(src); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("校验执行了初始化代码: %v", err)
	}
}

func TestServerRejectsCrossOriginMutations(t *testing.T) {
	h := NewServer().Handler()
	for _, route := range []struct{ method, path string }{
		{http.MethodPut, "/api/script"},
		{http.MethodPost, "/api/script/check"},
		{http.MethodPost, "/api/run"},
		{http.MethodPost, "/api/stop"},
		{http.MethodPost, "/api/analyze"},
		{http.MethodPost, "/api/strategy/run"},
		{http.MethodPost, "/api/factor-candidates"},
	} {
		t.Run(route.path, func(t *testing.T) {
			for _, header := range []string{"Origin", "Sec-Fetch-Site"} {
				r := httptest.NewRequest(route.method, "http://localhost"+route.path, strings.NewReader("{}"))
				if header == "Origin" {
					r.Header.Set(header, "https://example.invalid")
				} else {
					r.Header.Set(header, "cross-site")
				}
				w := httptest.NewRecorder()
				h.ServeHTTP(w, r)
				if w.Code != http.StatusForbidden {
					t.Fatalf("%s: status=%d, want 403", header, w.Code)
				}
			}
		})
	}
}

func TestScriptRequestsRejectOversizedBody(t *testing.T) {
	h := NewServer().Handler()
	for _, route := range []struct{ method, path string }{
		{http.MethodPut, "/api/script"},
		{http.MethodPost, "/api/script/check"},
	} {
		r := httptest.NewRequest(route.method, route.path, strings.NewReader(`{"content":"`+strings.Repeat(" ", int(maxCandidateBody))+`"}`))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("%s: status=%d, want 413", route.path, w.Code)
		}
	}
}
