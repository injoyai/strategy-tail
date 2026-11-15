package lab

import (
	"cmp"
	"context"
	"errors"
	"log"
	"math"
	"net/http"
	"slices"
	"strings"
	"time"

	common "github.com/injoyai/strategy-tail"
	"github.com/injoyai/strategy-tail/internal/marketstore"
	"github.com/injoyai/tdx/v2/protocol"
)

const dashboardCalendarIndex = "sh000001"
const dashboardMaxRank = 50

type dashboardIndexQuote struct {
	Code   string   `json:"code"`
	Name   string   `json:"name"`
	Date   string   `json:"date"`
	Close  float64  `json:"close,omitempty"`
	Change *float64 `json:"change"`
}

var dashboardIndexList = [...]dashboardIndexQuote{
	{Code: dashboardCalendarIndex, Name: "上证指数"},
	{Code: "sz399001", Name: "深证成指"},
	{Code: "sz399006", Name: "创业板指"},
	{Code: "sh000688", Name: "科创50"},
	{Code: "sh000300", Name: "沪深300"},
	{Code: "sh000905", Name: "中证500"},
	{Code: "sh000852", Name: "中证1000"},
	{Code: "bj899050", Name: "北证50"},
}

type dashboardStockQuote struct {
	Code   string   `json:"code"`
	Close  float64  `json:"close,omitempty"`
	Change *float64 `json:"change"`
	Amount float64  `json:"amount,omitempty"`
}

type dashboardBreadth struct {
	Total        int     `json:"total"`
	Up           int     `json:"up"`
	Flat         int     `json:"flat"`
	Down         int     `json:"down"`
	Unclassified int     `json:"unclassified"`
	Amount       float64 `json:"amount"`
	WithAmount   int     `json:"withAmount"`
}

type dashboardExchange struct {
	Exchange string `json:"exchange"`
	dashboardBreadth
}

type dashboardRankings struct {
	Amount []dashboardStockQuote `json:"amount"`
	Up     []dashboardStockQuote `json:"up"`
	Down   []dashboardStockQuote `json:"down"`
}

type dashboardStockSummary struct {
	dashboardBreadth
	Exchanges []dashboardExchange `json:"exchanges"`
	Rankings  dashboardRankings   `json:"rankings"`
}

type marketDashboard struct {
	Date          string                `json:"date"`
	PreviousDate  string                `json:"previousDate"`
	Indexes       []dashboardIndexQuote `json:"indexes"`
	Stocks        dashboardStockSummary `json:"stocks"`
	Source        string                `json:"source"`
	Phase         string                `json:"phase"`
	CapturedAt    string                `json:"capturedAt,omitempty"`
	Notice        string                `json:"notice,omitempty"`
	RefreshAfter  int64                 `json:"refreshAfterMs"`
	StockQuotes   int                   `json:"stockQuotes,omitempty"`
	StockUniverse int                   `json:"stockUniverse,omitempty"`
}

func (s *Server) handleMarketDashboard(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if common.Pull == nil {
		writeErr(w, http.StatusServiceUnavailable, "行情库尚未连接")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	at := time.Now()
	snapshot, err := s.dashboard.viewAt(ctx, at, readDashboardTDX, readDashboardDaily)
	if err != nil {
		if r.Context().Err() == nil {
			log.Printf("[lab] 读取数据看板: %v", err)
			writeErr(w, http.StatusServiceUnavailable, "行情读取失败，请重试")
		}
		return
	}
	snapshot.RefreshAfter = max(1000, snapshot.RefreshAfter-time.Since(at).Milliseconds())
	writeJSON(w, snapshot)
}

func (c *dashboardLiveCache) viewAt(ctx context.Context, at time.Time, live dashboardLiveReader,
	daily func(context.Context, string) (marketDashboard, error)) (marketDashboard, error) {
	at = at.In(protocol.Location())
	today := at.Format(time.DateOnly)
	phase, next := dashboardSchedule(at)
	wait := next.Sub(at)
	var view, intraday marketDashboard
	var liveErr error
	if phase == "trading" {
		intraday, liveErr = c.load(ctx, at, live)
		wait = min(wait, dashboardRefresh)
		if intraday.Date != "" {
			view = intraday
			if liveErr != nil {
				view.Notice = "实时行情暂未更新，显示上次采集的盘中快照。"
			}
		}
	} else {
		intraday, _ = c.latest(today)
		if phase == "break" {
			view = intraday
		}
	}
	if view.Date == "" {
		var err error
		view, err = daily(ctx, today)
		if err != nil && intraday.Date == "" {
			return marketDashboard{}, err
		}
		if phase == "closed" && intraday.Date != "" && view.Date < today {
			view = intraday
			view.Notice = "显示最近的盘中快照，等待当日日线入库。"
			wait = min(wait, time.Minute)
		}
		if phase == "trading" {
			view.Notice = "实时行情读取失败，显示最近入库日线。"
			if errors.Is(liveErr, errDashboardNoRecentMinute) {
				view.Notice = "等待本交易时段的报价，显示最近入库日线。"
			}
		}
	}
	view.Phase = phase
	view.RefreshAfter = wait.Milliseconds()
	return view, nil
}

func dashboardCodes() []string {
	indexes := make([]string, 0, len(dashboardIndexList))
	for _, index := range dashboardIndexList {
		indexes = append(indexes, index.Code)
	}
	return indexes
}

func readDashboardDaily(ctx context.Context, through string) (marketDashboard, error) {
	days, err := common.Pull.ReadDashboardDays(ctx, dashboardCalendarIndex, dashboardCodes(), through)
	if err != nil {
		return marketDashboard{}, err
	}

	snapshot := marketDashboard{
		Source:  "daily",
		Indexes: dashboardIndexQuotes(days.Indexes),
		Stocks:  summarizeDashboardStocks(days.Stocks),
	}
	if dates := days.Indexes[dashboardCalendarIndex]; len(dates) > 0 {
		snapshot.Date = dates[0].Date
		if len(dates) > 1 {
			snapshot.PreviousDate = dates[1].Date
		}
	}
	return snapshot, nil
}

func dashboardIndexQuotes(bars map[string][]marketstore.DashboardIndexDay) []dashboardIndexQuote {
	indexes := dashboardIndexList
	for i, index := range indexes {
		days := bars[index.Code]
		if len(days) == 0 {
			continue
		}
		index.Date = days[0].Date
		if validDashboardValue(days[0].Close) {
			index.Close = days[0].Close
		}
		if len(days) > 1 {
			index.Change = dashboardChange(index.Close, days[1].Close)
		}
		indexes[i] = index
	}
	return indexes[:]
}

func validDashboardValue(value float64) bool {
	return value > 0 && !math.IsInf(value, 0)
}

func dashboardChange(close, previous float64) *float64 {
	if !validDashboardValue(close) || !validDashboardValue(previous) {
		return nil
	}
	value := (close - previous) / previous * 100
	if math.IsInf(value, 0) {
		return nil
	}
	return &value
}

func summarizeDashboardStocks(rows []marketstore.DashboardStockDay) dashboardStockSummary {
	summary := dashboardStockSummary{
		Exchanges: []dashboardExchange{{Exchange: "sh"}, {Exchange: "sz"}, {Exchange: "bj"}},
		Rankings:  dashboardRankings{Amount: []dashboardStockQuote{}, Up: []dashboardStockQuote{}, Down: []dashboardStockQuote{}},
	}
	byExchange := make(map[string]*dashboardBreadth, len(summary.Exchanges))
	for i := range summary.Exchanges {
		item := &summary.Exchanges[i]
		byExchange[item.Exchange] = &item.dashboardBreadth
	}
	for _, bar := range rows {
		if !protocol.IsStock(bar.Code) {
			continue
		}
		quote := dashboardStockQuote{Code: bar.Code, Change: dashboardChange(bar.Close, bar.Previous)}
		if validDashboardValue(bar.Close) {
			quote.Close = bar.Close
		}
		if validDashboardValue(bar.Amount) {
			quote.Amount = bar.Amount
			summary.Rankings.Amount = append(summary.Rankings.Amount, quote)
		}
		if quote.Change != nil {
			switch {
			case *quote.Change > 0:
				summary.Rankings.Up = append(summary.Rankings.Up, quote)
			case *quote.Change < 0:
				summary.Rankings.Down = append(summary.Rankings.Down, quote)
			}
		}
		summary.dashboardBreadth.add(quote)
		byExchange[strings.ToLower(bar.Code[:2])].add(quote)
	}
	summary.Rankings.Amount = topDashboardStocks(summary.Rankings.Amount, func(a, b dashboardStockQuote) int { return cmp.Compare(b.Amount, a.Amount) })
	summary.Rankings.Up = topDashboardStocks(summary.Rankings.Up, func(a, b dashboardStockQuote) int { return cmp.Compare(*b.Change, *a.Change) })
	summary.Rankings.Down = topDashboardStocks(summary.Rankings.Down, func(a, b dashboardStockQuote) int { return cmp.Compare(*a.Change, *b.Change) })
	return summary
}

func (b *dashboardBreadth) add(quote dashboardStockQuote) {
	b.Total++
	if quote.Amount > 0 {
		b.Amount += quote.Amount
		b.WithAmount++
	}
	switch {
	case quote.Change == nil:
		b.Unclassified++
	case *quote.Change > 0:
		b.Up++
	case *quote.Change < 0:
		b.Down++
	default:
		b.Flat++
	}
}

func topDashboardStocks(rows []dashboardStockQuote, compare func(dashboardStockQuote, dashboardStockQuote) int) []dashboardStockQuote {
	slices.SortFunc(rows, func(a, b dashboardStockQuote) int {
		return cmp.Or(compare(a, b), strings.Compare(a.Code, b.Code))
	})
	return rows[:min(len(rows), dashboardMaxRank)]
}
