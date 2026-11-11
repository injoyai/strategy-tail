package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/injoyai/strategy-tail/internal/marketstore"
	"github.com/injoyai/tdx/v2/extend/model"
	"github.com/injoyai/tdx/v2/protocol"
)

type failStage struct{ stage string }

func (w failStage) Write(p []byte) (int, error) {
	if bytes.Contains(p, []byte(`"stage":"`+w.stage+`"`)) {
		return 0, errors.New("injected interruption")
	}
	return len(p), nil
}

func TestInplaceMigrationResumes(t *testing.T) {
	for _, stage := range []string{"before-alter", "altered"} {
		t.Run(stage, func(t *testing.T) {
			db, dsn := testDB(t)
			ctx := context.Background()
			dir := t.TempDir()
			opt := options{Apply: true, Paused: true, InPlace: true, StateDir: dir}
			err := migrate(ctx, db, opt, json.NewEncoder(failStage{stage}))
			if err == nil || !strings.Contains(err.Error(), "injected interruption") {
				t.Fatalf("expected stage interruption, got %v", err)
			}
			var rows int
			if err := db.QueryRow("SELECT COUNT(*) FROM cn_index_kline_day").Scan(&rows); err != nil || rows != 1 {
				t.Fatalf("index lost after interruption: %d %v", rows, err)
			}
			if s, err := marketstore.NewMySQLStore(dsn); err == nil {
				s.Close()
				t.Fatal("opened incomplete market schema")
			}
			if err := migrate(ctx, db, opt, json.NewEncoder(io.Discard)); err != nil {
				t.Fatal(err)
			}
			s, err := marketstore.NewMySQLStore(dsn)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			code := model.Code{Market: protocol.MarketAStock, Code: "sh000001"}
			got, err := s.QueryMinute(code, "", "")
			if err != nil || len(got) != 2 || got[0].Source != "trade" || got[0].Volume != 9876543210 || got[0].Close != 1.123456789 {
				t.Fatalf("stock data changed %+v %v", got, err)
			}
			code.Market = protocol.MarketIndex
			got, err = s.QueryMinute(code, "", "")
			if err != nil || len(got) != 1 || got[0].Close != 7 {
				t.Fatalf("index data changed %+v %v", got, err)
			}
			state, err := readState(filepath.Join(dir, "kline_minute_2025.json"))
			if err != nil || !state.Complete || state.StockDigest.Rows != 2 || state.IndexDigest.Rows != 1 {
				t.Fatalf("bad checkpoint %+v %v", state, err)
			}
			var legacy int
			if err := db.QueryRow("SELECT COUNT(*) FROM information_schema.TABLES WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME IN ('kline_day','kline_minute_2025')").Scan(&legacy); err != nil || legacy != 0 {
				t.Fatal("legacy table not renamed", err)
			}
		})
	}
}
func TestInplaceRejectsChangedCheckpoint(t *testing.T) {
	db, _ := testDB(t)
	ctx := context.Background()
	dir := t.TempDir()
	opt := options{Apply: true, Paused: true, InPlace: true, StateDir: dir}
	if err := migrate(ctx, db, opt, json.NewEncoder(failStage{"before-alter"})); err == nil {
		t.Fatal("expected interruption")
	}
	if _, err := db.Exec("UPDATE kline_day SET close=44 WHERE market='cn/stock'"); err != nil {
		t.Fatal(err)
	}
	if err := migrate(ctx, db, opt, json.NewEncoder(io.Discard)); err == nil || !strings.Contains(err.Error(), "checkpoint differs") {
		t.Fatalf("accepted changed stock baseline: %v", err)
	}
}
func TestInplaceRequiresPausedState(t *testing.T) {
	db, _ := testDB(t)
	if err := migrate(context.Background(), db, options{Apply: true, InPlace: true, StateDir: t.TempDir()}, json.NewEncoder(io.Discard)); err == nil {
		t.Fatal("accepted missing pause")
	}
	if err := verifyBackup(filepath.Join(t.TempDir(), "missing"), "x"); err == nil {
		t.Fatal("accepted missing recovery proof")
	}
}
func TestPendingMarkerWithoutLegacy(t *testing.T) {
	db, dsn := testDB(t)
	for _, q := range []string{"DROP TABLE kline_day,kline_minute_2025", schemaDDL, "INSERT INTO kline_schema(version,ready) VALUES('market-v1',0)"} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	if s, err := marketstore.NewMySQLStore(dsn); err == nil {
		s.Close()
		t.Fatal("opened schema with pending marker and no legacy tables")
	}
}
func TestStateWriteIsDurableAndRoundTrips(t *testing.T) {
	p := filepath.Join(t.TempDir(), "state.json")
	want := inplaceState{Source: "kline_day", StockDigest: digest{1, "abc"}}
	if err := saveJSON(p, want); err != nil {
		t.Fatal(err)
	}
	got, err := readState(p)
	if err != nil || got != want {
		t.Fatalf("%+v %v", got, err)
	}
	if s, err := os.Stat(p); err != nil || s.Mode().Perm()&0077 != 0 {
		t.Fatal("state is not private")
	}
}

func TestInplaceIndexConflictPreservesSource(t *testing.T) {
	db, _ := testDB(t)
	ctx := context.Background()
	j := job{Source: "kline_day", Target: "cn_index_kline_day", Columns: columns("kline_day")}
	if err := ensureTarget(ctx, db, j); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO cn_index_kline_day(code,time,close) VALUES('sh000001','2025-01-02',123)"); err != nil {
		t.Fatal(err)
	}
	err := migrate(ctx, db, options{Apply: true, Paused: true, InPlace: true, StateDir: t.TempDir()}, json.NewEncoder(io.Discard))
	if err == nil || !strings.Contains(err.Error(), "index conflict") {
		t.Fatalf("accepted conflicting target index: %v", err)
	}
	var rows int
	var close float64
	if err := db.QueryRow("SELECT COUNT(*),MAX(close) FROM kline_day WHERE market='cn/index'").Scan(&rows, &close); err != nil || rows != 1 || close != 9 {
		t.Fatalf("source index changed %d %g %v", rows, close, err)
	}
}
