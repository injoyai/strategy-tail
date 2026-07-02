package main

import (
	"github.com/injoyai/strategy-tail"
	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/strategies/buy"
	"github.com/injoyai/strategy-tail/strategies/sell"
)

func main() {

	//获取无需验资的代码
	codes := common.GetAllCodes() // common.GetNoPriceLimitCodes()

	years := []int{2013, 2014, 2015, 2016, 2017, 2018, 2019, 2020, 2021, 2022, 2023, 2024, 2025, 2026}
	years = []int{2018, 2019, 2020, 2021, 2022, 2023, 2024, 2025, 2026}
	years = []int{2024, 2025, 2026}
	//years = []int{2026}

	core.Backtest{
		Buyer: TestBuyer,
		Seller: sell.Or{
			sell.MACD反转{Lookback: 12},
			//sell.MACD买入后连跌{AfterDays: 3, Days: 5},
		},
		Goroutines:   common.DefaultGoroutines * 2,
		Codes:        codes,
		Years:        years,
		GetDayKlines: common.Pull.DayKlines,
		GetMinKlines: common.Pull.MinKlines,
	}.Run()

}

var (
	TestBuyer = buy.And{
		//buy.A流通市值{Min: 400},
		//buy.A现价{Max: 120},
		//buy.A过滤涨停{},

		buy.A近N天符合(30, buy.A倍量{MinRatio: 2.9}),
		buy.A底顶部抬升{Window: 12},
		buy.MACD连涨{MinDays: 1, MaxDays: 2},
	}

	// PullbackBuyer 多头上涨后缩量回调,MACD量柱反转向上时买入
	// 1. 多头上涨: MA[10,20,30]多头排列 + MA20向上 + MA30向上
	// 2. 缩量回调: 近期高点回调2~8% + 收盘价在MA20附近(±5%) + 今日成交量缩量(<=5日均量1.2倍)
	// 3. MACD反转: 今天MACD量柱比昨天大 + 昨天是近4日最低点(量柱拐头向上)
	PullbackBuyer = buy.And{
		buy.A流通市值{Min: 400},
		buy.A现价{Max: 120},
		buy.A过滤涨停{},

		buy.A底顶部抬升{Window: 8},

		buy.MACD连涨{MinDays: 1},
	}

	// BaseBuyer 57.58% 胜率 1.58 盈亏比 105.01% 年化
	BaseBuyer = buy.And{
		buy.A流通市值{Min: 400},
		buy.A现价{Max: 120},
		buy.A过滤涨停{},

		buy.MAUp{Period: 20},
		buy.MAUp{Period: 30},
		buy.MAUp{Period: 60},

		// MACD量柱反转向上
		buy.MACD反转{MinLookback: 4},
	}
)
