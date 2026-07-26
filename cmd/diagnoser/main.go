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
		Buyer:        Buyer,
		GetDayKlines: common.Pull.DayKlines,
	}

	matched, result := d.Check("600999", time.Now())
	fmt.Println("匹配:", matched)
	fmt.Println(result)
}

var Buyer = buy.And{
	buy.A流通市值{Min: 400}, //流通市值大于N亿
	buy.A现价{Max: 120},   //价格小于120,太贵了买不起
	buy.A过滤涨停{},         //过滤涨停,涨停买不进去

	buy.MACD负数{MinDays: 6}, //MACD阴线,5
	buy.N天前符合{
		From: 1,
		To:   1,
		Buyer: buy.And{
			buy.MACD反转{MinLookback: 4},
		},
	},
	buy.MACD连涨{MinDays: 2, MaxDays: 2},

	buy.A现价大于N日均线(60), //当天价格高于N日均线

	buy.And{
		buy.MAUp{Period: 30, MinSlope: 0.0005}, //N日均线向上,且增速大于0.05%
	},
}
