package main

import (
	"time"

	"github.com/injoyai/conv/cfg"
	"github.com/injoyai/logs"
	common "github.com/injoyai/strategy-tail"
	"github.com/injoyai/strategy-tail/internal/xorms"
	"github.com/injoyai/tdx/v2"
)

func init() {
	logs.Info("版本:", "v1.4.6")
	logs.Info("详情:", "修复盘后历史数据没有更新的问题")
}

func main() {
	common.MustInitialize()
	pool, err := tdx.NewPool(func() (tdx.Client, error) { return tdx.DialDefault() }, 1)
	logs.PanicErr(err)
	defer pool.Close()
	common.Manage.ClientPool = pool

	ks, err := common.Pull.DayKlines("sh000001", time.Time{}, time.Now())
	logs.PanicErr(err)
	db, err := xorms.NewSQLite(screenDBPath())
	if err != nil {
		logs.Panicf("初始化服务失败: %v\n", err)
	}

	svc := &ScreenService{
		DB:           db,
		LookbackDays: cfg.GetInt("screen.lookback", 180),
		Interval:     cfg.GetDuration("screen.interval", time.Second*5),
		Goroutines:   common.DefaultGoroutines,
		Codes:        common.GetAllCodes(),
		Strategies:   screenStrategies(ks),
	}

	go func() {
		logs.Info("开始运行主程序...")
		logs.PrintErr(svc.Run())
	}()

	port := cfg.GetInt("screen.port", 9090)
	useLocal := cfg.GetBool("screen.html.local")
	logs.PrintErr(Api(port, useLocal, svc))
}
