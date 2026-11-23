package extend

import (
	"testing"

	"github.com/injoyai/tdx/v2/protocol"
)

func rsiTestKlines(closes ...float64) Klines {
	ks := make(Klines, len(closes))
	for i, c := range closes {
		ks[i] = &Kline{Kline: &protocol.Kline{Close: c}}
	}
	return ks
}

func TestRSI边界(t *testing.T) {
	cases := []struct {
		name   string
		closes []float64
		n      int
		want   int64
	}{
		{"只有上涨返回100", []float64{10, 11, 12}, 2, 100},
		{"上涨后窗口无下跌不残留旧值", []float64{10, 9, 10, 11}, 2, 100},
		{"只有下跌返回0", []float64{12, 11, 10}, 2, 0},
		{"完全横盘返回0", []float64{10, 10, 10}, 2, 0},
		{"涨跌并存保留整数截断", []float64{10, 11, 9}, 2, 33},
		{"数据不足返回0", []float64{10, 11}, 2, 0},
		{"非法周期返回0", []float64{10, 11, 12}, 0, 0},
		{"空序列返回0", nil, 2, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := rsiTestKlines(c.closes...).RSI(c.n); got != c.want {
				t.Fatalf("RSI(%d) = %d, want %d", c.n, got, c.want)
			}
		})
	}
}
