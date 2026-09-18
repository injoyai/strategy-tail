package main

// 尾盘阴线收回策略 · 多参数矩阵回测
//
// 策略逻辑：股票处于上升趋势，上涨过程中出现有实体的阴线（盘中跌破趋势支撑均线），
// 但尾盘又站回均线（如 MA5/MA10），则收盘买入，博取次日反弹。
// 卖出：次日强制平仓（A持仓N天{Days:1}，T+1 买入次日首个卖出判定触发），
// 加止盈止损风控兜底。
//
// 参数矩阵：
//   - 趋势定义：MA5向上 / MA10向上 / MA20向上 / 均线多头排列(5,10,20) / 无趋势过滤
//   - 支撑均线：MA5 / MA10（尾盘收回的均线）
//
// 性能：每只股票的日线/分钟线只从 SQLite 读取一次并缓存，
// 10 个参数组合在内存中共享同一份数据（每组合使用独立克隆，避免引擎分钟级覆写互相污染）。

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

	common.Update()

	codes := common.GetNoPriceLimitCodes()
	cost, pos, _, _, mcIterations := common.LoadBacktestConfig()

	years := []int{2022, 2023, 2024, 2025, 2026}

	// 趋势过滤定义
	trends := []struct {
		name  string
		trend core.Buyer
	}{
		{"MA5向上", buy.MAUp{Period: 5}},
		{"MA10向上", buy.MAUp{Period: 10}},
		{"MA20向上", buy.MAUp{Period: 20}},
		{"多头排列5/10/20", buy.A均线多头排列{5, 10, 20}},
		{"无趋势过滤", nil},
	}

	// 支撑均线（尾盘收回的均线）
	supportPeriods := []int{5, 10}

	// ---- 构造全部参数组合 ----
	type combo struct {
		name          string
		buyerName     string
		supportPeriod int
		buyer         core.Buyer
	}
	combos := make([]combo, 0, len(trends)*len(supportPeriods))
	for _, trend := range trends {
		for _, support := range supportPeriods {
			inner := buy.And{
				buy.A流通市值{Min: 20},
				buy.A价格{Min: 2, Max: 120},
				buy.A过滤涨停{},
				buy.A阴线收回{
					SupportPeriod: support,
					MinBodyRatio:  0.3,
					MaxRise:       1.0,
					Trend:         trend.trend,
				},
			}
			name := fmt.Sprintf("%s·收回MA%d", trend.name, support)
			combos = append(combos, combo{
				name:          name,
				buyerName:     name,
				supportPeriod: support,
				buyer:         inner,
			})
		}
	}

	// 卖出：次日强制平仓（博一日反弹）+ 止盈止损风控兜底（全部组合共用）
	seller := sell.Or{
		sell.A持仓N天{Days: 1},
		sell.A止盈止损{TakeProfit: 0.10, StopLoss: 0.08},
	}

	// ---- 通过公共研究执行层逐股跑全部组合 ----
	logs.Infof("开始回测 %d 只股票 × %d 组参数 × %d 年", len(codes), len(combos), len(years))
	variants := make([]researchrun.Variant, len(combos))
	for i, combo := range combos {
		variants[i] = researchrun.Variant{Name: combo.name, Buyer: buy.Strategy(combo.buyerName, combo.buyer)}
	}
	run, err := researchrun.Run(context.Background(), researchrun.Config{
		Codes:         codes,
		Years:         years,
		Variants:      variants,
		DefaultSeller: seller,
		Cost:          cost,
		Position:      pos,
		Workers:       common.DefaultGoroutines * 2,
		DataMode:      researchrun.Intraday,
		GetDayKlines:  common.Pull.DayKlines,
		GetMinKlines:  common.Pull.MinKlines,
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
	fmt.Println("尾盘阴线收回策略 · 参数矩阵汇总（2022-2026，次日卖出，按平均收益降序）")
	fmt.Println("====================================================================================")
	fmt.Printf("%-24s %6s %8s %10s %10s %10s %10s %12s\n",
		"趋势/支撑", "笔数", "胜率%", "平均%", "中位%", "盈亏比", "均持天数", "收益额(万)")
	for _, r := range rows {
		if r.stats.Total == 0 {
			fmt.Printf("%-24s %6d %8s %10s %10s %10s %10s %12s\n",
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
		fmt.Printf("%-24s %6d %8.1f %10.2f %10.2f %10s %10.1f %12.1f\n",
			r.name, r.stats.Total, r.stats.WinRate, r.stats.AvgProfit,
			median(r.trades), pf, holding, r.profit/1e4)
	}
	fmt.Println("------------------------------------------------------------------------------------")

	// ---- 交易记录落盘（AGENTS.md 6.1 硬性要求）：每组合一个 CSV + 全部组合一个可视化 HTML ----
	strategyName := "尾盘阴线收回"
	var allTrades []core.Trade
	for _, r := range rows {
		if path := core.ExportTradesCSV(strategyName, r.name, r.trades); path != "" {
			logs.Infof("交易记录已落盘: %s", path)
		}
		allTrades = append(allTrades, r.trades...)
	}
	if path := core.ExportTradesHTML(strategyName, "matrix_2022_2026", allTrades, common.Pull.DayKlines); path != "" {
		logs.Infof("可视化报告已生成: %s", path)
	}

	// 最优参数的蒙特卡洛
	if len(rows) > 0 {
		if best := rows[0]; best.stats.Total > 10 {
			mc := core.MonteCarlo(best.trades, mcIterations, 100000)
			logs.Infof("最优组合[%s]蒙特卡洛(%d次): 中位收益%.1f%% | 95%%区间[%.1f%%, %.1f%%] | 盈利概率%.0f%%",
				best.name, mcIterations, mc.ReturnP50, mc.ReturnP5, mc.ReturnP95, mc.ProbProfit*100)
		}
	}
}
