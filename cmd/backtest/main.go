package main

import (
	common "github.com/injoyai/strategy-tail"
	"github.com/injoyai/strategy-tail/core"
)

func main() {

	//获取所有代码（与原版一致）
	codes := common.GetAllCodes()
	//codes = []string{"sh600887"}

	// 从 config.yaml 加载成本和仓位配置
	cost, pos, _, benchmark, _ := common.LoadBacktestConfig()

	years := []int{2022, 2023, 2024, 2025, 2026}
	years = []int{2026}

	core.Backtest{
		Buyer:        common.MACDBuyer,
		Seller:       common.MACDSeller,
		Goroutines:   common.DefaultGoroutines * 2,
		Codes:        codes,
		Years:        years,
		GetDayKlines: common.Pull.DayKlines,
		GetMinKlines: common.Pull.MinKlines,
		Benchmark:    benchmark,

		// 成本和仓位从 config.yaml 读取
		Cost:     cost,
		Position: pos,
	}.Run()

}
