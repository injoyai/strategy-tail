package main

import (
	common "github.com/injoyai/strategy-tail"
	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/strategies/buy"
)

func main() {
	codes := common.GetAllCodes()
	years := []int{2024, 2025, 2026}

	core.ForwardReturnAnalysis{
		Buyer:        TestBuy,
		Codes:        codes,
		Years:        years,
		GetDayKlines: common.Pull.DayKlines,
		ForwardDays:  core.DefaultForwardDays(),
		Goroutines:   common.DefaultGoroutines * 2,
		// SingleCode: "sz000001", // 可选：仅扫描指定股票
		KlineBefore: 30, // 命中点前显示 30 天 K 线
		KlineAfter:  30, // 命中点后显示 30 天 K 线
		CodeNames:   common.Manage.Codes.GetName,
	}.Run()
}

// TestBuy 买入策略(与 cmd/backtest 保持一致,可按需修改)
var TestBuy = buy.And{
	//buy.A
	buy.A通达信倍量{Ratio: 2.9},
	buy.MAUp{Period: 60},
}
