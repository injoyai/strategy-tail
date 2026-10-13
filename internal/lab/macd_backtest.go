package lab

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"time"

	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/internal/researchrun"
	sb "github.com/injoyai/strategy-tail/strategies/buy"
)

// MACDParameters maps cmd/backtest's existing composition to editable values.
// The strategy components, indicator calculations and execution model stay shared.
type MACDParameters struct {
	Enabled         *MACDConditions `json:"enabled,omitempty"`
	MinFloatCap     float64         `json:"minFloatCap"`
	MaxPrice        float64         `json:"maxPrice"`
	NegativeMinDays int             `json:"negativeMinDays"`
	NegativeMaxDays int             `json:"negativeMaxDays"`
	RisingMinDays   int             `json:"risingMinDays"`
	RisingMaxDays   int             `json:"risingMaxDays"`
	PriceMAPeriod   int             `json:"priceMAPeriod"`
	TrendPeriod1    int             `json:"trendPeriod1"`
	TrendMinSlope1  float64         `json:"trendMinSlope1"`
	TrendPeriod2    int             `json:"trendPeriod2"`
	TrendMinSlope2  float64         `json:"trendMinSlope2"`
	SellLookback    int             `json:"sellLookback"`
}

// Missing switches keep old requests and report snapshots equivalent to the CLI.
type MACDConditions struct {
	FloatCap bool `json:"floatCap"`
	Price    bool `json:"price"`
	LimitUp  bool `json:"limitUp"`
	Negative bool `json:"negative"`
	Rising   bool `json:"rising"`
	PriceMA  bool `json:"priceMA"`
	Trend1   bool `json:"trend1"`
	Trend2   bool `json:"trend2"`
	SellMACD bool `json:"sellMACD"`
}

func (p MACDParameters) conditions() MACDConditions {
	if p.Enabled != nil {
		return *p.Enabled
	}
	return MACDConditions{true, true, true, true, true, true, true, true, true}
}

func defaultMACDConfig() RunConfig {
	year := time.Now().Year()
	enabled := (MACDParameters{}).conditions()
	return RunConfig{StartYear: year, EndYear: year, SampleMode: "all", ScriptName: "MACD参数回测", MACD: &MACDParameters{
		Enabled:     &enabled,
		MinFloatCap: 400, MaxPrice: 120, NegativeMinDays: 6,
		RisingMinDays: 2, RisingMaxDays: 2, PriceMAPeriod: 30,
		TrendPeriod1: 30, TrendMinSlope1: 0.0005,
		TrendPeriod2: 20, TrendMinSlope2: 0.0005, SellLookback: 10,
	}}
}

func (p MACDParameters) validate() error {
	e := p.conditions()
	for _, value := range []struct {
		name    string
		value   float64
		enabled bool
	}{
		{"流通市值下限", p.MinFloatCap, e.FloatCap}, {"价格上限", p.MaxPrice, e.Price},
		{"均线一斜率", p.TrendMinSlope1, e.Trend1}, {"均线二斜率", p.TrendMinSlope2, e.Trend2},
	} {
		if value.enabled && (math.IsNaN(value.value) || math.IsInf(value.value, 0) || value.value < 0) {
			return fmt.Errorf("%s须为非负有限数值", value.name)
		}
	}
	for _, value := range []struct {
		name    string
		value   int
		zero    bool
		enabled bool
	}{
		{"负柱天数下限", p.NegativeMinDays, false, e.Negative}, {"负柱天数上限", p.NegativeMaxDays, true, e.Negative},
		{"连涨天数下限", p.RisingMinDays, false, e.Rising}, {"连涨天数上限", p.RisingMaxDays, true, e.Rising},
		{"价格均线周期", p.PriceMAPeriod, false, e.PriceMA}, {"趋势均线一周期", p.TrendPeriod1, false, e.Trend1},
		{"趋势均线二周期", p.TrendPeriod2, false, e.Trend2}, {"卖出回看天数", p.SellLookback, false, e.SellMACD},
	} {
		if !value.enabled {
			continue
		}
		min := 1
		if value.zero {
			min = 0
		}
		if value.value < min || value.value > 1000 {
			return fmt.Errorf("%s须为 %d～1000 的整数", value.name, min)
		}
	}
	return nil
}

func (p MACDParameters) buyer() core.Buyer {
	e := p.conditions()
	buyers := sb.And{}
	for _, condition := range []struct {
		enabled bool
		buyer   core.Buyer
	}{
		{e.FloatCap, sb.A流通市值{Min: p.MinFloatCap}}, {e.Price, sb.A现价{Max: p.MaxPrice}},
		{e.LimitUp, sb.A过滤涨停{}}, {e.Negative, sb.MACD负数{MinDays: p.NegativeMinDays, MaxDays: p.NegativeMaxDays}},
		{e.Rising, sb.MACD连涨{MinDays: p.RisingMinDays, MaxDays: p.RisingMaxDays}}, {e.PriceMA, sb.A现价大于N日均线(p.PriceMAPeriod)},
	} {
		if condition.enabled {
			buyers = append(buyers, condition.buyer)
		}
	}
	trends := sb.And{}
	if e.Trend1 {
		trends = append(trends, sb.MAUp{Period: p.TrendPeriod1, MinSlope: p.TrendMinSlope1})
	}
	if e.Trend2 {
		trends = append(trends, sb.MAUp{Period: p.TrendPeriod2, MinSlope: p.TrendMinSlope2})
	}
	if len(trends) > 0 {
		buyers = append(buyers, trends)
	}
	if len(buyers) == 0 {
		return sb.A全部{}
	}
	return buyers
}

func (s *Server) handleMACDConfig(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, defaultMACDConfig())
}

func validateMACDCodes(codes []string) error {
	if len(codes) > 10000 {
		return fmt.Errorf("指定股票不能超过 10000 只")
	}
	for _, code := range codes {
		if len(code) != 8 || (code[:2] != "sh" && code[:2] != "sz") {
			return fmt.Errorf("股票代码须为 sh 或 sz 前缀加六位数字")
		}
		for _, c := range code[2:] {
			if c < '0' || c > '9' {
				return fmt.Errorf("股票代码须为 sh 或 sz 前缀加六位数字")
			}
		}
	}
	return nil
}

func (s *Server) handleMACDRun(w http.ResponseWriter, r *http.Request) {
	if !requireSameOrigin(w, r) {
		return
	}
	var cfg RunConfig
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384))
	d.DisallowUnknownFields()
	if err := d.Decode(&cfg); err != nil || cfg.MACD == nil {
		writeErr(w, http.StatusBadRequest, "请填写 MACD 回测参数")
		return
	}
	if err := cfg.Validate(); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	cfg.ScriptName = "MACD参数回测"
	variants := []core.Variant{{Name: cfg.ScriptName, Buyer: cfg.MACD.buyer()}}
	if err := s.runner.Start(cfg, variants); err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, map[string]any{"ok": true, "variants": 1})
}

// Keep cmd/backtest's per-year exclusion: a missing year does not discard a
// stock's other years. Coverage/progress count stock × year jobs in this mode.
func runMACDYears(ctx context.Context, cfg researchrun.Config) (researchrun.Report, error) {
	combined := researchrun.Report{Results: make([]researchrun.Result, len(cfg.Variants))}
	for i, variant := range cfg.Variants {
		combined.Results[i].Variant = variant
	}
	for i, year := range cfg.Years {
		one := cfg
		one.Years = []int{year}
		if cfg.OnCodeDone != nil {
			one.OnCodeDone = func(p researchrun.Progress) {
				p.Done += i * len(cfg.Codes)
				p.Total = len(cfg.Codes) * len(cfg.Years)
				cfg.OnCodeDone(p)
			}
		}
		result, err := researchrun.Run(ctx, one)
		if err != nil {
			return combined, err
		}
		for j := range result.Results {
			combined.Results[j].Trades = append(combined.Results[j].Trades, result.Results[j].Trades...)
		}
		combined.Coverage.Requested += result.Coverage.Requested
		combined.Coverage.Completed += result.Coverage.Completed
		combined.Coverage.Skipped += result.Coverage.Skipped
		combined.Coverage.Failures = append(combined.Coverage.Failures, result.Coverage.Failures...)
	}
	return combined, nil
}
