package main

import (
	"flag"
	"fmt"

	common "github.com/injoyai/strategy-tail"
	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/strategies/buy"
	"github.com/injoyai/strategy-tail/strategies/sell"
)

func main() {
	mcMin := flag.Float64("mcMin", 0, "流通市值下限(亿)")
	mcMax := flag.Float64("mcMax", 0, "流通市值上限(亿), 0=无上限")
	flag.Parse()

	codes := common.GetAllCodes()

	cost, pos, _, benchmark, _ := common.LoadBacktestConfig()
	_ = cost
	_ = pos
	_ = benchmark

	years := []int{2022, 2023, 2024, 2025, 2026}

	mcFilter := buy.A流通市值{}
	if *mcMin > 0 {
		mcFilter.Min = *mcMin
	}
	if *mcMax > 0 {
		mcFilter.Max = *mcMax
	}

	// 基础买入过滤
	baseBuyer := buy.And{
		buy.A价格{Min: 2, Max: 120},
		buy.A过滤涨停{},
	}

	// 布林+RSI 买入条件 + 市值过滤
	testBuy := buy.And{
		baseBuyer,
		buy.A流通市值{Min: mcFilter.Min, Max: mcFilter.Max},
		buy.A布林下轨{Period: 20, StdTimes: 2},
		buy.RSI{Period: 14, Threshold: 20},
		buy.MAUp{Period: 60},
	}

	// 卖出：无止损版本（仅持仓20天+布林中轨）
	bollSell := sell.A回到布林中轨{Period: 20}
	testSell := sell.Or{
		sell.A持仓N天{Days: 20},
		bollSell,
	}

	label := fmt.Sprintf("市值[%.0f,%.0f]亿", *mcMin, *mcMax)
	if *mcMax == 0 && *mcMin > 0 {
		label = fmt.Sprintf("市值>%.0f亿", *mcMin)
	}
	if *mcMin == 0 && *mcMax > 0 {
		label = fmt.Sprintf("市值<%.0f亿", *mcMax)
	}
	if *mcMin == 0 && *mcMax == 0 {
		label = "全市值"
	}
	fmt.Printf("\n========== %s ==========\n", label)

	core.Backtest{
		Buyer:        testBuy,
		Seller:       testSell,
		Goroutines:   common.DefaultGoroutines * 2,
		Codes:        codes,
		Years:        years,
		GetDayKlines: common.Pull.DayKlines,
		GetMinKlines: nil,
	}.Run()
}
