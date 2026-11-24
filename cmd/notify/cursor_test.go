package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/fasthttp/websocket"
)

func TestMigrateLegacyCursor(t *testing.T) {
	for _, saved := range []string{"http://a", ""} {
		t.Run(saved, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "cursor")
			if err := os.WriteFile(path, []byte("100\n"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := migrateCursor(path, saved, "http://b"); err != nil {
				t.Fatal(err)
			}
			owner := saved
			if owner == "" {
				owner = "http://b"
			}
			if id, ok := readServerCursor(path, owner); !ok || id != 100 {
				t.Fatalf("owner cursor=%d known=%v", id, ok)
			}
			if saved != "" {
				if id, ok := readServerCursor(path, "http://b"); ok || id != 0 {
					t.Fatalf("new server inherited cursor=%d", id)
				}
			}
			if err := writeServerCursor(path, "http://c", 1); err != nil {
				t.Fatal(err)
			}
			if err := migrateCursor(path, "http://c", "http://c"); err != nil {
				t.Fatal(err)
			}
			if id, ok := readServerCursor(path, owner); !ok || id != 100 {
				t.Fatal("repeat migration lost checkpoint")
			}
			if id, ok := readServerCursor(path, "http://c"); !ok || id != 1 {
				t.Fatal("restart lost other server checkpoint")
			}
		})
	}
}

func TestCursorNormalizesServerAddress(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cursor")
	if err := writeServerCursor(path, "127.0.0.1:8765/", 5); err != nil {
		t.Fatal(err)
	}
	if id, ok := readServerCursor(path, "ws://127.0.0.1:8765/api/screen/events"); !ok || id != 5 {
		t.Fatalf("cursor=%d known=%v", id, ok)
	}
}

func TestSwitchServerResumesItsOwnCursor(t *testing.T) {
	type connection struct{ server, after string }
	connections := make(chan connection, 8)
	makeServer := func(name string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			up := websocket.Upgrader{}
			conn, err := up.Upgrade(w, r, nil)
			if err != nil {
				return
			}
			defer conn.Close()
			after := r.URL.Query().Get("after")
			connections <- connection{name, after}
			id, _ := strconv.ParseInt(after, 10, 64)
			if err := conn.WriteJSON(saleEvent{Type: "sell", ID: id + 1}); err != nil {
				return
			}
			conn.ReadMessage()
		}))
	}
	a, b := makeServer("a"), makeServer("b")
	defer a.Close()
	defer b.Close()
	path := filepath.Join(t.TempDir(), "cursor")
	if err := writeServerCursor(path, a.URL, 100); err != nil {
		t.Fatal(err)
	}
	cfg := config{server: newServerStore(a.URL), statePath: path, retry: time.Millisecond, replay: true}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); run(ctx, cfg, a.URL, 100) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("connection loop did not stop")
		}
	})
	for _, step := range []struct {
		target, name, after string
		want                int64
	}{
		{a.URL, "a", "100", 101}, {b.URL, "b", "0", 1}, {a.URL, "a", "101", 102},
	} {
		cfg.server.Set(step.target)
		select {
		case got := <-connections:
			if got.server != step.name || got.after != step.after {
				t.Fatalf("connection=%+v want %s after %s", got, step.name, step.after)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("connection timed out")
		}
		deadline := time.Now().Add(5 * time.Second)
		for {
			if id, ok := readServerCursor(path, step.target); ok && id == step.want {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("event for %s was lost", step.name)
			}
			time.Sleep(time.Millisecond)
		}
	}
	if id, ok := readServerCursor(path, b.URL); !ok || id != 1 {
		t.Fatal("switching back overwrote the other checkpoint")
	}
}

func TestCorruptCursorRecoversAtNextEvent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cursor")
	if err := os.WriteFile(path, []byte("not-a-number"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := migrateCursor(path, "http://a", "http://a"); err != nil {
		t.Fatal(err)
	}
	if id, ok := readServerCursor(path, "http://a"); ok || id != 0 {
		t.Fatalf("cursor=%d known=%v", id, ok)
	}
	if err := writeServerCursor(path, "http://a", 1); err != nil {
		t.Fatal(err)
	}
	if id, ok := readServerCursor(path, "http://a"); !ok || id != 1 {
		t.Fatalf("cursor=%d known=%v", id, ok)
	}
}
