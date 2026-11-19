package marketstore

import (
	"context"
	"fmt"
	"strings"
)

type SectorDayClose struct {
	Date, Code string
	Close      float64
}

// SectorHistory reads five preceding index sessions and the corresponding block closes.
func (r *MySQLReader) SectorHistory(ctx context.Context, before string) ([]string, []SectorDayClose, error) {
	if err := timeRange("", before, "2006-01-02"); err != nil {
		return nil, nil, err
	}
	rows, err := r.db.QueryContext(ctx, "SELECT DATE_FORMAT(`time`,'%Y-%m-%d') FROM cn_index_kline_day WHERE code=? AND `time`<? ORDER BY `time` DESC LIMIT 5", "sh000001", before)
	if err != nil {
		return nil, nil, fmt.Errorf("读取指数交易日: %w", err)
	}
	var dates []string
	for rows.Next() {
		var date string
		if err = rows.Scan(&date); err != nil {
			break
		}
		dates = append(dates, date)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil || len(dates) != 5 {
		if err == nil {
			err = fmt.Errorf("历史交易日不足五天")
		}
		return nil, nil, err
	}
	for i, j := 0, len(dates)-1; i < j; i, j = i+1, j-1 {
		dates[i], dates[j] = dates[j], dates[i]
	}
	args := make([]any, len(dates))
	for i, d := range dates {
		args[i] = d
	}
	query := "SELECT DATE_FORMAT(`time`,'%Y-%m-%d'),code,close FROM cn_block_kline_day WHERE `time` IN (" + strings.TrimSuffix(strings.Repeat("?,", len(dates)), ",") + ")"
	rows, err = r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, nil, fmt.Errorf("读取板块日线: %w", err)
	}
	defer rows.Close()
	var out []SectorDayClose
	for rows.Next() {
		var v SectorDayClose
		if err = rows.Scan(&v.Date, &v.Code, &v.Close); err != nil {
			return nil, nil, err
		}
		out = append(out, v)
	}
	return dates, out, rows.Err()
}
