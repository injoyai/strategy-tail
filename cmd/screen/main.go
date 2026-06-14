package main

import (
	"time"

	"github.com/injoyai/conv/cfg"
	"github.com/injoyai/logs"
	common "github.com/injoyai/strategy-tail"
	"github.com/injoyai/tdx/lib/xorms"
)

const dbPath = "./data/database/trade.db"

func main() {

	db, err := xorms.NewSqlite(dbPath)
	if err != nil {
		logs.Panicf("初始化服务失败: %v\n", err)
	}
	logs.Info("数据库连接成功...")

	svc := &ScreenService{
		DB:           db,
		LookbackDays: cfg.GetInt("lookback_days", 10),
		Interval:     cfg.GetDuration("interval", time.Second*10),
		Codes:        common.GetNoPriceLimitCodes(),
		Buyer:        common.MACDBuyer,
		Seller:       common.MACDSeller,
	}

	//初始化
	logs.Info("开始初始化")
	if err := svc.Init(); err != nil {
		logs.Panicf("初始化服务失败: %v\n", err)
	}

	go svc.Run()

	port := cfg.GetInt("port", 9090)
	logs.PrintErr(Api(port, svc))
}
