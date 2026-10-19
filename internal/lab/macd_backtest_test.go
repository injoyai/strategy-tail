package lab

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"
	"time"

	common "github.com/injoyai/strategy-tail"
	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/internal/researchrun"
	"github.com/injoyai/strategy-tail/lib/extend"
	sb "github.com/injoyai/strategy-tail/strategies/buy"
	ss "github.com/injoyai/strategy-tail/strategies/sell"
	"github.com/injoyai/tdx/protocol"
)

func TestMACDReportExportsOriginalNetCosts(t *testing.T) {
	t.Chdir(t.TempDir())
	old := common.Pull
	common.Pull = &extend.PullKline{Config: extend.PullKlineConfig{Dir: t.TempDir()}}
	t.Cleanup(func() { common.Pull = old })
	trade := core.Trade{Code: "sh600000", BuyTime: time.Date(2025, 1, 2, 15, 0, 0, 0, time.Local), SellTime: time.Date(2025, 1, 3, 15, 0, 0, 0, time.Local), BuyPrice: protocol.Yuan(10), SellPrice: protocol.Yuan(11), BuyCost: 1005, SellIncome: 1090, Quantity: 100}
	cfg := defaultMACDConfig()
	rep := &Report{Config: cfg, Source: "macd", StartedAt: "2025-01-01T10:00:00+08:00", FinishedAt: "2025-01-01T10:00:01+08:00", Variants: []VariantReport{{Name: "MACD", Stats: newTradeStatsJSON(core.Stats([]core.Trade{trade})), Trades: toTradeJSON([]core.Trade{trade})}}}
	if err := saveReport(rep, nil, nil, []researchrun.Result{{Trades: []core.Trade{trade}}}); err != nil {
		t.Fatal(err)
	}
	paths, err := filepath.Glob(filepath.Join("output", "trades", "*", "*.csv"))
	if err != nil || len(paths) != 1 {
		t.Fatalf("csv paths %v: %v", paths, err)
	}
	f, err := os.Open(paths[0])
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	rows, err := csv.NewReader(f).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	profit, _ := strconv.ParseFloat(rows[1][6], 64)
	rate, _ := strconv.ParseFloat(rows[1][7], 64)
	if profit != 85 || math.Abs(rate-8.46) > 1e-9 {
		t.Fatalf("costs lost on export: profit=%v rate=%v", profit, rate)
	}
	b, err := os.ReadFile(filepath.Join("output", "trades", rep.RunID(), "report.json"))
	if err != nil {
		t.Fatal(err)
	}
	var saved Report
	if err := json.Unmarshal(b, &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Config.MACD.SellLookback != 10 || saved.Config.MACD.TrendMinSlope1 != 0.0005 {
		t.Fatal("parameter snapshot lost")
	}
}

func TestMACDDefaultsMatchCurrentCLIComposition(t *testing.T) {
	cfg := defaultMACDConfig()
	want := sb.And{
		sb.A流通市值{Min: 400}, sb.A现价{Max: 120}, sb.A过滤涨停{},
		sb.MACD负数{MinDays: 6}, sb.MACD连涨{MinDays: 2, MaxDays: 2},
		sb.A现价大于N日均线(30),
		sb.And{sb.MAUp{Period: 30, MinSlope: 0.0005}, sb.MAUp{Period: 20, MinSlope: 0.0005}},
	}
	if !reflect.DeepEqual(cfg.MACD.buyer(), want) {
		t.Fatal("default buy strategy differs from cmd/backtest")
	}
	if !reflect.DeepEqual(cfg.seller(), ss.Or{ss.MACD反转{Lookback: 10}}) {
		t.Fatal("default sell strategy differs from cmd/backtest")
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	changed := *cfg.MACD
	changed.MaxPrice = 5
	ks := extend.Klines{&extend.Kline{Kline: &protocol.Kline{Close: protocol.Yuan(10)}}}
	if changed.buyer().(sb.And)[1].Buy("sh600000", ks) {
		t.Fatal("price parameter was ignored")
	}
	if !cfg.MACD.buyer().(sb.And)[1].Buy("sh600000", ks) {
		t.Fatal("default price filter changed")
	}
}

func TestMACDAPIValidatesParamsBeforeStarting(t *testing.T) {
	s := NewServer()
	h := s.Handler()
	var cfg RunConfig
	json.Unmarshal(doReq(t, h, http.MethodGet, "/api/macd/config", nil, http.StatusOK), &cfg)
	cfg.MACD.SellLookback = 0
	doReq(t, h, http.MethodPost, "/api/macd/run", cfg, http.StatusBadRequest)
	cfg = defaultMACDConfig()
	cfg.HoldingDays = 1
	doReq(t, h, http.MethodPost, "/api/macd/run", cfg, http.StatusBadRequest)
	cfg.HoldingDays = 0
	cfg.SampleMode, cfg.SampleCodes = "codes", []string{"../../file"}
	doReq(t, h, http.MethodPost, "/api/macd/run", cfg, http.StatusBadRequest)
	if s.runner.running.Load() {
		t.Fatal("invalid parameters started a task")
	}
}

func TestMACDConditionsUseOnlySelectedRules(t *testing.T) {
	cfg := defaultMACDConfig()
	cfg.MACD.Enabled = &MACDConditions{Price: true}
	cfg.MACD.MaxPrice = 5
	cfg.MACD.NegativeMinDays = 0 // Disabled fields do not constrain validation.
	cfg.MACD.SellLookback = 0
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	want := sb.And{sb.A现价{Max: 5}}
	if !reflect.DeepEqual(cfg.MACD.buyer(), want) {
		t.Fatalf("disabled conditions remain active: %+v", cfg.MACD.buyer())
	}
	ks := extend.Klines{&extend.Kline{Kline: &protocol.Kline{Close: protocol.Yuan(10)}}}
	if cfg.MACD.buyer().Buy("sh600000", ks) {
		t.Fatal("enabled price condition ignored")
	}
	if !reflect.DeepEqual(cfg.seller(), ss.Or{}) {
		t.Fatal("disabled MACD seller remained active")
	}
	cfg.MACD.Enabled.Price = false
	if !cfg.MACD.buyer().Buy("sh600000", ks) {
		t.Fatal("empty selection must allow all samples")
	}
	cfg.MACD.Enabled.Trend2 = true
	if !reflect.DeepEqual(cfg.MACD.buyer(), sb.And{sb.And{sb.MAUp{Period: 20, MinSlope: 0.0005}}}) {
		t.Fatal("trend switches are not independent")
	}
	cfg.MACD.Enabled.SellMACD = true
	if err := cfg.Validate(); err == nil {
		t.Fatal("enabled seller bypassed parameter validation")
	}
}

func TestMACDLegacyRequestsKeepAllConditions(t *testing.T) {
	cfg := defaultMACDConfig()
	wantBuyer, wantSeller := cfg.MACD.buyer(), cfg.seller()
	cfg.MACD.Enabled = nil
	if !reflect.DeepEqual(cfg.MACD.buyer(), wantBuyer) || !reflect.DeepEqual(cfg.seller(), wantSeller) {
		t.Fatal("old request semantics changed")
	}
	var restored MACDParameters
	cfg.MACD.Enabled = &MACDConditions{Rising: true}
	b, err := json.Marshal(cfg.MACD)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &restored); err != nil {
		t.Fatal(err)
	}
	if restored.conditions() != *cfg.MACD.Enabled {
		t.Fatal("selection lost on report roundtrip")
	}
}

func TestMACDYearsRetainOtherYearsWhenOneYearIsMissing(t *testing.T) {
	get := func(code string, start, end time.Time) (extend.Klines, error) {
		if end.Year() == 2024 {
			return nil, fmt.Errorf("missing year")
		}
		ks := extend.Klines{}
		for i := 0; i < 5; i++ {
			date := time.Date(2025, 1, 2+i, 0, 0, 0, 0, time.Local)
			ks = append(ks, &extend.Kline{Unix: date.Unix(), Kline: &protocol.Kline{Time: date, Close: protocol.Yuan(10 + float64(i)), Open: protocol.Yuan(10 + float64(i)), High: protocol.Yuan(11 + float64(i)), Low: protocol.Yuan(9 + float64(i))}})
		}
		return ks, nil
	}
	var progress researchrun.Progress
	cfg := researchrun.Config{Codes: []string{"sh600000"}, Years: []int{2024, 2025}, Variants: []researchrun.Variant{{Name: "test", Buyer: sb.A全部{}}}, DefaultSeller: ss.A持仓N天{Days: 1}, Cost: core.DefaultCost(), Position: core.PositionConfig{MaxPerCode: 1}, Workers: 1, DataMode: researchrun.DailyClose, GetDayKlines: get, OnCodeDone: func(p researchrun.Progress) { progress = p }}
	r, err := runMACDYears(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if r.Coverage.Requested != 2 || r.Coverage.Completed != 1 || r.Coverage.Skipped != 1 || len(r.Results[0].Trades) == 0 || progress.Done != 2 || progress.Total != 2 {
		t.Fatalf("partial year lost: %+v progress=%+v", r, progress)
	}
	legacy := core.Backtest{Buyer: sb.A全部{}, Seller: cfg.DefaultSeller, Cost: cfg.Cost, Position: cfg.Position}
	ks, _ := get("sh600000", time.Time{}, time.Date(2025, 12, 31, 0, 0, 0, 0, time.Local))
	want := legacy.Do("sh600000", nil, ks, nil)
	if !reflect.DeepEqual(r.Results[0].Trades, want) {
		t.Fatal("web trades differ from direct engine")
	}
}
