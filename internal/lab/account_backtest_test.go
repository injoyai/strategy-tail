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
	"github.com/injoyai/strategy-tail/internal/researchrun"
	"github.com/injoyai/strategy-tail/lib/extend"
	ss "github.com/injoyai/strategy-tail/strategies/sell"
	"github.com/injoyai/tdx/protocol"
)

type accountBuyer struct{ onlyDate string }

func (accountBuyer) Name() string { return "account buyer" }
func (b accountBuyer) Buy(_ string, ks extend.Klines) bool {
	return b.onlyDate == "" || ks[len(ks)-1].Time.Format(time.DateOnly) == b.onlyDate
}

type accountNeverSell struct{}

func (accountNeverSell) Name() string                              { return "hold" }
func (accountNeverSell) Sell(string, extend.Klines, core.Buy) bool { return false }

func accountBars(dates []string, prices ...float64) extend.Klines {
	ks := make(extend.Klines, len(prices))
	for i, p := range prices {
		d, _ := time.ParseInLocation(time.DateOnly, dates[i], time.Local)
		v := protocol.Yuan(p)
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
	_, _, _, err := runAccountBacktest(context.Background(), cfg, AccountConfig{100000, 5, 20000})
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
	cfg := accountTestConfig(data, accountBuyer{}, accountNeverSell{})
	cfg.Cost = core.Cost{MinCommission: 5, StampDutyRate: .001, Slippage: protocol.Yuan(.01)}
	r, accounts, benchmark, err := runAccountBacktest(context.Background(), cfg, AccountConfig{InitialCapital: 100000, MaxPositions: 5, OrderBudget: 20000})
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
	nearAccount(t, accounts[0].Points[0].Cash, 80976)
	nearAccount(t, accounts[0].Points[0].Equity, 99976)
	nearAccount(t, accounts[0].Points[1].Equity, 103776)
	nearAccount(t, accounts[0].Points[1].Return, 3.776)
	nearAccount(t, benchmark[1].Return, 10)
	nearAccount(t, tr.SellIncome, 22800) // 期末估值不虚构卖出费用。
	if data["sh600001"][1].Close != protocol.Yuan(12) {
		t.Fatal("source K-lines changed")
	}
}

func TestAccountPriorityPositionsAndInsufficientCash(t *testing.T) {
	dates := []string{"2025-01-02"}
	data := map[string]extend.Klines{"sh000001": accountBars(dates, 100)}
	for _, c := range []string{"sh600003", "sh600001", "sh600002"} {
		data[c] = accountBars(dates, 10)
	}
	cfg := accountTestConfig(data, accountBuyer{}, accountNeverSell{})
	cfg.Codes = []string{"sh600003", "sh600002", "sh600001", "sh600001"}
	r, accounts, _, err := runAccountBacktest(context.Background(), cfg, AccountConfig{InitialCapital: 25000, MaxPositions: 2, OrderBudget: 20000})
	if err != nil {
		t.Fatal(err)
	}
	trades := r.Results[0].Trades
	if len(trades) != 2 || trades[0].Code != "sh600001" || trades[0].Quantity != 2000 || trades[1].Code != "sh600002" || trades[1].Quantity != 500 {
		t.Fatalf("priority/partial fill: %+v", trades)
	}
	nearAccount(t, accounts[0].Points[0].Cash, 0)
	if accounts[0].Points[0].Holdings != 2 {
		t.Fatal("position limit")
	}
	if r.Coverage.Requested != 3 {
		t.Fatal("duplicate code counted twice")
	}
	r, accounts, _, err = runAccountBacktest(context.Background(), cfg, AccountConfig{InitialCapital: 100000, MaxPositions: 2, OrderBudget: 20000})
	if err != nil || len(r.Results[0].Trades) != 2 {
		t.Fatalf("position limit ignored despite spare cash: %v", err)
	}
	nearAccount(t, accounts[0].Points[0].Cash, 60000)
}

func TestAccountSellsBeforeRebuyAndUsesMinutePrice(t *testing.T) {
	dates := []string{"2025-01-02", "2025-01-03"}
	data := map[string]extend.Klines{"sh000001": accountBars(dates, 100, 101), "sh600001": accountBars(dates, 10, 20)}
	cfg := accountTestConfig(data, accountBuyer{}, ss.A持仓N天{Days: 1})
	d, _ := time.ParseInLocation(time.DateTime, "2025-01-03 09:31:00", time.Local)
	cfg.GetMinKlines = func(string, time.Time, time.Time) (protocol.Klines, error) {
		p := protocol.Yuan(11)
		return protocol.Klines{&protocol.Kline{Time: d, Open: p, Close: p, High: p, Low: p}}, nil
	}
	r, accounts, _, err := runAccountBacktest(context.Background(), cfg, AccountConfig{InitialCapital: 10000, MaxPositions: 1, OrderBudget: 10000})
	if err != nil {
		t.Fatal(err)
	}
	ts := r.Results[0].Trades
	if len(ts) != 2 || ts[0].Virtual || !ts[1].Virtual || !ts[0].SellTime.Equal(d) || ts[1].Quantity != 500 {
		t.Fatalf("sells/rebuy: %+v", ts)
	}
	nearAccount(t, accounts[0].Points[1].Equity, 11000)
	nearAccount(t, accounts[0].Points[1].Cash, 1000)
	if data["sh600001"][1].Close != protocol.Yuan(20) {
		t.Fatal("minute sell contaminated buy close")
	}
}

func TestAccountCarriesHoldingsAndCashAcrossYears(t *testing.T) {
	dates := []string{"2025-12-31", "2026-01-02"}
	data := map[string]extend.Klines{"sh000001": accountBars(dates, 100, 110), "sh600001": accountBars(dates, 10, 12)}
	cfg := accountTestConfig(data, accountBuyer{onlyDate: dates[0]}, accountNeverSell{})
	cfg.Years = []int{2025, 2026}
	r, accounts, _, err := runAccountBacktest(context.Background(), cfg, AccountConfig{InitialCapital: 100000, MaxPositions: 5, OrderBudget: 20000})
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
	r, a, _, err := runAccountBacktest(context.Background(), cfg, account)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Results[1].Trades) != 0 || len(a[1].Points) != 2 {
		t.Fatal("empty strategy lost account curve")
	}
	nearAccount(t, a[1].Points[1].Return, 0)
	r2, a2, _, err := runAccountBacktest(context.Background(), cfg, account)
	if err != nil || !reflect.DeepEqual(r, r2) || !reflect.DeepEqual(a, a2) {
		t.Fatal("non-deterministic result")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, _, err := runAccountBacktest(ctx, cfg, account); !errors.Is(err, context.Canceled) {
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
	if _, _, _, err := runAccountBacktest(context.Background(), cfg, AccountConfig{100000, 5, 20000}); err == nil {
		t.Fatal("missing benchmark silently accepted")
	}
}

func TestAccountValuationGapsAndBenchmarkCoverage(t *testing.T) {
	dates := []string{"2025-01-02", "2025-01-03", "2025-01-06"}
	data := map[string]extend.Klines{"sh000001": accountBars(dates, 100, 105, 110), "sh600001": accountBars([]string{dates[0], dates[2]}, 10, 12)}
	cfg := accountTestConfig(data, accountBuyer{}, accountNeverSell{})
	account := AccountConfig{100000, 5, 20000}
	r, a, _, err := runAccountBacktest(context.Background(), cfg, account)
	if err != nil {
		t.Fatal(err)
	}
	if a[0].StaleValuationDays != 1 || a[0].MissingMinuteDays != 1 {
		t.Fatalf("quality: %+v", a[0])
	}
	nearAccount(t, a[0].Points[1].Equity, 100000)
	var profit float64
	for _, tr := range r.Results[0].Trades {
		profit += tr.ProfitAmount()
	}
	nearAccount(t, profit, a[0].Points[2].Equity-account.InitialCapital)
	data["sh000001"] = accountBars([]string{dates[0], dates[2]}, 100, 110)
	data["sh600001"] = accountBars(dates, 10, 11, 12)
	if _, _, _, err := runAccountBacktest(context.Background(), cfg, account); err == nil {
		t.Fatal("missing index day accepted")
	}
	data["sh000001"] = extend.Klines{nil, data["sh000001"][0]}
	if _, _, _, err := runAccountBacktest(context.Background(), cfg, account); err == nil {
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
	common.Pull = &extend.PullKline{Config: extend.PullKlineConfig{Dir: dir}}
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
