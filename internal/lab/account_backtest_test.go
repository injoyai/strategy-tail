package lab

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	common "github.com/injoyai/strategy-tail"
	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/internal/portfolioresearch"
	"github.com/injoyai/strategy-tail/internal/researchrun"
	"github.com/injoyai/strategy-tail/lib/extend"
	ss "github.com/injoyai/strategy-tail/strategies/sell"
	"github.com/injoyai/tdx/v2/protocol"
)

type accountBuyer struct{ onlyDate string }

func (accountBuyer) Name() string { return "account buyer" }
func (b accountBuyer) Buy(_ string, ks extend.Klines) bool {
	return b.onlyDate == "" || ks[len(ks)-1].Time.Format(time.DateOnly) == b.onlyDate
}

type accountNeverSell struct{}

func (accountNeverSell) Name() string                              { return "hold" }
func (accountNeverSell) Sell(string, extend.Klines, core.Buy) bool { return false }

type accountTestSeller func(string, extend.Klines, core.Buy) bool

func (accountTestSeller) Name() string { return "account test seller" }
func (f accountTestSeller) Sell(code string, ks extend.Klines, buy core.Buy) bool {
	return f(code, ks, buy)
}

func accountBars(dates []string, prices ...float64) extend.Klines {
	ks := make(extend.Klines, len(prices))
	for i, p := range prices {
		d, _ := time.ParseInLocation(time.DateTime, dates[i]+" 15:00:00", time.Local)
		v := p
		ks[i] = &extend.Kline{Unix: d.Unix(), Kline: &protocol.Kline{Time: d, Open: v, Close: v, High: v, Low: v, Volume: 1000}}
	}
	return ks
}

func accountTestConfig(data map[string]extend.Klines, buyer core.Buyer, seller core.Seller) researchrun.Config {
	codes := []string{}
	for code := range data {
		if code != "sh000001" {
			codes = append(codes, code)
		}
	}
	return researchrun.Config{Codes: codes, Years: []int{2025},
		Variants: []researchrun.Variant{{Name: "test", Buyer: buyer}}, DefaultSeller: seller,
		GetDayKlines: func(code string, start, end time.Time) (extend.Klines, error) {
			var out extend.Klines
			for _, k := range data[code] {
				if k == nil || k.Kline == nil {
					out = append(out, k)
					continue
				}
				if !k.Time.Before(start) && !k.Time.After(end) {
					out = append(out, k)
				}
			}
			return out, nil
		},
		GetMinKlines: func(string, time.Time, time.Time) (protocol.Klines, error) { return nil, nil },
	}
}

func nearAccount(t *testing.T, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-7 {
		t.Fatalf("got %.10f want %.10f", got, want)
	}
}

func TestAccountCloseSignalBuysAtNextOpen(t *testing.T) {
	dates := []string{"2025-01-02", "2025-01-03"}
	bars := accountBars(dates, 10, 13)
	bars[1].Open = 12
	cfg := accountTestConfig(map[string]extend.Klines{
		"sh000001": accountBars(dates, 100, 101),
		"sh600001": bars,
	}, accountBuyer{onlyDate: dates[0]}, accountNeverSell{})
	r, accounts, _, _, _, err := runAccountBacktest(context.Background(), cfg, AccountConfig{100000, 5, 20000})
	if err != nil {
		t.Fatal(err)
	}
	ts := r.Results[0].Trades
	if len(ts) != 1 || ts[0].BuyTime.Format(time.DateTime) != dates[1]+" 09:30:00" || ts[0].BuyPrice != 12 {
		t.Fatalf("收盘信号未按次日开盘成交: %+v", ts)
	}
	nearAccount(t, accounts[0].Points[0].Cash, 100000)
}

// TestAccountBacktestKeepsEarlyYearWhenLaterYearEmpty 后续年份无行情时，
// 账户回测仍使用早年有效样本，且不重复拼接预热日线。
func TestAccountBacktestKeepsEarlyYearWhenLaterYearEmpty(t *testing.T) {
	dates := []string{"2024-01-02", "2024-01-03", "2024-01-04"}
	cfg := accountTestConfig(map[string]extend.Klines{
		"sh000001": accountBars(dates, 100, 101, 102),
		"sh600001": accountBars(dates, 10, 10.5, 11),
	}, accountBuyer{onlyDate: dates[1]}, accountNeverSell{})
	cfg.Years = []int{2024, 2025}
	r, _, _, _, loaded, err := runAccountBacktest(context.Background(), cfg, AccountConfig{100000, 5, 20000})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Results[0].Trades) == 0 {
		t.Fatal("2024 年有效样本应产生交易")
	}
	if r.Coverage.Completed != 1 || r.Coverage.Skipped != 0 {
		t.Fatalf("2024 样本不得因 2025 无行情被排除: %+v", r.Coverage)
	}
	if len(r.Coverage.Failures) != 1 || r.Coverage.Failures[0].Year != 2025 ||
		r.Coverage.Failures[0].Stage != "empty" {
		t.Fatalf("空年份未披露: %+v", r.Coverage.Failures)
	}
	ks := loaded["sh600001"]
	if len(ks) != len(dates) {
		t.Fatalf("加载日线数 = %d, want %d", len(ks), len(dates))
	}
	for i := 1; i < len(ks); i++ {
		if !ks[i].Time.After(ks[i-1].Time) {
			t.Fatalf("日线重复或乱序: %v -> %v", ks[i-1].Time, ks[i].Time)
		}
	}
}

// TestAccountBacktestCrossYearKeepsPositionsAndStableTrades 多年数据只使用
// 最早有效年份的预热，日线不重复；跨年持仓和现金连续；延长结束年份不得
// 改写原区间内已发生的实际成交。
func TestAccountBacktestCrossYearKeepsPositionsAndStableTrades(t *testing.T) {
	dates := []string{
		"2024-01-02", "2024-01-03", "2024-01-04",
		"2025-01-02", "2025-01-03",
	}
	base := accountTestConfig(map[string]extend.Klines{
		"sh000001": accountBars(dates, 100, 101, 102, 103, 104),
		"sh600001": accountBars(dates, 10, 10.5, 11, 11.5, 12),
	}, accountBuyer{onlyDate: dates[0]}, accountNeverSell{})
	base.Years = []int{2024, 2025}
	r, reports, _, _, loaded, err := runAccountBacktest(context.Background(), base, AccountConfig{100000, 5, 20000})
	if err != nil {
		t.Fatal(err)
	}
	ks := loaded["sh600001"]
	if len(ks) != len(dates) {
		t.Fatalf("多年加载日线数 = %d, want %d", len(ks), len(dates))
	}
	for i := 1; i < len(ks); i++ {
		if !ks[i].Time.After(ks[i-1].Time) {
			t.Fatalf("多年日线重复或乱序: %v -> %v", ks[i-1].Time, ks[i].Time)
		}
	}
	points := reports[0].Points
	if len(points) != len(dates) {
		t.Fatalf("账户点数 = %d, want %d", len(points), len(dates))
	}
	for i := 1; i < len(points); i++ {
		if points[i].Holdings != 1 {
			t.Fatalf("%s 跨年持仓丢失: %+v", points[i].Date, points[i])
		}
	}
	nearAccount(t, points[1].Cash, points[3].Cash)

	// 原区间运行（只请求 2024）应产生同一笔实际买入；终点虚拟平仓不计入比较。
	short := base
	short.Years = []int{2024}
	shortReport, _, _, _, _, err := runAccountBacktest(context.Background(), short, AccountConfig{100000, 5, 20000})
	if err != nil {
		t.Fatal(err)
	}
	actual := func(trades []core.Trade) []core.Trade {
		var out []core.Trade
		for _, trade := range trades {
			if !trade.Virtual {
				out = append(out, trade)
			}
		}
		return out
	}
	if !reflect.DeepEqual(actual(r.Results[0].Trades), actual(shortReport.Results[0].Trades)) {
		t.Fatalf("延长结束年份改写了原区间成交: %+v vs %+v", actual(r.Results[0].Trades), actual(shortReport.Results[0].Trades))
	}
}

func TestAccountOpeningBuySkipsSuspensionAndLimit(t *testing.T) {
	dates := []string{"2025-01-02", "2025-01-03", "2025-01-06"}
	data := map[string]extend.Klines{
		"sh000001": accountBars(dates, 100, 101, 102),
		"sh600001": accountBars(dates, 10, 11, 10),
		"sh600002": accountBars(dates, 10, 10, 10),
		"sh600003": accountBars([]string{dates[0], dates[2]}, 10, 10),
	}
	data["sh600001"][1].Low = 10.5 // 开盘涨停，即使日内开板也不能补作开盘成交。
	data["sh600002"][1].Volume = 0
	cfg := accountTestConfig(data, accountBuyer{onlyDate: dates[0]}, accountNeverSell{})
	r, reports, _, _, _, err := runAccountBacktest(context.Background(), cfg, AccountConfig{100000, 5, 20000})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Results[0].Trades) != 0 || reports[0].UnfilledBuys != 3 {
		t.Fatalf("不可成交的买单未在开盘失效: %+v, %+v", r.Results[0].Trades, reports[0])
	}
}

func TestAccountUnfilledDailySellCarriesPastLimit(t *testing.T) {
	dates := []string{"2025-01-02", "2025-01-03", "2025-01-06", "2025-01-07", "2025-01-08"}
	cfg := accountTestConfig(map[string]extend.Klines{
		"sh000001": accountBars(dates, 100, 101, 102, 103, 104),
		"sh600001": accountBars(dates, 10, 10, 10, 9, 9.2),
	}, accountBuyer{onlyDate: dates[0]}, dateCodeSeller{"sh600001": {dates[2]: true}})
	r, reports, _, _, _, err := runAccountBacktest(context.Background(), cfg, AccountConfig{100000, 5, 20000})
	if err != nil {
		t.Fatal(err)
	}
	ts := r.Results[0].Trades
	if len(ts) != 1 || ts[0].Virtual || ts[0].SellPrice != 9.2 ||
		ts[0].SellTime.Format(time.DateTime) != dates[4]+" 09:30:00" || reports[0].Points[3].Holdings != 1 {
		t.Fatalf("卖单未正确跨过跌停日: %+v", ts)
	}
}

func TestAccountBacktestProgressByPhase(t *testing.T) {
	dates := []string{"2025-01-02", "2025-01-03", "2025-01-06"}
	cfg := accountTestConfig(map[string]extend.Klines{
		"sh000001": accountBars(dates, 100, 101, 102),
		"sh600001": accountBars(dates, 10, 11, 12),
	}, accountBuyer{}, accountNeverSell{})
	// 重复代码不多计，缺失股票仍计入已处理数量。
	cfg.Codes = []string{"sh600001", "600001", "sh600002"}
	var progress []researchrun.Progress
	cfg.OnCodeDone = func(p researchrun.Progress) { progress = append(progress, p) }
	_, _, _, _, _, err := runAccountBacktest(context.Background(), cfg, AccountConfig{100000, 5, 20000})
	if err != nil {
		t.Fatal(err)
	}
	if len(progress) != 7 {
		t.Fatalf("unexpected progress: %+v", progress)
	}
	for i, p := range progress {
		phase, done, total := "loading", i, 2
		if i >= 3 {
			phase, done, total = "simulating", i-3, 3
		}
		if p.Phase != phase || p.Done != done || p.Total != total {
			t.Fatalf("progress[%d] = %+v, want %s %d/%d", i, p, phase, done, total)
		}
		if i > 3 && p.Code != dates[i-4] {
			t.Fatalf("simulation date: %+v", p)
		}
	}
	if progress[1].Failure == nil && progress[2].Failure == nil {
		t.Fatal("missing stock failure was lost")
	}
}

func TestAccountCashBudgetLotsAndDailyValuation(t *testing.T) {
	dates := []string{"2025-01-02", "2025-01-03"}
	data := map[string]extend.Klines{"sh000001": accountBars(dates, 100, 110), "sh600001": accountBars(dates, 10, 12)}
	data["sh600001"][1].Open = 10
	cfg := accountTestConfig(data, accountBuyer{}, accountNeverSell{})
	cfg.Cost = core.Cost{MinCommission: 5, StampDutyRate: .001, Slippage: .01}
	r, accounts, _, benchmark, _, err := runAccountBacktest(context.Background(), cfg, AccountConfig{InitialCapital: 100000, MaxPositions: 5, OrderBudget: 20000})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Results[0].Trades) != 1 {
		t.Fatalf("trades: %+v", r.Results)
	}
	tr := r.Results[0].Trades[0]
	if tr.Quantity != 1900 || !tr.Virtual {
		t.Fatalf("unexpected holding: %+v", tr)
	}
	nearAccount(t, tr.BuyCost, 19024)
	nearAccount(t, accounts[0].Points[0].Cash, 100000)
	nearAccount(t, accounts[0].Points[0].Equity, 100000)
	nearAccount(t, accounts[0].Points[1].Cash, 80976)
	nearAccount(t, accounts[0].Points[1].Equity, 103776)
	nearAccount(t, accounts[0].Points[1].Return, 3.776)
	nearAccount(t, benchmark[1].Return, 10)
	nearAccount(t, tr.SellIncome, 22800) // 期末估值不虚构卖出费用。
	if data["sh600001"][1].Close != 12 {
		t.Fatal("source K-lines changed")
	}
}

func TestAccountPriorityPositionsAndInsufficientCash(t *testing.T) {
	dates := []string{"2025-01-02", "2025-01-03"}
	data := map[string]extend.Klines{"sh000001": accountBars(dates, 100, 100)}
	for _, c := range []string{"sh600003", "sh600001", "sh600002"} {
		data[c] = accountBars(dates, 10, 10)
	}
	cfg := accountTestConfig(data, accountBuyer{}, accountNeverSell{})
	cfg.Codes = []string{"sh600003", "sh600002", "sh600001", "sh600001"}
	r, accounts, _, _, _, err := runAccountBacktest(context.Background(), cfg, AccountConfig{InitialCapital: 25000, MaxPositions: 2, OrderBudget: 20000})
	if err != nil {
		t.Fatal(err)
	}
	trades := r.Results[0].Trades
	if len(trades) != 2 || trades[0].Code != "sh600001" || trades[0].Quantity != 2000 || trades[1].Code != "sh600002" || trades[1].Quantity != 500 {
		t.Fatalf("priority/partial fill: %+v", trades)
	}
	nearAccount(t, accounts[0].Points[1].Cash, 0)
	if accounts[0].Points[1].Holdings != 2 {
		t.Fatal("position limit")
	}
	if r.Coverage.Requested != 3 {
		t.Fatal("duplicate code counted twice")
	}
	r, accounts, _, _, _, err = runAccountBacktest(context.Background(), cfg, AccountConfig{InitialCapital: 100000, MaxPositions: 2, OrderBudget: 20000})
	if err != nil || len(r.Results[0].Trades) != 2 {
		t.Fatalf("position limit ignored despite spare cash: %v", err)
	}
	nearAccount(t, accounts[0].Points[1].Cash, 60000)
}

func TestAccountFactorScoreDirectionControlsNextOpenPriority(t *testing.T) {
	dates := []string{"2025-01-02", "2025-01-03", "2025-01-06"}
	data := map[string]extend.Klines{
		"sh000001": accountBars(dates, 100, 100, 100),
		"sh600001": accountBars(dates, 10, 10, 10),
		"sh600002": accountBars(dates, 10, 10.5, 10),
		"sh600003": accountBars(dates, 10, 9.5, 10),
	}
	account := AccountConfig{InitialCapital: 20000, MaxPositions: 1, OrderBudget: 20000}
	for _, tc := range []struct {
		direction string
		code      string
	}{
		{portfolioresearch.DirectionHigherIsBetter, "sh600002"},
		{portfolioresearch.DirectionLowerIsBetter, "sh600003"},
	} {
		t.Run(tc.direction, func(t *testing.T) {
			spec := StrategySpec{
				Version: strategySpecVersion,
				Run:     StrategyRunSpec{Account: &account},
				FactorScores: []FactorScoreSpec{
					{Kind: "momentum", Days: 1, Direction: tc.direction},
				},
			}
			if err := spec.Validate(); err != nil {
				t.Fatal(err)
			}
			variants, err := spec.RecordVariants()
			if err != nil || len(variants) != 2 || variants[1].Name != "因子评分" {
				t.Fatalf("expected baseline and scored variants, got %v (%v)", variants, err)
			}
			factors, err := spec.buildScoreFactors()
			if err != nil {
				t.Fatal(err)
			}
			base := accountBuyer{onlyDate: dates[1]}
			cfg := accountTestConfig(data, base, accountNeverSell{})
			cfg.Variants = append(cfg.Variants, researchrun.Variant{
				Name: variants[1].Name, Buyer: scoredAccountBuyer{Buyer: base, factors: factors},
			})
			r, _, _, _, _, err := runAccountBacktest(context.Background(), cfg, account)
			if err != nil {
				t.Fatal(err)
			}
			if trades := r.Results[0].Trades; len(trades) != 1 || trades[0].Code != "sh600001" {
				t.Fatalf("baseline priority changed: %+v", trades)
			}
			if trades := r.Results[1].Trades; len(trades) != 1 || trades[0].Code != tc.code ||
				trades[0].BuyTime.Format(time.DateTime) != dates[2]+" 09:30:00" {
				t.Fatalf("expected %s first at the next open, got %+v", tc.code, trades)
			}
		})
	}
}

func TestAccountMinuteSignalUsesLaterPriceAndRebuyWaitsForOpen(t *testing.T) {
	dates := []string{"2025-01-02", "2025-01-03", "2025-01-06", "2025-01-07"}
	data := map[string]extend.Klines{"sh000001": accountBars(dates, 100, 101, 102, 103), "sh600001": accountBars(dates, 10, 10, 12, 20)}
	data["sh600001"][2].FloatStock = 10_000_000
	data["sh600001"][2].Turnover = 5
	baseSeller := ss.A持仓N天{Days: 1}
	cfg := accountTestConfig(data, accountBuyer{}, accountTestSeller(func(code string, ks extend.Klines, buy core.Buy) bool {
		current := ks[len(ks)-1]
		if current.Time.Format(time.DateTime) != dates[2]+" 09:31:00" || current.Unix != current.Time.Unix() || current.Turnover != 1 {
			t.Errorf("卖出信号应读取当前分钟的时间和累计换手率: %+v", current)
		}
		return baseSeller.Sell(code, ks, buy)
	}))
	cfg.GetMinKlines = func(string, time.Time, time.Time) (protocol.Klines, error) {
		signal, _ := time.ParseInLocation(time.DateTime, dates[2]+" 09:31:00", time.Local)
		return protocol.Klines{
			&protocol.Kline{Time: signal, Open: 10, Close: 10, High: 10, Low: 10, Volume: 1000},
			&protocol.Kline{Time: signal.Add(time.Minute), Open: 10, Close: 10.8, High: 10.8, Low: 10, Volume: 1000},
		}, nil
	}
	r, accounts, _, _, _, err := runAccountBacktest(context.Background(), cfg, AccountConfig{InitialCapital: 10000, MaxPositions: 1, OrderBudget: 10000})
	if err != nil {
		t.Fatal(err)
	}
	ts := r.Results[0].Trades
	if len(ts) != 2 || ts[0].Virtual || !ts[1].Virtual ||
		ts[0].SellTime.Format(time.DateTime) != dates[2]+" 09:32:00" || ts[0].SellPrice != 10.8 ||
		ts[1].BuyTime.Format(time.DateTime) != dates[3]+" 09:30:00" || ts[1].Quantity != 500 {
		t.Fatalf("sells/rebuy: %+v", ts)
	}
	nearAccount(t, accounts[0].Points[2].Cash, 10800)
	nearAccount(t, accounts[0].Points[3].Cash, 800)
	if data["sh600001"][2].Close != 12 {
		t.Fatal("minute sell contaminated daily close")
	}
}

func TestAccountCarriesHoldingsAndCashAcrossYears(t *testing.T) {
	dates := []string{"2025-12-31", "2026-01-02"}
	data := map[string]extend.Klines{"sh000001": accountBars(dates, 100, 110), "sh600001": accountBars(dates, 10, 12)}
	cfg := accountTestConfig(data, accountBuyer{onlyDate: dates[0]}, accountNeverSell{})
	cfg.Years = []int{2025, 2026}
	data["sh600001"][1].Open = 10
	r, accounts, _, _, _, err := runAccountBacktest(context.Background(), cfg, AccountConfig{InitialCapital: 100000, MaxPositions: 5, OrderBudget: 20000})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Results[0].Trades) != 1 {
		t.Fatal("year-end liquidation")
	}
	nearAccount(t, accounts[0].Points[1].Equity, 104000)
	nearAccount(t, accounts[0].Points[1].Cash, 80000)
}

func TestAccountIndependentVariantsNoTradesAndCancellation(t *testing.T) {
	dates := []string{"2025-01-02", "2025-01-03"}
	data := map[string]extend.Klines{"sh000001": accountBars(dates, 100, 110), "sh600001": accountBars(dates, 10, 12)}
	cfg := accountTestConfig(data, accountBuyer{}, accountNeverSell{})
	cfg.Variants = append(cfg.Variants, researchrun.Variant{Name: "none", Buyer: accountBuyer{onlyDate: "1999-01-01"}})
	account := AccountConfig{InitialCapital: 100000, MaxPositions: 5, OrderBudget: 20000}
	r, a, _, _, _, err := runAccountBacktest(context.Background(), cfg, account)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Results[1].Trades) != 0 || len(a[1].Points) != 2 {
		t.Fatal("empty strategy lost account curve")
	}
	nearAccount(t, a[1].Points[1].Return, 0)
	r2, a2, _, _, _, err := runAccountBacktest(context.Background(), cfg, account)
	if err != nil || !reflect.DeepEqual(r, r2) || !reflect.DeepEqual(a, a2) {
		t.Fatal("non-deterministic result")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, _, _, _, err := runAccountBacktest(ctx, cfg, account); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel: %v", err)
	}
}

func TestAccountRejectsInvalidConfigAndMissingBenchmark(t *testing.T) {
	for _, c := range []AccountConfig{{}, {100000, 0, 20000}, {math.Inf(1), 5, 20000}, {100000, 5, math.NaN()}, {100000, 5, -1}} {
		if c.Validate() == nil {
			t.Fatalf("accepted %+v", c)
		}
	}
	dates := []string{"2025-01-02"}
	cfg := accountTestConfig(map[string]extend.Klines{"sh600001": accountBars(dates, 10)}, accountBuyer{}, accountNeverSell{})
	if _, _, _, _, _, err := runAccountBacktest(context.Background(), cfg, AccountConfig{100000, 5, 20000}); err == nil {
		t.Fatal("missing benchmark silently accepted")
	}
}

func TestAccountValuationGapsAndBenchmarkCoverage(t *testing.T) {
	dates := []string{"2025-01-02", "2025-01-03", "2025-01-06", "2025-01-07"}
	data := map[string]extend.Klines{"sh000001": accountBars(dates, 100, 105, 110, 111),
		"sh600001": accountBars([]string{dates[0], dates[1], dates[3]}, 10, 10, 12)}
	cfg := accountTestConfig(data, accountBuyer{}, accountNeverSell{})
	account := AccountConfig{100000, 5, 20000}
	r, a, _, _, _, err := runAccountBacktest(context.Background(), cfg, account)
	if err != nil {
		t.Fatal(err)
	}
	if a[0].StaleValuationDays != 1 || a[0].MissingMinuteDays != 1 {
		t.Fatalf("quality: %+v", a[0])
	}
	nearAccount(t, a[0].Points[2].Equity, 100000)
	var profit float64
	for _, tr := range r.Results[0].Trades {
		profit += tr.ProfitAmount()
	}
	nearAccount(t, profit, a[0].Points[3].Equity-account.InitialCapital)
	data["sh000001"] = accountBars([]string{dates[0], dates[2], dates[3]}, 100, 110, 111)
	if _, _, _, _, _, err := runAccountBacktest(context.Background(), cfg, account); err == nil {
		t.Fatal("missing index day accepted")
	}
	data["sh000001"] = extend.Klines{nil, data["sh000001"][0]}
	if _, _, _, _, _, err := runAccountBacktest(context.Background(), cfg, account); err == nil {
		t.Fatal("invalid index day accepted")
	}
}

func TestAccountReportRoundTripAndSimpleConfig(t *testing.T) {
	account := &AccountConfig{100000, 5, 20000}
	spec := simpleE2ESpec()
	spec.Run.Account = account
	if spec.RunConfig().Account != account {
		t.Fatal("simple mode lost account config")
	}
	cfg := spec.RunConfig()
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	cfg.Account = &AccountConfig{}
	if cfg.Validate() == nil {
		t.Fatal("run config skipped account validation")
	}
	rep := Report{Config: spec.RunConfig(), Benchmark: []BenchmarkPoint{{"2025-01-02", 0}},
		Variants: []VariantReport{{Name: "account", Account: &AccountReport{Points: []AccountPoint{{Date: "2025-01-02", Equity: 100000, Cash: 100000}}}}}}
	b, err := json.Marshal(rep)
	if err != nil {
		t.Fatal(err)
	}
	var restored Report
	if err := json.Unmarshal(b, &restored); err != nil || !reflect.DeepEqual(rep, restored) {
		t.Fatalf("report round trip: %v", err)
	}
}

func TestAccountBuilderAPIReport(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	old := common.Pull
	common.Pull = fixturePull(t, dir)
	t.Cleanup(func() { common.Pull = old })
	base := time.Date(2025, 1, 2, 0, 0, 0, 0, time.Local)
	writeFakeDayDB(t, dir, "sh600000", 3, base)
	writeFakeDayDB(t, dir, "sh000001", 3, base)
	s := NewServer()
	cfg := RunConfig{StartYear: 2025, EndYear: 2025, SampleMode: "codes", SampleCodes: []string{"sh600000"},
		Account: &AccountConfig{100000, 5, 20000},
		Builder: &BuilderSpec{Version: 1, Name: "账户测试", Buy: &BuilderNode{Type: "condition", ComponentID: "buy_all"}, HoldToEnd: true}}
	invalid := cfg
	invalid.Account = &AccountConfig{}
	doReq(t, s.Handler(), http.MethodPost, "/api/builder/run", invalid, http.StatusBadRequest)
	doReq(t, s.Handler(), http.MethodPost, "/api/builder/run", cfg, http.StatusOK)
	waitRunnerDone(t, s.runner)
	b := doReq(t, s.Handler(), http.MethodGet, "/api/report/latest", nil, http.StatusOK)
	var rep Report
	if err := json.Unmarshal(b, &rep); err != nil {
		t.Fatal(err)
	}
	if rep.Source != sourceBuilder || !reflect.DeepEqual(rep.Config.Account, cfg.Account) || len(rep.Benchmark) != 3 || len(rep.Variants) != 1 {
		t.Fatalf("account report metadata lost: %+v", rep)
	}
	v := rep.Variants[0]
	if v.Account == nil || len(v.Account.Points) != 3 || len(v.Trades) != 1 || v.Trades[0].Quantity <= 100 || !v.Trades[0].Virtual {
		t.Fatalf("account result lost: %+v", v)
	}
	nearAccount(t, v.Account.Points[2].Equity-cfg.Account.InitialCapital, v.Trades[0].Profit)
	stored, err := os.ReadFile(filepath.Join("output", "trades", rep.RunID(), "report.json"))
	if err != nil {
		t.Fatal(err)
	}
	var disk Report
	if err := json.Unmarshal(stored, &disk); err != nil || !reflect.DeepEqual(rep, disk) {
		t.Fatalf("saved account report differs: %v", err)
	}
}
