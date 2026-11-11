package extend

import (
	"bytes"
	"compress/gzip"
	"database/sql"
	"encoding/binary"
	"math"
	"path/filepath"
	"testing"
	"time"

	"github.com/injoyai/strategy-tail/internal/xorms"
	"github.com/injoyai/tdx/v2/protocol"
)

func TestV2MinuteBlockPreservesFractionalAndNegativePrices(t *testing.T) {
	want := minuteRecord{Sequence: 7, Bar: &protocol.Kline{Last: -.00001, Open: 12.345678, High: 13.000001, Low: -1.000002, Close: 12.345679, Amount: 123456.123456, Volume: 100, Time: time.Unix(1704187860, 0)}}
	var w *gzip.Writer
	data, e := encodeMinuteBlock([]minuteRecord{want}, &w)
	if e != nil {
		t.Fatal(e)
	}
	var r *gzip.Reader
	got, e := decodeMinuteBlock(data, &r)
	if e != nil {
		t.Fatal(e)
	}
	if len(got) != 1 || got[0].Sequence != want.Sequence {
		t.Fatal(got)
	}
	if !got[0].Bar.Time.Equal(want.Bar.Time) {
		t.Fatal("time changed")
	}
	actual, expected := *got[0].Bar, *want.Bar
	actual.Time = time.Time{}
	expected.Time = time.Time{}
	if actual != expected {
		t.Fatalf("record lost precision: %+v != %+v", actual, expected)
	}

}

func TestV2LegacyIntegerScaleIsReadWithoutMutation(t *testing.T) {
	db, e := sql.Open("sqlite", ":memory:")
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, e = db.Exec("CREATE TABLE Kline(Close BIGINT)"); e != nil {
		t.Fatal(e)
	}
	legacy, e := legacyPriceScale(db)
	if e != nil || !legacy {
		t.Fatal(legacy, e)
	}
	k := &protocol.Kline{Close: 12345, Amount: 123456789}
	scaleBar(k, .001)
	if k.Close != 12.345 || k.Amount != 123456.789 {
		t.Fatalf("wrong price scale: %+v", k)
	}

}

func TestV2LegacyMinuteBlockDecodesMilliPrices(t *testing.T) {
	raw := []byte{'K', 'L', 'M', '1', 96, 0, 0, 0}
	for _, v := range []int64{1, 10000, 10001, 11000, 9000, 10555, 2, 300, 1234567, 1704187860, 0, 0} {
		raw = binary.LittleEndian.AppendUint64(raw, uint64(v))
	}
	var b bytes.Buffer
	w := gzip.NewWriter(&b)
	w.Write(raw)
	w.Close()
	var r *gzip.Reader
	got, e := decodeMinuteBlock(b.Bytes(), &r)
	if e != nil || len(got) != 1 || got[0].Bar.Close != 10.555 || got[0].Bar.Amount != 1234.567 {
		t.Fatal(got, e)
	}
}

func TestV2MinuteRejectsNonFinitePrice(t *testing.T) {
	if _, e := appendMinuteRecord(nil, minuteRecord{Bar: &protocol.Kline{Time: time.Unix(0, 0), Close: math.NaN()}}); e == nil {
		t.Fatal("non-finite price accepted")
	}
}

func TestV2LegacyRawMinuteConversion(t *testing.T) {
	dir := t.TempDir()
	const code = "sh600000"
	db, e := xorms.NewSQLite(filepath.Join(dir, DirMinute, code, code+"-2026.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	_, e = db.Exec("CREATE TABLE Kline(Last BIGINT,Open BIGINT,High BIGINT,Low BIGINT,Close BIGINT,`Order` INTEGER,Volume BIGINT,Amount BIGINT,Time DATETIME,UpCount INTEGER,DownCount INTEGER)")
	if e != nil {
		t.Fatal(e)
	}
	at := time.Date(2026, 1, 5, 10, 0, 0, 0, time.Local)
	if _, e = db.Insert(&protocol.Kline{Time: at, Close: 12345, Amount: 1234567}); e != nil {
		t.Fatal(e)
	}
	got, e := readMinuteFile(filepath.Join(dir, DirMinute, code, code+"-2026.db"))
	if e != nil || len(got) != 1 || got[0].Close != 12.345 || got[0].Amount != 1234.567 {
		t.Fatal(got, e)
	}
	var storageType string
	if e = db.DB().DB.QueryRow("SELECT typeof(Close) FROM Kline").Scan(&storageType); e != nil || storageType != "integer" {
		t.Fatal(storageType, e)
	}
	var stored protocol.Klines
	if e = db.Find(&stored); e != nil {
		t.Fatal(e)
	}
	legacy, e := legacyPriceScale(db.DB().DB)
	if e != nil || !legacy || len(stored) != 1 || math.Abs(stored[0].Close-12345) > 1e-8 {
		t.Fatal(stored, legacy, e)
	}
}

// Existing KLM1 blocks remain readable without rewriting their version.
func TestV2LegacyCompressedReader(t *testing.T) {
	db, e := sql.Open("sqlite", ":memory:")
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	if e = initMinuteStore(db); e != nil {
		t.Fatal(e)
	}
	at := time.Date(2026, 1, 5, 10, 0, 0, 0, time.Local)
	for i := 0; i < 2; i++ {
		raw := []byte{'K', 'L', 'M', '1', 96, 0, 0, 0}
		for _, v := range []int64{int64(i + 1), 10000, 10001, 11000, 9000, 10555, 2, 300, 1234567, at.AddDate(0, 0, i).Unix(), 0, 0} {
			raw = binary.LittleEndian.AppendUint64(raw, uint64(v))
		}
		var b bytes.Buffer
		w := gzip.NewWriter(&b)
		if _, e = w.Write(raw); e != nil {
			t.Fatal(e)
		}
		if e = w.Close(); e != nil {
			t.Fatal(e)
		}
		if _, e = db.Exec("INSERT INTO KlineBlock VALUES(?,?)", minuteDay(at.AddDate(0, 0, i)), b.Bytes()); e != nil {
			t.Fatal(e)
		}
	}
	if _, e = db.Exec("UPDATE KlineMeta SET NextSequence=3,LastUnix=?,RecordCount=2 WHERE ID=1", at.AddDate(0, 0, 1).Unix()); e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec("PRAGMA user_version=101"); e != nil {
		t.Fatal(e)
	}
	version, e := minuteVersion(db)
	if e != nil || version != 101 {
		t.Fatal(version, e)
	}
	got, e := readMinuteRecords(db, version)
	if e != nil || len(got) != 2 || got[0].Bar.Close != 10.555 || got[1].Bar.Close != 10.555 {
		t.Fatal(got, e)
	}
}
