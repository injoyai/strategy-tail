package lab

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/injoyai/strategy-tail/lib/extend"
	"github.com/injoyai/tdx/v2/protocol"
)

// A Lab-owned derived cache; the market-data store remains read-only.
type indexResearchCache struct {
	mu   sync.Mutex
	path string
}
type researchMonth struct {
	start, end time.Time
	readStart  time.Time
	key, token string
	points     []indexResearchPoint
	dirty      bool
}

func (c *indexResearchCache) query(ctx context.Context, market *extend.MarketReader, code string, from, to time.Time, top int, now time.Time) ([]indexResearchPoint, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now = now.In(protocol.Location())
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	if from.After(today) {
		return []indexResearchPoint{}, nil
	}
	if to.After(today) {
		to = today
	}
	path := c.path
	if path == "" {
		path = filepath.Join("data", "lab", "index-research.sqlite")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err = db.ExecContext(ctx, `PRAGMA busy_timeout=5000; CREATE TABLE IF NOT EXISTS index_research_months (cache_key TEXT NOT NULL, month TEXT NOT NULL, through TEXT NOT NULL, token TEXT NOT NULL, payload BLOB NOT NULL, PRIMARY KEY(cache_key,month))`); err != nil {
		return nil, err
	}
	sum := sha256.Sum256([]byte(market.StorageIdentity()))
	key := fmt.Sprintf("v2:%s:%s:%d", hex.EncodeToString(sum[:]), code, top)
	var months []researchMonth
	for start := time.Date(from.Year(), from.Month(), 1, 0, 0, 0, 0, from.Location()); !start.After(to); start = start.AddDate(0, 1, 0) {
		end := start.AddDate(0, 1, -1)
		if end.After(today) {
			end = today
		}
		m := researchMonth{start: start, end: end, readStart: start, key: start.Format("2006-01"), token: "stable"}
		// Recently ingested bars can arrive late or be amended; recheck only the
		// recent tail at most once per hour when the page is opened.
		if !end.Before(today.AddDate(0, 0, -6)) {
			m.token = now.Format("2006-01-02T15")
		}
		var through, token string
		var payload []byte
		err := db.QueryRowContext(ctx, `SELECT through,token,payload FROM index_research_months WHERE cache_key=? AND month=?`, key, m.key).Scan(&through, &token, &payload)
		if err == sql.ErrNoRows {
			m.dirty = true
		} else if err != nil {
			return nil, err
		} else {
			if err = json.Unmarshal(payload, &m.points); err != nil {
				return nil, fmt.Errorf("指数研究缓存损坏: %w", err)
			}
			if through != end.Format(time.DateOnly) || (m.token != "stable" && token != m.token) {
				m.dirty = true
				last, parseErr := time.ParseInLocation(time.DateOnly, through, now.Location())
				if parseErr == nil && !last.After(end) {
					m.readStart = today.AddDate(0, 0, -6)
					if last.AddDate(0, 0, 1).Before(m.readStart) {
						m.readStart = last.AddDate(0, 0, 1)
					}
					if m.readStart.Before(start) {
						m.readStart = start
					}
					if m.readStart.After(end) {
						m.readStart = end
					}
					cutoff := m.readStart.Format(time.DateOnly)
					m.points = slices.DeleteFunc(m.points, func(p indexResearchPoint) bool { return p.Date >= cutoff })
				} else {
					m.points = nil
				}
			}
		}
		months = append(months, m)
	}
	dirty := false
	for i := 0; i < len(months); {
		if !months[i].dirty {
			i++
			continue
		}
		j := i + 1
		for j < len(months) && months[j].dirty && months[j].readStart.Equal(months[j-1].end.AddDate(0, 0, 1)) {
			j++
		}
		points, err := readIndexResearch(ctx, market, code, months[i].readStart, months[j-1].end, top)
		if err != nil {
			return nil, err
		}
		// Read a bounded warm-up window; fall back to older history only if the
		// source has fewer than 14 previous observations in that window.
		warmStart := months[i].readStart.AddDate(0, 0, -45)
		allBars, err := market.DayKlines(code, warmStart, months[j-1].end)
		if err != nil {
			return nil, err
		}
		prior := 0
		for _, bar := range allBars {
			if bar.Time.Before(months[i].readStart) {
				prior++
			}
		}
		if prior < 14 {
			allBars, err = market.DayKlines(code, time.Time{}, months[j-1].end)
			if err != nil {
				return nil, err
			}
		}
		rsi := indexRSI(allBars)
		monthIndex := make(map[string]int, j-i)
		for k := i; k < j; k++ {
			monthIndex[months[k].key] = k
		}
		for _, point := range points {
			if v, ok := rsi[point.Date]; ok {
				point.RSI = v
			}
			if len(point.Date) >= 7 {
				if k, ok := monthIndex[point.Date[:7]]; ok {
					months[k].points = append(months[k].points, point)
				}
			}
		}

		dirty = true
		i = j
	}
	if dirty {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return nil, err
		}
		defer tx.Rollback()
		for _, m := range months {
			if !m.dirty {
				continue
			}
			payload, err := json.Marshal(m.points)
			if err != nil {
				return nil, err
			}
			if _, err = tx.ExecContext(ctx, `INSERT INTO index_research_months(cache_key,month,through,token,payload) VALUES(?,?,?,?,?) ON CONFLICT(cache_key,month) DO UPDATE SET through=excluded.through,token=excluded.token,payload=excluded.payload`, key, m.key, m.end.Format(time.DateOnly), m.token, payload); err != nil {
				return nil, err
			}
		}
		if err = tx.Commit(); err != nil {
			return nil, err
		}
	}
	var out []indexResearchPoint
	first, last := from.Format(time.DateOnly), to.Format(time.DateOnly)
	for _, m := range months {
		for _, p := range m.points {
			if p.Date >= first && p.Date <= last {
				out = append(out, p)
			}
		}
	}
	slices.SortFunc(out, func(a, b indexResearchPoint) int { return strings.Compare(a.Date, b.Date) })
	if out == nil {
		out = []indexResearchPoint{}
	}
	return out, nil
}

func indexRSI(bars extend.Klines) map[string]*float64 {
	result := make(map[string]*float64, len(bars))
	var changes []float64
	var prev float64
	for _, bar := range bars {
		date := bar.Time.Format(time.DateOnly)
		if !positiveFinite(bar.Close) {
			prev = 0
			changes = nil
			continue
		}
		if prev == 0 {
			prev = bar.Close
			continue
		}
		delta := bar.Close - prev
		prev = bar.Close
		changes = append(changes, delta)
		if len(changes) > 14 {
			changes = changes[1:]
		}
		if len(changes) < 14 {
			continue
		}
		var gain, loss float64
		for _, d := range changes {
			if d > 0 {
				gain += d
			} else {
				loss -= d
			}
		}
		v := 50.0
		if gain+loss > 0 {
			v = 100 * gain / (gain + loss)
		}
		result[date] = &v
	}
	return result
}
