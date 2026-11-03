package extend

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/injoyai/tdx/v2/protocol"
)

const minuteStoreVersion = 102
const minuteRecordSize = 96
const minuteBlockLimit = 100000 * minuteRecordSize

var minuteMagic = []byte{'K', 'L', 'M', '2', 96, 0, 0, 0}
var minuteZone, minuteZoneError = time.LoadLocation("Asia/Shanghai")

type minuteRecord struct {
	Sequence uint64
	Bar      *protocol.Kline
}

type minuteQuery interface {
	Query(string, ...any) (*sql.Rows, error)
	QueryRow(string, ...any) *sql.Row
}

func minuteDay(t time.Time) int {
	y, m, d := t.In(minuteZone).Date()
	return y*10000 + int(m)*100 + d
}

func appendMinuteRecord(raw []byte, r minuteRecord) ([]byte, error) {
	k := r.Bar
	if k == nil || k.Time.Nanosecond() != 0 {
		return nil, fmt.Errorf("分钟压缩只接受非空、整秒时间记录")
	}
	for _, v := range []float64{k.Last, k.Open, k.High, k.Low, k.Close, k.Amount} {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return nil, fmt.Errorf("分钟价格/金额必须为有限值")
		}
	}
	for _, v := range [...]uint64{r.Sequence, math.Float64bits(k.Last), math.Float64bits(k.Open), math.Float64bits(k.High), math.Float64bits(k.Low), math.Float64bits(k.Close), uint64(k.Order), uint64(k.Volume), math.Float64bits(k.Amount), uint64(k.Time.Unix()), uint64(k.UpCount), uint64(k.DownCount)} {
		raw = binary.LittleEndian.AppendUint64(raw, v)
	}
	return raw, nil
}

func encodeMinuteBlock(records []minuteRecord, writer **gzip.Writer) ([]byte, error) {
	if len(records)*minuteRecordSize > minuteBlockLimit {
		return nil, fmt.Errorf("单日分钟记录超过压缩格式上限")
	}
	raw := append([]byte(nil), minuteMagic...)
	var err error
	for _, r := range records {
		raw, err = appendMinuteRecord(raw, r)
		if err != nil {
			return nil, err
		}
	}
	var buffer bytes.Buffer
	if *writer == nil {
		*writer, err = gzip.NewWriterLevel(&buffer, 6)
	} else {
		(*writer).Reset(&buffer)
	}
	if err != nil {
		return nil, err
	}
	if _, err = (*writer).Write(raw); err != nil {
		return nil, err
	}
	if err = (*writer).Close(); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

func decodeMinuteBlock(data []byte, reader **gzip.Reader) ([]minuteRecord, error) {
	var err error
	if *reader == nil {
		*reader, err = gzip.NewReader(bytes.NewReader(data))
	} else {
		err = (*reader).Reset(bytes.NewReader(data))
	}
	if err != nil {
		return nil, err
	}
	raw, err := io.ReadAll(io.LimitReader(*reader, int64(minuteBlockLimit+len(minuteMagic)+1)))
	closeErr := (*reader).Close()
	if err != nil {
		return nil, err
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if len(raw) < len(minuteMagic) || len(raw) > minuteBlockLimit+len(minuteMagic) || !(bytes.Equal(raw[:len(minuteMagic)], minuteMagic) || bytes.Equal(raw[:len(minuteMagic)], []byte{'K', 'L', 'M', '1', 96, 0, 0, 0})) || (len(raw)-len(minuteMagic))%minuteRecordSize != 0 {
		return nil, fmt.Errorf("无效的分钟压缩块或不支持的版本")
	}
	records := make([]minuteRecord, 0, (len(raw)-len(minuteMagic))/minuteRecordSize)
	for offset := len(minuteMagic); offset < len(raw); offset += minuteRecordSize {
		var v [12]int64
		for i := range v {
			v[i] = int64(binary.LittleEndian.Uint64(raw[offset+i*8:]))
		}
		if v[0] < 0 {
			return nil, fmt.Errorf("分钟记录序号为负")
		}
		k := &protocol.Kline{Last: float64(v[1]) / 1000.0, Open: float64(v[2]) / 1000.0, High: float64(v[3]) / 1000.0, Low: float64(v[4]) / 1000.0, Close: float64(v[5]) / 1000.0, Order: int(v[6]), Volume: v[7], Amount: float64(v[8]) / 1000.0, Time: time.Unix(v[9], 0).In(protocol.Location()), UpCount: int(v[10]), DownCount: int(v[11])}
		if raw[3] == '2' {
			k.Last = math.Float64frombits(uint64(v[1]))
			k.Open = math.Float64frombits(uint64(v[2]))
			k.High = math.Float64frombits(uint64(v[3]))
			k.Low = math.Float64frombits(uint64(v[4]))
			k.Close = math.Float64frombits(uint64(v[5]))
			k.Amount = math.Float64frombits(uint64(v[8]))
		}
		records = append(records, minuteRecord{uint64(v[0]), k})
	}
	return records, nil
}

func initMinuteStore(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, query := range []string{
		"CREATE TABLE KlineBlock (Day INTEGER PRIMARY KEY, Data BLOB NOT NULL)",
		"CREATE TABLE KlineMeta (ID INTEGER PRIMARY KEY CHECK(ID=1), NextSequence INTEGER NOT NULL, LastUnix INTEGER NOT NULL, RecordCount INTEGER NOT NULL)",
		"INSERT INTO KlineMeta VALUES (1,1,0,0)",
		"PRAGMA user_version=102",
	} {
		if _, err = tx.Exec(query); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func minuteVersion(q minuteQuery) (int, error) {
	var version int
	err := q.QueryRow("PRAGMA user_version").Scan(&version)
	if err == nil && version != 0 && version != 101 && version != minuteStoreVersion {
		err = fmt.Errorf("不支持的分钟库版本 %d", version)
	}
	return version, err
}

func readMinuteRecords(q minuteQuery, version int) ([]minuteRecord, error) {
	if version == 0 {
		legacy, err := legacyPriceScale(q)
		if err != nil {
			return nil, err
		}
		rows, err := q.Query("SELECT rowid,Last,Open,High,Low,Close,`Order`,Volume,Amount,Time,UpCount,DownCount FROM Kline ORDER BY rowid")
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		records := make([]minuteRecord, 0, 60000)
		for rows.Next() {
			k := new(protocol.Kline)
			var seq int64
			var at time.Time
			if err = rows.Scan(&seq, &k.Last, &k.Open, &k.High, &k.Low, &k.Close, &k.Order, &k.Volume, &k.Amount, &at, &k.UpCount, &k.DownCount); err != nil {
				return nil, err
			}
			if seq < 0 {
				return nil, fmt.Errorf("分钟库包含负 rowid")
			}
			k.Time = at.In(protocol.Location())
			if legacy {
				scaleBar(k, .001)
			}
			records = append(records, minuteRecord{uint64(seq), k})
		}
		return records, rows.Err()
	}
	rows, err := q.Query("SELECT Data FROM KlineBlock ORDER BY Day")
	if err != nil {
		return nil, err
	}
	var records []minuteRecord
	var reader *gzip.Reader
	for rows.Next() {
		var data []byte
		if err = rows.Scan(&data); err != nil {
			rows.Close()
			return nil, err
		}
		part, err := decodeMinuteBlock(data, &reader)
		if err != nil {
			rows.Close()
			return nil, err
		}
		records = append(records, part...)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	sort.Slice(records, func(i, j int) bool { return records[i].Sequence < records[j].Sequence })
	var count int
	var next int64
	if err = q.QueryRow("SELECT RecordCount,NextSequence FROM KlineMeta WHERE ID=1").Scan(&count, &next); err != nil {
		return nil, err
	}
	if count != len(records) || next < 1 {
		return nil, fmt.Errorf("分钟压缩库记录数或元数据不一致")
	}
	for i, r := range records {
		if r.Sequence >= uint64(next) || (i > 0 && r.Sequence == records[i-1].Sequence) {
			return nil, fmt.Errorf("分钟压缩库记录序号不一致")
		}
	}
	return records, nil
}

func readMinuteFile(filename string) (protocol.Klines, error) {
	db, err := sql.Open("sqlite", filename)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	// Keep one snapshot open across format detection and reading during atomic migration.
	tx, err := db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	version, err := minuteVersion(tx)
	if err != nil {
		return nil, err
	}
	records, err := readMinuteRecords(tx, version)
	if err != nil {
		return nil, err
	}
	ks := make(protocol.Klines, len(records))
	for i, r := range records {
		ks[i] = r.Bar
	}
	return ks, nil
}

func writeMinuteGroups(tx *sql.Tx, groups map[int][]minuteRecord) error {
	if minuteZoneError != nil {
		return minuteZoneError
	}
	var writer *gzip.Writer
	days := make([]int, 0, len(groups))
	for day := range groups {
		days = append(days, day)
	}
	sort.Ints(days)
	for _, day := range days {
		records := groups[day]
		if len(records) == 0 {
			if _, err := tx.Exec("DELETE FROM KlineBlock WHERE Day=?", day); err != nil {
				return err
			}
			continue
		}
		data, err := encodeMinuteBlock(records, &writer)
		if err != nil {
			return err
		}
		if _, err = tx.Exec("INSERT OR REPLACE INTO KlineBlock (Day,Data) VALUES (?,?)", day, data); err != nil {
			return err
		}
	}
	return nil
}

// MinuteCompression describes one verified replacement; records and order are preserved.
type MinuteCompression struct {
	Changed     bool
	Records     int
	BeforeBytes int64
	AfterBytes  int64
	SHA256      string
}

func minuteDigest(records []minuteRecord) ([32]byte, error) {
	h := sha256.New()
	var buf []byte
	for _, r := range records {
		var err error
		buf, err = appendMinuteRecord(buf[:0], r)
		if err != nil {
			return [32]byte{}, err
		}
		h.Write(buf)
	}
	var sum [32]byte
	copy(sum[:], h.Sum(nil))
	return sum, nil
}

// CompressMinuteDatabase atomically replaces one legacy file after a full decode
// and integrity check. The caller must hold the data directory's update lock.
func CompressMinuteDatabase(filename string) (result MinuteCompression, err error) {
	if minuteZoneError != nil {
		return result, minuteZoneError
	}
	info, err := os.Lstat(filename)
	if err != nil {
		return result, err
	}
	if !info.Mode().IsRegular() {
		return result, fmt.Errorf("只转换普通数据库文件")
	}
	result.BeforeBytes = info.Size()
	result.AfterBytes = info.Size()
	db, err := sql.Open("sqlite", filename)
	if err != nil {
		return result, err
	}
	defer db.Close()
	tx, err := db.Begin()
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	version, err := minuteVersion(tx)
	if err != nil {
		return result, err
	}
	if version == minuteStoreVersion || version == 101 {
		return result, nil
	}
	var objects int
	if err = tx.QueryRow("SELECT count(*) FROM sqlite_master WHERE NOT (type='table' AND name='Kline')").Scan(&objects); err != nil {
		return result, err
	}
	if objects != 0 {
		return result, fmt.Errorf("旧分钟库包含其他对象，保留原文件: %s", filename)
	}
	var mode string
	if err = tx.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil {
		return result, err
	}
	if mode != "delete" {
		return result, fmt.Errorf("旧分钟库不是 DELETE 日志模式: %s", mode)
	}
	var integrity string
	if err = tx.QueryRow("PRAGMA integrity_check").Scan(&integrity); err != nil {
		return result, err
	}
	if integrity != "ok" {
		return result, fmt.Errorf("原库完整性检查失败: %s", integrity)
	}
	// Refuse extra fields instead of silently discarding them.
	columns, err := tx.Query("PRAGMA table_info(Kline)")
	if err != nil {
		return result, err
	}
	var names []string
	for columns.Next() {
		var id, notnull, pk int
		var name, typ string
		var def any
		if err = columns.Scan(&id, &name, &typ, &notnull, &def, &pk); err != nil {
			columns.Close()
			return result, err
		}
		names = append(names, name)
	}
	err = columns.Err()
	columns.Close()
	if err != nil {
		return result, err
	}
	if fmt.Sprint(names) != "[Last Open High Low Close Order Volume Amount Time UpCount DownCount]" {
		return result, fmt.Errorf("旧分钟库字段不匹配: %v", names)
	}
	records, err := readMinuteRecords(tx, 0)
	if err != nil {
		return result, err
	}
	want, err := minuteDigest(records)
	if err != nil {
		return result, err
	}
	result.Records = len(records)
	result.SHA256 = fmt.Sprintf("%x", want)
	tmp, err := os.CreateTemp(filepath.Dir(filename), ".kline-compress-*.db")
	if err != nil {
		return result, err
	}
	tempPath := tmp.Name()
	tmp.Close()
	defer os.Remove(tempPath)
	out, err := sql.Open("sqlite", tempPath)
	if err != nil {
		return result, err
	}
	defer out.Close()
	if err = initMinuteStore(out); err != nil {
		return result, err
	}
	wtx, err := out.Begin()
	if err != nil {
		return result, err
	}
	defer wtx.Rollback()
	groups := map[int][]minuteRecord{}
	var next uint64 = 1
	var lastUnix int64
	for i, r := range records {
		day := minuteDay(r.Bar.Time)
		groups[day] = append(groups[day], r)
		if r.Sequence >= next {
			next = r.Sequence + 1
		}
		if i == 0 || r.Bar.Time.Unix() > lastUnix {
			lastUnix = r.Bar.Time.Unix()
		}
	}
	if next > uint64(^uint64(0)>>1) {
		return result, fmt.Errorf("分钟记录序号溢出")
	}
	if err = writeMinuteGroups(wtx, groups); err != nil {
		return result, err
	}
	if _, err = wtx.Exec("UPDATE KlineMeta SET NextSequence=?,LastUnix=?,RecordCount=? WHERE ID=1", int64(next), lastUnix, len(records)); err != nil {
		return result, err
	}
	if err = wtx.Commit(); err != nil {
		return result, err
	}
	if err = out.QueryRow("PRAGMA integrity_check").Scan(&integrity); err != nil {
		return result, err
	}
	if integrity != "ok" {
		return result, fmt.Errorf("压缩库完整性检查失败: %s", integrity)
	}
	got, err := readMinuteRecords(out, minuteStoreVersion)
	if err != nil {
		return result, err
	}
	actual, err := minuteDigest(got)
	if err != nil {
		return result, err
	}
	if actual != want || len(got) != len(records) {
		return result, fmt.Errorf("压缩前后数据校验不一致")
	}
	if err = out.Close(); err != nil {
		return result, err
	}
	if err = os.Chmod(tempPath, info.Mode().Perm()); err != nil {
		return result, err
	}
	f, err := os.OpenFile(tempPath, os.O_RDWR, 0)
	if err != nil {
		return result, err
	}
	err = f.Sync()
	closeErr := f.Close()
	if err != nil {
		return result, err
	}
	if closeErr != nil {
		return result, closeErr
	}
	if err = tx.Rollback(); err != nil {
		return result, err
	}
	if err = db.Close(); err != nil {
		return result, err
	}
	current, err := os.Stat(filename)
	if err != nil {
		return result, err
	}
	if !os.SameFile(info, current) || !info.ModTime().Equal(current.ModTime()) || info.Size() != current.Size() {
		return result, fmt.Errorf("原文件在转换期间发生变化，未替换")
	}
	stat, err := os.Stat(tempPath)
	if err != nil {
		return result, err
	}
	result.AfterBytes = stat.Size()
	if err = os.Rename(tempPath, filename); err != nil {
		return result, err
	}
	result.Changed = true
	dir, err := os.Open(filepath.Dir(filename))
	if err != nil {
		return result, err
	}
	err = dir.Sync()
	dir.Close()
	return result, err
}

// CompressMinutes serializes with CLI/Web updates and supports mixed-format resumes.
func (p *PullKline) CompressMinutes(workers int, report func(string, MinuteCompression, error)) error {
	release, err := lockUpdates(p.Config.Dir)
	if err != nil {
		return err
	}
	defer release()
	var files []string
	err = filepath.WalkDir(filepath.Join(p.Config.Dir, DirMinute), func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.Type().IsRegular() || strings.HasPrefix(d.Name(), ".") || !strings.HasSuffix(d.Name(), ".db") {
			return nil
		}
		files = append(files, path)
		return nil
	})
	if err != nil {
		return err
	}
	if workers < 1 {
		workers = 1
	}
	if workers > 8 {
		workers = 8
	}
	jobs := make(chan string)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error
	failures := 0
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for path := range jobs {
				r, err := CompressMinuteDatabase(path)
				mu.Lock()
				if err != nil {
					failures++
					if firstErr == nil {
						firstErr = fmt.Errorf("%s: %w", path, err)
					}
				}
				if report != nil {
					report(path, r, err)
				}
				mu.Unlock()
			}
		}()
	}
	for _, path := range files {
		jobs <- path
	}
	close(jobs)
	wg.Wait()
	if firstErr != nil {
		return fmt.Errorf("%d 个分钟库未能转换（原始文件保留或已完成校验替换）：%w", failures, firstErr)
	}
	return nil
}
