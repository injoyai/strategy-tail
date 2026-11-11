// migrate-index-minute imports index/ETF minute tables or fund CSVs using local snapshots.
package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"sync"
	"time"

	_ "github.com/glebarez/go-sqlite"
	"github.com/go-sql-driver/mysql"
	common "github.com/injoyai/strategy-tail"
	"github.com/injoyai/strategy-tail/internal/marketstore"
	"github.com/injoyai/tdx/v2/extend/model"
	"github.com/injoyai/tdx/v2/protocol"
)

var indexName = regexp.MustCompile(`^(sh(000[0-9]{3}|999999)|sz399[0-9]{3}|bj899[0-9]{3})\.db$`)
var etfName = regexp.MustCompile(`^(sh(50|51|52|53|56|58)[0-9]{4}|sz(15|16)[0-9]{4})\.db$`)

type job struct {
	File   string
	Year   int
	Code   string
	Market protocol.Market
}
type receipt struct {
	AtUTC      time.Time       `json:"atUTC"`
	Seconds    float64         `json:"seconds"`
	File       string          `json:"file"`
	Code       string          `json:"code"`
	Year       int             `json:"year"`
	Market     protocol.Market `json:"market"`
	Years      []int           `json:"actualYears,omitempty"`
	SHA256     string          `json:"sourceSHA256"`
	Rows       int             `json:"sourceRows"`
	Duplicates int             `json:"duplicateRows"`
	Added      int             `json:"added"`
	Existing   int             `json:"existing"`
	Conflicts  int             `json:"preservedConflicts"`
	First      string          `json:"first"`
	Last       string          `json:"last"`
	Error      string          `json:"error,omitempty"`
	Imported   bool            `json:"imported"`
	Target     string          `json:"target,omitempty"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	stdinDSN := flag.Bool("config-stdin", false, "从标准输入读取私有 JSON DSN，不落盘或进入参数")
	fromYear := flag.Int("start-year", 0, "只导入该年起的年度库；0 表示全部")
	marketArg := flag.String("market", protocol.MarketIndex.String(), "目标市场：cn/index 或 cn/etf；ETF 文件中的跨年记录按实际年份存储")
	dir := flag.String("dir", "", "分钟源：<年份>/<代码>.db 或基金 <代码>.csv；只读访问")
	workers := flag.Int("workers", 2, "并发文件数（1..4）")
	code := flag.String("code", "", "仅导入指定原始代码")
	year := flag.Int("year", 0, "仅导入指定年份目录；ETF 跨年记录仍按实际年份存储")
	preview := flag.Bool("preview", false, "只校验源快照，不连接或写入 MySQL")
	resume := flag.String("resume-log", "", "跳过该 JSONL 中已成功导入且源内容哈希未变的文件")
	flag.Parse()
	market := protocol.Market(*marketArg)
	if market != protocol.MarketIndex && market != protocol.MarketEtfLof {
		return errors.New("market 仅支持 cn/index 或 cn/etf")
	}
	if *fromYear != 0 && (*fromYear < 1990 || *fromYear > 2100) {
		return errors.New("start-year 应为 1990..2100 或 0")
	}
	if *dir == "" || *workers < 1 || *workers > 4 {
		return errors.New("必须设置 -dir；workers 为 1..4")
	}
	jobs, err := listJobs(*dir, *code, *year, *fromYear, market)
	if err != nil {
		return err
	}
	if len(jobs) == 0 {
		return errors.New("没有匹配的分钟源文件")
	}
	completed := map[string]string{}
	completedTargets := map[string]string{}
	if *resume != "" {
		f, e := os.Open(*resume)
		if e != nil {
			return e
		}
		defer f.Close()
		decoder := json.NewDecoder(f)
		for {
			var r receipt
			e = decoder.Decode(&r)
			if errors.Is(e, io.EOF) {
				break
			}
			if e != nil {
				return e
			}
			matchesMarket := r.Market == market || (r.Market == "" && market == protocol.MarketIndex)
			if r.File != "" && r.Error == "" && r.SHA256 != "" && r.Imported && matchesMarket {
				completed[r.File] = r.SHA256
				completedTargets[r.File] = r.Target
			}
		}
	}
	var dst *marketstore.MySQLStore
	if !*preview {
		var dsn string
		var e error
		if *stdinDSN {
			e = json.NewDecoder(os.Stdin).Decode(&dsn)
		} else {
			dsn, e = common.MySQLDSN()
		}
		if e != nil {
			return errors.New("不能读取私有目标配置")
		}
		cfg, e := mysql.ParseDSN(dsn)
		if e != nil {
			return e
		}
		cfg.ReadTimeout = 5 * time.Minute
		dst, e = marketstore.NewMySQLImportStore(cfg.FormatDSN())
		if e != nil {
			return e
		}
		defer dst.Close()
	}
	for file := range completed {
		if dst == nil || completedTargets[file] != dst.Identity() {
			delete(completed, file)
		}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	tasks := make(chan []job)
	results := make(chan receipt)
	var wg sync.WaitGroup
	for i := 0; i < *workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for group := range tasks {
				for _, j := range group {
					if ctx.Err() != nil {
						break
					}
					r := receipt{File: j.File, Code: j.Code, Year: j.Year, Market: j.Market}
					hash, err := snapshotImport(dst, j, completed[j.File], &r)
					r.SHA256 = hash
					if err != nil {
						r.Error = err.Error()
					}
					results <- r
				}
			}
		}()
	}
	go func() {
		defer close(tasks)
		for _, group := range groupJobs(jobs) {
			select {
			case tasks <- group:
			case <-ctx.Done():
				return
			}
		}
	}()
	go func() { wg.Wait(); close(results) }()
	start := time.Now()
	enc := json.NewEncoder(os.Stdout)
	done, added, failed := 0, 0, 0
	for r := range results {
		r.AtUTC, r.Seconds = time.Now().UTC(), time.Since(start).Seconds()
		done++
		added += r.Added
		if r.Error != "" {
			failed++
			stop()
		}
		if err := enc.Encode(r); err != nil {
			return err
		}
		if done%25 == 0 || r.Error != "" {
			fmt.Fprintf(os.Stderr, "files=%d/%d added=%d failed=%d elapsed=%s\n", done, len(jobs), added, failed, time.Since(start).Round(time.Second))
		}
	}
	fmt.Fprintf(os.Stderr, "complete files=%d added=%d failed=%d elapsed=%s\n", done, added, failed, time.Since(start).Round(time.Second))
	if failed > 0 {
		return fmt.Errorf("%d 个文件失败，详见逐库回执", failed)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return nil
}

// A worker owns all files for one code, so overlapping years cannot race.
func groupJobs(jobs []job) [][]job {
	var groups [][]job
	for _, j := range jobs {
		if len(groups) == 0 || groups[len(groups)-1][0].Code != j.Code {
			groups = append(groups, []job{j})
		} else {
			groups[len(groups)-1] = append(groups[len(groups)-1], j)
		}
	}
	return groups
}

func listJobs(root, filter string, yearFilter, fromYear int, market protocol.Market) ([]job, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	var out []job
	namePattern := indexName
	if market == protocol.MarketEtfLof {
		namePattern = etfName
	}
	for _, entry := range entries {
		if !entry.IsDir() && filepath.Ext(entry.Name()) == ".csv" {
			name := entry.Name()[:len(entry.Name())-4]
			if market != protocol.MarketEtfLof || !entry.Type().IsRegular() || !etfName.MatchString(name+".db") {
				return nil, fmt.Errorf("非预期基金 CSV：%s", entry.Name())
			}
			if filter != "" && filter != name {
				continue
			}
			if yearFilter != 0 || fromYear != 0 {
				return nil, errors.New("基金 CSV 按全部年份处理，year 和 start-year 必须为 0")
			}
			out = append(out, job{File: filepath.Join(root, entry.Name()), Code: name, Market: market})
			continue
		}
		if !entry.IsDir() {
			continue
		}
		year, e := strconv.Atoi(entry.Name())
		if e != nil || len(entry.Name()) != 4 || year < 1990 || year > 2100 {
			continue
		}
		if yearFilter != 0 && year != yearFilter {
			continue
		}
		if fromYear != 0 && year < fromYear {
			continue
		}
		files, e := os.ReadDir(filepath.Join(root, entry.Name()))
		if e != nil {
			return nil, e
		}
		for _, file := range files {
			if !file.Type().IsRegular() || !namePattern.MatchString(file.Name()) {
				return nil, fmt.Errorf("非预期源文件：%s/%s", entry.Name(), file.Name())
			}
			code := file.Name()[:len(file.Name())-3]
			if filter != "" && filter != code {
				continue
			}
			out = append(out, job{File: filepath.Join(root, entry.Name(), file.Name()), Year: year, Code: code, Market: market})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Code != out[j].Code {
			return out[i].Code < out[j].Code
		}
		if market == protocol.MarketEtfLof {
			return out[i].Year > out[j].Year
		}
		return out[i].Year < out[j].Year
	})
	return out, nil
}
func noJournal(file string) error {
	for _, suffix := range []string{"-wal", "-journal"} {
		s, e := os.Stat(file + suffix)
		if e == nil && s.Size() > 0 {
			return fmt.Errorf("源库有活动日志 %s，停止复制", suffix)
		}
		if e != nil && !os.IsNotExist(e) {
			return e
		}
	}
	return nil
}
func snapshotImport(dst *marketstore.MySQLStore, j job, completed string, r *receipt) (string, error) {
	r.Market = j.Market
	if dst != nil {
		r.Target = dst.Identity()
	}
	if err := noJournal(j.File); err != nil {
		return "", err
	}
	src, err := os.Open(j.File)
	if err != nil {
		return "", err
	}
	defer src.Close()
	before, err := src.Stat()
	if err != nil {
		return "", err
	}
	if !before.Mode().IsRegular() {
		return "", errors.New("源文件不是普通文件")
	}
	temp, err := os.CreateTemp("", "index-minute-snapshot-*.db")
	if err != nil {
		return "", err
	}
	defer os.Remove(temp.Name())
	h := sha256.New()
	n, copyErr := io.Copy(io.MultiWriter(temp, h), src)
	closeErr := temp.Close()
	if copyErr != nil {
		return "", copyErr
	}
	if closeErr != nil {
		return "", closeErr
	}
	after, err := os.Stat(j.File)
	if err != nil {
		return "", err
	}
	if n != before.Size() || !os.SameFile(before, after) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		return "", errors.New("复制期间源库发生变化，未导入")
	}
	if err = noJournal(j.File); err != nil {
		return "", err
	}
	hash := hex.EncodeToString(h.Sum(nil))
	if _, err = src.Seek(0, io.SeekStart); err != nil {
		return hash, err
	}
	checkHash := sha256.New()
	if _, err = io.Copy(checkHash, src); err != nil {
		return hash, err
	}
	if hash != hex.EncodeToString(checkHash.Sum(nil)) {
		return hash, errors.New("复制后源内容哈希变化，未导入")
	}
	if err = noJournal(j.File); err != nil {
		return hash, err
	}
	if completed == hash {
		r.Imported = true
		return hash, nil
	}
	if filepath.Ext(j.File) == ".csv" {
		if j.Market != protocol.MarketEtfLof {
			return hash, errors.New("CSV 仅支持基金市场")
		}
		// Validate the entire frozen file before any year is committed.
		stats, err := readCSVSnapshot(temp.Name(), nil)
		if err != nil {
			return hash, err
		}
		r.Rows, r.Duplicates, r.First, r.Last, r.Years = stats.Rows, stats.Duplicates, stats.First, stats.Last, stats.Years
		if dst == nil {
			return hash, nil
		}
		if _, err = readCSVSnapshot(temp.Name(), func(rows []*model.KlineMinute) error {
			return importMinuteRows(dst, j, rows, r)
		}); err != nil {
			return hash, err
		}
		r.Imported = true
		return hash, nil
	}
	rows, duplicates, err := readSnapshot(temp.Name(), j.Year, j.Market)
	if err != nil {
		return hash, err
	}
	r.Rows = len(rows)
	r.Duplicates = duplicates
	if len(rows) == 0 {
		r.Imported = dst != nil
		return hash, nil
	}
	r.First = rows[0].Time
	r.Last = rows[len(rows)-1].Time
	for _, k := range rows {
		year, _ := strconv.Atoi(k.Time[:4])
		if len(r.Years) == 0 || r.Years[len(r.Years)-1] != year {
			r.Years = append(r.Years, year)
		}
	}
	if dst == nil {
		return hash, nil
	}
	if err = importMinuteRows(dst, j, rows, r); err != nil {
		return hash, err
	}
	r.Imported = true
	return hash, nil
}

func importMinuteRows(dst *marketstore.MySQLStore, j job, rows []*model.KlineMinute, r *receipt) error {
	if len(rows) == 0 {
		return nil
	}
	code := model.Code{Market: j.Market, Code: j.Code}
	// Preserve all existing keys, including different values; verify the full imported interval.
	first, last := rows[0].Time, rows[len(rows)-1].Time
	old, err := dst.QueryMinute(code, first, last)
	if err != nil {
		return err
	}
	oldByTime := map[string]*model.KlineMinute{}
	for _, k := range old {
		oldByTime[k.Time] = k
	}
	r.Existing += len(old)
	for begin := 0; begin < len(rows); {
		year, _ := strconv.Atoi(rows[begin].Time[:4])
		end := begin + 1
		for end < len(rows) && rows[end].Time[:4] == rows[begin].Time[:4] {
			end++
		}
		added, e := dst.ImportMinute(code, year, rows[begin:end], nil)
		r.Added += added
		if e != nil {
			return e
		}
		begin = end
	}
	actual, err := dst.QueryMinute(code, first, last)
	if err != nil {
		return err
	}
	byTime := map[string]*model.KlineMinute{}
	for _, k := range actual {
		byTime[k.Time] = k
	}
	for _, want := range rows {
		if previous := oldByTime[want.Time]; previous != nil {
			if got := byTime[want.Time]; got == nil || !reflect.DeepEqual(previous, got) {
				return fmt.Errorf("目标已有记录发生变化：%s", want.Time)
			}
			if !reflect.DeepEqual(previous, want) {
				r.Conflicts++
			}
		} else if got := byTime[want.Time]; got == nil || !reflect.DeepEqual(got, want) {
			return fmt.Errorf("目标缺失或内容不一致：%s", want.Time)
		}
	}
	return nil
}
func readSnapshot(file string, year int, market protocol.Market) ([]*model.KlineMinute, int, error) {
	if market != protocol.MarketIndex && market != protocol.MarketEtfLof {
		return nil, 0, errors.New("不支持的源市场")
	}
	marketInfo, _ := market.Info()
	u := url.URL{Scheme: "file", Path: file}
	q := u.Query()
	q.Set("mode", "ro")
	q.Set("immutable", "1")
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, 0, err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	var check string
	if err = db.QueryRow("PRAGMA quick_check").Scan(&check); err != nil {
		return nil, 0, err
	}
	if check != "ok" {
		return nil, 0, fmt.Errorf("快照不完整：%s", check)
	}
	rs, err := db.Query(`SELECT Date,Year,Month,Day,Hour,Minute,Open,High,Low,Close,Volume,Amount FROM KlineMinute1 ORDER BY Date,ID`)
	if err != nil {
		return nil, 0, err
	}
	defer rs.Close()
	out := []*model.KlineMinute{}
	duplicates := 0
	for rs.Next() {
		var epoch int64
		var y, month, day, hour, minute int
		k := new(model.KlineMinute)
		if err = rs.Scan(&epoch, &y, &month, &day, &hour, &minute, &k.Open, &k.High, &k.Low, &k.Close, &k.Volume, &k.Amount); err != nil {
			return nil, 0, err
		}
		at := time.Unix(epoch, 0).In(protocol.Location())
		ay, am, ad := at.Date()
		ah, amin, sec := at.Clock()
		if (market == protocol.MarketIndex && y != year) || y < 1990 || y > 2100 || ay != y || int(am) != month || ad != day || ah != hour || amin != minute || sec != 0 {
			return nil, 0, fmt.Errorf("日期字段不一致：%d %d-%d-%d %d:%d", epoch, y, month, day, hour, minute)
		}
		if k.Amount == nil {
			return nil, 0, errors.New("旧分钟源成交额为空")
		}
		for _, v := range []float64{k.Open, k.High, k.Low, k.Close, *k.Amount} {
			if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
				return nil, 0, errors.New("价格或金额非法")
			}
		}
		k.Volume, err = marketInfo.Minute.ToStorageVolume(k.Volume)
		if err != nil {
			return nil, 0, err
		}
		k.Time = at.Format(protocol.TimeLayout)
		if len(out) > 0 && out[len(out)-1].Time == k.Time {
			out[len(out)-1] = k
			duplicates++
		} else {
			out = append(out, k)
		}
	}
	return out, duplicates, rs.Err()
}
