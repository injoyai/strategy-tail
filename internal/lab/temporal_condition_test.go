package lab

import (
	"math"
	"testing"
	"time"

	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/lib/extend"
	sb "github.com/injoyai/strategy-tail/strategies/buy"
	ss "github.com/injoyai/strategy-tail/strategies/sell"
	"github.com/injoyai/tdx/v2/protocol"
)

func TestTemporalConditionBoundaries(t *testing.T) {
	bars := func(prices ...float64) extend.Klines {
		ks := make(extend.Klines, len(prices))
		for i, price := range prices {
			ks[i] = &extend.Kline{Kline: &protocol.Kline{Time: time.Date(2025, 1, i+1, 0, 0, 0, 0, time.Local), Close: price}}
		}
		return ks
	}
	t.Run("position equality and warmup", func(t *testing.T) {
		above := sb.A站上N日均线{Period: 2, Days: 2}
		below := ss.A收盘跌破均线{Period: 2, Days: 2}
		for _, tt := range []struct {
			prices    []float64
			buy, sell bool
		}{
			{[]float64{1, 2, 3}, false, false}, // 原条件额外要求 Period+Days 根。
			{[]float64{3, 2, 1}, false, false},
			{[]float64{1, 2, 3, 4}, true, false},
			{[]float64{4, 3, 2, 1}, false, true},
			{[]float64{2, 2, 2, 2}, false, true},
			{[]float64{1, 2, 3, 1}, false, false},
		} {
			ks := bars(tt.prices...)
			if got := above.Buy("", ks); got != tt.buy {
				t.Errorf("above %v = %v, want %v", tt.prices, got, tt.buy)
			}
			if got := below.Sell("", ks, core.Buy{}); got != tt.sell {
				t.Errorf("below %v = %v, want %v", tt.prices, got, tt.sell)
			}
		}
	})
	t.Run("weakest slope threshold", func(t *testing.T) {
		ks := bars(1, 2, 3, 4, 5, 6, 7)
		for _, tt := range []struct {
			threshold float64
			want      bool
		}{
			{0, true}, {0.2, true}, {math.Nextafter(0.2, 1), false}, {-0.2, true}, {math.NaN(), true},
		} {
			buyer := sb.MAUp{Period: 3, Lookback: 2, MinSlope: tt.threshold}
			if got := buyer.Buy("", ks); got != tt.want {
				t.Errorf("threshold %v = %v, want %v", tt.threshold, got, tt.want)
			}
		}
		if (sb.MAUp{Period: 3, Lookback: 2, MinSlope: -1}).Buy("", bars(2, 2, 2, 2, 2)) {
			t.Fatal("negative threshold must not admit a flat MA")
		}
	})
	t.Run("buy requires positive base but sell permits zero", func(t *testing.T) {
		prices := make([]float64, 251)
		prices[250] = 3
		ks := bars(prices...)
		if (sb.MAUp{Period: 250, Lookback: 1}).Buy("", ks) {
			t.Fatal("buy slope requires positive prior MA")
		}
		if (ss.LongMADown{Lookback: 1}).Sell("", ks, core.Buy{}) {
			t.Fatal("rising from zero is not a long MA downturn")
		}
	})
}
