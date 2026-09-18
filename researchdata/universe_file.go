package researchdata

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// universeDateLayout 成员文件的日期格式（本地时区，日期粒度）。
const universeDateLayout = "2006-01-02"

// MembershipRecord 一条上市/退市成员记录（成员文件的一行）。
// DelistedAt 零值表示尚未退市；成员资格为闭区间 [ListedAt, DelistedAt]，
// 即退市日当天仍视为成员（保守包含，避免漏掉退市前最后几个交易日）。
// AvailableAt 是本条记录最早可知的时间，用于质量标记与上游 PIT 确认，
// 不参与成员资格判断——成员资格按客观事实回答。
type MembershipRecord struct {
	Code        string    `json:"code"`
	ListedAt    time.Time `json:"listedAt"`
	DelistedAt  time.Time `json:"delistedAt,omitempty"`
	Board       string    `json:"board,omitempty"`
	AvailableAt time.Time `json:"availableAt"`
	Source      string    `json:"source,omitempty"`
	Version     string    `json:"version,omitempty"`
}

// Validate 拒绝无法回答成员关系的记录。
func (r MembershipRecord) Validate() error {
	if strings.TrimSpace(r.Code) == "" {
		return fmt.Errorf("researchdata: membership code is empty")
	}
	if r.ListedAt.IsZero() {
		return fmt.Errorf("researchdata: membership %q listed time is empty", r.Code)
	}
	if r.AvailableAt.IsZero() {
		return fmt.Errorf("researchdata: membership %q available time is empty", r.Code)
	}
	if !r.DelistedAt.IsZero() && r.DelistedAt.Before(r.ListedAt) {
		return fmt.Errorf("researchdata: membership %q delisted %s precedes listed %s",
			r.Code, r.DelistedAt.Format(universeDateLayout), r.ListedAt.Format(universeDateLayout))
	}
	return nil
}

// member 该代码在 asOf 日（日期粒度）是否为成员。
func (r MembershipRecord) member(asOf time.Time) bool {
	a := asOf
	if !r.ListedAt.After(a) && (r.DelistedAt.IsZero() || !a.After(r.DelistedAt)) {
		return true
	}
	return false
}

// FileUniverse 有界本地文件适配器：一次加载后不可变，路径只来自本地配置，
// 不由 HTTP 请求提供。适配器的存在不构成数据可信证明；质量等级由调用方
// 显式声明（缺省 unverified），不得因数据可加载而自动升级。
type FileUniverse struct {
	id, version, source string
	pit                 string
	records             map[string]MembershipRecord
	sortedCodes         []string
	skipped             int
	coverageStart       time.Time
	coverageEnd         time.Time
	includeDelisted     bool
}

// FileUniverseConfig 文件股票池配置。PIT 缺省 unverified。
type FileUniverseConfig struct {
	ID      string
	Version string
	Source  string
	PIT     string // verified | unverified
}

// Codes asOf 当日成员，升序去重。
func (u *FileUniverse) Codes(asOf time.Time) ([]string, error) {
	out := make([]string, 0, len(u.records))
	for _, rec := range u.records {
		if rec.member(asOf) {
			out = append(out, rec.Code)
		}
	}
	sort.Strings(out)
	return out, nil
}

// CodesBetween 区间内任一天曾是成员的代码并集（含已退市），升序去重。
// 调用方不得把该并集当作每日截面；每日成员仍按 Codes/Contains 过滤。
func (u *FileUniverse) CodesBetween(start, end time.Time) ([]string, error) {
	if end.Before(start) {
		return nil, fmt.Errorf("researchdata: universe interval end %s precedes start %s",
			end.Format(universeDateLayout), start.Format(universeDateLayout))
	}
	out := make([]string, 0, len(u.records))
	for _, rec := range u.records {
		// [listed, delisted] 与 [start, end] 相交。
		if rec.ListedAt.After(end) {
			continue
		}
		if !rec.DelistedAt.IsZero() && rec.DelistedAt.Before(start) {
			continue
		}
		out = append(out, rec.Code)
	}
	sort.Strings(out)
	return out, nil
}

// Contains 单代码成员判断；未知代码恒为 false。
func (u *FileUniverse) Contains(code string, asOf time.Time) (bool, error) {
	rec, ok := u.records[code]
	if !ok {
		return false, nil
	}
	return rec.member(asOf), nil
}

// Snapshot 质量快照：覆盖日期、是否含退市与显式 PIT 声明。
func (u *FileUniverse) Snapshot() UniverseSnapshot {
	return UniverseSnapshot{
		ID:              u.id,
		Version:         u.version,
		Source:          u.source,
		Mode:            UniverseModeHistoricalMembership,
		CoverageStart:   u.coverageStart.Format(universeDateLayout),
		CoverageEnd:     u.coverageEnd.Format(universeDateLayout),
		IncludeDelisted: u.includeDelisted,
		MembershipPIT:   u.pit,
		Size:            len(u.records),
	}
}

// Skipped 非严格模式下被跳过的问题行数（严格模式恒为 0）。
func (u *FileUniverse) Skipped() int { return u.skipped }

// membershipRow JSONL 行的原始字段形态。
type membershipRow struct {
	Code        string `json:"code"`
	ListedAt    string `json:"listed_at"`
	DelistedAt  string `json:"delisted_at"`
	Board       string `json:"board"`
	AvailableAt string `json:"available_at"`
	Source      string `json:"source"`
	Version     string `json:"version"`
}

// LoadUniverseFile 从本地 CSV/JSONL 文件加载历史成员股票池（一次加载不可变）。
// 扩展名（不区分大小写）决定格式：.csv 为逗号分隔且首行表头；.jsonl/.ndjson
// 为每行一个 JSON 对象。字段合同：code, listed_at, delisted_at, board,
// available_at, source, version（delisted_at 空表示尚未退市）。
//
// strict=true：重复代码、非法日期、listed>delisted、缺 code/listed_at/
// available_at 任一问题都整体报错；strict=false：问题行跳过并计入 Skipped
// （重复代码保留先出现的记录），用于容忍小缺陷继续探索。
func LoadUniverseFile(path string, cfg FileUniverseConfig, strict bool) (*FileUniverse, error) {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".csv", ".jsonl", ".ndjson":
	default:
		return nil, fmt.Errorf("researchdata: 不支持的 universe 文件格式: %s（仅 .csv/.jsonl/.ndjson）", filepath.Ext(path))
	}
	if strings.TrimSpace(cfg.ID) == "" {
		base := filepath.Base(path)
		cfg.ID = strings.TrimSuffix(base, filepath.Ext(base))
	}
	if strings.TrimSpace(cfg.Source) == "" {
		cfg.Source = "local_file"
	}
	switch cfg.PIT {
	case "":
		cfg.PIT = UniversePITUnverified
	case UniversePITVerified, UniversePITUnverified:
	default:
		return nil, fmt.Errorf("researchdata: universe PIT 非法: %q", cfg.PIT)
	}

	rows, skipped, err := readUniverseRows(path, strict)
	if err != nil {
		return nil, err
	}

	u := &FileUniverse{
		id: cfg.ID, version: cfg.Version, source: cfg.Source, pit: cfg.PIT,
		records: make(map[string]MembershipRecord, len(rows)),
		skipped: skipped,
	}
	for _, rec := range rows {
		// readUniverseRows 已保证记录无重复、字段合法。
		u.records[rec.Code] = rec
		if u.coverageStart.IsZero() || rec.ListedAt.Before(u.coverageStart) {
			u.coverageStart = rec.ListedAt
		}
		end := rec.ListedAt
		if !rec.DelistedAt.IsZero() && rec.DelistedAt.After(end) {
			end = rec.DelistedAt
		}
		if u.coverageEnd.Before(end) {
			u.coverageEnd = end
		}
		if !rec.DelistedAt.IsZero() {
			u.includeDelisted = true
		}
	}
	u.sortedCodes = make([]string, 0, len(u.records))
	for code := range u.records {
		u.sortedCodes = append(u.sortedCodes, code)
	}
	sort.Strings(u.sortedCodes)
	if len(u.records) == 0 {
		return nil, fmt.Errorf("researchdata: universe 文件 %s 无有效记录", path)
	}
	return u, nil
}

// readUniverseRows 按扩展名解析文件为记录列表。
func readUniverseRows(path string, strict bool) ([]MembershipRecord, int, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer f.Close()
	switch strings.ToLower(filepath.Ext(path)) {
	case ".csv":
		return parseUniverseCSV(f, strict)
	default:
		return parseUniverseJSONL(f, strict)
	}
}

// parseUniverseCSV 解析带表头的 CSV。
func parseUniverseCSV(r io.Reader, strict bool) ([]MembershipRecord, int, error) {
	cr := csv.NewReader(r)
	cr.FieldsPerRecord = -1 // 允许列数差异，由字段合同统一校验
	header, err := cr.Read()
	if err != nil {
		return nil, 0, fmt.Errorf("researchdata: universe CSV 表头读取失败: %w", err)
	}
	idx := make(map[string]int, len(header))
	for i, name := range header {
		idx[strings.TrimSpace(name)] = i
	}
	for _, must := range []string{"code", "listed_at"} {
		if _, ok := idx[must]; !ok {
			return nil, 0, fmt.Errorf("researchdata: universe CSV 缺少必需列 %q", must)
		}
	}

	seen := make(map[string]bool)
	var records []MembershipRecord
	skipped := 0
	line := 1
	for {
		row, err := cr.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, 0, fmt.Errorf("researchdata: universe CSV 第 %d 行读取失败: %w", line+1, err)
		}
		line++
		field := func(name string) string {
			i, ok := idx[name]
			if !ok || i >= len(row) {
				return ""
			}
			return strings.TrimSpace(row[i])
		}
		rec, perr := rowToRecord(map[string]string{
			"code":         field("code"),
			"listed_at":    field("listed_at"),
			"delisted_at":  field("delisted_at"),
			"board":        field("board"),
			"available_at": field("available_at"),
			"source":       field("source"),
			"version":      field("version"),
		}, line)
		if perr != nil {
			if strict {
				return nil, 0, perr
			}
			skipped++
			continue
		}
		if seen[rec.Code] {
			if strict {
				return nil, 0, fmt.Errorf("researchdata: universe CSV 第 %d 行代码 %s 重复", line, rec.Code)
			}
			skipped++
			continue
		}
		seen[rec.Code] = true
		records = append(records, rec)
	}
	return records, skipped, nil
}

// parseUniverseJSONL 解析每行一个 JSON 对象的文件。
func parseUniverseJSONL(r io.Reader, strict bool) ([]MembershipRecord, int, error) {
	seen := make(map[string]bool)
	var records []MembershipRecord
	skipped := 0
	dec := json.NewDecoder(r)
	line := 0
	for {
		var raw membershipRow
		if err := dec.Decode(&raw); err != nil {
			if err == io.EOF {
				break
			}
			line++
			if strict {
				return nil, 0, fmt.Errorf("researchdata: universe JSONL 第 %d 行解析失败: %w", line, err)
			}
			skipped++
			// 坏行后无法可靠重新对齐 decoder，直接放弃剩余内容。
			break
		}
		line++
		rec, perr := rowToRecord(map[string]string{
			"code":         raw.Code,
			"listed_at":    raw.ListedAt,
			"delisted_at":  raw.DelistedAt,
			"board":        raw.Board,
			"available_at": raw.AvailableAt,
			"source":       raw.Source,
			"version":      raw.Version,
		}, line)
		if perr != nil {
			if strict {
				return nil, 0, perr
			}
			skipped++
			continue
		}
		if seen[rec.Code] {
			if strict {
				return nil, 0, fmt.Errorf("researchdata: universe JSONL 第 %d 行代码 %s 重复", line, rec.Code)
			}
			skipped++
			continue
		}
		seen[rec.Code] = true
		records = append(records, rec)
	}
	return records, skipped, nil
}

// rowToRecord 原始字段 → 记录（含日期解析与字段合同校验）。
func rowToRecord(fields map[string]string, line int) (MembershipRecord, error) {
	bad := func(format string, args ...any) error {
		return fmt.Errorf("researchdata: universe 第 %d 行 %s", line, fmt.Sprintf(format, args...))
	}
	code := strings.TrimSpace(fields["code"])
	if code == "" {
		return MembershipRecord{}, bad("code 为空")
	}
	listed, err := parseUniverseDate(fields["listed_at"])
	if err != nil {
		return MembershipRecord{}, bad("listed_at 非法: %q", fields["listed_at"])
	}
	var delisted time.Time
	if s := strings.TrimSpace(fields["delisted_at"]); s != "" {
		delisted, err = parseUniverseDate(s)
		if err != nil {
			return MembershipRecord{}, bad("delisted_at 非法: %q", s)
		}
	}
	avail, err := parseUniverseDate(fields["available_at"])
	if err != nil {
		return MembershipRecord{}, bad("available_at 非法: %q", fields["available_at"])
	}
	rec := MembershipRecord{
		Code:        code,
		ListedAt:    listed,
		DelistedAt:  delisted,
		Board:       strings.TrimSpace(fields["board"]),
		AvailableAt: avail,
		Source:      strings.TrimSpace(fields["source"]),
		Version:     strings.TrimSpace(fields["version"]),
	}
	if err := rec.Validate(); err != nil {
		return MembershipRecord{}, bad("%v", err)
	}
	return rec, nil
}

// parseUniverseDate 只接受 YYYY-MM-DD。
func parseUniverseDate(s string) (time.Time, error) {
	return time.ParseInLocation(universeDateLayout, strings.TrimSpace(s), time.Local)
}

// LoadUniverseOrDefault 尝试加载本地 universe 文件；文件不存在时回退当前
// 静态股票池并显式降级（degraded=true），不阻塞现有探索流程。文件存在但
// 加载失败时返回错误——坏文件静默降级会掩盖数据质量问题。
func LoadUniverseOrDefault(path string, cfg FileUniverseConfig, strict bool, fallback func() []string) (Universe, bool, error) {
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return NewStaticUniverse(StaticUniverseConfig{
				ID:     "current_static",
				Source: "tdx_local",
				Codes:  fallback,
			}), true, nil
		}
		return nil, false, err
	}
	u, err := LoadUniverseFile(path, cfg, strict)
	if err != nil {
		return nil, false, err
	}
	return u, false, nil
}
