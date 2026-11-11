package extend

import (
	"math"
	"testing"

	"github.com/injoyai/tdx/v2/protocol"
)

func TestRisePercentKeepsStrategyThresholdUnits(t *testing.T) {
	for _, tc := range []struct {
		bar  protocol.Kline
		want float64
		ok   bool
	}{
		{protocol.Kline{Last: 10, Open: 12, Close: 10.5}, 5, true},
		{protocol.Kline{Open: 10, Close: 10.5}, 5, true},
		{protocol.Kline{Open: 10, Close: 10}, 0, true},
		{protocol.Kline{Close: 10}, 0, false},
	} {
		k := &Kline{Kline: &tc.bar}
		got, ok := k.RisePercent()
		if ok != tc.ok || math.Abs(got-tc.want) > 1e-9 {
			t.Fatalf("got %v %v, want %v %v", got, ok, tc.want, tc.ok)
		}
	}
}
