package main

import (
	"context"
	"time"

	"github.com/injoyai/logs"
	common "github.com/injoyai/strategy-tail"
	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/internal/researchrun"
	"github.com/injoyai/strategy-tail/lib/extend"
)

// ============================================================================
// 大盘状态分析入口
// ============================================================================
// 跑一次 MACD 策略回测（2022-2026），给每笔交易打上买入日的大盘状态标签，
// 按多维度分组统计胜率/盈亏比/平均收益，出具 HTML 报告。

func main() {
	common.MustInitialize()
	logs.Info("=== 大盘状态分析 ===")

	logs.PanicErr(common.Update())

	// 1. 加载配置
	cost, pos, _, benchmark, _ := common.LoadBacktestConfig()
	years := []int{2022, 2023, 2024, 2025, 2026}
	codes := common.GetNoPriceLimitCodes()

	// 2. 拉取基准指数 K 线（沪深300），覆盖足够历史用于 MA60+波动率计算
	benchStart := time.Date(years[0]-2, 1, 1, 0, 0, 0, 0, time.Local)
	benchEnd := time.Date(years[len(years)-1], 12, 31, 23, 0, 0, 0, time.Local)
	logs.Infof("拉取基准指数 [%s] K线...", benchmark)
	benchKs, err := common.Pull.DayKlines(benchmark, benchStart, benchEnd)
	logs.PanicErr(err)
	logs.Infof("基准 K 线数: %d (%s ~ %s)",
		len(benchKs),
		safeTimeFormat(benchKs, 0),
		safeTimeFormat(benchKs, len(benchKs)-1))

	// 3. 计算每个交易日的大盘状态
	logs.Info("计算大盘状态...")
	regimes := ComputeRegimes(benchKs)
	logs.Infof("大盘状态覆盖交易日数: %d", len(regimes))

	// 4. 跑回测，收集所有交易
	logs.Info("开始回测...")
	trades, coverage, err := runBacktest(context.Background(), common.MACDBuyer, common.MACDSeller, codes, years, cost, pos)
	logs.PanicErr(err)
	logs.Infof("总交易笔数: %d", len(trades))

	// 5. 给交易打标签
	tagged := TagTrades(trades, regimes)

	// 6. 分组分析
	result := Analyze(tagged)
	result.StrategyName = common.MACDBuyer.Name()
	result.Benchmark = benchmark
	result.Coverage = coverage

	// 7. 打印控制台汇总
	PrintSummary(result)

	// 8. 导出 HTML 报告
	ExportHTML(result)

	// 9. 导出 PDF 报告（手机查看专用）
	ExportPDF(result)

	logs.Info("完成！")
}

// YearCoverage 记录单年度研究样本的加载覆盖，供 HTML/PDF 报告披露。
type YearCoverage struct {
	Year     int                  `json:"year"`
	Coverage researchrun.Coverage `json:"coverage"`
}

// runBacktest 按年调用共享研究执行器。逐年运行保留历史样本口径：
// 某年缺数据只排除该代码当年，不影响它在其他年份的交易。
func runBacktest(ctx context.Context, buyer core.Buyer, seller core.Seller, codes []string, years []int, cost core.Cost, pos core.PositionConfig) ([]core.Trade, []YearCoverage, error) {
	all := make([]core.Trade, 0, 10000)
	coverage := make([]YearCoverage, 0, len(years))
	for _, year := range years {
		logs.Infof("回测 %d 年...", year)
		run, err := researchrun.Run(ctx, researchrun.Config{
			Codes:        codes,
			Years:        []int{year},
			Variants:     []researchrun.Variant{{Name: buyer.Name(), Buyer: buyer, Seller: seller}},
			Cost:         cost,
			Position:     pos,
			Workers:      common.DefaultGoroutines * 2,
			DataMode:     researchrun.DailyClose,
			GetDayKlines: common.Pull.DayKlines,
			OnCodeDone: func(progress researchrun.Progress) {
				if progress.Done%500 == 0 || progress.Done == progress.Total {
					logs.Infof("  %d 年数据进度: %d/%d", year, progress.Done, progress.Total)
				}
			},
		})
		if err != nil {
			return nil, coverage, err
		}
		researchrun.LogCoverage(run.Coverage)
		coverage = append(coverage, YearCoverage{Year: year, Coverage: run.Coverage})
		ts := run.Results[0].Trades
		all = append(all, ts...)
		logs.Infof("  %d 年交易笔数: %d", year, len(ts))
	}
	return all, coverage, nil
}

func safeTimeFormat(ks extend.Klines, i int) string {
	if i < 0 || i >= len(ks) || ks[i] == nil {
		return "N/A"
	}
	return ks[i].Time.Format("2006-01-02")
}
