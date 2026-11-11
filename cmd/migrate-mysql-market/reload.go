package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/go-sql-driver/mysql"
)

type reloadConnections struct {
	Source string `json:"sourceDSN"`
	Target string `json:"targetDSN"`
}
type reloadManifest struct {
	ColdManifestSHA string   `json:"coldManifestSHA256"`
	UUID            string   `json:"serverUUID"`
	FromYear        int      `json:"fromYear"`
	Tables          []string `json:"sourceTables"`
}
type reloadCheckpoint struct {
	Job    job    `json:"job"`
	Digest digest `json:"digest"`
}

func reloadDB(dsn string, readonly bool) (*sql.DB, *mysql.Config, error) {
	c, e := mysql.ParseDSN(dsn)
	if e != nil {
		return nil, nil, errors.New("invalid private connection configuration")
	}
	c.ParseTime = false
	c.InterpolateParams = false
	c.ClientFoundRows = false
	c.Timeout = 5 * time.Second
	c.ReadTimeout = 20 * time.Minute
	if c.Params == nil {
		c.Params = map[string]string{}
	}
	c.Params["charset"] = "utf8mb4"
	if readonly {
		c.Params["transaction_read_only"] = "1"
	}
	db, e := sql.Open("mysql", c.FormatDSN())
	if e != nil {
		return nil, nil, errors.New("cannot open connection")
	}
	db.SetMaxOpenConns(2)
	return db, c, nil
}

func reloadFields(fields []string) string {
	a := make([]string, len(fields))
	for i, f := range fields {
		a[i] = "CAST(`" + f + "` AS CHAR CHARACTER SET utf8mb4)"
	}
	return strings.Join(a, ",")
}

type reloadQuery interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func reloadDigest(ctx context.Context, q reloadQuery, query string, args ...any) (digest, error) {
	r, e := q.QueryContext(ctx, query, args...)
	if e != nil {
		return digest{}, e
	}
	defer r.Close()
	cols, e := r.Columns()
	if e != nil {
		return digest{}, e
	}
	values := make([]sql.RawBytes, len(cols))
	ptrs := make([]any, len(cols))
	for i := range values {
		ptrs[i] = &values[i]
	}
	h := sha256.New()
	var n int64
	for r.Next() {
		if e = r.Scan(ptrs...); e != nil {
			return digest{}, e
		}
		for _, v := range values {
			hashValue(h, v, v != nil)
		}
		n++
	}
	if e = r.Err(); e != nil {
		return digest{}, e
	}
	return digest{n, hex.EncodeToString(h.Sum(nil))}, nil
}

// Bounded batches preserve source text precision; the whole code/year is verified before COMMIT.
func reloadStream(ctx context.Context, src, dst *sql.DB, j job, sourceQuery string, sourceArgs []any, targetQuery string, targetArgs []any, check func([]sql.RawBytes) error) (digest, error) {
	r, e := src.QueryContext(ctx, sourceQuery, sourceArgs...)
	if e != nil {
		return digest{}, e
	}
	defer r.Close()
	tx, e := dst.BeginTx(ctx, nil)
	if e != nil {
		return digest{}, e
	}
	defer tx.Rollback()
	fields := len(j.Columns)
	values := make([]sql.RawBytes, fields)
	ptrs := make([]any, fields)
	for i := range values {
		ptrs[i] = &values[i]
	}
	h := sha256.New()
	var n int64
	batch := make([]any, 0, 1000*fields)
	var fullStmt *sql.Stmt
	defer func() {
		if fullStmt != nil {
			fullStmt.Close()
		}
	}()
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		placeholder := "(" + strings.TrimSuffix(strings.Repeat("?,", fields), ",") + ")"
		marks := strings.TrimSuffix(strings.Repeat(placeholder+",", len(batch)/fields), ",")
		query := "INSERT INTO `" + j.Target + "` (" + quoted(j.Columns) + ") VALUES " + marks + " ON DUPLICATE KEY UPDATE `" + j.Columns[0] + "`=`" + j.Target + "`.`" + j.Columns[0] + "`"
		var err error
		if len(batch) == 1000*fields {
			if fullStmt == nil {
				fullStmt, err = tx.PrepareContext(ctx, query)
			}
			if err == nil {
				_, err = fullStmt.ExecContext(ctx, batch...)
			}
		} else {
			_, err = tx.ExecContext(ctx, query, batch...)
		}
		batch = batch[:0]
		return err
	}
	for r.Next() {
		if e = r.Scan(ptrs...); e != nil {
			return digest{}, e
		}
		if check != nil {
			if e = check(values); e != nil {
				return digest{}, e
			}
		}
		for _, v := range values {
			hashValue(h, v, v != nil)
			if v == nil {
				batch = append(batch, nil)
			} else {
				batch = append(batch, string(v))
			}
		}
		n++
		if len(batch) == 1000*fields {
			if e = flush(); e != nil {
				return digest{}, e
			}
		}
	}
	if e = r.Err(); e != nil {
		return digest{}, e
	}
	if e = flush(); e != nil {
		return digest{}, e
	}
	before := digest{n, hex.EncodeToString(h.Sum(nil))}
	after, e := reloadDigest(ctx, tx, targetQuery, targetArgs...)
	if e != nil {
		return digest{}, e
	}
	if before != after {
		return digest{}, fmt.Errorf("full-field verification failed for %s/%s/%s: source=%d target=%d", j.Source, j.Market, j.Code, before.Rows, after.Rows)
	}
	if e = tx.Commit(); e != nil {
		return digest{}, e
	}
	return before, nil
}

func reloadJob(ctx context.Context, src, dst *sql.DB, j job, year int) (digest, error) {
	fields := reloadFields(j.Columns)
	sq := "SELECT " + fields + " FROM `" + j.Source + "` WHERE market=? AND code=? AND time>=? ORDER BY time"
	tq := "SELECT " + fields + " FROM `" + j.Target + "` WHERE code=? ORDER BY time"
	return reloadStream(ctx, src, dst, j, sq, []any{j.Market, j.Code, fmt.Sprintf("%04d-01-01", year)}, tq, []any{j.Code}, func(v []sql.RawBytes) error {
		if string(v[0]) != j.Code {
			return errors.New("noncanonical source code")
		}
		y, e := strconv.Atoi(string(v[1][:min(4, len(v[1]))]))
		if e != nil || y < year {
			return errors.New("invalid source date")
		}
		if j.Source != "kline_day" && string(v[1][:4]) != j.Source[len(j.Source)-4:] {
			return errors.New("source year does not match table")
		}
		return nil
	})
}

func reloadCreate(ctx context.Context, src, dst *sql.DB, source, target string) error {
	exists, e := tableExists(ctx, dst, target)
	if e != nil || exists {
		return e
	}
	var name, ddl string
	if e = src.QueryRowContext(ctx, "SHOW CREATE TABLE `"+source+"`").Scan(&name, &ddl); e != nil {
		return e
	}
	header := "CREATE TABLE `" + source + "`"
	if name != source || !strings.HasPrefix(ddl, header) {
		return errors.New("unexpected source DDL")
	}
	ddl = "CREATE TABLE `" + target + "`" + strings.TrimPrefix(ddl, header)
	_, e = dst.ExecContext(ctx, ddl)
	return e
}

func reloadTables(ctx context.Context, src *sql.DB, year int) ([]string, error) {
	r, e := src.QueryContext(ctx, "SELECT TABLE_NAME FROM information_schema.TABLES WHERE TABLE_SCHEMA=DATABASE() AND (TABLE_NAME='kline_day' OR TABLE_NAME REGEXP '^kline_minute_[0-9]{4}$') ORDER BY TABLE_NAME")
	if e != nil {
		return nil, e
	}
	defer r.Close()
	var out []string
	for r.Next() {
		var t string
		if e = r.Scan(&t); e != nil {
			return nil, e
		}
		if t == "kline_day" {
			out = append(out, t)
			continue
		}
		y, _ := strconv.Atoi(t[len(t)-4:])
		if y >= year {
			out = append(out, t)
		}
	}
	return out, r.Err()
}

func reloadJobs(ctx context.Context, src *sql.DB, table string) ([]job, error) {
	r, e := src.QueryContext(ctx, "SELECT DISTINCT market,code FROM `"+table+"` ORDER BY market,code")
	if e != nil {
		return nil, e
	}
	defer r.Close()
	var jobs []job
	for r.Next() {
		j := job{Source: table, Columns: columns(table)}
		if e = r.Scan(&j.Market, &j.Code); e != nil {
			return nil, e
		}
		if j.Market != "cn/stock" && j.Market != "cn/index" {
			return nil, errors.New("unexpected source market")
		}
		j.Target, e = targetTable(table, j.Market)
		if e != nil {
			return nil, e
		}
		jobs = append(jobs, j)
	}
	return jobs, r.Err()
}

func reloadAux(ctx context.Context, src, dst *sql.DB, year int) error {
	for _, a := range []struct {
		name  string
		cols  []string
		order string
	}{
		{"minute_coverage", []string{"market", "code", "year", "from_time", "through_time", "trade_fallback"}, "market,code,year"},
		{"minute_empty_scan", []string{"market", "code", "year", "from_time", "checked_at"}, "market,code,year"},
		{"update_marks", []string{"mark_key", "time"}, "mark_key"},
	} {
		if e := reloadCreate(ctx, src, dst, a.name, a.name); e != nil {
			return e
		}
		query := "SELECT " + reloadFields(a.cols) + " FROM `" + a.name + "`"
		var args []any
		if a.name != "update_marks" {
			query += " WHERE year>=?"
			args = []any{year}
		}
		query += " ORDER BY " + a.order
		target := "SELECT " + reloadFields(a.cols) + " FROM `" + a.name + "` ORDER BY " + a.order
		if _, e := reloadStream(ctx, src, dst, job{Source: a.name, Target: a.name, Columns: a.cols}, query, args, target, nil, nil); e != nil {
			return e
		}
	}
	return nil
}

func runReload(ctx context.Context, c reloadConnections, opt options, year int, expected string, enc *json.Encoder) error {
	if !opt.Apply || !opt.Paused || opt.StateDir == "" || opt.SpacePath == "" || opt.BackupProof == "" || year < 2000 || year > time.Now().Year() {
		return errors.New("reload requires apply, paused writers, state, data volume, cold proof and a valid starting year")
	}
	src, sc, e := reloadDB(c.Source, true)
	if e != nil {
		return e
	}
	defer src.Close()
	dst, dc, e := reloadDB(c.Target, false)
	if e != nil {
		return e
	}
	defer dst.Close()
	if sc.Addr != "127.0.0.1:13318" || dc.Addr != "127.0.0.1:3306" || sc.DBName != "market_data" || dc.DBName != "market_data" {
		return errors.New("reload requires the reviewed loopback source and target endpoints")
	}
	var su, du string
	var ro, sro int
	if e = src.QueryRowContext(ctx, "SELECT @@server_uuid,@@read_only,@@super_read_only").Scan(&su, &ro, &sro); e != nil {
		return e
	}
	if e = dst.QueryRowContext(ctx, "SELECT @@server_uuid").Scan(&du); e != nil {
		return e
	}
	if expected == "" || su != expected || du != expected || ro != 1 || sro != 1 {
		return errors.New("source/target identity or read-only fence mismatch")
	}
	if e = verifyBackup(opt.BackupProof, expected); e != nil {
		return e
	}
	rawProof, e := os.ReadFile(filepath.Join(filepath.Dir(opt.BackupProof), "backup-manifest.json"))
	if e != nil {
		return e
	}
	h := sha256.Sum256(rawProof)
	return reloadInto(ctx, src, dst, opt, year, expected, hex.EncodeToString(h[:]), enc)
}

func reloadInto(ctx context.Context, src, dst *sql.DB, opt options, year int, expected, coldHash string, enc *json.Encoder) error {
	guard, e := dst.Conn(ctx)
	if e != nil {
		return e
	}
	defer guard.Close()
	var acquired int
	if e = guard.QueryRowContext(ctx, "SELECT GET_LOCK(CONCAT(DATABASE(),':market-split-v1'),0)").Scan(&acquired); e != nil {
		return e
	}
	if acquired != 1 {
		return errors.New("another migration is running")
	}
	defer guard.ExecContext(context.Background(), "SELECT RELEASE_LOCK(CONCAT(DATABASE(),':market-split-v1'))")
	if e = os.MkdirAll(opt.StateDir, 0700); e != nil {
		return e
	}
	tables, e := reloadTables(ctx, src, year)
	if e != nil {
		return e
	}
	if len(tables) < 2 || tables[0] != "kline_day" {
		return errors.New("incomplete cold source table list")
	}
	manifest := reloadManifest{UUID: expected, FromYear: year, Tables: tables, ColdManifestSHA: coldHash}
	path := filepath.Join(opt.StateDir, "manifest.json")
	raw, e := os.ReadFile(path)
	if os.IsNotExist(e) {
		var count int
		if e = dst.QueryRowContext(ctx, "SELECT COUNT(*) FROM information_schema.TABLES WHERE TABLE_SCHEMA=DATABASE()").Scan(&count); e != nil {
			return e
		}
		if count != 0 {
			return errors.New("initial reload target must be empty")
		}
		if e = saveJSON(path, manifest); e != nil {
			return e
		}
	} else if e != nil {
		return e
	} else {
		var old reloadManifest
		if e = json.Unmarshal(raw, &old); e != nil {
			return e
		}
		if old.UUID != manifest.UUID || old.ColdManifestSHA != coldHash || old.FromYear != year || strings.Join(old.Tables, ",") != strings.Join(tables, ",") {
			return errors.New("reload manifest changed")
		}
	}
	if _, e = dst.ExecContext(ctx, schemaDDL); e != nil {
		return e
	}
	var ready int
	e = dst.QueryRowContext(ctx, "SELECT ready FROM kline_schema WHERE version='market-v1'").Scan(&ready)
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return e
	}
	if ready == 1 {
		return errors.New("reload already complete; refusing replay")
	}
	if _, e = dst.ExecContext(ctx, "INSERT INTO kline_schema(version,ready) VALUES('market-v1',0) ON DUPLICATE KEY UPDATE ready=0"); e != nil {
		return e
	}
	checkpoints := filepath.Join(opt.StateDir, "jobs")
	if e = os.MkdirAll(checkpoints, 0700); e != nil {
		return e
	}
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(stop)
	started := time.Now()
	var total int64
	done := 0
	var nonempty []job
	for _, table := range tables {
		if e = enc.Encode(map[string]any{"stage": "enumerating", "source": table, "atUTC": time.Now().UTC()}); e != nil {
			return e
		}
		jobs, e := reloadJobs(ctx, src, table)
		if e != nil {
			return e
		}
		prepared := map[string]bool{}
		for _, j := range jobs {
			select {
			case <-stop:
				return errors.New("paused after completing verified code/year")
			default:
			}
			if e = guard.PingContext(ctx); e != nil {
				return e
			}
			if e = freeSpace(opt.SpacePath, opt.MinFree); e != nil {
				return e
			}
			if !prepared[j.Target] {
				if e = reloadCreate(ctx, src, dst, j.Source, j.Target); e != nil {
					return e
				}
				if e = ensureTarget(ctx, dst, j); e != nil {
					return e
				}
				prepared[j.Target] = true
			}
			id := sha256.Sum256([]byte(j.Source + "/" + j.Market + "/" + j.Code))
			cp := filepath.Join(checkpoints, hex.EncodeToString(id[:])+".json")
			var d digest
			raw, e = os.ReadFile(cp)
			if e == nil {
				var saved reloadCheckpoint
				if e = json.Unmarshal(raw, &saved); e != nil {
					return e
				}
				if saved.Job.Source != j.Source || saved.Job.Market != j.Market || saved.Job.Code != j.Code || saved.Job.Target != j.Target {
					return errors.New("checkpoint identity mismatch")
				}
				d, e = reloadDigest(ctx, dst, "SELECT "+reloadFields(j.Columns)+" FROM `"+j.Target+"` WHERE code=? ORDER BY time", j.Code)
				if e != nil {
					return e
				}
				if d != saved.Digest {
					return errors.New("verified target checkpoint changed")
				}
			} else if os.IsNotExist(e) {
				d, e = reloadJob(ctx, src, dst, j, year)
				if e != nil {
					return e
				}
				if e = saveJSON(cp, reloadCheckpoint{j, d}); e != nil {
					return e
				}
			} else {
				return e
			}
			total += d.Rows
			done++
			if d.Rows > 0 {
				nonempty = append(nonempty, j)
			}
			if e = enc.Encode(map[string]any{"stage": "verified-job", "source": j.Source, "target": j.Target, "market": j.Market, "code": j.Code, "rows": d.Rows, "sha256": d.SHA256, "done": done, "totalRows": total, "seconds": time.Since(started).Seconds(), "atUTC": time.Now().UTC()}); e != nil {
				return e
			}
		}
		if e = enc.Encode(map[string]any{"stage": "table-complete", "source": table, "verifiedJobs": len(jobs), "atUTC": time.Now().UTC()}); e != nil {
			return e
		}
	}
	if e = verifyCodes(ctx, dst, nonempty); e != nil {
		return e
	}
	if e = reloadAux(ctx, src, dst, year); e != nil {
		return e
	}
	if _, e = dst.ExecContext(ctx, "UPDATE kline_schema SET ready=1 WHERE version='market-v1' AND ready=0"); e != nil {
		return e
	}
	sort.Strings(tables)
	return enc.Encode(map[string]any{"summary": true, "complete": true, "method": "reload-from-cold-source", "fromYear": year, "sourceTables": tables, "verifiedJobs": done, "rows": total, "seconds": time.Since(started).Seconds(), "coldRecoveryPointUnchanged": true})
}
