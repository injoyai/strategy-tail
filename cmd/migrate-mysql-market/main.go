// migrate-mysql-market copies paused legacy MySQL tables into market/year tables.
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/go-sql-driver/mysql"
	common "github.com/injoyai/strategy-tail"
	"github.com/injoyai/strategy-tail/internal/marketstore"
	"github.com/injoyai/tdx/v2/protocol"
)

var legacyTable = regexp.MustCompile(`^kline_(day|minute_[0-9]{4})$`)

const schemaDDL = "CREATE TABLE IF NOT EXISTS kline_schema (version VARCHAR(32) CHARACTER SET ascii COLLATE ascii_bin PRIMARY KEY, ready TINYINT NOT NULL DEFAULT 0) ENGINE=InnoDB"

type job struct {
	Source  string   `json:"source"`
	Target  string   `json:"target"`
	Market  string   `json:"market"`
	Code    string   `json:"code"`
	Columns []string `json:"-"`
}
type progress struct {
	job
	Rows     int64   `json:"rows"`
	Added    int64   `json:"added"`
	Verified bool    `json:"verified"`
	Done     int     `json:"done"`
	Total    int     `json:"total"`
	Seconds  float64 `json:"seconds"`
	Error    string  `json:"error,omitempty"`
}
type options struct {
	Apply       bool
	Paused      bool
	SpacePath   string
	MinFree     uint64
	InPlace     bool
	StateDir    string
	BackupProof string
	TmpPath     string
}

func columns(table string) []string {
	c := []string{"code", "time", "open", "high", "low", "close", "volume", "amount"}
	if table == "kline_day" {
		return append(c, "turnover", "float_stock", "total_stock")
	}
	return append(c, "source")
}
func quoted(c []string) string { return "`" + strings.Join(c, "`,`") + "`" }
func targetTable(source, market string) (string, error) {
	if !legacyTable.MatchString(source) {
		return "", fmt.Errorf("invalid legacy table %q", source)
	}
	if source == "kline_day" {
		return marketstore.MySQLDayTable(protocol.Market(market))
	}
	var year int
	if _, err := fmt.Sscanf(source, "kline_minute_%d", &year); err != nil {
		return "", err
	}
	return marketstore.MySQLMinuteTable(protocol.Market(market), year)
}
func plan(ctx context.Context, db *sql.DB) ([]job, []string, error) {
	r, err := db.QueryContext(ctx, "SELECT TABLE_NAME FROM information_schema.TABLES WHERE TABLE_SCHEMA=DATABASE() AND (TABLE_NAME='kline_day' OR TABLE_NAME REGEXP '^kline_minute_[0-9]{4}$') ORDER BY TABLE_NAME")
	if err != nil {
		return nil, nil, err
	}
	tables := []string{}
	for r.Next() {
		var t string
		if err := r.Scan(&t); err != nil {
			r.Close()
			return nil, nil, err
		}
		if !legacyTable.MatchString(t) {
			r.Close()
			return nil, nil, errors.New("unsafe source table")
		}
		tables = append(tables, t)
	}
	err = r.Err()
	r.Close()
	if err != nil {
		return nil, nil, err
	}
	if len(tables) == 0 {
		return nil, nil, errors.New("no legacy tables")
	}
	jobs := []job{}
	for _, table := range tables {
		r, err := db.QueryContext(ctx, "SELECT DISTINCT market,code FROM `"+table+"` ORDER BY market,code")
		if err != nil {
			return nil, nil, err
		}
		for r.Next() {
			var market, code string
			if err := r.Scan(&market, &code); err != nil {
				r.Close()
				return nil, nil, err
			}
			target, err := targetTable(table, market)
			if err != nil {
				r.Close()
				return nil, nil, err
			}
			jobs = append(jobs, job{table, target, market, code, columns(table)})
		}
		err = r.Err()
		r.Close()
		if err != nil {
			return nil, nil, err
		}
	}
	return jobs, tables, nil
}

// CREATE LIKE preserves every existing value type; the empty clone is then rebuilt.
func ensureTarget(ctx context.Context, db *sql.DB, j job) error {
	var exists int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM information_schema.TABLES WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME=?", j.Target).Scan(&exists); err != nil {
		return err
	}
	if exists == 0 {
		if _, err := db.ExecContext(ctx, "CREATE TABLE `"+j.Target+"` LIKE `"+j.Source+"`"); err != nil {
			return err
		}
	}
	var marketColumn int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM information_schema.COLUMNS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME=? AND COLUMN_NAME='market'", j.Target).Scan(&marketColumn); err != nil {
		return err
	}
	if marketColumn > 0 {
		var n int64
		if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM `"+j.Target+"`").Scan(&n); err != nil {
			return err
		}
		if n != 0 {
			return errors.New("refusing to alter a populated target with a market column")
		}
		if _, err := db.ExecContext(ctx, "ALTER TABLE `"+j.Target+"` DROP PRIMARY KEY, DROP COLUMN market, ADD PRIMARY KEY(code,time), CONVERT TO CHARACTER SET utf8mb4 COLLATE utf8mb4_bin"); err != nil {
			return err
		}
	}
	r, err := db.QueryContext(ctx, "SELECT COLUMN_NAME,IS_NULLABLE,COLLATION_NAME FROM information_schema.COLUMNS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME=? ORDER BY ORDINAL_POSITION", j.Target)
	if err != nil {
		return err
	}
	got := []string{}
	for r.Next() {
		var name, nullable string
		var collation sql.NullString
		if err := r.Scan(&name, &nullable, &collation); err != nil {
			r.Close()
			return err
		}
		if nullable != "NO" {
			r.Close()
			return fmt.Errorf("unexpected nullable target column %s", name)
		}
		if collation.Valid && collation.String != "utf8mb4_bin" {
			r.Close()
			return errors.New("target text collation is not binary")
		}
		got = append(got, name)
	}
	err = r.Err()
	r.Close()
	if err != nil {
		return err
	}
	if strings.Join(got, ",") != strings.Join(j.Columns, ",") {
		return fmt.Errorf("unexpected target columns %v", got)
	}
	var pk sql.NullString
	if err := db.QueryRowContext(ctx, "SELECT GROUP_CONCAT(COLUMN_NAME ORDER BY SEQ_IN_INDEX) FROM information_schema.STATISTICS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME=? AND INDEX_NAME='PRIMARY'", j.Target).Scan(&pk); err != nil {
		return err
	}
	if pk.String != "code,time" {
		return fmt.Errorf("unexpected target primary key %q", pk.String)
	}
	return nil
}

func compareSQL(j job) string {
	matches := []string{}
	for _, c := range j.Columns {
		if c == "code" || c == "time" {
			continue
		}
		if c == "source" {
			matches = append(matches, "(BINARY a.`source` <=> BINARY b.`source`)")
		} else {
			matches = append(matches, "(a.`"+c+"` <=> b.`"+c+"`)")
		}
	}
	return "SELECT COUNT(*),COALESCE(SUM(b.code IS NULL OR NOT (" + strings.Join(matches, " AND ") + ")),0),COALESCE(SUM(NOT (BINARY a.market <=> BINARY ?)),0) FROM `" + j.Source + "` a LEFT JOIN `" + j.Target + "` b ON b.code=a.code COLLATE utf8mb4_bin AND b.time=a.time WHERE a.market=? AND a.code=?"
}
func copyJob(ctx context.Context, db *sql.DB, j job) (int64, int64, error) {
	tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback()
	cols := quoted(j.Columns)
	result, err := tx.ExecContext(ctx, "INSERT INTO `"+j.Target+"` ("+cols+") SELECT "+cols+" FROM `"+j.Source+"` WHERE market=? AND code=? ORDER BY time ON DUPLICATE KEY UPDATE time=`"+j.Target+"`.time", j.Market, j.Code)
	if err != nil {
		return 0, 0, err
	}
	added, err := result.RowsAffected()
	if err != nil {
		return 0, 0, err
	}
	var rows, mismatch, badMarket, targetRows int64
	if err := tx.QueryRowContext(ctx, compareSQL(j), j.Market, j.Market, j.Code).Scan(&rows, &mismatch, &badMarket); err != nil {
		return 0, 0, err
	}
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM `"+j.Target+"` WHERE code=?", j.Code).Scan(&targetRows); err != nil {
		return 0, 0, err
	}
	if mismatch != 0 || badMarket != 0 || rows != targetRows {
		return 0, 0, fmt.Errorf("verification failed: source=%d target=%d mismatches=%d noncanonicalMarkets=%d", rows, targetRows, mismatch, badMarket)
	}
	if err := tx.Commit(); err != nil {
		return 0, 0, err
	}
	return rows, added, nil
}
func freeSpace(path string, min uint64) error {
	if path == "" {
		return nil
	}
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return err
	}
	available := uint64(st.Bavail) * uint64(st.Bsize)
	if available < min {
		return fmt.Errorf("free space below reserve: %d bytes", available)
	}
	return nil
}
func verifyCodes(ctx context.Context, db *sql.DB, jobs []job) error {
	expected := map[string]map[string]bool{}
	for _, j := range jobs {
		if expected[j.Target] == nil {
			expected[j.Target] = map[string]bool{}
		}
		expected[j.Target][j.Code] = true
	}
	for table, codes := range expected {
		r, err := db.QueryContext(ctx, "SELECT DISTINCT code FROM `"+table+"`")
		if err != nil {
			return err
		}
		n := 0
		for r.Next() {
			var code string
			if err := r.Scan(&code); err != nil {
				r.Close()
				return err
			}
			if !codes[code] {
				r.Close()
				return fmt.Errorf("unexpected target code in %s: %q", table, code)
			}
			n++
		}
		err = r.Err()
		r.Close()
		if err != nil {
			return err
		}
		if n != len(codes) {
			return fmt.Errorf("target code count mismatch: %s", table)
		}
	}
	return nil
}

func migrate(ctx context.Context, db *sql.DB, opt options, enc *json.Encoder) error {
	if opt.InPlace {
		return migrateInPlace(ctx, db, opt, enc)
	}
	if opt.Apply && !opt.Paused {
		return errors.New("-apply requires explicit -writers-paused")
	}
	if opt.Apply {
		if err := freeSpace(opt.SpacePath, opt.MinFree); err != nil {
			return err
		}
	}
	// The lock connection stays pinned; losing it aborts further work.
	guard, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer guard.Close()
	if opt.Apply {
		var acquired int
		if err := guard.QueryRowContext(ctx, "SELECT GET_LOCK(CONCAT(DATABASE(),':market-split-v1'),0)").Scan(&acquired); err != nil {
			return err
		}
		if acquired != 1 {
			return errors.New("another market split is running")
		}
		defer guard.ExecContext(context.Background(), "SELECT RELEASE_LOCK(CONCAT(DATABASE(),':market-split-v1'))")
	}
	jobs, tables, err := plan(ctx, db)
	if err != nil {
		return err
	}
	targets := map[string]job{}
	for _, j := range jobs {
		targets[j.Target] = j
	}
	if !opt.Apply {
		names := []string{}
		for name := range targets {
			names = append(names, name)
		}
		sort.Strings(names)
		return enc.Encode(map[string]any{"plan": true, "sourceTables": tables, "targetTables": names, "jobs": len(jobs), "writes": false})
	}
	var ready int
	err = db.QueryRowContext(ctx, "SELECT ready FROM kline_schema WHERE version='market-v1'").Scan(&ready)
	var me *mysql.MySQLError
	if err != nil && !errors.Is(err, sql.ErrNoRows) && !(errors.As(err, &me) && me.Number == 1146) {
		return err
	}
	if err == nil && ready == 1 {
		return errors.New("market-v1 is already ready; do not replay legacy data after cutover")
	}
	// READ locks prevent even an overlooked legacy updater from changing the source.
	locks := []string{}
	for _, table := range tables {
		locks = append(locks, "`"+table+"` READ")
	}
	if _, err := guard.ExecContext(ctx, "SET SESSION lock_wait_timeout=15"); err != nil {
		return err
	}
	if _, err := guard.ExecContext(ctx, "LOCK TABLES "+strings.Join(locks, ",")); err != nil {
		return err
	}
	defer guard.ExecContext(context.Background(), "UNLOCK TABLES")
	// Re-enumerate after acquiring the source locks.
	jobs, lockedTables, err := plan(ctx, db)
	if err != nil {
		return err
	}
	if strings.Join(lockedTables, ",") != strings.Join(tables, ",") {
		return errors.New("source table list changed while locking")
	}
	targets = map[string]job{}
	for _, j := range jobs {
		targets[j.Target] = j
	}
	if _, err := db.ExecContext(ctx, schemaDDL); err != nil {
		return err
	}
	if _, err := db.ExecContext(ctx, "INSERT INTO kline_schema(version,ready) VALUES ('market-v1',0) ON DUPLICATE KEY UPDATE ready=0"); err != nil {
		return err
	}
	for _, j := range targets {
		if err := ensureTarget(ctx, db, j); err != nil {
			return fmt.Errorf("prepare %s: %w", j.Target, err)
		}
	}
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(stop)
	started := time.Now()
	var rows, added int64
	for i, j := range jobs {
		select {
		case <-stop:
			return errors.New("paused after completing in-flight transaction; rerun to verify and continue")
		default:
		}
		if err := freeSpace(opt.SpacePath, opt.MinFree); err != nil {
			return err
		}
		if err := guard.PingContext(ctx); err != nil {
			return fmt.Errorf("source lock connection lost: %w", err)
		}
		jobCtx, cancel := context.WithTimeout(ctx, 20*time.Minute)
		count, n, err := copyJob(jobCtx, db, j)
		cancel()
		p := progress{job: j, Rows: count, Added: n, Verified: err == nil, Done: i + 1, Total: len(jobs), Seconds: time.Since(started).Seconds()}
		if err != nil {
			p.Error = err.Error()
		}
		if e := enc.Encode(p); e != nil {
			return e
		}
		if err != nil {
			return fmt.Errorf("%s/%s/%s: %w", j.Source, j.Market, j.Code, err)
		}
		rows += count
		added += n
		if (i+1)%100 == 0 {
			fmt.Fprintf(os.Stderr, "verified=%d/%d rows=%d added=%d elapsed=%s\n", i+1, len(jobs), rows, added, time.Since(started).Round(time.Second))
		}
	}
	if err := verifyCodes(ctx, db, jobs); err != nil {
		return err
	}
	if err := guard.PingContext(ctx); err != nil {
		return fmt.Errorf("source lock lost before final acceptance: %w", err)
	}
	if _, err := db.ExecContext(ctx, "UPDATE kline_schema SET ready=1 WHERE version='market-v1' AND ready=0"); err != nil {
		return err
	}
	return enc.Encode(map[string]any{"summary": true, "complete": true, "sourceTables": len(tables), "targetTables": len(targets), "verifiedJobs": len(jobs), "rows": rows, "added": added, "seconds": time.Since(started).Seconds(), "oldTablesPreserved": true})
}
func run() error {
	reloadYear := flag.Int("reload-from-year", 0, "从只读冷备源向新空库重导指定年份起的行情")
	inplace := flag.Bool("in-place", false, "迁出指数后复用股票原表，使用 INPLACE 重建主键")
	stateDir := flag.String("state-dir", "", "原表复用的持久化 SHA256 断点目录")
	backupProof := flag.String("backup-proof", "", "root 验证的冷备恢复点凭据 JSON（不含密码）")
	tmpPath := flag.String("tmp-space-path", "", "MySQL 临时目录所在的宿主机卷")
	apply := flag.Bool("apply", false, "执行复制；默认仅输出只读计划")
	paused := flag.Bool("writers-paused", false, "确认全部行情写入已暂停")
	stdin := flag.Bool("config-stdin", false, "从标准输入读取 JSON DSN，不落盘")
	expectedUUID := flag.String("expected-server-uuid", "", "执行时必须明确指定目标 MySQL UUID")
	space := flag.String("space-path", "", "与数据库同卷的空间检查路径；生产执行必须指定")
	reserve := flag.Uint64("min-free-gib", 100, "磁盘最小保留 GiB")
	flag.Parse()
	if *reloadYear != 0 {
		if !*stdin {
			return errors.New("reload requires private source and target configuration over stdin")
		}
		var c reloadConnections
		if err := json.NewDecoder(os.Stdin).Decode(&c); err != nil {
			return errors.New("cannot read private reload connections")
		}
		return runReload(context.Background(), c, options{Apply: *apply, Paused: *paused, StateDir: *stateDir, SpacePath: *space, MinFree: *reserve * 1024 * 1024 * 1024, BackupProof: *backupProof}, *reloadYear, *expectedUUID, json.NewEncoder(os.Stdout))
	}
	var dsn string
	var err error
	if *stdin {
		err = json.NewDecoder(os.Stdin).Decode(&dsn)
	} else {
		dsn, err = common.MySQLDSN()
	}
	if err != nil {
		return errors.New("cannot read private MySQL configuration")
	}
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		return errors.New("invalid MySQL DSN")
	}
	cfg.ParseTime = false
	cfg.ClientFoundRows = false
	cfg.InterpolateParams = false
	cfg.Timeout = 5 * time.Second
	cfg.ReadTimeout = 20 * time.Minute
	if *inplace {
		cfg.ReadTimeout = 12 * time.Hour
	}
	if cfg.Params == nil {
		cfg.Params = map[string]string{}
	}
	cfg.Params["charset"] = "utf8mb4"
	db, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		return err
	}
	defer db.Close()
	db.SetMaxOpenConns(3)
	ctx := context.Background()
	var uuid, database string
	if err := db.QueryRowContext(ctx, "SELECT @@server_uuid,DATABASE()").Scan(&uuid, &database); err != nil {
		return err
	}
	if *apply && (*expectedUUID == "" || *expectedUUID != uuid) {
		return errors.New("-apply requires matching -expected-server-uuid")
	}
	if *apply && *space == "" {
		return errors.New("-apply requires -space-path on the database host")
	}
	if err := json.NewEncoder(os.Stdout).Encode(map[string]any{"serverUUID": uuid, "database": database, "apply": *apply, "startedUTC": time.Now().UTC()}); err != nil {
		return err
	}
	if *inplace {
		if !*apply || *stateDir == "" || *backupProof == "" || *tmpPath == "" {
			return errors.New("-in-place requires -apply, -state-dir, -backup-proof and -tmp-space-path")
		}
		if err := verifyBackup(*backupProof, uuid); err != nil {
			return err
		}
	}
	return migrate(ctx, db, options{Apply: *apply, Paused: *paused, SpacePath: *space, MinFree: *reserve * 1024 * 1024 * 1024, InPlace: *inplace, StateDir: *stateDir, BackupProof: *backupProof, TmpPath: *tmpPath}, json.NewEncoder(os.Stdout))
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
