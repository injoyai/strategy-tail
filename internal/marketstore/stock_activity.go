package marketstore

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// StockTurnover is a stock session with positive volume and turnover in yuan.
// Unavailable amounts are represented by zero for exclusion from the sample.
type StockTurnover struct {
	Date   string
	Code   string
	Amount float64
}

// ReadStockTurnover streams the selected range in trading-date order. The
// caller can compute each cross-section without retaining the full history.
func (r *MySQLReader) ReadStockTurnover(ctx context.Context, from, to string, visit func(StockTurnover)) error {
	if from == "" || to == "" {
		return errors.New("成交数据查询需要起止日期")
	}
	if err := timeRange(from, to, time.DateOnly); err != nil {
		return err
	}
	const query = "SELECT `time`,code,COALESCE(amount,0) FROM cn_stock_kline_day WHERE `time` BETWEEN ? AND ? AND volume>0 ORDER BY `time`"
	rows, err := r.db.QueryContext(ctx, query, from, to)
	if err != nil {
		return fmt.Errorf("读取股票日线成交数据: %w", err)
	}
	defer rows.Close()
	var row StockTurnover
	for rows.Next() {
		if err := rows.Scan(&row.Date, &row.Code, &row.Amount); err != nil {
			return err
		}
		visit(row)
	}
	return rows.Err()
}
