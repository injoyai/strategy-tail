package common

import (
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/injoyai/goutil/oss"
	"github.com/injoyai/logs"
	"github.com/injoyai/strategy-tail/strategies/buy"
	"github.com/injoyai/strategy-tail/strategies/sell"
	"github.com/injoyai/tdx"
	"github.com/injoyai/tdx/extend"
	"github.com/injoyai/tdx/lib/xorms"
	"github.com/injoyai/tdx/protocol"
)

var (
	DefaultBuyer = buy.And{
		buy.A流通市值{Min: 400}, //流通市值大于N亿
		buy.A现价{Max: 120},   //价格小于120,太贵了买不起
		buy.A过滤涨停{},         //过滤涨停,涨停买不进去

		buy.MACD{Lookback: 20}, //MACD
		buy.MACD负数{Days: 5},    //MACD阴线,5

		buy.A现价大于N日均线(30), //当天价格高于N日均线

		buy.And{
			buy.MAUp{Period: 20, MinSlope: 0.0002}, //N日均线向上,且增速大于0.05%
			buy.MAUp{Period: 30, MinSlope: 0.0005}, //N日均线向上,且增速大于0.05%
		},
	}
	DefaultSeller = sell.Or{
		sell.MACD{Lookback: 10},
	}
)

const (
	万 = 1e4
	亿 = 1e8
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

func GetNoPriceLimitCodes() []string {
	codes := []string(nil)
	for _, code := range Manage.Codes.GetStockCodes() {
		if strings.HasPrefix(code, "sh60") || strings.HasPrefix(code, "sz00") {
			codes = append(codes, code)
		}
	}
	return codes
}

func GetDayKlines(code string, start, end time.Time) (extend.Klines, error) {
	ks, err := Pull.DayKlines(code)
	if err != nil {
		return nil, err
	}
	ls := extend.Klines{}
	for _, k := range ks {
		if k.Time.Before(start) || k.Time.After(end) {
			continue
		}
		ls = append(ls, k)
	}
	return ls, nil
}

func GetMinKlines(code string, start, end time.Time) (protocol.Klines, error) {
	years := []int(nil)
	for i := start.Year(); i <= end.Year(); i++ {
		years = append(years, i)
	}
	ks := protocol.Klines{}
	mu := sync.Mutex{}
	wg := sync.WaitGroup{}
	for _, year := range years {
		wg.Add(1)
		go func(code string, year int) {
			defer wg.Done()
			filename := filepath.Join(MinKlineDir, code, code+"-"+strconv.Itoa(year)+".db")
			if !oss.Exists(filename) {
				return
			}
			db, err := xorms.NewSqlite(filename)
			if err != nil {
				logs.Err(err)
				return
			}
			defer db.Close()
			ls := protocol.Klines{}
			err = db.Find(&ls)
			if err != nil {
				logs.Err(err)
				return
			}
			res := protocol.Klines{}
			for _, l := range ls {
				if l.Time.Year() != year {
					continue
				}
				res = append(res, l)
			}
			mu.Lock()
			defer mu.Unlock()
			ks = append(ks, res...)
		}(code, year)
	}
	wg.Wait()
	ks.Sort()
	return ks, nil
}
