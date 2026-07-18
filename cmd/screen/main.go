package main

import (
	"time"

	"github.com/injoyai/conv/cfg"
	"github.com/injoyai/logs"
	common "github.com/injoyai/strategy-tail"
	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/strategies/buy"
	"github.com/injoyai/strategy-tail/strategies/sell"
	"github.com/injoyai/tdx/lib/xorms"
)

const dbPath = "./data/database/trade.db"

func init() {
	logs.Info("版本:", "v1.4.1")
	logs.Info("详情:", "古法编程优化版")
}

func main() {

	db, err := xorms.NewSqlite(dbPath)
	if err != nil {
		logs.Panicf("初始化服务失败: %v\n", err)
	}

	svc := &ScreenService{
		DB:           db,
		LookbackDays: cfg.GetInt("screen.lookback", 180),
		Interval:     cfg.GetDuration("screen.interval", time.Second*5),
		Goroutines:   common.DefaultGoroutines,
		Codes:        common.GetAllCodes(),
		Strategies:   strategies,
	}

	logs.Info("开始运行主程序...")
	go svc.Run()

	port := cfg.GetInt("screen.port", 9090)
	useLocal := cfg.GetBool("screen.html.local")
	logs.PrintErr(Api(port, useLocal, svc))
}

/*



 */

var (
	strategies = []Strategy{
		{
			Key:    "macd-premium",
			Name:   "MACD精选",
			Buyer:  common.MACDBuyer,
			Seller: common.MACDSeller,
			Tags: map[string]core.Buyer{
				"科创+":  buy.A科创板{},
				"创业+":  buy.A创业板{},
				"北证^":  buy.A北证板{},
				"中市值+": buy.A流通市值{Min: 600, Max: 800},
				"涨停^":  buy.A涨停{},
				"倍量^":  buy.N天前符合{From: 5, To: 20, Buyer: buy.A通达信倍量{Ratio: 2.9}},
			},
		},
		{
			Key:    "macd-base",
			Name:   "MACD基础(测试)",
			Buyer:  common.MACDBaseBuyer,
			Seller: common.MACDSeller,
			Tags: map[string]core.Buyer{
				"科创+":  buy.A科创板{},
				"创业+":  buy.A创业板{},
				"北证^":  buy.A北证板{},
				"中市值+": buy.A流通市值{Min: 600, Max: 800},
				"涨停^":  buy.A涨停{},
				"倍量^":  buy.N天前符合{From: 5, To: 20, Buyer: buy.A通达信倍量{Ratio: 2.9}},
			},
		},
		{
			Key:  "boll-rsi",
			Name: "布林+RSI(测试)",
			Buyer: buy.And{
				common.BaseBuyer,
				buy.A布林下轨{Period: 20, StdTimes: 2},
				buy.RSI{Period: 14, Threshold: 20},
				buy.MAUp{Period: 60},
			},
			Seller: sell.A回到布林中轨{Period: 20},
			Tags: map[string]core.Buyer{
				"MACD": buy.MACD反转{MinLookback: 4},
			},
		},
	}
)
