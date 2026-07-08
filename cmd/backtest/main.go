package main

import (
	common "github.com/injoyai/strategy-tail"
	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/strategies/buy"
	"github.com/injoyai/strategy-tail/strategies/sell"
)

func main() {

	//获取所有代码（与原版一致）
	codes := common.GetAllCodes()
	//codes = []string{"sh600887"}

	// 从 config.yaml 加载成本和仓位配置
	cost, pos, _, benchmark, _ := common.LoadBacktestConfig()

	years := []int{2018, 2019, 2020, 2021, 2022, 2023, 2024, 2025, 2026}
	years = []int{2022, 2023, 2024, 2025, 2026}
	years = []int{2024, 2025, 2026}
	//years = []int{2026}

	core.Backtest{
		Buyer:        common.MACDBuyer,
		Seller:       TestSell,
		Goroutines:   common.DefaultGoroutines * 2,
		Codes:        codes,
		Years:        years,
		GetDayKlines: common.Pull.DayKlines,
		GetMinKlines: common.Pull.MinKlines,

		Benchmark: benchmark,
		Cost:      cost,
		Position:  pos,
	}.Run()

}

var (
	TestBuy = buy.And{
		//common.MACDBaseBuyer,
		//buy.Not(buy.A近),
		BollBuy,
		//buy.MACD反转{MinLookback: 4},
	}

	TestSell = sell.Or{
		common.MACDSeller,
		sell.And{
			sell.A盈利(0.005),
			sell.MACD反转{Lookback: 2},
		},
	}

	BaseBuyer = buy.And{
		buy.A价格{Min: 2, Max: 120},
		buy.A过滤涨停{},
	}

	BollBuy = buy.And{
		BaseBuyer,
		buy.A布林下轨{Period: 20, StdTimes: 2},
		buy.RSI{Period: 14, Threshold: 30},
		buy.MAUp{Period: 60},
	}
	BollSell = sell.A回到布林中轨{Period: 20}
)
