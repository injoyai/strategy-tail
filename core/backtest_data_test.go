package core

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/injoyai/strategy-tail/lib/extend"
	"github.com/injoyai/tdx/v2/protocol"
)

// 本文件为 package core 内部测试（_backtest / scanYear 未导出），
// 不 import strategies/sell（会形成 core→sell→core 测试依赖环），
// Buyer/Seller 使用本地 stub。

type dataBuyer struct{}

func (dataBuyer) Name() string                   { return "always" }
func (dataBuyer) Buy(string, extend.Klines) bool { return true }

type dataSeller struct{}

func (dataSeller) Name() string                         { return "hold" }
func (dataSeller) Sell(string, extend.Klines, Buy) bool { return false }

// dataBars 生成 [start, start+n) 的恒价日 K。
func dataBars(start time.Time, n int) extend.Klines {
	ks := make(extend.Klines, 0, n)
	for i := 0; i < n; i++ {
		at := start.AddDate(0, 0, i)
		ks = append(ks, &extend.Kline{Kline: &protocol.Kline{
			Time: at, Open: 10, High: 10,
			Low: 10, Close: 10, Volume: 100,
		}})
	}
	return ks
}

// TestBacktestOnlyHistoryNoTargetYear 所有数据早于目标年份时切分不会发生
// （cut 兜底置空 dks）：历史段不得被当作当年区间参与回测，结果应为空。
func TestBacktestOnlyHistoryNoTargetYear(t *testing.T) {
	dks := dataBars(time.Date(2023, 6, 1, 0, 0, 0, 0, time.Local), 20)
	bt := Backtest{
		Buyer:        dataBuyer{},
		Seller:       dataSeller{},
		Cost:         Cost{Slippage: 0},
		Position:     PositionConfig{SharesPerLot: 100},
		GetDayKlines: func(string, time.Time, time.Time) (extend.Klines, error) { return dks, nil },
		Goroutines:   1,
	}
	trades, err := bt._backtest([]string{"000001"}, 2024)
	if err != nil {
		t.Fatalf("仅历史数据不应报错: %v", err)
	}
	if len(trades) != 0 {
		t.Fatalf("仅历史数据不得产生 2024 年交易，实际 %d 笔", len(trades))
	}
}

// TestBacktestNilDataProvider 空数据源在入口统一拦截：返回明确错误，
// 不再依赖协程内调用 nil 函数 panic。
func TestBacktestNilDataProvider(t *testing.T) {
	bt := Backtest{Buyer: dataBuyer{}, Seller: dataSeller{}, Goroutines: 1}
	if _, err := bt._backtest([]string{"000001"}, 2024); err == nil || !strings.Contains(err.Error(), "未设置 GetDayKlines") {
		t.Fatalf("应报未设置数据源错误: %v", err)
	}
}

// TestBacktestProviderErrorSkipsCode 行情读取失败逐票跳过（记录后继续），
// nil 切片不算错误、不产生交易。
func TestBacktestProviderErrorSkipsCode(t *testing.T) {
	providerErr := errors.New("missing db")
	bt := Backtest{
		Buyer:    dataBuyer{},
		Seller:   dataSeller{},
		Cost:     Cost{Slippage: 0},
		Position: PositionConfig{SharesPerLot: 100},
		GetDayKlines: func(code string, _, _ time.Time) (extend.Klines, error) {
			if code == "bad" {
				return nil, providerErr
			}
			return nil, nil
		},
		Goroutines: 2,
	}
	trades, err := bt._backtest([]string{"bad", "good"}, 2024)
	if err != nil {
		t.Fatalf("单票读取失败不应使整年失败: %v", err)
	}
	if len(trades) != 0 {
		t.Fatalf("无数据不应产生交易: %d", len(trades))
	}
}

// TestScanYearOnlyHistoryNoTargetYear forward_return.scanYear 同一截断语义：
// 仅历史数据不得产生当年买入信号。
func TestScanYearOnlyHistoryNoTargetYear(t *testing.T) {
	dks := dataBars(time.Date(2023, 6, 1, 0, 0, 0, 0, time.Local), 20)
	fa := ForwardReturnAnalysis{
		Codes:        []string{"000001"},
		Buyer:        dataBuyer{},
		GetDayKlines: func(string, time.Time, time.Time) (extend.Klines, error) { return dks, nil },
		Goroutines:   1,
	}
	if frs := fa.scanYear(2024); len(frs) != 0 {
		t.Fatalf("仅历史数据不得产生 2024 年信号，实际 %d 条", len(frs))
	}
}
