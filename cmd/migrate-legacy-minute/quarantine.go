package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"time"
)

type sourcePolicy struct {
	InvalidNumericRows bool         `json:"invalidNumericRows"`
	BadDays            []badDayRule `json:"badDays"`
}
type badDayRule struct {
	Code   string `json:"code"`
	Date   string `json:"date"`
	SHA256 string `json:"sourceSHA256"`
	Reason string `json:"reason"`
}
type isolatedRow struct {
	RowID  int64              `json:"rowid"`
	Reason string             `json:"reason"`
	Values map[string]*string `json:"rawValues"`
}

func loadSourcePolicy(file string) (sourcePolicy, error) {
	var p sourcePolicy
	if file == "" {
		return p, nil
	}
	bs, err := os.ReadFile(file)
	if err != nil {
		return p, err
	}
	if err = json.Unmarshal(bs, &p); err != nil {
		return p, err
	}
	for _, r := range p.BadDays {
		if _, err = time.Parse(time.DateOnly, r.Date); err != nil {
			return p, err
		}
		if r.Code == "" || len(r.SHA256) != 64 || r.Reason == "" {
			return p, fmt.Errorf("隔离日期规则缺少代码、源哈希或原因")
		}
	}
	return p, nil
}

// The caller passes only a disposable local snapshot, never a production source path.
func isolateSnapshot(snapshot string, j job, hash string, p sourcePolicy) ([]isolatedRow, error) {
	if !p.InvalidNumericRows && len(p.BadDays) == 0 {
		return nil, nil
	}
	db, err := sql.Open("sqlite", snapshot)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	var version int
	if err = db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return nil, err
	}
	if version != 0 {
		return nil, nil
	}
	predicate := "0"
	args := []any{}
	reasons := map[string]string{}
	if p.InvalidNumericRows {
		predicate = fmt.Sprintf("Time IS NULL OR strftime('%%Y',Time,'+8 hours') IS NULL OR strftime('%%Y',Time,'+8 hours') != '%04d'", j.year)
		for _, c := range []string{"Last", "Open", "High", "Low", "Close", "Order", "Volume", "Amount", "UpCount", "DownCount"} {
			predicate += " OR `" + c + "` IS NULL OR typeof(`" + c + "`) NOT IN ('integer','real') OR `" + c + "`>1.7976931348623157e308 OR `" + c + "`< -1.7976931348623157e308"
		}
		for _, c := range []string{"Open", "High", "Low", "Close", "Volume", "Amount"} {
			predicate += " OR `" + c + "`<0"
		}
	}
	for _, r := range p.BadDays {
		if r.Code != j.code.Code || r.Date[:4] != strconv.Itoa(j.year) {
			continue
		}
		if r.SHA256 != hash {
			return nil, fmt.Errorf("隔离日期规则源哈希不匹配：%s %s", r.Code, r.Date)
		}
		predicate += " OR strftime('%Y-%m-%d',Time,'+8 hours')=?"
		args = append(args, r.Date)
		reasons[r.Date] = r.Reason
	}
	if predicate == "0" {
		return nil, nil
	}
	columns := []string{"Last", "Open", "High", "Low", "Close", "Order", "Volume", "Amount", "Time", "UpCount", "DownCount"}
	query := "SELECT rowid,strftime('%Y-%m-%d',Time,'+8 hours')"
	for _, c := range columns {
		query += ",CAST(`" + c + "` AS TEXT)"
	}
	query += " FROM Kline WHERE (" + predicate + ") ORDER BY rowid"
	rs, err := db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	var out []isolatedRow
	days := map[string]bool{}
	for rs.Next() {
		r := isolatedRow{Reason: "字段缺失、非法数值、负价格/成交量/成交额或目录年份不符", Values: map[string]*string{}}
		var day sql.NullString
		values := make([]sql.NullString, len(columns))
		dest := []any{&r.RowID, &day}
		for i := range values {
			dest = append(dest, &values[i])
		}
		if err = rs.Scan(dest...); err != nil {
			rs.Close()
			return nil, err
		}
		if day.Valid && reasons[day.String] == "" {
			days[day.String] = true
		}
		if why := reasons[day.String]; why != "" {
			r.Reason = why
		}
		for i, c := range columns {
			if values[i].Valid {
				v := values[i].String
				r.Values[c] = &v
			} else {
				r.Values[c] = nil
			}
		}
		out = append(out, r)
	}
	err = rs.Err()
	rs.Close()
	if err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, nil
	}
	for day := range days {
		if err := checkLinkedBadDay(db, day); err != nil {
			return out, err
		}
	}
	var count int
	if err = db.QueryRow("SELECT COUNT(*) FROM Kline").Scan(&count); err != nil {
		return nil, err
	}
	if count > 0 && float64(len(out))/float64(count) > 0.2 {
		return out, fmt.Errorf("源库异常行超过20%%，需要单独复核：%d/%d", len(out), count)
	}
	if _, err = db.Exec("DELETE FROM Kline WHERE ("+predicate+")", args...); err != nil {
		return nil, err
	}
	return out, nil
}

// This detects the observed corruption family only to stop for review, never to delete on a heuristic.
func checkLinkedBadDay(db *sql.DB, day string) error {
	const dateExpr = "strftime('%Y-%m-%d',Time,'+8 hours')"
	var count, distinct, flat int
	var price float64
	err := db.QueryRow("SELECT COUNT(*),COUNT(DISTINCT Open),SUM(CASE WHEN Volume=0 AND Amount=0 AND Open=High AND Open=Low AND Open=Close THEN 1 ELSE 0 END),MIN(Open) FROM Kline WHERE "+dateExpr+"=?", day).Scan(&count, &distinct, &flat, &price)
	if err != nil {
		return err
	}
	if count < 241 || distinct != 1 || flat < count-2 || price <= 0 {
		return nil
	}
	var before, after float64
	if err = db.QueryRow("SELECT Close FROM Kline WHERE "+dateExpr+"<? ORDER BY Time DESC,rowid DESC LIMIT 1", day).Scan(&before); err == sql.ErrNoRows {
		return nil
	} else if err != nil {
		return err
	}
	if err = db.QueryRow("SELECT Open FROM Kline WHERE "+dateExpr+">? ORDER BY Time,rowid LIMIT 1", day).Scan(&after); err == sql.ErrNoRows {
		return nil
	} else if err != nil {
		return err
	}
	if before > 0 && after > 0 && price/before >= 1000 && price/after >= 1000 {
		return fmt.Errorf("发现未核实关联坏日 %s：平价零成交%d/%d，价位较相邻日期跳变逾千倍；未导入该库", day, flat, count)
	}
	return nil
}
