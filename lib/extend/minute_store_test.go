package extend

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/injoyai/strategy-tail/internal/xorms"
	"github.com/injoyai/tdx/v2/protocol"
)

func legacyMinuteFixture(t *testing.T, path string, ks protocol.Klines) {
	t.Helper()
	db, err := xorms.NewSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = db.Sync2(new(protocol.Kline)); err != nil {
		t.Fatal(err)
	}
	for _, k := range ks {
		if _, err = db.Insert(k); err != nil {
			t.Fatal(err)
		}
	}
}

func TestMinuteCompressionRoundTripAndResume(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	year := time.Now().Year()
	dir := t.TempDir()
	code := "sh600000"
	path := filepath.Join(dir, DirMinute, code, code+"-"+time.Date(year, 1, 1, 0, 0, 0, 0, loc).Format("2006")+".db")
	at := time.Date(year, 1, 5, 0, 1, 0, 0, loc)
	ks := protocol.Klines{
		{Time: at.Add(10 * time.Hour), Close: 0.125, Amount: 9.007199254740992e+12, Volume: 7000000000},
		{Time: at, Close: 0.111, UpCount: 7, DownCount: 8},
		{Time: at.Add(10 * time.Hour), Close: 0.126},
		{Time: at.AddDate(-2, 0, 0), Close: 0.222},
		{Time: at.AddDate(0, 0, 1), Close: 0.333},
	}
	legacyMinuteFixture(t, path, ks)
	want, err := readMinuteFile(path)
	if err != nil {
		t.Fatal(err)
	}
	oracleDB, err := xorms.NewSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	var original protocol.Klines
	if err = oracleDB.Find(&original); err != nil {
		t.Fatal(err)
	}
	oracleDB.Close()
	oracle := protocol.Klines{}
	for _, k := range original {
		k.Time = k.Time.In(protocol.Location())
		oracle = append(oracle, k)
	}
	oracle.Sort()
	want.Sort()
	if !reflect.DeepEqual(want, oracle) {
		t.Fatal("legacy reader differs from original xorm reader")
	}
	result, err := CompressMinuteDatabase(path)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Changed || result.Records != len(ks) {
		t.Fatalf("result: %+v", result)
	}
	got, err := readMinuteFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got.Sort()
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("bars changed:\nwant=%+v\ngot=%+v", want, got)
	}
	result, err = CompressMinuteDatabase(path)
	if err != nil || result.Changed {
		t.Fatalf("resume=%+v err=%v", result, err)
	}

}

func TestMinuteCompressionRefusesUnexpectedSchemaAndPrecision(t *testing.T) {
	for _, mode := range []string{"extra-table", "fractional-time"} {
		t.Run(mode, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "test.db")
			at := time.Date(2024, 1, 2, 10, 0, 0, 0, time.Local)
			legacyMinuteFixture(t, path, protocol.Klines{{Time: at, Close: 0.123}})
			db, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			query := "CREATE TABLE KeepMe (Value INTEGER)"
			if mode == "fractional-time" {
				query = "UPDATE Kline SET Time='2024-01-02 02:00:00.123456789'"
			}
			if _, err = db.Exec(query); err != nil {
				t.Fatal(err)
			}
			db.Close()
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = CompressMinuteDatabase(path); err == nil {
				t.Fatal("unsafe conversion accepted")
			}
			after, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(before, after) {
				t.Fatal("original changed after rejected conversion")
			}
		})
	}
}

func TestMinuteCompressedReadFailsOnCorruption(t *testing.T) {
	dir := t.TempDir()
	code := "sh600000"
	path := filepath.Join(dir, DirMinute, code, code+"-2024.db")
	legacyMinuteFixture(t, path, protocol.Klines{{Time: time.Date(2024, 1, 2, 10, 0, 0, 0, time.Local), Close: 0.123}})
	if _, err := CompressMinuteDatabase(path); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("UPDATE KlineBlock SET Data=x'010203'"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if _, err = readMinuteFile(path); err == nil {
		t.Fatal("corruption silently ignored")
	}
}

func TestMinuteReadersDuringAtomicCompression(t *testing.T) {
	dir := t.TempDir()
	code := "sh600000"
	path := filepath.Join(dir, DirMinute, code, code+"-2024.db")
	at := time.Date(2024, 1, 2, 10, 0, 0, 0, time.Local)
	var bars protocol.Klines
	for i := 0; i < 100; i++ {
		bars = append(bars, &protocol.Kline{Time: at.Add(time.Duration(i) * time.Minute), Close: float64(i+1) / 1000.0})
	}
	legacyMinuteFixture(t, path, bars)
	for _, k := range bars {
		k.Time = k.Time.In(protocol.Location())
	}
	var wg sync.WaitGroup
	results := make(chan error, 3)
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 30; j++ {
				got, err := readMinuteFile(path)
				if err != nil {
					results <- err
					return
				}
				if !reflect.DeepEqual(got, bars) {
					results <- fmt.Errorf("atomic replacement changed read result")
					return
				}
			}
		}()
	}
	_, err := CompressMinuteDatabase(path)
	wg.Wait()
	close(results)
	if err != nil {
		t.Fatal(err)
	}
	for err := range results {
		t.Fatal(err)
	}
}
