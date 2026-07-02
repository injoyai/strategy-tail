package main

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/injoyai/lorca"
	common "github.com/injoyai/strategy-tail"
	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/strategies/buy"
)

type chartData struct {
	Code        string              `json:"code"`
	Strategy    string              `json:"strategy"`
	Matched     bool                `json:"matched"`
	Klines      []chartKline        `json:"klines"`
	Annotations []core.Annotation   `json:"annotations"`
	Explain     []core.ExplainStep  `json:"explain"`
	Diagnosis   core.DiagnoseResult `json:"diagnosis"`
}

type chartKline struct {
	Time   string  `json:"time"`
	Open   float64 `json:"open"`
	High   float64 `json:"high"`
	Low    float64 `json:"low"`
	Close  float64 `json:"close"`
	Volume int64   `json:"volume"`
}

func main() {
	code := "sh601991"
	var buyer core.Buyer
	buyer = buy.Strategy("测试", buy.And{
		buy.A底顶部抬升{Window: 12},
		buy.MACD连涨{MinDays: 1, MaxDays: 2},
		buy.A近N天符合(30, buy.A倍量{MinRatio: 2.9}),
	})
	end := time.Now()
	end = time.Date(2026, 4, 8, 15, 1, 0, 0, time.Local)

	// 拉取近1年日线
	start := end.AddDate(-1, 0, 0)
	dks, err := common.Pull.DayKlines(code, start, end)
	if err != nil {
		fmt.Printf("拉取数据失败: %v\n", err)
		os.Exit(1)
	}

	// 判断是否命中
	matched := buyer.Buy(code, dks)

	// 获取标注点（如策略实现了 Visualizer）
	var anns []core.Annotation
	if v, ok := buyer.(core.Visualizer); ok {
		anns = v.Annotate(code, dks)
	}

	// 获取逐步判定原因（如策略实现了 Explainer）
	var explain []core.ExplainStep
	if e, ok := buyer.(core.Explainer); ok {
		explain = e.Explain(code, dks)
	}

	// 获取诊断树
	d := &core.Diagnoser{
		Buyer:        buyer,
		GetDayKlines: common.Pull.DayKlines,
	}
	_, diagnosis := d.Check(code, end)

	// 组装图表数据
	data := chartData{
		Code:        code,
		Strategy:    buyer.Name(),
		Matched:     matched,
		Annotations: anns,
		Explain:     explain,
		Diagnosis:   diagnosis,
	}
	for _, k := range dks {
		data.Klines = append(data.Klines, chartKline{
			Time:   k.Time.Format("2006-01-02"),
			Open:   k.Open.Float64(),
			High:   k.High.Float64(),
			Low:    k.Low.Float64(),
			Close:  k.Close.Float64(),
			Volume: k.Volume,
		})
	}

	// 序列化为 JSON
	jsonBytes, err := json.Marshal(data)
	if err != nil {
		fmt.Printf("序列化失败: %v\n", err)
		os.Exit(1)
	}

	// 生成 HTML
	html := renderHTML(jsonBytes)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		fmt.Printf("启动本地服务失败: %v\n", err)
		os.Exit(1)
	}
	defer listener.Close()

	server := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(html))
		}),
	}
	defer server.Close()
	go func() {
		_ = server.Serve(listener)
	}()

	indexURL := "http://" + listener.Addr().String()

	fmt.Printf("股票: %s  策略: %s  命中: %v\n", code, buyer.Name(), matched)
	fmt.Printf("K线: %d 根  标注: %d 个\n", len(data.Klines), len(anns))
	fmt.Printf("正在打开浏览器... %s\n", indexURL)

	// 用 lorca 打开浏览器
	err = lorca.Run(&lorca.Config{
		Width:  1400,
		Height: 850,
		Index:  indexURL,
	})
	if err != nil {
		fmt.Printf("打开浏览器失败: %v\n", err)
		fmt.Printf("可手动打开: %s\n", indexURL)
		os.Exit(1)
	}
}
