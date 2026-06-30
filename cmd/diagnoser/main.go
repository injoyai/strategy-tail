package main

import (
	"fmt"

	common "github.com/injoyai/strategy-tail"
	"github.com/injoyai/strategy-tail/core"
)

func main() {
	d := &core.Diagnoser{
		Buyer:        common.MACDBuyer,
		GetDayKlines: common.Pull.DayKlines,
	}

	matched, result := d.Check("sh601138")
	fmt.Println("匹配:", matched)
	fmt.Println(result)
}
