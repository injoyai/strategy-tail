package core_test

import (
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/lib/extend"
	"github.com/injoyai/strategy-tail/strategies/sell"
	"github.com/injoyai/tdx/v2/protocol"
)

func TestDoMultiPositionTPlusOne(t *testing.T) {
	for _, hour := range []int{0, 15} {
		for _, days := range []int{2, 3} {
			t.Run(fmt.Sprintf("hour=%d/days=%d", hour, days), func(t *testing.T) {
				start := time.Date(2025, 1, 6, hour, 0, 0, 0, time.Local)
				bars := extend.Klines{
					testKline(start, 10.2, 10.2, 10.2, 10.2),
					testKline(start.AddDate(0, 0, 1), 10, 10, 10, 10),
				}
				minutes := protocol.Klines{{
					Time: core.DayOf(bars[1].Time).Add(9*time.Hour + 30*time.Minute),
					Open: 10, High: 10.02,
					Low: 10, Close: 10.02, Volume: 1000,
				}}
				if days == 3 {
					bars = append(bars, testKline(start.AddDate(0, 0, 2), 10.3, 10.3, 10.3, 10.3))
				}
				bt := core.Backtest{
					Buyer: alwaysBuyer{}, Seller: sell.A盈利(0.001),
					Position: core.PositionConfig{MaxPerCode: 2, SharesPerLot: 100},
				}
				trades := bt.Do("test", nil, bars, minutes)
				if len(trades) != 2 {
					t.Fatalf("want two positions, got %d trades", len(trades))
				}
				for _, tr := range trades {
					if days == 2 && !tr.Virtual {
						t.Fatalf("position must remain open on day 2: %+v", tr)
					}
					if days == 3 && (tr.Virtual || !tr.SellTime.Equal(bars[2].Time)) {
						t.Fatalf("eligible position must sell on day 3: %+v", tr)
					}
					if !tr.Virtual && !core.DayOf(tr.SellTime).After(core.DayOf(tr.BuyTime)) {
						t.Fatalf("T+1 violated: %+v", tr)
					}
				}
			})
		}
	}
}

func TestDoKeepsInputUnchanged(t *testing.T) {
	start := time.Date(2025, 1, 6, 15, 0, 0, 0, time.Local)
	his := extend.Klines{testKline(start.AddDate(0, 0, -1), 9, 9, 9, 9)}
	bars := extend.Klines{
		testKline(start, 10, 10, 10, 10),
		testKline(start.AddDate(0, 0, 1), 11, 11, 11, 11),
	}
	minutes := protocol.Klines{{
		Time: core.DayOf(bars[1].Time).Add(9*time.Hour + 30*time.Minute),
		Open: 10.5, High: 10.5,
		Low: 10.5, Close: 10.5, Volume: 1000,
	}}
	wantHis, wantBars, wantMinutes := cloneKlines(his), cloneKlines(bars), cloneMinKlines(minutes)
	bt := core.Backtest{
		Buyer: alwaysBuyer{}, Seller: alwaysSeller{},
		Position: core.PositionConfig{MaxPerCode: 2, SharesPerLot: 100},
	}
	first := bt.Do("test", his, bars, minutes)
	if !reflect.DeepEqual(his, wantHis) || !reflect.DeepEqual(bars, wantBars) || !reflect.DeepEqual(minutes, wantMinutes) {
		t.Fatal("backtest modified input market data")
	}
	if second := bt.Do("test", his, bars, minutes); !reflect.DeepEqual(first, second) {
		t.Fatalf("reusing input changed trades: first=%+v second=%+v", first, second)
	}
}

func TestDoVirtualUsesFinalDailyClose(t *testing.T) {
	cases := []struct {
		name         string
		minuteCloses []float64
		maxPerCode   int
		profit       float64
		realSales    int
	}{
		{"缺分钟", nil, 1, 1, 0},
		{"分钟均无效", []float64{0}, 1, 1, 0},
		{"未平仓的最后分钟早于收盘", []float64{10}, 1, 1, 0},
		{"旧仓提前卖出且新仓当日买入", []float64{11, 12}, 2, 0.05, 1},
	}
	for _, tc := range cases {
		for _, fees := range []struct {
			name string
			cost core.Cost
		}{{"零费用", core.Cost{}}, {"默认费用", core.DefaultCost()}} {
			t.Run(tc.name+"/"+fees.name, func(t *testing.T) {
				start := time.Date(2025, 1, 6, 15, 0, 0, 0, time.Local)
				bars := extend.Klines{
					testKline(start, 10, 10, 10, 10),
					testKline(start.AddDate(0, 0, 1), 10, 12, 10, 12),
				}
				wantPrice, wantTime := bars[1].Close, bars[1].Time
				var minutes protocol.Klines
				for i, price := range tc.minuteCloses {
					at := core.DayOf(wantTime).Add(9*time.Hour + 31*time.Minute)
					if i > 0 {
						at = wantTime
					}
					p := price
					minutes = append(minutes, &protocol.Kline{Time: at, Open: p, High: p, Low: p, Close: p})
				}
				bt := core.Backtest{
					Buyer: alwaysBuyer{}, Seller: sell.A盈利(tc.profit), Cost: fees.cost,
					Position: core.PositionConfig{MaxPerCode: tc.maxPerCode, SharesPerLot: 100},
				}
				trades := bt.Do("test", nil, bars, minutes)
				if len(trades) != tc.realSales+1 {
					t.Fatalf("want %d real sales and one virtual trade, got %+v", tc.realSales, trades)
				}
				virtuals := 0
				for _, tr := range trades {
					if !tr.Virtual {
						if !tr.SellTime.Equal(minutes[0].Time) || tr.SellExecPrice != minutes[0].Close-fees.cost.Slippage {
							t.Fatalf("real sale must retain its minute execution: %+v", tr)
						}
						continue
					}
					virtuals++
					wantExec, wantIncome := fees.cost.SellIncome(wantPrice, 100)
					if !tr.SellTime.Equal(wantTime) || tr.SellTime.Before(tr.BuyTime) || tr.SellExecPrice != wantExec || tr.SellIncome != wantIncome {
						t.Fatalf("virtual must use daily close/time and existing costs: got %+v, want time=%s exec=%v income=%g", tr, wantTime, wantExec, wantIncome)
					}
				}
				if virtuals != 1 {
					t.Fatalf("want one virtual trade, got %d", virtuals)
				}
			})
		}
	}
}
