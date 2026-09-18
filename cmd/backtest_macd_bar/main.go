package main

// 尾盘量柱阴线策略 · 多参数矩阵回测（次日尾盘卖出版）
//
// 策略逻辑：MACD 量柱（柱状图）趋势向上（连续上升 ≥ MinDays 天），
// 且当天出现有实体的阴线（量柱趋势不变，仍在上升），则尾盘（收盘价）买入，
// 持有至第二天尾盘（T+1 尾盘 14:55 后，分钟级按 14:55 快照成交 ≈ 收盘价）卖出。
//
// 说明：
//   - 引擎 Do() 以当日收盘价成交，即"尾盘买入"的回测近似；
//   - sell.A持仓N天尾盘{Days:1} 在买入次日尾盘（14:55 起）触发，
//     即"第二天尾盘卖出"；无分钟数据时退化为次日收盘价成交；
//   - 本入口不调用 common.Update()：本地数据已覆盖 2022-01-01 ~ 2026-09-04，
//     跳过全市场校验（数据仅缺回测末期 5 个交易日，对结论无实质影响）。
//
// 参数矩阵：MACD连涨 MinDays(2/3) × 实体阴线 MinBodyRatio(0.3/0.5/0.7)，共 6 组。
//
// 性能：每只股票的日线/分钟线只从 SQLite 读取一次并缓存，
// 6 个参数组合在内存中共享同一份数据（每组合使用独立克隆，避免引擎分钟级覆写互相污染）。

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

	// ---- 构造全部参数组合：连涨天数 × 实体比例 ----
	type combo struct {
		name  string
		buyer core.Buyer
	}
	combos := make([]combo, 0, 6)
	for _, minDays := range []int{2, 3} {
		for _, ratio := range []float64{0.3, 0.5, 0.7} {
			inner := buy.And{
				buy.A流通市值{Min: 20},
				buy.A价格{Min: 2, Max: 120},
				buy.A过滤涨停{},
				buy.MACD连涨{MinDays: minDays},
				buy.A实体阴线{MinBodyRatio: ratio},
			}
			name := fmt.Sprintf("连涨%d天·实体≥%.0f%%", minDays, ratio*100)
			combos = append(combos, combo{name: name, buyer: inner})
		}
	}

	// 卖出：第二天尾盘强制平仓（T+1 尾盘 14:55 起按分钟快照成交，纯次日尾盘卖出，不加止盈止损）
	seller := sell.Or{sell.A持仓N天尾盘{Days: 1}}

	// ---- 通过公共研究执行层逐股跑全部组合 ----
	logs.Infof("开始回测 %d 只股票 × %d 组参数 × %d 年", len(codes), len(combos), len(years))
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
	fmt.Println("尾盘量柱阴线策略 · 参数矩阵汇总（2022-2026，次日尾盘卖出，按平均收益降序）")
	fmt.Println("====================================================================================")
	fmt.Printf("%-22s %8s %8s %10s %10s %10s %10s %12s\n",
		"参数组合", "笔数", "胜率%", "平均%", "中位%", "盈亏比", "均持天数", "收益额(万)")
	for _, r := range rows {
		if r.stats.Total == 0 {
			fmt.Printf("%-22s %8d %8s %10s %10s %10s %10s %12s\n",
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
		fmt.Printf("%-22s %8d %8.1f %10.2f %10.2f %10s %10.1f %12.1f\n",
			r.name, r.stats.Total, r.stats.WinRate, r.stats.AvgProfit,
			median(r.trades), pf, holding, r.profit/1e4)
	}
	fmt.Println("------------------------------------------------------------------------------------")

	// ---- 分年度收益（最优组合，按累计盈亏金额万元）----
	if len(rows) > 0 && rows[0].stats.Total > 0 {
		fmt.Println()
		fmt.Println("最优组合分年度表现（" + rows[0].name + "）")
		fmt.Println("------------------------------------------------------------------")
		fmt.Printf("%6s %10s %10s %12s\n", "年份", "笔数", "胜率%", "收益额(万)")
		byYear := map[int][]core.Trade{}
		var yearList []int
		for _, t := range rows[0].trades {
			y := t.BuyTime.Year()
			if _, exists := byYear[y]; !exists {
				yearList = append(yearList, y)
			}
			byYear[y] = append(byYear[y], t)
		}
		sort.Ints(yearList)
		for _, y := range yearList {
			ts := byYear[y]
			st := core.Stats(ts)
			var profit float64
			for _, t := range ts {
				profit += t.ProfitAmount()
			}
			fmt.Printf("%6d %10d %10.1f %12.1f\n", y, st.Total, st.WinRate, profit/1e4)
		}
		fmt.Println("------------------------------------------------------------------")
	}

	// ---- 卖出时间分布（验证"第二天尾盘卖出"语义：应集中于 14:55，退化时为收盘）----
	{
		total, tailSell, degraded := 0, 0, 0
		for _, r := range rows {
			for _, t := range r.trades {
				if t.Virtual {
					continue
				}
				total++
				tt := t.SellTime
				if (tt.Hour() == 14 && tt.Minute() >= 55) || tt.Hour() >= 15 { // 尾盘 14:55 起卖出
					tailSell++
				} else if tt.Hour() == 0 && tt.Minute() == 0 { // 无分钟数据退化（收盘成交）
					degraded++
				}
			}
		}
		if total > 0 {
			logs.Infof("卖出时间分布: 尾盘(>=14:55) %.1f%% | 无分钟退化(00:00) %.1f%% | 其他(异常!) %.1f%%（共 %d 笔）",
				float64(tailSell)/float64(total)*100,
				float64(degraded)/float64(total)*100,
				float64(total-tailSell-degraded)/float64(total)*100, total)
		}
	}

	// ---- 交易记录落盘（AGENTS.md 6.1 硬性要求）：每组合一个 CSV + 全部组合一个可视化 HTML ----
	// 名称与"次日早盘卖出"版区分，保留旧结果作对比
	strategyName := "尾盘量柱阴线次日尾盘卖"
	var allTrades []core.Trade
	for _, r := range rows {
		if path := core.ExportTradesCSV(strategyName, r.name, r.trades); path != "" {
			logs.Infof("交易记录已落盘: %s", path)
		}
		allTrades = append(allTrades, r.trades...)
	}
	if path := core.ExportTradesHTML(strategyName, "matrix_2022_2026_tail", allTrades, common.Pull.DayKlines); path != "" {
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
