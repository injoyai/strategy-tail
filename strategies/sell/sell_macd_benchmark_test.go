package sell

import (
	"testing"

	"github.com/injoyai/strategy-tail/core"
)

var macdBenchmarkResult bool

func BenchmarkMACDReversal(b *testing.B) {
	dks := t3cSeries(t3cCloses(1000, 10000, 9000), 100)
	s := MACD反转{}
	if !t3cMACD反转Oracle(s, dks) || !s.Sell("sh600000", dks, core.Buy{}) {
		b.Fatal("夹具必须触发完整的反转判断")
	}
	b.Run("Legacy", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			macdBenchmarkResult = t3cMACD反转Oracle(s, dks)
		}
	})
	b.Run("Rule", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			macdBenchmarkResult = s.Sell("sh600000", dks, core.Buy{})
		}
	})
}
