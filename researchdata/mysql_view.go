package researchdata

import (
	"database/sql"
	"encoding/json"
	"time"

	"github.com/injoyai/logs"
)

// MySQLView queries revisions written by the external research data project.
type MySQLView struct{ db *sql.DB }

func NewMySQLView(db *sql.DB) *MySQLView { return &MySQLView{db: db} }

func (s *MySQLView) Latest(dataset, code string, asOf time.Time) (Record, bool) {
	if s == nil || s.db == nil || asOf.IsZero() {
		return Record{}, false
	}
	row := s.db.QueryRow(`
SELECT dataset, code, record_key, source, revision, event_at, available_at, values_json, attributes_json
FROM research_records
WHERE dataset = ? AND code = ? AND event_at <= ? AND available_at <= ?
ORDER BY event_at DESC, available_at DESC, record_key DESC
LIMIT 1`, dataset, code, asOf.UnixNano(), asOf.UnixNano())
	record, err := scanRecord(row)
	if err != nil && err != sql.ErrNoRows {
		logs.Warnf("researchdata: Latest 查询失败: dataset=%s code=%s err=%v", dataset, code, err)
	}
	return record, err == nil
}

func (s *MySQLView) Range(dataset, code string, start, end, asOf time.Time) []Record {
	if s == nil || s.db == nil || asOf.IsZero() {
		return nil
	}
	query := `
SELECT dataset, code, record_key, source, revision, event_at, available_at, values_json, attributes_json
FROM research_records
WHERE dataset = ? AND code = ? AND event_at <= ? AND available_at <= ?`
	args := []any{dataset, code, asOf.UnixNano(), asOf.UnixNano()}
	if !start.IsZero() {
		query += ` AND event_at >= ?`
		args = append(args, start.UnixNano())
	}
	if !end.IsZero() {
		query += ` AND event_at <= ?`
		args = append(args, end.UnixNano())
	}
	query += ` ORDER BY event_at ASC, available_at ASC, record_key ASC`
	rows, err := s.db.Query(query, args...)
	if err != nil {
		logs.Warnf("researchdata: Range 查询失败, 返回空结果: dataset=%s code=%s start=%s end=%s err=%v",
			dataset, code, start, end, err)
		return nil
	}
	defer rows.Close()
	latest := make(map[string]Record)
	for rows.Next() {
		record, err := scanRecord(rows)
		if err != nil {
			logs.Warnf("researchdata: Range 扫描记录失败, 返回空结果: dataset=%s code=%s err=%v", dataset, code, err)
			return nil
		}
		old, ok := latest[record.Key]
		if !ok || record.AvailableAt.After(old.AvailableAt) {
			latest[record.Key] = record
		}
	}
	if err := rows.Err(); err != nil {
		logs.Warnf("researchdata: Range 遍历记录失败, 返回空结果: dataset=%s code=%s err=%v", dataset, code, err)
		return nil
	}
	out := make([]Record, 0, len(latest))
	for _, record := range latest {
		out = append(out, record)
	}
	sortRecords(out)
	return out
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanRecord(row rowScanner) (Record, error) {
	var record Record
	var eventAt, availableAt int64
	var values, attributes []byte
	err := row.Scan(&record.Dataset, &record.Code, &record.Key, &record.Source, &record.Revision,
		&eventAt, &availableAt, &values, &attributes)
	if err != nil {
		return Record{}, err
	}
	record.EventAt = time.Unix(0, eventAt)
	record.AvailableAt = time.Unix(0, availableAt)
	if len(values) > 0 {
		if err := json.Unmarshal(values, &record.Values); err != nil {
			return Record{}, err
		}
	}
	if len(attributes) > 0 {
		if err := json.Unmarshal(attributes, &record.Attributes); err != nil {
			return Record{}, err
		}
	}
	return record, nil
}

var _ View = (*MySQLView)(nil)
