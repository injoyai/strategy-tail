# SQLite 买点持久化重构 实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 将 /cmd/screen 的 JSON 文件持久化改为 SQLite，区分今日买点（实时）和历史买点（收盘确认），卖出只判定一次，历史现价持续更新。

**Architecture:** SQLite 做持久化 + 内存缓存。今日买点仅内存实时计算，收盘后写入 DB 成为历史买点。卖出判定仅针对未 sold 的历史买点，首次卖出即标记 sold=1 不再重判。每次选股刷新时更新所有历史买卖点现价并推送。

**Tech Stack:** Go, database/sql, github.com/glebarez/go-sqlite (已在 go.sum), 现有框架 fbr/tdx

---

## File Structure

| 文件 | 操作 | 职责 |
|------|------|------|
| `cmd/screen/main.go` | 修改 | 全部改动集中在此文件 |

---

### Task 1: 添加 SQLite DB 层

**Files:**
- Modify: `cmd/screen/main.go`

- [ ] **Step 1: 添加 import 和 BuyPoint 结构体**

在 import 块中添加 `database/sql` 和 SQLite 驱动：

```go
import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	_ "github.com/glebarez/go-sqlite"
	"github.com/injoyai/conv/cfg"
	"github.com/injoyai/frame/fbr"
	"github.com/injoyai/logs"
	"github.com/injoyai/strategy-tail"
	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/tdx"
	"github.com/injoyai/strategy-tail/lib/extend"
	"github.com/injoyai/tdx/protocol"
)
```

在响应数据结构区域之后、ScreenService 定义之前，添加 BuyPoint 结构体和 DB 初始化函数：

```go
// =========================================================
// SQLite 持久化
// =========================================================

const dbPath = "./data/screen.db"

// BuyPoint - 历史买点（内存 + DB 对应）
type BuyPoint struct {
	ID        int64
	Code      string
	BuyTime   time.Time
	BuyPrice  float64
	BuyDate   string // YYYY-MM-DD
	Sold      bool
	SellPrice float64
	SellTime  time.Time
}

// initDB - 初始化 SQLite 数据库，创建表和索引
func initDB() (*sql.DB, error) {
	if err := os.MkdirAll(filepath.Dir(dbPath), 0755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, err
	}
	// 启用 WAL 模式，提升并发读写性能
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		return nil, err
	}
	schema := `
	CREATE TABLE IF NOT EXISTS buy_points (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		code TEXT NOT NULL,
		buy_time DATETIME NOT NULL,
		buy_price REAL NOT NULL,
		buy_date TEXT NOT NULL,
		sold INTEGER DEFAULT 0,
		sell_price REAL DEFAULT 0,
		sell_time DATETIME,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);
	CREATE INDEX IF NOT EXISTS idx_buy_date ON buy_points(buy_date);
	CREATE INDEX IF NOT EXISTS idx_sold ON buy_points(sold);
	`
	if _, err := db.Exec(schema); err != nil {
		return nil, err
	}
	return db, nil
}

// loadHistoryBuys - 从 DB 加载指定日期范围的历史买点
func loadHistoryBuys(db *sql.DB, dates []string) ([]BuyPoint, error) {
	if len(dates) == 0 {
		return nil, nil
	}
	// 构建 IN 子句占位符
	placeholders := ""
	args := make([]any, len(dates))
	for i, d := range dates {
		if i > 0 {
			placeholders += ","
		}
		placeholders += "?"
		args[i] = d
	}
	query := "SELECT id, code, buy_time, buy_price, buy_date, sold, sell_price, sell_time FROM buy_points WHERE buy_date IN (" + placeholders + ") ORDER BY buy_time DESC"
	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []BuyPoint
	for rows.Next() {
		var bp BuyPoint
		var sold int
		var sellPrice sql.NullFloat64
		var sellTime sql.NullString
		if err := rows.Scan(&bp.ID, &bp.Code, &bp.BuyTime, &bp.BuyPrice, &bp.BuyDate, &sold, &sellPrice, &sellTime); err != nil {
			return nil, err
		}
		bp.Sold = sold == 1
		if sellPrice.Valid {
			bp.SellPrice = sellPrice.Float64
		}
		if sellTime.Valid {
			bp.SellTime, _ = time.Parse(time.DateTime, sellTime.String)
		}
		result = append(result, bp)
	}
	return result, rows.Err()
}

// insertBuyPoints - 批量插入买点记录
func insertBuyPoints(db *sql.DB, date string, buys []core.Buy) error {
	if len(buys) == 0 {
		return nil
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	// 先删除该日期的旧记录
	if _, err := tx.Exec("DELETE FROM buy_points WHERE buy_date = ?", date); err != nil {
		tx.Rollback()
		return err
	}
	stmt, err := tx.Prepare("INSERT INTO buy_points (code, buy_time, buy_price, buy_date) VALUES (?, ?, ?, ?)")
	if err != nil {
		tx.Rollback()
		return err
	}
	defer stmt.Close()
	for _, b := range buys {
		if _, err := stmt.Exec(b.Code, b.Time, b.Price.Float64(), date); err != nil {
			tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

// markAsSold - 标记买点为已卖出
func markAsSold(db *sql.DB, id int64, sellPrice float64, sellTime time.Time) error {
	_, err := db.Exec("UPDATE buy_points SET sold = 1, sell_price = ?, sell_time = ? WHERE id = ?", sellPrice, sellTime.Format(time.DateTime), id)
	return err
}

// deleteOldBuyPoints - 删除超出回看窗口的旧记录
func deleteOldBuyPoints(db *sql.DB, validDates []string) error {
	if len(validDates) == 0 {
		return nil
	}
	placeholders := ""
	args := make([]any, len(validDates))
	for i, d := range validDates {
		if i > 0 {
			placeholders += ","
		}
		placeholders += "?"
		args[i] = d
	}
	_, err := db.Exec("DELETE FROM buy_points WHERE buy_date NOT IN ("+placeholders+")", args...)
	return err
}
```

- [ ] **Step 2: 编译验证**

Run: `cd c:\ssd\strategy-tail && go build ./cmd/screen/`
Expected: 编译通过（新代码未引用，不影响现有逻辑）

---

### Task 2: 修改 ScreenService 结构体

**Files:**
- Modify: `cmd/screen/main.go`

- [ ] **Step 1: 替换 ScreenService 结构体字段**

将现有 ScreenService：

```go
type ScreenService struct {
	mu               sync.RWMutex
	sellLookbackDays int
	buyHistory       map[string][]core.Buy
	soldBuys         map[string]*SellItem
	lastBuys         *BuyResponse
	lastSells        *SellResponse
	lastHistory      *HistoryResponse
	subscribers      map[*fbr.Websocket]bool
}
```

替换为：

```go
type ScreenService struct {
	mu               sync.RWMutex
	sellLookbackDays int
	db               *sql.DB
	historyBuys      []BuyPoint           // 从 DB 加载的历史买点（已收盘确认）
	todayBuys        []BuyItem            // 今日实时买点（仅内存，收盘后写入 DB）
	todayPersisted   bool                 // 今日买点是否已持久化
	lastBuys         *BuyResponse
	lastSells        *SellResponse
	lastHistory      *HistoryResponse
	subscribers      map[*fbr.Websocket]bool
}
```

- [ ] **Step 2: 修改 newScreenService**

将现有：

```go
func newScreenService(sellLookbackDays int) *ScreenService {
	return &ScreenService{
		sellLookbackDays: sellLookbackDays,
		buyHistory:       make(map[string][]core.Buy),
		soldBuys:         make(map[string]*SellItem),
		subscribers:      make(map[*fbr.Websocket]bool),
	}
}
```

替换为：

```go
func newScreenService(sellLookbackDays int) (*ScreenService, error) {
	db, err := initDB()
	if err != nil {
		return nil, err
	}
	return &ScreenService{
		sellLookbackDays: sellLookbackDays,
		db:               db,
		subscribers:      make(map[*fbr.Websocket]bool),
	}, nil
}
```

- [ ] **Step 3: 编译验证**

Run: `cd c:\ssd\strategy-tail && go build ./cmd/screen/`
Expected: 编译失败（因为引用了已删除的字段），这是预期的，后续 Task 修复

---

### Task 3: 重写 backfillHistory（改用 SQLite）

**Files:**
- Modify: `cmd/screen/main.go`

- [ ] **Step 1: 替换 backfillHistory 函数**

将整个 `backfillHistory` 函数替换为：

```go
// backfillHistory - 启动时回填最近 N 个交易日的买点（不含今日）
// 使用本地历史日线（收盘数据），今日买点由 doScreenBuys 用实时行情计算
func (s *ScreenService) backfillHistory() {
	now := time.Now()
	today := now.Format(time.DateOnly)
	dates := s.recentDates(now)

	// 1. 从 DB 加载已有历史
	s.mu.Lock()
	historyBuys, err := loadHistoryBuys(s.db, dates)
	if err != nil {
		logs.Warnf("[回填] 加载 DB 历史失败，将全量计算: %v\n", err)
		historyBuys = nil
	}
	s.historyBuys = historyBuys
	s.mu.Unlock()

	// 2. 找出缺失的日期（DB 中没有记录的日期）
	var missing []string
	for _, date := range dates {
		if date == today {
			continue
		}
		found := false
		for _, bp := range historyBuys {
			if bp.BuyDate == date {
				found = true
				break
			}
		}
		if !found {
			missing = append(missing, date)
		}
	}

	if len(missing) == 0 {
		logs.Infof("[回填] DB 历史完整，无需重新计算\n")
		return
	}

	logs.Infof("[回填] 需补齐 %d 个交易日: %v\n", len(missing), missing)

	codes := common.GetNoPriceLimitCodes()
	for _, date := range missing {
		day, err := time.ParseInLocation(time.DateOnly, date, time.Local)
		if err != nil {
			logs.Errf("[回填] 解析日期 %s 失败: %v", date, err)
			continue
		}
		at := time.Date(day.Year(), day.Month(), day.Day(), 15, 0, 0, 0, time.Local)

		scr := core.Screen{
			Buyer:        common.MACDBuyer,
			Codes:        codes,
			Goroutines:   10,
			GetDayKlines: common.GetDayKlines,
		}
		buys, err := scr.Run(codes, at)
		if err != nil {
			logs.Errf("[回填] %s 选股失败: %v", date, err)
			continue
		}

		coreBuys := toCoreBuys(buys)
		// 写入 DB
		if err := insertBuyPoints(s.db, date, coreBuys); err != nil {
			logs.Errf("[回填] %s 写入 DB 失败: %v", date, err)
			continue
		}
		logs.Infof("[回填] %s 选出 %d 只\n", date, len(buys))
	}

	// 重新从 DB 加载
	s.mu.Lock()
	s.historyBuys, _ = loadHistoryBuys(s.db, dates)
	s.mu.Unlock()

	// 清理超出回看窗口的旧记录
	validDates := make([]string, 0, len(dates))
	for _, d := range dates {
		validDates = append(validDates, d)
	}
	if err := deleteOldBuyPoints(s.db, validDates); err != nil {
		logs.Errf("[回填] 清理旧记录失败: %v\n", err)
	}

	logs.Infof("[回填] 完成\n")
}
```

- [ ] **Step 2: 编译验证**

Run: `cd c:\ssd\strategy-tail && go build ./cmd/screen/`
Expected: 仍有编译错误（其他函数引用了旧字段），继续

---

### Task 4: 重写 doScreenBuys（今日买点仅内存 + 收盘后持久化）

**Files:**
- Modify: `cmd/screen/main.go`

- [ ] **Step 1: 替换 doScreenBuys 函数**

将整个 `doScreenBuys` 函数替换为：

```go
// doScreenBuys - 执行一次选股，更新今日买点、广播 {type:"buy"}
// 今日买点仅保存在内存中，收盘后才写入 DB
func (s *ScreenService) doScreenBuys() {
	codes := common.GetNoPriceLimitCodes()
	now := time.Now()

	logs.Infof("[选股] 拉取实时行情，共 %d 只股票\n", len(codes))
	quoteMap, err := getRealtimeQuotes(codes)
	if err != nil {
		logs.Errf("[选股] 拉取行情失败: %v\n", err)
		return
	}

	scr := core.Screen{
		Buyer:        common.MACDBuyer,
		Codes:        codes,
		Goroutines:   10,
		GetDayKlines: makeIntradayGetDayKlines(quoteMap),
	}

	buys, err := scr.Run(codes, now)
	if err != nil {
		logs.Errf("[选股] 执行失败: %v", err)
		return
	}

	// 计算涨幅，构建今日买点列表
	items := make([]BuyItem, 0, len(buys))
	for _, b := range buys {
		riseRate := 0.0
		if b.Price > 0 {
			if quote := quoteMap[b.Code]; quote != nil && quote.K.Last > 0 {
				riseRate = (b.Price.Float64() - quote.K.Last.Float64()) / quote.K.Last.Float64() * 100
			}
		}
		items = append(items, BuyItem{
			Code:  b.Code,
			Name:  common.Manage.Codes.GetName(b.Code),
			Date:  b.Time.Format(time.DateOnly),
			Time:  b.Time.Format(time.DateTime),
			Price: b.Price.Float64(),
			Rise:  riseRate,
		})
	}

	resp := &BuyResponse{
		Type:    "buy",
		Count:   len(items),
		Time:    now.Format(time.DateTime),
		Results: items,
	}

	s.mu.Lock()
	s.todayBuys = items
	s.lastBuys = resp
	s.mu.Unlock()

	logs.Infof("[选股] 选出 %d 只股票\n", len(items))
	s.broadcast(resp)

	// 收盘后（15:00 之后）持久化今日买点到 DB
	if now.Hour() >= 15 && !s.todayPersisted {
		s.persistTodayBuys(now, buys, quoteMap)
	}

	// 买点更新后，紧接着重算卖点（仅历史买点）和更新历史现价
	s.doScreenSells(quoteMap)
	s.doScreenHistory(quoteMap)
}
```

- [ ] **Step 2: 添加 persistTodayBuys 方法**

在 `doScreenBuys` 之后添加：

```go
// persistTodayBuys - 收盘后将今日买点写入 DB，成为历史买点
func (s *ScreenService) persistTodayBuys(now time.Time, buys []*core.Buy, quoteMap map[string]*protocol.Quote) {
	today := now.Format(time.DateOnly)
	coreBuys := toCoreBuys(buys)

	if err := insertBuyPoints(s.db, today, coreBuys); err != nil {
		logs.Errf("[收盘持久化] 写入 DB 失败: %v\n", err)
		return
	}

	// 重新从 DB 加载历史买点
	dates := s.recentDates(now)
	s.mu.Lock()
	s.historyBuys, _ = loadHistoryBuys(s.db, dates)
	s.todayPersisted = true
	s.mu.Unlock()

	logs.Infof("[收盘持久化] 今日买点 %d 只已写入 DB\n", len(coreBuys))

	// 新写入的历史买点也需要判定卖出
	s.doScreenSells(quoteMap)
	s.doScreenHistory(quoteMap)
}
```

- [ ] **Step 3: 编译验证**

Run: `cd c:\ssd\strategy-tail && go build ./cmd/screen/`
Expected: 仍有编译错误，继续

---

### Task 5: 重写 doScreenSells（仅判定未卖出的历史买点）

**Files:**
- Modify: `cmd/screen/main.go`

- [ ] **Step 1: 替换 doScreenSells 函数**

将整个 `doScreenSells` 函数替换为：

```go
// doScreenSells - 基于未卖出的历史买点判定卖点，更新并广播 {type:"sell"}
// 已 sold=1 的买点不再重复判定
func (s *ScreenService) doScreenSells(quoteMap map[string]*protocol.Quote) {
	now := time.Now()

	// 收集未卖出的历史买点
	s.mu.RLock()
	var candidates []BuyPoint
	for _, bp := range s.historyBuys {
		if !bp.Sold {
			candidates = append(candidates, bp)
		}
	}
	s.mu.RUnlock()

	if len(candidates) == 0 {
		resp := &SellResponse{Type: "sell", Count: 0, Time: now.Format(time.DateTime), Results: []SellItem{}}
		s.mu.Lock()
		s.lastSells = resp
		s.mu.Unlock()
		s.broadcast(resp)
		return
	}

	// 收集需要拉取行情的代码
	codeSet := make(map[string]struct{}, len(candidates))
	for _, b := range candidates {
		codeSet[b.Code] = struct{}{}
	}

	if quoteMap == nil {
		codes := make([]string, 0, len(codeSet))
		for code := range codeSet {
			codes = append(codes, code)
		}
		var err error
		quoteMap, err = getRealtimeQuotes(codes)
		if err != nil {
			logs.Errf("[卖点] 拉取行情失败: %v\n", err)
			return
		}
	}

	getKlines := makeIntradayGetDayKlines(quoteMap)
	start := now.AddDate(0, -4, 0)
	end := time.Date(now.Year(), now.Month(), now.Day(), 15, 1, 0, 0, time.Local)

	sells := make([]SellItem, 0)
	soldIDs := make([]int64, 0) // 需要标记为已卖出的 ID

	for _, bp := range candidates {
		dks, err := getKlines(bp.Code, start, end)
		if err != nil || len(dks) == 0 {
			continue
		}
		coreBuy := core.Buy{
			Code:  bp.Code,
			Time:  bp.BuyTime,
			Price: protocol.Yuan(bp.BuyPrice),
		}
		if !common.MACDSeller.Sell(bp.Code, dks, coreBuy) {
			continue
		}
		today := dks[len(dks)-1]
		sellPrice := today.Close.Float64()
		profitRate := 0.0
		if bp.BuyPrice > 0 {
			profitRate = (sellPrice - bp.BuyPrice) / bp.BuyPrice * 100
		}
		sells = append(sells, SellItem{
			Code:       bp.Code,
			Name:       common.Manage.Codes.GetName(bp.Code),
			BuyTime:    bp.BuyTime.Format(time.DateTime),
			BuyPrice:   bp.BuyPrice,
			SellTime:   now.Format(time.DateTime),
			SellPrice:  sellPrice,
			ProfitRate: profitRate,
		})
		soldIDs = append(soldIDs, bp.ID)
	}

	// 按代码+买入时间排序
	sort.Slice(sells, func(i, j int) bool {
		if sells[i].Code != sells[j].Code {
			return sells[i].Code < sells[j].Code
		}
		return sells[i].BuyTime < sells[j].BuyTime
	})

	resp := &SellResponse{
		Type:    "sell",
		Count:   len(sells),
		Time:    now.Format(time.DateTime),
		Results: sells,
	}

	// 标记已卖出：更新 DB 和内存
	if len(soldIDs) > 0 {
		for i, id := range soldIDs {
			if err := markAsSold(s.db, id, sells[i].SellPrice, now); err != nil {
				logs.Errf("[卖点] 标记卖出失败 id=%d: %v\n", id, err)
			}
		}
		// 更新内存中的 sold 状态
		s.mu.Lock()
		soldIDSet := make(map[int64]SellItem, len(soldIDs))
		for i, id := range soldIDs {
			soldIDSet[id] = sells[i]
		}
		for i := range s.historyBuys {
			if si, ok := soldIDSet[s.historyBuys[i].ID]; ok {
				s.historyBuys[i].Sold = true
				s.historyBuys[i].SellPrice = si.SellPrice
				s.historyBuys[i].SellTime = now
			}
		}
		s.lastSells = resp
		s.mu.Unlock()
	} else {
		s.mu.Lock()
		s.lastSells = resp
		s.mu.Unlock()
	}

	logs.Infof("[卖点] 检出 %d 只\n", len(sells))
	s.broadcast(resp)
}
```

- [ ] **Step 2: 编译验证**

Run: `cd c:\ssd\strategy-tail && go build ./cmd/screen/`
Expected: 仍有编译错误，继续

---

### Task 6: 重写 doScreenHistory（每次刷新都更新现价）

**Files:**
- Modify: `cmd/screen/main.go`

- [ ] **Step 1: 替换 doScreenHistory 函数**

将整个 `doScreenHistory` 函数替换为：

```go
// doScreenHistory - 汇总历史买点并广播 {type:"history"}
// 每次调用都更新现价，quoteMap 由调用方传入；如果为 nil 则内部拉取
func (s *ScreenService) doScreenHistory(quoteMap map[string]*protocol.Quote) {
	now := time.Now()

	s.mu.RLock()
	historyBuys := make([]BuyPoint, len(s.historyBuys))
	copy(historyBuys, s.historyBuys)
	s.mu.RUnlock()

	if len(historyBuys) == 0 {
		resp := &HistoryResponse{Type: "history", Time: now.Format(time.DateTime), Total: 0, Results: []BuyItem{}}
		s.mu.Lock()
		s.lastHistory = resp
		s.mu.Unlock()
		s.broadcast(resp)
		return
	}

	// 收集需要拉取行情的代码
	codeSet := make(map[string]struct{}, len(historyBuys))
	for _, bp := range historyBuys {
		codeSet[bp.Code] = struct{}{}
	}

	if quoteMap == nil {
		codes := make([]string, 0, len(codeSet))
		for code := range codeSet {
			codes = append(codes, code)
		}
		var err error
		quoteMap, err = getRealtimeQuotes(codes)
		if err != nil {
			logs.Errf("[历史] 拉取实时行情失败: %v\n", err)
		}
	}

	// 构建历史买点列表
	all := make([]BuyItem, 0, len(historyBuys))
	for _, bp := range historyBuys {
		item := BuyItem{
			Code:      bp.Code,
			Name:      common.Manage.Codes.GetName(bp.Code),
			Date:      bp.BuyDate,
			Time:      bp.BuyTime.Format(time.DateTime),
			Price:     bp.BuyPrice,
			Sold:      bp.Sold,
			SellPrice: bp.SellPrice,
			SellTime:  bp.SellTime.Format(time.DateTime),
		}
		if bp.Sold {
			// 已卖出：收益率用卖出价计算，但现价仍更新
			if bp.BuyPrice > 0 {
				item.IncomeRate = (bp.SellPrice - bp.BuyPrice) / bp.BuyPrice * 100
			}
		}
		// 无论是否卖出，都更新现价
		if q, ok := quoteMap[bp.Code]; ok && q.K.Close > 0 {
			item.CurrPrice = q.K.Close.Float64()
			if !bp.Sold && bp.BuyPrice > 0 {
				item.IncomeRate = (item.CurrPrice - bp.BuyPrice) / bp.BuyPrice * 100
			}
		}
		all = append(all, item)
	}

	// 按时间倒序
	sort.Slice(all, func(i, j int) bool {
		return all[i].Time > all[j].Time
	})

	resp := &HistoryResponse{
		Type:    "history",
		Time:    now.Format(time.DateTime),
		Total:   len(all),
		Results: all,
	}

	s.mu.Lock()
	s.lastHistory = resp
	s.mu.Unlock()

	s.broadcast(resp)
}
```

- [ ] **Step 2: 编译验证**

Run: `cd c:\ssd\strategy-tail && go build ./cmd/screen/`
Expected: 仍有编译错误，继续

---

### Task 7: 更新 startBackground、scheduleDailyRefresh、refreshYesterdayBuys、isTradingTime

**Files:**
- Modify: `cmd/screen/main.go`

- [ ] **Step 1: 替换 refreshYesterdayBuys**

将整个 `refreshYesterdayBuys` 函数替换为：

```go
// refreshYesterdayBuys - 用收盘数据刷新昨天的买点
// 跨天时调用：昨天盘中用实时行情计算的买点可能与收盘数据有差异，需用收盘数据重算
func (s *ScreenService) refreshYesterdayBuys() {
	now := time.Now()
	dates := s.recentDates(now)
	if len(dates) < 2 {
		return
	}

	today := now.Format(time.DateOnly)
	var yesterday string
	for _, d := range dates {
		if d != today {
			yesterday = d
			break
		}
	}
	if yesterday == "" {
		return
	}

	day, err := time.ParseInLocation(time.DateOnly, yesterday, time.Local)
	if err != nil {
		logs.Errf("[跨天刷新] 解析日期 %s 失败: %v", yesterday, err)
		return
	}
	at := time.Date(day.Year(), day.Month(), day.Day(), 15, 0, 0, 0, time.Local)

	codes := common.GetNoPriceLimitCodes()
	scr := core.Screen{
		Buyer:        common.MACDBuyer,
		Codes:        codes,
		Goroutines:   10,
		GetDayKlines: common.GetDayKlines,
	}
	buys, err := scr.Run(codes, at)
	if err != nil {
		logs.Errf("[跨天刷新] %s 选股失败: %v", yesterday, err)
		return
	}

	coreBuys := toCoreBuys(buys)
	// 用收盘数据覆盖 DB 中该日期的记录
	if err := insertBuyPoints(s.db, yesterday, coreBuys); err != nil {
		logs.Errf("[跨天刷新] 写入 DB 失败: %v\n", err)
		return
	}

	// 重新从 DB 加载
	s.mu.Lock()
	s.historyBuys, _ = loadHistoryBuys(s.db, dates)
	s.mu.Unlock()

	logs.Infof("[跨天刷新] %s 用收盘数据刷新，选出 %d 只\n", yesterday, len(buys))
}
```

- [ ] **Step 2: 替换 startBackground**

将整个 `startBackground` 函数替换为：

```go
// startBackground - 启动后台定时任务
func (s *ScreenService) startBackground(interval time.Duration) {
	// 重置今日持久化标记（新的一天）
	s.todayPersisted = false

	// 立即选股，确保客户端尽快拿到数据
	s.doScreenBuys()

	// 异步回填历史买点，回填完成后重算卖点
	go func() {
		s.backfillHistory()
		s.doScreenSells(nil)
		s.doScreenHistory(nil)
	}()

	// 启动跨天刷新协程
	go s.scheduleDailyRefresh()

	logs.Infof("[服务] 后台选股任务启动，间隔 %v\n", interval)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for range ticker.C {
		if !common.Manage.Workday.TodayIs() || !isTradingTime() {
			continue
		}
		s.doScreenBuys()
	}
}
```

- [ ] **Step 3: 替换 scheduleDailyRefresh**

将整个 `scheduleDailyRefresh` 函数替换为：

```go
// scheduleDailyRefresh - 每个交易日 00:00 用收盘数据刷新上一交易日的买点
func (s *ScreenService) scheduleDailyRefresh() {
	for {
		now := time.Now()
		next := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
		if !next.After(now) {
			next = next.AddDate(0, 0, 1)
		}
		timer := time.NewTimer(next.Sub(now))
		<-timer.C

		if !common.Manage.Workday.TodayIs() {
			continue
		}

		logs.Infof("[定时刷新] 交易日 00:00，用收盘数据刷新上一交易日买点\n")
		s.refreshYesterdayBuys()
		// 重置今日持久化标记
		s.todayPersisted = false
		// 跨天后同步推送最新历史买点
		s.doScreenHistory(nil)
	}
}
```

- [ ] **Step 4: 修改 isTradingTime（延长到 15:01）**

将 `isTradingTime` 函数替换为：

```go
// isTradingTime - 判断是否处于交易时间段
// 交易时间：上午 09:30 - 11:30，下午 13:00 - 15:01
func isTradingTime() bool {
	now := time.Now()
	h, m := now.Hour(), now.Minute()

	// 上午 09:30 - 11:30
	if h == 9 && m >= 30 {
		return true
	}
	if h == 10 {
		return true
	}
	if h == 11 && m <= 30 {
		return true
	}

	// 下午 13:00 - 15:01
	if h == 13 || h == 14 {
		return true
	}
	if h == 15 && m <= 1 {
		return true
	}

	return false
}
```

- [ ] **Step 5: 编译验证**

Run: `cd c:\ssd\strategy-tail && go build ./cmd/screen/`
Expected: 仍有编译错误（main 函数引用了旧的 newScreenService），继续

---

### Task 8: 更新 main 函数 + 删除旧 JSON 代码

**Files:**
- Modify: `cmd/screen/main.go`

- [ ] **Step 1: 更新 main 函数**

将 `main` 函数替换为：

```go
func main() {
	port := cfg.GetInt("port", 9090)
	interval := cfg.GetDuration("interval", time.Minute)
	sellLookbackDays := cfg.GetInt("sell_lookback_days", 10)

	svc, err := newScreenService(sellLookbackDays)
	if err != nil {
		logs.Panicf("初始化服务失败: %v\n", err)
	}
	defer svc.db.Close()

	// 实时计算今天的买点
	go svc.startBackground(interval)

	s := fbr.Default(
		fbr.WithPort(port),
		fbr.WithALL("/ws", func(c fbr.Ctx) {
			c.Websocket(func(ws *fbr.Websocket) {
				svc.addSubscriber(ws)
				defer svc.removeSubscriber(ws)

				// 新连接立即推送当前快照
				buys, sells, history := svc.snapshot()
				if buys != nil {
					if data, err := json.Marshal(buys); err == nil {
						ws.WriteText(string(data))
					}
				}
				if sells != nil {
					if data, err := json.Marshal(sells); err == nil {
						ws.WriteText(string(data))
					}
				}
				if history != nil {
					if data, err := json.Marshal(history); err == nil {
						ws.WriteText(string(data))
					}
				}

				ws.DiscardRead()
			})
		}),
	)

	s.Run()
}
```

- [ ] **Step 2: 删除所有旧 JSON 持久化代码**

删除以下代码块（按出现顺序）：

1. 常量 `historyFilePath` 和 `soldFilePath`
2. 结构体 `persistedBuy`
3. 函数 `loadHistoryFile`
4. 函数 `saveHistoryFile`
5. 函数 `persistHistory`
6. 函数 `loadSoldHistory`
7. 函数 `persistSoldHistory`

- [ ] **Step 3: 清理不再使用的 import**

从 import 块中移除 `"os"` 和 `"path/filepath"`（如果不再被其他代码使用）。注意 `initDB` 和 `os.MkdirAll` 仍需要这两个包，所以保留。

检查是否有其他不再使用的 import，移除之。

- [ ] **Step 4: 编译验证**

Run: `cd c:\ssd\strategy-tail && go build ./cmd/screen/`
Expected: 编译通过

---

### Task 9: 端到端验证

**Files:**
- Modify: `cmd/screen/main.go`（如有编译问题则修复）

- [ ] **Step 1: 完整编译**

Run: `cd c:\ssd\strategy-tail && go build ./cmd/screen/`
Expected: 编译通过，生成可执行文件

- [ ] **Step 2: 检查代码一致性**

检查以下要点：
- `doScreenBuys` 中今日买点只存 `todayBuys` 内存字段
- `doScreenSells` 只遍历 `historyBuys` 中 `Sold=false` 的记录
- `doScreenHistory` 每次调用都更新现价
- `persistTodayBuys` 在 15:00 后将今日买点写入 DB
- 所有 DB 操作有错误处理
- WS 推送的 history 数据中 `curr_price` 每次刷新都更新

- [ ] **Step 3: 提交代码**

```bash
git add cmd/screen/main.go
git commit -m "refactor: 将买点持久化从 JSON 文件改为 SQLite

- 新增 SQLite DB 层（initDB, BuyPoint, CRUD 操作）
- 今日买点仅内存实时计算，收盘后写入 DB
- 卖出判定仅针对未 sold 的历史买点，首次卖出即确认
- 历史买卖点现价每次选股刷新时更新
- 交易时间延长到 15:01
- 移除 buy_history.json / sold_history.json 相关代码"
```
