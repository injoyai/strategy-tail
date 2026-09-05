package main

import (
	"encoding/csv"
	"flag"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/injoyai/logs"
	common "github.com/injoyai/strategy-tail"
	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/lib/extend"
	"github.com/injoyai/strategy-tail/lib/report"
	"github.com/injoyai/strategy-tail/strategies/buy"
	"github.com/injoyai/tdx/protocol"
)

// ReportData 汇总回测结果，供 PDF 报告渲染。
type ReportData = report.ReportData

// 参数化配置：用于对比不同 MACD顺滑 参数组合
var (
	flagSmooth = flag.Int("smooth", 5, "MACD顺滑 EMA 平滑周期")
	flagDays   = flag.Int("days", 10, "MACD顺滑回看天数")
	flagRev    = flag.Int("rev", 1, "MACD顺滑同号段最大拐头次数")
)

func main() {
	// 日线数据已就绪，本策略为日线级；分钟线为空时引擎自动退化为日线级卖出。
	// 如需强制更新数据，运行时设置环境变量 MACD_SMOOTH_UPDATE=1。
	if os.Getenv("MACD_SMOOTH_UPDATE") == "1" {
		if err := common.Update(); err != nil {
			logs.Warnf("数据更新失败: %v", err)
		}
	}

	// -merge: 合并 2022-2026 五个年份 CSV 做全周期分析（不做回测重跑）
	mergeMode := flag.Bool("merge", false, "合并 2022-2026 已有 CSV 做全周期分析")
	flag.Parse()
	if *mergeMode {
		runMergeAnalysis()
		return
	}

	codes := common.GetNoPriceLimitCodes()
	cost, pos, _, benchmark, mcIterations := common.LoadBacktestConfig()
	years := []int{2026}

	buyer := buildBuyer()
	seller := buildSeller()

	logs.Infof("买入策略: %s\n", buyer.Name())
	logs.Infof("卖出策略: %s\n", seller.Name())

	bt := &core.Backtest{
		Buyer:        buyer,
		Seller:       seller,
		Goroutines:   common.DefaultGoroutines * 2,
		Codes:        codes,
		Years:        years,
		GetDayKlines: common.Pull.DayKlines,
		GetMinKlines: common.Pull.MinKlines,
		Benchmark:    benchmark,
		Cost:         cost,
		Position:     pos,
	}

	// core.Backtest.Run() 不返回交易明细，这里复现 _backtest 循环收集 []Trade 供 PDF 报告。
	allTrades := []core.Trade(nil)
	results := make([]core.AnalyzeResult, 0, len(years))
	for _, year := range years {
		ls := runYear(bt, codes, year)
		logs.Infof("[%d] 成交 %d 笔", year, len(ls))
		allTrades = append(allTrades, ls...)

		benchStart := time.Date(year, 1, 1, 0, 0, 0, 0, time.Local)
		benchEnd := time.Date(year, 12, 31, 23, 0, 0, 0, time.Local)
		benchKlines, _ := bt.GetDayKlines(benchmark, benchStart, benchEnd)

		res := core.Analyze(year, ls, bt.GetDayKlines, benchKlines, cost, pos)
		results = append(results, res)

		// Analyze 内部会写 output/backtest/{year}.csv，备份到本模块目录
		backupCSV(filepath.Join("output", "backtest", fmt.Sprintf("%d.csv", year)),
			filepath.Join("output", "backtest-macd-smooth", fmt.Sprintf("%d.csv", year)))
	}

	core.PrintAnalyzeResults(results)

	// ---- 蒙特卡洛模拟（跨年度全部交易）----
	var mc core.MonteCarloResult
	if len(allTrades) > 10 {
		mc = core.MonteCarlo(allTrades, mcIterations, 100000)
		logs.Infof("蒙特卡洛(%d次): 中位收益%.1f%% | 95%%区间[%.1f%%, %.1f%%] | 盈利概率%.0f%% | 破产概率%.0f%%",
			mcIterations, mc.ReturnP50, mc.ReturnP5, mc.ReturnP95, mc.ProbProfit*100, mc.ProbRuin*100)
	}

	// ---- 前视偏差审计 ----
	audit := core.AuditLookAhead(allTrades, cost, func(code string) (extend.Klines, error) {
		return bt.GetDayKlines(code, time.Time{}, time.Now())
	})
	if audit.Passed {
		logs.Info("前视偏差审计: 通过 ✓")
	} else {
		logs.Warnf("前视偏差审计: 发现 %d 个问题", len(audit.Issues))
		for _, issue := range audit.Issues {
			logs.Warn("  - " + issue)
		}
	}

	// ---- 汇总数据 ----
	data := &ReportData{
		StrategyName: "MACD量柱流畅 · 反转 · 30日均线上行",
		BuyerName:    buyer.Name(),
		SellerName:   seller.Name(),
		Benchmark:    benchmark,
		Years:        years,
		Results:      results,
		AllTrades:    allTrades,
		MC:           mc,
		Audit:        audit,
		Cost:         cost,
		Position:     pos,
		GeneratedAt:  time.Now().Format("2006-01-02"),
	}

	if err := exportReport(data); err != nil {
		logs.Errorf("PDF 生成失败: %v", err)
		os.Exit(1)
	}
	logs.Info("回测与报告生成完成")
}

// buildBuyer 构造买入条件。
// 用户需求 → 实现原语映射：
//   - MACD 量柱流畅:  buy.MACD顺滑{Smooth:flagSmooth, Days:flagDays, MaxReversals:flagRev}
//     对原始 MACD 量柱做 EMA(Smooth) 平滑，最近 Days 天内每个同号段
//     （连续正数段/连续负数段）的方向反转次数 <= MaxReversals，
//     零轴穿越开启新段且不计入拐头。
//   - MACD 反转:      buy.MACD反转{MinLookback:4}
//     今天量柱比昨天变大，且昨天是近 4~20 天窗口内的最低值（低位拐头）。
//   - 30 日均线向上:  buy.MAUp{Period:30, MinSlope:0.0005}
//     MA30 方向向上且每步涨速 >= 0.05%，中期趋势确认。
func buildBuyer() core.Buyer {
	return buy.And{
		// 常规过滤：流通市值、价格、涨停
		buy.A流通市值{Min: 100},
		buy.A现价{Max: 120},
		buy.A过滤涨停{},

		// MACD 量柱流畅（EMA 平滑后每个同号段反转 <= MaxReversals）
		buy.MACD顺滑{Smooth: *flagSmooth, Days: *flagDays, MaxReversals: *flagRev},

		// MACD 低位反转（今天量柱变大 + 昨天为近 4 日最低点）
		buy.MACD反转{MinLookback: 4},

		// 30 日均线向上（趋势方向确认）
		buy.MAUp{Period: 20, MinSlope: 0.0005},
		buy.MAUp{Period: 30, MinSlope: 0.0005},
	}
}

// buildSeller 构造卖出条件（使用默认卖出策略）。
// 默认卖出 = common.MACDSeller：
//   - 无盈利等第二次上升浪（sell.MACD反转{Lookback:10}）
//   - 有盈利则在反转的时候卖出（sell.And{sell.A盈利(0.005), sell.MACD反转{Lookback:2}}）
func buildSeller() core.Seller {
	return common.MACDSeller
}

// runYear 对单年度所有股票并行回测，逻辑与 core.Backtest._backtest 一致
// （core.Backtest.Run 不返回交易明细，这里用导出的 GetDayKlines / GetMinKlines / Do 收集）。
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

// backupCSV 将 Analyze 生成的 CSV 备份到本模块输出目录。
func backupCSV(src, dst string) {
	data, err := os.ReadFile(src)
	if err != nil {
		logs.Warnf("备份失败 %s -> %s: %v", src, dst, err)
		return
	}
	os.MkdirAll(filepath.Dir(dst), 0755)
	if err := os.WriteFile(dst, data, 0644); err != nil {
		logs.Warnf("写入失败 %s: %v", dst, err)
	}
}

// ============================================================================
// 合并分析模式（-merge）
// 读取 2022-2026 五个年份的 CSV 交易明细，恢复为 core.Trade，
// 做年度 + 五年全周期聚合分析，输出合并版 PDF 报告。
// ============================================================================

// runMergeAnalysis 合并分析入口。
func runMergeAnalysis() {
	years := []int{2022, 2023, 2024, 2025, 2026}
	cost, pos, _, benchmark, mcIterations := common.LoadBacktestConfig()

	// 读取各年份 CSV，恢复交易
	yearly := make(map[int][]core.Trade)
	var allTrades []core.Trade
	for _, year := range years {
		ts, err := loadTradesFromCSV(year)
		if err != nil {
			logs.Fatalf("读取 %d 年交易失败: %v", year, err)
		}
		yearly[year] = ts
		allTrades = append(allTrades, ts...)
		logs.Infof("[%d] 读取 %d 笔", year, len(ts))
	}
	logs.Infof("五年合计 %d 笔", len(allTrades))

	buyer := buildBuyer()
	seller := buildSeller()

	// 基准日线（2022-01-01 ~ 2026-12-31）
	benchStart := time.Date(2022, 1, 1, 0, 0, 0, 0, time.Local)
	benchEnd := time.Date(2026, 12, 31, 23, 0, 0, 0, time.Local)
	benchKlines, err := common.Pull.DayKlines(benchmark, benchStart, benchEnd)
	if err != nil {
		logs.Warnf("基准数据获取失败: %v", err)
	}

	// 年度分析（复用 Analyze 生成年度指标）
	results := make([]core.AnalyzeResult, 0, len(years)+1)
	for _, year := range years {
		bStart := time.Date(year, 1, 1, 0, 0, 0, 0, time.Local)
		bEnd := time.Date(year, 12, 31, 23, 0, 0, 0, time.Local)
		ybk, _ := common.Pull.DayKlines(benchmark, bStart, bEnd)
		res := core.Analyze(year, yearly[year], common.Pull.DayKlines, ybk, cost, pos)
		results = append(results, res)
	}

	// 全周期聚合分析：单独计算，避免 Analyze 的 HTML 可视化副作用过重
	total := analyzeMerged(allTrades, benchKlines, cost, pos)

	core.PrintAnalyzeResults(results)
	printMergedResult(total)

	// 蒙特卡洛（五年全部交易）
	var mc core.MonteCarloResult
	if len(allTrades) > 10 {
		mc = core.MonteCarlo(allTrades, mcIterations, 100000)
		logs.Infof("蒙特卡洛(%d次): 中位收益%.1f%% | 95%%区间[%.1f%%, %.1f%%] | 盈利概率%.0f%% | 破产概率%.0f%%",
			mcIterations, mc.ReturnP50, mc.ReturnP5, mc.ReturnP95, mc.ProbProfit*100, mc.ProbRuin*100)
	}

	// 前视偏差审计
	audit := core.AuditLookAhead(allTrades, cost, func(code string) (extend.Klines, error) {
		return common.Pull.DayKlines(code, time.Time{}, time.Now())
	})
	if audit.Passed {
		logs.Info("前视偏差审计: 通过 ✓")
	} else {
		logs.Warnf("前视偏差审计: 发现 %d 个问题", len(audit.Issues))
		for _, issue := range audit.Issues {
			logs.Warn("  - " + issue)
		}
	}

	data := &ReportData{
		StrategyName: "MACD量柱流畅 · 反转 · 30日均线上行",
		BuyerName:    buyer.Name(),
		SellerName:   seller.Name(),
		Benchmark:    benchmark,
		Years:        years,
		Results:      append(results, total),
		AllTrades:    allTrades,
		MC:           mc,
		Audit:        audit,
		Cost:         cost,
		Position:     pos,
		GeneratedAt:  time.Now().Format("2006-01-02"),
	}

	if err := exportReport(data); err != nil {
		logs.Errorf("PDF 生成失败: %v", err)
		os.Exit(1)
	}
	logs.Info("合并分析完成")
}

// loadTradesFromCSV 从 output/backtest-macd-smooth/{year}.csv 恢复交易记录。
// CSV 列: 代码,买入时间,买入价格,卖出时间,卖出价格,盈亏,收益率,持有天数
// 注意: CSV 不保存 Quantity/BuyCost/SellIncome，恢复时按一手(100股)估算成本，
// 与核心口径一致（Analyze 内部同样兼容 BuyCost<=0 时按 BuyPrice*SharesPerLot 估算）。
func loadTradesFromCSV(year int) ([]core.Trade, error) {
	path := filepath.Join("output", "backtest-macd-smooth", fmt.Sprintf("%d.csv", year))
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	r := csv.NewReader(f)
	rows, err := r.ReadAll()
	if err != nil {
		return nil, err
	}
	if len(rows) < 2 {
		return nil, nil
	}

	trades := make([]core.Trade, 0, len(rows)-1)
	for _, row := range rows[1:] {
		if len(row) < 8 {
			continue
		}
		buyTime, err1 := time.ParseInLocation(time.DateTime, row[1], time.Local)
		sellTime, err2 := time.ParseInLocation(time.DateTime, row[3], time.Local)
		buyPrice, err3 := strconv.ParseFloat(row[2], 64)
		sellPrice, err4 := strconv.ParseFloat(row[4], 64)
		if err1 != nil || err2 != nil || err3 != nil || err4 != nil {
			continue
		}
		// 配置中手续费为 0，CSV 中买入/卖出价格即含滑点的实际成交价
		// 同步填充 ExecPrice 供前瞻偏差审计使用，避免零值误报
		// Quantity 填充一手(100股)，使年度 Analyze 的盈亏统计口径与核心一致
		trades = append(trades, core.Trade{
			Code:          row[0],
			BuyTime:       buyTime,
			SellTime:      sellTime,
			BuyPrice:      protocol.Yuan(buyPrice),
			SellPrice:     protocol.Yuan(sellPrice),
			BuyExecPrice:  protocol.Yuan(buyPrice),
			SellExecPrice: protocol.Yuan(sellPrice),
			Quantity:      core.SharesPerLot,
		})
	}
	return trades, nil
}

// analyzeMerged 对合并后的全周期交易做聚合分析。
// 指标口径与 core.Analyze 保持一致（每手模型、收益率百分比）。
func analyzeMerged(allTrades []core.Trade, benchKlines extend.Klines, cost core.Cost, pos core.PositionConfig) core.AnalyzeResult {
	// 按买入时间排序
	sort.Slice(allTrades, func(i, j int) bool {
		return allTrades[i].BuyTime.Before(allTrades[j].BuyTime)
	})

	stats := core.Stats(allTrades)
	sharesPerLot := pos.SharesPerLot
	if sharesPerLot <= 0 {
		sharesPerLot = core.SharesPerLot
	}

	// 资金曲线（按实际成本口径，同 Analyze）
	var equityCurve []float64
	currentEquity := 0.0
	equityCurve = append(equityCurve, currentEquity)
	totalProfit := 0.0
	for _, t := range allTrades {
		profit := (t.SellPrice.Float64() - t.BuyPrice.Float64()) * float64(sharesPerLot)
		totalProfit += profit
		currentEquity += profit
		equityCurve = append(equityCurve, currentEquity)
	}

	// 最大回撤
	var maxDrawdown float64
	for i := range equityCurve {
		for j := i; j < len(equityCurve); j++ {
			if equityCurve[i]-equityCurve[j] > maxDrawdown {
				maxDrawdown = equityCurve[i] - equityCurve[j]
			}
		}
	}

	// 最低本金（峰值并发占用，同 calculateRequiredCapital）
	requiredCapital := calcRequiredCapital(allTrades, pos)

	annualReturn := 0.0
	maxDrawdownPct := 0.0
	if requiredCapital > 0 {
		annualReturn = totalProfit / requiredCapital * 100
		maxDrawdownPct = maxDrawdown * 100 / requiredCapital
	}

	// 风险调整指标
	tradeReturns := make([]float64, 0, len(allTrades))
	for _, t := range allTrades {
		r := (t.SellPrice.Float64() - t.BuyPrice.Float64()) / t.BuyPrice.Float64()
		tradeReturns = append(tradeReturns, r)
	}
	sharpe := core.SharpeRatio(tradeReturns, len(tradeReturns))
	sortino := core.SortinoRatio(tradeReturns, len(tradeReturns))
	calmar := core.CalmarRatio(annualReturn/100, maxDrawdownPct)

	// 基准收益
	benchReturn := core.BenchmarkReturn(benchKlines) * 100

	// 连胜连亏、持仓天数、偏度峰度、VaR
	winStreak, lossStreak := calcStreaks(allTrades)
	avgHoldingDays := calcAvgHoldingDays(allTrades)
	skew, kurt := calcSkewKurt(tradeReturns)
	var95, cvar95 := calcVaR(tradeReturns, 0.05)

	return core.AnalyzeResult{
		Year:             0, // 0 表示全周期聚合
		TotalTrades:      len(allTrades),
		WinRate:          stats.WinRate,
		TotalProfit:      totalProfit,
		AvgProfit:        stats.AvgProfit,
		MaxProfit:        stats.MaxProfit,
		MaxLoss:          stats.MaxLoss,
		ProfitFactor:     stats.ProfitFactor,
		MaxDrawdown:      maxDrawdown,
		RequiredCapital:  requiredCapital,
		AnnualReturn:     annualReturn,
		Sharpe:           sharpe,
		Sortino:          sortino,
		Calmar:           calmar,
		BenchReturn:      benchReturn,
		Alpha:            0,
		Beta:             0,
		MaxDrawdownPct:   maxDrawdownPct,
		DrawdownDuration: 0,
		UnderwaterMax:    0,
		WinStreakMax:     winStreak,
		LossStreakMax:    lossStreak,
		AvgHoldingDays:   avgHoldingDays,
		ProfitSkewness:   skew,
		ProfitKurtosis:   kurt,
		VaR95:            var95 * 100,
		CVaR95:           cvar95 * 100,
	}
}

// calcRequiredCapital 计算峰值并发占用本金（同 core.calculateRequiredCapital）。
func calcRequiredCapital(allTrades []core.Trade, pos core.PositionConfig) float64 {
	if len(allTrades) == 0 {
		return 0
	}
	sharesPerLot := pos.SharesPerLot
	if sharesPerLot <= 0 {
		sharesPerLot = core.SharesPerLot
	}
	dailyExposure := make(map[string]float64)
	for _, t := range allTrades {
		buyCost := t.BuyPrice.Float64() * float64(sharesPerLot)
		cur := t.BuyTime
		end := t.SellTime
		for !cur.After(end) {
			key := cur.Format(time.DateOnly)
			dailyExposure[key] += buyCost
			cur = cur.AddDate(0, 0, 1)
		}
	}
	maxExposure := 0.0
	for _, v := range dailyExposure {
		if v > maxExposure {
			maxExposure = v
		}
	}
	return maxExposure
}

// calcStreaks 计算最大连胜/连亏（同 core.calculateStreaks）。
func calcStreaks(trades []core.Trade) (maxWin, maxLoss int) {
	curWin, curLoss := 0, 0
	for _, t := range trades {
		r := (t.SellPrice.Float64() - t.BuyPrice.Float64()) / t.BuyPrice.Float64()
		if r > 0 {
			curWin++
			curLoss = 0
			if curWin > maxWin {
				maxWin = curWin
			}
		} else if r < 0 {
			curLoss++
			curWin = 0
			if curLoss > maxLoss {
				maxLoss = curLoss
			}
		} else {
			curWin, curLoss = 0, 0
		}
	}
	return
}

// calcAvgHoldingDays 平均持仓天数（自然日近似，同 HoldingDays）。
func calcAvgHoldingDays(trades []core.Trade) float64 {
	if len(trades) == 0 {
		return 0
	}
	var total float64
	for _, t := range trades {
		total += t.SellTime.Sub(t.BuyTime).Hours() / 24
	}
	return total / float64(len(trades))
}

// calcSkewKurt 偏度与峰度。
func calcSkewKurt(returns []float64) (skew, kurt float64) {
	n := len(returns)
	if n < 3 {
		return 0, 0
	}
	mean := 0.0
	for _, r := range returns {
		mean += r
	}
	mean /= float64(n)
	m2, m3, m4 := 0.0, 0.0, 0.0
	for _, r := range returns {
		d := r - mean
		m2 += d * d
		m3 += d * d * d
		m4 += d * d * d * d
	}
	m2 /= float64(n)
	m3 /= float64(n)
	m4 /= float64(n)
	if m2 <= 0 {
		return 0, 0
	}
	std := math.Sqrt(m2)
	if std == 0 {
		return 0, 0
	}
	skew = m3 / (std * std * std)
	kurt = m4/(m2*m2) - 3
	return
}

// calcVaR 历史模拟法 VaR / CVaR。
func calcVaR(returns []float64, level float64) (var95, cvar95 float64) {
	n := len(returns)
	if n == 0 {
		return 0, 0
	}
	sorted := make([]float64, n)
	copy(sorted, returns)
	sort.Float64s(sorted)
	idx := int(float64(n) * level)
	if idx >= n {
		idx = n - 1
	}
	var95 = sorted[idx]
	sum := 0.0
	cnt := 0
	for _, r := range sorted[:idx+1] {
		sum += r
		cnt++
	}
	if cnt > 0 {
		cvar95 = sum / float64(cnt)
	}
	return
}

// printMergedResult 打印全周期聚合结果。
func printMergedResult(r core.AnalyzeResult) {
	fmt.Printf("\n五年合并结果 (2022-2026):\n")
	fmt.Printf("  交易: %d  胜率: %.2f%%  总盈亏: %.0f  平均收益: %.2f%%\n",
		r.TotalTrades, r.WinRate, r.TotalProfit, r.AvgProfit)
	fmt.Printf("  盈亏比: %.2f  最大回撤: %.0f (%.2f%%)  最低本金: %.0f\n",
		r.ProfitFactor, r.MaxDrawdown, r.MaxDrawdownPct, r.RequiredCapital)
	fmt.Printf("  年化: %.2f%%  Sharpe: %.2f  Sortino: %.2f  Calmar: %.2f\n",
		r.AnnualReturn, r.Sharpe, r.Sortino, r.Calmar)
	fmt.Printf("  基准收益: %.2f%%  最大连胜: %d  最大连亏: %d  平均持仓: %.1f天\n",
		r.BenchReturn, r.WinStreakMax, r.LossStreakMax, r.AvgHoldingDays)
	fmt.Printf("  VaR95: %.2f%%  CVaR95: %.2f%%\n", r.VaR95, r.CVaR95)
}
