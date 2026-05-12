package main

import (
	"path/filepath"
	"strings"

	"github.com/injoyai/logs"
	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/strategies"
	"github.com/injoyai/tdx"
	"github.com/injoyai/tdx/extend"
	"github.com/injoyai/tdx/lib/xorms"
)

var (
	DatabaseDir = tdx.DefaultDatabaseDir
	DayKlineDir = filepath.Join(DatabaseDir, "day-kline")
	MinKlineDir = filepath.Join(DatabaseDir, "min-kline")
	Pull        *extend.PullKline
	Manage      *tdx.Manage
)

func init() {
	logs.SetFormatter(logs.TimeFormatter)

	db, err := xorms.NewSqlite(filepath.Join(DatabaseDir, "update.db"))
	logs.PanicErr(err)

	update, err := tdx.NewUpdated(db, 15, 1)
	logs.PanicErr(err)

	Manage, err = tdx.NewManage(tdx.WithDialGbbqDefault())
	logs.PanicErr(err)

	Pull = extend.NewPullKline(extend.PullKlineConfig{
		Tables:     []string{extend.Day},
		Dir:        DayKlineDir,
		Goroutines: 10,
	})

	if updated, err := update.Updated("pull"); err != nil || !updated {
		if Manage.Workday.TodayIs() {
			err = Pull.Update(Manage)
			logs.PanicErr(err)
			err = update.Update("pull")
			logs.PanicErr(err)
		}
	}

}

func main() {

	codes := []string(nil)
	for _, v := range Manage.Codes.GetStockCodes() {
		if strings.HasPrefix(v, "sh60") || strings.HasPrefix(v, "sz00") {
			codes = append(codes, v)
		}
	}

	years := []int{2018, 2019, 2020, 2021, 2022, 2023, 2024, 2025, 2026}
	//years = []int{2024}

	core.Backtest{
		Strategy: core.Strategy{
			Buyer:  strategies.BuyMACD{Lookback: 20},
			Seller: strategies.SellMACD{Lookback: 20},
		},
		Codes:        codes,
		Years:        years,
		GetDayKlines: getDayKlines,
		GetMinKlines: getMinKlines,
	}.Run()

	//core.Future{
	//	Buyer:        strategies.BuyRSI{},
	//	Days:         []int{0, 1, 2, 3, 5, 10, 15, 20},
	//	Years:        years,
	//	Codes:        codes,
	//	GetDayKlines: getDayKlines,
	//	GetMinKlines: getMinKlines,
	//}.Run()

}
