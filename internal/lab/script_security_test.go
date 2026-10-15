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

func TestScriptAndStopAcceptRemoteOrigin(t *testing.T) {
	h := NewServer().Handler()
	for _, path := range []string{"/api/script/check", "/api/stop"} {
		r := httptest.NewRequest(http.MethodPost, "http://192.168.1.10:8765"+path, strings.NewReader(`{"content":"package main"}`))
		r.Header.Set("Origin", "http://192.168.1.20:8765")
		r.Header.Set("Sec-Fetch-Site", "cross-site")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("%s: status=%d, body=%s", path, w.Code, w.Body.String())
		}
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
