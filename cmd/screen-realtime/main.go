package main

import (
	"github.com/injoyai/logs"
	common "github.com/injoyai/strategy-tail"
	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/strategies/buy"
)

func main() {
	codes := common.GetAllCodes()

	bs, err := core.Screen{
		Buyer:        buy.A倍量{},
		ShowBar:      true,
		Goroutines:   common.DefaultGoroutines,
		Codes:        codes,
		GetDayKlines: common.Pull.DayKlines,
	}.Run()
	logs.PanicErr(err)

	for _, v := range bs {
		logs.Debug(v)
	}

}
