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

	codes := []string(nil)
	for _, code := range getNoPriceLimitCodes() {
		//获取最新价格
		ks, err := Pull.DayKlines(code)
		logs.PanicErr(err)
		if len(ks) == 0 {
			continue
		}

		//计算市值
		if x := ks[len(ks)-1].FloatValue().Float64() / 1e8; x < 1000 {
			continue
		}

		codes = append(codes, code)
	}

	years := []int{2018, 2019, 2020, 2021, 2022, 2023, 2024, 2025, 2026}
	//years = []int{2026}

	core.Backtest{
		BuyAll: core.BuyAll{Buyers: []core.Buyer{
			buy.NotLimitUp{},       //过滤涨停,涨停买不进去
			buy.Price{Max: 120},    //价格小于120,太贵了买不起
			buy.MACD{Lookback: 20}, //MACD
			//buy.MACDNegative{Days: 5},              //MACD阴线
			// buy.MAUp{Period: 15}, //均线向上

			//buy.VolumeShrink{Days: 20, Ratio: 0.8}, //缩量
		}},
		SellAny: core.SellAny{Sellers: []core.Seller{
			sell.MACD{Lookback: 10},
		}},
		Codes:        codes,
		Years:        years,
		GetDayKlines: getDayKlines,
		GetMinKlines: getMinKlines,
	}.Run()

}
