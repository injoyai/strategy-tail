package main

import (
	common "github.com/injoyai/strategy-tail"
	"github.com/injoyai/strategy-tail/core"
)

func main() {
	common.Update()

	codes := common.GetNoPriceLimitCodes()

	// 从 config.yaml 加载成本和仓位配置
	cost, pos, _, benchmark, mcIterations := common.LoadBacktestConfig()

	// 仅测 2026 年（与前面几轮策略回测保持一致口径）
	years := []int{2026}

	core.Backtest{
		Buyer:        common.MACDBuyer,
		Seller:       common.MACDSeller,
		Goroutines:   common.DefaultGoroutines * 2,
		Codes:        codes,
		Years:        years,
		GetDayKlines: common.Pull.DayKlines,
		GetMinKlines: common.Pull.MinKlines,

		Benchmark:    benchmark,
		Cost:         cost,
		Position:     pos,
		MCIterations: mcIterations,
	}.Run()
}
