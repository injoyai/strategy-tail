package main

import (
	"time"

	"github.com/injoyai/logs"
	common "github.com/injoyai/strategy-tail"
	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/strategies/buy"
	"github.com/injoyai/strategy-tail/strategies/sell"
)

func main() {

	codes := common.GetNoPriceLimitCodes()

	ks, err := common.Pull.DayKlines("sh000300", time.Time{}, time.Now())
	logs.PanicErr(err)
	// logs.Debug(len(ks))
	// return

	b := buy.A指数多头排列{
		Ks:      ks,
		Periods: []int{10, 20, 60, 120},
	}
	_ = b

	// 从 config.yaml 加载成本和仓位配置
	cost, pos, _, benchmark, _ := common.LoadBacktestConfig()

	years := []int{2018, 2019, 2020, 2021, 2022, 2023, 2024, 2025, 2026}
	years = []int{2022, 2023, 2024, 2025, 2026}
	//years = []int{2020, 2021, 2022, 2023, 2024, 2025, 2026}
	//years = []int{2026}

	core.Backtest{
		Buyer: buy.And{
			TestBuy,
		},
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

	core.Backtest{
		Buyer: buy.And{
			b,
			TestBuy,
		},
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
	TestBuy = MACDBuyer

	TestSell = sell.Or{
		MACDSeller,
	}

	BaseBuyer = buy.And{
		buy.A价格{Min: 2, Max: 120},
		buy.A过滤涨停{},
	}

	BollBuy = buy.And{
		BaseBuyer,
		buy.A布林下轨{Period: 20, StdTimes: 2},
		buy.RSI{Period: 14, Threshold: 20},
		buy.MAUp{Period: 60},
	}
	BollSell = sell.A回到布林中轨{Period: 20}

	MACDSeller = sell.Or{
		sell.MACD反转{Lookback: 10},
	}

	MACDBuyer = buy.And{
		buy.A流通市值{Min: 400},
		buy.A现价{Max: 120},
		buy.A过滤涨停{},

		buy.MACD负数{MinDays: 6},
		buy.MACD连涨{MinDays: 1, MaxDays: 1},

		buy.A现价大于N日均线(45),

		buy.And{
			buy.MAUp{Period: 30, MinSlope: 0.0005},
			buy.MAUp{Period: 20, MinSlope: 0.0005},
		},
	}
)
