package main

import (
	"crypto/sha256"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/injoyai/strategy-tail/internal/marketstore"
	"github.com/injoyai/strategy-tail/lib/extend"
	"github.com/injoyai/tdx/v2/extend/model"
)

func TestMigrateLegacyMinutePreservesTarget(t *testing.T) {
	dsn := os.Getenv("PULL_MYSQL_DSN")
	if dsn == "" {
		t.Skip("PULL_MYSQL_DSN 未设置")
	}
	dst, err := marketstore.NewMySQLStore(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer dst.Close()
	keys, err := openMinuteKeys(dsn, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer keys.Close()
	code := model.Code{Market: "cn/stock", Code: fmt.Sprintf("audit%d", time.Now().UnixNano())}
	file := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite", file)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE Kline(Last REAL,Open REAL,High REAL,Low REAL,Close REAL,"Order" INTEGER,Volume INTEGER,Amount REAL,Time DATETIME,UpCount INTEGER,DownCount INTEGER)`)
	if err != nil {
		t.Fatal(err)
	}
	for _, at := range []string{"2024-01-02 09:31:00+08:00", "2024-01-02 09:32:00+08:00", "2024-01-02 09:33:00+08:00"} {
		if _, err = db.Exec(`INSERT INTO Kline VALUES(10,10,10,10,10,0,100,1000,?,0,0)`, at); err != nil {
			t.Fatal(err)
		}
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	original := []*model.KlineMinute{{Time: "2024-01-02 09:31", Close: 99}, {Time: "2024-01-02 09:33", Close: 98}, {Time: "2024-01-03 09:31", Close: 97}}
	if err = dst.SaveMinute(code, 2024, "", original, nil); err != nil {
		t.Fatal(err)
	}
	r, err := migrateLegacyMinute(dst, keys, job{file: file, code: code, year: 2024}, receipt{}, sourcePolicy{})
	if err != nil || r.Rows != 1 || r.Existing != 2 || r.Missing != 1 || !r.Verified || r.Invalid {
		t.Fatalf("migration=%+v %v", r, err)
	}
	got, err := dst.QueryMinute(code, "", "")
	if err != nil || len(got) != 4 || got[0].Close != 99 || got[1].Close != 10 || got[2].Close != 98 || got[3].Close != 97 {
		t.Fatalf("target changed: %+v %v", got, err)
	}
	r, err = migrateLegacyMinute(dst, keys, job{file: file, code: code, year: 2024}, receipt{}, sourcePolicy{})
	if err != nil || r.Rows != 0 || !r.Skipped || r.Missing != 0 || !r.Verified || r.Invalid {
		t.Fatalf("rerun=%+v %v", r, err)
	}
}

func TestCopyMinuteSourcePreservesProductionInput(t *testing.T) {
	file := filepath.Join(t.TempDir(), "input.db")
	db, err := sql.Open("sqlite", file)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE Kline(Last INTEGER,Open INTEGER,High INTEGER,Low INTEGER,Close INTEGER,"Order" INTEGER,Volume INTEGER,Amount INTEGER,Time DATETIME,UpCount INTEGER,DownCount INTEGER);
 INSERT INTO Kline VALUES(6630,6630,6630,6630,6630,0,1530,1014390000,'2024-01-02 01:30:00',0,0);
 INSERT INTO Kline VALUES(6630,6630,6630,6630,6640,0,1530,1014390000,'2024-01-02 01:30:00',0,0);`)
	if err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, digest, err := copyMinuteSource(file)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(snapshot)
	if len(digest) != 64 {
		t.Fatal("snapshot hash missing")
	}
	data, err := extend.ReadLegacyMinuteSource(snapshot, model.Code{Market: "cn/stock", Code: "sh600000"}, 2024)
	if err != nil || len(data.Rows) != 1 {
		t.Fatal(data, err)
	}
	k := data.Rows[0]
	if k.Time != "2024-01-02 09:30" || k.Open != 6.63 || k.Close != 6.64 || k.Volume != 153000 || (k.Amount == nil || *k.Amount != 1014390) {
		t.Fatalf("source units or UTC date lost: %+v", k)
	}
	after, err := os.ReadFile(file)
	if err != nil || sha256.Sum256(before) != sha256.Sum256(after) {
		t.Fatal("source bytes changed", err)
	}
	names, err := os.ReadDir(filepath.Dir(file))
	if err != nil || len(names) != 1 {
		t.Fatal("source sidecars created", names, err)
	}
	if err = os.WriteFile(file+"-wal", []byte("active"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err = copyMinuteSource(file); err == nil {
		t.Fatal("active WAL copied")
	}
}

func TestQuarantineChangesOnlyDisposableSnapshot(t *testing.T) {
	file := filepath.Join(t.TempDir(), "input.db")
	db, err := sql.Open("sqlite", file)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE Kline(Last INTEGER,Open INTEGER,High INTEGER,Low INTEGER,Close INTEGER,"Order" INTEGER,Volume INTEGER,Amount INTEGER,Time DATETIME,UpCount INTEGER,DownCount INTEGER)`)
	if err != nil {
		t.Fatal(err)
	}
	for day := 1; day <= 10; day++ {
		amount := 100000
		price := 10000
		if day == 3 {
			amount = -100
		}
		if day == 4 {
			price = 56342720
		}
		_, err = db.Exec(`INSERT INTO Kline VALUES(?,?,?,?,?,0,100,?,?,0,0)`, price, price, price, price, price, amount, fmt.Sprintf("2024-01-%02d 01:30:00", day))
		if err != nil {
			t.Fatal(err)
		}
	}
	db.Close()
	before, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, hash, err := copyMinuteSource(file)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(snapshot)
	j := job{file: file, code: model.Code{Market: "cn/stock", Code: "sh600103"}, year: 2024}
	policy := sourcePolicy{InvalidNumericRows: true, BadDays: []badDayRule{{Code: j.code.Code, Date: "2024-01-04", SHA256: hash, Reason: "已核实坏日"}}}
	isolated, err := isolateSnapshot(snapshot, j, hash, policy)
	if err != nil || len(isolated) != 2 {
		t.Fatal(isolated, err)
	}
	if isolated[0].Values["Amount"] == nil || *isolated[0].Values["Amount"] != "-100" || isolated[1].Reason != "已核实坏日" {
		t.Fatal("quarantine raw values lost", isolated)
	}
	data, err := extend.ReadLegacyMinuteSource(snapshot, j.code, j.year)
	if err != nil || len(data.Rows) != 8 || data.Dropped != 0 {
		t.Fatal(data, err)
	}
	after, err := os.ReadFile(file)
	if err != nil || sha256.Sum256(before) != sha256.Sum256(after) {
		t.Fatal("production source changed", err)
	}
	if _, err = isolateSnapshot(snapshot, j, "wrong-hash", policy); err == nil {
		t.Fatal("hash-pinned quarantine rule accepted changed source")
	}
}

func TestUnreviewedLinkedBadDayDoesNotDeleteSnapshotRows(t *testing.T) {
	file := filepath.Join(t.TempDir(), "input.db")
	db, err := sql.Open("sqlite", file)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE Kline(Last INTEGER,Open INTEGER,High INTEGER,Low INTEGER,Close INTEGER,"Order" INTEGER,Volume INTEGER,Amount INTEGER,Time DATETIME,UpCount INTEGER,DownCount INTEGER)`)
	if err != nil {
		t.Fatal(err)
	}
	for _, at := range []string{"2024-01-01 07:00:00", "2024-01-03 01:30:00"} {
		if _, err = db.Exec(`INSERT INTO Kline VALUES(10000,10000,10000,10000,10000,0,100,100000,?,0,0)`, at); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 241; i++ {
		amount, volume := 0, 0
		if i == 240 {
			amount = -1
			volume = 1
		}
		at := time.Date(2024, 1, 2, 1, 30, 0, 0, time.UTC).Add(time.Duration(i) * time.Minute).Format(time.DateTime)
		if _, err = db.Exec(`INSERT INTO Kline VALUES(56342720,56342720,56342720,56342720,56342720,0,?,?,?,0,0)`, volume, amount, at); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()
	j := job{file: file, code: model.Code{Market: "cn/stock", Code: "sh600221"}, year: 2024}
	_, err = isolateSnapshot(file, j, "hash", sourcePolicy{InvalidNumericRows: true})
	if err == nil || !strings.Contains(err.Error(), "未核实关联坏日") {
		t.Fatal("unreviewed linked corruption accepted", err)
	}
	db, err = sql.Open("sqlite", file)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var count int
	if err = db.QueryRow("SELECT COUNT(*) FROM Kline").Scan(&count); err != nil || count != 243 {
		t.Fatal("snapshot changed before review", count, err)
	}
}

func TestLegacyMinuteJobsOrderByYearThenCode(t *testing.T) {
	root := t.TempDir()
	for _, code := range []string{"sh600000", "sz000001"} {
		if err := os.Mkdir(filepath.Join(root, code), 0700); err != nil {
			t.Fatal(err)
		}
		for _, year := range []string{"2000", "2025"} {
			if err := os.WriteFile(filepath.Join(root, code, code+"-"+year+".db"), nil, 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	jobs, bad, err := legacyMinuteJobs(root, "", 0)
	if err != nil || bad != 0 || len(jobs) != 4 {
		t.Fatal(jobs, bad, err)
	}
	if jobs[0].year != 2000 || jobs[1].year != 2000 || jobs[2].year != 2025 || jobs[3].year != 2025 || jobs[0].code.Code != "sh600000" {
		t.Fatal("not ordered by year then code", jobs)
	}
}

func TestLegacyMinuteJobsStartYearInclusive(t *testing.T) {
	root := t.TempDir()
	code := "sh600000"
	dir := filepath.Join(root, code)
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	for _, year := range []string{"2017", "2018", "2025"} {
		if err := os.WriteFile(filepath.Join(dir, code+"-"+year+".db"), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	jobs, bad, err := legacyMinuteJobs(root, "", 2018)
	if err != nil || bad != 0 || len(jobs) != 2 || jobs[0].year != 2018 || jobs[1].year != 2025 {
		t.Fatal("start-year boundary or latest year lost", jobs, bad, err)
	}
}
