package main

import (
	common "github.com/injoyai/strategy-tail"
	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/strategies/buy"
	"github.com/injoyai/strategy-tail/strategies/sell"
)

func main() {

	//获取所有代码（与原版一致）
	codes := common.GetAllCodes()
	//codes = []string{"sh000001"}

	// 从 config.yaml 加载成本和仓位配置
	cost, pos, _, benchmark, _ := common.LoadBacktestConfig()

	years := []int{2018, 2019, 2020, 2021, 2022, 2023, 2024, 2025, 2026}
	//years = []int{2022, 2023, 2024, 2025, 2026}
	//years = []int{2020, 2021, 2022, 2023, 2024, 2025, 2026}
	years = []int{2026}

	core.Backtest{
		Buyer:        TestBuy,
		Seller:       TestSell,
		Goroutines:   common.DefaultGoroutines * 2,
		Codes:        codes,
		Years:        years,
		GetDayKlines: common.Pull.DayKlines,
		GetMinKlines: common.Pull.MinKlines, // nil, // 日线策略无需分钟线，加速回测

		Benchmark: benchmark,
		Cost:      cost,
		Position:  pos,
	}.Run()

}

var (
	// 布林带+RSI 三重确认策略（均值回归）
	// 买入：价格跌破布林下轨(20,2σ) + RSI超卖(<30) + MA60向上(趋势过滤) + 基础过滤
	TestBuy = MACDBuyer

	// 卖出：风控优先(止盈15%/止损8% + 最大持仓20天) + 回到布林中轨
	TestSell = sell.Or{
		//sell.A止盈止损{TakeProfit: 0.15, StopLoss: 0.08}, // 止盈15% 止损8%
		//sell.A持仓N天{Days: 20},                          // 最大持仓20个交易日
		//BollSell,                                         // 回到布林中轨卖出
		MACDSeller,
	}

	BaseBuyer = buy.And{
		buy.A价格{Min: 2, Max: 120},
		buy.A过滤涨停{},
	}

	BollBuy = buy.And{
		BaseBuyer,
		buy.A布林下轨{Period: 20, StdTimes: 2},
		buy.RSI{Period: 14, Threshold: 20},
		buy.MAUp{Period: 60},
	}
	BollSell = sell.A回到布林中轨{Period: 20}

	MACDSeller = sell.Or{
		//无盈利等第二次上升浪
		sell.MACD反转{Lookback: 10},
		//有盈利则在反转的时候卖出
		sell.And{
			sell.A盈利(0.005),
			sell.MACD反转{Lookback: 2},
		},
	}

	MACDBuyer = buy.And{
		buy.A流通市值{Min: 400}, //流通市值大于N亿
		buy.A现价{Max: 120},   //价格小于120,太贵了买不起
		buy.A过滤涨停{},         //过滤涨停,涨停买不进去

		buy.MACD反转{MinLookback: 4}, //MACD
		buy.MACD负数{MinDays: 5},     //MACD阴线,5
		//buy.N天前符合{
		//	From: 1,
		//	To:   2,
		//	Buyer: buy.And{
		//		buy.MACD反转{MinLookback: 4},
		//		buy.MACD负数{MinDays: 5}, //MACD阴线,5
		//	},
		//},

		buy.A现价大于N日均线(30), //当天价格高于N日均线

		buy.And{
			buy.MAUp{Period: 20, MinSlope: 0.0002}, //N日均线向上,且增速大于0.05%
			buy.MAUp{Period: 30, MinSlope: 0.0005}, //N日均线向上,且增速大于0.05%
		},
	}
)
