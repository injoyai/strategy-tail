package main

// 胜率提升假设验证 · 多方向组合矩阵（纯日线快速版）
//
// 在基准「MACD连涨2天 + 实体阴线≥70%」（收盘买 / T+1收盘卖）上，一次验证 4 个想象方向：
//
//	方向A 缩量阴线：阴线当天量能萎缩 = 洗盘而非出货（量价经典逻辑，原策略未用量能）
//	  组1：阴线量 < 昨日量（A缩量{Days:1, Ratio:1.0}）
//	  组2：阴线量 < 5日均量80%（A缩量{Days:5, Ratio:0.8}）
//	方向B 回调不破位：强势整理
//	  组3：阴线收盘仍在 MA5 上方（A收盘高于均线{5}）
//	方向C 阴线不吞昨阳：收盘仍高于昨日收盘（A收盘大于昨收，跳空高开后的浅回调）
//	  组4：+ A收盘大于昨收
//	方向D 延长持有：T+1 胜率受单日随机性压制，给反弹时间发酵
//	  组5：基准 × T+2；组6：基准 × T+3
//
// 口径：买入 = 当日收盘价成交；mks 传 nil（引擎无分钟数据时买卖均退化日线收盘成交）。
// 本入口不调用 common.Update()：本地数据覆盖 2022-01-01 ~ 2026-09-04（与既有回测一致）。

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

	codes := common.GetNoPriceLimitCodes()
	cost, pos, _, _, mcIterations := common.LoadBacktestConfig()

	years := []int{2022, 2023, 2024, 2025, 2026}

	// ---- 7 组合矩阵：4 个方向 × 对照基准 ----
	type combo struct {
		name   string
		buyer  core.Buyer
		seller core.Seller
	}

	baseInner := buy.And{
		buy.A流通市值{Min: 20},
		buy.A价格{Min: 2, Max: 120},
		buy.A过滤涨停{},
		buy.MACD连涨{MinDays: 2},
		buy.A实体阴线{MinBodyRatio: 0.7},
	}
	withExtra := func(extra ...core.Buyer) buy.And {
		dst := append(buy.And{}, baseInner...)
		return append(dst, extra...)
	}
	t1 := sell.Or{sell.A持仓N天{Days: 1}}
	t2 := sell.Or{sell.A持仓N天{Days: 2}}
	t3 := sell.Or{sell.A持仓N天{Days: 3}}

	combos := []combo{
		{name: "基准", buyer: baseInner, seller: t1},
		{name: "A1+阴线量<昨量", buyer: withExtra(buy.A缩量{Days: 1, Ratio: 1.0}), seller: t1},
		{name: "A2+阴线量<5日均80%", buyer: withExtra(buy.A缩量{Days: 5, Ratio: 0.8}), seller: t1},
		{name: "B+收盘在MA5上", buyer: withExtra(buy.A收盘高于均线{Period: 5}), seller: t1},
		{name: "C+收盘>昨收", buyer: withExtra(buy.A收盘大于昨收{}), seller: t1},
		{name: "D2基准持有2天", buyer: baseInner, seller: t2},
		{name: "D3基准持有3天", buyer: baseInner, seller: t3},
	}

	// ---- 通过公共研究执行层逐股跑全部组合 ----
	logs.Infof("开始回测 %d 只股票 × %d 组假设 × %d 年（纯日线）", len(codes), len(combos), len(years))
	variants := make([]researchrun.Variant, len(combos))
	for i, combo := range combos {
		variants[i] = researchrun.Variant{
			Name: combo.name, Buyer: buy.Strategy(combo.name, combo.buyer), Seller: combo.seller,
		}
	}
	run, err := researchrun.Run(context.Background(), researchrun.Config{
		Codes:        codes,
		Years:        years,
		Variants:     variants,
		Cost:         cost,
		Position:     pos,
		Workers:      common.DefaultGoroutines * 2,
		DataMode:     researchrun.DailyClose,
		GetDayKlines: common.Pull.DayKlines,
	})
	logs.PanicErr(err)
	researchrun.LogCoverage(run.Coverage)

	// ---- 汇总对比表（按胜率降序，无交易置底；关注点即胜率）----
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

	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].stats.Total == 0 || rows[j].stats.Total == 0 {
			return rows[i].stats.Total > rows[j].stats.Total
		}
		return rows[i].stats.WinRate > rows[j].stats.WinRate
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
	fmt.Println("==========================================================================================")
	fmt.Println("胜率提升假设验证（2022-2026，收盘买/收盘卖，纯日线，按胜率降序）")
	fmt.Println("==========================================================================================")
	fmt.Printf("%-24s %8s %8s %10s %10s %10s %10s %12s\n",
		"组合", "笔数", "胜率%", "平均%", "中位%", "盈亏比", "均持天数", "收益额(万)")
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
	fmt.Println("------------------------------------------------------------------------------------------")

	// ---- 最优（胜率最高且有交易）组合分年度表现 ----
	for _, r := range rows {
		if r.stats.Total > 0 {
			fmt.Println()
			fmt.Println("胜率最高组合分年度表现（" + r.name + "）")
			fmt.Println("------------------------------------------------------------------")
			fmt.Printf("%6s %10s %10s %12s\n", "年份", "笔数", "胜率%", "收益额(万)")
			byYear := map[int][]core.Trade{}
			var yearList []int
			for _, t := range r.trades {
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
			break
		}
	}

	// ---- 交易记录落盘（AGENTS.md 6.1 硬性要求）：每组合一个 CSV ----
	strategyName := "胜率提升假设验证"
	for _, r := range rows {
		if path := core.ExportTradesCSV(strategyName, r.name, r.trades); path != "" {
			logs.Infof("交易记录已落盘: %s", path)
		}
	}

	// 胜率最高组合的蒙特卡洛
	for _, r := range rows {
		if r.stats.Total > 10 {
			mc := core.MonteCarlo(r.trades, mcIterations, 100000)
			logs.Infof("胜率最高组合[%s]蒙特卡洛(%d次): 中位收益%.1f%% | 95%%区间[%.1f%%, %.1f%%] | 盈利概率%.0f%%",
				r.name, mcIterations, mc.ReturnP50, mc.ReturnP5, mc.ReturnP95, mc.ProbProfit*100)
			break
		}
	}
}
