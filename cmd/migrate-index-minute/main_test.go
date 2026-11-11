package main

import (
	"crypto/sha256"
	"database/sql"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/injoyai/strategy-tail/internal/marketstore"
	"github.com/injoyai/tdx/v2/extend/model"
	"github.com/injoyai/tdx/v2/protocol"
)

func fixture(t *testing.T) job {
	t.Helper()
	dir := t.TempDir()
	file := filepath.Join(dir, "sh000001.db")
	db, err := sql.Open("sqlite", file)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE KlineMinute1(ID INTEGER PRIMARY KEY,Date INTEGER,Year INTEGER,Month INTEGER,Day INTEGER,Hour INTEGER,Minute INTEGER,Open REAL,High REAL,Low REAL,Close REAL,Volume INTEGER,Amount REAL);
 CREATE TABLE KlineMinute5(Note TEXT); INSERT INTO KlineMinute5 VALUES('do not import');`)
	if err != nil {
		t.Fatal(err)
	}
	for i, minute := range []int{31, 31, 32} {
		at := time.Date(2025, 1, 2, 9, minute, 0, 0, protocol.Location())
		_, err = db.Exec(`INSERT INTO KlineMinute1 VALUES(?,?,2025,1,2,9,?,10.123,11.234,9.012,?,123,1000.456)`, i+1, at.Unix(), minute, float64(10+i))
		if err != nil {
			t.Fatal(err)
		}
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	return job{File: file, Year: 2025, Code: "sh000001", Market: protocol.MarketIndex}
}
func digest(t *testing.T, file string) [32]byte {
	t.Helper()
	bs, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	return sha256.Sum256(bs)
}
func TestSnapshotPreservesSourceAndMinuteContract(t *testing.T) {
	j := fixture(t)
	before := digest(t, j.File)
	r := receipt{}
	hash, err := snapshotImport(nil, j, "", &r)
	if err != nil {
		t.Fatal(err)
	}
	if len(hash) != 64 || r.Rows != 2 || r.Duplicates != 1 || r.First != "2025-01-02 09:31" || r.Last != "2025-01-02 09:32" || r.Imported {
		t.Fatalf("unexpected snapshot receipt: %+v", r)
	}
	if digest(t, j.File) != before {
		t.Fatal("source content changed")
	}
	files, err := os.ReadDir(filepath.Dir(j.File))
	if err != nil || len(files) != 1 {
		t.Fatal("source directory modified", files, err)
	}
	rows, _, err := readSnapshot(j.File, j.Year, j.Market)
	if err != nil || rows[0].Close != 11 || rows[0].Open != 10.123 || rows[0].Volume != 123 || (rows[0].Amount == nil || *rows[0].Amount != 1000.456) {
		t.Fatal("precision, duplicates, or source units lost", rows, err)
	}
}
func TestSnapshotRejectsActiveJournal(t *testing.T) {
	j := fixture(t)
	if err := os.WriteFile(j.File+"-wal", []byte("active"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := snapshotImport(nil, j, "", new(receipt)); err == nil {
		t.Fatal("active source journal accepted")
	}
}
func TestSnapshotRejectsWrongCalendar(t *testing.T) {
	j := fixture(t)
	j.Year = 2024
	if _, err := snapshotImport(nil, j, "", new(receipt)); err == nil {
		t.Fatal("year mismatch accepted")
	}
}
func TestListJobsPreservesOriginalCodes(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "2025"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, code := range []string{"sh999999", "bj899050", "sz399001"} {
		if err := os.WriteFile(filepath.Join(root, "2025", code+".db"), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	jobs, err := listJobs(root, "", 0, 0, protocol.MarketIndex)
	if err != nil || len(jobs) != 3 || jobs[1].Code != "sh999999" {
		t.Fatal(jobs, err)
	}
}

func TestListJobsStartYearKeepsLatest(t *testing.T) {
	root := t.TempDir()
	for _, year := range []string{"2017", "2018", "2026"} {
		dir := filepath.Join(root, year)
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "sh999999.db"), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	jobs, err := listJobs(root, "", 0, 2018, protocol.MarketIndex)
	if err != nil || len(jobs) != 2 || jobs[0].Year != 2018 || jobs[1].Year != 2026 {
		t.Fatal("start-year truncated latest data", jobs, err)
	}
	jobs, err = listJobs(root, "", 2017, 2018, protocol.MarketIndex)
	if err != nil || len(jobs) != 0 {
		t.Fatal("explicit year bypassed floor", jobs, err)
	}
}
func TestImportPreservesExistingKeysAndVerifiesFullInterval(t *testing.T) {
	dsn := os.Getenv("PULL_MYSQL_DSN")
	if dsn == "" {
		t.Skip("PULL_MYSQL_DSN 未设置")
	}
	dst, err := marketstore.NewMySQLStore(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer dst.Close()
	j := fixture(t)
	j.Code = fmt.Sprintf("index-test-%d", time.Now().UnixNano())
	code := model.Code{Market: protocol.MarketIndex, Code: j.Code}
	original := []*model.KlineMinute{{Time: "2025-01-02 09:31", Close: 99, Source: "trade"}, {Time: "2025-01-03 09:31", Close: 77}}
	if err = dst.SaveMinute(code, 2025, "", original, nil); err != nil {
		t.Fatal(err)
	}
	before := digest(t, j.File)
	r := receipt{}
	hash, err := snapshotImport(dst, j, "", &r)
	if err != nil || r.Added != 1 || r.Conflicts != 1 || !r.Imported {
		t.Fatal(r, err)
	}
	got, err := dst.QueryMinute(code, "", "")
	if err != nil || len(got) != 3 || !reflect.DeepEqual(got[0], original[0]) || !reflect.DeepEqual(got[2], original[1]) {
		t.Fatal("existing content or newer tail changed", got, err)
	}
	r = receipt{}
	if _, err = snapshotImport(dst, j, "", &r); err != nil || r.Added != 0 || r.Conflicts != 1 {
		t.Fatal("import not idempotent", r, err)
	}
	r = receipt{}
	if _, err = snapshotImport(dst, j, hash, &r); err != nil || !r.Imported || r.Added != 0 {
		t.Fatal("hash resume failed", r, err)
	}
	if before != digest(t, j.File) {
		t.Fatal("source content changed")
	}
}

func mixedETFFixture(t *testing.T) job {
	t.Helper()
	j := fixture(t)
	j.Year, j.Code, j.Market = 2026, "sh510300", protocol.MarketEtfLof
	db, err := sql.Open("sqlite", j.File)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 1, 5, 9, 31, 0, 0, protocol.Location())
	_, err = db.Exec(`INSERT INTO KlineMinute1 VALUES(4,?,2026,1,5,9,31,20.123,21.234,19.012,20.456,200,4000.789)`, at.Unix())
	if err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	return j
}

func TestETFSourceUsesCalendarYearsAndShareVolume(t *testing.T) {
	j := mixedETFFixture(t)
	before := digest(t, j.File)
	r := receipt{}
	if _, err := snapshotImport(nil, j, "", &r); err != nil {
		t.Fatal(err)
	}
	if r.Rows != 3 || r.Duplicates != 1 || r.Market != protocol.MarketEtfLof || !reflect.DeepEqual(r.Years, []int{2025, 2026}) || r.First != "2025-01-02 09:31" || r.Last != "2026-01-05 09:31" {
		t.Fatal("mixed history was truncated or mislabeled", r)
	}
	rows, _, err := readSnapshot(j.File, j.Year, j.Market)
	if err != nil || rows[0].Volume != 12300 || rows[2].Volume != 20000 || rows[0].Open != 10.123 || (rows[2].Amount == nil || *rows[2].Amount != 4000.789) {
		t.Fatal("ETF units changed", rows, err)
	}
	if before != digest(t, j.File) {
		t.Fatal("source changed")
	}
	j.Market = protocol.MarketIndex
	if _, _, err = readSnapshot(j.File, j.Year, j.Market); err == nil {
		t.Fatal("index calendar constraint was weakened")
	}
}

func TestETFRejectsCalendarMismatchAndVolumeOverflow(t *testing.T) {
	for _, statement := range []string{
		`UPDATE KlineMinute1 SET Year=2024 WHERE ID=1`,
		fmt.Sprintf(`UPDATE KlineMinute1 SET Volume=%d WHERE ID=1`, int64(math.MaxInt64/100+1)),
	} {
		j := mixedETFFixture(t)
		db, err := sql.Open("sqlite", j.File)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = db.Exec(statement); err != nil {
			t.Fatal(err)
		}
		db.Close()
		if _, _, err = readSnapshot(j.File, j.Year, j.Market); err == nil {
			t.Fatal("invalid ETF row accepted", statement)
		}
	}
}

func TestETFJobSelectionKeepsAllSuppliedYears(t *testing.T) {
	root := t.TempDir()
	for _, year := range []string{"2000", "2017", "2025", "2026"} {
		dir := filepath.Join(root, year)
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "sh510300.db"), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	jobs, err := listJobs(root, "", 0, 0, protocol.MarketEtfLof)
	if err != nil || len(jobs) != 4 || jobs[0].Year != 2026 || jobs[3].Year != 2000 || jobs[0].Market != protocol.MarketEtfLof {
		t.Fatal(jobs, err)
	}
	if _, err = listJobs(root, "", 0, 0, protocol.MarketIndex); err == nil {
		t.Fatal("ETF source accepted as index")
	}
}

func TestETFImportSplitsYearsAndPreservesExisting(t *testing.T) {
	dsn := os.Getenv("PULL_MYSQL_DSN")
	if dsn == "" {
		t.Skip("PULL_MYSQL_DSN 未设置")
	}
	dst, err := marketstore.NewMySQLStore(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer dst.Close()
	j := mixedETFFixture(t)
	j.Code = fmt.Sprintf("etf-test-%d", time.Now().UnixNano())
	code := model.Code{Market: j.Market, Code: j.Code}
	prior := &model.KlineMinute{Time: "2025-01-02 09:31", Close: 99, Source: "trade"}
	tail := &model.KlineMinute{Time: "2026-02-02 09:31", Close: 77}
	for year, row := range map[int]*model.KlineMinute{2025: prior, 2026: tail} {
		if err = dst.SaveMinute(code, year, "", []*model.KlineMinute{row}, nil); err != nil {
			t.Fatal(err)
		}
	}
	r := receipt{}
	if _, err = snapshotImport(dst, j, "", &r); err != nil || r.Added != 2 || r.Conflicts != 1 || !r.Imported {
		t.Fatal(r, err)
	}
	got, err := dst.QueryMinute(code, "", "")
	if err != nil || len(got) != 4 || !reflect.DeepEqual(got[0], prior) || !reflect.DeepEqual(got[3], tail) || got[1].Volume != 12300 || got[2].Time != "2026-01-05 09:31" || got[2].Volume != 20000 {
		t.Fatal("year partition, unit conversion or existing values changed", got, err)
	}
	r = receipt{}
	if _, err = snapshotImport(dst, j, "", &r); err != nil || r.Added != 0 || !r.Imported {
		t.Fatal("mixed-year import was not idempotent", r, err)
	}
}

func TestETFNewerSourceWinsOverlappingYears(t *testing.T) {
	root := t.TempDir()
	older, newer := fixture(t), mixedETFFixture(t)
	for year, j := range map[int]job{2025: older, 2026: newer} {
		db, err := sql.Open("sqlite", j.File)
		if err != nil {
			t.Fatal(err)
		}
		closeValue := 3.529
		if year == 2026 {
			closeValue = 3.521
		}
		if _, err = db.Exec("UPDATE KlineMinute1 SET Close=? WHERE Year=2025", closeValue); err != nil {
			t.Fatal(err)
		}
		if year == 2025 {
			at := time.Date(2025, 1, 2, 9, 33, 0, 0, protocol.Location())
			if _, err = db.Exec("INSERT INTO KlineMinute1 VALUES(4,?,2025,1,2,9,33,3,3,3,3,10,3000)", at.Unix()); err != nil {
				t.Fatal(err)
			}
		}
		if err = db.Close(); err != nil {
			t.Fatal(err)
		}
		dir := filepath.Join(root, fmt.Sprint(year))
		if err = os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err = os.Rename(j.File, filepath.Join(dir, "sh510300.db")); err != nil {
			t.Fatal(err)
		}
	}
	jobs, err := listJobs(root, "", 0, 0, protocol.MarketEtfLof)
	if err != nil {
		t.Fatal(err)
	}
	groups := groupJobs(jobs)
	if len(groups) != 1 || len(groups[0]) != 2 || groups[0][0].Year != 2026 || groups[0][1].Year != 2025 {
		t.Fatal("same-code files must run together, newer first", groups)
	}
	dsn := os.Getenv("PULL_MYSQL_DSN")
	if dsn == "" {
		t.Skip("PULL_MYSQL_DSN 未设置；源优先级及分组检查已通过")
	}
	dst, err := marketstore.NewMySQLStore(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer dst.Close()
	code := model.Code{Market: protocol.MarketEtfLof, Code: fmt.Sprintf("etf-priority-%d", time.Now().UnixNano())}
	for n, j := range groups[0] {
		j.Code = code.Code
		r := receipt{}
		if _, err = snapshotImport(dst, j, "", &r); err != nil || !r.Imported {
			t.Fatal("overlapping sources must import without readback failure", r, err)
		}
		if n == 1 && (r.Conflicts != 2 || r.Added != 1) {
			t.Fatal("older unique minute must fill; newer values must survive", r)
		}
	}
	got, err := dst.QueryMinute(code, "", "")
	if err != nil || len(got) != 4 || got[0].Close != 3.521 || got[1].Close != 3.521 || got[2].Time != "2025-01-02 09:33" || got[3].Time != "2026-01-05 09:31" {
		t.Fatal("wrong source priority or missing older minute", got, err)
	}
}

func TestGroupsKeepDifferentCodesIndependent(t *testing.T) {
	jobs := []job{{Code: "sh510020", Year: 2026}, {Code: "sh510020", Year: 2025}, {Code: "sh510030", Year: 2026}, {Code: "sh510030", Year: 2025}}
	groups := groupJobs(jobs)
	if len(groups) != 2 || len(groups[0]) != 2 || len(groups[1]) != 2 || groups[0][0].Code == groups[1][0].Code {
		t.Fatal("code groups must not split or merge", groups)
	}
}
