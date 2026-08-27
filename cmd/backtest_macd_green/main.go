package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/injoyai/logs"
	common "github.com/injoyai/strategy-tail"
	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/lib/extend"
	"github.com/injoyai/strategy-tail/strategies/buy"
	"github.com/injoyai/strategy-tail/strategies/sell"
	"github.com/injoyai/tdx/protocol"
)

// ReportData 汇总回测结果，供 PDF 报告渲染。
type ReportData struct {
	StrategyName string
	BuyerName    string
	SellerName   string
	Benchmark    string
	Years        []int
	Results      []core.AnalyzeResult
	AllTrades    []core.Trade
	MC           core.MonteCarloResult
	Audit        core.AuditResult
	Cost         core.Cost
	Position     core.PositionConfig
	GeneratedAt  string
}

func main() {
	// 默认跳过数据更新：全量拉取分钟线（全 A 股）耗时数小时。
	// 日线数据已就绪，本策略为日线级；分钟线为空时引擎自动退化为日线级卖出。
	// 如需强制更新数据，运行时设置环境变量 MACD_GREEN_UPDATE=1。
	if os.Getenv("MACD_GREEN_UPDATE") == "1" {
		if err := common.Update(); err != nil {
			logs.Warnf("数据更新失败: %v", err)
		}
	}

	codes := common.GetNoPriceLimitCodes()
	cost, pos, _, benchmark, mcIterations := common.LoadBacktestConfig()
	years := []int{2025, 2026}

	buyer := buildBuyer()
	seller := buildSeller()

	logs.Infof("买入策略: %s", buyer.Name())
	logs.Infof("卖出策略: %s", seller.Name())

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
			filepath.Join("output", "backtest-macd-green", fmt.Sprintf("%d.csv", year)))
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
		StrategyName: "下跌企稳 · MACD绿柱缩短转红",
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

	if err := ExportPDF(data); err != nil {
		logs.Errorf("PDF 生成失败: %v", err)
		os.Exit(1)
	}
	logs.Info("回测与报告生成完成")
}

// buildBuyer 构造买入条件。
// 用户需求 → 实现原语映射：
//   - 下跌趋势:      buy.BuyCloseBelowMA{Period:60}（收盘价低于 60 日均线，中期空头）
//   - 大量负数 MACD 柱: MACD负柱缩短{Lookback:15}（近 15 根柱子内曾出现连续负柱段，空头充分释放）
//   - 负柱慢慢减少:  MACD负柱缩短{MinDays:3}（连续 >=3 根负柱逐日收窄，空头动能衰竭）
//   - 股价企稳:      buy.A企稳信号{}（阳线 + 收涨 + 不创新低）
//   - 负柱转正买入:  buy.MACD转红{}（今日量柱 > 0 且昨日 <= 0）
//   - 放量确认:      buy.A成交量放大{}（转红当天量 > 近5日均量1.5倍，资金进场）
func buildBuyer() core.Buyer {
	return buy.And{
		// 常规过滤：流通市值、价格、涨停
		buy.A流通市值{Min: 400},
		buy.A现价{Max: 120},
		buy.A过滤涨停{},

		// 下跌趋势：收盘价低于 60 日均线（中期空头）
		buy.BuyCloseBelowMA{Period: 60},

		// 大量负柱 + 负柱慢慢减少（绿柱缩短）
		buy.MACD负柱缩短{MinDays: 3, Lookback: 15},

		// 股价企稳（右侧确认）
		buy.A企稳信号{},

		// 量柱由负转正，当日买入
		buy.MACD转红{},

		// 放量确认：资金真实进场
		buy.A成交量放大{Period: 5, Ratio: 1.5},
	}
}

// buildSeller 构造卖出条件（风控优先，社区抄底策略"放宽止损、让利润奔跑"）。
func buildSeller() core.Seller {
	return sell.Or{
		// 止盈 15% / 止损 10%：抄底买在下跌趋势中，止损需容纳日线级波动，
		// 10% 止损 + 15% 止盈，目标盈亏比 1.5:1（社区经典截断亏损/让利润奔跑）
		sell.A止盈止损{TakeProfit: 0.15, StopLoss: 0.10},
		// 盈利后峰值回撤 8% 离场，锁定利润
		sell.A追踪止损{Drawdown: 0.08},
		// MACD 红柱拐头（近 10 日高点后回落）卖出
		sell.MACD反转{Lookback: 10},
	}
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