package main

import (
	"path/filepath"
	"strings"
	"time"

	"github.com/injoyai/logs"
	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/strategies/buy"
	"github.com/injoyai/strategy-tail/strategies/sell"
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
		Tables:     []string{extend.Day}, //, extend.Minute},
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

func getNoPriceLimitCodes() []string {
	codes := []string(nil)
	for _, code := range Manage.Codes.GetStockCodes() {
		if strings.HasPrefix(code, "sh60") || strings.HasPrefix(code, "sz00") {
			codes = append(codes, code)
		}
	}
	return codes
}

func main() {

	//获取无需验资的代码
	codes := getNoPriceLimitCodes()

	years := []int{2018, 2019, 2020, 2021, 2022, 2023, 2024, 2025, 2026}
	//years = []int{2026}

	core.Backtest{
		Buyer: buy.And{
			buy.NotLimitUp{},               //过滤涨停,涨停买不进去
			buy.Price{Max: 120},            //价格小于120,太贵了买不起
			buy.FloatMarketValue{Min: 300}, //流通市值大于N亿
			buy.MACD{Lookback: 20},         //MACD
			//buy.MACDNegative{Days: 5},  //MACD阴线

			buy.And{
				buy.MAUp{Period: 60},  //均线向上
				buy.MAUp{Period: 250}, //均线向上
			},

			//buy.VolumeShrink{Days: 20, Ratio: 0.8}, //缩量
		},
		Seller: sell.Or{
			sell.MACD{Lookback: 3},
		},
		Goroutines:   20,
		Codes:        codes,
		Years:        years,
		GetDayKlines: getDayKlines,
		GetMinKlines: getMinKlines,
	}.Run()

}
