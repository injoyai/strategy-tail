package main

import (
	common "github.com/injoyai/strategy-tail"
	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/strategies/buy"
)

func main() {
	codes := common.GetAllCodes()
	_, _, years, _, _ := common.LoadBacktestConfig()

	core.ForwardReturnAnalysis{
		Buyer:        TestBuy,
		Codes:        codes,
		Years:        years,
		GetDayKlines: common.Pull.DayKlines,
		ForwardDays:  core.DefaultForwardDays(),
		Goroutines:   common.DefaultGoroutines * 2,
	}.Run()
}

// TestBuy 买入策略(与 cmd/backtest 保持一致,可按需修改)
var TestBuy = buy.And{
	buy.A通达信倍量{Ratio: 2.9},
}
