package screener

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/tdx/v2/protocol"
)

type HistoryMarket interface {
	Market
	MinuteKlines(code string, start, end time.Time) (protocol.Klines, error)
}

type Signal struct {
	Code         string   `json:"code"`
	Name         string   `json:"name"`
	Strategy     string   `json:"strategy"`
	BuyTime      string   `json:"buyTime"`
	BuyPrice     float64  `json:"buyPrice"`
	SellTime     string   `json:"sellTime,omitempty"`
	SellPrice    float64  `json:"sellPrice,omitempty"`
	SellSource   string   `json:"sellSource,omitempty"`
	LastPrice    float64  `json:"lastPrice,omitempty"`
	Incomplete   bool     `json:"incomplete,omitempty"`
	Rate         float64  `json:"rate"`
	Tags         []string `json:"tags,omitempty"`
	Legacy       bool     `json:"legacy,omitempty"`
	Tracked      bool     `json:"tracked,omitempty"`
	StrategyName string   `json:"strategyName,omitempty"`
}

type SignalKey struct{ Code, Strategy, Day string }

func (s Signal) Key() SignalKey {
	day, _, _ := strings.Cut(s.BuyTime, " ")
	return SignalKey{s.Code, s.Strategy, day}
}

type History struct {
	Rows        []Signal
	Through     string
	FailedCodes []string
}

type ReplayPosition struct {
	Signal Signal
	Seller core.Seller
}

type entry struct {
	row    Signal
	buy    core.Buy
	seller core.Seller
}

// Replay reconstructs independent daily buy signals over the selected window.
// Each signal tracks the first subsequent sell. Open signals are marked at the
// latest available close and remain open until a seller actually matches.
func Replay(ctx context.Context, m HistoryMarket, rules []Rule, at time.Time, lookback, workers int, tracked map[SignalKey]bool, pending map[string][]ReplayPosition, progress ScanProgress) (History, error) {
	at = at.In(protocol.Location())
	end := at
	if at.Hour() < 15 {
		end = at.AddDate(0, 0, -1)
	}
	codes, err := m.Codes()
	if err != nil {
		return History{}, err
	}
	codes = slices.Clone(codes)
	for code := range pending {
		codes = append(codes, code)
	}
	if len(codes) == 0 {
		return History{}, errors.New("行情库没有可选股票")
	}
	slices.Sort(codes)
	codes = slices.Compact(codes)
	benchmark, err := m.LatestDayDate(BenchmarkCode)
	if err != nil {
		return History{}, err
	}
	if benchmark == "" {
		return History{}, errors.New("上证指数日线为空")
	}
	if benchmark < end.Format(time.DateOnly) {
		end, err = time.ParseInLocation(time.DateOnly, benchmark, protocol.Location())
		if err != nil {
			return History{}, err
		}
	}
	end = core.DayOf(end).Add(15 * time.Hour)
	return replay(ctx, m, codes, rules, end, lookback, workers, tracked, pending, progress)
}

func replay(ctx context.Context, m HistoryMarket, codes []string, rules []Rule, end time.Time, lookback, workers int, tracked map[SignalKey]bool, pending map[string][]ReplayPosition, progress ScanProgress) (History, error) {
	type symbol struct {
		rows []Signal
		err  error
	}
	results := make([]symbol, len(codes))
	if err := processSymbols(ctx, codes, workers, progress, func(i int) {
		code := codes[i]
		results[i].rows, results[i].err = replaySymbol(m, code, rules, end, lookback, tracked, pending[code])
	}); err != nil {
		return History{}, err
	}

	history := History{Through: end.Format(time.DateOnly)}
	var first error
	for i, item := range results {
		if item.err != nil {
			history.FailedCodes = append(history.FailedCodes, codes[i])
			if first == nil {
				first = fmt.Errorf("%s: %w", codes[i], item.err)
			}
		}
		history.Rows = append(history.Rows, item.rows...)
	}
	if len(history.FailedCodes) == len(codes) && first != nil {
		return History{}, first
	}
	slices.Sort(history.FailedCodes)
	return history, nil
}

func replaySymbol(m HistoryMarket, code string, rules []Rule, end time.Time, lookback int, tracked map[SignalKey]bool, pending []ReplayPosition) ([]Signal, error) {
	start := end.AddDate(-6, 0, 0)
	var entries []entry
	for _, position := range pending {
		row := position.Signal
		buyAt, err := time.ParseInLocation(time.DateTime, row.BuyTime, protocol.Location())
		if err != nil {
			return nil, err
		}
		if buyAt.Before(start) {
			start = core.DayOf(buyAt)
		}
		entries = append(entries, entry{row, core.Buy{Code: code, Time: buyAt, Price: row.BuyPrice}, position.Seller})
	}
	bars, err := m.DayKlines(code, start, end)
	if err != nil || len(bars) == 0 {
		return nil, err
	}
	dailyCount := len(bars)
	name := m.Name(code)
	for i := max(0, dailyCount-lookback); i < dailyCount; i++ {
		day := bars[i]
		if day.Close <= 0 {
			continue
		}
		buyAt := core.DayOf(day.Time.In(protocol.Location())).Add(15 * time.Hour)
		for _, rule := range rules {
			if tracked[SignalKey{code, rule.ID, buyAt.Format(time.DateOnly)}] {
				continue
			}
			input := rule.Input(bars[:i+1])
			if rule.Buyer.Buy(code, input) {
				row := Signal{Code: code, Name: name, Strategy: rule.ID, StrategyName: rule.Name,
					BuyTime: buyAt.Format(time.DateTime), BuyPrice: day.Close, Tags: rule.tags(code, input), Tracked: true}
				entries = append(entries, entry{row, core.Buy{Code: code, Time: buyAt, Price: day.Close}, rule.Seller})
			}
		}
	}
	if len(entries) == 0 {
		return nil, nil
	}
	var from time.Time
	for _, entry := range entries {
		nextDay := core.DayOf(entry.buy.Time).AddDate(0, 0, 1)
		if entry.seller != nil && !nextDay.After(end) && (from.IsZero() || nextDay.Before(from)) {
			from = nextDay
		}
	}
	minutes := make(map[string]protocol.Klines)
	var minuteErr error
	if !from.IsZero() {
		series, err := m.MinuteKlines(code, from, end)
		minuteErr = err
		for _, bar := range series {
			if bar.Close > 0 && !bar.Time.Before(from) && !bar.Time.After(end) {
				date := bar.Time.In(protocol.Location()).Format(time.DateOnly)
				minutes[date] = append(minutes[date], bar)
			}
		}
		for _, series := range minutes {
			slices.SortStableFunc(series, func(a, b *protocol.Kline) int { return a.Time.Compare(b.Time) })
		}
	}
	last := bars[len(bars)-1].Close
	out := make([]Signal, 0, len(entries))
	for _, entry := range entries {
		row := entry.row
		row.LastPrice = last
		row.Incomplete = minuteErr != nil && entry.seller != nil
		if entry.seller != nil && minuteErr == nil {
			// Only minute bars can confirm an exit as the live worker would.
			usable := bars
			for i, day := range bars {
				if !core.DayOf(day.Time).After(core.DayOf(entry.buy.Time)) {
					continue
				}
				date := day.Time.Format(time.DateOnly)
				if len(minutes[date]) == 0 {
					usable = bars[:i]
					break
				}
			}
			if signal := core.GetSell(entry.seller, usable, entry.buy, minutes); signal != nil {
				row.SellTime, row.SellPrice, row.LastPrice = signal.Time.Format(time.DateTime), signal.Price, signal.Price
				row.SellSource = "minute"
			} else {
				row.Incomplete = len(usable) < len(bars)
			}
		}
		row.SetRate()
		out = append(out, row)
	}
	return out, minuteErr
}

func (s *Signal) SetRate() {
	price := s.LastPrice
	if s.SellTime != "" {
		price = s.SellPrice
	}
	if s.BuyPrice > 0 && price > 0 {
		rate := (price - s.BuyPrice) / s.BuyPrice * 100
		if !math.IsNaN(rate) && !math.IsInf(rate, 0) {
			s.Rate = rate
		}
	}
}

func SortSignals(rows []Signal) {
	slices.SortStableFunc(rows, func(a, b Signal) int {
		if order := strings.Compare(b.BuyTime, a.BuyTime); order != 0 {
			return order
		}
		if order := strings.Compare(a.Code, b.Code); order != 0 {
			return order
		}
		return strings.Compare(a.Strategy, b.Strategy)
	})
}
