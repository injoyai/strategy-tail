// main.go - 实时选股屏幕服务
//
// 该服务实现以下功能：
// 1. 从通达信获取实时行情数据
// 2. 基于预设的MACD反转策略筛选买点
// 3. 基于最近 N 个交易日的买点，使用卖出策略实时判定卖点
// 4. 通过 WebSocket 单一连接（/ws）推送两类消息：
//    - {"type":"buy", ...}  最新买点列表（今日实时）
//    - {"type":"sell", ...} 最新卖点列表
//    - {"type":"history", ...} 历史买卖点列表（现价持续更新）
//
// 交易时间：
// - 上午：9:30 - 11:30
// - 下午：13:00 - 15:01
//
// 非交易时间不执行自动刷新

package main

import (
	"database/sql"
	_ "embed"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	_ "github.com/glebarez/go-sqlite"
	"github.com/injoyai/conv/cfg"
	"github.com/injoyai/frame/fbr" // Web框架
	"github.com/injoyai/logs"      // 日志库
	"github.com/injoyai/strategy-tail"
	"github.com/injoyai/strategy-tail/core" // 选股核心模块
	"github.com/injoyai/tdx"                // 通达信SDK
	"github.com/injoyai/tdx/extend"         // 通达信扩展功能
	"github.com/injoyai/tdx/protocol"       // 通达信协议定义
)

//go:embed index.html
var indexHTML string

// =========================================================
// 实时行情 / K线 工具
// =========================================================

// getRealtimeQuotes - 批量获取实时行情
// 通达信 API 每次最多支持 80 个代码，需分批拉取
func getRealtimeQuotes(codes []string) (map[string]*protocol.Quote, error) {
	if len(codes) == 0 {
		return nil, nil
	}

	quoteMap := make(map[string]*protocol.Quote, len(codes))
	batchSize := 80

	for i := 0; i < len(codes); i += batchSize {
		end := i + batchSize
		if end > len(codes) {
			end = len(codes)
		}

		if err := common.Manage.Do(func(c *tdx.Client) error {
			quotes, err := c.GetQuote(codes[i:end]...)
			if err != nil {
				return err
			}
			for _, q := range quotes {
				quoteMap[protocol.AddPrefix(q.Code)] = q
			}
			return nil
		}); err != nil {
			logs.Errf("[行情] 批量获取失败(%d-%d): %v", i, end, err)
		}
	}

	return quoteMap, nil
}

// quoteToKline - 将实时行情转换为 K 线（流通股/总股本继承上一日）
func quoteToKline(quote *protocol.Quote, prevKline *extend.Kline) *extend.Kline {
	now := time.Now()
	return &extend.Kline{
		Unix: now.Unix(),
		Kline: &protocol.Kline{
			Last:   quote.K.Last,
			Open:   quote.K.Open,
			High:   quote.K.High,
			Low:    quote.K.Low,
			Close:  quote.K.Close,
			Volume: int64(quote.TotalHand) * 100,
			Amount: protocol.Yuan(quote.Amount),
			Time:   now,
		},
		FloatStock: prevKline.FloatStock,
		TotalStock: prevKline.TotalStock,
	}
}

// makeIntradayGetDayKlines - 构造盘中版 K 线获取函数
// 1. 读取本地历史日线
// 2. 按 [start, end] 过滤
// 3. 如有实时行情，拼接/替换今日 K 线
func makeIntradayGetDayKlines(quoteMap map[string]*protocol.Quote) func(code string, start, end time.Time) (extend.Klines, error) {
	return func(code string, start, end time.Time) (extend.Klines, error) {
		ks, err := common.Pull.DayKlines(code)
		if err != nil {
			return nil, err
		}

		var ls extend.Klines
		for _, k := range ks {
			if !k.Time.Before(start) && !k.Time.After(end) {
				ls = append(ls, k)
			}
		}

		if len(ls) == 0 {
			return ls, nil
		}

		if quote, ok := quoteMap[code]; ok {
			last := ls[len(ls)-1]
			todayKline := quoteToKline(quote, last)

			nowDate := time.Now().Format(time.DateOnly)
			if last.Time.Format(time.DateOnly) == nowDate {
				ls[len(ls)-1] = todayKline
			} else {
				ls = append(ls, todayKline)
			}
		}

		return ls, nil
	}
}

// =========================================================
// 响应数据结构
// =========================================================

// BuyItem - 买入信号条目
type BuyItem struct {
	Code       string  `json:"code"`        // 股票代码（带前缀）
	Name       string  `json:"name"`        // 股票名称
	Date       string  `json:"date"`        // 日期 YYYY-MM-DD
	Time       string  `json:"time"`        // 信号产生时间
	Price      float64 `json:"price"`       // 买入价
	Rise       float64 `json:"rise"`        // 盘中涨幅百分比（仅当日买点有效）
	CurrPrice  float64 `json:"curr_price"`  // 现价
	IncomeRate float64 `json:"income_rate"` // 收益率百分比
	Sold       bool    `json:"sold"`        // 是否已卖出
	SellPrice  float64 `json:"sell_price"`  // 卖出价（已卖出时有效）
	SellTime   string  `json:"sell_time"`   // 卖出时间（已卖出时有效）
}

// BuyResponse - 买点响应
type BuyResponse struct {
	Type    string    `json:"type"`    // 固定 "buy"
	Count   int       `json:"count"`   // 数量
	Time    string    `json:"time"`    // 刷新时间
	Results []BuyItem `json:"results"` // 列表
}

// SellItem - 卖出信号条目
type SellItem struct {
	Code       string  `json:"code"`        // 股票代码
	Name       string  `json:"name"`        // 股票名称
	BuyTime    string  `json:"buy_time"`    // 买入时间
	BuyPrice   float64 `json:"buy_price"`   // 买入价
	SellTime   string  `json:"sell_time"`   // 卖出时间
	SellPrice  float64 `json:"sell_price"`  // 卖出价
	ProfitRate float64 `json:"profit_rate"` // 收益率百分比
}

// SellResponse - 卖点响应
type SellResponse struct {
	Type    string     `json:"type"`    // 固定 "sell"
	Count   int        `json:"count"`   // 数量
	Time    string     `json:"time"`    // 刷新时间
	Results []SellItem `json:"results"` // 列表
}

// HistoryResponse - 历史买点响应（扁平结构，按时间倒序）
type HistoryResponse struct {
	Type    string    `json:"type"`    // 固定 "history"
	Time    string    `json:"time"`    // 刷新时间
	Total   int       `json:"total"`   // 总买点数量
	Results []BuyItem `json:"results"` // 所有历史买点，按时间倒序
}

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

// insertBuyPoints - 批量插入买点记录（先删除该日期旧记录再插入）
func insertBuyPoints(db *sql.DB, date string, buys []core.Buy) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	if _, err := tx.Exec("DELETE FROM buy_points WHERE buy_date = ?", date); err != nil {
		tx.Rollback()
		return err
	}
	if len(buys) == 0 {
		return tx.Commit()
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

// =========================================================
// 服务核心
// =========================================================

// ScreenService - 选股服务，管理买点历史、卖点判定和 WebSocket 推送
type ScreenService struct {
	mu               sync.RWMutex
	sellLookbackDays int
	db               *sql.DB
	historyBuys      []BuyPoint       // 从 DB 加载的历史买点（已收盘确认）
	todayBuys        []BuyItem        // 今日实时买点（仅内存，收盘后写入 DB）
	todayPersisted   bool             // 今日买点是否已持久化
	lastBuys         *BuyResponse     // 最新买点快照
	lastSells        *SellResponse    // 最新卖点快照
	lastHistory      *HistoryResponse // 最新历史买点快照
	subscribers      map[*fbr.Websocket]bool
}

func newScreenService(sellLookbackDays int) (*ScreenService, error) {
	db, err := initDB()
	if err != nil {
		return nil, err
	}

	svc := &ScreenService{
		sellLookbackDays: sellLookbackDays,
		db:               db,
		subscribers:      make(map[*fbr.Websocket]bool),
	}

	// 启动时立即从 DB 加载历史买点，确保新 WS 连接能拿到历史数据
	svc.reloadHistoryBuys()
	if len(svc.historyBuys) > 0 {
		svc.doScreenSells(nil)
		svc.doScreenHistory(nil)
	}

	return svc, nil
}

// addSubscriber - 添加订阅者
func (s *ScreenService) addSubscriber(ws *fbr.Websocket) {
	s.mu.Lock()
	s.subscribers[ws] = true
	s.mu.Unlock()
}

// removeSubscriber - 移除订阅者
func (s *ScreenService) removeSubscriber(ws *fbr.Websocket) {
	s.mu.Lock()
	delete(s.subscribers, ws)
	s.mu.Unlock()
}

// snapshot - 获取当前快照供新连接订阅时推送
func (s *ScreenService) snapshot() (*BuyResponse, *SellResponse, *HistoryResponse) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lastBuys, s.lastSells, s.lastHistory
}

// broadcast - 向所有订阅者广播消息
func (s *ScreenService) broadcast(payload any) {
	data, err := json.Marshal(payload)
	if err != nil {
		logs.Errf("[广播] 序列化失败: %v", err)
		return
	}
	msg := string(data)
	s.mu.RLock()
	defer s.mu.RUnlock()
	for ws := range s.subscribers {
		ws.WriteText(msg)
	}
}

// recentDates - 返回最近 N 个交易日（含今日，若今日为交易日）日期的字符串集合
func (s *ScreenService) recentDates(now time.Time) []string {
	n := s.sellLookbackDays
	if n <= 0 {
		n = 10
	}
	start := now.AddDate(0, 0, -(n + 30))
	end := time.Date(now.Year(), now.Month(), now.Day(), 23, 59, 59, 0, time.Local)

	dates := make([]string, 0, n)
	for t := range common.Manage.Workday.Iter(start, end, true) {
		dates = append(dates, t.Format(time.DateOnly))
		if len(dates) >= n {
			break
		}
	}
	return dates
}

// reloadHistoryBuys - 从 DB 重新加载历史买点到内存
func (s *ScreenService) reloadHistoryBuys() {
	dates := s.recentDates(time.Now())
	historyBuys, err := loadHistoryBuys(s.db, dates)
	if err != nil {
		logs.Errf("[DB] 重新加载历史买点失败: %v\n", err)
		return
	}
	s.historyBuys = historyBuys
}

// backfillHistory - 启动时回填最近 N 个交易日的买点（不含今日）
// 使用本地历史日线（收盘数据），今日买点由 doScreenBuys 用实时行情计算
func (s *ScreenService) backfillHistory() {
	now := time.Now()
	today := now.Format(time.DateOnly)
	dates := s.recentDates(now)

	// 1. 从 DB 加载已有历史
	s.mu.Lock()
	s.reloadHistoryBuys()
	s.mu.Unlock()

	// 2. 找出缺失的日期（DB 中没有记录的日期）
	var missing []string
	for _, date := range dates {
		if date == today {
			continue
		}
		found := false
		for _, bp := range s.historyBuys {
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
		if err := insertBuyPoints(s.db, date, coreBuys); err != nil {
			logs.Errf("[回填] %s 写入 DB 失败: %v", date, err)
			continue
		}
		logs.Infof("[回填] %s 选出 %d 只\n", date, len(buys))
	}

	// 重新从 DB 加载
	s.mu.Lock()
	s.reloadHistoryBuys()
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
	if err := insertBuyPoints(s.db, yesterday, coreBuys); err != nil {
		logs.Errf("[跨天刷新] 写入 DB 失败: %v\n", err)
		return
	}

	s.mu.Lock()
	s.reloadHistoryBuys()
	s.mu.Unlock()

	logs.Infof("[跨天刷新] %s 用收盘数据刷新，选出 %d 只\n", yesterday, len(buys))
}

// =========================================================
// 工具函数
// =========================================================

// toCoreBuys - 转换 *core.Buy -> core.Buy
func toCoreBuys(ls []*core.Buy) []core.Buy {
	out := make([]core.Buy, 0, len(ls))
	for _, b := range ls {
		if b != nil {
			out = append(out, *b)
		}
	}
	return out
}

// =========================================================
// 选股 / 卖点 / 历史推送
// =========================================================

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

// persistTodayBuys - 收盘后将今日买点写入 DB，成为历史买点
func (s *ScreenService) persistTodayBuys(now time.Time, buys []*core.Buy, quoteMap map[string]*protocol.Quote) {
	today := now.Format(time.DateOnly)
	coreBuys := toCoreBuys(buys)

	if err := insertBuyPoints(s.db, today, coreBuys); err != nil {
		logs.Errf("[收盘持久化] 写入 DB 失败: %v\n", err)
		return
	}

	s.mu.Lock()
	s.reloadHistoryBuys()
	s.todayPersisted = true
	s.mu.Unlock()

	logs.Infof("[收盘持久化] 今日买点 %d 只已写入 DB\n", len(coreBuys))

	// 新写入的历史买点也需要判定卖出
	s.doScreenSells(quoteMap)
	s.doScreenHistory(quoteMap)
}

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
	soldIDs := make([]int64, 0)

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
			// 已卖出：收益率用卖出价计算
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

// =========================================================
// 后台调度
// =========================================================

// startBackground - 启动后台定时任务
func (s *ScreenService) startBackground(interval time.Duration) {
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
		s.todayPersisted = false
		s.doScreenHistory(nil)
	}
}

// =========================================================
// 交易时间判断
// =========================================================

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

// =========================================================
// 入口
// =========================================================

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
		fbr.WithALL("/", func(c fbr.Ctx) {
			c.Set("Content-Type", "text/html; charset=utf-8")
			c.SendString(indexHTML)
		}),
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
