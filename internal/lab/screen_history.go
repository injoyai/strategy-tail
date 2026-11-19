package lab

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	_ "github.com/glebarez/go-sqlite"
	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/internal/screener"
	"github.com/injoyai/tdx/v2/protocol"
)

var errScreenLedgerRead = errors.New("本地选股交易库读取失败")

func (s *screenService) runHistory(ctx context.Context, market screener.HistoryMarket, config screenConfig, strategy screenStrategy, at time.Time, lookback int, progress screener.ScanProgress) error {
	defer func() {
		s.mu.Lock()
		s.historyStrategy = ""
		s.mu.Unlock()
	}()
	history, pending, err := s.replayScreenHistory(ctx, market, config, strategy.ID, at, lookback, progress)
	if ctx.Err() != nil {
		return errStopped
	}
	if err != nil {
		log.Printf("[screen] 重建历史信号失败: %v", err)
		return errors.New("读取行情或计算历史信号失败，请重试。")
	}

	screener.SortSignals(history.Rows)
	s.mu.Lock()
	defer s.mu.Unlock()
	current, loadErr := s.load()
	if loadErr != nil || current.Version != config.Version {
		return errors.New("选股策略配置已变化，请重新计算历史信号。")
	}
	if err := saveScreenReplay(ctx, s.ledgerPath(), strategy, history, lookback, pending); err != nil {
		if ctx.Err() != nil {
			return errStopped
		}
		log.Printf("[screen] 保存历史回算结果失败: %v", err)
		return errors.New("保存历史回算结果失败，请重试。")
	}
	return nil
}

func (s *screenService) replayScreenHistory(ctx context.Context, market screener.HistoryMarket, config screenConfig, strategyID string, at time.Time, lookback int, progress screener.ScanProgress) (screener.History, map[screener.SignalKey]screenPosition, error) {
	trades, err := s.trackedScreenEntries(strategyID)
	if err != nil {
		return screener.History{}, nil, err
	}
	tracked := make(map[screener.SignalKey]bool, len(trades))
	pending := make(map[screener.SignalKey]screenPosition)
	byCode := make(map[string][]screener.ReplayPosition)
	for _, entry := range trades {
		key := entry.Signal.Key()
		tracked[key] = true
		if entry.Signal.SellTime != "" {
			continue
		}
		seller, err := screenEntrySeller(entry.Strategy)
		if err != nil {
			return screener.History{}, nil, err
		}
		if seller != nil {
			pending[key] = entry
			byCode[key.Code] = append(byCode[key.Code], screener.ReplayPosition{Signal: entry.Signal, Seller: seller})
		}
	}
	legacy, err := readLegacySignals(s.legacyPath)
	if err != nil {
		return screener.History{}, nil, err
	}
	for _, row := range legacy {
		if row.Strategy == strategyID {
			tracked[row.Key()] = true
		}
	}
	custom, err := compileScreen(config)
	if err != nil {
		return screener.History{}, nil, err
	}
	end := at
	if at.Hour() < 15 {
		end = at.AddDate(0, 0, -1)
	}
	rules, err := screener.BuildRules(market, at.AddDate(0, 0, -2*lookback), end, custom, strategyID)
	if err != nil {
		return screener.History{}, nil, err
	}
	history, err := screener.Replay(ctx, market, rules, at, lookback, 4, tracked, byCode, progress)
	return history, pending, err
}

type screenHistoryPage struct {
	Rows            []screener.Signal `json:"rows"`
	Total           int               `json:"total"`
	Page            int               `json:"page"`
	Pages           int               `json:"pages"`
	Through         string            `json:"through,omitempty"`
	Running         bool              `json:"running"`
	ArchiveError    string            `json:"archiveError,omitempty"`
	PriceError      string            `json:"priceError,omitempty"`
	FailedCodes     []string          `json:"failedCodes,omitempty"`
	Lookback        int               `json:"lookback"`
	DefaultLookback int               `json:"defaultLookback,omitempty"`
	AvgHoldingDays  float64           `json:"avgHoldingDays"`
	Stats           screenPerformance `json:"stats"`
}

func (s *Server) handleScreenHistory(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if mode := q.Get("mode"); mode != "" && mode != "sell" && mode != "history" {
		writeErr(w, http.StatusBadRequest, "无效的信号类型")
		return
	}
	page, _ := strconv.Atoi(q.Get("page"))
	page = max(1, page)
	status, mode := q.Get("status"), q.Get("mode")
	switch status {
	case "", "sold", "open":
	default:
		writeErr(w, http.StatusBadRequest, "无效的信号状态")
		return
	}
	tracked := q.Get("tracked")
	switch tracked {
	case "", "yes", "no", "all":
	default:
		writeErr(w, http.StatusBadRequest, "无效的记录来源")
		return
	}
	strategy := q.Get("strategy")
	rows, config, out, err := s.screen.historySnapshot(mode == "sell" || tracked == "yes", strategy)
	if err != nil {
		message := "无法读取选股配置"
		if errors.Is(err, errScreenLedgerRead) {
			message = errScreenLedgerRead.Error()
		}
		writeErr(w, http.StatusInternalServerError, message)
		return
	}
	code := strings.ToLower(strings.TrimSpace(q.Get("code")))
	tag := q.Get("tag")
	days, _ := strconv.Atoi(q.Get("days"))
	cutoff := ""
	now := s.screen.now().In(protocol.Location())
	if days > 0 {
		cutoff = now.AddDate(0, 0, -min(days, 3650)+1).Format(time.DateOnly)
	}
	matches := make([]screener.Signal, 0)
	var holdingDays int
	var closed int
	for _, row := range rows {
		if tracked == "yes" && !row.Tracked || tracked == "no" && row.Tracked {
			continue
		}
		if strategy != "" && strategy != row.Strategy || code != "" && !strings.Contains(row.Code, code) && !strings.Contains(strings.ToLower(row.Name), code) {
			continue
		}
		if mode == "sell" && !strings.HasPrefix(row.SellTime, now.Format(time.DateOnly)) || cutoff != "" && row.BuyTime < cutoff {
			continue
		}
		if status == "sold" && row.SellTime == "" || status == "open" && row.SellTime != "" {
			continue
		}
		if tag != "" && !containsScreenTag(row, tag) {
			continue
		}
		if row.Name == "" {
			row.Name = s.screen.market.Name(row.Code)
		}
		matches = append(matches, row)
		if row.SellTime != "" {
			buy, buyErr := time.Parse(time.DateOnly, row.BuyTime[:10])
			sell, sellErr := time.Parse(time.DateOnly, row.SellTime[:10])
			trade := core.Trade{BuyPrice: row.BuyPrice, SellPrice: row.SellPrice, BuyTime: buy, SellTime: sell}
			closed++
			if buyErr == nil && sellErr == nil {
				holdingDays += trade.HoldingDays()
			}
		}
	}
	out.PriceError = s.screen.markScreenPositions(r.Context(), matches, now)
	out.Stats = summarizeScreenPerformance(matches)
	if closed > 0 {
		out.AvgHoldingDays = float64(holdingDays) / float64(closed)
	}
	out.Total = len(matches)
	out.Rows, out.Page, out.Pages = matches, 1, 1
	if mode != "history" || q.Has("page") {
		const pageSize = 30
		out.Pages = max(1, (out.Total+pageSize-1)/pageSize)
		out.Page = min(page, out.Pages)
		from := (out.Page - 1) * pageSize
		out.Rows = matches[from:min(from+pageSize, out.Total)]
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Screen-Config-Version", strconv.Itoa(config.Version))
	writeJSON(w, out)
}

func containsScreenTag(row screener.Signal, tag string) bool {
	for _, item := range row.Tags {
		if item == tag {
			return true
		}
	}
	return false
}

func (s *Server) handleScreenHistoryRun(w http.ResponseWriter, r *http.Request) {
	screen := s.screen
	screen.mu.Lock()
	defer screen.mu.Unlock()
	q := r.URL.Query()
	lookback := screen.lookback
	if q.Has("lookback") {
		days, err := strconv.Atoi(q.Get("lookback"))
		if err != nil || days < 1 || days > maxScreenHistoryLookback {
			writeErr(w, http.StatusBadRequest, "回看天数须为 1 至 "+strconv.Itoa(maxScreenHistoryLookback)+" 个交易日")
			return
		}
		lookback = days
	}
	strategyIDs := q["strategy"]
	if len(strategyIDs) != 1 || strings.TrimSpace(strategyIDs[0]) == "" {
		writeErr(w, http.StatusBadRequest, "请选择一套选股策略")
		return
	}
	config, err := screen.load()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "无法读取选股配置")
		return
	}
	index := slices.IndexFunc(config.Strategies, func(strategy screenStrategy) bool {
		return strategy.ID == strategyIDs[0]
	})
	if index < 0 {
		writeErr(w, http.StatusBadRequest, "选股策略不存在，请重新选择")
		return
	}
	strategy := config.Strategies[index]
	if screen.historyStrategy != "" {
		writeErr(w, http.StatusConflict, "历史信号正在计算")
		return
	}
	market, ok := screen.market.(screener.HistoryMarket)
	if !ok {
		writeErr(w, http.StatusBadRequest, "请先连接行情库")
		return
	}
	if !s.runner.mu.TryLock() {
		writeErr(w, http.StatusConflict, "已有批量任务在运行")
		return
	}
	screen.historyStrategy = strategy.ID
	at := screen.now().In(protocol.Location())
	const task = "screen_history"
	ctl := s.runner.beginTask(taskStart{kind: task, phase: "preparing"})
	s.runner.launchTask(ctl, "idle", func(stop chan struct{}) error {
		ctx, cancel := screenTaskContext(stop)
		defer cancel()
		return screen.runHistory(ctx, market, config, strategy, at, lookback, s.runner.screenProgress(task))
	})
	writeJSON(w, map[string]bool{"running": true})
}

func (s *screenService) historySnapshot(localOnly bool, strategyID string) ([]screener.Signal, screenConfig, screenHistoryPage, error) {
	s.mu.Lock()
	config, err := s.load()
	out := screenHistoryPage{}
	if !localOnly {
		out.Running = s.historyStrategy != "" && (strategyID == "" || s.historyStrategy == strategyID)
		out.Lookback, out.DefaultLookback = s.lookback, s.lookback
	}
	s.mu.Unlock()
	if err != nil {
		return nil, config, out, err
	}

	rows, archiveError, err := s.readScreenHistoryRows(localOnly)
	if err != nil {
		return nil, config, out, err
	}
	out.ArchiveError = archiveError
	if !localOnly {
		replay, err := readScreenReplay(s.ledgerPath(), strategyID)
		if err != nil {
			log.Printf("[screen] 读取历史回算结果失败: %v", err)
			out.ArchiveError = strings.TrimSpace(out.ArchiveError + " 历史回算结果读取失败。")
		} else if replay != nil {
			out.Through, out.FailedCodes, out.Lookback = replay.Through, replay.FailedCodes, replay.Lookback
		}
	}
	screener.SortSignals(rows)
	return rows, config, out, nil
}

func (s *screenService) readScreenHistoryRows(localOnly bool) ([]screener.Signal, string, error) {
	trades, err := s.trackedScreenEntries("")
	if err != nil {
		log.Printf("[screen] 读取本地交易库失败: %v", err)
		return nil, "", errScreenLedgerRead
	}
	rows := make([]screener.Signal, 0, len(trades))
	tracked := make(map[screener.SignalKey]bool, len(trades))
	for _, entry := range trades {
		row := entry.Signal
		tracked[row.Key()] = true
		rows = append(rows, row)
	}
	if localOnly {
		return rows, "", nil
	}
	legacy, err := readLegacySignals(s.legacyPath)
	if err != nil {
		log.Printf("[screen] 读取旧交易库失败: %v", err)
		return rows, "原 Screen 历史库读取失败。", nil
	}
	for _, row := range legacy {
		key := row.Key()
		if tracked[key] {
			continue
		}
		rows = append(rows, row)
	}
	return rows, "", nil
}

func readLegacySignals(path string) ([]screener.Signal, error) {
	db, err := openExistingScreenDB(path)
	if err != nil || db == nil {
		return nil, err
	}
	defer db.Close()
	rows, err := db.Query(`SELECT Code, COALESCE(Name,''), Strategy, BuyTime, BuyPrice,
		COALESCE(Sold,0), COALESCE(SellTime,''), COALESCE(SellPrice,0), COALESCE(Tags,'') FROM Trade`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []screener.Signal
	for rows.Next() {
		var row screener.Signal
		var sold bool
		var tags string
		if err := rows.Scan(&row.Code, &row.Name, &row.Strategy, &row.BuyTime, &row.BuyPrice,
			&sold, &row.SellTime, &row.SellPrice, &tags); err != nil {
			return nil, err
		}
		if _, err := time.Parse(time.DateTime, row.BuyTime); err != nil || row.BuyPrice <= 0 {
			continue
		}
		if sold {
			if _, err := time.Parse(time.DateTime, row.SellTime); err != nil || row.SellPrice <= 0 {
				continue
			}
			row.SellSource = "legacy"
		} else {
			row.LastPrice, row.SellPrice, row.SellTime = row.SellPrice, 0, ""
		}
		if tags != "" {
			_ = json.Unmarshal([]byte(tags), &row.Tags)
		}
		row.Legacy = true
		row.SetRate()
		out = append(out, row)
	}
	return out, rows.Err()
}
