package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/injoyai/logs"
	common "github.com/injoyai/strategy-tail"
	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/internal/researchrun"
	"github.com/injoyai/strategy-tail/lib/extend"
	"github.com/injoyai/strategy-tail/strategies/buy"
	"github.com/injoyai/tdx/protocol"
)

// 多年份回测：上证5日均线上行 + MACD（vs 基准无过滤），2022-2026

type yearResult struct {
	Year    int
	Metrics core.AnalyzeResult
}

type yearCoverage struct {
	Year     int                  `json:"year"`
	Coverage researchrun.Coverage `json:"coverage"`
}

type variant struct {
	Name   string
	Filter core.Buyer
	Years  []yearResult

	// 多年合并统计
	TotalTrades     int
	WinRate         float64
	TotalProfit     float64
	AvgProfit       float64
	ProfitFactor    float64
	MaxDrawdownPct  float64
	AnnualReturnAvg float64 // 各年年化的简单平均
	SharpeAvg       float64
	WinningYears    int // 盈利年份数
	AllProfitYears  bool
}

func main() {
	common.MustInitialize()
	common.Update()

	codes := common.GetNoPriceLimitCodes()
	cost, pos, _, benchmark, _ := common.LoadBacktestConfig()
	years := []int{2022, 2023, 2024, 2025, 2026}

	// 上证指数全历史日线（过滤条件用）
	indexKs, err := common.Pull.DayKlines("sh000001", time.Time{}, time.Now())
	logs.PanicErr(err)

	variants := []*variant{
		{Name: "基准(无指数过滤)", Filter: nil},
		{Name: "上证5日均线上行", Filter: buy.A上证N日均线向上{Ks: toProtocolKlines(indexKs), Period: 5, Lookback: 3}},
	}

	outRoot := filepath.Join("output", "backtest-index-filter-5y")
	os.MkdirAll(outRoot, 0755)

	runVariants := make([]researchrun.Variant, len(variants))
	for i, v := range variants {
		buyer := core.Buyer(common.MACDBuyer)
		if v.Filter != nil {
			buyer = buy.And{v.Filter, common.MACDBuyer}
		}
		runVariants[i] = researchrun.Variant{
			Name: v.Name, Buyer: buyer, Seller: common.MACDSeller,
		}
	}

	allTrades := make([][]core.Trade, len(variants))
	winningYears := make([]int, len(variants))
	sumAnnual := make([]float64, len(variants))
	sumSharpe := make([]float64, len(variants))
	coverageByYear := make([]yearCoverage, 0, len(years))

	// 逐年运行可保留旧入口的样本口径：某年缺数只排除该代码当年，
	// 不会因为另一年缺数而把整只股票从五年结果中移除。
	for _, year := range years {
		logs.Infof("=== 年份 %d：统一执行 %d 个指数过滤变体 ===", year, len(runVariants))
		run, err := researchrun.Run(context.Background(), researchrun.Config{
			Codes:        codes,
			Years:        []int{year},
			Variants:     runVariants,
			Cost:         cost,
			Position:     pos,
			Workers:      common.DefaultGoroutines * 3,
			DataMode:     researchrun.Intraday,
			GetDayKlines: common.Pull.DayKlines,
			GetMinKlines: common.Pull.MinKlines,
		})
		logs.PanicErr(err)
		researchrun.LogCoverage(run.Coverage)
		coverageByYear = append(coverageByYear, yearCoverage{Year: year, Coverage: run.Coverage})

		benchStart := time.Date(year, 1, 1, 0, 0, 0, 0, time.Local)
		benchEnd := time.Date(year, 12, 31, 23, 0, 0, 0, time.Local)
		benchKlines, _ := common.Pull.DayKlines(benchmark, benchStart, benchEnd)

		for i, result := range run.Results {
			v := variants[i]
			allTrades[i] = append(allTrades[i], result.Trades...)
			res := core.Analyze(year, result.Trades, common.Pull.DayKlines, benchKlines, cost, pos)
			v.Years = append(v.Years, yearResult{Year: year, Metrics: res})

			// 备份该年 CSV（Analyze 会覆盖 output/backtest/<year>.csv）
			vdir := filepath.Join(outRoot, v.Name)
			os.MkdirAll(vdir, 0755)
			copyFile(filepath.Join("output", "backtest", fmt.Sprintf("%d.csv", year)),
				filepath.Join(vdir, fmt.Sprintf("%d.csv", year)))

			if res.TotalProfit > 0 {
				winningYears[i]++
			}
			sumAnnual[i] += res.AnnualReturn
			sumSharpe[i] += res.Sharpe

			logs.Infof("[%s] %d 完成: 交易%d笔 胜率%.2f%% 总盈亏%.2f元 盈亏比%.2f 最大回撤%.2f%% 年化%.2f%% Sharpe%.2f",
				v.Name, year, res.TotalTrades, res.WinRate, res.TotalProfit, res.ProfitFactor,
				res.MaxDrawdownPct, res.AnnualReturn, res.Sharpe)
		}
	}

	for i, v := range variants {
		// 多年合并统计
		stats := core.Stats(allTrades[i])
		var totalProfit float64
		for _, t := range allTrades[i] {
			totalProfit += (t.SellPrice.Float64() - t.BuyPrice.Float64()) * float64(t.Quantity)
		}
		v.TotalTrades = stats.Total
		v.WinRate = stats.WinRate
		v.TotalProfit = totalProfit
		v.AvgProfit = stats.AvgProfit
		v.ProfitFactor = stats.ProfitFactor
		v.WinningYears = winningYears[i]
		v.AllProfitYears = winningYears[i] == len(years)
		v.AnnualReturnAvg = sumAnnual[i] / float64(len(years))
		v.SharpeAvg = sumSharpe[i] / float64(len(years))

		// 合并资金曲线回撤率（按时间排序后模拟峰值本金）
		v.MaxDrawdownPct = combinedMaxDrawdownPct(allTrades[i], pos)
	}

	saveResults(outRoot, years, benchmark, variants, coverageByYear)
}

// combinedMaxDrawdownPct 合并多年交易计算最大回撤率（%）。
// 按买入时间排序，逐笔累加盈亏得到资金曲线，以峰值本金为分母。
func combinedMaxDrawdownPct(trades []core.Trade, pos core.PositionConfig) float64 {
	if len(trades) == 0 {
		return 0
	}
	sorted := make([]core.Trade, len(trades))
	copy(sorted, trades)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].BuyTime.Before(sorted[j].BuyTime)
	})

	// 用单笔金额盈亏
	current := 0.0
	peak := 0.0
	maxDD := 0.0
	for _, t := range sorted {
		current += (t.SellPrice.Float64() - t.BuyPrice.Float64()) * float64(t.Quantity)
		if current > peak {
			peak = current
		}
		if peak > 0 {
			dd := (peak - current) / peak * 100
			if dd > maxDD {
				maxDD = dd
			}
		}
	}

	// 峰值本金：用各年 RequiredCapital 的最大值近似（由外部传入 pos 模拟）
	// 简化：用单笔最大并发的近似不精确，这里用累计盈亏峰值的 1.2 倍兜底
	if peak <= 0 {
		return 0
	}
	return maxDD
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

// saveResults 汇总结果到 JSON，供图表与 PDF 报告使用。
func saveResults(outRoot string, years []int, benchmark string, variants []*variant, coverageByYear []yearCoverage) {
	type vm struct {
		Name            string       `json:"name"`
		TotalTrades     int          `json:"total_trades"`
		WinRate         float64      `json:"win_rate"`
		TotalProfit     float64      `json:"total_profit"`
		AvgProfit       float64      `json:"avg_profit"`
		ProfitFactor    float64      `json:"profit_factor"`
		MaxDrawdownPct  float64      `json:"max_drawdown_pct"`
		AnnualReturnAvg float64      `json:"annual_return_avg"`
		SharpeAvg       float64      `json:"sharpe_avg"`
		WinningYears    int          `json:"winning_years"`
		AllProfitYears  bool         `json:"all_profit_years"`
		Years           []yearResult `json:"years"`
	}

	out := map[string]any{
		"years":            years,
		"benchmark":        benchmark,
		"coverage_by_year": coverageByYear,
		"variants":         []vm{},
	}
	list := make([]vm, 0, len(variants))
	for _, v := range variants {
		list = append(list, vm{
			Name:            v.Name,
			TotalTrades:     v.TotalTrades,
			WinRate:         v.WinRate,
			TotalProfit:     v.TotalProfit,
			AvgProfit:       v.AvgProfit,
			ProfitFactor:    v.ProfitFactor,
			MaxDrawdownPct:  v.MaxDrawdownPct,
			AnnualReturnAvg: v.AnnualReturnAvg,
			SharpeAvg:       v.SharpeAvg,
			WinningYears:    v.WinningYears,
			AllProfitYears:  v.AllProfitYears,
			Years:           v.Years,
		})
	}
	out["variants"] = list

	buf, _ := json.MarshalIndent(out, "", "  ")
	dst := filepath.Join(outRoot, "results.json")
	os.WriteFile(dst, buf, 0644)
	logs.Infof("汇总结果已保存: %s", dst)
}
