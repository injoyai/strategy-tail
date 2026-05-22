package main

import (
	"path/filepath"
	"strings"
	"time"

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
	now         = time.Now()
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
	for _, code := range Manage.Codes.GetStockCodes() {
		if strings.HasPrefix(code, "sh60") || strings.HasPrefix(code, "sz00") {

			//获取最新价格
			ks, err := Pull.DayKlines(code)
			logs.PanicErr(err)
			if len(ks) == 0 {
				continue
			}

			last := ks[len(ks)-1]

			if last.Close.Float64() > 120 {
				continue
			}

			//计算市值
			if x := last.FloatValue().Float64() / 1e8; x < 1000 {
				continue
			}

			codes = append(codes, code)
		}
	}

	years := []int{2018, 2019, 2020, 2021, 2022, 2023, 2024, 2025, 2026}
	years = []int{2026}

	core.Backtest{
		BuyAll: core.BuyAll{Buyers: []core.Buyer{
			strategies.BuyMACD{Lookback: 20},
			strategies.BuyMACDNegative{Days: 5},
		}},
		SellAny: core.SellAny{Sellers: []core.Seller{
			strategies.SellMACD{Lookback: 10},
		}},
		Codes:        codes,
		Years:        years,
		GetDayKlines: getDayKlines,
		GetMinKlines: getMinKlines,
	}.Run()

}
