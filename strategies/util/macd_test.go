package util

import (
	"fmt"
	"math"
	"testing"

	"github.com/injoyai/strategy-tail/lib/extend"
	"github.com/injoyai/tdx/v2/protocol"
)

func macdTestKlines(n int) extend.Klines {
	ks := make(extend.Klines, n)
	for i := range ks {
		ks[i] = &extend.Kline{Kline: &protocol.Kline{Close: float64(10000+i*13+(i%7)*97) / 1000.0}}
	}
	return ks
}

// 保留原版数组算法，逐点验证浮点运算与预热前缀的结果不变。
func referenceMACD(ks extend.Klines, fast, slow, signal int) ([]float64, []float64) {
	closes := make([]float64, len(ks))
	for i := range ks {
		closes[i] = ks[i].Close
	}
	a, b := emaSeries(closes, fast), emaSeries(closes, slow)
	dif := make([]float64, len(ks))
	for i := range dif {
		dif[i] = a[i] - b[i]
	}
	dea := emaSeries(dif, signal)
	hist := make([]float64, len(ks))
	for i := range hist {
		hist[i] = dif[i] - dea[i]
	}
	return dif, hist
}

func TestMACDMatchesOriginalForEveryPrefix(t *testing.T) {
	ks := macdTestKlines(120)
	for _, params := range [][3]int{{12, 26, 9}, {5, 13, 3}, {1, 26, 1}, {0, -1, 0}, {26, 1, 2}} {
		for n := 0; n <= len(ks); n++ {
			dif, hist := referenceMACD(ks[:n], params[0], params[1], params[2])
			for i, got := range [][]float64{MACDDIF(ks[:n], params[0], params[1]), MACDHistogram(ks[:n], params[0], params[1], params[2])} {
				want := [][]float64{dif, hist}[i]
				if len(got) != len(want) {
					t.Fatal("序列长度改变")
				}
				for j := range got {
					if math.Float64bits(got[j]) != math.Float64bits(want[j]) {
						t.Fatalf("params=%v prefix=%d series=%d index=%d: got %.18g want %.18g", params, n, i, j, got[j], want[j])
					}
				}
			}
		}
	}
	if MACDHistogram(nil, 12, 26, 9) != nil || MACDDIF(nil, 12, 26) != nil {
		t.Fatal("空输入应返回 nil")
	}
}

var benchmarkMACDResult []float64

func BenchmarkMACDPrefixes(b *testing.B) {
	for _, n := range []int{250, 1000} {
		ks := macdTestKlines(n)
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			b.ReportAllocs()
			for j := 0; j < b.N; j++ {
				for i := 35; i <= n; i++ {
					benchmarkMACDResult = MACDHistogram(ks[:i], 12, 26, 9)
				}
			}
		})
	}
}
