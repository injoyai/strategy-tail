package researchrun

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/lib/extend"
	"github.com/injoyai/tdx/protocol"
)

type alwaysBuyer struct{}

func (alwaysBuyer) Name() string                   { return "always" }
func (alwaysBuyer) Buy(string, extend.Klines) bool { return true }

type nextDaySeller struct{}

func (nextDaySeller) Name() string { return "next day" }
func (nextDaySeller) Sell(_ string, dks extend.Klines, buy core.Buy) bool {
	return len(dks) > 0 && dks[len(dks)-1].Time.After(buy.Time)
}

func TestRunReportsCoverageAndPreservesVariantOrder(t *testing.T) {
	providerErr := errors.New("missing database")
	day := func(code string, _, _ time.Time) (extend.Klines, error) {
		if code == "bad" {
			return nil, providerErr
		}
		return testKlines(), nil
	}
	progress := make([]Progress, 0, 2)
	report, err := Run(context.Background(), Config{
		Codes:         []string{"good", "bad"},
		Years:         []int{2024},
		Variants:      []Variant{{Name: "first", Buyer: alwaysBuyer{}}, {Name: "second", Buyer: alwaysBuyer{}}},
		DefaultSeller: nextDaySeller{},
		Position:      core.DefaultPositionConfig(),
		Workers:       2,
		DataMode:      DailyClose,
		GetDayKlines:  day,
		OnCodeDone:    func(p Progress) { progress = append(progress, p) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Coverage.Requested != 2 || report.Coverage.Completed != 1 || report.Coverage.Skipped != 1 {
		t.Fatalf("unexpected coverage: %+v", report.Coverage)
	}
	if len(report.Coverage.Failures) != 1 || report.Coverage.Failures[0].Stage != "day" {
		t.Fatalf("unexpected failures: %+v", report.Coverage.Failures)
	}
	if len(report.Results) != 2 || report.Results[0].Variant.Name != "first" || report.Results[1].Variant.Name != "second" {
		t.Fatalf("variant order changed: %+v", report.Results)
	}
	if len(report.Results[0].Trades) == 0 || len(report.Results[1].Trades) == 0 {
		t.Fatalf("expected trades for both variants: %+v", report.Results)
	}
	if len(progress) != 2 || progress[1].Done != 2 {
		t.Fatalf("unexpected progress: %+v", progress)
	}
}

func TestRunRequiresMinuteProviderInIntradayMode(t *testing.T) {
	_, err := Run(context.Background(), Config{
		Years:         []int{2024},
		Variants:      []Variant{{Buyer: alwaysBuyer{}}},
		DefaultSeller: nextDaySeller{},
		DataMode:      Intraday,
		GetDayKlines:  func(string, time.Time, time.Time) (extend.Klines, error) { return nil, nil },
	})
	if err == nil {
		t.Fatal("expected validation error")
	}
}

func TestRunHonorsCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Run(ctx, Config{
		Codes:         []string{"good"},
		Years:         []int{2024},
		Variants:      []Variant{{Buyer: alwaysBuyer{}}},
		DefaultSeller: nextDaySeller{},
		DataMode:      DailyClose,
		GetDayKlines:  func(string, time.Time, time.Time) (extend.Klines, error) { return testKlines(), nil },
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context cancellation, got %v", err)
	}
}

func TestForEachCodeDataLoadsAndReports(t *testing.T) {
	codes := []string{"sh600000", "sz000001"}
	want := testKlines()
	var mu sync.Mutex
	loaded := make(map[string][]YearData, 2)
	progress := make([]Progress, 0, 2)
	err := ForEachCodeData(context.Background(), Config{
		Codes:    codes,
		Years:    []int{2024},
		Workers:  2,
		DataMode: DailyClose,
		GetDayKlines: func(string, time.Time, time.Time) (extend.Klines, error) {
			return want, nil
		},
		OnCodeDone: func(p Progress) { progress = append(progress, p) },
	}, func(code string, datas []YearData) {
		mu.Lock()
		loaded[code] = datas
		mu.Unlock()
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != len(codes) {
		t.Fatalf("expected %d loaded codes, got %d: %v", len(codes), len(loaded), loaded)
	}
	for _, code := range codes {
		datas, ok := loaded[code]
		if !ok {
			t.Fatalf("code %s was never loaded", code)
		}
		if len(datas) != 1 {
			t.Fatalf("expected 1 year of data for %s, got %d", code, len(datas))
		}
		if len(datas[0].His) != 0 {
			t.Fatalf("expected empty His before %d for %s, got %d entries", 2024, code, len(datas[0].His))
		}
		if len(datas[0].Dks) != len(want) {
			t.Fatalf("expected %d day K-lines for %s, got %d", len(want), code, len(datas[0].Dks))
		}
		for i := range want {
			if !datas[0].Dks[i].Time.Equal(want[i].Time) {
				t.Fatalf("unexpected Dks[%d].Time for %s: %v", i, code, datas[0].Dks[i].Time)
			}
		}
	}
	if len(progress) != 2 || progress[0].Done != 1 || progress[1].Done != 2 {
		t.Fatalf("unexpected progress: %+v", progress)
	}
	for _, p := range progress {
		if p.Failure != nil {
			t.Fatalf("unexpected failure: %+v", p.Failure)
		}
	}
}

func TestForEachCodeDataSkipsFailedCode(t *testing.T) {
	providerErr := errors.New("missing database")
	day := func(code string, _, _ time.Time) (extend.Klines, error) {
		if code == "bad" {
			return nil, providerErr
		}
		return testKlines(), nil
	}
	var mu sync.Mutex
	loaded := make([]string, 0, 1)
	progress := make([]Progress, 0, 2)
	err := ForEachCodeData(context.Background(), Config{
		Codes:        []string{"good", "bad"},
		Years:        []int{2024},
		Workers:      2,
		DataMode:     DailyClose,
		GetDayKlines: day,
		OnCodeDone:   func(p Progress) { progress = append(progress, p) },
	}, func(code string, datas []YearData) {
		mu.Lock()
		loaded = append(loaded, code)
		mu.Unlock()
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 1 || loaded[0] != "good" {
		t.Fatalf("expected only good to be loaded, got %v", loaded)
	}
	if len(progress) != 2 {
		t.Fatalf("expected 2 progress events, got %d", len(progress))
	}
	failures := 0
	for _, p := range progress {
		if p.Failure == nil {
			continue
		}
		failures++
		if p.Failure.Code != "bad" || p.Failure.Stage != "day" || p.Failure.Message != providerErr.Error() {
			t.Fatalf("unexpected failure: %+v", p.Failure)
		}
	}
	if failures != 1 {
		t.Fatalf("expected 1 failure, got %d", failures)
	}
	if progress[1].Done != 2 {
		t.Fatalf("expected Done to reach the total code count, got %+v", progress)
	}
}

func TestForEachCodeDataHonorsCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	called := false
	err := ForEachCodeData(ctx, Config{
		Codes:        []string{"good"},
		Years:        []int{2024},
		DataMode:     DailyClose,
		GetDayKlines: func(string, time.Time, time.Time) (extend.Klines, error) { return testKlines(), nil },
	}, func(string, []YearData) { called = true })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context cancellation, got %v", err)
	}
	if called {
		t.Fatal("expected fn to never be invoked")
	}
}

func TestForEachCodeDataSplitsWarmupWindow(t *testing.T) {
	base := time.Date(2023, 12, 29, 0, 0, 0, 0, time.Local)
	prices := []float64{9, 10, 10.1, 10.2}
	day := func(string, time.Time, time.Time) (extend.Klines, error) {
		all := make(extend.Klines, len(prices))
		for i, price := range prices {
			at := base
			if i > 0 {
				at = time.Date(2024, 1, i, 0, 0, 0, 0, time.Local)
			}
			all[i] = &extend.Kline{Kline: &protocol.Kline{
				Time: at, Open: protocol.Yuan(price), High: protocol.Yuan(price),
				Low: protocol.Yuan(price), Close: protocol.Yuan(price), Volume: 100,
			}}
		}
		return all, nil
	}
	var datas []YearData
	err := ForEachCodeData(context.Background(), Config{
		Codes:        []string{"sh600000"},
		Years:        []int{2024},
		DataMode:     DailyClose,
		GetDayKlines: day,
	}, func(_ string, d []YearData) { datas = d })
	if err != nil {
		t.Fatal(err)
	}
	if len(datas) != 1 {
		t.Fatalf("expected 1 year of data, got %d", len(datas))
	}
	if len(datas[0].His) != 1 || !datas[0].His[0].Time.Equal(base) {
		t.Fatalf("expected warm-up K-line at %v, got %+v", base, datas[0].His)
	}
	if len(datas[0].Dks) != len(prices)-1 {
		t.Fatalf("expected %d year K-lines, got %d", len(prices)-1, len(datas[0].Dks))
	}
	if !datas[0].Dks[0].Time.Equal(time.Date(2024, 1, 1, 0, 0, 0, 0, time.Local)) {
		t.Fatalf("unexpected first Dks time: %v", datas[0].Dks[0].Time)
	}
	if !datas[0].Dks[len(datas[0].Dks)-1].Time.Equal(time.Date(2024, 1, len(prices)-1, 0, 0, 0, 0, time.Local)) {
		t.Fatalf("unexpected last Dks time: %v", datas[0].Dks[len(datas[0].Dks)-1].Time)
	}
}

func TestForEachCodeDataIntradayLoadsMinuteBars(t *testing.T) {
	mks := protocol.Klines{&protocol.Kline{Time: time.Date(2024, 1, 2, 10, 0, 0, 0, time.Local)}}
	err := ForEachCodeData(context.Background(), Config{
		Codes:        []string{"sh600000"},
		Years:        []int{2024},
		DataMode:     Intraday,
		GetDayKlines: func(string, time.Time, time.Time) (extend.Klines, error) { return testKlines(), nil },
		GetMinKlines: func(string, time.Time, time.Time) (protocol.Klines, error) { return mks, nil },
	}, func(_ string, d []YearData) {
		if len(d[0].Mks) != len(mks) || !d[0].Mks[0].Time.Equal(mks[0].Time) {
			t.Errorf("unexpected minute bars: %+v", d[0].Mks)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestForEachCodeDataValidatesConfig(t *testing.T) {
	day := func(string, time.Time, time.Time) (extend.Klines, error) { return testKlines(), nil }
	cases := []struct {
		name string
		cfg  Config
	}{
		{"empty years", Config{Codes: []string{"good"}, DataMode: DailyClose, GetDayKlines: day}},
		{"nil day provider", Config{Codes: []string{"good"}, Years: []int{2024}, DataMode: DailyClose}},
		{"intraday without minute provider", Config{Codes: []string{"good"}, Years: []int{2024}, DataMode: Intraday, GetDayKlines: day}},
		{"unsupported data mode", Config{Codes: []string{"good"}, Years: []int{2024}, DataMode: 99, GetDayKlines: day}},
	}
	for _, tc := range cases {
		if err := ForEachCodeData(context.Background(), tc.cfg, func(string, []YearData) {}); err == nil {
			t.Fatalf("%s: expected validation error", tc.name)
		}
	}
	if err := ForEachCodeData(context.Background(), Config{
		Years: []int{2024}, DataMode: DailyClose, GetDayKlines: day,
	}, func(string, []YearData) {}); err != nil {
		t.Fatalf("expected empty codes to succeed, got %v", err)
	}
}

func testKlines() extend.Klines {
	base := time.Date(2023, 12, 29, 0, 0, 0, 0, time.Local)
	prices := []float64{10, 10.1, 10.2}
	result := make(extend.Klines, len(prices))
	for i, price := range prices {
		at := base.AddDate(0, 0, i+3)
		result[i] = &extend.Kline{Kline: &protocol.Kline{
			Time: at, Open: protocol.Yuan(price), High: protocol.Yuan(price),
			Low: protocol.Yuan(price), Close: protocol.Yuan(price), Volume: 100,
		}}
	}
	return result
}

// klinesInYear 生成指定年份内连续 3 根日 K（provider 按请求年份返回数据）。
func klinesInYear(year int) extend.Klines {
	prices := []float64{10, 10.1, 10.2}
	result := make(extend.Klines, len(prices))
	for i, price := range prices {
		result[i] = &extend.Kline{Kline: &protocol.Kline{
			Time: time.Date(year, 1, i+1, 0, 0, 0, 0, time.Local),
			Open: protocol.Yuan(price), High: protocol.Yuan(price),
			Low: protocol.Yuan(price), Close: protocol.Yuan(price), Volume: 100,
		}}
	}
	return result
}

// TestForEachCodeYearDataKeepsPartialYears 单年缺失不得作废该股票其他年份：
// 缺失年份进入失败明细与 SkippedCodeYears，成功年份照常回调。
func TestForEachCodeYearDataKeepsPartialYears(t *testing.T) {
	day := func(_ string, _, end time.Time) (extend.Klines, error) {
		if end.Year() != 2024 {
			return nil, nil // 2023 年无数据 → loadYear 报 no day K-line data
		}
		return klinesInYear(2024), nil
	}
	var mu sync.Mutex
	type visit struct {
		code string
		year int
		days int
	}
	visits := []visit(nil)
	progress := make([]Progress, 0, 1)
	coverage, err := ForEachCodeYearData(context.Background(), Config{
		Codes:        []string{"sh600000"},
		Years:        []int{2023, 2024},
		Workers:      1,
		DataMode:     DailyClose,
		GetDayKlines: day,
		OnCodeDone:   func(p Progress) { progress = append(progress, p) },
	}, func(code string, year int, data YearData) {
		mu.Lock()
		visits = append(visits, visit{code: code, year: year, days: len(data.Dks)})
		mu.Unlock()
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(visits) != 1 || visits[0].code != "sh600000" || visits[0].year != 2024 || visits[0].days != 3 {
		t.Fatalf("expected only 2024 to be visited, got %+v", visits)
	}
	if coverage.RequestedCodes != 1 || coverage.CompletedCodes != 1 {
		t.Fatalf("CompletedCodes 应为至少有一个成功年份的股票数: %+v", coverage)
	}
	if coverage.RequestedCodeYears != 2 || coverage.CompletedCodeYears != 1 || coverage.SkippedCodeYears != 1 {
		t.Fatalf("unexpected code-year coverage: %+v", coverage)
	}
	if len(coverage.Failures) != 1 || coverage.Failures[0].Code != "sh600000" ||
		coverage.Failures[0].Year != 2023 || coverage.Failures[0].Stage != "day" {
		t.Fatalf("unexpected failures: %+v", coverage.Failures)
	}
	// 部分年份成功时该票仍算完成，进度不得报整票失败
	if len(progress) != 1 || progress[0].Done != 1 || progress[0].Failure != nil {
		t.Fatalf("unexpected progress: %+v", progress)
	}
}

// TestForEachCodeYearDataReportsWholeCodeFailure 整票所有年份失败时进度报失败，
// 失败明细区分年份。
func TestForEachCodeYearDataReportsWholeCodeFailure(t *testing.T) {
	progress := make([]Progress, 0, 1)
	coverage, err := ForEachCodeYearData(context.Background(), Config{
		Codes:        []string{"sh600003"},
		Years:        []int{2024, 2025},
		Workers:      1,
		DataMode:     DailyClose,
		GetDayKlines: func(string, time.Time, time.Time) (extend.Klines, error) { return nil, nil },
		OnCodeDone:   func(p Progress) { progress = append(progress, p) },
	}, func(string, int, YearData) { t.Error("整票无数据时不应回调") })
	if err != nil {
		t.Fatal(err)
	}
	if coverage.CompletedCodes != 0 || coverage.CompletedCodeYears != 0 || coverage.SkippedCodeYears != 2 {
		t.Fatalf("unexpected coverage: %+v", coverage)
	}
	if len(coverage.Failures) != 2 {
		t.Fatalf("expected 2 code-year failures, got %+v", coverage.Failures)
	}
	if len(progress) != 1 || progress[0].Failure == nil || progress[0].Failure.Code != "sh600003" {
		t.Fatalf("unexpected progress: %+v", progress)
	}
}

// TestForEachCodeYearDataCancelInsideFnStopsWithoutMissingCounts 回调内取消后
// 不再访问后续年份，也不把未访问年份计为普通缺失。
func TestForEachCodeYearDataCancelInsideFnStopsWithoutMissingCounts(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	visited := []int(nil)
	coverage, err := ForEachCodeYearData(ctx, Config{
		Codes:    []string{"sh600000"},
		Years:    []int{2024, 2025},
		Workers:  1,
		DataMode: DailyClose,
		GetDayKlines: func(_ string, _, end time.Time) (extend.Klines, error) {
			return klinesInYear(end.Year()), nil
		},
	}, func(_ string, year int, _ YearData) {
		visited = append(visited, year)
		cancel()
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context cancellation, got %v", err)
	}
	if len(visited) != 1 || visited[0] != 2024 {
		t.Fatalf("expected only the first year to be visited, got %v", visited)
	}
	if coverage.CompletedCodes != 0 || coverage.CompletedCodeYears != 0 || coverage.SkippedCodeYears != 0 {
		t.Fatalf("取消不得计入覆盖率: %+v", coverage)
	}
}

func TestForEachCodeYearDataValidatesConfig(t *testing.T) {
	day := func(string, time.Time, time.Time) (extend.Klines, error) { return testKlines(), nil }
	cases := []struct {
		name string
		cfg  Config
	}{
		{"empty years", Config{Codes: []string{"sh600000"}, DataMode: DailyClose, GetDayKlines: day}},
		{"nil day provider", Config{Codes: []string{"sh600000"}, Years: []int{2024}, DataMode: DailyClose}},
		{"intraday without minute provider", Config{Codes: []string{"sh600000"}, Years: []int{2024}, DataMode: Intraday, GetDayKlines: day}},
		{"unsupported data mode", Config{Codes: []string{"sh600000"}, Years: []int{2024}, DataMode: 99, GetDayKlines: day}},
	}
	for _, tc := range cases {
		if _, err := ForEachCodeYearData(context.Background(), tc.cfg, func(string, int, YearData) {}); err == nil {
			t.Fatalf("%s: expected validation error", tc.name)
		}
	}
	coverage, err := ForEachCodeYearData(context.Background(), Config{
		Years: []int{2024}, DataMode: DailyClose, GetDayKlines: day,
	}, func(string, int, YearData) {})
	if err != nil {
		t.Fatalf("expected empty codes to succeed, got %v", err)
	}
	if coverage.RequestedCodeYears != 0 || coverage.CompletedCodes != 0 {
		t.Fatalf("unexpected coverage for empty codes: %+v", coverage)
	}
}
