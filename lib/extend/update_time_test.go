package extend

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/injoyai/tdx"
	"github.com/injoyai/tdx/lib/xorms"
)

func TestLastSuccessfulUpdate(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "行情 #数据")
	pull := &PullKline{Config: PullKlineConfig{Dir: dir}}
	last, err := pull.LastSuccessfulUpdate()
	if err != nil || !last.IsZero() {
		t.Fatalf("missing database: %v, %v", last, err)
	}
	filename := filepath.Join(dir, "update.db")
	if _, err := os.Stat(filename); !os.IsNotExist(err) {
		t.Fatal("status read created a database")
	}
	db, err := xorms.NewSqlite(filename)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Sync2(new(tdx.UpdateModel)); err != nil {
		t.Fatal(err)
	}
	for _, timestamp := range []int64{-1, 0, time.Date(2026, 10, 12, 16, 0, 0, 0, time.FixedZone("CST", 8*3600)).Unix()} {
		if _, err := db.Exec(`DELETE FROM "update"`); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Insert(&tdx.UpdateModel{Key: "pull", Time: timestamp}); err != nil {
			t.Fatal(err)
		}
		last, err := pull.LastSuccessfulUpdate()
		if err != nil || (timestamp <= 0 && !last.IsZero()) || (timestamp > 0 && last.Unix() != timestamp) {
			t.Fatalf("timestamp=%d last=%v err=%v", timestamp, last, err)
		}
	}
	if _, err := db.Exec(`DELETE FROM "update"`); err != nil {
		t.Fatal(err)
	}
	last, err = pull.LastSuccessfulUpdate()
	if err != nil || !last.IsZero() {
		t.Fatalf("missing marker: %v, %v", last, err)
	}
	db.Close()
	before, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pull.LastSuccessfulUpdate(); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(filename)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("status read changed the database")
	}
	if err := os.WriteFile(filename, []byte("invalid database"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := pull.LastSuccessfulUpdate(); err == nil {
		t.Fatal("unreadable marker reported as no success record")
	}
}
