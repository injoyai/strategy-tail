package marketstore

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/injoyai/tdx/v2/extend/model"
	"github.com/injoyai/tdx/v2/protocol"
)

// StockSessionBars aggregates the minutes already present for today's stock session.
// The returned time is the last stored minute, and a stock with no minutes is absent.
func (r *MySQLReader) StockSessionBars(codes []string, at time.Time) (map[string]*model.KlineMinute, error) {
	out := make(map[string]*model.KlineMinute, len(codes))
	if len(codes) == 0 {
		return out, nil
	}
	at = at.In(protocol.Location())
	const prefix = "cn_stock"
	years, err := r.minuteYears(prefix)
	if err != nil || !slices.Contains(years, at.Year()) {
		return out, err
	}
	table := fmt.Sprintf("`%s_kline_minute_%04d`", prefix, at.Year())
	from := at.Format(time.DateOnly) + " 00:00"
	through := at.Format(protocol.TimeLayout)
	for batch := range slices.Chunk(codes, 250) {
		query := "SELECT a.code, DATE_FORMAT(a.last_at,'%Y-%m-%d %H:%i'), f.open, a.high, a.low, l.close, a.volume, a.amount " +
			"FROM (SELECT code, MIN(`time`) first_at, MAX(`time`) last_at, MAX(high) high, MIN(low) low, SUM(volume) volume, " +
			"CASE WHEN COUNT(amount)=COUNT(*) THEN SUM(amount) END amount FROM " + table +
			" WHERE code IN (" + strings.TrimSuffix(strings.Repeat("?,", len(batch)), ",") +
			") AND `time` BETWEEN ? AND ? GROUP BY code) a JOIN " + table +
			" f ON f.code=a.code AND f.`time`=a.first_at JOIN " + table + " l ON l.code=a.code AND l.`time`=a.last_at"
		args := make([]any, 0, len(batch)+2)
		for _, code := range batch {
			args = append(args, code)
		}
		args = append(args, from, through)
		if err := r.readSessionBars(query, args, out); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (r *MySQLReader) readSessionBars(query string, args []any, out map[string]*model.KlineMinute) error {
	rows, err := r.db.Query(query, args...)
	if err != nil {
		return fmt.Errorf("查询已入库的盘中行情: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var code string
		bar := new(model.KlineMinute)
		if err := rows.Scan(&code, &bar.Time, &bar.Open, &bar.High, &bar.Low, &bar.Close, &bar.Volume, &bar.Amount); err != nil {
			return err
		}
		out[code] = bar
	}
	return rows.Err()
}
