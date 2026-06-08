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
	//years = []int{2024, 2025, 2026}
	//years = []int{2026}
	//years = []int{2013, 2014, 2015, 2016, 2017, 2018, 2019, 2020, 2021, 2022, 2023, 2024, 2025, 2026}

	core.Backtest{
		Buyer:        buy.TrendVolumeV2{},  //common.DefaultBuyer,
		Seller:       sell.TrendVolumeV2{}, // common.DefaultSeller,
		Goroutines:   20,
		Codes:        codes,
		Years:        years,
		GetDayKlines: common.GetDayKlines,
		GetMinKlines: nil, //common.GetMinKlines,
	}.Run()

}
