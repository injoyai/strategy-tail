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

	common.Update()

	codes := common.GetNoPriceLimitCodes()

	ks, err := common.Pull.DayKlines("sh000001", time.Time{}, time.Now())
	logs.PanicErr(err)
	// logs.Debug(len(ks))
	// return

	b := buy.A指数多头排列{
		Ks:      ks,
		Periods: []int{5, 60},
	}
	_ = b

	// 从 config.yaml 加载成本和仓位配置
	cost, pos, _, benchmark, mcIterations := common.LoadBacktestConfig()

	years := []int{2018, 2019, 2020, 2021, 2022, 2023, 2024, 2025, 2026}
	years = []int{2022, 2023, 2024, 2025, 2026}
	//years = []int{2020, 2021, 2022, 2023, 2024, 2025, 2026}
	years = []int{2026}

	core.Backtest{
		Buyer: buy.And{
			b,
			MACDBuyer,
			//common.MACDBuyer,
		},
		Seller:       MACDSeller,
		Goroutines:   common.DefaultGoroutines * 2,
		Codes:        codes,
		Years:        years,
		GetDayKlines: common.Pull.DayKlines,
		GetMinKlines: common.Pull.MinKlines,

		Benchmark: benchmark,
		Cost:      cost,
		Position:  pos,

		MCIterations: mcIterations,
	}.Run()
}

var (
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

	MACDBuyer2 = buy.And{
		// 常规过滤：流通市值、价格、涨停
		buy.A流通市值{Min: 400},
		buy.A现价{Max: 120},
		buy.A过滤涨停{},

		// MACD 量柱流畅（EMA 平滑后每个同号段反转 <= MaxReversals）
		//buy.MACD顺滑{Smooth: 5, Days: 10, MaxReversals: 1},

		// MACD 低位反转（今天量柱变大 + 昨天为近 4 日最低点）
		buy.MACD反转{MinLookback: 4},

		buy.A现价大于N日均线(30),

		// 30 日均线向上（趋势方向确认）
		buy.MAUp{Period: 20, MinSlope: 0.0005},
		buy.MAUp{Period: 30, MinSlope: 0.0005},
	}

	MACDBuyer = buy.And{
		buy.A流通市值{Min: 400},
		buy.A现价{Max: 120},
		buy.A过滤涨停{},

		buy.MACD负数{MinDays: 6},
		buy.MACD连涨{MinDays: 2, MaxDays: 2},

		buy.A现价大于N日均线(30),

		buy.And{
			buy.MAUp{Period: 30, MinSlope: 0.0005},
			buy.MAUp{Period: 20, MinSlope: 0.0005},
		},
	}

	// MACD转红Buyer 之前MACD量柱为负、连续涨数日后今天量柱由负转红（零轴金叉）。
	// MACD转红 保证今天>0且昨天<=0；MACD连涨{MinDays:3} 保证含今天连涨3天，
	// 因昨天<=0且连涨使得更早柱子更负，已隐含“此前量柱为负”。
	MACD转红Buyer = buy.And{
		buy.A流通市值{Min: 100},
		buy.A现价{Max: 120},
		buy.A过滤涨停{},

		buy.MACD转红{},
		buy.MACD连涨{MinDays: 3, MaxDays: 5},

		buy.And{
			buy.MAUp{Period: 30, MinSlope: 0.0005},
			buy.MAUp{Period: 20, MinSlope: 0.0005},
		},
	}
)
