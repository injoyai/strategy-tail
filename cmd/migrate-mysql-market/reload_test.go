package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/injoyai/strategy-tail/internal/marketstore"
	"github.com/injoyai/tdx/v2/extend/model"
	"github.com/injoyai/tdx/v2/protocol"
)

func reloadTarget(t *testing.T, dsn string) (*sql.DB, string) {
	t.Helper()
	c, e := mysql.ParseDSN(dsn)
	if e != nil {
		t.Fatal("bad test DSN")
	}
	name := c.DBName + "_reload_target"
	c.DBName = ""
	admin, e := sql.Open("mysql", c.FormatDSN())
	if e != nil {
		t.Fatal(e)
	}
	if _, e = admin.Exec("DROP DATABASE IF EXISTS `" + name + "`"); e != nil {
		t.Fatal(e)
	}
	if _, e = admin.Exec("CREATE DATABASE `" + name + "`"); e != nil {
		t.Fatal(e)
	}
	c.DBName = name
	targetDSN := c.FormatDSN()
	db, e := sql.Open("mysql", targetDSN)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { db.Close(); admin.Exec("DROP DATABASE `" + name + "`"); admin.Close() })
	return db, targetDSN
}
func reloadAuxFixture(t *testing.T, src *sql.DB) {
	t.Helper()
	for _, q := range []string{
		"CREATE TABLE minute_coverage(market VARCHAR(16) NOT NULL,code VARCHAR(32) NOT NULL,year INT NOT NULL,from_time DATETIME NOT NULL,through_time DATETIME NOT NULL,trade_fallback TINYINT NOT NULL,PRIMARY KEY(market,code,year))",
		"CREATE TABLE minute_empty_scan(market VARCHAR(16) NOT NULL,code VARCHAR(32) NOT NULL,year INT NOT NULL,from_time DATETIME NOT NULL,checked_at BIGINT NOT NULL,PRIMARY KEY(market,code,year))",
		"CREATE TABLE update_marks(mark_key VARCHAR(255) NOT NULL,time BIGINT NOT NULL,PRIMARY KEY(mark_key))",
		"INSERT INTO minute_coverage VALUES('cn/stock','sh000001',2017,'2017-01-01','2017-12-31',0),('cn/stock','sh000001',2025,'2025-01-01','2025-12-31',1)",
		"INSERT INTO minute_empty_scan VALUES('cn/stock','sh000001',2017,'2017-01-01',123),('cn/stock','sh000001',2025,'2025-01-01',456)",
		"INSERT INTO update_marks VALUES('dataset-key',123456)",
		"INSERT INTO kline_day(market,code,time,close) VALUES('cn/stock','retired','2017-01-02',88)",
		"CREATE TABLE kline_minute_2017 LIKE kline_minute_2025",
	} {
		if _, e := src.Exec(q); e != nil {
			t.Fatal(e)
		}
	}
}
func TestReloadFrom2018AndResume(t *testing.T) {
	src, dsn := testDB(t)
	dst, targetDSN := reloadTarget(t, dsn)
	reloadAuxFixture(t, src)
	ctx := context.Background()
	opt := options{Apply: true, Paused: true, StateDir: t.TempDir()}
	before, e := reloadDigest(ctx, src, "SELECT "+reloadFields(columns("kline_day"))+" FROM kline_day ORDER BY market,code,time")
	if e != nil {
		t.Fatal(e)
	}
	if e = reloadInto(ctx, src, dst, opt, 2018, "fixture-uuid", "fixture-cold", json.NewEncoder(failStage{"verified-job"})); e == nil {
		t.Fatal("expected injected stop")
	}
	if s, e := marketstore.NewMySQLStore(targetDSN); e == nil {
		s.Close()
		t.Fatal("opened partially loaded target")
	}
	if e = reloadInto(ctx, src, dst, opt, 2018, "fixture-uuid", "different-cold", json.NewEncoder(io.Discard)); e == nil {
		t.Fatal("accepted different cold source")
	}
	var log bytes.Buffer
	if e = reloadInto(ctx, src, dst, opt, 2018, "fixture-uuid", "fixture-cold", json.NewEncoder(&log)); e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(log.String(), `"complete":true`) {
		t.Fatal("missing complete summary")
	}
	after, e := reloadDigest(ctx, src, "SELECT "+reloadFields(columns("kline_day"))+" FROM kline_day ORDER BY market,code,time")
	if e != nil || after != before {
		t.Fatal("source changed", e)
	}
	var count int
	for _, q := range []string{"SELECT COUNT(*) FROM cn_stock_kline_day WHERE time<'2018-01-01'", "SELECT COUNT(*) FROM minute_coverage WHERE year<2018", "SELECT COUNT(*) FROM minute_empty_scan WHERE year<2018", "SELECT COUNT(*) FROM information_schema.TABLES WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME LIKE '%2017'"} {
		if e = dst.QueryRow(q).Scan(&count); e != nil || count != 0 {
			t.Fatalf("old data retained for %s: %d %v", q, count, e)
		}
	}
	if e = dst.QueryRow("SELECT COUNT(*) FROM minute_coverage").Scan(&count); e != nil || count != 1 {
		t.Fatal("aux missing", count, e)
	}
	store, e := marketstore.NewMySQLStore(targetDSN)
	if e != nil {
		t.Fatal(e)
	}
	defer store.Close()
	rows, e := store.QueryMinute(model.Code{Market: protocol.MarketAStock, Code: "sh000001"}, "", "")
	if e != nil || len(rows) != 2 || rows[0].Volume != 9876543210 || rows[0].Close != 1.123456789 {
		t.Fatalf("field loss %+v %v", rows, e)
	}
	if e = reloadInto(ctx, src, dst, opt, 2018, "fixture-uuid", "fixture-cold", json.NewEncoder(io.Discard)); e == nil {
		t.Fatal("replayed complete source")
	}
}
func TestReloadBatchesAndConflicts(t *testing.T) {
	src, dsn := testDB(t)
	dst, _ := reloadTarget(t, dsn)
	ctx := context.Background()
	j := job{Source: "kline_minute_2025", Target: "cn_stock_kline_minute_2025", Market: "cn/stock", Code: "sh000001", Columns: columns("kline_minute_2025")}
	if e := reloadCreate(ctx, src, dst, j.Source, j.Target); e != nil {
		t.Fatal(e)
	}
	if e := ensureTarget(ctx, dst, j); e != nil {
		t.Fatal(e)
	}
	var vals []string
	var args []any
	base := time.Date(2025, 1, 3, 9, 30, 0, 0, time.UTC)
	for i := 0; i < 2100; i++ {
		vals = append(vals, "(?,?,?,?,?,?)")
		args = append(args, "cn/stock", "sh000001", base.Add(time.Duration(i)*time.Minute).Format("2006-01-02 15:04:05"), 1.2345678901234567, int64(9000000000+i), "q\\'\n")
	}
	if _, e := src.Exec("INSERT INTO kline_minute_2025(market,code,time,close,volume,source) VALUES "+strings.Join(vals, ","), args...); e != nil {
		t.Fatal(e)
	}
	d, e := reloadJob(ctx, src, dst, j, 2018)
	if e != nil || d.Rows != 2102 {
		t.Fatal("batch copy failed", d, e)
	}
	again, e := reloadJob(ctx, src, dst, j, 2018)
	if e != nil || again != d {
		t.Fatal("replay differs", again, e)
	}
	if _, e = dst.Exec("UPDATE cn_stock_kline_minute_2025 SET close=999 WHERE time='2025-01-02 09:31'"); e != nil {
		t.Fatal(e)
	}
	if _, e = reloadJob(ctx, src, dst, j, 2018); e == nil {
		t.Fatal("overwrote target conflict")
	}
	var close float64
	if e = dst.QueryRow("SELECT close FROM cn_stock_kline_minute_2025 WHERE time='2025-01-02 09:31'").Scan(&close); e != nil || close != 999 {
		t.Fatal("changed conflicting value", close, e)
	}
}
func TestReloadRejectsNonemptyUnownedTarget(t *testing.T) {
	src, dsn := testDB(t)
	dst, _ := reloadTarget(t, dsn)
	reloadAuxFixture(t, src)
	if _, e := dst.Exec("CREATE TABLE unrelated(id INT PRIMARY KEY)"); e != nil {
		t.Fatal(e)
	}
	e := reloadInto(context.Background(), src, dst, options{StateDir: t.TempDir()}, 2018, "uuid", "cold", json.NewEncoder(io.Discard))
	if e == nil || !strings.Contains(e.Error(), "must be empty") {
		t.Fatal("accepted populated unowned target", e)
	}
}
func TestReloadPreconditions(t *testing.T) {
	e := runReload(context.Background(), reloadConnections{}, options{}, 2018, "uuid", json.NewEncoder(os.Stdout))
	if e == nil {
		t.Fatal("accepted missing approvals")
	}
}
