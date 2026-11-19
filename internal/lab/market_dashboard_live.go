package lab

import (
	"context"
	"errors"
	"fmt"
	"log"
	"slices"
	"sync"
	"time"

	common "github.com/injoyai/strategy-tail"
	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/internal/marketstore"
	tdx "github.com/injoyai/tdx/v2"
	"github.com/injoyai/tdx/v2/protocol"
)

const (
	dashboardRefresh        = 15 * time.Second
	dashboardMorningOpen    = 9*time.Hour + 30*time.Minute
	dashboardMorningClose   = 11*time.Hour + 30*time.Minute
	dashboardAfternoonOpen  = 13 * time.Hour
	dashboardAfternoonClose = 15 * time.Hour
)

var errDashboardNoRecentMinute = errors.New("上证指数本时段分钟线尚不可用")

type dashboardQuoteFeed interface {
	GetKlineMinute(string, uint16, uint16) (*tdx.KlineResp, error)
	GetQuote(...string) ([]tdx.Quote, error)
}

func dashboardSchedule(at time.Time) (phase string, next time.Time) {
	at = at.In(protocol.Location())
	day := core.DayOf(at)
	morning, midday := day.Add(dashboardMorningOpen), day.Add(dashboardMorningClose)
	afternoon, closeAt := day.Add(dashboardAfternoonOpen), day.Add(dashboardAfternoonClose)
	if dashboardWeekday(at) {
		switch {
		case at.Before(morning):
			return "closed", morning
		case at.Before(midday):
			return "trading", midday
		case at.Before(afternoon):
			return "break", afternoon
		case at.Before(closeAt):
			return "trading", closeAt
		}
	}
	for next = morning.AddDate(0, 0, 1); !dashboardWeekday(next); next = next.AddDate(0, 0, 1) {
	}
	return "closed", next
}

func dashboardWeekday(at time.Time) bool {
	return at.Weekday() != time.Saturday && at.Weekday() != time.Sunday
}

func dashboardHasRecentMinute(at time.Time, bars *tdx.KlineResp) bool {
	if bars == nil || len(bars.List) == 0 {
		return false
	}
	bar := bars.List[len(bars.List)-1]
	stamp, err := time.ParseInLocation(protocol.TimeLayout, bar.Time, at.Location())
	if err != nil || !validDashboardValue(bar.Close) {
		return false
	}
	start := core.DayOf(at).Add(dashboardMorningOpen)
	if afternoon := core.DayOf(at).Add(dashboardAfternoonOpen); !at.Before(afternoon) {
		start = afternoon
	}
	// A current minute can carry its closing timestamp up to one minute ahead.
	return !stamp.Before(start) && !stamp.Before(at.Truncate(time.Minute).Add(-2*time.Minute)) &&
		!stamp.After(at.Truncate(time.Minute).Add(time.Minute))
}

func readDashboardTDX(ctx context.Context, at time.Time) (marketDashboard, error) {
	var codes []string
	if market := common.Pull.Metadata(); market != nil && market.Codes != nil {
		codes = market.Codes.GetStockCodes()
	}
	if len(codes) == 0 {
		var err error
		codes, err = common.Pull.LocalStockCodes()
		if err != nil {
			return marketDashboard{}, err
		}
	}
	client := tdx.NewClient(tdx.WithLevel(tdx.LevelNone))
	defer client.Close()
	view, err := collectDashboardQuotes(ctx, tdx.WithContext(ctx, client), codes, at)
	if err != nil {
		return marketDashboard{}, err
	}
	view.CapturedAt = time.Now().In(protocol.Location()).Format(time.RFC3339)
	return view, nil
}

func collectDashboardQuotes(ctx context.Context, feed dashboardQuoteFeed, stocks []string, at time.Time) (marketDashboard, error) {
	at = at.In(protocol.Location())
	bars, err := feed.GetKlineMinute(dashboardCalendarIndex, 0, 1)
	if err != nil {
		return marketDashboard{}, fmt.Errorf("读取上证实时分钟线: %w", err)
	}
	if !dashboardHasRecentMinute(at, bars) {
		return marketDashboard{}, errDashboardNoRecentMinute
	}

	day := at.Format(time.DateOnly)
	indexCodes := dashboardCodes()
	indexQuotes, err := feed.GetQuote(indexCodes...)
	if err != nil {
		// A single unavailable index must not invalidate the others.
		indexQuotes = nil
		for _, code := range indexCodes {
			if ctx.Err() != nil {
				return marketDashboard{}, ctx.Err()
			}
			if quotes, err := feed.GetQuote(code); err == nil {
				indexQuotes = append(indexQuotes, quotes...)
			}
		}
	}
	indexes := dashboardLiveIndexes(day, indexQuotes)
	if indexes[0].Date == "" {
		return marketDashboard{}, errors.New("上证指数实时报价不可用")
	}

	// Keep failures local to their exchange. Suspended names may return a
	// quote with zero volume, which counts toward coverage but not turnover.
	groups := map[protocol.Exchange][]string{}
	for _, code := range stocks {
		if protocol.IsStock(code) {
			exchange, _, err := protocol.DecodeCode(code)
			if err == nil {
				groups[exchange] = append(groups[exchange], code)
			}
		}
	}
	rows := make([]marketstore.DashboardStockDay, 0, len(stocks))
	expected, received := 0, 0
	for _, exchange := range []protocol.Exchange{protocol.ExchangeSH, protocol.ExchangeSZ, protocol.ExchangeBJ} {
		expected += len(groups[exchange])
		for batch := range slices.Chunk(groups[exchange], 80) {
			if err := ctx.Err(); err != nil {
				return marketDashboard{}, err
			}
			quotes, err := feed.GetQuote(batch...)
			if err != nil {
				continue
			}
			for _, quote := range quotes {
				if quote.Standard == nil || quote.Standard.Kline == nil || (quote.Volume > 0 && !validDashboardValue(quote.Price)) {
					continue
				}
				received++
				if quote.Volume <= 0 {
					continue
				}
				rows = append(rows, marketstore.DashboardStockDay{
					Code: dashboardQuoteCode(quote), Close: quote.Price, Previous: quote.PreClose, Amount: quote.Standard.Kline.Amount,
				})
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return marketDashboard{}, err
	}
	if received == 0 {
		return marketDashboard{}, errors.New("沪深北股票实时报价不可用")
	}

	return marketDashboard{Date: day, Source: "tdx", Indexes: indexes,
		Stocks: summarizeDashboardStocks(rows), StockUniverse: expected, StockQuotes: received}, nil
}

func dashboardQuoteCode(quote tdx.Quote) string {
	return quote.Instrument.Exchange.String() + quote.Instrument.Code
}

func dashboardLiveIndexes(date string, quotes []tdx.Quote) []dashboardIndexQuote {
	indexes := dashboardIndexList
	byCode := make(map[string]tdx.Quote, len(quotes))
	for _, quote := range quotes {
		byCode[dashboardQuoteCode(quote)] = quote
	}
	for i, index := range indexes {
		quote, ok := byCode[index.Code]
		if ok && validDashboardValue(quote.Price) {
			indexes[i].Date = date
			indexes[i].Close = quote.Price
			indexes[i].Change = dashboardChange(quote.Price, quote.PreClose)
		}
	}
	return indexes[:]
}

type dashboardLiveReader func(context.Context, time.Time) (marketDashboard, error)

type dashboardLiveCache struct {
	dashboardSnapshotCache[marketDashboard]
}

func (c *dashboardLiveCache) load(ctx context.Context, at time.Time, read dashboardLiveReader) (marketDashboard, error) {
	return c.loadWithInterval(ctx, at, dashboardRefresh, read)
}

type dashboardSnapshotCache[T any] struct {
	mu          sync.Mutex
	last        T
	lastDay     string
	attemptDay  string
	nextAttempt time.Time
	err         error
	pending     chan struct{}
}

func (c *dashboardSnapshotCache[T]) latest(date string) (T, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.lastDay == date {
		return c.last, c.err
	}
	var empty T
	return empty, c.err
}

// loadWithInterval shares one bounded request across all viewers. Leaving the page cancels
// its wait without aborting the upstream request for other viewers.
func (c *dashboardSnapshotCache[T]) loadWithInterval(ctx context.Context, at time.Time, period time.Duration,
	read func(context.Context, time.Time) (T, error)) (T, error) {
	day := at.Format(time.DateOnly)
	c.mu.Lock()
	if c.pending == nil && (day != c.attemptDay || !at.Before(c.nextAttempt)) {
		c.attemptDay = day
		c.nextAttempt = at.Add(period)
		c.pending = make(chan struct{})
		go c.refresh(at, day, read)
	}
	pending := c.pending
	c.mu.Unlock()

	if pending != nil {
		select {
		case <-ctx.Done():
			var empty T
			return empty, ctx.Err()
		case <-pending:
		}
	}
	return c.latest(day)
}

func (c *dashboardSnapshotCache[T]) refresh(at time.Time, day string, read func(context.Context, time.Time) (T, error)) {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	view, err := read(ctx, at)
	if err != nil && !errors.Is(err, errDashboardNoRecentMinute) {
		log.Printf("[lab] 更新数据看板行情: %v", err)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err == nil {
		c.last = view
		c.lastDay = day
	}
	c.err = err
	close(c.pending)
	c.pending = nil
}
