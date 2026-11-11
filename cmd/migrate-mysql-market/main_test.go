package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/injoyai/strategy-tail/internal/marketstore"
	"github.com/injoyai/tdx/v2/extend/model"
	"github.com/injoyai/tdx/v2/protocol"
)

func testDB(t *testing.T) (*sql.DB, string) {
	t.Helper()
	dsn := os.Getenv("MIGRATE_MYSQL_TEST_DSN")
	if dsn == "" {
		t.Skip("set MIGRATE_MYSQL_TEST_DSN to an isolated split_migration_* database")
	}
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		t.Fatal("invalid test DSN")
	}
	if !strings.HasPrefix(cfg.DBName, "split_migration_") || strings.ContainsAny(cfg.DBName, "`\\'\"") {
		t.Fatal("test database must have split_migration_ prefix")
	}
	name := cfg.DBName
	cfg.DBName = ""
	admin, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	if _, err := admin.Exec("DROP DATABASE IF EXISTS `" + name + "`"); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec("CREATE DATABASE `" + name + "`"); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(5)
	t.Cleanup(func() { db.Close() })
	for _, q := range []string{
		"CREATE TABLE kline_day (market VARCHAR(16) NOT NULL,code VARCHAR(32) NOT NULL,time DATE NOT NULL,open DOUBLE NOT NULL DEFAULT 0,high DOUBLE NOT NULL DEFAULT 0,low DOUBLE NOT NULL DEFAULT 0,close DOUBLE NOT NULL DEFAULT 0,volume BIGINT NOT NULL DEFAULT 0,amount DOUBLE NOT NULL DEFAULT 0,turnover DOUBLE NOT NULL DEFAULT 0,float_stock DOUBLE NOT NULL DEFAULT 0,total_stock DOUBLE NOT NULL DEFAULT 0,PRIMARY KEY(market,code,time)) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci",
		"CREATE TABLE kline_minute_2025 (market VARCHAR(16) NOT NULL,code VARCHAR(32) NOT NULL,time DATETIME NOT NULL,open DOUBLE NOT NULL DEFAULT 0,high DOUBLE NOT NULL DEFAULT 0,low DOUBLE NOT NULL DEFAULT 0,close DOUBLE NOT NULL DEFAULT 0,volume BIGINT NOT NULL DEFAULT 0,amount DOUBLE NOT NULL DEFAULT 0,source VARCHAR(8) NOT NULL DEFAULT '',PRIMARY KEY(market,code,time)) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci",
		"INSERT INTO kline_day(market,code,time,close,turnover,float_stock,total_stock) VALUES ('cn/stock','sh000001','2025-01-02',3,1.25,1234,5678),('cn/index','sh000001','2025-01-02',9,0,0,0)",
		"INSERT INTO kline_minute_2025(market,code,time,open,high,low,close,volume,amount,source) VALUES ('cn/stock','sh000001','2025-01-02 09:31',1,2,0.5,1.123456789,9876543210,123456.789,'trade'),('cn/stock','sh000001','2025-01-02 09:32',2,3,1,2,0,0,''),('cn/index','sh000001','2025-01-02 09:31',7,8,6,7,100,700,'')",
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	return db, dsn
}
func TestTargetNames(t *testing.T) {
	for source, want := range map[string]string{"kline_day": "cn_stock_kline_day", "kline_minute_2025": "cn_stock_kline_minute_2025"} {
		got, err := targetTable(source, "cn/stock")
		if err != nil || got != want {
			t.Fatalf("%q %v", got, err)
		}
	}
	if _, err := targetTable("kline_minute_2025`", "cn/stock"); err == nil {
		t.Fatal("accepted SQL identifier injection")
	}
	if err := freeSpace(t.TempDir(), ^uint64(0)); err == nil {
		t.Fatal("space reserve ignored")
	}
}

type inspectLock struct {
	t       *testing.T
	db      *sql.DB
	checked bool
}

func (w *inspectLock) Write(b []byte) (int, error) {
	if !w.checked && bytes.Contains(b, []byte(`"verified":true`)) {
		w.checked = true
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		defer cancel()
		if _, err := w.db.ExecContext(ctx, "UPDATE kline_day SET close=100 WHERE market='cn/stock'"); err == nil {
			w.t.Error("legacy source writer was not blocked")
		}
	}
	return len(b), nil
}
func TestMigrationAndReadiness(t *testing.T) {
	db, dsn := testDB(t)
	ctx := context.Background()
	var b bytes.Buffer
	if err := migrate(ctx, db, options{}, json.NewEncoder(&b)); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM information_schema.TABLES WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='cn_stock_kline_day'").Scan(&count); err != nil || count != 0 {
		t.Fatal("plan mutated database", err)
	}
	if s, err := marketstore.NewMySQLStore(dsn); err == nil {
		s.Close()
		t.Fatal("opened unmigrated legacy database")
	}
	if err := migrate(ctx, db, options{Apply: true}, json.NewEncoder(io.Discard)); err == nil {
		t.Fatal("accepted missing write pause")
	}
	lock := &inspectLock{t: t, db: db}
	if err := migrate(ctx, db, options{Apply: true, Paused: true}, json.NewEncoder(lock)); err != nil {
		t.Fatal(err)
	}
	if !lock.checked {
		t.Fatal("no verified receipt")
	}
	store, err := marketstore.NewMySQLStore(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	a, err := store.QueryMinute(model.Code{Market: protocol.MarketAStock, Code: "sh000001"}, "", "")
	if err != nil || len(a) != 2 || a[0].Close != 1.123456789 || a[0].Volume != 9876543210 || a[0].Source != "trade" {
		t.Fatalf("minute fields changed: %+v %v", a, err)
	}
	index, err := store.QueryMinute(model.Code{Market: protocol.MarketIndex, Code: "sh000001"}, "", "")
	if err != nil || len(index) != 1 || index[0].Close != 7 {
		t.Fatalf("market mixed: %+v %v", index, err)
	}
	day, err := store.QueryDayRecords(model.Code{Market: protocol.MarketAStock, Code: "sh000001"}, "", "")
	if err != nil || len(day) != 1 || day[0].Close != 3 || day[0].FloatStock != 1234 || day[0].Turnover != 1.25 {
		t.Fatalf("day changed: %+v %v", day, err)
	}
	j := job{"kline_minute_2025", "cn_stock_kline_minute_2025", "cn/stock", "sh000001", columns("kline_minute_2025")}
	n, added, err := copyJob(ctx, db, j)
	if err != nil || n != 2 || added != 0 {
		t.Fatalf("repeat duplicated: %d %d %v", n, added, err)
	}
	if _, err := db.Exec("SELECT * FROM kline_day LIMIT 1"); err != nil {
		t.Fatal("legacy table lost", err)
	}
	if err := migrate(ctx, db, options{Apply: true, Paused: true}, json.NewEncoder(io.Discard)); err == nil {
		t.Fatal("replayed legacy after readiness")
	}
}
func TestMismatchRollsBackAndPreservesExisting(t *testing.T) {
	db, _ := testDB(t)
	ctx := context.Background()
	j := job{"kline_minute_2025", "cn_stock_kline_minute_2025", "cn/stock", "sh000001", columns("kline_minute_2025")}
	if err := ensureTarget(ctx, db, j); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO cn_stock_kline_minute_2025(code,time,close) VALUES('sh000001','2025-01-02 09:31',99)"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := copyJob(ctx, db, j); err == nil {
		t.Fatal("did not reject conflicting existing value")
	}
	var n int
	var close float64
	if err := db.QueryRow("SELECT COUNT(*),MAX(close) FROM cn_stock_kline_minute_2025").Scan(&n, &close); err != nil || n != 1 || close != 99 {
		t.Fatalf("rollback changed existing target %d %g %v", n, close, err)
	}
	if _, err := db.Exec("DELETE FROM cn_stock_kline_minute_2025"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := copyJob(ctx, db, j); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO cn_stock_kline_minute_2025(code,time) VALUES('rogue','2025-01-01')"); err != nil {
		t.Fatal(err)
	}
	if err := verifyCodes(ctx, db, []job{j}); err == nil {
		t.Fatal("accepted unexpected target code")
	}
}
func TestCompareIncludesAllFields(t *testing.T) {
	got := columns("kline_day")
	want := []string{"code", "time", "open", "high", "low", "close", "volume", "amount", "turnover", "float_stock", "total_stock"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%v", got)
	}
}
