package main

import (
	"fmt"
	"time"

	common "github.com/injoyai/strategy-tail"
	"github.com/injoyai/strategy-tail/core"
)

func main() {
	d := &core.Diagnoser{
		Buyer:        common.MACDBuyer,
		GetDayKlines: common.Pull.DayKlines,
	}

	matched, result := d.Check("sz000988", time.Date(2026, 06, 12, 0, 0, 0, 0, time.Local))
	fmt.Println("匹配:", matched)
	fmt.Println(result)
}
