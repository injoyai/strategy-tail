package lab

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"
	"time"

	common "github.com/injoyai/strategy-tail"
	"github.com/injoyai/strategy-tail/internal/marketstore"
	tdx "github.com/injoyai/tdx/v2"
	"github.com/injoyai/tdx/v2/protocol"
)

type dashboardSectorQuote struct {
	Code        string  `json:"code"`
	Name        string  `json:"name"`
	Change      float64 `json:"change"`
	FiveDayRank *int    `json:"fiveDayRank,omitempty"`
	FiveDaySum  *int    `json:"fiveDaySum,omitempty"`
}

type dashboardSectorSnapshot struct {
	Date       string                 `json:"date"`
	Industries []dashboardSectorQuote `json:"industries"`
	Concepts   []dashboardSectorQuote `json:"concepts"`
	Expected   int                    `json:"expected"`
	Received   int                    `json:"received"`
	Notice     string                 `json:"notice,omitempty"`
}

type dashboardSectorFeed interface {
	dashboardQuoteFeed
	GetTdxZs() ([]*protocol.TdxZs, error)
}

type dashboardSector struct {
	name     string
	industry bool
}

type dashboardSectorService struct {
	cache dashboardSnapshotCache[dashboardSectorSnapshot]
	// Only the cache's single refresh worker reads or replaces the catalog.
	catalogDay   string
	catalog      map[string]dashboardSector
	historyDate  string
	historyAt    time.Time
	historyDates []string
	historyRows  []marketstore.SectorDayClose
}

func (s *Server) handleDashboardSectors(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	at := time.Now().In(protocol.Location())
	phase, next := dashboardSchedule(at)
	period := dashboardRefresh
	if phase != "trading" {
		period = 5 * time.Minute
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	view, err := s.sectors.cache.loadWithInterval(ctx, at, min(period, next.Sub(at)), s.sectors.readTDX)
	if r.Context().Err() != nil {
		return
	}
	if view.Date == "" {
		writeErr(w, http.StatusServiceUnavailable, "板块行情暂不可用")
		return
	}
	if err != nil {
		view.Notice = "板块行情暂未更新，显示上次报价。"
	}
	writeJSON(w, view)
}

func (s *dashboardSectorService) readTDX(ctx context.Context, at time.Time) (dashboardSectorSnapshot, error) {
	client := tdx.NewClient(tdx.WithLevel(tdx.LevelNone))
	defer client.Close()
	view, err := s.read(ctx, tdx.WithContext(ctx, client), at)
	if err != nil {
		return view, err
	}
	if common.Pull == nil {
		view.Notice = "五日排名暂不可用：行情库未连接。"
		return view, nil
	}
	if err := s.refreshSectorHistory(ctx, view.Date, at, common.Pull.SectorHistory); err != nil {
		view.Notice = "五日排名暂不可用：历史日线读取失败或交易日不足。"
		return view, nil
	}
	applySectorFiveDay(&view, s.catalog, s.historyDates, s.historyRows)
	if !hasSectorFiveDayRank(view.Industries) && !hasSectorFiveDayRank(view.Concepts) {
		view.Notice = "五日排名暂无完整历史数据，补齐后将自动更新。"
	}
	return view, nil
}

func (s *dashboardSectorService) refreshSectorHistory(ctx context.Context, date string, at time.Time, read func(context.Context, string) ([]string, []marketstore.SectorDayClose, error)) error {
	if s.historyDate == date && !at.Before(s.historyAt) && at.Sub(s.historyAt) < 5*time.Minute {
		return nil
	}
	dates, rows, err := read(ctx, date)
	if err != nil {
		return err
	}
	s.historyDate, s.historyAt, s.historyDates, s.historyRows = date, at, dates, rows
	return nil
}

func hasSectorFiveDayRank(rows []dashboardSectorQuote) bool {
	for _, row := range rows {
		if row.FiveDayRank != nil {
			return true
		}
	}
	return false
}

func (s *dashboardSectorService) read(ctx context.Context, feed dashboardSectorFeed, at time.Time) (dashboardSectorSnapshot, error) {
	at = at.In(protocol.Location())
	date, err := dashboardSectorDate(feed, at)
	if err != nil {
		return dashboardSectorSnapshot{}, err
	}
	day := at.Format(time.DateOnly)
	if s.catalogDay != day {
		catalog, err := feed.GetTdxZs()
		if err != nil {
			return dashboardSectorSnapshot{}, fmt.Errorf("读取通达信板块目录: %w", err)
		}
		s.catalog = dashboardSectorCatalog(catalog)
		if len(s.catalog) == 0 {
			return dashboardSectorSnapshot{}, errors.New("通达信板块目录为空")
		}
		s.catalogDay = day
	}

	view := dashboardSectorSnapshot{
		Date: date, Expected: len(s.catalog),
		Industries: []dashboardSectorQuote{}, Concepts: []dashboardSectorQuote{},
	}
	for batch := range slices.Chunk(slices.Sorted(maps.Keys(s.catalog)), 80) {
		if err := ctx.Err(); err != nil {
			return dashboardSectorSnapshot{}, err
		}
		quotes, err := feed.GetQuote(batch...)
		if err != nil {
			continue
		}
		for _, quote := range quotes {
			code := dashboardQuoteCode(quote)
			sector, ok := s.catalog[code]
			change := dashboardChange(quote.Price, quote.PreClose)
			if !ok || change == nil {
				continue
			}
			row := dashboardSectorQuote{Code: code, Name: sector.name, Change: *change}
			if sector.industry {
				view.Industries = append(view.Industries, row)
			} else {
				view.Concepts = append(view.Concepts, row)
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return dashboardSectorSnapshot{}, err
	}
	view.Received = len(view.Industries) + len(view.Concepts)
	if view.Received == 0 {
		return dashboardSectorSnapshot{}, errors.New("未收到有效的板块指数报价")
	}
	slices.SortFunc(view.Industries, compareDashboardSectors)
	slices.SortFunc(view.Concepts, compareDashboardSectors)
	return view, nil
}

func dashboardSectorDate(feed dashboardSectorFeed, at time.Time) (string, error) {
	bars, err := feed.GetKlineMinute(dashboardCalendarIndex, 0, 1)
	if err != nil {
		return "", fmt.Errorf("确认板块行情日期: %w", err)
	}
	if bars == nil || len(bars.List) == 0 {
		return "", errDashboardNoRecentMinute
	}
	if phase, _ := dashboardSchedule(at); phase == "trading" && !dashboardHasRecentMinute(at, bars) {
		return "", errDashboardNoRecentMinute
	}
	bar := bars.List[len(bars.List)-1]
	stamp, err := time.ParseInLocation(protocol.TimeLayout, bar.Time, at.Location())
	if err != nil || !validDashboardValue(bar.Close) || stamp.After(at.Truncate(time.Minute).Add(time.Minute)) {
		return "", errDashboardNoRecentMinute
	}
	return stamp.Format(time.DateOnly), nil
}

func dashboardSectorCatalog(rows []*protocol.TdxZs) map[string]dashboardSector {
	catalog := make(map[string]dashboardSector)
	for _, row := range rows {
		// Type 2 identifies industry indices. The provider's themed concept
		// list is type 4/subtype 2; other lists also contain style baskets.
		industry := row.Type == 2
		if !industry && (row.Type != 4 || row.SubType != 2) {
			continue
		}
		instrument, err := protocol.ParseInstrument("sh" + row.Code)
		if err != nil {
			continue
		}
		market, err := instrument.Market()
		name := strings.TrimSpace(row.Name)
		if err != nil || market != protocol.MarketBlock || name == "" {
			continue
		}
		code := instrument.Exchange.String() + instrument.Code
		if _, exists := catalog[code]; !exists {
			catalog[code] = dashboardSector{name: name, industry: industry}
		}
	}
	return catalog
}

func compareDashboardSectors(a, b dashboardSectorQuote) int {
	return cmp.Or(cmp.Compare(b.Change, a.Change), strings.Compare(a.Code, b.Code))
}

// Historical rankings use the current category catalog; today's rank is the live ordering.
func applySectorFiveDay(view *dashboardSectorSnapshot, catalog map[string]dashboardSector, dates []string, rows []marketstore.SectorDayClose) {
	if len(dates) != 5 {
		return
	}
	closes := make(map[string]map[string]float64, len(dates))
	for _, row := range rows {
		if closes[row.Date] == nil {
			closes[row.Date] = map[string]float64{}
		}
		closes[row.Date][row.Code] = row.Close
	}
	sums := map[string]int{}
	counts := map[string]int{}
	for i := 1; i < len(dates); i++ {
		var industry, concept []dashboardSectorQuote
		for code, info := range catalog {
			prev, now := closes[dates[i-1]][code], closes[dates[i]][code]
			change := dashboardChange(now, prev)
			if change == nil {
				continue
			}
			row := dashboardSectorQuote{Code: code, Change: *change}
			if info.industry {
				industry = append(industry, row)
			} else {
				concept = append(concept, row)
			}
		}
		for _, group := range [][]dashboardSectorQuote{industry, concept} {
			slices.SortFunc(group, compareDashboardSectors)
			for j, row := range group {
				sums[row.Code] += j + 1
				counts[row.Code]++
			}
		}
	}
	for _, group := range [][]dashboardSectorQuote{view.Industries, view.Concepts} {
		type score struct {
			index, sum int
			code       string
		}
		var scores []score
		for i := range group {
			code := group[i].Code
			if counts[code] != 4 {
				continue
			}
			sum := sums[code] + i + 1
			group[i].FiveDaySum = new(int)
			*group[i].FiveDaySum = sum
			scores = append(scores, score{index: i, sum: sum, code: code})
		}
		slices.SortFunc(scores, func(a, b score) int { return cmp.Or(cmp.Compare(a.sum, b.sum), strings.Compare(a.code, b.code)) })
		for i, v := range scores {
			rank := i + 1
			if i > 0 && v.sum == scores[i-1].sum {
				rank = *group[scores[i-1].index].FiveDayRank
			}
			group[v.index].FiveDayRank = &rank
		}
	}
}
