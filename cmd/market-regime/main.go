package main

import (
	"fmt"
	"sync"
	"time"

	"github.com/injoyai/bar"
	"github.com/injoyai/logs"
	common "github.com/injoyai/strategy-tail"
	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/lib/extend"
	"github.com/injoyai/tdx/protocol"
)

// ============================================================================
// 大盘状态分析入口
// ============================================================================
// 跑一次 MACD 策略回测（2022-2026），给每笔交易打上买入日的大盘状态标签，
// 按多维度分组统计胜率/盈亏比/平均收益，出具 HTML 报告。

func main() {
	logs.Info("=== 大盘状态分析 ===")

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
	trades := runBacktest(common.MACDBuyer, common.MACDSeller, codes, years, cost, pos)
	logs.Infof("总交易笔数: %d", len(trades))

	// 5. 给交易打标签
	tagged := TagTrades(trades, regimes)

	// 6. 分组分析
	result := Analyze(tagged)
	result.StrategyName = common.MACDBuyer.Name()
	result.Benchmark = benchmark

	// 7. 打印控制台汇总
	PrintSummary(result)

	// 8. 导出 HTML 报告
	ExportHTML(result)

	// 9. 导出 PDF 报告（手机查看专用）
	ExportPDF(result)

	logs.Info("完成！")
}

// runBacktest 遍历 codes×years，调用 Backtest.Do 收集所有交易。
// 不调用 Backtest.Run() 以避免其打印和导出副作用。
func runBacktest(buyer core.Buyer, seller core.Seller, codes []string, years []int, cost core.Cost, pos core.PositionConfig) []core.Trade {
	bt := core.Backtest{
		Buyer:        buyer,
		Seller:       seller,
		Goroutines:   common.DefaultGoroutines * 2,
		GetDayKlines: common.Pull.DayKlines,
		GetMinKlines: nil, // 大盘状态分析不需要分钟线精度，跳过以加速
		Cost:         cost,
		Position:     pos,
	}

	all := make([]core.Trade, 0, 10000)
	for _, year := range years {
		logs.Infof("回测 %d 年...", year)
		ts := backtestYear(bt, codes, year)
		all = append(all, ts...)
		logs.Infof("  %d 年交易笔数: %d", year, len(ts))
	}
	return all
}

// backtestYear 单年回测，复刻 core.Backtest._backtest 的核心逻辑
// 但只调用导出的 Do 方法，不依赖未导出的 _backtest
func backtestYear(bt core.Backtest, codes []string, year int) []core.Trade {
	hisStart := time.Date(year-2, 6, 1, 0, 0, 0, 0, time.Local)
	start := time.Date(year, 1, 1, 0, 0, 0, 0, time.Local)
	end := time.Date(year, 12, 31, 23, 0, 0, 0, time.Local)

	var mu sync.Mutex
	result := make([]core.Trade, 0, 2000)

	b := bar.NewCoroutine(len(codes), bt.Goroutines, bar.WithPrefix(fmt.Sprintf("[%d]", year)))
	defer b.Close()

	for _, code := range codes {
		code := code
		b.Go(func() {
			b.SetPrefix(fmt.Sprintf("[%d][%s]", year, code))

			dks, err := bt.GetDayKlines(code, hisStart, end)
			if err != nil {
				b.Logf("[错误] %s %s", code, err)
				b.Flush()
				return
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
					b.Logf("[错误] %s %s", code, err)
					b.Flush()
					return
				}
			}

			ts := bt.Do(code, his, dks, mks)
			if len(ts) > 0 {
				mu.Lock()
				result = append(result, ts...)
				mu.Unlock()
			}
		})
	}
	b.Wait()
	return result
}

func safeTimeFormat(ks extend.Klines, i int) string {
	if i < 0 || i >= len(ks) || ks[i] == nil {
		return "N/A"
	}
	return ks[i].Time.Format("2006-01-02")
}
