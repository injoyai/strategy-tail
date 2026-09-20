package researchdata

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"time"

	_ "github.com/glebarez/go-sqlite"
)

// SQLiteStore is a persistent View for full-market historical research data.
// It keeps dataset contracts and record revisions in one local SQLite file.
type SQLiteStore struct {
	db *sql.DB
	mu sync.Mutex
}

// OpenSQLiteStore opens (or creates) a persistent research-data database.
func OpenSQLiteStore(path string) (*SQLiteStore, error) {
	if path == "" {
		return nil, fmt.Errorf("researchdata: sqlite path is empty")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("researchdata: create sqlite directory: %w", err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("researchdata: open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)
	store := &SQLiteStore{db: db}
	if err := store.init(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (s *SQLiteStore) init() error {
	const schema = `
PRAGMA journal_mode=WAL;
PRAGMA busy_timeout=5000;
CREATE TABLE IF NOT EXISTS research_datasets (
  id TEXT PRIMARY KEY,
  spec_json BLOB NOT NULL
);
CREATE TABLE IF NOT EXISTS research_records (
  dataset TEXT NOT NULL,
  code TEXT NOT NULL,
  record_key TEXT NOT NULL,
  source TEXT NOT NULL,
  revision TEXT NOT NULL DEFAULT '',
  event_at INTEGER NOT NULL,
  available_at INTEGER NOT NULL,
  values_json BLOB,
  attributes_json BLOB,
  PRIMARY KEY (dataset, code, record_key, available_at, revision)
);
CREATE INDEX IF NOT EXISTS idx_research_records_lookup
  ON research_records(dataset, code, event_at, available_at);
`
	if _, err := s.db.Exec(schema); err != nil {
		return fmt.Errorf("researchdata: initialize sqlite: %w", err)
	}
	return nil
}

func (s *SQLiteStore) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// RegisterDataset persists an immutable normalized dataset contract. Repeating
// the exact same registration is idempotent; semantic changes require a new ID
// or version and are rejected instead of silently reinterpreting old records.
func (s *SQLiteStore) RegisterDataset(spec DatasetSpec) error {
	if err := spec.Validate(); err != nil {
		return err
	}
	b, err := json.Marshal(spec)
	if err != nil {
		return fmt.Errorf("researchdata: encode dataset %q: %w", spec.ID, err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var old []byte
	err = s.db.QueryRow(`SELECT spec_json FROM research_datasets WHERE id = ?`, spec.ID).Scan(&old)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		_, err = s.db.Exec(`INSERT INTO research_datasets(id, spec_json) VALUES(?, ?)`, spec.ID, b)
		if err != nil {
			return fmt.Errorf("researchdata: register dataset %q: %w", spec.ID, err)
		}
		return nil
	case err != nil:
		return fmt.Errorf("researchdata: read dataset %q: %w", spec.ID, err)
	}
	var oldSpec DatasetSpec
	if err := json.Unmarshal(old, &oldSpec); err != nil {
		return fmt.Errorf("researchdata: decode dataset %q: %w", spec.ID, err)
	}
	if !reflect.DeepEqual(oldSpec, spec) {
		return fmt.Errorf("researchdata: dataset %q already registered with a different contract", spec.ID)
	}
	return nil
}

// Ingest validates a complete batch and commits it atomically. An identical
// record revision is replaced, making interrupted historical syncs resumable.
func (s *SQLiteStore) Ingest(records []Record) error {
	for _, record := range records {
		if err := record.Validate(); err != nil {
			return err
		}
	}
	if len(records) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("researchdata: begin sqlite ingest: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	datasets := make(map[string]DatasetSpec)
	for _, record := range records {
		spec, ok := datasets[record.Dataset]
		if !ok {
			var raw []byte
			if err := tx.QueryRow(`SELECT spec_json FROM research_datasets WHERE id = ?`, record.Dataset).Scan(&raw); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return fmt.Errorf("researchdata: dataset %q is not registered", record.Dataset)
				}
				return fmt.Errorf("researchdata: read dataset %q: %w", record.Dataset, err)
			}
			if err := json.Unmarshal(raw, &spec); err != nil {
				return fmt.Errorf("researchdata: decode dataset %q: %w", record.Dataset, err)
			}
			datasets[record.Dataset] = spec
		}
		if record.Source != spec.Source {
			return fmt.Errorf("researchdata: dataset %q source %q does not match catalog source %q", record.Dataset, record.Source, spec.Source)
		}
		if err := validateRecordFields(spec, record); err != nil {
			return err
		}
		values, err := json.Marshal(record.Values)
		if err != nil {
			return fmt.Errorf("researchdata: encode record %q values: %w", record.Key, err)
		}
		attributes, err := json.Marshal(record.Attributes)
		if err != nil {
			return fmt.Errorf("researchdata: encode record %q attributes: %w", record.Key, err)
		}
		_, err = tx.Exec(`
INSERT INTO research_records(dataset, code, record_key, source, revision, event_at, available_at, values_json, attributes_json)
VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(dataset, code, record_key, available_at, revision) DO UPDATE SET
  source=excluded.source,
  event_at=excluded.event_at,
  values_json=excluded.values_json,
  attributes_json=excluded.attributes_json`,
			record.Dataset, record.Code, record.Key, record.Source, record.Revision,
			record.EventAt.UnixNano(), record.AvailableAt.UnixNano(), values, attributes)
		if err != nil {
			return fmt.Errorf("researchdata: ingest record %q: %w", record.Key, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("researchdata: commit sqlite ingest: %w", err)
	}
	return nil
}

func validateRecordFields(spec DatasetSpec, record Record) error {
	if len(spec.Fields) == 0 {
		return nil
	}
	allowed := make(map[string]struct{}, len(spec.Fields))
	for _, field := range spec.Fields {
		allowed[field.Name] = struct{}{}
	}
	for field := range record.Values {
		if _, ok := allowed[field]; !ok {
			return fmt.Errorf("researchdata: dataset %q does not declare field %q", record.Dataset, field)
		}
	}
	return nil
}

func (s *SQLiteStore) Latest(dataset, code string, asOf time.Time) (Record, bool) {
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
	return record, err == nil
}

func (s *SQLiteStore) Range(dataset, code string, start, end, asOf time.Time) []Record {
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
		return nil
	}
	defer rows.Close()
	latest := make(map[string]Record)
	for rows.Next() {
		record, err := scanRecord(rows)
		if err != nil {
			return nil
		}
		old, ok := latest[record.Key]
		if !ok || record.AvailableAt.After(old.AvailableAt) {
			latest[record.Key] = record
		}
	}
	if err := rows.Err(); err != nil {
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

var _ View = (*SQLiteStore)(nil)
