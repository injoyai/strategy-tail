package extend

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/injoyai/logs"
	"github.com/injoyai/strategy-tail/internal/update"
	"github.com/injoyai/strategy-tail/internal/xorms"
	"github.com/injoyai/tdx/v2"
	"github.com/injoyai/tdx/v2/extend/metadata"
	"github.com/injoyai/tdx/v2/extend/model"
	"github.com/injoyai/tdx/v2/extend/pull"
	"github.com/injoyai/tdx/v2/extend/storage/sqlite"
	"github.com/injoyai/tdx/v2/protocol"
	"github.com/robfig/cron/v3"
	"xorm.io/xorm"
)

// MarketData holds the strategy application's standard market connection and metadata snapshots.
type MarketData struct {
	tdx.ClientPool
	Codes *metadata.Codes

	calendar        atomic.Pointer[metadata.Workday]
	equity          atomic.Pointer[metadata.Gbbq]
	calendarMu      sync.Mutex
	calendarChecked time.Time
	cache           *sqlite.Metadata
	service         *pull.Service
}

func (m *MarketData) Do(f func(tdx.Client) error) error {
	if m == nil || m.ClientPool == nil {
		return errors.New("标准行情连接池尚未初始化")
	}
	return m.DoContext(context.Background(), f)
}

func (m *MarketData) Workday() *metadata.Workday { return m.calendar.Load() }
func (m *MarketData) Gbbq() *metadata.Gbbq       { return m.equity.Load() }

// CheckTradingDay refreshes a calendar that does not yet cover today. An
// observed calendar cannot prove whether an unobserved weekday is a holiday.
func (m *MarketData) CheckTradingDay(now time.Time) (trading, known bool, err error) {
	now = now.In(protocol.Location())
	if now.Weekday() == time.Saturday || now.Weekday() == time.Sunday {
		return false, true, nil
	}
	if w := m.Workday(); w != nil {
		if trading, known = w.Status(now); known {
			return trading, known, nil
		}
	}
	m.calendarMu.Lock()
	defer m.calendarMu.Unlock()
	if w := m.Workday(); w != nil {
		if trading, known = w.Status(now); known {
			return trading, known, nil
		}
	}
	if m.service == nil || time.Since(m.calendarChecked) < time.Minute {
		return false, false, nil
	}
	m.calendarChecked = time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	snapshot, err := m.service.FetchWorkdays(ctx)
	if err != nil {
		return false, false, err
	}
	if err := m.saveCalendar(ctx, snapshot); err != nil {
		return false, false, err
	}
	trading, known = m.Workday().Status(now)
	return trading, known, nil
}

func (m *MarketData) saveCalendar(ctx context.Context, snapshot model.WorkdaySnapshot) error {
	if err := m.cache.SaveWorkdays(ctx, snapshot); err != nil {
		return err
	}
	snapshot, err := m.cache.LoadWorkdays(ctx, protocol.MarketAStock)
	if err != nil {
		return err
	}
	calendar, err := metadata.NewWorkday(snapshot)
	if err != nil {
		return err
	}
	m.calendar.Store(calendar)
	return nil
}

var standardExchanges = []protocol.Exchange{protocol.ExchangeSH, protocol.ExchangeSZ, protocol.ExchangeBJ}

// OpenMarketData loads cached metadata, initializes a standard connection pool,
// and schedules application-owned refreshes. The returned cleanup stops refreshes
// and closes resources after in-flight refreshes have finished.
func OpenMarketData(dir string) (result *MarketData, cleanup func() error, err error) {
	pool, err := tdx.NewPool(func() (tdx.Client, error) { return tdx.DialDefault() }, 1)
	if err != nil {
		return nil, nil, err
	}
	cr := cron.New(cron.WithSeconds(), cron.WithLocation(protocol.Location()),
		cron.WithChain(cron.SkipIfStillRunning(cron.DefaultLogger)))
	var db, equityDB *xorms.Engine
	var service *pull.Service
	closeAll := func() error {
		<-cr.Stop().Done()
		var err error
		if service != nil {
			err = errors.Join(err, service.Close())
		}
		if equityDB != nil {
			err = errors.Join(err, equityDB.Close())
		}
		if db != nil {
			err = errors.Join(err, db.Close())
		}
		return errors.Join(err, pool.Close())
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, closeAll())
		}
	}()
	db, err = xorms.NewSQLite(filepath.Join(dir, "metadata.db"))
	if err != nil {
		return nil, nil, err
	}
	cache, err := sqlite.NewMetadata(db.Engine)
	if err != nil {
		return nil, nil, err
	}
	service, err = pull.New(&pull.Config{Pool: pool})
	if err != nil {
		return nil, nil, err
	}
	m := &MarketData{ClientPool: pool, cache: cache, service: service}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	codes, err := cache.LoadCodes(ctx, standardExchanges)
	if err != nil {
		return nil, nil, err
	}
	m.Codes, err = metadata.NewCodes(codes.Rows)
	if err != nil {
		return nil, nil, err
	}
	refreshCodes := func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		snapshot, err := service.FetchCodes(ctx, standardExchanges...)
		if err != nil {
			return err
		}
		seen := map[string]bool{}
		for _, row := range snapshot.Rows {
			seen[row.Exchange] = true
		}
		for _, ex := range standardExchanges {
			if !seen[ex.String()] {
				return fmt.Errorf("%s 行情代码目录为空", ex)
			}
		}
		if err := cache.ReplaceCodes(ctx, snapshot); err != nil {
			return err
		}
		return m.Codes.Update(snapshot.Rows)
	}
	if !sameMarketDay(codes.FetchedAt, time.Now()) || len(codes.Rows) == 0 {
		if err := refreshCodes(); err != nil {
			if len(codes.Rows) == 0 {
				return nil, nil, fmt.Errorf("加载行情代码目录: %w", err)
			}
			logs.Warnf("刷新行情代码目录失败，继续使用缓存: %v", err)
		}
	}
	calendar, err := cache.LoadWorkdays(ctx, protocol.MarketAStock)
	if err != nil {
		return nil, nil, err
	}
	if calendar.Source != "" {
		w, err := metadata.NewWorkday(calendar)
		if err != nil {
			return nil, nil, err
		}
		m.calendar.Store(w)
	}

	if _, _, err := m.CheckTradingDay(time.Now()); err != nil {
		logs.Warnf("加载交易日快照失败: %v", err)
	}

	equityDB, err = openEquityCache(dir)
	if err != nil {
		return nil, nil, err
	}
	marks, err := update.New(equityDB, 9, 0)
	if err != nil {
		return nil, nil, err
	}
	var rows []*model.Gbbq
	if err := equityDB.Asc("Time").Find(&rows); err != nil {
		return nil, nil, err
	}
	for _, row := range rows {
		row.Time = row.Time.In(protocol.Location())
	}
	equity, err := metadata.NewGbbq(rows)
	if err != nil {
		return nil, nil, err
	}
	m.equity.Store(equity)

	refresh := func() {
		if err := refreshCodes(); err != nil {
			logs.Warnf("刷新行情代码目录失败: %v", err)
		}
		fresh, err := marks.IsUpdated("gbbq")
		if err == nil && !fresh {
			err = m.refreshEquity(equityDB, marks)
		}
		if err != nil {
			logs.Warnf("刷新股本和分红快照失败，保留缓存: %v", err)
		}
	}
	if _, err := cr.AddFunc("0 5 9 * * 1-5", refresh); err != nil {
		return nil, nil, err
	}
	// Metadata bootstrap may take thousands of protocol queries; do not hold up
	// the application when usable equity data already exists.
	if len(rows) == 0 {
		if err := m.refreshEquity(equityDB, marks); err != nil {
			return nil, nil, fmt.Errorf("加载股本和分红快照: %w", err)
		}
	}
	cr.Start()
	return m, closeAll, nil
}

func sameMarketDay(a, b time.Time) bool {
	return a.In(protocol.Location()).Format(time.DateOnly) == b.In(protocol.Location()).Format(time.DateOnly)
}

func openEquityCache(dir string) (*xorms.Engine, error) {
	filename := filepath.Join(dir, "gbbq.db")
	// Previous releases kept this cache under the runtime's default data path.
	// Import its contents separately when the configured data directory changed.
	db, err := xorms.NewSQLite(filename)
	if err != nil {
		return nil, err
	}
	if err := db.Sync2(new(model.Gbbq)); err != nil {
		db.Close()
		return nil, err
	}
	old := filepath.Join(DefaultDatabaseDir, "gbbq.db")
	a, _ := filepath.Abs(filename)
	b, _ := filepath.Abs(old)
	if a == b {
		return db, nil
	}
	count, err := db.Count(new(model.Gbbq))
	if err != nil {
		db.Close()
		return nil, err
	}
	if count > 0 {
		return db, nil
	}
	if _, err := os.Stat(old); errors.Is(err, os.ErrNotExist) {
		return db, nil
	} else if err != nil {
		db.Close()
		return nil, err
	}
	legacy, err := xorms.OpenReadOnly(old)
	if err != nil {
		db.Close()
		return nil, err
	}
	defer legacy.Close()
	var rows []*model.Gbbq
	if err := legacy.Find(&rows); err != nil {
		db.Close()
		return nil, err
	}
	if err := db.SessionFunc(func(tx *xorm.Session) error { return insertEquity(tx, rows) }); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func insertEquity(tx *xorm.Session, rows []*model.Gbbq) error {
	for len(rows) > 0 {
		n := min(len(rows), 100)
		if _, err := tx.Insert(rows[:n]); err != nil {
			return err
		}
		rows = rows[n:]
	}
	return nil
}

func (m *MarketData) refreshEquity(db *xorms.Engine, marks *update.Tracker) error {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Minute)
	defer cancel()
	snapshot, err := m.service.FetchGbbq(ctx, m.Codes.GetStockCodes())
	if err != nil {
		return err
	}
	if len(snapshot.Rows) == 0 {
		return errors.New("股本和分红快照为空")
	}
	all := m.Gbbq().All()
	for _, code := range snapshot.Codes {
		delete(all, code)
	}
	rows := append([]*model.Gbbq(nil), snapshot.Rows...)
	for _, old := range all {
		rows = append(rows, old...)
	}
	equity, err := metadata.NewGbbq(rows)
	if err != nil {
		return err
	}
	if err := db.SessionFunc(func(tx *xorm.Session) error {
		if _, err := tx.Where("1=1").Delete(new(model.Gbbq)); err != nil {
			return err
		}
		return insertEquity(tx, rows)
	}); err != nil {
		return err
	}
	m.equity.Store(equity)
	return marks.Mark("gbbq")
}

// ReadMarketMetadata opens existing snapshots without connecting to TDX or
// creating local files. An absent optional snapshot leaves its lookup empty.
func ReadMarketMetadata(dir string) (*MarketData, error) {
	var codes []*model.CodeModel
	var equity []*model.Gbbq
	err := readSnapshot(filepath.Join(dir, "metadata.db"), "codes", &codes)
	if err != nil {
		return nil, err
	}
	err = readSnapshot(filepath.Join(dir, "gbbq.db"), "Gbbq", &equity)
	if err != nil {
		return nil, err
	}
	index, err := metadata.NewCodes(codes)
	if err != nil {
		return nil, err
	}
	for _, row := range equity {
		row.Time = row.Time.In(protocol.Location())
	}
	events, err := metadata.NewGbbq(equity)
	if err != nil {
		return nil, err
	}
	m := &MarketData{Codes: index}
	m.equity.Store(events)
	return m, nil
}

func readSnapshot(path, table string, out any) error {
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	db, err := xorms.OpenReadOnly(path)
	if err != nil {
		return err
	}
	defer db.Close()
	exists, err := db.IsTableExist(table)
	if err != nil || !exists {
		return err
	}
	return db.Table(table).Find(out)
}
