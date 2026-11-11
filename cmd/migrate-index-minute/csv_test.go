package main

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/injoyai/strategy-tail/internal/marketstore"
	"github.com/injoyai/tdx/v2/extend/model"
	"github.com/injoyai/tdx/v2/protocol"
)

const csvHeader = "日期,时间,开盘,最高,最低,收盘,成交量,成交额\n"
const csvHistory = "2004-12-20,09:31,0.95,0.97,0.95,0.96,1607,153879\n" +
	"2004-12-20,09:31,0.95,0.97,0.95,0.97,1608,153880\n" +
	"2004-12-20,09:32,1.123,1.234,1.012,1.2,2,123.456\n" +
	"2025-12-31,15:00:00,2,2.2,1.9,2.1,7,1456.7\n"

func csvFixture(t *testing.T, text string) job {
	t.Helper()
	file := filepath.Join(t.TempDir(), "sz160105.csv")
	if err := os.WriteFile(file, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	return job{File: file, Code: "sz160105", Market: protocol.MarketEtfLof}
}

func TestCSVFullHistoryUnitsAndYearChunks(t *testing.T) {
	j := csvFixture(t, "\ufeff"+csvHeader+csvHistory)
	before := digest(t, j.File)
	var chunks [][]*model.KlineMinute
	stats, err := readCSVSnapshot(j.File, func(rows []*model.KlineMinute) error {
		chunks = append(chunks, rows)
		return nil
	})
	if err != nil || stats.Rows != 3 || stats.Duplicates != 1 || stats.First != "2004-12-20 09:31" || stats.Last != "2025-12-31 15:00" || !reflect.DeepEqual(stats.Years, []int{2004, 2025}) {
		t.Fatal("history truncated or wrong metadata", stats, err)
	}
	if len(chunks) != 2 || len(chunks[0]) != 2 || len(chunks[1]) != 1 || chunks[0][0].Close != 0.97 || chunks[0][0].Volume != 160800 || chunks[0][1].Open != 1.123 || (chunks[0][1].Amount == nil || *chunks[0][1].Amount != 123.456) || chunks[1][0].Volume != 700 {
		t.Fatal("wrong units, duplicate choice or year grouping", chunks)
	}
	r := receipt{}
	if _, err = snapshotImport(nil, j, "", &r); err != nil || r.Rows != 3 || r.Imported {
		t.Fatal("preview failed", r, err)
	}
	if digest(t, j.File) != before {
		t.Fatal("CSV source was modified")
	}
	files, err := os.ReadDir(filepath.Dir(j.File))
	if err != nil || len(files) != 1 {
		t.Fatal("source directory modified", files, err)
	}
}

func TestCSVRejectsUnconfirmedOrMalformedRows(t *testing.T) {
	valid := "2004-12-20,09:31,1,2,1,1.5,10,1500\n"
	for name, text := range map[string]string{
		"header":     strings.Replace(csvHeader, "成交量,成交额", "成交额,成交量", 1) + valid,
		"negative":   csvHeader + strings.Replace(valid, ",1,2,1,1.5,", ",-1,2,1,1.5,", 1),
		"nan":        csvHeader + strings.Replace(valid, ",1,2,1,1.5,", ",NaN,2,1,1.5,", 1),
		"fractional": csvHeader + strings.Replace(valid, ",10,1500", ",1.5,1500", 1),
		"overflow":   csvHeader + strings.Replace(valid, ",10,1500", fmt.Sprintf(",%d,1500", int64(math.MaxInt64/100+1)), 1),
		"seconds":    csvHeader + strings.Replace(valid, "09:31", "09:31:01", 1),
		"date":       csvHeader + strings.Replace(valid, "2004-12-20", "2004-02-30", 1),
		"descending": csvHeader + strings.Replace(valid, "2004-12-20", "2025-12-31", 1) + valid,
	} {
		t.Run(name, func(t *testing.T) {
			j := csvFixture(t, text)
			if _, err := readCSVSnapshot(j.File, nil); err == nil {
				t.Fatal("invalid CSV accepted")
			}
		})
	}
	for _, text := range []string{"", csvHeader} {
		j := csvFixture(t, text)
		stats, err := readCSVSnapshot(j.File, nil)
		if err != nil || stats.Rows != 0 {
			t.Fatal("empty source not accounted for", stats, err)
		}
	}
}

func TestCSVAutoSelectionRequiresFundMarketAndAllYears(t *testing.T) {
	j := csvFixture(t, csvHeader+csvHistory)
	root := filepath.Dir(j.File)
	jobs, err := listJobs(root, "", 0, 0, protocol.MarketEtfLof)
	if err != nil || len(jobs) != 1 || jobs[0].Year != 0 || jobs[0].Code != "sz160105" {
		t.Fatal(jobs, err)
	}
	if _, err = listJobs(root, "", 0, 2018, protocol.MarketEtfLof); err == nil {
		t.Fatal("CSV year cutoff silently accepted")
	}
	if _, err = listJobs(root, "", 0, 0, protocol.MarketIndex); err == nil {
		t.Fatal("CSV misclassified as index")
	}
}

func TestCSVImportPreservesExistingAndRejectsLateBadRowsBeforeWrite(t *testing.T) {
	dsn := os.Getenv("PULL_MYSQL_DSN")
	if dsn == "" {
		t.Skip("PULL_MYSQL_DSN 未设置")
	}
	dst, err := marketstore.NewMySQLStore(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer dst.Close()
	j := csvFixture(t, csvHeader+csvHistory)
	j.Code = fmt.Sprintf("csv-test-%d", time.Now().UnixNano())
	code := model.Code{Market: j.Market, Code: j.Code}
	prior := &model.KlineMinute{Time: "2004-12-20 09:31", Close: 99, Source: "trade"}
	tail := &model.KlineMinute{Time: "2026-01-05 09:31", Close: 88}
	for year, row := range map[int]*model.KlineMinute{2004: prior, 2026: tail} {
		if err = dst.SaveMinute(code, year, "", []*model.KlineMinute{row}, nil); err != nil {
			t.Fatal(err)
		}
	}
	r := receipt{}
	if _, err = snapshotImport(dst, j, "", &r); err != nil || !r.Imported || r.Added != 2 || r.Conflicts != 1 {
		t.Fatal(r, err)
	}
	got, err := dst.QueryMinute(code, "", "")
	if err != nil || len(got) != 4 || !reflect.DeepEqual(got[0], prior) || !reflect.DeepEqual(got[3], tail) || got[1].Volume != 200 || (got[1].Amount == nil || *got[1].Amount != 123.456) || got[2].Time != "2025-12-31 15:00" || got[2].Volume != 700 {
		t.Fatal("CSV changed old values or units", got, err)
	}
	r = receipt{}
	if _, err = snapshotImport(dst, j, "", &r); err != nil || r.Added != 0 || !r.Imported {
		t.Fatal("CSV was not idempotent", r, err)
	}
	bad := csvFixture(t, csvHeader+strings.Replace(csvHistory, "2.1,7,1456.7", "-2.1,7,1456.7", 1))
	bad.Code = fmt.Sprintf("bad-csv-%d", time.Now().UnixNano())
	r = receipt{}
	if _, err = snapshotImport(dst, bad, "", &r); err == nil || r.Added != 0 || r.Imported {
		t.Fatal("bad late row committed earlier years", r, err)
	}
	got, err = dst.QueryMinute(model.Code{Market: bad.Market, Code: bad.Code}, "", "")
	if err != nil || len(got) != 0 {
		t.Fatal("invalid CSV wrote partial data", got, err)
	}
}
