package main

import (
	"path/filepath"
	"strings"

	"github.com/injoyai/logs"
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

	key := "pull"
	if updated, err := update.Updated(key); err != nil || !updated {
		//if Manage.Workday.TodayIs() {
		//	err = Pull.Update(Manage)
		//	logs.PanicErr(err)
		//	err = update.Update(key)
		//	logs.PanicErr(err)
		//}
	}

}

func main() {

	codes := []string(nil)
	for _, v := range Manage.Codes.GetStockCodes() {
		if strings.HasPrefix(v, "sh60") || strings.HasPrefix(v, "sz00") {
			codes = append(codes, v)
		}
	}

	s := Backtest{
		IStrategy: Strategy{
			Buyer:  BuyTailMorning{},
			Seller: SellTomorrow("10:00:00"),
		},
	}

	years := []int{2018, 2019, 2020, 2021, 2022, 2023, 2024, 2025}
	years = []int{2023}
	s.Run(codes, years)
}
