package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)

type digest struct {
	Rows   int64  `json:"rows"`
	SHA256 string `json:"sha256"`
}
type inplaceState struct {
	Source          string `json:"source"`
	Stock           string `json:"stock"`
	Index           string `json:"index"`
	StockDigest     digest `json:"stockDigest"`
	IndexDigest     digest `json:"indexDigest"`
	IndicesVerified bool   `json:"indicesVerified"`
	Complete        bool   `json:"complete"`
}
type inplaceManifest struct {
	UUID   string   `json:"serverUUID"`
	Tables []string `json:"tables"`
}

func saveJSON(path string, v any) error {
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".state-")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if _, err = f.Write(raw); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = os.Rename(name, path); err != nil {
		return err
	}
	d, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
func hashValue(h hash.Hash, value []byte, valid bool) {
	if !valid {
		h.Write([]byte{0})
		return
	}
	h.Write([]byte{1})
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], uint64(len(value)))
	h.Write(size[:])
	h.Write(value)
}

// SQL casts are identical before/after ALTER, including float and calendar text.
func tableDigest(ctx context.Context, c *sql.Conn, table string, fields []string, market string) (digest, error) {
	expr := make([]string, len(fields))
	for i, f := range fields {
		expr[i] = "CAST(`" + f + "` AS CHAR CHARACTER SET utf8mb4)"
	}
	if market != "" {
		expr = append(expr, "market")
	}
	q := "SELECT " + strings.Join(expr, ",") + " FROM `" + table + "`"
	var args []any
	if market != "" {
		q += " WHERE market=?"
		args = append(args, market)
	}
	q += " ORDER BY code,time"
	r, err := c.QueryContext(ctx, q, args...)
	if err != nil {
		return digest{}, err
	}
	defer r.Close()
	h := sha256.New()
	var count int64
	values := make([]sql.RawBytes, len(expr))
	ptrs := make([]any, len(expr))
	for i := range ptrs {
		ptrs[i] = &values[i]
	}
	lastLog := time.Now()
	for r.Next() {
		if err := r.Scan(ptrs...); err != nil {
			return digest{}, err
		}
		if market != "" && string(values[len(fields)]) != market {
			return digest{}, errors.New("noncanonical source market")
		}
		for _, v := range values[:len(fields)] {
			hashValue(h, v, v != nil)
		}
		count++
		if time.Since(lastLog) > 30*time.Second {
			fmt.Fprintf(os.Stderr, "digest table=%s market=%s rows=%d\n", table, market, count)
			lastLog = time.Now()
		}
	}
	if err := r.Err(); err != nil {
		return digest{}, err
	}
	return digest{count, hex.EncodeToString(h.Sum(nil))}, nil
}
func equalSQL(fields []string, a, b string) string {
	matches := []string{}
	for _, f := range fields {
		if f == "code" || f == "time" {
			continue
		}
		x, y := "`"+a+"`.`"+f+"`", "`"+b+"`.`"+f+"`"
		if f == "source" {
			x, y = "BINARY "+x, "BINARY "+y
		}
		matches = append(matches, "("+x+" <=> "+y+")")
	}
	return strings.Join(matches, " AND ")
}
func indexMismatch(ctx context.Context, c *sql.Conn, j job, code string) (int64, int64, error) {
	source, target := "`"+j.Source+"`", "`"+j.Target+"`"
	q := "SELECT COUNT(*),COALESCE(SUM(" + target + ".code IS NULL OR NOT (" + equalSQL(j.Columns, j.Source, j.Target) + ")),0) FROM " + source + " LEFT JOIN " + target + " ON " + target + ".code=" + source + ".code COLLATE utf8mb4_bin AND " + target + ".time=" + source + ".time WHERE " + source + ".market=?"
	args := []any{"cn/index"}
	if code != "" {
		q += " AND " + source + ".code=?"
		args = append(args, code)
	}
	var n, bad int64
	err := c.QueryRowContext(ctx, q, args...).Scan(&n, &bad)
	return n, bad, err
}

// Explicit LOCK TABLES survives COMMIT; BEGIN would release the source fence.
func copyIndexCode(ctx context.Context, c *sql.Conn, j job, code string) error {
	cols := quoted(j.Columns)
	_, err := c.ExecContext(ctx, "INSERT INTO `"+j.Target+"` ("+cols+") SELECT "+cols+" FROM `"+j.Source+"` WHERE market=? AND code=? ORDER BY time ON DUPLICATE KEY UPDATE time=`"+j.Target+"`.time", "cn/index", code)
	if err == nil {
		var n, bad, target int64
		n, bad, err = indexMismatch(ctx, c, j, code)
		if err == nil {
			err = c.QueryRowContext(ctx, "SELECT COUNT(*) FROM `"+j.Target+"` WHERE code=?", code).Scan(&target)
		}
		if err == nil && (bad != 0 || n != target) {
			err = fmt.Errorf("index conflict: source=%d target=%d mismatch=%d", n, target, bad)
		}
	}
	if err != nil {
		c.ExecContext(context.Background(), "ROLLBACK")
		return err
	}
	_, err = c.ExecContext(ctx, "COMMIT")
	return err
}
func tableExists(ctx context.Context, db *sql.DB, table string) (bool, error) {
	var n int
	err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM information_schema.TABLES WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME=?", table).Scan(&n)
	return n == 1, err
}
func readState(path string) (inplaceState, error) {
	var s inplaceState
	b, err := os.ReadFile(path)
	if err != nil {
		return s, err
	}
	err = json.Unmarshal(b, &s)
	return s, err
}
func verifyBackup(path, uuid string) error {
	st, err := os.Lstat(path)
	if err != nil {
		return err
	}
	parent, err := os.Lstat(filepath.Dir(path))
	if err != nil {
		return err
	}
	if !st.Mode().IsRegular() || st.Mode()&0022 != 0 || !parent.IsDir() || parent.Mode()&0022 != 0 {
		return errors.New("unsafe recovery proof")
	}
	if st.Sys().(*syscall.Stat_t).Uid != 0 || parent.Sys().(*syscall.Stat_t).Uid != 0 {
		return errors.New("recovery proof must be root-owned")
	}
	var p struct {
		Complete     bool   `json:"complete"`
		UUID         string `json:"serverUUID"`
		Clean        bool   `json:"cleanShutdown"`
		Reflink      bool   `json:"reflinkRequired"`
		Differences  int    `json:"metadataDifferences"`
		ManifestSHA  string `json:"manifestSHA256"`
		LogicalBytes int64  `json:"logicalBytes"`
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err = json.Unmarshal(raw, &p); err != nil {
		return err
	}
	if !p.Complete || p.UUID != uuid || !p.Clean || !p.Reflink || p.Differences != 0 || p.LogicalBytes <= 0 {
		return errors.New("recovery point not verified for this server")
	}
	manifestPath := filepath.Join(filepath.Dir(path), "backup-manifest.json")
	ms, err := os.Lstat(manifestPath)
	if err != nil {
		return err
	}
	if !ms.Mode().IsRegular() || ms.Mode()&0022 != 0 || ms.Sys().(*syscall.Stat_t).Uid != 0 {
		return errors.New("unsafe recovery manifest")
	}
	raw, err = os.ReadFile(manifestPath)
	if err != nil {
		return err
	}
	h := sha256.Sum256(raw)
	if hex.EncodeToString(h[:]) != p.ManifestSHA {
		return errors.New("recovery manifest hash mismatch")
	}
	return nil
}

func processInplaceTable(ctx context.Context, db *sql.DB, source, stateDir, tmpPath string, enc *json.Encoder) error {
	stock, err := targetTable(source, "cn/stock")
	if err != nil {
		return err
	}
	index, err := targetTable(source, "cn/index")
	if err != nil {
		return err
	}
	path := filepath.Join(stateDir, source+".json")
	state, err := readState(path)
	saved := err == nil
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if saved && (state.Source != source || state.Stock != stock || state.Index != index) {
		return errors.New("checkpoint table identity mismatch")
	}
	sourceExists, err := tableExists(ctx, db, source)
	if err != nil {
		return err
	}
	stockExists, err := tableExists(ctx, db, stock)
	if err != nil {
		return err
	}
	if sourceExists == stockExists {
		return fmt.Errorf("expected exactly one old/new stock table: %s", source)
	}
	j := job{Source: source, Target: index, Market: "cn/index", Columns: columns(source)}
	if sourceExists {
		if err := ensureTarget(ctx, db, j); err != nil {
			return err
		}
	} else if !saved || !state.IndicesVerified {
		return errors.New("renamed stock table has no verified recovery checkpoint")
	}
	current := source
	if !sourceExists {
		current = stock
	}
	c, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer func() {
		c.ExecContext(context.Background(), "ROLLBACK")
		c.ExecContext(context.Background(), "UNLOCK TABLES")
		c.ExecContext(context.Background(), "SET autocommit=1")
		c.Close()
	}()
	if _, err = c.ExecContext(ctx, "SET autocommit=0"); err != nil {
		return err
	}
	if _, err = c.ExecContext(ctx, "SET SESSION lock_wait_timeout=15"); err != nil {
		return err
	}
	lock := func(name string) error {
		_, err := c.ExecContext(ctx, "LOCK TABLES `"+name+"` WRITE,`"+index+"` WRITE")
		return err
	}
	if err := lock(current); err != nil {
		return err
	}
	emit := func(stage string, extra map[string]any) error {
		r := map[string]any{"stage": stage, "source": source, "stock": stock, "index": index, "atUTC": time.Now().UTC()}
		for k, v := range extra {
			r[k] = v
		}
		return enc.Encode(r)
	}
	if err := emit("verifying-stock-baseline", nil); err != nil {
		return err
	}
	market := "cn/stock"
	if !sourceExists {
		market = ""
	}
	before, err := tableDigest(ctx, c, current, j.Columns, market)
	if err != nil {
		return err
	}
	if saved {
		if before != state.StockDigest {
			return fmt.Errorf("stock checkpoint differs: %s", source)
		}
	} else {
		state = inplaceState{Source: source, Stock: stock, Index: index, StockDigest: before}
		if err := saveJSON(path, state); err != nil {
			return err
		}
	}
	if sourceExists {
		if !state.IndicesVerified {
			r, err := c.QueryContext(ctx, "SELECT DISTINCT code FROM `"+source+"` WHERE market=? ORDER BY code", "cn/index")
			if err != nil {
				return err
			}
			codes := []string{}
			for r.Next() {
				var code string
				if err := r.Scan(&code); err != nil {
					r.Close()
					return err
				}
				codes = append(codes, code)
			}
			err = r.Err()
			r.Close()
			if err != nil {
				return err
			}
			if err := emit("copying-indices", map[string]any{"codes": len(codes)}); err != nil {
				return err
			}
			for _, code := range codes {
				if err := copyIndexCode(ctx, c, j, code); err != nil {
					return err
				}
			}
			sourceDigest, err := tableDigest(ctx, c, source, j.Columns, "cn/index")
			if err != nil {
				return err
			}
			targetDigest, err := tableDigest(ctx, c, index, j.Columns, "")
			if err != nil {
				return err
			}
			if sourceDigest != targetDigest {
				return errors.New("full index digest mismatch or extra target records")
			}
			state.IndexDigest = sourceDigest
			state.IndicesVerified = true
			if err := saveJSON(path, state); err != nil {
				return err
			}
		} else {
			targetDigest, err := tableDigest(ctx, c, index, j.Columns, "")
			if err != nil {
				return err
			}
			if targetDigest != state.IndexDigest {
				return errors.New("saved index digest changed")
			}
		}
		if err := emit("indices-verified", map[string]any{"rows": state.IndexDigest.Rows, "sha256": state.IndexDigest.SHA256}); err != nil {
			return err
		}
		_, bad, err := indexMismatch(ctx, c, j, "")
		if err != nil {
			return err
		}
		if bad != 0 {
			return errors.New("remaining original index rows differ from saved copy")
		}
		var deleted int64
		for {
			result, err := c.ExecContext(ctx, "DELETE FROM `"+source+"` WHERE market=? ORDER BY code,time LIMIT 10000", "cn/index")
			if err != nil {
				return err
			}
			n, err := result.RowsAffected()
			if err != nil {
				return err
			}
			if _, err = c.ExecContext(ctx, "COMMIT"); err != nil {
				return err
			}
			deleted += n
			if n == 0 {
				break
			}
			if deleted%1000000 == 0 {
				if err := emit("removing-copied-indices", map[string]any{"deleted": deleted}); err != nil {
					return err
				}
			}
		}
		var unexpected string
		err = c.QueryRowContext(ctx, "SELECT market FROM `"+source+"` WHERE market<>? LIMIT 1", "cn/stock").Scan(&unexpected)
		if err == nil {
			return fmt.Errorf("unexpected residual market %q", unexpected)
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if _, err = c.ExecContext(ctx, "COMMIT"); err != nil {
			return err
		}
		if err := emit("before-alter", map[string]any{"stockRows": before.Rows, "indexRows": state.IndexDigest.Rows}); err != nil {
			return err
		}
		// Keep each old collation: converting historical CI tables would force COPY.
		_, err = c.ExecContext(ctx, "ALTER TABLE `"+source+"` DROP PRIMARY KEY,DROP COLUMN market,ADD PRIMARY KEY(code,time),RENAME TO `"+stock+"`,ALGORITHM=INPLACE,LOCK=EXCLUSIVE")
		if err != nil {
			return err
		}
		if err := emit("altered", nil); err != nil {
			return err
		}
		if _, err = c.ExecContext(ctx, "UNLOCK TABLES"); err != nil {
			return err
		}
		if err := lock(stock); err != nil {
			return err
		}
	}
	if err := emit("verifying-stock-result", nil); err != nil {
		return err
	}
	after, err := tableDigest(ctx, c, stock, j.Columns, "")
	if err != nil {
		return err
	}
	if after != state.StockDigest {
		return errors.New("stock count or full-field SHA256 changed after ALTER")
	}
	indexAfter, err := tableDigest(ctx, c, index, j.Columns, "")
	if err != nil {
		return err
	}
	if indexAfter != state.IndexDigest {
		return errors.New("index copy changed after ALTER")
	}
	if _, err = c.ExecContext(ctx, "COMMIT"); err != nil {
		return err
	}
	state.Complete = true
	if err := saveJSON(path, state); err != nil {
		return err
	}
	return emit("table-complete", map[string]any{"stockRows": after.Rows, "stockSHA256": after.SHA256, "indexRows": indexAfter.Rows, "indexSHA256": indexAfter.SHA256})
}

func migrateInPlace(ctx context.Context, db *sql.DB, opt options, enc *json.Encoder) error {
	if !opt.Apply || !opt.Paused {
		return errors.New("in-place migration requires -apply -writers-paused")
	}
	if opt.StateDir == "" {
		return errors.New("in-place migration requires a durable state directory")
	}
	if err := os.MkdirAll(opt.StateDir, 0700); err != nil {
		return err
	}
	guard, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer guard.Close()
	var acquired int
	if err := guard.QueryRowContext(ctx, "SELECT GET_LOCK(CONCAT(DATABASE(),':market-split-v1'),0)").Scan(&acquired); err != nil {
		return err
	}
	if acquired != 1 {
		return errors.New("another market split is running")
	}
	defer guard.ExecContext(context.Background(), "SELECT RELEASE_LOCK(CONCAT(DATABASE(),':market-split-v1'))")
	var uuid string
	if err := db.QueryRowContext(ctx, "SELECT @@server_uuid").Scan(&uuid); err != nil {
		return err
	}
	path := filepath.Join(opt.StateDir, "manifest.json")
	var manifest inplaceManifest
	raw, err := os.ReadFile(path)
	if err == nil {
		if err = json.Unmarshal(raw, &manifest); err != nil {
			return err
		}
		if manifest.UUID != uuid {
			return errors.New("state directory belongs to another server")
		}
	} else if os.IsNotExist(err) {
		manifest.UUID = uuid
		r, err := db.QueryContext(ctx, "SELECT TABLE_NAME FROM information_schema.TABLES WHERE TABLE_SCHEMA=DATABASE() AND (TABLE_NAME='kline_day' OR TABLE_NAME REGEXP '^kline_minute_[0-9]{4}$') ORDER BY TABLE_NAME")
		if err != nil {
			return err
		}
		for r.Next() {
			var table string
			if err := r.Scan(&table); err != nil {
				r.Close()
				return err
			}
			manifest.Tables = append(manifest.Tables, table)
		}
		err = r.Err()
		r.Close()
		if err != nil {
			return err
		}
		if len(manifest.Tables) == 0 {
			return errors.New("no legacy table to migrate")
		}
		if err := saveJSON(path, manifest); err != nil {
			return err
		}
	} else {
		return err
	}
	seen := map[string]bool{}
	for _, table := range manifest.Tables {
		if !legacyTable.MatchString(table) || seen[table] {
			return errors.New("invalid table manifest")
		}
		seen[table] = true
	}
	sort.Strings(manifest.Tables)
	if _, err := db.ExecContext(ctx, schemaDDL); err != nil {
		return err
	}
	var ready int
	err = db.QueryRowContext(ctx, "SELECT ready FROM kline_schema WHERE version='market-v1'").Scan(&ready)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if ready == 1 {
		return errors.New("market layout already ready; refusing to replay maintenance")
	}
	if _, err := db.ExecContext(ctx, "INSERT INTO kline_schema(version,ready) VALUES('market-v1',0) ON DUPLICATE KEY UPDATE ready=0"); err != nil {
		return err
	}
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(stop)
	started := time.Now()
	var stockRows, indexRows int64
	for _, table := range manifest.Tables {
		select {
		case <-stop:
			return errors.New("paused after completing current table")
		default:
		}
		if err := guard.PingContext(ctx); err != nil {
			return err
		}
		if err := freeSpace(opt.SpacePath, opt.MinFree); err != nil {
			return err
		}
		if opt.TmpPath != "" {
			var bytes uint64
			stock, _ := targetTable(table, "cn/stock")
			if err := db.QueryRowContext(ctx, "SELECT COALESCE(MAX(FILE_SIZE),0) FROM information_schema.INNODB_TABLESPACES WHERE NAME IN (CONCAT(DATABASE(),'/',?),CONCAT(DATABASE(),'/',?))", table, stock).Scan(&bytes); err != nil {
				return err
			}
			if err := freeSpace(opt.TmpPath, bytes*2+20*1024*1024*1024); err != nil {
				return fmt.Errorf("temporary disk reserve: %w", err)
			}
		}
		if err := processInplaceTable(ctx, db, table, opt.StateDir, opt.TmpPath, enc); err != nil {
			return fmt.Errorf("%s: %w", table, err)
		}
		state, err := readState(filepath.Join(opt.StateDir, table+".json"))
		if err != nil {
			return err
		}
		if !state.Complete {
			return errors.New("table checkpoint is incomplete")
		}
		stockRows += state.StockDigest.Rows
		indexRows += state.IndexDigest.Rows
	}
	var legacy int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM information_schema.TABLES WHERE TABLE_SCHEMA=DATABASE() AND (TABLE_NAME='kline_day' OR TABLE_NAME REGEXP '^kline_minute_[0-9]{4}$')").Scan(&legacy); err != nil {
		return err
	}
	if legacy != 0 {
		return errors.New("unexpected legacy table remains")
	}
	if _, err := db.ExecContext(ctx, "UPDATE kline_schema SET ready=1 WHERE version='market-v1' AND ready=0"); err != nil {
		return err
	}
	return enc.Encode(map[string]any{"summary": true, "complete": true, "method": "in-place", "verifiedTables": len(manifest.Tables), "stockRows": stockRows, "indexRows": indexRows, "seconds": time.Since(started).Seconds(), "recoveryPointRequired": true})
}
