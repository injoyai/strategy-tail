// Package marketstore reads the MySQL schema maintained by market-data.
package marketstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/injoyai/tdx/v2/extend/model"
	"github.com/injoyai/tdx/v2/protocol"
)

type MySQLReader struct {
	db       *sql.DB
	identity string

	mu      sync.Mutex
	years   map[string][]int
	yearsAt time.Time
}

func OpenMySQLReader(dsn string) (*MySQLReader, error) {
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		return nil, errors.New("MySQL 行情连接配置无效")
	}
	if strings.TrimSpace(cfg.DBName) == "" {
		return nil, errors.New("MySQL 行情配置缺少数据库名")
	}
	cfg.ParseTime = false
	cfg.InterpolateParams = false
	if cfg.Timeout == 0 {
		cfg.Timeout = 5 * time.Second
	}
	if cfg.ReadTimeout == 0 {
		cfg.ReadTimeout = 30 * time.Second
	}
	if cfg.Params == nil {
		cfg.Params = make(map[string]string)
	}
	cfg.Params["charset"] = "utf8mb4"
	db, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		return nil, fmt.Errorf("打开 MySQL 行情库: %w", err)
	}
	db.SetMaxOpenConns(16)
	db.SetMaxIdleConns(16)
	db.SetConnMaxLifetime(time.Hour)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("连接 MySQL 行情库: %w", err)
	}
	return &MySQLReader{db: db, identity: "mysql://" + cfg.Addr + "/" + cfg.DBName}, nil
}

func (r *MySQLReader) Close() error     { return r.db.Close() }
func (r *MySQLReader) Identity() string { return r.identity }

var marketName = regexp.MustCompile(`^[a-z][a-z0-9]*/[a-z][a-z0-9]*$`)

func marketPrefix(market protocol.Market) (string, error) {
	name := market.String()
	if len(name) > 16 || !marketName.MatchString(name) {
		return "", fmt.Errorf("无效的 MySQL 行情市场 %q", name)
	}
	return strings.ReplaceAll(name, "/", "_"), nil
}

func dayTable(market protocol.Market) (string, error) {
	prefix, err := marketPrefix(market)
	if err != nil {
		return "", err
	}
	return prefix + "_kline_day", nil
}

func timeRange(start, end, layout string) error {
	for _, value := range []string{start, end} {
		if value == "" {
			continue
		}
		parsed, err := time.Parse(layout, value)
		if err != nil || parsed.Format(layout) != value {
			return fmt.Errorf("无效的源时间 %q，格式须为 %s", value, layout)
		}
	}
	if start != "" && end != "" && start > end {
		return errors.New("行情起始时间晚于结束时间")
	}
	return nil
}

func boundedQuery(query string, code model.Code, start, end string) (string, []any) {
	args := []any{code.Code}
	if start != "" {
		query += " AND `time`>=?"
		args = append(args, start)
	}
	if end != "" {
		query += " AND `time`<=?"
		args = append(args, end)
	}
	return query + " ORDER BY `time`", args
}

func (r *MySQLReader) QueryDay(code model.Code, start, end string) ([]*model.KlineDay, error) {
	if err := timeRange(start, end, time.DateOnly); err != nil {
		return nil, err
	}
	table, err := dayTable(code.Market)
	if err != nil {
		return nil, err
	}
	query, args := boundedQuery("SELECT DATE_FORMAT(`time`,'%Y-%m-%d'),`open`,`high`,`low`,`close`,`volume`,`amount` FROM `"+table+"` WHERE `code`=?", code, start, end)
	rows, err := r.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("查询日线: %w", err)
	}
	defer rows.Close()
	var out []*model.KlineDay
	for rows.Next() {
		k := new(model.KlineDay)
		if err := rows.Scan(&k.Time, &k.Open, &k.High, &k.Low, &k.Close, &k.Volume, &k.Amount); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

func (r *MySQLReader) DayCodes(market protocol.Market) ([]model.Code, error) {
	table, err := dayTable(market)
	if err != nil {
		return nil, err
	}
	rows, err := r.db.Query("SELECT DISTINCT `code` FROM `" + table + "` ORDER BY `code`")
	if err != nil {
		return nil, fmt.Errorf("枚举日线证券: %w", err)
	}
	defer rows.Close()
	var out []model.Code
	for rows.Next() {
		code := model.Code{Market: market}
		if err := rows.Scan(&code.Code); err != nil {
			return nil, err
		}
		out = append(out, code)
	}
	return out, rows.Err()
}

func (r *MySQLReader) LastDayTime(code model.Code) (string, error) {
	table, err := dayTable(code.Market)
	if err != nil {
		return "", err
	}
	var last sql.NullString
	err = r.db.QueryRow("SELECT DATE_FORMAT(MAX(`time`),'%Y-%m-%d') FROM `"+table+"` WHERE `code`=?", code.Code).Scan(&last)
	if err != nil {
		return "", fmt.Errorf("读取最新日线日期: %w", err)
	}
	return last.String, nil
}

func (r *MySQLReader) minuteYears(prefix string) ([]int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if time.Since(r.yearsAt) >= time.Minute {
		rows, err := r.db.Query("SELECT TABLE_NAME FROM information_schema.TABLES WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME REGEXP '^[a-z][a-z0-9]*_[a-z][a-z0-9]*_kline_minute_[0-9]{4}$'")
		if err != nil {
			return nil, fmt.Errorf("查询分钟分表: %w", err)
		}
		defer rows.Close()
		years := make(map[string][]int)
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				return nil, err
			}
			market, suffix, ok := strings.Cut(name, "_kline_minute_")
			year, err := strconv.Atoi(suffix)
			if ok && err == nil && year > 0 {
				years[market] = append(years[market], year)
			}
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
		for _, values := range years {
			slices.Sort(values)
		}
		r.years, r.yearsAt = years, time.Now()
	}
	return r.years[prefix], nil
}

func (r *MySQLReader) QueryMinute(code model.Code, start, end string) ([]*model.KlineMinute, error) {
	if err := timeRange(start, end, protocol.TimeLayout); err != nil {
		return nil, err
	}
	prefix, err := marketPrefix(code.Market)
	if err != nil {
		return nil, err
	}
	years, err := r.minuteYears(prefix)
	if err != nil {
		return nil, err
	}
	var out []*model.KlineMinute
	for _, year := range years {
		label := fmt.Sprintf("%04d", year)
		if (start != "" && label < start[:4]) || (end != "" && label > end[:4]) {
			continue
		}
		table := prefix + "_kline_minute_" + label
		query, args := boundedQuery("SELECT DATE_FORMAT(`time`,'%Y-%m-%d %H:%i'),`open`,`high`,`low`,`close`,`volume`,`amount`,`source` FROM `"+table+"` WHERE `code`=?", code, start, end)
		bars, err := r.queryMinutes(query, args)
		if err != nil {
			return nil, err
		}
		out = append(out, bars...)
	}
	return out, nil
}

func (r *MySQLReader) queryMinutes(query string, args []any) ([]*model.KlineMinute, error) {
	rows, err := r.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("查询分钟线: %w", err)
	}
	defer rows.Close()
	var out []*model.KlineMinute
	for rows.Next() {
		k := new(model.KlineMinute)
		if err := rows.Scan(&k.Time, &k.Open, &k.High, &k.Low, &k.Close, &k.Volume, &k.Amount, &k.Source); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

func missingTable(err error) bool {
	var mysqlErr *mysql.MySQLError
	return errors.As(err, &mysqlErr) && mysqlErr.Number == 1146
}
