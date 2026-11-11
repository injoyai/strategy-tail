package marketstore

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/injoyai/strategy-tail/internal/xorms"
	"github.com/injoyai/tdx/v2/extend/model"
	"github.com/injoyai/tdx/v2/extend/storage"
	"github.com/injoyai/tdx/v2/protocol"
)

// MigrateStats 汇总一次 SQLite → Store 迁移的结果。
type MigrateStats struct {
	Codes      int   `json:"codes"`      // 源目录中的代码数
	DayRows    int64 `json:"dayRows"`    // 写入的日线行数
	MinuteRows int64 `json:"minuteRows"` // 写入的分钟线行数
	Coverages  int   `json:"coverages"`  // 同步的分钟覆盖标记数
	Skipped    int   `json:"skipped"`    // 没有补入记录或目标端更新而跳过的批次
}

// MigrateProgress 是单个代码（或代码年份）复制完成后的进度快照。
type MigrateProgress struct {
	Market  string `json:"market"`
	Code    string `json:"code"`
	Year    int    `json:"year,omitempty"` // 0 表示日线
	Rows    int    `json:"rows"`
	Skipped bool   `json:"skipped,omitempty"`
	Done    int    `json:"done"`
	Total   int    `json:"total"`
}

// MigrateOptions 控制 SQLite 到目标 Store 的迁移。
type MigrateOptions struct {
	// Workers 是并发处理的批次（代码或代码年份）数；<=0 时使用 4。
	Workers int
	// OnProgress 在每个批次结束后串行回调，可为空。
	OnProgress func(MigrateProgress)
}

// migrateItem 是一个迁移批次：year=0 表示该代码的日线，否则为该年份的分钟线。
type migrateItem struct {
	code model.Code
	year int
	file string
}

func (item migrateItem) label() string {
	if item.year == 0 {
		return item.code.Key() + " 日线"
	}
	return fmt.Sprintf("%s %d 年分钟线", item.code.Key(), item.year)
}

// MigrateSQLite 把源时间 SQLite K 线复制到 dst，可重复执行。
//
// 批次布局由存储自身解析：{dir}/{market}/day/{code}.db 和
// {dir}/{market}/min/{code}/{code}-{year}.db。分钟覆盖标记一并复制，
// 空扫描缓存只是短期缓存，不迁移。非 MySQL 目标沿用 SaveDay/SaveMinute
// 的“删除时间 >= from 后重插”语义。
//
// MySQL 目标通过 ImportDay/ImportMinute 只补缺失记录，保留目标已有值与更新的尾部。
// 其他 Store 若同一代码/年份已有比源更晚的数据，跳过该批次并计入 Skipped，
// 避免迁移把目标端更新的数据回退成源端快照。批次失败后停止派发剩余批次，
// 已开始的批次会自行结束，最终返回第一个错误。
func MigrateSQLite(ctx context.Context, source *SQLiteSource, dst storage.BarStore, opt MigrateOptions) (MigrateStats, error) {
	if dst == nil {
		return MigrateStats{}, errors.New("pull: 迁移目标存储不能为空")
	}
	if source == nil {
		return MigrateStats{}, errors.New("pull: SQLite 迁移源不能为空")
	}
	items, codes, err := migrateItems(source)
	if err != nil {
		return MigrateStats{}, err
	}
	stats := MigrateStats{Codes: codes}
	if len(items) == 0 {
		return stats, nil
	}
	workers := opt.Workers
	if workers <= 0 {
		workers = 4
	}
	if workers > len(items) {
		workers = len(items)
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	type result struct {
		item    migrateItem
		day     int
		minute  int
		covered bool
		skipped bool
		err     error
	}
	jobs := make(chan migrateItem)
	results := make(chan result)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for item := range jobs {
				r := result{item: item}
				if err := ctx.Err(); err != nil {
					r.err = err
				} else {
					r.day, r.minute, r.covered, r.skipped, r.err = migrateBatch(source, dst, item)
				}
				results <- r
			}
		}()
	}
	go func() {
		defer close(jobs)
		for _, item := range items {
			select {
			case jobs <- item:
			case <-ctx.Done():
				return
			}
		}
	}()
	go func() {
		wg.Wait()
		close(results)
	}()

	var firstErr error
	done := 0
	for r := range results {
		done++
		stats.DayRows += int64(r.day)
		stats.MinuteRows += int64(r.minute)
		if r.covered {
			stats.Coverages++
		}
		if r.skipped {
			stats.Skipped++
		}
		if r.err != nil && firstErr == nil {
			firstErr = fmt.Errorf("%s: %w", r.item.label(), r.err)
			cancel()
		}
		if opt.OnProgress != nil {
			opt.OnProgress(MigrateProgress{
				Market:  r.item.code.Market.String(),
				Code:    r.item.code.Code,
				Year:    r.item.year,
				Rows:    r.day + r.minute,
				Skipped: r.skipped,
				Done:    done,
				Total:   len(items),
			})
		}
	}
	if firstErr == nil {
		firstErr = ctx.Err()
	}
	return stats, firstErr
}

func migrateBatch(s *SQLiteSource, dst storage.BarStore, item migrateItem) (day, minute int, covered, skipped bool, err error) {
	if item.year == 0 {
		day, skipped, err = migrateDay(dst, item.code, item.file, s.version == 1)
		return day, 0, false, skipped, err
	}
	minute, covered, skipped, err = migrateMinute(dst, item.code, item.year, item.file)
	return 0, minute, covered, skipped, err
}

func migrateDay(dst storage.BarStore, code model.Code, file string, legacy bool) (rows int, skipped bool, err error) {
	records, err := readSourceDay(file, "", legacy)
	if err != nil {
		return 0, false, err
	}
	if len(records) == 0 {
		return 0, false, nil
	}
	if importer, ok := dst.(interface {
		ImportDayRecords(model.Code, []*DayRecord) (int, error)
	}); ok {
		n, err := importer.ImportDayRecords(code, records)
		return n, n == 0 && err == nil, err
	}
	ks := make([]*model.KlineDay, 0, len(records))
	for _, row := range records {
		ks = append(ks, &row.KlineDay)
	}
	last, err := dst.LastDayTime(code)
	if err != nil {
		return 0, false, err
	}
	if last > ks[len(ks)-1].Time {
		return 0, true, nil
	}
	if err := dst.SaveDay(code, ks[0].Time, ks); err != nil {
		return 0, false, err
	}
	return len(ks), false, nil
}

func migrateMinute(dst storage.BarStore, code model.Code, year int, file string) (rows int, covered, skipped bool, err error) {
	ks, err := readSQLiteMinute(file)
	if err != nil {
		return 0, false, false, err
	}
	if len(ks) == 0 {
		return 0, false, false, nil
	}
	coverage, err := readSQLiteCoverage(file)
	if err != nil {
		return 0, false, false, err
	}

	// 旧指数年度文件可能混有相邻年份的记录，按真实年份分组，避免整批被
	// validateMinutes 拒绝，也避免把别的年份写进错误的 MySQL 年表。
	groups := map[int][]*model.KlineMinute{}
	years := make([]int, 0, 1)
	for _, k := range ks {
		y, ok := minuteYear(k.Time)
		if !ok {
			return 0, false, false, fmt.Errorf("pull: 分钟时间 %q 格式无效", k.Time)
		}
		if _, exists := groups[y]; !exists {
			years = append(years, y)
		}
		groups[y] = append(groups[y], k)
	}
	sort.Ints(years)

	for _, y := range years {
		group := groups[y]
		var mark *model.MinuteCoverage
		if y == year {
			mark = coverage
		}
		if importer, ok := dst.(interface {
			ImportMinute(model.Code, int, []*model.KlineMinute, *model.MinuteCoverage) (int, error)
		}); ok {
			newCoverage := false
			if mark != nil {
				existing, err := dst.QueryMinuteCoverage(code, y)
				if err != nil {
					return rows, covered, skipped, err
				}
				newCoverage = existing == nil
			}
			n, err := importer.ImportMinute(code, y, group, mark)
			if err != nil {
				return rows, covered, skipped, err
			}
			rows += n
			covered = covered || newCoverage
			skipped = skipped || n == 0
			continue
		}
		last, err := dst.LastMinuteTime(code, y)
		if err != nil {
			return rows, covered, skipped, err
		}
		if last > group[len(group)-1].Time {
			skipped = true
			continue
		}
		if err := dst.SaveMinute(code, y, group[0].Time, group, mark); err != nil {
			return rows, covered, skipped, err
		}
		if mark != nil {
			covered = true
		}
		rows += len(group)
	}
	return rows, covered, skipped, nil
}

// migrateItems 枚举磁盘上的日线库与分钟库，返回按市场/代码/年份排序的批次。
func migrateItems(s *SQLiteSource) ([]migrateItem, int, error) {
	items := []migrateItem{}
	codes := map[model.Code]struct{}{}
	add := func(code model.Code, year int, file string) {
		code.Name = ""
		items = append(items, migrateItem{code: code, year: year, file: file})
		codes[code] = struct{}{}
	}
	err := filepath.WalkDir(s.Identity(), func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".db") {
			return nil
		}
		parent := filepath.Dir(path)
		switch {
		case filepath.Base(parent) == "day":
			market, ok := migrateMarket(s.Identity(), filepath.Dir(parent))
			if !ok {
				return nil
			}
			name := strings.TrimSuffix(entry.Name(), ".db")
			if name == "" {
				return nil
			}
			if s.version == 2 {
				var err error
				market, err = decodeSQLiteName(market)
				if err != nil {
					return err
				}
				name, err = decodeSQLiteName(name)
				if err != nil {
					return err
				}
			}
			add(model.Code{Market: protocol.Market(market), Code: migrateCode(protocol.Market(market), name)}, 0, path)
		case filepath.Base(filepath.Dir(parent)) == "min":
			market, ok := migrateMarket(s.Identity(), filepath.Dir(filepath.Dir(parent)))
			if !ok {
				return nil
			}
			name, year, ok := parseMinuteFileName(entry.Name())
			if !ok || name != filepath.Base(parent) {
				return nil
			}
			if s.version == 2 {
				var err error
				market, err = decodeSQLiteName(market)
				if err != nil {
					return err
				}
				name, err = decodeSQLiteName(name)
				if err != nil {
					return err
				}
			}
			add(model.Code{Market: protocol.Market(market), Code: migrateCode(protocol.Market(market), name)}, year, path)
		}
		return nil
	})
	if err != nil {
		return nil, 0, err
	}
	sort.Slice(items, func(i, j int) bool {
		a, b := items[i], items[j]
		if a.code.Market != b.code.Market {
			return a.code.Market < b.code.Market
		}
		if a.code.Code != b.code.Code {
			return a.code.Code < b.code.Code
		}
		return a.year < b.year
	})
	return items, len(codes), nil
}

// migrateMarket 返回相对根目录的市场目录名，如 cn/stock。
func migrateMarket(root, dir string) (string, bool) {
	rel, err := filepath.Rel(root, dir)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return filepath.ToSlash(rel), true
}

// migrateCode 还原 FileName 对期货代码中 "/" 的替换（如 cff-IF2609 → cff/IF2609）。
// 其他市场的代码不含分隔符，文件名即原始代码。
func migrateCode(market protocol.Market, name string) string {
	if market == protocol.MarketFuture {
		return strings.Replace(name, "-", "/", 1)
	}
	return name
}

// parseMinuteFileName 从 sh600000-2024.db 解析代码与年份。
func parseMinuteFileName(name string) (string, int, bool) {
	if !strings.HasSuffix(name, ".db") {
		return "", 0, false
	}
	stem := strings.TrimSuffix(name, ".db")
	sep := strings.LastIndexByte(stem, '-')
	if sep <= 0 || len(stem)-sep-1 != 4 {
		return "", 0, false
	}
	year, err := strconv.Atoi(stem[sep+1:])
	if err != nil || year < 1900 || yearToString(year) != stem[sep+1:] {
		return "", 0, false
	}
	return stem[:sep], year, true
}

func minuteYear(s string) (int, bool) {
	if len(s) < 5 || s[4] != '-' {
		return 0, false
	}
	year, err := strconv.Atoi(s[:4])
	if err != nil {
		return 0, false
	}
	return year, true
}

// readSQLiteMinute reads exactly one annual file, including any misplaced adjacent-year rows.
// Read-only access preserves the migration source without SDK schema synchronization.
func readSQLiteMinute(filename string) ([]*model.KlineMinute, error) {
	if _, err := os.Stat(filename); err != nil {
		return nil, err
	}
	db, err := xorms.OpenReadOnly(filename)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	rows := []*model.KlineMinute{}
	err = db.Table("KlineMinute").Asc("Time").Find(&rows)
	return rows, err
}
