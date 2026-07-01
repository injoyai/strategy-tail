package main

import (
	"fmt"
	"time"

	common "github.com/injoyai/strategy-tail"
	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/strategies/buy"
)

func main() {
	d := &core.Diagnoser{
		Buyer:        buy.A底部抬升{},
		GetDayKlines: common.Pull.DayKlines,
	}

	matched, result := d.Check("sz000988", time.Date(2026, 06, 12, 0, 0, 0, 0, time.Local))
	fmt.Println("匹配:", matched)
	fmt.Println(result)
}
