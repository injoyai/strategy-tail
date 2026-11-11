package marketstore

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/injoyai/strategy-tail/internal/xorms"
	"github.com/injoyai/tdx/v2/extend/model"
	"github.com/injoyai/tdx/v2/extend/storage/sqlite"
	"github.com/injoyai/tdx/v2/protocol"
)

func TestReadSQLiteMinutePreservesAnnualFileContents(t *testing.T) {
	dir := t.TempDir()
	src, err := sqlite.NewPull(dir, 4)
	if err != nil {
		t.Fatal(err)
	}
	code := model.Code{Market: protocol.MarketIndex, Code: "sh000001"}
	rows := []*model.KlineMinute{
		{Time: "2024-12-31 15:00", Close: 3000, Volume: 100},
		{Time: "2025-01-02 09:31", Close: 3001, Volume: 200, Source: model.MinuteSourceTrade},
	}
	if err := src.SaveMinute(code, 2024, "", rows[:1], nil); err != nil {
		t.Fatal(err)
	}
	if err := src.Close(); err != nil {
		t.Fatal(err)
	}
	filename := filepath.Join(dir, code.Market.String(), "min", code.Code, code.Code+"-2024.db")
	db, err := xorms.NewSQLite(filename)
	if err != nil {
		t.Fatal(err)
	}
	// Some historical annual files contain adjacent-year rows; keep them for year routing.
	_, err = db.Insert(rows[1])
	closeErr := db.Close()
	if err != nil {
		t.Fatal(err)
	}
	if closeErr != nil {
		t.Fatal(closeErr)
	}
	before, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	got, err := readSQLiteMinute(filename)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, rows) {
		t.Fatalf("rows=%+v, want=%+v", got, rows)
	}
	after, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatal("source file changed while reading")
	}
}

func TestReadSQLiteMinuteDoesNotCreateMissingFile(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "missing.db")
	if _, err := readSQLiteMinute(filename); err == nil {
		t.Fatal("missing source should fail")
	}
	if _, err := os.Stat(filename); !os.IsNotExist(err) {
		t.Fatalf("source was created: %v", err)
	}
}

func TestReadArchivedSourceV1DayPreservesEquity(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".time-format"), []byte("source-time-v1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "cn", "stock", "day", "sh600000.db")
	db, err := xorms.NewSQLite(file)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec("CREATE TABLE KlineDay(Time TEXT PRIMARY KEY, Open REAL, High REAL, Low REAL, Close REAL, Volume INTEGER, Amount REAL, Turnover REAL, FloatStock REAL, TotalStock REAL)")
	if err == nil {
		_, err = db.Exec("INSERT INTO KlineDay VALUES('2025-01-02',10,12,9,11,1200,0,1.2,100000,200000)")
	}
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	source, err := OpenSQLiteSource(dir)
	if err != nil {
		t.Fatal(err)
	}
	items, _, err := migrateItems(source)
	if err != nil || len(items) != 1 || items[0].file != file {
		t.Fatalf("archive items=%+v err=%v", items, err)
	}
	got, err := readSourceDay(items[0].file, "", source.version == 1)
	if err != nil || len(got) != 1 || got[0].Close != 11 || got[0].Amount == nil || *got[0].Amount != 0 || got[0].Turnover != 1.2 || got[0].FloatStock != 100000 || got[0].TotalStock != 200000 {
		t.Fatalf("archive read=%+v err=%v", got, err)
	}
	after, err := os.ReadFile(file)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("archive changed: %v", err)
	}
}
