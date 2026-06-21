package main

import (
	"github.com/injoyai/strategy-tail"
	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/strategies/buy"
	"github.com/injoyai/strategy-tail/strategies/sell"
)

func main() {

	//获取无需验资的代码
	codes := common.GetNoPriceLimitCodes()

	years := []int{2018, 2019, 2020, 2021, 2022, 2023, 2024, 2025, 2026}
	years = []int{2024, 2025, 2026}
	//years = []int{2026}
	//years = []int{2013, 2014, 2015, 2016, 2017, 2018, 2019, 2020, 2021, 2022, 2023, 2024, 2025, 2026}

	core.Backtest{
		Buyer: buy.And{
			buy.A流通市值{Min: 400}, //流通市值大于N亿
			buy.A现价{Max: 120},   //价格小于120,太贵了买不起
			buy.A过滤涨停{},         //过滤涨停,涨停买不进去

			buy.MACD{Lookback: 4}, //MACD
			buy.MACD负数{Days: 5},   //MACD阴线,5

			buy.A现价大于N日均线(30), //当天价格高于N日均线

			buy.A单日涨幅范围{Min: 0, Max: 8}, //限制单日涨幅

			buy.And{
				buy.MAUp{Period: 20, MinSlope: 0.0002}, //N日均线向上,且增速大于0.05%
				buy.MAUp{Period: 30, MinSlope: 0.0005}, //N日均线向上,且增速大于0.05%
			},
		},
		Seller: sell.Or{
			sell.MACD{Lookback: 10},
			//sell.A固定止盈(0.2),
		},
		Goroutines:   20,
		Codes:        codes,
		Years:        years,
		GetDayKlines: common.Pull.DayKlines,
		GetMinKlines: common.Pull.MinKlines,
	}.Run()

}
