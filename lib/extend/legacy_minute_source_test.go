package extend

import (
	"bytes"
	"compress/gzip"
	"database/sql"
	"encoding/binary"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/injoyai/tdx/v2/extend/pull"
	"github.com/injoyai/tdx/v2/protocol"
)

type legacyMinuteRecord [12]int64

func writeLegacyMinuteFile(t *testing.T, filename string, day int, records []legacyMinuteRecord) {
	t.Helper()
	db, err := sql.Open("sqlite", filename)
	if err != nil {
		t.Fatalf("open legacy sqlite: %v", err)
	}
	defer db.Close()
	for _, query := range []string{
		"CREATE TABLE KlineBlock (Day INTEGER PRIMARY KEY, Data BLOB NOT NULL)",
		"CREATE TABLE KlineMeta (ID INTEGER PRIMARY KEY CHECK(ID=1), NextSequence INTEGER NOT NULL, LastUnix INTEGER NOT NULL, RecordCount INTEGER NOT NULL)",
		"PRAGMA user_version=101",
	} {
		if _, err := db.Exec(query); err != nil {
			t.Fatalf("init legacy minute db: %v", err)
		}
	}
	var raw bytes.Buffer
	raw.Write([]byte{'K', 'L', 'M', '1', 96, 0, 0, 0})
	for i := range records {
		records[i][0] = int64(i + 1)
		for _, v := range records[i] {
			if err := binary.Write(&raw, binary.LittleEndian, v); err != nil {
				t.Fatalf("encode record: %v", err)
			}
		}
	}
	var data bytes.Buffer
	writer := gzip.NewWriter(&data)
	if _, err := writer.Write(raw.Bytes()); err != nil {
		t.Fatalf("gzip block: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("gzip block: %v", err)
	}
	if _, err := db.Exec("INSERT INTO KlineBlock (Day,Data) VALUES (?,?)", day, data.Bytes()); err != nil {
		t.Fatalf("insert block: %v", err)
	}
	next := int64(1)
	for _, r := range records {
		if r[0] >= next {
			next = r[0] + 1
		}
	}
	if _, err := db.Exec("INSERT INTO KlineMeta VALUES (1,?,?,?)", next, records[len(records)-1][9], len(records)); err != nil {
		t.Fatalf("insert meta: %v", err)
	}
}

func legacyMinuteRecordOf(at time.Time, close, volume, amount int64) legacyMinuteRecord {
	return legacyMinuteRecord{0, close, close, close, close, close, 0, volume, amount, at.Unix(), 0, 0}
}

func TestReadLegacyMinuteSource(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "sh600000-2024.db")
	at := time.Date(2024, 1, 2, 9, 30, 0, 0, protocol.Location())
	next := at.Add(time.Minute)
	writeLegacyMinuteFile(t, filename, 20240102, []legacyMinuteRecord{
		legacyMinuteRecordOf(at, 10060, 1530, 1014390000),
		legacyMinuteRecordOf(at, 10070, 1540, 1015390000), // 同一分钟重复，保留最后一条
		legacyMinuteRecordOf(next, 10100, 1600, 1016390000),
	})
	code, err := pull.ParseCode("sh600000")
	if err != nil {
		t.Fatalf("parse code: %v", err)
	}
	got, err := ReadLegacyMinuteSource(filename, code, 2024)
	if err != nil {
		t.Fatalf("read legacy minute: %v", err)
	}
	if len(got.Rows) != 2 || got.Dropped != 0 {
		t.Fatalf("rows = %d dropped = %d", len(got.Rows), got.Dropped)
	}
	if got.Rows[0].Time != "2024-01-02 09:30" || got.Rows[0].Close != 10.07 {
		t.Fatalf("first row = %+v", got.Rows[0])
	}
	if got.Rows[0].Volume != 154000 || (got.Rows[0].Amount == nil || *got.Rows[0].Amount != 1015390) {
		t.Fatalf("first volume/amount = %+v", got.Rows[0])
	}
	if got.Rows[1].Time != "2024-01-02 09:31" || got.Rows[1].Close != 10.1 || got.Rows[1].Volume != 160000 {
		t.Fatalf("second row = %+v", got.Rows[1])
	}
}

func TestReadLegacyMinuteSourceIndexKeepsLots(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "sh000001-2024.db")
	at := time.Date(2024, 1, 2, 9, 30, 0, 0, protocol.Location())
	writeLegacyMinuteFile(t, filename, 20240102, []legacyMinuteRecord{
		legacyMinuteRecordOf(at, 3608100, 1530, 1014390000),
	})
	code, err := pull.ParseCode("sh000001")
	if err != nil {
		t.Fatalf("parse code: %v", err)
	}
	got, err := ReadLegacyMinuteSource(filename, code, 2024)
	if err != nil {
		t.Fatalf("read legacy minute: %v", err)
	}
	if len(got.Rows) != 1 || got.Rows[0].Close != 3608.1 || got.Rows[0].Volume != 1530 {
		t.Fatalf("index row = %+v", got.Rows)
	}
}

func TestReadLegacyMinuteSourceRejectsCorrupt(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "sh600000-2024.db")
	at := time.Date(2024, 1, 2, 9, 30, 0, 0, protocol.Location())
	writeLegacyMinuteFile(t, filename, 20240102, []legacyMinuteRecord{
		legacyMinuteRecordOf(at, 10060, 1530, 1014390000),
		legacyMinuteRecordOf(at.AddDate(1, 0, 0), 10070, 1540, 1015390000),
	})
	code, err := pull.ParseCode("sh600000")
	if err != nil {
		t.Fatalf("parse code: %v", err)
	}
	_, err = ReadLegacyMinuteSource(filename, code, 2024)
	if !errors.Is(err, ErrLegacyMinuteCorrupt) {
		t.Fatalf("err = %v, want ErrLegacyMinuteCorrupt", err)
	}
}
