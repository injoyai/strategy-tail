package screener

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/lib/extend"
	"github.com/injoyai/tdx/v2/protocol"
)

// Market provides the same read-only inputs as the standalone Screen.
type Market interface {
	Codes() ([]string, error)
	Name(code string) string
	LatestDayDate(code string) (string, error)
	DayKlines(code string, start, end time.Time) (extend.Klines, error)
	StockSessionBars(codes []string, at time.Time) (map[string]*protocol.Kline, error)
	EquityAt(code string, at time.Time) *protocol.Equity
}

type Rule struct {
	ID      string
	Name    string
	Buyer   core.Buyer
	Seller  core.Seller
	Tags    map[string]core.Buyer
	Builtin bool
}

type Hit struct {
	ID   string   `json:"id"`
	Tags []string `json:"tags,omitempty"`
}

type Match struct {
	Code  string  `json:"code"`
	Name  string  `json:"name"`
	Time  string  `json:"time"`
	Price float64 `json:"price"`
	Rise  float64 `json:"rise"`
	Hits  []Hit   `json:"hits"`
}

type Result struct {
	AsOf           string   `json:"asOf"`
	DailyAvailable bool     `json:"dailyAvailable"`
	Universe       int      `json:"universe"`
	Evaluated      int      `json:"evaluated"`
	NoSessionBars  int      `json:"noSessionBars"`
	Failed         int      `json:"failed"`
	FailedCodes    []string `json:"failedCodes,omitempty"`
	Matches        []Match  `json:"matches"`
}

// Scanner is owned by a single worker. Its history cache is replaced when the
// session, history depth, or availability of today's official day bars changes.
type Scanner struct {
	cacheKey string
	days     map[string]extend.Klines
}

const legacyHistoryBars = 300
const customHistoryBars = 1200

// BenchmarkCode identifies the session calendar proxy and legacy trend index.
const BenchmarkCode = "sh000001"

// ScanProgress publishes the number of symbols processed, including skipped symbols.
type ScanProgress func(done, total int, code string)

func (s *Scanner) Scan(m Market, at time.Time, enabledPresets map[string]bool, custom []Rule, workers int) (Result, error) {
	return s.ScanWithProgress(context.Background(), m, at, enabledPresets, custom, workers, nil)
}

func (s *Scanner) ScanWithProgress(ctx context.Context, m Market, at time.Time, enabledPresets map[string]bool, custom []Rule, workers int, progress ScanProgress) (Result, error) {
	at = at.In(protocol.Location())
	date := at.Format(time.DateOnly)
	result := Result{AsOf: at.Format(time.DateTime), Matches: []Match{}}
	codes, err := m.Codes()
	if err != nil {
		return result, fmt.Errorf("读取股票池: %w", err)
	}
	if len(codes) == 0 {
		return result, errors.New("行情库没有可选股票")
	}
	codes = slices.Clone(codes)
	slices.Sort(codes)
	codes = slices.Compact(codes)
	result.Universe = len(codes)

	var daily bool
	if at.Hour() >= 15 {
		latest, err := m.LatestDayDate(BenchmarkCode)
		if err != nil {
			return result, fmt.Errorf("检查当日收盘行情: %w", err)
		}
		daily = latest == date
	}
	result.DailyAvailable = daily
	minutes, err := m.StockSessionBars(codes, at)
	if err != nil {
		return result, fmt.Errorf("读取当日分钟行情: %w", err)
	}

	rules, err := buildRules(m, at, dayEnd(at, daily), enabledPresets, custom, daily || len(minutes) > 0)
	if err != nil {
		return result, err
	}

	legacyStart := at.AddDate(-1, -6, 0)
	legacyStartDate := legacyStart.Format(time.DateOnly)
	limit, start := legacyHistoryBars, legacyStart
	if len(custom) > 0 {
		limit, start = customHistoryBars, at.AddDate(-6, 0, 0)
	}
	cacheKey := fmt.Sprintf("%s/%t/%d", date, daily, limit)
	if cacheKey != s.cacheKey {
		s.cacheKey, s.days = cacheKey, make(map[string]extend.Klines)
	}

	rows := make([]symbolResult, len(codes))
	scan := func(i int) {
		code := codes[i]
		if minutes[code] == nil && !daily {
			return
		}
		bars, cached := s.days[code]
		if !cached {
			bars, rows[i].err = m.DayKlines(code, start, dayEnd(at, daily))
			if rows[i].err != nil {
				return
			}
			if len(bars) > limit {
				bars = slices.Clone(bars[len(bars)-limit:])
			}
			rows[i].history = bars
		}
		ks, intraday := sessionKlines(m, code, date, bars, minutes[code])
		if len(ks) == 0 {
			return
		}
		rows[i].evaluated = true
		rows[i].match = evaluate(code, ks, legacyStartDate, intraday, rules)
	}
	if err := processSymbols(ctx, codes, workers, progress, scan); err != nil {
		return result, err
	}

	var firstError error
	for i, row := range rows {
		code := codes[i]
		switch {
		case row.err != nil:
			if firstError == nil {
				firstError = fmt.Errorf("%s 日线: %w", code, row.err)
			}
			result.Failed++
			result.FailedCodes = append(result.FailedCodes, code)
		case !row.evaluated:
			result.NoSessionBars++
		default:
			result.Evaluated++
		}
		if row.history != nil {
			s.days[code] = row.history
		}
		if row.match != nil {
			row.match.Name = m.Name(code)
			result.Matches = append(result.Matches, *row.match)
		}
	}
	if result.Evaluated == 0 && firstError != nil {
		return result, firstError
	}
	return result, nil
}

// BuildRules loads the index data needed by enabled preset rules.
func BuildRules(m Market, start, end time.Time, enabledPresets map[string]bool, custom []Rule) ([]Rule, error) {
	return buildRules(m, start, end, enabledPresets, custom, true)
}

func buildRules(m Market, start, end time.Time, enabledPresets map[string]bool, custom []Rule, loadIndex bool) ([]Rule, error) {
	var index extend.Klines
	if enabledPresets[IndexPreset] && loadIndex {
		var err error
		index, err = m.DayKlines(BenchmarkCode, start.AddDate(0, -3, 0), end)
		if err != nil {
			return nil, fmt.Errorf("读取上证指数日线: %w", err)
		}
		if len(index) == 0 {
			return nil, errors.New("上证指数日线为空，无法运行 MACD精选2")
		}
	}
	rules := make([]Rule, 0, len(enabledPresets)+len(custom))
	for _, preset := range Presets(index) {
		if enabledPresets[preset.Key] {
			rules = append(rules, Rule{ID: preset.Key, Name: preset.Name, Buyer: preset.Buyer, Seller: preset.Seller, Tags: preset.Tags, Builtin: true})
		}
	}
	rules = append(rules, custom...)

	return rules, nil
}

type symbolResult struct {
	history   extend.Klines
	match     *Match
	evaluated bool
	err       error
}

func dayEnd(at time.Time, daily bool) time.Time {
	if daily {
		return at
	}
	return at.AddDate(0, 0, -1)
}

func sessionKlines(m Market, code, date string, bars extend.Klines, minute *protocol.Kline) (extend.Klines, bool) {
	if len(bars) == 0 {
		return nil, false
	}
	last := bars[len(bars)-1]
	if last.Time.Format(time.DateOnly) == date {
		return bars, false
	}
	if minute == nil || minute.Time.Format(time.DateOnly) != date {
		return nil, false
	}
	bar := *minute
	bar.Last = last.Close
	intraday := &extend.Kline{Unix: bar.Time.Unix(), Kline: &bar}
	if equity := m.EquityAt(code, bar.Time); equity != nil {
		intraday.FloatStock, intraday.TotalStock = equity.Float, equity.Total
		if equity.Float > 0 {
			intraday.Turnover = equity.Turnover(protocol.ToShares(bar.Volume))
		}
	}
	return append(bars[:len(bars):len(bars)], intraday), true
}

func evaluate(code string, ks extend.Klines, legacyStartDate string, intraday bool, rules []Rule) *Match {
	last := ks[len(ks)-1]
	if last.Close <= 0 || math.IsNaN(last.Close) || math.IsInf(last.Close, 0) {
		return nil
	}
	legacyLimit := legacyHistoryBars
	if intraday {
		legacyLimit++
	}
	legacy := ks[max(0, len(ks)-legacyLimit):]
	for len(legacy) > 0 && legacy[0].Time.Format(time.DateOnly) < legacyStartDate {
		legacy = legacy[1:]
	}
	var hits []Hit
	for _, rule := range rules {
		input := ks
		if rule.Builtin {
			input = legacy
			if intraday && len(input) < 2 {
				continue // Standalone Screen needs at least one preceding daily bar.
			}
		}
		if !rule.Buyer.Buy(code, input) {
			continue
		}
		hits = append(hits, Hit{ID: rule.ID, Tags: rule.tags(code, input)})
	}
	if len(hits) == 0 {
		return nil
	}
	rise, _ := last.RisePercent()
	if math.IsNaN(rise) || math.IsInf(rise, 0) {
		rise = 0
	}
	return &Match{Code: code, Time: last.Time.Format(time.DateTime), Price: last.Close, Rise: rise, Hits: hits}
}

func (r Rule) tags(code string, ks extend.Klines) []string {
	var tags []string
	for label, tag := range r.Tags {
		if tag.Buy(code, ks) {
			tags = append(tags, label)
		}
	}
	sort.Strings(tags)
	return tags
}

func (r Rule) Input(ks extend.Klines) extend.Klines {
	limit := customHistoryBars
	if r.Builtin {
		limit = legacyHistoryBars
	}
	if len(ks) > 0 && ks[len(ks)-1].Time.Hour() < 15 {
		limit++
	}
	return ks[max(0, len(ks)-limit):]
}

// WithSessionBar adds the latest stored minute aggregation to completed daily bars.
func WithSessionBar(m Market, code string, at time.Time, bars extend.Klines, minute *protocol.Kline) extend.Klines {
	if combined, appended := sessionKlines(m, code, at.In(protocol.Location()).Format(time.DateOnly), bars, minute); appended {
		return combined
	}
	return bars
}

// processSymbols serializes completion callbacks while allowing independent work.
func processSymbols(ctx context.Context, codes []string, workers int, progress ScanProgress, work func(int)) error {
	if progress != nil {
		progress(0, len(codes), "")
	}
	jobs := make(chan int)
	var wg sync.WaitGroup
	var mu sync.Mutex
	done := 0
	for range min(max(1, workers), len(codes)) {
		wg.Go(func() {
			for i := range jobs {
				if ctx.Err() != nil {
					return
				}
				work(i)
				if progress != nil {
					mu.Lock()
					done++
					progress(done, len(codes), codes[i])
					mu.Unlock()
				}
			}
		})
	}
send:
	for i := range codes {
		select {
		case <-ctx.Done():
			break send
		case jobs <- i:
		}
	}
	close(jobs)
	wg.Wait()
	return ctx.Err()
}
