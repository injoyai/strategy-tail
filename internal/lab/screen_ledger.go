package lab

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/injoyai/strategy-tail/internal/screener"
)

type screenPosition struct {
	ID       int64
	Signal   screener.Signal
	Strategy screenStrategy
}

type screenSaleEvent struct {
	Type   string          `json:"type"`
	ID     int64           `json:"id"`
	Name   string          `json:"strategyName"`
	Signal screener.Signal `json:"signal"`
}

type screenReplayStatus struct {
	Through     string
	Lookback    int
	FailedCodes []string
}

func openScreenLedger(path string) (*sql.DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`
		PRAGMA busy_timeout=5000;
		CREATE TABLE IF NOT EXISTS screen_trades (
			id INTEGER PRIMARY KEY,
			strategy TEXT NOT NULL,
			code TEXT NOT NULL,
			buy_day TEXT NOT NULL,
			entry TEXT NOT NULL,
			seller TEXT NOT NULL,
			exit TEXT,
			sell_time TEXT,
			UNIQUE(strategy, code, buy_day)
		);
		CREATE INDEX IF NOT EXISTS screen_pending ON screen_trades (id) WHERE exit IS NULL;
		CREATE TABLE IF NOT EXISTS screen_sale_events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			trade_id INTEGER NOT NULL UNIQUE,
			payload TEXT NOT NULL
		);
		CREATE TABLE IF NOT EXISTS screen_replay_status (
			id INTEGER PRIMARY KEY CHECK (id = 1),
			payload BLOB NOT NULL
		);
	`); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func insertScreenEntries(db *sql.DB, entries []screenPosition) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.Prepare(`INSERT INTO screen_trades (strategy, code, buy_day, entry, seller)
		VALUES (?, ?, ?, ?, ?) ON CONFLICT(strategy, code, buy_day) DO NOTHING`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, entry := range entries {
		row := entry.Signal
		if _, err := time.Parse(time.DateTime, row.BuyTime); err != nil {
			return err
		}
		payload, err := json.Marshal(row)
		if err != nil {
			return err
		}
		seller, err := json.Marshal(entry.Strategy)
		if err != nil {
			return err
		}
		if _, err := stmt.Exec(row.Strategy, row.Code, row.BuyTime[:10], string(payload), string(seller)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// closeScreenEntry commits the exit and its notification in a single transaction.
func closeScreenEntry(db *sql.DB, entry screenPosition, exit screener.Signal) error {
	payload, err := json.Marshal(exit)
	if err != nil {
		return err
	}
	event, err := json.Marshal(screenSaleEvent{Type: "sell", Name: entry.Strategy.Name, Signal: exit})
	if err != nil {
		return err
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`UPDATE screen_trades SET exit=?, sell_time=? WHERE id=? AND exit IS NULL`, string(payload), exit.SellTime, entry.ID)
	if err != nil {
		return err
	}
	changed, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if changed != 0 {
		if _, err := tx.Exec(`INSERT INTO screen_sale_events (trade_id, payload) VALUES (?, ?)`, entry.ID, string(event)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func screenEntries(db *sql.DB, beforeDay string) ([]screenPosition, error) {
	query := `SELECT id, COALESCE(exit, entry), seller FROM screen_trades`
	args := []any{}
	if beforeDay != "" {
		query += ` WHERE exit IS NULL AND buy_day < ?`
		args = append(args, beforeDay)
	}
	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var entries []screenPosition
	for rows.Next() {
		var entry screenPosition
		var row, seller string
		if err := rows.Scan(&entry.ID, &row, &seller); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(row), &entry.Signal); err != nil {
			return nil, fmt.Errorf("交易 %d: %w", entry.ID, err)
		}
		if err := json.Unmarshal([]byte(seller), &entry.Strategy); err != nil {
			return nil, fmt.Errorf("交易 %d: %w", entry.ID, err)
		}
		entries = append(entries, entry)
	}
	return entries, rows.Err()
}

func screenEvents(db *sql.DB, after int64, day string) ([]screenSaleEvent, error) {
	rows, err := db.Query(`SELECT e.id, e.payload FROM screen_sale_events e
		JOIN screen_trades t ON t.id=e.trade_id WHERE e.id > ? AND t.sell_time >= ?
		ORDER BY e.id LIMIT 100`, after, day)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []screenSaleEvent
	for rows.Next() {
		var event screenSaleEvent
		var id int64
		var payload string
		if err := rows.Scan(&id, &payload); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(payload), &event); err != nil {
			return nil, err
		}
		event.ID = id
		out = append(out, event)
	}
	return out, rows.Err()
}

func (s *screenService) ledgerPath() string {
	return filepath.Join(filepath.Dir(s.path), "screen-trades.db")
}

func (s *screenService) trackedScreenEntries() ([]screenPosition, error) {
	db, err := openExistingScreenLedger(s.ledgerPath())
	if err != nil || db == nil {
		return nil, err
	}
	defer db.Close()
	return screenEntries(db, "")
}

func openExistingScreenLedger(path string) (*sql.DB, error) {
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	location := url.URL{Scheme: "file", Path: path, RawQuery: "mode=ro&_pragma=busy_timeout(5000)"}
	return sql.Open("sqlite", location.String())
}

func saveScreenReplay(ctx context.Context, path string, config screenConfig, history screener.History, lookback int, pending map[screener.SignalKey]screenPosition) error {
	payload, err := json.Marshal(screenReplayStatus{Through: history.Through, Lookback: lookback, FailedCodes: history.FailedCodes})
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	db, err := openScreenLedger(path)
	if err != nil {
		return err
	}
	defer db.Close()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	insert, err := tx.PrepareContext(ctx, `INSERT INTO screen_trades (strategy, code, buy_day, entry, seller, exit, sell_time)
		VALUES (?, ?, ?, ?, ?, ?, ?) ON CONFLICT(strategy, code, buy_day) DO NOTHING`)
	if err != nil {
		return err
	}
	defer insert.Close()
	update, err := tx.PrepareContext(ctx, `UPDATE screen_trades
		SET entry=CASE WHEN ? IS NULL THEN ? ELSE entry END, exit=?, sell_time=? WHERE id=? AND exit IS NULL`)
	if err != nil {
		return err
	}
	defer update.Close()
	strategies := make(map[string]screenStrategy, len(config.Strategies))
	for _, strategy := range config.Strategies {
		strategies[strategy.ID] = strategy
	}
	for _, row := range history.Rows {
		var exit, sellTime any
		if row.SellTime != "" {
			data, err := json.Marshal(row)
			if err != nil {
				return err
			}
			exit, sellTime = string(data), row.SellTime
		}
		buy := row
		buy.SellTime, buy.SellPrice, buy.SellSource = "", 0, ""
		buy.SetRate()
		entry, err := json.Marshal(buy)
		if err != nil {
			return err
		}
		key := row.Key()
		if old, ok := pending[key]; ok {
			if _, err := update.ExecContext(ctx, sellTime, string(entry), exit, sellTime, old.ID); err != nil {
				return err
			}
			continue
		}
		strategy := strategies[row.Strategy]
		if !strategy.Enabled {
			return fmt.Errorf("回算结果包含未启用的策略 %q", row.Strategy)
		}
		seller, err := json.Marshal(strategy)
		if err != nil {
			return err
		}
		if _, err := insert.ExecContext(ctx, row.Strategy, row.Code, key.Day, string(entry), string(seller), exit, sellTime); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO screen_replay_status (id, payload) VALUES (1, ?)
		ON CONFLICT(id) DO UPDATE SET payload = excluded.payload`, payload); err != nil {
		return err
	}
	return tx.Commit()
}

func readScreenReplay(path string) (*screenReplayStatus, error) {
	db, err := openExistingScreenLedger(path)
	if err != nil || db == nil {
		return nil, err
	}
	defer db.Close()
	var exists bool
	// The ledger can predate the replay table. Read requests leave it untouched.
	if err := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='screen_replay_status')`).Scan(&exists); err != nil || !exists {
		return nil, err
	}
	var payload []byte
	err = db.QueryRow(`SELECT payload FROM screen_replay_status WHERE id = 1`).Scan(&payload)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var snapshot screenReplayStatus
	if err := json.Unmarshal(payload, &snapshot); err != nil {
		return nil, err
	}
	return &snapshot, nil
}
