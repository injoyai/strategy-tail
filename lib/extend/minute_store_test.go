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

	"github.com/injoyai/tdx/v2/lib/xorms"
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
	p := &PullKline{Config: PullKlineConfig{Dir: dir}}
	start, end := time.Date(year, 1, 1, 0, 0, 0, 0, time.Local), time.Date(year, 12, 31, 23, 59, 0, 0, time.Local)
	want, err := p.MinKlines(code, start, end)
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
		if k.Time.Year() == year {
			oracle = append(oracle, k)
		}
	}
	oracle.Sort()
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
	got, err := p.MinKlines(code, start, end)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("bars changed:\nwant=%+v\ngot=%+v", want, got)
	}
	result, err = CompressMinuteDatabase(path)
	if err != nil || result.Changed {
		t.Fatalf("resume=%+v err=%v", result, err)
	}
	// Refresh the final bar and append another day through the existing updater.
	last := ks[4].Time
	_, err = p.updateMinuteKlineYear(nil, code, year, protocol.Klines{{Time: last, Close: 0.444}, {Time: last.Add(time.Minute), Close: 0.445}})
	if err != nil {
		t.Fatal(err)
	}
	got, err = p.MinKlines(code, start, end)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(want)+1 || got[len(got)-2].Close != 0.444 || got[len(got)-1].Close != 0.445 {
		t.Fatalf("update changed boundary: %+v", got)
	}
	_, err = p.updateMinuteKlineYear(nil, code, year, protocol.Klines{{Time: last.Add(time.Minute), Close: 0.445}})
	if err != nil {
		t.Fatal(err)
	}
	if again, err := p.MinKlines(code, start, end); err != nil || !reflect.DeepEqual(got, again) {
		t.Fatalf("repeat update changed bars: %v", err)
	}
	_, err = p.updateMinuteKlineYear(nil, code, year, protocol.Klines{{Time: last.Add(time.Minute + time.Nanosecond), Close: 0.999}})
	if err == nil {
		t.Fatal("fractional update accepted")
	}
	if again, err := p.MinKlines(code, start, end); err != nil || !reflect.DeepEqual(got, again) {
		t.Fatalf("failed transaction changed records: %v", err)
	}
	if _, err = p.updateMinuteKlineYear(emptyKlineManager(t), code, year, nil); err != nil {
		t.Fatal(err)
	}
	if again, err := p.MinKlines(code, start, end); err != nil || !reflect.DeepEqual(got, again) {
		t.Fatalf("empty response changed compressed data: %v", err)
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
	p := &PullKline{Config: PullKlineConfig{Dir: dir}}
	if _, err = p.MinKlines(code, time.Date(2024, 1, 1, 0, 0, 0, 0, time.Local), time.Date(2024, 12, 31, 0, 0, 0, 0, time.Local)); err == nil {
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
	p := &PullKline{Config: PullKlineConfig{Dir: dir}}
	var wg sync.WaitGroup
	results := make(chan error, 3)
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 30; j++ {
				got, err := p.MinKlines(code, at, at)
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
