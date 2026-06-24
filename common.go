package common

import (
	"strings"
	"time"

	"github.com/injoyai/conv/cfg"
	"github.com/injoyai/logs"
	"github.com/injoyai/strategy-tail/lib/extend"
	"github.com/injoyai/strategy-tail/strategies/buy"
	"github.com/injoyai/strategy-tail/strategies/sell"
	"github.com/injoyai/tdx"
)

var (
	DefaultBuyer  = MACDBuyer
	DefaultSeller = MACDSeller

	MACDBuyer = buy.And{
		buy.A流通市值{Min: 400}, //流通市值大于N亿
		buy.A现价{Max: 120},     //价格小于120,太贵了买不起
		buy.A过滤涨停{},         //过滤涨停,涨停买不进去

		buy.MACD{Lookback: 4}, //MACD
		buy.MACD负数{Days: 5}, //MACD阴线,5

		buy.A现价大于N日均线(30), //当天价格高于N日均线

		buy.And{
			buy.MAUp{Period: 20, MinSlope: 0.0002}, //N日均线向上,且增速大于0.05%
			buy.MAUp{Period: 30, MinSlope: 0.0005}, //N日均线向上,且增速大于0.05%
		},
	}
	MACDSeller = sell.Or{
		sell.MACD{Lookback: 10},
	}
)

const (
	万                = 1e4
	亿                = 1e8
	DefaultGoroutines = 10
	DatabaseDir       = tdx.DefaultDatabaseDir
)

var (
	Pull   *extend.PullKline
	Manage *tdx.Manage
)

func init() {
	logs.SetFormatter(logs.TimeFormatter)

	var err error

	Manage, err = tdx.NewManage(tdx.WithDialGbbqDefault())
	logs.PanicErr(err)

	Pull, err = extend.NewPullKline(extend.PullKlineConfig{
		Types:      cfg.GetStrings("pull.types", []string{extend.Day}),
		Dir:        cfg.GetString("pull.database", tdx.DefaultDatabaseDir),
		Goroutines: cfg.GetInt("pull.goroutines", DefaultGoroutines),
	})
	logs.PanicErr(err)

	Pull.Update(Manage)
	go func() {
		for range time.NewTimer(time.Minute).C {
			if Manage.Workday.TodayIs() {
				logs.PrintErr(Pull.Update(Manage))
			}
		}
	}()

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

func Get科创Codes() []string {
	codes := []string(nil)
	for _, code := range Manage.Codes.GetStockCodes() {
		if strings.HasPrefix(code, "sh68") {
			codes = append(codes, code)
		}
	}
	return codes
}

func Get创业Codes() []string {
	codes := []string(nil)
	for _, code := range Manage.Codes.GetStockCodes() {
		if strings.HasPrefix(code, "sz30") {
			codes = append(codes, code)
		}
	}
	return codes
}

func GetAllCodes() []string {
	return Manage.Codes.GetStockCodes()
}

// IsTradingTime - 判断是否处于交易时间段
// 交易时间：上午 09:30 - 11:30，下午 13:00 - 15:01
func IsTradingTime() bool {
	now := time.Now()
	h, m := now.Hour(), now.Minute()

	// 上午 09:30 - 11:30
	if h == 9 && m >= 30 {
		return true
	}
	if h == 10 {
		return true
	}
	if h == 11 && m <= 31 {
		return true
	}

	// 下午 13:00 - 15:01
	if h == 13 || h == 14 {
		return true
	}
	if h == 15 && m <= 1 {
		return true
	}

	return false
}
