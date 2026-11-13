package lab

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"math"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/internal/lab/builder"
	"github.com/injoyai/strategy-tail/internal/screener"
	"github.com/injoyai/strategy-tail/lib/extend"
	tdx "github.com/injoyai/tdx/v2"
	"github.com/injoyai/tdx/v2/protocol"
)

type screenLiveFeed struct{ client tdx.Client }

type screenCloseFeed interface {
	minutes(context.Context, string, time.Time) (protocol.Klines, error)
	quotes(context.Context, []string, time.Time) (map[string]*protocol.Kline, error)
}

func (f screenLiveFeed) minutes(ctx context.Context, code string, at time.Time) (protocol.Klines, error) {
	data, err := tdx.WithContext(ctx, f.client).GetKlineMinute(code, 0, 250)
	if err != nil {
		return nil, err
	}
	day := at.Format(time.DateOnly)
	out := make(protocol.Klines, 0, len(data.List))
	for _, row := range data.List {
		if row.Standard == nil || !validScreenPrice(row.Close) {
			continue
		}
		bar := *row.Standard
		clock := bar.Time.Sub(core.DayOf(bar.Time))
		inSession := clock >= 9*time.Hour+30*time.Minute && clock <= 11*time.Hour+30*time.Minute ||
			clock >= 13*time.Hour && clock <= 15*time.Hour
		if bar.Time.Format(time.DateOnly) == day && !bar.Time.After(at) && inSession {
			out = append(out, &bar)
		}
	}
	return out, nil
}

func (f screenLiveFeed) quotes(ctx context.Context, codes []string, at time.Time) (map[string]*protocol.Kline, error) {
	rows := make(map[string]*protocol.Kline, len(codes))
	var failure error
	for batch := range slices.Chunk(codes, 80) {
		if err := ctx.Err(); err != nil {
			return rows, errors.Join(failure, err)
		}
		quotes, err := tdx.WithContext(ctx, f.client).GetQuote(batch...)
		if err != nil {
			failure = errors.Join(failure, err)
			continue
		}
		for _, quote := range quotes {
			if quote.Standard == nil || quote.Standard.Kline == nil || !validScreenPrice(quote.Price) || quote.Volume <= 0 {
				continue
			}
			bar := *quote.Standard.Kline
			bar.Time = at
			rows[quote.Instrument.Exchange.String()+quote.Instrument.Code] = &bar
		}
	}
	return rows, failure
}

func validScreenPrice(price float64) bool {
	return price > 0 && !math.IsNaN(price) && !math.IsInf(price, 0)
}

type screenMinutes func(context.Context, string, time.Time) (protocol.Klines, error)

// scanScreenExits reads pending transactions from the ledger on every pass.
func scanScreenExits(ctx context.Context, db *sql.DB, market screener.Market, read screenMinutes, at time.Time) error {
	at = at.In(protocol.Location())
	day := core.DayOf(at)
	entries, err := screenEntries(db, day.Format(time.DateOnly))
	if err != nil {
		return err
	}
	byCode := map[string][]screenPosition{}
	for _, entry := range entries {
		if entry.Strategy.Buy != nil && entry.Strategy.SellSignal == nil && len(entry.Strategy.Risks) == 0 {
			continue
		}
		byCode[entry.Signal.Code] = append(byCode[entry.Signal.Code], entry)
	}
	type job struct {
		code    string
		entries []screenPosition
	}
	jobs := make(chan job)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var failed int
	var first error
	for range min(6, len(byCode)) {
		wg.Go(func() {
			for work := range jobs {
				if err := scanScreenSymbolExits(ctx, db, market, read, work.code, work.entries, at); err != nil {
					mu.Lock()
					failed++
					if first == nil {
						first = fmt.Errorf("%s: %w", work.code, err)
					}
					mu.Unlock()
				}
			}
		})
	}
	for code, entries := range byCode {
		if ctx.Err() != nil {
			break
		}
		jobs <- job{code, entries}
	}
	close(jobs)
	wg.Wait()
	if failed != 0 {
		return fmt.Errorf("%d 只股票未完成卖点检查，首个错误: %w", failed, first)
	}
	return ctx.Err()
}

func scanScreenSymbolExits(ctx context.Context, db *sql.DB, market screener.Market, read screenMinutes, code string, entries []screenPosition, at time.Time) error {
	minutes, err := read(ctx, code, at)
	if err != nil || len(minutes) == 0 {
		return err
	}
	day := core.DayOf(at)
	start := at.AddDate(-6, 0, 0)
	for _, entry := range entries {
		buy, err := time.ParseInLocation(time.DateTime, entry.Signal.BuyTime, protocol.Location())
		if err != nil {
			return err
		}
		if buy.Before(start) {
			start = buy
		}
	}
	bars, err := market.DayKlines(code, start, day.AddDate(0, 0, -1))
	if err != nil || len(bars) == 0 {
		return err
	}
	last := minutes[len(minutes)-1]
	session := minutes.Kline(last.Time, bars[len(bars)-1].Close)
	bars = screener.WithSessionBar(market, code, at, bars, session)
	if bars[len(bars)-1].Time.Before(day) {
		return nil
	}
	for _, entry := range entries {
		seller, err := screenEntrySeller(entry.Strategy)
		if err != nil {
			return err
		}
		row := entry.Signal
		buyAt, err := time.ParseInLocation(time.DateTime, row.BuyTime, protocol.Location())
		if err != nil {
			return err
		}
		signal := core.GetSellSince(seller, bars, core.Buy{Code: code, Time: buyAt, Price: row.BuyPrice}, day,
			map[string]protocol.Klines{day.Format(time.DateOnly): minutes})
		if signal == nil {
			continue
		}
		row.SellTime, row.SellPrice, row.LastPrice = signal.Time.Format(time.DateTime), signal.Price, signal.Price
		row.SellSource = "live"
		row.Incomplete = false
		row.SetRate()
		if err := closeScreenEntry(db, entry, row); err != nil {
			return err
		}
	}
	return nil
}

func screenEntrySeller(strategy screenStrategy) (core.Seller, error) {
	for _, preset := range screener.Presets(nil) {
		if preset.Key == strategy.ID {
			return preset.Seller, nil
		}
	}
	spec := builder.BuilderSpec{SellSignal: strategy.SellSignal, Risks: strategy.Risks}
	return spec.Seller()
}

// closeScreenMarket uses quotes to shortlist candidates and a dated index
// minute to confirm the trading session before evaluating the close.
type closeScreenMarket struct {
	screener.Market
	quotes map[string]*protocol.Kline
	codes  []string
	index  *protocol.Kline
	at     time.Time
}

func (m closeScreenMarket) Codes() ([]string, error) { return m.codes, nil }
func (m closeScreenMarket) LatestDayDate(string) (string, error) {
	return m.at.AddDate(0, 0, -1).Format(time.DateOnly), nil
}
func (m closeScreenMarket) StockSessionBars([]string, time.Time) (map[string]*protocol.Kline, error) {
	return m.quotes, nil
}
func (m closeScreenMarket) DayKlines(code string, start, end time.Time) (extend.Klines, error) {
	bars, err := m.Market.DayKlines(code, start, end)
	if err != nil || code != screener.BenchmarkCode || m.index == nil {
		return bars, err
	}
	return screener.WithSessionBar(m.Market, code, m.at, bars, m.index), nil
}

func captureScreenClose(ctx context.Context, market screener.Market, feed screenCloseFeed, config screenConfig, at time.Time) (screener.Result, bool, error) {
	indexMinutes, err := feed.minutes(ctx, screener.BenchmarkCode, at)
	if err != nil || !hasCloseMinute(indexMinutes, at) {
		return screener.Result{}, false, err
	}
	codes, err := market.Codes()
	if err != nil {
		return screener.Result{}, false, err
	}
	presets, custom, err := compileScreen(config)
	if err != nil {
		return screener.Result{}, false, err
	}
	closeAt := core.DayOf(at).Add(15 * time.Hour)
	index := indexMinutes.Kline(closeAt, 0)
	quotes, quoteErr := feed.quotes(ctx, codes, closeAt)
	source := closeScreenMarket{Market: market, codes: codes, quotes: quotes, index: index, at: closeAt}
	var scanner screener.Scanner
	candidates, err := scanner.Scan(source, closeAt, presets, custom, 10)
	if err != nil {
		return screener.Result{}, false, errors.Join(quoteErr, err)
	}
	if candidates.Failed > 0 {
		quoteErr = errors.Join(quoteErr, fmt.Errorf("收盘初筛 %d 只股票日线读取失败", candidates.Failed))
	}
	verified := candidates.Matches[:0]
	for _, row := range candidates.Matches {
		minutes, err := feed.minutes(ctx, row.Code, at)
		if err != nil {
			quoteErr = errors.Join(quoteErr, err)
			if ctx.Err() != nil {
				break
			}
			continue
		}
		if !hasCloseMinute(minutes, at) {
			if len(minutes) > 0 {
				quoteErr = errors.Join(quoteErr, fmt.Errorf("%s 收盘分钟线尚不可用", row.Code))
			}
			continue
		}
		// The dated minute certifies today's close. The quote carries
		// full-session OHLC and volume, including the opening auction.
		if row.Price != minutes[len(minutes)-1].Close {
			quoteErr = errors.Join(quoteErr, fmt.Errorf("%s 收盘报价尚未与分钟线一致", row.Code))
			continue
		}
		verified = append(verified, row)
	}
	candidates.Matches = verified
	return candidates, quoteErr == nil, quoteErr
}

func hasCloseMinute(minutes protocol.Klines, at time.Time) bool {
	return len(minutes) > 0 && minutes[len(minutes)-1].Time.Equal(core.DayOf(at).Add(15*time.Hour))
}

func persistScreenBuys(db *sql.DB, config screenConfig, result screener.Result) error {
	byID := make(map[string]screenStrategy, len(config.Strategies))
	for _, strategy := range config.Strategies {
		byID[strategy.ID] = strategy
	}
	var rows []screenPosition
	day, _, _ := strings.Cut(result.AsOf, " ")
	for _, match := range result.Matches {
		if match.Time != day+" 15:00:00" {
			continue
		}
		for _, hit := range match.Hits {
			strategy := byID[hit.ID]
			if !strategy.Enabled {
				continue
			}
			rows = append(rows, screenPosition{Strategy: strategy, Signal: screener.Signal{Tracked: true, StrategyName: strategy.Name,
				Code: match.Code, Name: match.Name, Strategy: hit.ID, BuyTime: match.Time, BuyPrice: match.Price, Tags: hit.Tags}})
		}
	}
	if len(rows) == 0 {
		return nil
	}
	return insertScreenEntries(db, rows)
}

func (s *screenService) runLive(ctx context.Context, interval time.Duration) {
	client := tdx.NewClient()
	defer client.Close()
	feed := screenLiveFeed{client}
	s.mu.Lock()
	s.liveMinutes = feed.minutes
	s.mu.Unlock()
	var closeDay string
	tick := time.NewTicker(interval)
	defer tick.Stop()
	for {
		at := s.now().In(protocol.Location())
		day := core.DayOf(at)
		clock := at.Sub(day)
		inSession := clock >= 9*time.Hour+31*time.Minute && clock < 11*time.Hour+32*time.Minute ||
			clock >= 13*time.Hour && clock < 15*time.Hour+20*time.Minute
		if at.Weekday() != time.Saturday && at.Weekday() != time.Sunday && inSession {
			err := s.runLivePass(ctx, feed, at, &closeDay)
			s.mu.Lock()
			s.liveError = ""
			if err != nil {
				s.liveError = "盘中交易扫描未完成，请检查实时行情或本地交易库。"
			}
			s.mu.Unlock()
			if err != nil && ctx.Err() == nil {
				log.Printf("[screen] 盘中交易扫描: %v", err)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

func (s *screenService) runLivePass(ctx context.Context, feed screenCloseFeed, at time.Time, closeDay *string) error {
	db, err := openScreenLedger(s.ledgerPath())
	if err != nil {
		return err
	}
	defer db.Close()
	exitCtx, stopExits := context.WithTimeout(ctx, 45*time.Second)
	exitErr := scanScreenExits(exitCtx, db, s.market, feed.minutes, at)
	stopExits()
	if at.Hour() < 15 || *closeDay == at.Format(time.DateOnly) {
		return exitErr
	}
	s.mu.Lock()
	config, err := s.load()
	s.mu.Unlock()
	if err != nil || !hasEnabledScreenStrategy(config) {
		return errors.Join(exitErr, err)
	}
	closeCtx, stopClose := context.WithTimeout(ctx, 90*time.Second)
	defer stopClose()
	result, complete, captureErr := captureScreenClose(closeCtx, s.market, feed, config, at)
	s.mu.Lock()
	defer s.mu.Unlock()
	current, err := s.load()
	if err != nil || current.Version != config.Version {
		return errors.Join(exitErr, captureErr, err)
	}
	err = persistScreenBuys(db, config, result)
	if complete && err == nil {
		*closeDay = at.Format(time.DateOnly)
	}
	return errors.Join(exitErr, captureErr, err)
}
