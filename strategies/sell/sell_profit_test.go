package sell

import (
	"math"
	"testing"

	"github.com/injoyai/strategy-tail/core"
)

func TestProfitNoAllocations(t *testing.T) {
	dks := t3cSeries([]float64{10.0, 11.0}, 100)
	buy := core.Buy{Time: dks[0].Time, Price: 10.0}
	for _, tc := range []struct {
		name string
		pct  float64
		want bool
	}{
		{"default", 0, true}, {"exact", 0.1, true}, {"below", 1, false},
		{"invalid", math.NaN(), false}, {"infinite", math.Inf(1), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got bool
			allocs := testing.AllocsPerRun(1000, func() {
				got = A盈利(tc.pct).Sell("sh600000", dks, buy)
			})
			if got != tc.want || allocs != 0 {
				t.Fatalf("got=%v want=%v allocs=%g, want zero allocations", got, tc.want, allocs)
			}
		})
	}
}

func BenchmarkProfit(b *testing.B) {
	dks := t3cSeries([]float64{10.0, 11.0}, 100)
	buy := core.Buy{Time: dks[0].Time, Price: 10.0}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		macdBenchmarkResult = A盈利(0.1).Sell("sh600000", dks, buy)
	}
}
