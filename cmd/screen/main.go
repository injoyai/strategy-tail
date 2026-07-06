package main

import (
	"time"

	"github.com/injoyai/conv/cfg"
	"github.com/injoyai/logs"
	common "github.com/injoyai/strategy-tail"
	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/strategies/buy"
	"github.com/injoyai/tdx/lib/xorms"
)

const dbPath = "./data/database/trade.db"

func main() {

	db, err := xorms.NewSqlite(dbPath)
	if err != nil {
		logs.Panicf("初始化服务失败: %v\n", err)
	}

	//可切换策略列表
	strategies := []StrategyDef{
		{Key: "macd", Name: "MACD策略", Buyer: common.MACDBuyer},
		{Key: "base", Name: "Base策略", Buyer: common.BaseBuyer},
	}

	//构建联合 Buyer(所有策略的 Or,扫描用)
	union := make([]core.Buyer, 0, len(strategies))
	for _, st := range strategies {
		union = append(union, buy.Strategy(st.Name, st.Buyer))
	}

	svc := &ScreenService{
		DB:           db,
		LookbackDays: cfg.GetInt("screen.lookback", 60),
		Interval:     cfg.GetDuration("screen.interval", time.Second*5),
		Goroutines:   common.DefaultGoroutines,
		Codes:        common.GetAllCodes(),
		Strategies:   strategies,
		Seller:       common.MACDSeller,
		Buyer:        buy.Strategy("全部", buy.Or(union)),
		Tags: map[string]core.Buyer{
			"科创+":  buy.A科创板{},
			"创业+":  buy.A创业板{},
			"北证^":  buy.A北证板{},
			"中市值+": buy.A流通市值{Min: 600, Max: 800},
			"涨停^":  buy.A涨停{},
		},
	}

	//初始化
	if err := svc.Init(); err != nil {
		logs.Panicf("初始化服务失败: %v\n", err)
	}

	logs.Info("开始运行主程序...")
	go svc.Run()

	port := cfg.GetInt("screen.port", 9090)
	useLocal := cfg.GetBool("screen.html.local")
	logs.PrintErr(Api(port, useLocal, svc))
}

var _ core.Buyer = Screen{}

type Screen struct {
	name string
	core.Buyer
	Tags map[string]core.Buyer
}

func (s Screen) Name() string {
	return s.name
}
