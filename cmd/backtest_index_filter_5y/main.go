package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/injoyai/logs"
	common "github.com/injoyai/strategy-tail"
	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/lib/extend"
	"github.com/injoyai/strategy-tail/strategies/buy"
	"github.com/injoyai/tdx/protocol"
)

// 多年份回测：上证5日均线上行 + MACD（vs 基准无过滤），2022-2026

type yearResult struct {
	Year    int
	Metrics core.AnalyzeResult
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

	for _, v := range variants {
		allTrades := []core.Trade(nil)
		winYears := 0
		sumAnnual, sumSharpe := 0.0, 0.0

		for _, year := range years {
			logs.Infof("=== 变体[%s] 年份 %d 开始 ===", v.Name, year)

			buyer := core.Buyer(common.MACDBuyer)
			if v.Filter != nil {
				buyer = buy.And{v.Filter, common.MACDBuyer}
			}

			bt := core.Backtest{
				Buyer:        buyer,
				Seller:       common.MACDSeller,
				Goroutines:   common.DefaultGoroutines * 3,
				Codes:        codes,
				Years:        []int{year},
				GetDayKlines: common.Pull.DayKlines,
				GetMinKlines: common.Pull.MinKlines,
				Benchmark:    benchmark,
				Cost:         cost,
				Position:     pos,
			}

			trades := runYear(&bt, codes, year)
			allTrades = append(allTrades, trades...)

			benchStart := time.Date(year, 1, 1, 0, 0, 0, 0, time.Local)
			benchEnd := time.Date(year, 12, 31, 23, 0, 0, 0, time.Local)
			benchKlines, _ := bt.GetDayKlines(benchmark, benchStart, benchEnd)

			res := core.Analyze(year, trades, bt.GetDayKlines, benchKlines, cost, pos)
			v.Years = append(v.Years, yearResult{Year: year, Metrics: res})

			// 备份该年 CSV（Analyze 会覆盖 output/backtest/<year>.csv）
			vdir := filepath.Join(outRoot, v.Name)
			os.MkdirAll(vdir, 0755)
			copyFile(filepath.Join("output", "backtest", fmt.Sprintf("%d.csv", year)),
				filepath.Join(vdir, fmt.Sprintf("%d.csv", year)))

			if res.TotalProfit > 0 {
				winYears++
			}
			sumAnnual += res.AnnualReturn
			sumSharpe += res.Sharpe

			logs.Infof("[%s] %d 完成: 交易%d笔 胜率%.2f%% 总盈亏%.2f元 盈亏比%.2f 最大回撤%.2f%% 年化%.2f%% Sharpe%.2f",
				v.Name, year, res.TotalTrades, res.WinRate, res.TotalProfit, res.ProfitFactor,
				res.MaxDrawdownPct, res.AnnualReturn, res.Sharpe)
		}

		// 多年合并统计
		stats := core.Stats(allTrades)
		var totalProfit float64
		for _, t := range allTrades {
			totalProfit += (t.SellPrice.Float64() - t.BuyPrice.Float64()) * float64(t.Quantity)
		}
		v.TotalTrades = stats.Total
		v.WinRate = stats.WinRate
		v.TotalProfit = totalProfit
		v.AvgProfit = stats.AvgProfit
		v.ProfitFactor = stats.ProfitFactor
		v.WinningYears = winYears
		v.AllProfitYears = winYears == len(years)
		v.AnnualReturnAvg = sumAnnual / float64(len(years))
		v.SharpeAvg = sumSharpe / float64(len(years))

		// 合并资金曲线回撤率（按时间排序后模拟峰值本金）
		v.MaxDrawdownPct = combinedMaxDrawdownPct(allTrades, pos)
	}

	saveResults(outRoot, years, benchmark, variants)
}

// runYear 对单年度所有股票并行回测，逻辑与 core.Backtest._backtest 一致。
func runYear(bt *core.Backtest, codes []string, year int) []core.Trade {
	hisStart := time.Date(year-2, 6, 1, 0, 0, 0, 0, time.Local)
	start := time.Date(year, 1, 1, 0, 0, 0, 0, time.Local)
	end := time.Date(year, 12, 31, 23, 0, 0, 0, time.Local)

	result := []core.Trade(nil)
	var mu sync.Mutex
	var wg sync.WaitGroup
	ch := make(chan string)

	workers := bt.Goroutines
	if workers <= 0 {
		workers = 10
	}
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for code := range ch {
				dks, err := bt.GetDayKlines(code, hisStart, end)
				if err != nil || len(dks) == 0 {
					continue
				}
				his := []*extend.Kline(nil)
				for i, v := range dks {
					if v.Time.Before(start) {
						his = append(his, v)
					} else {
						dks = dks[i:]
						break
					}
				}
				var mks protocol.Klines
				if bt.GetMinKlines != nil {
					mks, err = bt.GetMinKlines(code, start, end)
					if err != nil {
						continue
					}
				}
				ts := bt.Do(code, his, dks, mks)
				if len(ts) > 0 {
					mu.Lock()
					result = append(result, ts...)
					mu.Unlock()
				}
			}
		}()
	}
	for _, code := range codes {
		ch <- code
	}
	close(ch)
	wg.Wait()
	return result
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
func saveResults(outRoot string, years []int, benchmark string, variants []*variant) {
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
		"years":     years,
		"benchmark": benchmark,
		"variants":  []vm{},
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
