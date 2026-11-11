package main

import (
	"crypto/sha256"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/injoyai/strategy-tail/internal/marketstore"
	"github.com/injoyai/tdx/v2/extend/model"
	"github.com/injoyai/tdx/v2/protocol"
)

func TestDayImportYearFloorPreservesExistingAndNewerTail(t *testing.T) {
	dsn := os.Getenv("PULL_MYSQL_DSN")
	if dsn == "" {
		t.Skip("PULL_MYSQL_DSN 未设置")
	}
	dst, err := marketstore.NewMySQLStore(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer dst.Close()
	file := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite", file)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE Kline(Unix INTEGER PRIMARY KEY,Last INTEGER,Open INTEGER,High INTEGER,Low INTEGER,Close INTEGER,"Order" INTEGER,Volume INTEGER,Amount INTEGER,Time DATETIME,UpCount INTEGER,DownCount INTEGER,Turnover REAL,FloatStock INTEGER,TotalStock INTEGER)`)
	if err != nil {
		t.Fatal(err)
	}
	for _, date := range []string{"1999-12-31", "2000-01-04", "2000-01-05", "2026-11-05"} {
		at, _ := time.ParseInLocation(time.DateOnly, date, protocol.Location())
		if _, err = db.Exec(`INSERT INTO Kline VALUES(?,10000,10000,12000,9000,11000,0,123,4567000,?,0,0,0.29,1000,2000)`, at.Unix(), date+" 00:00:00"); err != nil {
			t.Fatal(err)
		}
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(file)
	code := model.Code{Market: protocol.MarketAStock, Code: fmt.Sprintf("day-audit-%d", time.Now().UnixNano())}
	prior := []*model.KlineDay{{Time: "2000-01-04", Close: 99}, {Time: "2026-11-06", Close: 88}}
	if err = dst.SaveDay(code, "", prior); err != nil {
		t.Fatal(err)
	}
	n, skipped, err := migrateLegacyDay(dst, job{file: file, code: code}, 2000)
	if err != nil || n != 2 || skipped {
		t.Fatal(n, skipped, err)
	}
	got, err := dst.QueryDayRecords(code, "", "")
	if err != nil || len(got) != 4 || !reflect.DeepEqual(&got[0].KlineDay, prior[0]) || !reflect.DeepEqual(&got[3].KlineDay, prior[1]) {
		t.Fatal("year floor or existing tail changed", got, err)
	}
	amount := 4567.0
	want := &marketstore.DayRecord{KlineDay: model.KlineDay{Time: "2000-01-05", Open: 10, High: 12, Low: 9, Close: 11, Volume: 12300, Amount: &amount}, Turnover: 0.29, FloatStock: 1000, TotalStock: 2000}
	if !reflect.DeepEqual(got[1], want) || got[2].Time != "2026-11-05" {
		t.Fatal("fields or latest data lost", got)
	}
	n, skipped, err = migrateLegacyDay(dst, job{file: file, code: code}, 2000)
	if err != nil || n != 0 || !skipped {
		t.Fatal("repeated import added rows", n, skipped, err)
	}
	after, _ := os.ReadFile(file)
	if sha256.Sum256(before) != sha256.Sum256(after) {
		t.Fatal("legacy source changed")
	}
}
