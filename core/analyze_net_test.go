package core

import (
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/injoyai/strategy-tail/lib/extend"
)

func TestAnalyzeSettlementOrderIndependent(t *testing.T) {
	for _, key := range []string{"buy_time", "code", "same_identity"} {
		t.Run(key, func(t *testing.T) {
			sell := time.Date(2025, 1, 10, 15, 0, 0, 0, time.Local)
			trades := make([]Trade, 4)
			for i, profit := range []float64{100, -60, -60, 20} {
				trades[i] = Trade{Code: "sh600000", BuyTime: sell.AddDate(0, 0, i-9), SellTime: sell,
					BuyPrice: 10, SellPrice: (1000 + profit) / 100,
					BuyCost: 1000, SellIncome: 1000 + profit, Quantity: 100}
				if key != "buy_time" {
					trades[i].BuyTime = sell.AddDate(0, 0, -5)
				}
				if key == "code" {
					trades[i].Code = []string{"sh600001", "sh600002", "sh600003", "sh600004"}[i]
				}
			}
			want := Analyze(2025, trades, nil, nil, Cost{}, DefaultPositionConfig())
			if want.MaxDrawdown != 120 {
				t.Fatalf("drawdown = %v, want 120", want.MaxDrawdown)
			}
			wantHTML, err := buildTradeVisualHTML(nil, []AnalyzeResult{want}, map[int][]Trade{2025: append([]Trade(nil), trades...)})
			if err != nil {
				t.Fatal(err)
			}
			for _, order := range [][]int{{1, 0, 2, 3}, {3, 2, 1, 0}, {2, 3, 0, 1}} {
				shuffled := make([]Trade, len(trades))
				for i, j := range order {
					shuffled[i] = trades[j]
				}
				got := Analyze(2025, shuffled, nil, nil, Cost{}, DefaultPositionConfig())
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("order %v changed statistics: got %+v, want %+v", order, got, want)
				}
				html, err := buildTradeVisualHTML(nil, []AnalyzeResult{got}, map[int][]Trade{2025: shuffled})
				if err != nil || html != wantHTML {
					t.Fatalf("order %v changed report: %v", order, err)
				}
			}
		})
	}
}

func TestAnalyzeUsesNetProfitAmount(t *testing.T) {
	chdirTemp(t)
	buyTime := time.Date(2026, 1, 5, 15, 0, 0, 0, time.Local)
	trade := Trade{
		Code:       "sh600000",
		BuyTime:    buyTime,
		SellTime:   buyTime.AddDate(0, 0, 1),
		BuyPrice:   10,
		SellPrice:  10.5,
		BuyCost:    1005,
		SellIncome: 1030,
		Quantity:   100,
	}
	getDayKlines := func(string, time.Time, time.Time) (extend.Klines, error) {
		return nil, nil
	}

	result := Analyze(2026, []Trade{trade}, getDayKlines, nil, Cost{}, PositionConfig{})
	if math.Abs(result.TotalProfit-25) > 1e-6 {
		t.Fatalf("expected net TotalProfit=25, got %v", result.TotalProfit)
	}
	wantRate := (1030.0 - 1005.0) / 1005.0 * 100
	if math.Abs(result.AvgProfit-wantRate) > 1e-6 {
		t.Fatalf("expected net AvgProfit=%v, got %v", wantRate, result.AvgProfit)
	}
}

func TestAnalyzeDoesNotWriteArtifacts(t *testing.T) {
	chdirTemp(t)
	later := time.Date(2026, 1, 6, 15, 0, 0, 0, time.Local)
	earlier := later.AddDate(0, 0, -1)
	trades := []Trade{{BuyTime: later}, {BuyTime: earlier}}
	Analyze(2026, trades, nil, nil, Cost{}, PositionConfig{})
	if _, err := os.Stat("output"); !os.IsNotExist(err) {
		t.Fatalf("Analyze should be pure, output stat error = %v", err)
	}
	if !trades[0].BuyTime.Equal(later) {
		t.Fatal("Analyze should not reorder caller trades")
	}
}

func TestExportYearTradesCSVWritesExplicitArtifact(t *testing.T) {
	chdirTemp(t)
	trade := Trade{
		Code:       "sh600000",
		BuyTime:    time.Date(2026, 1, 5, 15, 0, 0, 0, time.Local),
		SellTime:   time.Date(2026, 1, 6, 15, 0, 0, 0, time.Local),
		BuyPrice:   10,
		SellPrice:  10.5,
		BuyCost:    1005,
		SellIncome: 1030,
		Quantity:   100,
	}

	path, err := ExportYearTradesCSV(2026, []Trade{trade})
	if err != nil {
		t.Fatal(err)
	}
	if path != filepath.Join("output", "backtest", "2026.csv") {
		t.Fatalf("unexpected path: %s", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "净盈亏") {
		t.Fatalf("CSV missing net-profit header: %s", data)
	}
}

func TestExportTradeVisualHTMLRejectsMissingLoader(t *testing.T) {
	err := ExportTradeVisualHTML([]int{2026}, map[int][]Trade{2026: nil}, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "缺少日线数据源") {
		t.Fatalf("ExportTradeVisualHTML() error = %v", err)
	}
}

func TestCalculateRequiredCapitalMatchesDailyExposure(t *testing.T) {
	d := func(day int) time.Time { return time.Date(2025, 1, day, 15, 0, 0, 0, time.Local) }
	trades := []Trade{
		{BuyTime: d(1), SellTime: d(10), BuyCost: 1000}, // 跨两个周末
		{BuyTime: d(5), SellTime: d(20), BuyCost: 2000},
		{BuyTime: d(10), SellTime: d(15), BuyCost: 500}, // 与首笔卖出同日，卖出当日仍持仓
	}

	// 逐日累加作为基准，验证事件扫描口径完全一致
	daily := map[string]float64{}
	for _, tr := range trades {
		for cur := tr.BuyTime; !cur.After(tr.SellTime); cur = cur.AddDate(0, 0, 1) {
			daily[cur.Format(time.DateOnly)] += tr.BuyCost
		}
	}
	want := 0.0
	for _, v := range daily {
		if v > want {
			want = v
		}
	}

	if got := calculateRequiredCapital(trades, DefaultPositionConfig()); math.Abs(got-want) > 1e-9 {
		t.Fatalf("事件扫描所需本金 = %v, 逐日累加 = %v", got, want)
	}
}

func TestAnnualizationPeriodsUsesElapsedSpan(t *testing.T) {
	start := time.Date(2025, 1, 1, 15, 0, 0, 0, time.Local)

	// 40 笔交易分散在约一年内：年化期数应接近交易笔数
	var yearly []Trade
	for i := 0; i < 40; i++ {
		buy := start.AddDate(0, 0, i*9)
		yearly = append(yearly, Trade{BuyTime: buy, SellTime: buy.AddDate(0, 0, 1)})
	}
	if got := annualizationPeriods(yearly); got < 38 || got > 45 {
		t.Fatalf("全年 40 笔的年化期数 = %d, 期望接近 40", got)
	}

	// 相同笔数压缩到两个月内：年化期数应按实际频率显著提高
	var dense []Trade
	for i := 0; i < 40; i++ {
		buy := start.AddDate(0, 0, i)
		dense = append(dense, Trade{BuyTime: buy, SellTime: buy.AddDate(0, 0, 1)})
	}
	if got := annualizationPeriods(dense); got < 200 {
		t.Fatalf("两个月 40 笔的年化期数 = %d, 期望按实际频率放大", got)
	}
}

func TestAnalyzeCalmarUsesDecimalDrawdown(t *testing.T) {
	sell := time.Date(2025, 1, 5, 15, 0, 0, 0, time.Local)
	trades := []Trade{
		{Code: "sh600000", BuyTime: sell.AddDate(0, 0, -4), SellTime: sell,
			BuyPrice: 10, SellPrice: 11, BuyCost: 1000, SellIncome: 1100, Quantity: 100},
		{Code: "sh600001", BuyTime: sell.AddDate(0, 0, -3), SellTime: sell.AddDate(0, 0, -2),
			BuyPrice: 10, SellPrice: 9.5, BuyCost: 1000, SellIncome: 950, Quantity: 100},
	}
	// 峰值并发本金 2000，总盈亏 50，最大回撤 50（比例 0.025），Calmar = 0.025 / 0.025 = 1
	r := Analyze(2025, trades, nil, nil, Cost{}, DefaultPositionConfig())
	if math.Abs(r.Calmar-1) > 1e-9 {
		t.Fatalf("Calmar = %v, 期望 1（回撤应按小数而非百分数参与计算）", r.Calmar)
	}
}

func TestAnalyzeCalmarInfiniteWithoutDrawdown(t *testing.T) {
	buy := time.Date(2025, 1, 1, 15, 0, 0, 0, time.Local)
	trade := Trade{
		Code: "sh600000", BuyTime: buy, SellTime: buy.AddDate(0, 0, 1),
		BuyPrice: 10, SellPrice: 11, BuyCost: 1000, SellIncome: 1100, Quantity: 100,
	}
	r := Analyze(2025, []Trade{trade}, nil, nil, Cost{}, DefaultPositionConfig())
	if !math.IsInf(r.Calmar, 1) {
		t.Fatalf("无回撤且有正收益时 Calmar = %v, 期望 +Inf", r.Calmar)
	}
}

func TestUnderwaterDepthIncludesCapital(t *testing.T) {
	for _, tc := range []struct {
		name          string
		profit        []float64
		capital, want float64
	}{
		{"initial_loss", []float64{0, -100}, 1000, 10},
		{"profit_then_loss", []float64{0, 100, -100}, 1000, 200.0 / 1100 * 100},
		{"all_profit", []float64{0, 100, 200}, 1000, 0},
		{"no_capital", []float64{0, -100}, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dates := make([]time.Time, len(tc.profit))
			_, _, _, got := drawdownMetrics(tc.profit, dates, tc.capital)
			if math.Abs(got-tc.want) > 1e-9 {
				t.Fatalf("underwater=%v want %v", got, tc.want)
			}
		})
	}
}
