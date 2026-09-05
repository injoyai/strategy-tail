package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/injoyai/logs"
	common "github.com/injoyai/strategy-tail"
	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/strategies/buy"
)

// 策略在代码中预设，参照 screen-realtime 的方式
var buyer core.Buyer = buy.And{
	buy.MACD顺滑{},
	buy.MACD转红{},
}

func main() {
	port := 18080
	if len(os.Args) > 1 {
		fmt.Sscanf(os.Args[1], "%d", &port)
	}

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(indexHTML))
	})

	http.HandleFunc("/api/screen", func(w http.ResponseWriter, r *http.Request) {
		date := r.URL.Query().Get("date")
		if date == "" {
			date = time.Now().Format("2006-01-02")
		}
		result, err := screenStocks(date)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(result)
	})

	addr := fmt.Sprintf(":%d", port)
	logs.Infof("服务启动: http://localhost%s", addr)
	logs.PanicErr(http.ListenAndServe(addr, nil))
}

// screenStocks 用指定日期进行选股，返回命中股票及其K线数据
func screenStocks(dateStr string) ([]stockResult, error) {
	date, err := time.ParseInLocation("2006-01-02", dateStr, time.Local)
	if err != nil {
		return nil, fmt.Errorf("日期格式错误: %v", err)
	}

	codes := common.GetNoPriceLimitCodes()

	bs, err := core.Screen{
		Buyer:        buyer,
		ShowBar:      false,
		Goroutines:   common.DefaultGoroutines,
		Codes:        codes,
		GetDayKlines: common.Pull.DayKlines,
	}.Run(date)
	if err != nil {
		return nil, err
	}

	results := make([]stockResult, 0, len(bs))
	for _, b := range bs {
		// 拉取该股票近 120 天 K 线用于展示
		start := date.AddDate(0, 0, -120)
		dks, err := common.Pull.DayKlines(b.Code, start, date)
		if err != nil || len(dks) == 0 {
			continue
		}
		klines := make([]chartKline, 0, len(dks))
		for _, k := range dks {
			klines = append(klines, chartKline{
				Time:   k.Time.Format("2006-01-02"),
				Open:   k.Open.Float64(),
				High:   k.High.Float64(),
				Low:    k.Low.Float64(),
				Close:  k.Close.Float64(),
				Volume: k.Volume,
			})
		}
		results = append(results, stockResult{
			Code:   b.Code,
			Price:  b.Price.Float64(),
			Rise:   b.Rise,
			Klines: klines,
		})
	}

	// 限制最多展示 30 只
	if len(results) > 30 {
		results = results[:30]
	}

	return results, nil
}

type stockResult struct {
	Code   string       `json:"code"`
	Price  float64      `json:"price"`
	Rise   float64      `json:"rise"`
	Klines []chartKline `json:"klines"`
}

type chartKline struct {
	Time   string  `json:"time"`
	Open   float64 `json:"open"`
	High   float64 `json:"high"`
	Low    float64 `json:"low"`
	Close  float64 `json:"close"`
	Volume int64   `json:"volume"`
}
