// Package researchrun provides the shared execution path for local strategy
// experiments. Commands keep ownership of strategy definitions and reporting;
// this package owns data loading, worker coordination and K-line isolation.
package researchrun

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/lib/extend"
	"github.com/injoyai/tdx/protocol"
)

// DataMode controls whether intraday bars are loaded for execution.
type DataMode uint8

const (
	DailyClose DataMode = iota
	Intraday
)

// Variant is one named buyer/seller combination in an experiment matrix.
// Seller may be nil when Config.DefaultSeller is set.
type Variant struct {
	Name   string
	Buyer  core.Buyer
	Seller core.Seller
}

// Config describes one matrix run. It deliberately contains execution inputs
// only; presentation and artifact naming remain with the calling command.
type Config struct {
	Codes         []string
	Years         []int
	Variants      []Variant
	DefaultSeller core.Seller
	Cost          core.Cost
	Position      core.PositionConfig
	Workers       int
	DataMode      DataMode
	GetDayKlines  core.GetDayKlines
	GetMinKlines  core.GetMinKlines
	OnCodeDone    func(Progress)
}

// Failure records why a requested code was excluded from all matrix results.
type Failure struct {
	Code    string `json:"code"`
	Year    int    `json:"year"`
	Stage   string `json:"stage"`
	Message string `json:"message"`
}

// Coverage makes silent data exclusion visible to callers and reports.
type Coverage struct {
	Requested int       `json:"requested"`
	Completed int       `json:"completed"`
	Skipped   int       `json:"skipped"`
	Failures  []Failure `json:"failures"`
}

// Progress is emitted serially after each requested code finishes or is skipped.
type Progress struct {
	Done    int
	Total   int
	Code    string
	Failure *Failure
}

// Result contains all trades for a variant, preserving variant order.
type Result struct {
	Variant Variant
	Trades  []core.Trade
}

// Report is the execution result plus auditable data coverage.
type Report struct {
	Results  []Result
	Coverage Coverage
}

// YearData holds one code-year data slice produced by loadYear: His covers
// the warm-up window before the requested year, Dks covers the requested
// year and Mks carries intraday bars when the config requests them.
type YearData struct {
	His extend.Klines
	Dks extend.Klines
	Mks protocol.Klines
}

type codeResult struct {
	code    string
	trades  [][]core.Trade
	failure *Failure
}

// Run executes all variants against every requested code. A code is excluded
// from every variant if any requested year cannot be loaded, matching the
// previous command behavior while making the exclusion observable.
func Run(ctx context.Context, cfg Config) (Report, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := validate(cfg); err != nil {
		return Report{}, err
	}

	report := Report{
		Results:  make([]Result, len(cfg.Variants)),
		Coverage: Coverage{Requested: len(cfg.Codes)},
	}
	for i, variant := range cfg.Variants {
		report.Results[i].Variant = variant
	}
	if len(cfg.Codes) == 0 {
		return report, nil
	}

	workers := cfg.Workers
	if workers <= 0 {
		workers = 10
	}
	if workers > len(cfg.Codes) {
		workers = len(cfg.Codes)
	}

	jobs := make(chan string)
	results := make(chan codeResult)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case code, ok := <-jobs:
					if !ok {
						return
					}
					result := runCode(ctx, cfg, code)
					select {
					case <-ctx.Done():
						return
					case results <- result:
					}
				}
			}
		}()
	}

	go func() {
		defer close(jobs)
		for _, code := range cfg.Codes {
			select {
			case <-ctx.Done():
				return
			case jobs <- code:
			}
		}
	}()
	go func() {
		wg.Wait()
		close(results)
	}()

	done := 0
	for result := range results {
		done++
		if result.failure != nil {
			report.Coverage.Skipped++
			report.Coverage.Failures = append(report.Coverage.Failures, *result.failure)
		} else {
			report.Coverage.Completed++
			for i, trades := range result.trades {
				report.Results[i].Trades = append(report.Results[i].Trades, trades...)
			}
		}
		if cfg.OnCodeDone != nil {
			cfg.OnCodeDone(Progress{
				Done: done, Total: len(cfg.Codes), Code: result.code, Failure: result.failure,
			})
		}
	}
	if err := ctx.Err(); err != nil {
		return report, err
	}
	return report, nil
}

// ForEachCodeData loads each code in cfg.Codes for every cfg.Years with a
// worker pool and invokes fn once per successfully loaded code. Loading
// reuses the exact Run/loadYear path so factor snapshot filling and IC
// analysis observe the same data slices the backtest would load.
// fn runs on worker goroutines; callers must synchronize any shared state
// it touches, and a blocking fn stalls its worker. His/Dks alias the slices
// returned by the provider, so mutating them inside fn may corrupt cached
// provider data. Codes whose data cannot be loaded are skipped without
// invoking fn but reported through cfg.OnCodeDone with a non-nil Failure.
// Codes cancelled mid-load also skip fn and report Failure nil; treat the
// returned ctx.Err() as authoritative for whether the visit is complete.
// Returns ctx.Err() when the context is cancelled.
func ForEachCodeData(ctx context.Context, cfg Config, fn func(code string, datas []YearData)) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if len(cfg.Years) == 0 {
		return fmt.Errorf("researchrun: years is empty")
	}
	if cfg.GetDayKlines == nil {
		return fmt.Errorf("researchrun: day K-line provider is nil")
	}
	if cfg.DataMode != DailyClose && cfg.DataMode != Intraday {
		return fmt.Errorf("researchrun: unsupported data mode %d", cfg.DataMode)
	}
	if cfg.DataMode == Intraday && cfg.GetMinKlines == nil {
		return fmt.Errorf("researchrun: minute K-line provider is nil")
	}
	if len(cfg.Codes) == 0 {
		return nil
	}

	workers := cfg.Workers
	if workers <= 0 {
		workers = 10
	}
	if workers > len(cfg.Codes) {
		workers = len(cfg.Codes)
	}

	jobs := make(chan string)
	var wg sync.WaitGroup
	var mu sync.Mutex
	done := 0
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case code, ok := <-jobs:
					if !ok {
						return
					}
					datas := make([]YearData, 0, len(cfg.Years))
					var fail *Failure
					for _, year := range cfg.Years {
						if ctx.Err() != nil {
							break
						}
						data, failure := loadYear(cfg, code, year)
						if failure != nil {
							fail = failure
							break
						}
						datas = append(datas, data)
					}
					if fail == nil && ctx.Err() == nil {
						fn(code, datas)
					}
					mu.Lock()
					done++
					if cfg.OnCodeDone != nil {
						cfg.OnCodeDone(Progress{
							Done: done, Total: len(cfg.Codes), Code: code, Failure: fail,
						})
					}
					mu.Unlock()
				}
			}
		}()
	}

	go func() {
		defer close(jobs)
		for _, code := range cfg.Codes {
			select {
			case <-ctx.Done():
				return
			case jobs <- code:
			}
		}
	}()
	wg.Wait()

	return ctx.Err()
}

func validate(cfg Config) error {
	if len(cfg.Years) == 0 {
		return fmt.Errorf("researchrun: years is empty")
	}
	if len(cfg.Variants) == 0 {
		return fmt.Errorf("researchrun: variants is empty")
	}
	if cfg.GetDayKlines == nil {
		return fmt.Errorf("researchrun: day K-line provider is nil")
	}
	if cfg.DataMode != DailyClose && cfg.DataMode != Intraday {
		return fmt.Errorf("researchrun: unsupported data mode %d", cfg.DataMode)
	}
	if cfg.DataMode == Intraday && cfg.GetMinKlines == nil {
		return fmt.Errorf("researchrun: minute K-line provider is nil")
	}
	for i, variant := range cfg.Variants {
		if variant.Buyer == nil {
			return fmt.Errorf("researchrun: variant %d buyer is nil", i)
		}
		if variant.Seller == nil && cfg.DefaultSeller == nil {
			return fmt.Errorf("researchrun: variant %d seller is nil", i)
		}
	}
	return nil
}

func runCode(ctx context.Context, cfg Config, code string) codeResult {
	datas := make([]YearData, 0, len(cfg.Years))
	for _, year := range cfg.Years {
		if ctx.Err() != nil {
			return codeResult{code: code}
		}
		data, failure := loadYear(cfg, code, year)
		if failure != nil {
			return codeResult{code: code, failure: failure}
		}
		datas = append(datas, data)
	}

	variantTrades := make([][]core.Trade, len(cfg.Variants))
	for i, variant := range cfg.Variants {
		if ctx.Err() != nil {
			return codeResult{code: code}
		}
		seller := variant.Seller
		if seller == nil {
			seller = cfg.DefaultSeller
		}
		bt := core.Backtest{
			Buyer:    variant.Buyer,
			Seller:   seller,
			Codes:    []string{code},
			Years:    cfg.Years,
			Cost:     cfg.Cost,
			Position: cfg.Position,
		}
		for _, data := range datas {
			variantTrades[i] = append(variantTrades[i], bt.Do(
				code, cloneKlines(data.His), cloneKlines(data.Dks), data.Mks,
			)...)
		}
	}
	return codeResult{code: code, trades: variantTrades}
}

func loadYear(cfg Config, code string, year int) (YearData, *Failure) {
	hisStart := time.Date(year-2, 6, 1, 0, 0, 0, 0, time.Local)
	start := time.Date(year, 1, 1, 0, 0, 0, 0, time.Local)
	end := time.Date(year, 12, 31, 23, 0, 0, 0, time.Local)

	all, err := cfg.GetDayKlines(code, hisStart, end)
	if err != nil {
		return YearData{}, failure(code, year, "day", err.Error())
	}
	if len(all) == 0 {
		return YearData{}, failure(code, year, "day", "no day K-line data")
	}

	split := -1
	for i, kline := range all {
		if !kline.Time.Before(start) {
			split = i
			break
		}
	}
	if split < 0 {
		return YearData{}, failure(code, year, "period", "no K-line data in requested year")
	}

	data := YearData{His: all[:split], Dks: all[split:]}
	if cfg.DataMode == Intraday {
		data.Mks, err = cfg.GetMinKlines(code, start, end)
		if err != nil {
			return YearData{}, failure(code, year, "minute", err.Error())
		}
	}
	return data, nil
}

func failure(code string, year int, stage, message string) *Failure {
	return &Failure{Code: code, Year: year, Stage: stage, Message: message}
}

// cloneKlines isolates callers from Backtest.Do's intentional intraday pointer
// overwrite. Keep this implementation centralized until the engine contract can
// be changed with explicit compatibility approval.
func cloneKlines(src extend.Klines) extend.Klines {
	if src == nil {
		return nil
	}
	dst := make(extend.Klines, len(src))
	for i, kline := range src {
		if kline == nil {
			continue
		}
		clone := *kline
		if kline.Kline != nil {
			inner := *kline.Kline
			clone.Kline = &inner
		}
		dst[i] = &clone
	}
	return dst
}
