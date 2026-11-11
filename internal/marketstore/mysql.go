package marketstore

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/injoyai/tdx/v2/extend/model"
	"github.com/injoyai/tdx/v2/extend/storage"
	"github.com/injoyai/tdx/v2/protocol"
)

// mysqlInsertBatch 单条 INSERT 的行数；10 列 × 1000 行仍在 MySQL 占位符上限内，
// 同时避免单条 SQL 过长。
const mysqlInsertBatch = 1000

// mysqlTablesRefreshed 是分钟分表清单的缓存时长。其他进程可能新建年份表，
// 过期后重新读取 information_schema，避免长时间漏读。
const mysqlTablesRefreshed = time.Minute

// mysqlTxAttempts 是写入事务遇到死锁或锁等待超时时的最大尝试次数。
// 并发写入相邻 (market,code) 主键区间时，范围删除的间隙锁可能与插入意向锁成环，
// 事务被整体回滚后可安全重试。
const mysqlTxAttempts = 3

// MySQLStore 按市场保存日线、按市场和源年份保存分钟线；辅助状态表共享市场键。
//
// DSN 参数由 NewMySQLStore 强制收敛，调用方不要依赖自行拼接：
//   - parseTime=false：驱动返回 "2006-01-02 15:04" 文本，与源时间字符串一致；
//   - charset=utf8mb4，timeout/readTimeout 未显式指定时默认 5s/30s（迁移等长事务可调大）；
//   - 不使用 interpolateParams：文本协议会明显拖慢热路径，批量写入复用 Prepare；
//   - 不使用 compress：当前驱动版本未实现，开启会 panic。
//
// Identity 只包含地址与库名，不含密码；同一库内的数据不做额外隔离。
type MySQLStore struct {
	db       *sql.DB
	identity string

	mu      sync.Mutex
	closed  bool
	tables  map[string]bool
	yearsAt time.Time
}

var _ storage.BarStore = (*MySQLStore)(nil)

// NewMySQLStore 打开 MySQL 存储并幂等建表。返回的存储由调用方关闭。
func NewMySQLStore(dsn string) (*MySQLStore, error) {
	return openMySQLStore(dsn, false, false)
}

// NewMySQLImportStore 仅用于已暂停业务写入的历史导入。
// 允许无旧混合表且 ready=0 的目标；调用方负责完成校验后设置验收标记。
func NewMySQLImportStore(dsn string) (*MySQLStore, error) {
	return openMySQLStore(dsn, true, false)
}

func openMySQLStore(dsn string, allowPending, readOnly bool) (*MySQLStore, error) {
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		return nil, fmt.Errorf("pull: 解析 MySQL DSN: %w", err)
	}
	if strings.TrimSpace(cfg.DBName) == "" {
		return nil, errors.New("pull: MySQL DSN 缺少数据库名")
	}
	cfg.ParseTime = false
	cfg.InterpolateParams = false
	cfg.ClientFoundRows = false // Import 返回实际补入行数，重复键不计入。
	// 未显式指定时用默认值；迁移等长事务可传入更大的 readTimeout。
	if cfg.Timeout == 0 {
		cfg.Timeout = 5 * time.Second
	}
	if cfg.ReadTimeout == 0 {
		cfg.ReadTimeout = 30 * time.Second
	}
	if cfg.Params == nil {
		cfg.Params = map[string]string{}
	}
	cfg.Params["charset"] = "utf8mb4"

	db, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		return nil, fmt.Errorf("pull: 打开 MySQL: %w", err)
	}
	db.SetMaxOpenConns(16)
	db.SetMaxIdleConns(16)
	db.SetConnMaxLifetime(time.Hour)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("pull: 连接 MySQL: %w", err)
	}
	s := &MySQLStore{
		db:       db,
		identity: "mysql://" + cfg.Addr + "/" + cfg.DBName,
		tables:   map[string]bool{},
	}
	if err := s.init(ctx, allowPending, readOnly); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *MySQLStore) Identity() string { return s.identity }

func (s *MySQLStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	s.tables = nil
	return s.db.Close()
}

const mysqlDayDDL = "CREATE TABLE IF NOT EXISTS `%s` (" +
	"`code` VARCHAR(32) NOT NULL," +
	"`time` DATE NOT NULL," +
	"`open` DOUBLE NOT NULL DEFAULT 0," +
	"`high` DOUBLE NOT NULL DEFAULT 0," +
	"`low` DOUBLE NOT NULL DEFAULT 0," +
	"`close` DOUBLE NOT NULL DEFAULT 0," +
	"`volume` BIGINT NOT NULL DEFAULT 0," +
	"`amount` DOUBLE NULL DEFAULT NULL," +
	"`turnover` DOUBLE NOT NULL DEFAULT 0," +
	"`float_stock` DOUBLE NOT NULL DEFAULT 0," +
	"`total_stock` DOUBLE NOT NULL DEFAULT 0," +
	"PRIMARY KEY (`code`,`time`)" +
	") ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin"

const mysqlMinuteDDL = "CREATE TABLE IF NOT EXISTS `%s` (" +
	"`code` VARCHAR(32) NOT NULL," +
	"`time` DATETIME NOT NULL," +
	"`open` DOUBLE NOT NULL DEFAULT 0," +
	"`high` DOUBLE NOT NULL DEFAULT 0," +
	"`low` DOUBLE NOT NULL DEFAULT 0," +
	"`close` DOUBLE NOT NULL DEFAULT 0," +
	"`volume` BIGINT NOT NULL DEFAULT 0," +
	"`amount` DOUBLE NULL DEFAULT NULL," +
	"`source` VARCHAR(8) NOT NULL DEFAULT ''," +
	"PRIMARY KEY (`code`,`time`)" +
	") ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin"

const mysqlCoverageDDL = "CREATE TABLE IF NOT EXISTS `minute_coverage` (" +
	"`market` VARCHAR(16) NOT NULL," +
	"`code` VARCHAR(32) NOT NULL," +
	"`year` SMALLINT NOT NULL," +
	"`from_time` VARCHAR(16) NOT NULL," +
	"`through_time` VARCHAR(16) NOT NULL DEFAULT ''," +
	"`trade_fallback` TINYINT NOT NULL DEFAULT 0," +
	"PRIMARY KEY (`market`,`code`,`year`)" +
	") ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin"

const mysqlEmptyScanDDL = "CREATE TABLE IF NOT EXISTS `minute_empty_scan` (" +
	"`market` VARCHAR(16) NOT NULL," +
	"`code` VARCHAR(32) NOT NULL," +
	"`year` SMALLINT NOT NULL," +
	"`from_time` VARCHAR(16) NOT NULL," +
	"`checked_at` BIGINT NOT NULL DEFAULT 0," +
	"PRIMARY KEY (`market`,`code`,`year`)" +
	") ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin"

const mysqlUpdateDDL = "CREATE TABLE IF NOT EXISTS `update_marks` (" +
	"`mark_key` VARCHAR(191) NOT NULL," +
	"`time` BIGINT NOT NULL DEFAULT 0," +
	"PRIMARY KEY (`mark_key`)" +
	") ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin"

func (s *MySQLStore) init(ctx context.Context, allowPending, readOnly bool) error {
	// 旧库只有在完整分表验收后才允许新程序接入，避免读到空表或半成品。
	var legacy int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM information_schema.TABLES WHERE TABLE_SCHEMA=DATABASE() AND (TABLE_NAME='kline_day' OR TABLE_NAME REGEXP '^kline_minute_[0-9]{4}$')").Scan(&legacy); err != nil {
		return err
	}
	var ready int
	err := s.db.QueryRowContext(ctx, "SELECT ready FROM kline_schema WHERE version='market-v1'").Scan(&ready)
	var missing *mysql.MySQLError
	noMarker := errors.Is(err, sql.ErrNoRows) || (errors.As(err, &missing) && missing.Number == 1146)
	if (err == nil && ready != 1 && !(allowPending && legacy == 0)) || (noMarker && legacy > 0) {
		return errors.New("pull: MySQL 行情库尚未完成市场分表，请先运行 migrate-mysql-market 并通过完整校验")
	}
	if err != nil && !noMarker {
		return fmt.Errorf("pull: 查询分表验收状态: %w", err)
	}
	if readOnly {
		return nil
	}
	for _, stmt := range []string{mysqlCoverageDDL, mysqlEmptyScanDDL, mysqlUpdateDDL} {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("pull: 初始化 MySQL 表: %w", err)
		}
	}
	return nil
}

// MySQLDayTable 返回市场专属日线表名，拒绝不能唯一映射的市场名。
func MySQLDayTable(market protocol.Market) (string, error) {
	prefix, err := mysqlMarketPrefix(market)
	return prefix + "_kline_day", err
}

// MySQLMinuteTable 返回市场和源年份对应的分钟表名。
func MySQLMinuteTable(market protocol.Market, year int) (string, error) {
	prefix, err := mysqlMarketPrefix(market)
	if err != nil {
		return "", err
	}
	if year < 1 || year > 9999 {
		return "", fmt.Errorf("pull: 非法年份 %d", year)
	}
	return prefix + "_kline_minute_" + yearToString(year), nil
}

func mysqlMarketPrefix(market protocol.Market) (string, error) {
	name := market.String()
	if name == "" || len(name) > 16 {
		return "", fmt.Errorf("pull: 无效 MySQL 市场名 %q", market)
	}
	parts := strings.Split(name, "/")
	if len(parts) == 1 {
		for i, c := range name {
			letter := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
			if !letter && !(i > 0 && c >= '0' && c <= '9') {
				return "", fmt.Errorf("pull: 非法 MySQL 自定义市场名 %q", market)
			}
		}
		// Built-in prefixes start with a letter. Hex also distinguishes
		// custom names by case on servers that fold SQL identifiers.
		return "_x_" + hex.EncodeToString([]byte(name)), nil
	}
	if len(parts) != 2 {
		return "", fmt.Errorf("pull: MySQL 市场名须为 region/asset 或单段市场名: %q", market)
	}
	for _, part := range parts {
		if part == "" || part[0] < 'a' || part[0] > 'z' {
			return "", fmt.Errorf("pull: 非法 MySQL 市场名 %q", market)
		}
		for _, c := range part {
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9') {
				return "", fmt.Errorf("pull: 非法 MySQL 市场名 %q", market)
			}
		}
	}
	prefix := strings.Join(parts, "_")
	if len(prefix)+len("_kline_minute_9999") > 64 {
		return "", errors.New("pull: MySQL 市场表名过长")
	}
	return prefix, nil
}

// DDL 在事务外执行；缓存按完整表名隔离市场和年份。
func (s *MySQLStore) ensureTable(ctx context.Context, name, ddl string) (string, error) {
	s.mu.Lock()
	ok, closed := s.tables[name], s.closed
	s.mu.Unlock()
	if closed {
		return "", errors.New("pull: MySQL 存储已关闭")
	}
	if ok {
		return name, nil
	}
	if _, err := s.db.ExecContext(ctx, fmt.Sprintf(ddl, name)); err != nil {
		return "", fmt.Errorf("pull: 创建行情表 %s: %w", name, err)
	}
	s.mu.Lock()
	if s.tables != nil {
		s.tables[name] = true
	}
	s.mu.Unlock()
	return name, nil
}

func (s *MySQLStore) ensureDayTable(ctx context.Context, market protocol.Market) (string, error) {
	name, err := MySQLDayTable(market)
	if err != nil {
		return "", err
	}
	return s.ensureTable(ctx, name, mysqlDayDDL)
}

func (s *MySQLStore) ensureMinuteTable(ctx context.Context, market protocol.Market, year int) (string, error) {
	name, err := MySQLMinuteTable(market, year)
	if err != nil {
		return "", err
	}
	return s.ensureTable(ctx, name, mysqlMinuteDDL)
}

// minuteYears 只返回请求市场的已存在年份，不泄漏可变缓存。
func (s *MySQLStore) minuteYears(ctx context.Context, market protocol.Market) (map[int]bool, error) {
	prefix, err := mysqlMarketPrefix(market)
	if err != nil {
		return nil, err
	}
	prefix += "_kline_minute_"
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, errors.New("pull: MySQL 存储已关闭")
	}
	if s.tables == nil || time.Since(s.yearsAt) >= mysqlTablesRefreshed {
		rows, err := s.db.QueryContext(ctx, "SELECT `table_name` FROM `information_schema`.`tables` WHERE `table_schema` = DATABASE() AND `table_name` REGEXP '^([a-z][a-z0-9]*_[a-z][a-z0-9]*|_x_[0-9a-f]+)_kline_(day|minute_[0-9]{4})$'")
		if err != nil {
			return nil, fmt.Errorf("pull: 查询行情分表: %w", err)
		}
		defer rows.Close()
		tables := map[string]bool{}
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				return nil, err
			}
			tables[name] = true
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
		s.tables = tables
		s.yearsAt = time.Now()
	}
	years := map[int]bool{}
	for name := range s.tables {
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		suffix := strings.TrimPrefix(name, prefix)
		var year int
		if _, err := fmt.Sscanf(suffix, "%d", &year); err == nil && year >= 1 && year <= 9999 && yearToString(year) == suffix {
			years[year] = true
		}
	}
	return years, nil
}

func (s *MySQLStore) availableYears(ctx context.Context, market protocol.Market, start, end string) ([]int, error) {
	startYear, endYear := minMaxYear(start, end)
	years, err := s.minuteYears(ctx, market)
	if err != nil {
		return nil, err
	}
	out := make([]int, 0, len(years))
	for year := range years {
		if year >= startYear && year <= endYear {
			out = append(out, year)
		}
	}
	sort.Ints(out)
	return out, nil
}

func (s *MySQLStore) QueryDay(code model.Code, start, end string) ([]*model.KlineDay, error) {
	return s.queryDay(code, start, end, false)
}

func (s *MySQLStore) queryDay(code model.Code, start, end string, readOnly bool) ([]*model.KlineDay, error) {
	rows, err := s.queryDayRecords(code, start, end, readOnly)
	if err != nil {
		return nil, err
	}
	out := make([]*model.KlineDay, 0, len(rows))
	for _, row := range rows {
		out = append(out, &row.KlineDay)
	}
	return out, nil
}

func (s *MySQLStore) QueryDayRecords(code model.Code, start, end string) ([]*DayRecord, error) {
	return s.queryDayRecords(code, start, end, false)
}

func (s *MySQLStore) queryDayRecords(code model.Code, start, end string, readOnly bool) ([]*DayRecord, error) {
	if err := validateRange(start, end, time.DateOnly); err != nil {
		return nil, err
	}
	table, err := s.dayTableForQuery(code.Market, readOnly)
	if err != nil {
		return nil, err
	}
	query := "SELECT DATE_FORMAT(`time`,'%Y-%m-%d'),`open`,`high`,`low`,`close`,`volume`,`amount`,`turnover`,`float_stock`,`total_stock` FROM `" + table + "` WHERE `code`=?"
	args := []any{code.Code}
	if start != "" {
		query += " AND `time`>=?"
		args = append(args, start)
	}
	if end != "" {
		query += " AND `time`<=?"
		args = append(args, end)
	}
	query += " ORDER BY `time`"
	rows, err := s.db.QueryContext(context.Background(), query, args...)
	if err != nil {
		return nil, fmt.Errorf("pull: 查询日线: %w", err)
	}
	defer rows.Close()
	out := []*DayRecord{}
	for rows.Next() {
		k := new(DayRecord)
		if err := rows.Scan(&k.Time, &k.Open, &k.High, &k.Low, &k.Close, &k.Volume, &k.Amount, &k.Turnover, &k.FloatStock, &k.TotalStock); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// DayCodes 枚举已有日线的证券，包含退市历史证券，不依赖当前在市列表。
func (s *MySQLStore) DayCodes(market protocol.Market) ([]model.Code, error) {
	return s.dayCodes(market, false)
}

func (s *MySQLStore) dayCodes(market protocol.Market, readOnly bool) ([]model.Code, error) {
	table, err := s.dayTableForQuery(market, readOnly)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(context.Background(), "SELECT DISTINCT `code` FROM `"+table+"` ORDER BY `code`")
	if err != nil {
		return nil, fmt.Errorf("pull: 查询日线证券: %w", err)
	}
	defer rows.Close()
	codes := []model.Code{}
	for rows.Next() {
		code := model.Code{Market: market}
		if err := rows.Scan(&code.Code); err != nil {
			return nil, err
		}
		codes = append(codes, code)
	}
	return codes, rows.Err()
}

func (s *MySQLStore) dayTableForQuery(market protocol.Market, readOnly bool) (string, error) {
	if readOnly {
		return MySQLDayTable(market)
	}
	return s.ensureDayTable(context.Background(), market)
}

func (s *MySQLStore) LastDayTime(code model.Code) (string, error) {
	return s.lastDayTime(code, false)
}

func (s *MySQLStore) lastDayTime(code model.Code, readOnly bool) (string, error) {
	table, err := s.dayTableForQuery(code.Market, readOnly)
	if err != nil {
		return "", err
	}
	var last sql.NullString
	err = s.db.QueryRowContext(context.Background(),
		"SELECT DATE_FORMAT(MAX(`time`),'%Y-%m-%d') FROM `"+table+"` WHERE `code`=?", code.Code).Scan(&last)
	if err != nil {
		return "", fmt.Errorf("pull: 查询最后日线: %w", err)
	}
	return last.String, nil
}

func (s *MySQLStore) SaveDay(code model.Code, from string, rows []*model.KlineDay) error {
	records, err := asDayRecords(rows)
	if err != nil {
		return err
	}
	_, err = s.writeDay(code, from, records, false)
	return err
}

func asDayRecords(rows []*model.KlineDay) ([]*DayRecord, error) {
	out := make([]*DayRecord, 0, len(rows))
	for _, row := range rows {
		if row == nil {
			return nil, errors.New("pull: nil 日线")
		}
		out = append(out, &DayRecord{KlineDay: *row})
	}
	return out, nil
}

// ImportDay 只补入不存在的历史日期，已有记录保持不变。
func (s *MySQLStore) ImportDay(code model.Code, rows []*model.KlineDay) (int, error) {
	records, err := asDayRecords(rows)
	if err != nil {
		return 0, err
	}
	return s.ImportDayRecords(code, records)
}

// ImportDayRecords preserves historical equity and turnover supplied by the source.
func (s *MySQLStore) ImportDayRecords(code model.Code, rows []*DayRecord) (int, error) {
	return s.writeDay(code, "", rows, true)
}

func (s *MySQLStore) writeDay(code model.Code, from string, rows []*DayRecord, keepExisting bool) (int, error) {
	if len(rows) == 0 {
		return 0, nil
	}
	if err := validateDays(from, rows); err != nil {
		return 0, err
	}
	ctx := context.Background()
	table, err := s.ensureDayTable(ctx, code.Market)
	if err != nil {
		return 0, err
	}
	for _, row := range rows {
		if row.Amount == nil {
			if err := s.requireNullableAmount(ctx, table); err != nil {
				return 0, err
			}
			break
		}
	}
	inserted := 0
	err = mysqlRunTx(ctx, s.db, func(tx *sql.Tx) error {
		var existing map[string]DayRecord
		if !keepExisting {
			// Do not lock a missing symbol's index gap. Concurrent first
			// inserts for different symbols can otherwise deadlock.
			hasTail, err := mysqlHasTail(ctx, tx, table, code.Code, from)
			if err != nil {
				return err
			}
			if hasTail {
				existing, err = mysqlDayExtras(ctx, tx, table, code.Code, from)
				if err != nil {
					return err
				}
				where, args := mysqlTailWhere(code.Code, from)
				if _, err := tx.ExecContext(ctx, "DELETE FROM `"+table+"`"+where, args...); err != nil {
					return fmt.Errorf("pull: 删除日线尾部: %w", err)
				}
			}
		}
		values := make([][]any, 0, len(rows))
		for _, k := range rows {
			turnover, floating, total := k.Turnover, k.FloatStock, k.TotalStock
			if previous, ok := existing[k.Time]; ok {
				turnover, floating, total = previous.Turnover, previous.FloatStock, previous.TotalStock
			}
			values = append(values, []any{code.Code, k.Time, k.Open, k.High, k.Low, k.Close, k.Volume, k.Amount, turnover, floating, total})
		}
		var err error
		inserted, err = mysqlInsert(ctx, tx, table, "`code`,`time`,`open`,`high`,`low`,`close`,`volume`,`amount`,`turnover`,`float_stock`,`total_stock`", values, keepExisting)
		return err
	})
	if err != nil {
		return 0, err
	}
	return inserted, nil
}

func mysqlDayExtras(ctx context.Context, tx *sql.Tx, table, code, from string) (map[string]DayRecord, error) {
	where, args := mysqlTailWhere(code, from)
	rows, err := tx.QueryContext(ctx, "SELECT DATE_FORMAT(`time`,'%Y-%m-%d'),`turnover`,`float_stock`,`total_stock` FROM `"+table+"`"+where+" FOR UPDATE", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]DayRecord{}
	for rows.Next() {
		var r DayRecord
		if err := rows.Scan(&r.Time, &r.Turnover, &r.FloatStock, &r.TotalStock); err != nil {
			return nil, err
		}
		out[r.Time] = r
	}
	return out, rows.Err()
}

func (s *MySQLStore) QueryMinute(code model.Code, start, end string) ([]*model.KlineMinute, error) {
	if err := validateRange(start, end, protocol.TimeLayout); err != nil {
		return nil, err
	}
	ctx := context.Background()
	years, err := s.availableYears(ctx, code.Market, start, end)
	if err != nil {
		return nil, err
	}
	out := []*model.KlineMinute{}
	for _, year := range years {
		table, err := MySQLMinuteTable(code.Market, year)
		if err != nil {
			return nil, err
		}
		query := "SELECT DATE_FORMAT(`time`,'%Y-%m-%d %H:%i'),`open`,`high`,`low`,`close`,`volume`,`amount`,`source` FROM `" + table + "` WHERE `code`=?"
		args := []any{code.Code}
		if start != "" {
			query += " AND `time`>=?"
			args = append(args, start)
		}
		if end != "" {
			query += " AND `time`<=?"
			args = append(args, end)
		}
		query += " ORDER BY `time`"
		rows, err := s.db.QueryContext(ctx, query, args...)
		if err != nil {
			return nil, fmt.Errorf("pull: 查询分钟线: %w", err)
		}
		for rows.Next() {
			k := new(model.KlineMinute)
			if err := rows.Scan(&k.Time, &k.Open, &k.High, &k.Low, &k.Close, &k.Volume, &k.Amount, &k.Source); err != nil {
				rows.Close()
				return nil, err
			}
			out = append(out, k)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (s *MySQLStore) LastMinuteTime(code model.Code, year int) (string, error) {
	ctx := context.Background()
	years, err := s.minuteYears(ctx, code.Market)
	if err != nil {
		return "", err
	}
	if !years[year] {
		return "", nil
	}
	table, err := MySQLMinuteTable(code.Market, year)
	if err != nil {
		return "", err
	}
	var last sql.NullString
	err = s.db.QueryRowContext(ctx,
		"SELECT DATE_FORMAT(MAX(`time`),'%Y-%m-%d %H:%i') FROM `"+table+"` WHERE `code`=?",
		code.Code).Scan(&last)
	if err != nil {
		return "", fmt.Errorf("pull: 查询最后分钟线: %w", err)
	}
	return last.String, nil
}

func (s *MySQLStore) SaveMinute(code model.Code, year int, from string, rows []*model.KlineMinute, coverage *model.MinuteCoverage) error {
	_, err := s.writeMinute(code, year, from, rows, coverage, false)
	return err
}

// ImportMinute 原子补入缺失的历史分钟，保留已有量价、来源和覆盖标记。
// coverage 仅在目标尚无标记时写入；返回实际补入行数，不以首末记录推断完成。
func (s *MySQLStore) ImportMinute(code model.Code, year int, rows []*model.KlineMinute, coverage *model.MinuteCoverage) (int, error) {
	return s.writeMinute(code, year, "", rows, coverage, true)
}

func (s *MySQLStore) writeMinute(code model.Code, year int, from string, rows []*model.KlineMinute, coverage *model.MinuteCoverage, keepExisting bool) (int, error) {
	if len(rows) == 0 {
		return 0, nil
	}
	if err := validateMinutes(year, from, rows); err != nil {
		return 0, err
	}
	if coverage != nil {
		if err := validateMinutes(year, coverage.From, rows); err != nil {
			return 0, err
		}
	}
	ctx := context.Background()
	table, err := s.ensureMinuteTable(ctx, code.Market, year)
	if err != nil {
		return 0, err
	}
	for _, row := range rows {
		if row.Amount == nil {
			if err := s.requireNullableAmount(ctx, table); err != nil {
				return 0, err
			}
			break
		}
	}
	values := make([][]any, 0, len(rows))
	for _, k := range rows {
		values = append(values, []any{code.Code, k.Time, k.Open, k.High, k.Low, k.Close, k.Volume, k.Amount, k.Source})
	}
	// 正常更新替换尾部；历史导入只补缺失键。
	inserted := 0
	err = mysqlRunTx(ctx, s.db, func(tx *sql.Tx) error {
		if !keepExisting {
			if err := mysqlDeleteTail(ctx, tx, table, code, from); err != nil {
				return fmt.Errorf("pull: 删除分钟尾部: %w", err)
			}
		}
		var err error
		inserted, err = mysqlInsert(ctx, tx, table, "`code`,`time`,`open`,`high`,`low`,`close`,`volume`,`amount`,`source`", values, keepExisting)
		if err != nil {
			return err
		}
		if coverage != nil {
			if err := mysqlMergeCoverage(ctx, tx, code, year, *coverage, keepExisting); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return inserted, nil
}

// Old deployments can keep reading NOT NULL tables and writing known
// amounts. Never let server SQL mode silently coerce an unknown amount to 0.
func (s *MySQLStore) requireNullableAmount(ctx context.Context, table string) error {
	var nullable string
	err := s.db.QueryRowContext(ctx, "SELECT IS_NULLABLE FROM information_schema.COLUMNS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME=? AND COLUMN_NAME='amount'", table).Scan(&nullable)
	if err != nil {
		return fmt.Errorf("pull: 检查 %s.amount: %w", table, err)
	}
	if nullable != "YES" {
		return fmt.Errorf("pull: %s.amount 不允许 NULL，无法写入未知成交额，请先迁移该列", table)
	}
	return nil
}

func mysqlMergeCoverage(ctx context.Context, tx *sql.Tx, code model.Code, year int, coverage model.MinuteCoverage, keepExisting bool) error {
	var old model.MinuteCoverage
	var tradeFallback int
	err := tx.QueryRowContext(ctx,
		"SELECT `from_time`,`through_time`,`trade_fallback` FROM `minute_coverage` WHERE `market`=? AND `code`=? AND `year`=? FOR UPDATE",
		code.Market.String(), code.Code, year).Scan(&old.From, &old.Through, &tradeFallback)
	switch {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return fmt.Errorf("pull: 查询分钟覆盖: %w", err)
	default:
		if keepExisting {
			return nil
		}
		old.TradeFallback = tradeFallback != 0
		if old.TradeFallback == coverage.TradeFallback &&
			(!coverage.TradeFallback || (old.Through >= coverage.From && coverage.Through >= old.From)) {
			coverage.From = min(old.From, coverage.From)
			coverage.Through = max(old.Through, coverage.Through)
		}
	}
	_, err = tx.ExecContext(ctx,
		"INSERT INTO `minute_coverage` (`market`,`code`,`year`,`from_time`,`through_time`,`trade_fallback`) VALUES (?,?,?,?,?,?) ON DUPLICATE KEY UPDATE `from_time`=?,`through_time`=?,`trade_fallback`=?",
		code.Market.String(), code.Code, year, coverage.From, coverage.Through, coverage.TradeFallback,
		coverage.From, coverage.Through, coverage.TradeFallback)
	if err != nil {
		return fmt.Errorf("pull: 写入分钟覆盖: %w", err)
	}
	return nil
}

func (s *MySQLStore) QueryMinuteCoverage(code model.Code, year int) (*model.MinuteCoverage, error) {
	ctx := context.Background()
	years, err := s.minuteYears(ctx, code.Market)
	if err != nil {
		return nil, err
	}
	// 与 SQLite 一致：年份分表不存在视为没有覆盖记录。
	if !years[year] {
		return nil, nil
	}
	var c model.MinuteCoverage
	var tradeFallback int
	err = s.db.QueryRowContext(ctx,
		"SELECT `from_time`,`through_time`,`trade_fallback` FROM `minute_coverage` WHERE `market`=? AND `code`=? AND `year`=?",
		code.Market.String(), code.Code, year).Scan(&c.From, &c.Through, &tradeFallback)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("pull: 查询分钟覆盖: %w", err)
	}
	c.TradeFallback = tradeFallback != 0
	return &c, nil
}

func (s *MySQLStore) LoadMinuteEmptyScan(code model.Code, year int) (*model.MinuteEmptyScan, error) {
	var scan model.MinuteEmptyScan
	err := s.db.QueryRowContext(context.Background(),
		"SELECT `from_time`,`checked_at` FROM `minute_empty_scan` WHERE `market`=? AND `code`=? AND `year`=?",
		code.Market.String(), code.Code, year).Scan(&scan.From, &scan.CheckedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("pull: 查询分钟空扫描: %w", err)
	}
	return &scan, nil
}

func (s *MySQLStore) SaveMinuteEmptyScan(code model.Code, year int, scan model.MinuteEmptyScan) error {
	_, err := s.db.ExecContext(context.Background(),
		"INSERT INTO `minute_empty_scan` (`market`,`code`,`year`,`from_time`,`checked_at`) VALUES (?,?,?,?,?) ON DUPLICATE KEY UPDATE `from_time`=?,`checked_at`=?",
		code.Market.String(), code.Code, year, scan.From, scan.CheckedAt, scan.From, scan.CheckedAt)
	if err != nil {
		return fmt.Errorf("pull: 写入分钟空扫描: %w", err)
	}
	return nil
}

func (s *MySQLStore) LoadUpdate(key string) (int64, bool, error) {
	var unix int64
	err := s.db.QueryRowContext(context.Background(),
		"SELECT `time` FROM `update_marks` WHERE `mark_key`=?", key).Scan(&unix)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("pull: 查询更新标记: %w", err)
	}
	return unix, true, nil
}

func (s *MySQLStore) SaveUpdate(key string, unix int64) error {
	_, err := s.db.ExecContext(context.Background(),
		"INSERT INTO `update_marks` (`mark_key`,`time`) VALUES (?,?) ON DUPLICATE KEY UPDATE `time`=?",
		key, unix, unix)
	if err != nil {
		return fmt.Errorf("pull: 写入更新标记: %w", err)
	}
	return nil
}

// mysqlRunTx 在独立事务中执行 fn，遇到死锁或锁等待超时按 mysqlTxAttempts 重试。
// MySQL 死锁会回滚整个事务，重新执行可保证 SaveDay/SaveMinute 的原子性不变。
func mysqlRunTx(ctx context.Context, db *sql.DB, fn func(*sql.Tx) error) error {
	var err error
	for attempt := 0; attempt < mysqlTxAttempts; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(attempt) * 50 * time.Millisecond):
			}
		}
		var tx *sql.Tx
		if tx, err = db.BeginTx(ctx, nil); err != nil {
			return err
		}
		if err = fn(tx); err == nil {
			err = tx.Commit()
		}
		if err != nil {
			tx.Rollback()
		}
		if err == nil || !mysqlRetryable(err) {
			return err
		}
	}
	return err
}

// mysqlRetryable 判断错误是否为可安全重试的事务级失败：1213 死锁、1205 锁等待超时，
// 以及连接在事务中被服务端/网络中断（此时事务已回滚，重放不会产生重复数据）。
func mysqlRetryable(err error) bool {
	if errors.Is(err, mysql.ErrInvalidConn) || errors.Is(err, driver.ErrBadConn) {
		return true
	}
	var me *mysql.MySQLError
	return errors.As(err, &me) && (me.Number == 1213 || me.Number == 1205)
}

// mysqlDeleteTail 删除某证券 Time>=from 的尾部。先做一次不加锁的存在性检查：
// 无匹配行时跳过 DELETE，避免空范围扫描对相邻证券的键区间加间隙锁，进而与并发插入成环。
func mysqlTailWhere(code, from string) (string, []any) {
	where := " WHERE `code`=?"
	args := []any{code}
	if from != "" {
		where += " AND `time`>=?"
		args = append(args, from)
	}
	return where, args
}

func mysqlHasTail(ctx context.Context, tx *sql.Tx, table, code, from string) (bool, error) {
	where, args := mysqlTailWhere(code, from)
	var one int
	err := tx.QueryRowContext(ctx, "SELECT 1 FROM `"+table+"`"+where+" LIMIT 1", args...).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

func mysqlDeleteTail(ctx context.Context, tx *sql.Tx, table string, code model.Code, from string) error {
	has, err := mysqlHasTail(ctx, tx, table, code.Code, from)
	if err != nil || !has {
		return err
	}
	where, args := mysqlTailWhere(code.Code, from)
	_, err = tx.ExecContext(ctx, "DELETE FROM `"+table+"`"+where, args...)
	return err
}

// mysqlInsert 在一个事务内批量插入；满批复用 Prepared Statement，
// 避免 InterpolateParams 关闭时的逐批 Prepare 往返。
func mysqlInsert(ctx context.Context, tx *sql.Tx, table, columns string, rows [][]any, keepExisting bool) (int, error) {
	if len(rows) == 0 {
		return 0, nil
	}
	inserted := int64(0)
	width := len(rows[0])
	pos := 0
	full := len(rows) / mysqlInsertBatch
	if full > 0 {
		stmt, err := tx.PrepareContext(ctx, mysqlInsertSQL(table, columns, mysqlInsertBatch, keepExisting))
		if err != nil {
			return 0, fmt.Errorf("pull: 准备批量插入: %w", err)
		}
		for i := 0; i < full; i++ {
			args := make([]any, 0, mysqlInsertBatch*width)
			for _, row := range rows[i*mysqlInsertBatch : (i+1)*mysqlInsertBatch] {
				args = append(args, row...)
			}
			result, err := stmt.ExecContext(ctx, args...)
			if err != nil {
				stmt.Close()
				return 0, fmt.Errorf("pull: 批量插入: %w", err)
			}
			count, err := result.RowsAffected()
			if err != nil {
				stmt.Close()
				return 0, err
			}
			inserted += count
		}
		if err := stmt.Close(); err != nil {
			return 0, err
		}
		pos = full * mysqlInsertBatch
	}
	if rest := rows[pos:]; len(rest) > 0 {
		args := make([]any, 0, len(rest)*width)
		for _, row := range rest {
			args = append(args, row...)
		}
		result, err := tx.ExecContext(ctx, mysqlInsertSQL(table, columns, len(rest), keepExisting), args...)
		if err != nil {
			return 0, fmt.Errorf("pull: 插入尾批: %w", err)
		}
		count, err := result.RowsAffected()
		if err != nil {
			return 0, err
		}
		inserted += count
	}
	return int(inserted), nil
}

func mysqlInsertSQL(table, columns string, rows int, keepExisting bool) string {
	var b strings.Builder
	b.WriteString("INSERT INTO `")
	b.WriteString(table)
	b.WriteString("` (")
	b.WriteString(columns)
	b.WriteString(") VALUES ")
	placeholders := "(" + strings.TrimSuffix(strings.Repeat("?,", strings.Count(columns, ",")+1), ",") + ")"
	for i := 0; i < rows; i++ {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(placeholders)
	}
	if keepExisting {
		b.WriteString(" ON DUPLICATE KEY UPDATE `time`=`time`")
	}
	return b.String()
}
