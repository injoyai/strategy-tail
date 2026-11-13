package lab

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	_ "github.com/glebarez/go-sqlite"
	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/internal/screener"
	"github.com/injoyai/tdx/v2/protocol"
)

func (s *screenService) runHistory(ctx context.Context, market screener.HistoryMarket, config screenConfig, at time.Time, lookback int, progress screener.ScanProgress) error {
	defer func() {
		s.mu.Lock()
		s.historyRunning = false
		s.mu.Unlock()
	}()
	history, pending, err := s.replayScreenHistory(ctx, market, config, at, lookback, progress)
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
	if err := saveScreenReplay(ctx, s.ledgerPath(), config, history, lookback, pending); err != nil {
		if ctx.Err() != nil {
			return errStopped
		}
		log.Printf("[screen] 保存历史回算结果失败: %v", err)
		return errors.New("保存历史回算结果失败，请重试。")
	}
	return nil
}

func (s *screenService) replayScreenHistory(ctx context.Context, market screener.HistoryMarket, config screenConfig, at time.Time, lookback int, progress screener.ScanProgress) (screener.History, map[screener.SignalKey]screenPosition, error) {
	trades, err := s.trackedScreenEntries()
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
		tracked[row.Key()] = true
	}
	presets, custom, err := compileScreen(config)
	if err != nil {
		return screener.History{}, nil, err
	}
	end := at
	if at.Hour() < 15 {
		end = at.AddDate(0, 0, -1)
	}
	rules, err := screener.BuildRules(market, at.AddDate(0, 0, -2*lookback), end, presets, custom)
	if err != nil {
		return screener.History{}, nil, err
	}
	history, err := screener.Replay(ctx, market, rules, at, lookback, 4, tracked, byCode, progress)
	return history, pending, err
}

type screenHistoryPage struct {
	Rows           []screener.Signal `json:"rows"`
	Total          int               `json:"total"`
	Page           int               `json:"page"`
	Pages          int               `json:"pages"`
	Through        string            `json:"through,omitempty"`
	Running        bool              `json:"running"`
	ArchiveError   string            `json:"archiveError,omitempty"`
	FailedCodes    []string          `json:"failedCodes,omitempty"`
	Lookback       int               `json:"lookback"`
	AvgHoldingDays float64           `json:"avgHoldingDays"`
	Stats          TradeStatsJSON    `json:"stats"`
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
	rows, config, out, err := s.screen.historySnapshot(mode == "sell" || tracked == "yes")
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "无法读取选股配置")
		return
	}
	strategy := q.Get("strategy")
	code := strings.ToLower(strings.TrimSpace(q.Get("code")))
	tag := q.Get("tag")
	days, _ := strconv.Atoi(q.Get("days"))
	cutoff := ""
	now := s.screen.now().In(protocol.Location())
	if days > 0 {
		cutoff = now.AddDate(0, 0, -min(days, 3650)+1).Format(time.DateOnly)
	}
	matches := make([]screener.Signal, 0)
	closed := make([]core.Trade, 0)
	var holdingDays int
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
		if status == "sold" && row.SellTime == "" || status == "open" && (row.SellTime != "" || row.Incomplete) {
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
			closed = append(closed, trade)
			if buyErr == nil && sellErr == nil {
				holdingDays += trade.HoldingDays()
			}
		}
	}
	out.Stats = newTradeStatsJSON(core.Stats(closed))
	if len(closed) > 0 {
		out.AvgHoldingDays = float64(holdingDays) / float64(len(closed))
	}
	out.Total = len(matches)
	const pageSize = 30
	out.Pages = max(1, (out.Total+pageSize-1)/pageSize)
	out.Page = min(page, out.Pages)
	from := (out.Page - 1) * pageSize
	out.Rows = matches[from:min(from+pageSize, out.Total)]
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
	config, err := screen.load()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "无法读取选股配置")
		return
	}
	if screen.historyRunning {
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
	screen.historyRunning = true
	at, lookback := screen.now().In(protocol.Location()), screen.lookback
	const task = "screen_history"
	ctl := s.runner.beginTask(taskStart{kind: task, phase: "preparing"})
	s.runner.launchTask(ctl, "idle", func(stop chan struct{}) error {
		ctx, cancel := screenTaskContext(stop)
		defer cancel()
		return screen.runHistory(ctx, market, config, at, lookback, s.runner.screenProgress(task))
	})
	writeJSON(w, map[string]bool{"running": true})
}

func (s *screenService) historySnapshot(localOnly bool) ([]screener.Signal, screenConfig, screenHistoryPage, error) {
	s.mu.Lock()
	config, err := s.load()
	out := screenHistoryPage{}
	if !localOnly {
		out.Running, out.Lookback = s.historyRunning, s.lookback
	}
	s.mu.Unlock()
	if err != nil {
		return nil, config, out, err
	}

	trades, err := s.trackedScreenEntries()
	if err != nil {
		log.Printf("[screen] 读取本地交易库失败: %v", err)
		out.ArchiveError = "本地选股交易库读取失败。"
	}
	if localOnly {
		rows := make([]screener.Signal, 0, len(trades))
		for _, entry := range trades {
			rows = append(rows, entry.Signal)
		}
		screener.SortSignals(rows)
		return rows, config, out, nil
	}
	replay, err := readScreenReplay(s.ledgerPath())
	if err != nil {
		log.Printf("[screen] 读取历史回算结果失败: %v", err)
		out.ArchiveError = strings.TrimSpace(out.ArchiveError + " 历史回算结果读取失败。")
	} else if replay != nil {
		out.Through, out.FailedCodes, out.Lookback = replay.Through, replay.FailedCodes, replay.Lookback
	}
	legacy, err := readLegacySignals(s.legacyPath)
	if err != nil {
		log.Printf("[screen] 读取旧交易库失败: %v", err)
		out.ArchiveError = strings.TrimSpace(out.ArchiveError + " 原 Screen 历史库读取失败。")
	}
	rows := make([]screener.Signal, 0, len(legacy)+len(trades))
	tracked := make(map[screener.SignalKey]bool, len(trades))
	for _, entry := range trades {
		row := entry.Signal
		tracked[row.Key()] = true
		rows = append(rows, row)
	}
	for _, row := range legacy {
		key := row.Key()
		if tracked[key] {
			continue
		}
		rows = append(rows, row)
	}
	screener.SortSignals(rows)
	return rows, config, out, nil
}

func readLegacySignals(path string) ([]screener.Signal, error) {
	if path == "" {
		return nil, nil
	}
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	location := url.URL{Scheme: "file", Path: path, RawQuery: "mode=ro"}
	db, err := sql.Open("sqlite", location.String())
	if err != nil {
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
