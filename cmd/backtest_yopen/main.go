package main

// 收盘大于昨开 · 附加条件对比回测（纯日线快速版）
//
// 目的：在当前最优组合「连涨2天·实体≥70%」基础上，附加买入条件
// 「当日收盘价 > 昨日开盘价」（buy.A收盘大于昨开），对比新旧条件效果。
//
// 快速口径（收盘买、收盘卖）：
//   - 买入 = 当日收盘价成交（引擎 Do() 固有行为）；
//   - 卖出 = sell.A持仓N天{Days:1}，即买入次日收盘价成交（T+1 收盘卖）；
//   - 买卖均只依赖日线，因此不拉分钟线（mks 传 nil），大幅提速；
//     引擎在无分钟数据时自动退化为按日线收盘成交（core/backtest.go）。
//
// 说明：
//   - 本入口不调用 common.Update()：本地数据已覆盖 2022-01-01 ~ 2026-09-04，
//     跳过全市场校验（与既有回测口径一致）；
//   - 基准组合与加条件组合共用同一份日线缓存、同一卖出规则，对比口径干净。

import (
	"context"
	"fmt"
	"math"
	"sort"

	"github.com/injoyai/logs"
	common "github.com/injoyai/strategy-tail"
	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/internal/researchrun"
	"github.com/injoyai/strategy-tail/strategies/buy"
	"github.com/injoyai/strategy-tail/strategies/sell"
)

func main() {
	common.MustInitialize()

	codes := common.GetNoPriceLimitCodes()
	cost, pos, _, _, mcIterations := common.LoadBacktestConfig()

	years := []int{2022, 2023, 2024, 2025, 2026}

	// ---- 2 组对比：基准 vs 附加"收盘>昨开" ----
	type combo struct {
		name  string
		buyer core.Buyer
	}
	combos := []combo{
		{name: "基准:连涨2天·实体≥70%", buyer: buy.And{
			buy.A流通市值{Min: 20},
			buy.A价格{Min: 2, Max: 120},
			buy.A过滤涨停{},
			buy.MACD连涨{MinDays: 2},
			buy.A实体阴线{MinBodyRatio: 0.7},
		}},
		{name: "+收盘>昨开", buyer: buy.And{
			buy.A流通市值{Min: 20},
			buy.A价格{Min: 2, Max: 120},
			buy.A过滤涨停{},
			buy.MACD连涨{MinDays: 2},
			buy.A实体阴线{MinBodyRatio: 0.7},
			buy.A收盘大于昨开{},
		}},
	}

	// 卖出：买入次日收盘强制平仓（T+1 收盘卖，纯日线，不加止盈止损）
	seller := sell.Or{sell.A持仓N天{Days: 1}}

	// ---- 通过公共研究执行层逐股跑全部组合 ----
	logs.Infof("开始回测 %d 只股票 × %d 组参数 × %d 年（纯日线）", len(codes), len(combos), len(years))
	variants := make([]researchrun.Variant, len(combos))
	for i, combo := range combos {
		variants[i] = researchrun.Variant{Name: combo.name, Buyer: buy.Strategy(combo.name, combo.buyer)}
	}
	run, err := researchrun.Run(context.Background(), researchrun.Config{
		Codes:         codes,
		Years:         years,
		Variants:      variants,
		DefaultSeller: seller,
		Cost:          cost,
		Position:      pos,
		Workers:       common.DefaultGoroutines * 2,
		DataMode:      researchrun.DailyClose,
		GetDayKlines:  common.Pull.DayKlines,
	})
	logs.PanicErr(err)
	researchrun.LogCoverage(run.Coverage)

	// ---- 汇总对比表（按平均收益降序，无交易置底）----
	type rowResult struct {
		name   string
		stats  core.TradeStats
		profit float64 // 累计盈亏金额（元）
		trades []core.Trade
	}
	rows := make([]rowResult, 0, len(combos))
	for ci, c := range combos {
		trades := run.Results[ci].Trades
		stats := core.Stats(trades)
		var profit float64
		for _, t := range trades {
			profit += t.ProfitAmount()
		}
		rows = append(rows, rowResult{name: c.name, stats: stats, profit: profit, trades: trades})
	}

	sort.Slice(rows, func(i, j int) bool {
		if rows[i].stats.Total == 0 || rows[j].stats.Total == 0 {
			return rows[i].stats.Total > rows[j].stats.Total
		}
		return rows[i].stats.AvgProfit > rows[j].stats.AvgProfit
	})

	median := func(trades []core.Trade) float64 {
		if len(trades) == 0 {
			return 0
		}
		rates := make([]float64, 0, len(trades))
		for _, t := range trades {
			buyPrice := t.BuyPrice.Float64()
			if buyPrice <= 0 {
				continue
			}
			rates = append(rates, (t.SellPrice.Float64()-buyPrice)/buyPrice*100)
		}
		sort.Float64s(rates)
		n := len(rates)
		if n == 0 {
			return 0
		}
		if n%2 == 1 {
			return rates[n/2]
		}
		return (rates[n/2-1] + rates[n/2]) / 2
	}

	fmt.Println()
	fmt.Println("====================================================================================")
	fmt.Println("收盘>昨开 附加条件对比（2022-2026，收盘买/T+1收盘卖，纯日线，按平均收益降序）")
	fmt.Println("====================================================================================")
	fmt.Printf("%-24s %8s %8s %10s %10s %10s %10s %12s\n",
		"参数组合", "笔数", "胜率%", "平均%", "中位%", "盈亏比", "均持天数", "收益额(万)")
	for _, r := range rows {
		if r.stats.Total == 0 {
			fmt.Printf("%-24s %8d %8s %10s %10s %10s %10s %12s\n",
				r.name, 0, "-", "-", "-", "-", "-", "-")
			continue
		}
		holding := 0.0
		for _, t := range r.trades {
			holding += float64(t.HoldingDays())
		}
		holding /= float64(len(r.trades))
		pf := fmt.Sprintf("%.2f", r.stats.ProfitFactor)
		if math.IsInf(r.stats.ProfitFactor, 1) {
			pf = "+Inf"
		}
		fmt.Printf("%-24s %8d %8.1f %10.2f %10.2f %10s %10.1f %12.1f\n",
			r.name, r.stats.Total, r.stats.WinRate, r.stats.AvgProfit,
			median(r.trades), pf, holding, r.profit/1e4)
	}
	fmt.Println("------------------------------------------------------------------------------------")

	// ---- 分年度收益（两个组合并列对比，按累计盈亏金额万元）----
	fmt.Println()
	fmt.Println("分年度收益对比（收益额万元）")
	fmt.Println("------------------------------------------------------------------")
	fmt.Printf("%6s", "年份")
	for _, r := range rows {
		fmt.Printf("  %16s", r.name)
	}
	fmt.Println()
	byYear := map[int]map[int][]core.Trade{} // year -> comboIdx -> trades
	var yearList []int
	for ci, r := range rows {
		for _, t := range r.trades {
			y := t.BuyTime.Year()
			if _, exists := byYear[y]; !exists {
				byYear[y] = map[int][]core.Trade{}
				yearList = append(yearList, y)
			}
			byYear[y][ci] = append(byYear[y][ci], t)
		}
	}
	sort.Ints(yearList)
	for _, y := range yearList {
		fmt.Printf("%6d", y)
		for ci := range rows {
			ts := byYear[y][ci]
			if len(ts) == 0 {
				fmt.Printf("  %16s", "-")
				continue
			}
			var profit float64
			for _, t := range ts {
				profit += t.ProfitAmount()
			}
			fmt.Printf("  %16.1f", profit/1e4)
		}
		fmt.Println()
	}
	fmt.Println("------------------------------------------------------------------")

	// ---- 交易记录落盘（AGENTS.md 6.1 硬性要求）：每组合一个 CSV ----
	strategyName := "收盘大于昨开日线对比"
	for _, r := range rows {
		if path := core.ExportTradesCSV(strategyName, r.name, r.trades); path != "" {
			logs.Infof("交易记录已落盘: %s", path)
		}
	}

	// 最优组合的蒙特卡洛
	if len(rows) > 0 {
		if best := rows[0]; best.stats.Total > 10 {
			mc := core.MonteCarlo(best.trades, mcIterations, 100000)
			logs.Infof("最优组合[%s]蒙特卡洛(%d次): 中位收益%.1f%% | 95%%区间[%.1f%%, %.1f%%] | 盈利概率%.0f%%",
				best.name, mcIterations, mc.ReturnP50, mc.ReturnP5, mc.ReturnP95, mc.ProbProfit*100)
		}
	}
}
