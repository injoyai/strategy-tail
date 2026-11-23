package lab

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHandleNotifyDownload(t *testing.T) {
	file := filepath.Join(t.TempDir(), "strategy-notify.exe")
	if err := os.WriteFile(file, []byte("exe-bytes"), 0644); err != nil {
		t.Fatal(err)
	}
	old := notifierCandidates
	notifierCandidates = []string{file}
	t.Cleanup(func() { notifierCandidates = old })

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/notify/tool", nil)
	(&Server{}).handleNotifyDownload(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("状态码 %d，期望 200", recorder.Code)
	}
	if got := recorder.Body.String(); got != "exe-bytes" {
		t.Fatalf("响应内容 %q", got)
	}
	if got := recorder.Header().Get("Content-Type"); got != "application/octet-stream" {
		t.Fatalf("Content-Type = %q", got)
	}
	if got := recorder.Header().Get("Content-Disposition"); !strings.Contains(got, "strategy-notify.exe") {
		t.Fatalf("Content-Disposition = %q", got)
	}
}

func TestHandleNotifyDownloadMissing(t *testing.T) {
	old := notifierCandidates
	notifierCandidates = []string{filepath.Join(t.TempDir(), "missing.exe")}
	t.Cleanup(func() { notifierCandidates = old })

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/notify/tool", nil)
	(&Server{}).handleNotifyDownload(recorder, request)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("状态码 %d，期望 404", recorder.Code)
	}
}
