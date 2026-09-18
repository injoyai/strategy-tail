package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/injoyai/logs"
	common "github.com/injoyai/strategy-tail"
	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/internal/researchrun"
	"github.com/injoyai/strategy-tail/lib/extend"
	"github.com/injoyai/strategy-tail/strategies/buy"
	"github.com/injoyai/tdx/protocol"
)

// 变体：在 common.MACDBuyer 基础上叠加不同的上证指数过滤条件
type variant struct {
	Name   string     // 变体名
	Filter core.Buyer // 指数过滤条件（nil=无过滤基准）
	Result core.AnalyzeResult
}

func main() {
	common.MustInitialize()
	common.Update()

	codes := common.GetNoPriceLimitCodes()
	cost, pos, _, benchmark, _ := common.LoadBacktestConfig()
	year := 2026

	// 上证指数全历史日线（过滤条件用）
	indexKs, err := common.Pull.DayKlines("sh000001", time.Time{}, time.Now())
	logs.PanicErr(err)

	variants := []variant{
		{Name: "基准(无指数过滤)", Filter: nil},
		{Name: "指数站上MA20", Filter: buy.A指数站上MA{Ks: indexKs, Period: 20}},
		{Name: "指数站上MA60", Filter: buy.A指数站上MA{Ks: indexKs, Period: 60}},
		{Name: "指数多头排列[5,20,60]", Filter: buy.A指数多头排列{Ks: indexKs, Periods: []int{5, 20, 60}}},
		{Name: "指数多头排列[20,60,120]", Filter: buy.A指数多头排列{Ks: indexKs, Periods: []int{20, 60, 120}}},
		{Name: "上证5日均线上行", Filter: buy.A上证N日均线向上{Ks: toProtocolKlines(indexKs), Period: 5, Lookback: 3}},
	}

	outRoot := filepath.Join("output", "backtest-index-filter")
	os.MkdirAll(outRoot, 0755)

	runVariants := make([]researchrun.Variant, len(variants))
	for i := range variants {
		buyer := core.Buyer(common.MACDBuyer)
		if variants[i].Filter != nil {
			buyer = buy.And{variants[i].Filter, common.MACDBuyer}
		}
		runVariants[i] = researchrun.Variant{
			Name: variants[i].Name, Buyer: buyer, Seller: common.MACDSeller,
		}
	}

	logs.Infof("=== 统一执行 %d 个指数过滤变体 ===", len(runVariants))
	run, err := researchrun.Run(context.Background(), researchrun.Config{
		Codes:        codes,
		Years:        []int{year},
		Variants:     runVariants,
		Cost:         cost,
		Position:     pos,
		Workers:      common.DefaultGoroutines * 2,
		DataMode:     researchrun.Intraday,
		GetDayKlines: common.Pull.DayKlines,
		GetMinKlines: common.Pull.MinKlines,
	})
	logs.PanicErr(err)
	researchrun.LogCoverage(run.Coverage)

	benchStart := time.Date(year, 1, 1, 0, 0, 0, 0, time.Local)
	benchEnd := time.Date(year, 12, 31, 23, 0, 0, 0, time.Local)
	benchKlines, _ := common.Pull.DayKlines(benchmark, benchStart, benchEnd)

	for i, result := range run.Results {
		v := &variants[i]
		res := core.Analyze(year, result.Trades, common.Pull.DayKlines, benchKlines, cost, pos)
		v.Result = res

		// 保存该组交易明细副本（Analyze 会覆盖 output/backtest/2026.csv，需立即备份）
		vdir := filepath.Join(outRoot, fmt.Sprintf("%02d_%s", i, v.Name))
		os.MkdirAll(vdir, 0755)
		copyFile(filepath.Join("output", "backtest", fmt.Sprintf("%d.csv", year)), filepath.Join(vdir, fmt.Sprintf("%d.csv", year)))

		logs.Infof("变体[%s]: 交易%d笔 胜率%.2f%% 总盈亏%.2f元 盈亏比%.2f 最大回撤%.2f%% 年化%.2f%% Sharpe%.2f",
			v.Name, res.TotalTrades, res.WinRate, res.TotalProfit, res.ProfitFactor, res.MaxDrawdownPct, res.AnnualReturn, res.Sharpe)
	}

	saveResults(outRoot, year, benchmark, variants, run.Coverage)
}

// toProtocolKlines 将 extend.Klines 转为 protocol.Klines（A上证N日均线向上 需要）。
func toProtocolKlines(ks extend.Klines) protocol.Klines {
	pk := make(protocol.Klines, 0, len(ks))
	for _, k := range ks {
		if k == nil {
			continue
		}
		pk = append(pk, k.Kline)
	}
	return pk
}

func copyFile(src, dst string) {
	data, err := os.ReadFile(src)
	if err != nil {
		logs.Warnf("复制失败 %s -> %s: %v", src, dst, err)
		return
	}
	if err := os.WriteFile(dst, data, 0644); err != nil {
		logs.Warnf("写入失败 %s: %v", dst, err)
	}
}

// saveResults 汇总所有变体的指标到 JSON，供图表与 PDF 报告使用。
func saveResults(outRoot string, year int, benchmark string, variants []variant, coverage researchrun.Coverage) {
	type metrics struct {
		TotalTrades      int
		WinRate          float64
		TotalProfit      float64
		AvgProfit        float64
		MaxProfit        float64
		MaxLoss          float64
		ProfitFactor     float64
		MaxDrawdown      float64
		MaxDrawdownPct   float64
		DrawdownDuration int
		UnderwaterMax    float64
		RequiredCapital  float64
		AnnualReturn     float64
		Sharpe           float64
		Sortino          float64
		Calmar           float64
		BenchReturn      float64
		Alpha            float64
		Beta             float64
		WinStreakMax     int
		LossStreakMax    int
		AvgHoldingDays   float64
		VaR95            float64
		CVaR95           float64
	}

	type vm struct {
		Name    string
		Metrics metrics
	}

	out := map[string]any{
		"year":      year,
		"benchmark": benchmark,
		"coverage":  coverage,
		"variants":  []vm{},
	}
	list := make([]vm, 0, len(variants))
	for _, v := range variants {
		r := v.Result
		list = append(list, vm{
			Name: v.Name,
			Metrics: metrics{
				TotalTrades:      r.TotalTrades,
				WinRate:          r.WinRate,
				TotalProfit:      r.TotalProfit,
				AvgProfit:        r.AvgProfit,
				MaxProfit:        r.MaxProfit,
				MaxLoss:          r.MaxLoss,
				ProfitFactor:     r.ProfitFactor,
				MaxDrawdown:      r.MaxDrawdown,
				MaxDrawdownPct:   r.MaxDrawdownPct,
				DrawdownDuration: r.DrawdownDuration,
				UnderwaterMax:    r.UnderwaterMax,
				RequiredCapital:  r.RequiredCapital,
				AnnualReturn:     r.AnnualReturn,
				Sharpe:           r.Sharpe,
				Sortino:          r.Sortino,
				Calmar:           r.Calmar,
				BenchReturn:      r.BenchReturn,
				Alpha:            r.Alpha,
				Beta:             r.Beta,
				WinStreakMax:     r.WinStreakMax,
				LossStreakMax:    r.LossStreakMax,
				AvgHoldingDays:   r.AvgHoldingDays,
				VaR95:            r.VaR95,
				CVaR95:           r.CVaR95,
			},
		})
	}
	out["variants"] = list

	buf, _ := json.MarshalIndent(out, "", "  ")
	dst := filepath.Join(outRoot, "results.json")
	os.WriteFile(dst, buf, 0644)
	logs.Infof("汇总结果已保存: %s", dst)
}
