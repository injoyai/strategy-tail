package tdx

import (
	"math"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/injoyai/strategy-tail/researchdata"
	"github.com/injoyai/tdx/protocol"
)

func statRow(market uint8, code, date, pe, dividend string) *protocol.TdxStat {
	fields := []string{"0", code, "", pe, date, "", "", "", "", "12.5", dividend}
	return &protocol.TdxStat{Market: market, Code: code, Date: date, Fields: fields}
}

func TestSnapshotPreservesSourceDateAvailabilityAndMissingValues(t *testing.T) {
	at := time.Date(2026, 10, 15, 16, 0, 0, 0, time.FixedZone("CST", 8*3600))
	rows := []*protocol.TdxStat{
		statRow(1, "600519", "20261014", "19.74", "4.05"),
		statRow(0, "000651", "20261014", "-8", "0"),
		statRow(0, "002555", "20261014", "NaN", "bad"),
		statRow(1, "688001", "20261014", "10", "2"),
		statRow(0, "300001", "20261014", "10", "2"),
		statRow(2, "920509", "20261014", "10", "2"),
		statRow(0, "001381", "", "10", "2"),
		statRow(1, "600000", "20261016", "10", "2"),
	}
	records, skipped, err := normalizeSnapshot(rows, at)
	if err != nil || len(records) != 3 || skipped != 2 {
		t.Fatalf("records=%d skipped=%d err=%v", len(records), skipped, err)
	}
	wantDate := time.Date(2026, 10, 14, 15, 0, 0, 0, at.Location())
	if r := records[0]; r.Code != "sh600519" || r.Key != "2026-10-14" || !r.EventAt.Equal(wantDate) || !r.AvailableAt.Equal(at) || r.Values["dividend_yield"] != 4.05 {
		t.Fatalf("incorrect source/time/units: %+v", r)
	}
	if r := records[1]; r.Values["pe_ttm"] != -8 || r.Values["dividend_yield"] != 0 {
		t.Fatalf("negative PE or zero dividend lost: %+v", r)
	}
	if r := records[2]; len(r.Values) != 1 || r.Values["pe_static"] != 12.5 {
		t.Fatalf("malformed values became valid zeroes: %+v", r)
	}
	store, err := researchdata.OpenSQLiteStore(filepath.Join(t.TempDir(), "research.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err = store.RegisterDataset(valuationSpec); err != nil {
		t.Fatal(err)
	}
	if err = store.Ingest(records); err != nil {
		t.Fatal(err)
	}
	if _, ok := store.Latest(valuationSpec.ID, "sh600519", at.Add(-time.Nanosecond)); ok {
		t.Fatal("snapshot leaked before actual collection time")
	}
	got, ok := store.Latest(valuationSpec.ID, "sh600519", at)
	if !ok || math.Abs(got.Values["pe_ttm"]-19.74) > 1e-12 {
		t.Fatalf("readback=%+v %v", got, ok)
	}
	if err = store.Ingest(records); err != nil {
		t.Fatal(err)
	}
	if got := store.Range(valuationSpec.ID, "sh600519", time.Time{}, at, at); len(got) != 1 {
		t.Fatalf("duplicate snapshot: %d", len(got))
	}
	// A later observation may revise the same source day, but cannot rewrite
	// what a strategy could see at the earlier observation time.
	later := at.Add(time.Hour)
	revised, _, err := normalizeSnapshot([]*protocol.TdxStat{statRow(1, "600519", "20261014", "20", "4")}, later)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.Ingest(revised); err != nil {
		t.Fatal(err)
	}
	earlier, _ := store.Latest(valuationSpec.ID, "sh600519", at)
	latest, _ := store.Latest(valuationSpec.ID, "sh600519", later)
	if earlier.Values["pe_ttm"] != 19.74 || latest.Values["pe_ttm"] != 20 {
		t.Fatalf("revision leakage: old=%+v new=%+v", earlier, latest)
	}
}

func TestSnapshotRejectsEmptyInvalidAndDuplicateBatches(t *testing.T) {
	at := time.Now()
	for _, rows := range [][]*protocol.TdxStat{nil, {nil}, {statRow(0, "001381", "", "0", "")}} {
		if _, _, err := normalizeSnapshot(rows, at); err == nil {
			t.Fatal("invalid batch accepted")
		}
	}
	r := statRow(1, "600519", "20200101", "10", "3")
	if _, _, err := normalizeSnapshot([]*protocol.TdxStat{r, r}, at); err == nil || !strings.Contains(err.Error(), "重复") {
		t.Fatalf("duplicate error: %v", err)
	}
	if _, _, err := normalizeSnapshot([]*protocol.TdxStat{r}, time.Time{}); err == nil {
		t.Fatal("missing observation time accepted")
	}
}
