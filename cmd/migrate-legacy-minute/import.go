package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"reflect"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/injoyai/strategy-tail/internal/marketstore"
	"github.com/injoyai/strategy-tail/lib/extend"
	"github.com/injoyai/tdx/v2/extend/model"
)

type receipt struct {
	AtUTC       time.Time     `json:"atUTC"`
	Seconds     float64       `json:"seconds"`
	Quarantined []isolatedRow `json:"quarantinedRecords,omitempty"`
	File        string        `json:"file"`
	Code        string        `json:"code"`
	Year        int           `json:"year"`
	Target      string        `json:"target"`
	SHA256      string        `json:"sourceSHA256"`
	SourceRows  int           `json:"sourceRows"`
	Dropped     int           `json:"dropped"`
	Existing    int           `json:"existingMinutes"`
	Missing     int           `json:"missingBefore"`
	Rows        int           `json:"rows"`
	Skipped     bool          `json:"skipped"`
	Invalid     bool          `json:"invalid"`
	Resumed     bool          `json:"resumed"`
	Verified    bool          `json:"verified"`
	First       string        `json:"first,omitempty"`
	Last        string        `json:"last,omitempty"`
	Error       string        `json:"error,omitempty"`
	Done        int           `json:"done"`
	Total       int           `json:"total"`
}

func openMinuteKeys(dsn string, workers int) (*sql.DB, error) {
	c, err := mysql.ParseDSN(dsn)
	if err != nil {
		return nil, err
	}
	c.ParseTime = false
	if c.Params == nil {
		c.Params = map[string]string{}
	}
	c.Params["transaction_read_only"] = "1"
	db, err := sql.Open("mysql", c.FormatDSN())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(workers)
	db.SetMaxIdleConns(workers)
	db.SetConnMaxLifetime(time.Hour)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err = db.PingContext(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}
func minuteKeys(db *sql.DB, j job) (map[string]bool, error) {
	result := map[string]bool{}
	if j.year < 1990 || j.year > 2100 {
		return nil, errors.New("非法年份")
	}
	table, err := marketstore.MySQLMinuteTable(j.code.Market, j.year)
	if err != nil {
		return nil, err
	}
	rs, err := db.Query("SELECT DATE_FORMAT(`time`,'%Y-%m-%d %H:%i') FROM `"+table+"` WHERE `code`=? ORDER BY `time`", j.code.Code)
	if err != nil {
		var me *mysql.MySQLError
		if errors.As(err, &me) && me.Number == 1146 {
			return result, nil
		}
		return nil, err
	}
	defer rs.Close()
	for rs.Next() {
		var t string
		if err = rs.Scan(&t); err != nil {
			return nil, err
		}
		result[t] = true
	}
	return result, rs.Err()
}
func migrateLegacyMinute(dst *marketstore.MySQLStore, keys *sql.DB, j job, previous receipt, policy sourcePolicy) (receipt, error) {
	r := receipt{File: j.file, Code: j.code.Key(), Year: j.year, Target: dst.Identity()}
	snapshot, hash, err := copyMinuteSource(j.file)
	if err != nil {
		return r, err
	}
	defer os.Remove(snapshot)
	r.SHA256 = hash
	if previous.Verified && previous.Target == r.Target && previous.SHA256 == hash {
		r = previous
		r.Rows = 0
		r.Skipped = true
		r.Resumed = true
		return r, nil
	}
	knownDay := false
	for _, rule := range policy.BadDays {
		if rule.Code == j.code.Code && rule.Date[:4] == fmt.Sprint(j.year) {
			knownDay = true
			break
		}
	}
	if knownDay {
		r.Quarantined, err = isolateSnapshot(snapshot, j, hash, policy)
		if err != nil {
			return r, err
		}
	}
	data, err := extend.ReadLegacyMinuteSource(snapshot, j.code, j.year)
	if !knownDay && policy.InvalidNumericRows && (err != nil || data.Dropped > 0) {
		r.Quarantined, err = isolateSnapshot(snapshot, j, hash, policy)
		if err != nil {
			return r, err
		}
		data, err = extend.ReadLegacyMinuteSource(snapshot, j.code, j.year)
	}
	if err != nil {
		r.Invalid = errors.Is(err, extend.ErrLegacyMinuteCorrupt)
		return r, err
	}
	r.SourceRows = len(data.Rows)
	r.Dropped = data.Dropped
	if r.Dropped > 0 {
		return r, fmt.Errorf("源库有 %d 条异常记录，未导入该库", r.Dropped)
	}
	if len(data.Rows) == 0 {
		r.Skipped = true
		r.Verified = true
		return r, nil
	}
	r.First = data.Rows[0].Time
	r.Last = data.Rows[len(data.Rows)-1].Time
	stored, err := minuteKeys(keys, j)
	if err != nil {
		return r, err
	}
	missing := make([]*model.KlineMinute, 0)
	for _, k := range data.Rows {
		if stored[k.Time] {
			r.Existing++
		} else {
			missing = append(missing, k)
		}
	}
	r.Missing = len(missing)
	if len(missing) == 0 {
		r.Skipped = true
		r.Verified = true
		return r, nil
	}
	// Existing timestamps never enter the import batch; the SDK also preserves keys inserted concurrently.
	r.Rows, err = dst.ImportMinute(j.code, j.year, missing, nil)
	if err != nil {
		return r, err
	}
	got, err := dst.QueryMinute(j.code, missing[0].Time, missing[len(missing)-1].Time)
	if err != nil {
		return r, err
	}
	byTime := map[string]*model.KlineMinute{}
	for _, k := range got {
		byTime[k.Time] = k
	}
	for _, want := range missing {
		actual := byTime[want.Time]
		if actual == nil || !reflect.DeepEqual(actual, want) {
			return r, fmt.Errorf("新增记录读回不一致：%s", want.Time)
		}
	}
	r.Verified = true
	return r, nil
}
func checkSourceJournal(file string) error {
	for _, suffix := range []string{"-wal", "-journal"} {
		s, e := os.Stat(file + suffix)
		if e == nil && s.Size() > 0 {
			return fmt.Errorf("源库有活动日志 %s", suffix)
		}
		if e != nil && !os.IsNotExist(e) {
			return e
		}
	}
	return nil
}
func copyMinuteSource(file string) (string, string, error) {
	if err := checkSourceJournal(file); err != nil {
		return "", "", err
	}
	info, err := os.Lstat(file)
	if err != nil {
		return "", "", err
	}
	if !info.Mode().IsRegular() {
		return "", "", errors.New("源不是普通文件")
	}
	src, err := os.Open(file)
	if err != nil {
		return "", "", err
	}
	defer src.Close()
	temp, err := os.CreateTemp("", "stock-minute-snapshot-*.db")
	if err != nil {
		return "", "", err
	}
	ok := false
	defer func() {
		if !ok {
			os.Remove(temp.Name())
		}
	}()
	h := sha256.New()
	n, copyErr := io.Copy(io.MultiWriter(temp, h), src)
	closeErr := temp.Close()
	if copyErr != nil {
		return "", "", copyErr
	}
	if closeErr != nil {
		return "", "", closeErr
	}
	current, err := os.Stat(file)
	if err != nil {
		return "", "", err
	}
	if n != info.Size() || !os.SameFile(info, current) || info.Size() != current.Size() || !info.ModTime().Equal(current.ModTime()) {
		return "", "", errors.New("源文件复制期间发生变化")
	}
	digest := hex.EncodeToString(h.Sum(nil))
	if _, err = src.Seek(0, io.SeekStart); err != nil {
		return "", "", err
	}
	again := sha256.New()
	if _, err = io.Copy(again, src); err != nil {
		return "", "", err
	}
	if digest != hex.EncodeToString(again.Sum(nil)) {
		return "", "", errors.New("源文件复读哈希不一致")
	}
	if err = checkSourceJournal(file); err != nil {
		return "", "", err
	}
	u := url.URL{Scheme: "file", Path: temp.Name()}
	q := u.Query()
	q.Set("mode", "ro")
	q.Set("immutable", "1")
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return "", "", err
	}
	var integrity string
	err = db.QueryRow("PRAGMA quick_check").Scan(&integrity)
	db.Close()
	if err != nil {
		return "", "", err
	}
	if integrity != "ok" {
		return "", "", fmt.Errorf("源快照完整性校验失败：%s", integrity)
	}
	ok = true
	return temp.Name(), digest, nil
}
