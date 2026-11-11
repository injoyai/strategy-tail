package extend

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func createLegacyDayFile(t *testing.T, filename, closeType string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filename)
	if err != nil {
		t.Fatalf("open legacy sqlite: %v", err)
	}
	schema := `CREATE TABLE Kline (
		Unix INTEGER PRIMARY KEY, Last INTEGER, Open INTEGER, High INTEGER, Low INTEGER, Close ` + closeType + `,
		"Order" INTEGER, Volume INTEGER, Amount INTEGER, Time DATETIME, UpCount INTEGER, DownCount INTEGER,
		Turnover REAL, FloatStock INTEGER, TotalStock INTEGER)`
	if _, err := db.Exec(schema); err != nil {
		t.Fatalf("create legacy table: %v", err)
	}
	return db
}

func TestReadLegacyDaySource(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "sh600000.db")
	db := createLegacyDayFile(t, filename, "INTEGER")
	t.Cleanup(func() { _ = db.Close() })

	rows := []struct {
		unix                   int64
		o, h, l, c             int64
		volume, amount         int64
		turnover               float64
		floatStock, totalStock int64
		at                     string
	}{
		{1793602800, 9980, 10220, 9930, 10060, 1164222, 1177122560000, 0.34955493913041519, 33305837500, 33305837500, "2026-11-02 07:00:00"},
		{1793689200, 10060, 10180, 9850, 10090, 977550, 978658304000, 0.29350710667461821, 33305837500, 33305837500, "2026-11-03 07:00:00"},
		// 同一交易日重复记录，应保留最后一行。
		{1793689260, 10060, 10180, 9850, 10100, 977560, 978668304000, 0.29351, 33305837500, 33305837500, "2026-11-03 07:01:00"},
	}
	for _, r := range rows {
		_, err := db.Exec(`INSERT INTO Kline (Unix, Last, Open, High, Low, Close, "Order", Volume, Amount, Time, UpCount, DownCount, Turnover, FloatStock, TotalStock)
			VALUES (?, 0, ?, ?, ?, ?, 0, ?, ?, ?, 0, 0, ?, ?, ?)`,
			r.unix, r.o, r.h, r.l, r.c, r.volume, r.amount, r.at, r.turnover, r.floatStock, r.totalStock)
		if err != nil {
			t.Fatalf("insert legacy row: %v", err)
		}
	}

	got, err := ReadLegacyDaySource(filename)
	if err != nil {
		t.Fatalf("read legacy day: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("rows = %d, want 2", len(got))
	}
	if got[0].Time != "2026-11-02" || got[0].Close != 10.06 || got[0].Open != 9.98 {
		t.Fatalf("first row = %+v", got[0])
	}
	if got[0].Volume != 116422200 {
		t.Fatalf("first volume = %d, want 116422200", got[0].Volume)
	}
	if got[0].Amount == nil || *got[0].Amount != 1177122560 {
		t.Fatalf("first amount = %v, want 1177122560", got[0].Amount)
	}
	if got[0].Turnover != 0.34955493913041519 || got[0].FloatStock != 33305837500 {
		t.Fatalf("first turnover/stock = %+v", got[0])
	}
	if got[1].Time != "2026-11-03" || got[1].Close != 10.1 || got[1].Volume != 97756000 {
		t.Fatalf("duplicate date row = %+v", got[1])
	}
}

func TestReadLegacyDaySourceKeepsFloatPrices(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "sh600004.db")
	db := createLegacyDayFile(t, filename, "REAL")
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`INSERT INTO Kline (Unix, Last, Open, High, Low, Close, "Order", Volume, Amount, Time, UpCount, DownCount, Turnover, FloatStock, TotalStock)
		VALUES (?, 0, 10.06, 10.18, 9.85, 10.09, 0, 977550, 978658304, '2026-11-03 07:00:00', 0, 0, 0.29, 1000, 2000)`, 1793689200); err != nil {
		t.Fatalf("insert legacy row: %v", err)
	}

	got, err := ReadLegacyDaySource(filename)
	if err != nil {
		t.Fatalf("read legacy day: %v", err)
	}
	if len(got) != 1 || got[0].Close != 10.09 || (got[0].Amount == nil || *got[0].Amount != 978658304) {
		t.Fatalf("float price row = %+v", got)
	}
}
