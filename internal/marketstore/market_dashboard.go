package marketstore

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

type DashboardIndexDay struct {
	Date  string
	Close float64
}

type DashboardStockDay struct {
	Code     string
	Close    float64
	Previous float64
	Amount   float64
}

type DashboardDays struct {
	Indexes map[string][]DashboardIndexDay
	Stocks  []DashboardStockDay
}

// ReadDashboardDays reads each index's last two bars through the requested date.
// Stocks are read on the calendar index's latest session, with closes from its
// preceding session for comparison. All reads share one database snapshot.
func (r *MySQLReader) ReadDashboardDays(ctx context.Context, calendar string, indexes []string, through string) (DashboardDays, error) {
	if err := timeRange("", through, time.DateOnly); err != nil {
		return DashboardDays{}, err
	}
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return DashboardDays{}, err
	}
	defer tx.Rollback()

	days := DashboardDays{Indexes: make(map[string][]DashboardIndexDay, len(indexes))}
	for _, code := range indexes {
		bars, err := readDashboardIndexDays(ctx, tx, code, through)
		if err != nil {
			return DashboardDays{}, fmt.Errorf("读取指数 %s 日线: %w", code, err)
		}
		days.Indexes[code] = bars
	}
	if dates := days.Indexes[calendar]; len(dates) != 0 {
		previous := ""
		if len(dates) > 1 {
			previous = dates[1].Date
		}
		days.Stocks, err = readDashboardStockDays(ctx, tx, dates[0].Date, previous)
		if err != nil {
			return DashboardDays{}, fmt.Errorf("读取 A 股 %s 日线截面: %w", dates[0].Date, err)
		}
	}
	return days, tx.Commit()
}

func readDashboardIndexDays(ctx context.Context, tx *sql.Tx, code, through string) ([]DashboardIndexDay, error) {
	const query = "SELECT `time`,close FROM cn_index_kline_day WHERE code=? AND `time`<=? ORDER BY `time` DESC LIMIT 2"
	rows, err := tx.QueryContext(ctx, query, code, through)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	bars := make([]DashboardIndexDay, 0, 2)
	for rows.Next() {
		var bar DashboardIndexDay
		if err := rows.Scan(&bar.Date, &bar.Close); err != nil {
			return nil, err
		}
		bars = append(bars, bar)
	}
	return bars, rows.Err()
}

func readDashboardStockDays(ctx context.Context, tx *sql.Tx, date, previous string) ([]DashboardStockDay, error) {
	// Enumerating distinct codes uses the leading primary-key column. Each
	// date lookup then uses (code, time), without a full history scan.
	const query = "SELECT d.code,d.close,COALESCE(p.close,0),COALESCE(d.amount,0) " +
		"FROM (SELECT DISTINCT code FROM cn_stock_kline_day) c " +
		"STRAIGHT_JOIN cn_stock_kline_day d ON d.code=c.code AND d.`time`=? " +
		"LEFT JOIN cn_stock_kline_day p ON p.code=d.code AND p.`time`=? WHERE d.volume>0"
	// Use NULL if the calendar index has no preceding bar.
	priorDate := sql.NullString{String: previous, Valid: previous != ""}
	rows, err := tx.QueryContext(ctx, query, date, priorDate)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var stocks []DashboardStockDay
	for rows.Next() {
		var bar DashboardStockDay
		if err := rows.Scan(&bar.Code, &bar.Close, &bar.Previous, &bar.Amount); err != nil {
			return nil, err
		}
		stocks = append(stocks, bar)
	}
	return stocks, rows.Err()
}
