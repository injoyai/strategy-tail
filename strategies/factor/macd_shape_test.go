package factor

import (
	"math"
	"testing"
	"time"

	"github.com/injoyai/strategy-tail/core"
)

func TestSeriesPosition(t *testing.T) {
	for _, tt := range []struct {
		values []float64
		want   float64
	}{
		{[]float64{-3, -1, -2}, 0.5}, {[]float64{-3, -1, -3}, 0},
		{[]float64{-3, -1, -1}, 1}, {[]float64{2, 2, 2}, 0}, {[]float64{2}, 0},
	} {
		wantVal(t, "range position", SeriesPosition(tt.values), tt.want, 0)
	}
	wantNaN(t, "empty range", SeriesPosition(nil))
	wantNaN(t, "missing range", SeriesPosition([]float64{1, math.NaN(), 2}))
}

func TestNegativeShorteningStreak(t *testing.T) {
	for _, tt := range []struct {
		values []float64
		want   int
	}{
		{nil, 0}, {[]float64{-1}, 0}, {[]float64{-4, -3, -2}, 2},
		{[]float64{-3, -2, 0}, 2}, {[]float64{-3, -2, 1}, 2},
		{[]float64{-3, 1, 2}, 0}, {[]float64{-3, -2, -2}, 0},
		{[]float64{0, -2, -1}, 1}, {[]float64{-2, math.NaN()}, 0},
	} {
		if got := NegativeShorteningStreak(tt.values); got != tt.want {
			t.Errorf("%v = %d, want %d", tt.values, got, tt.want)
		}
	}
}

func TestSameSignMaxReversals(t *testing.T) {
	for _, tt := range []struct {
		values     []float64
		days, want int
	}{
		{nil, 5, 0}, {[]float64{1, 2, 1, 2, 1}, 4, 3},
		{[]float64{1, 2, 1, 2, 1}, 2, 1}, {[]float64{1, 2, 2, 1}, 3, 1},
		{[]float64{1, 2, 1, -1, -2, -1, -2}, 6, 2},
		{[]float64{2, 1, 0, 1, 2}, 4, 1}, // 接触零轴不重置上一非零符号。
		{[]float64{-2, -1, 0, 1, 0, -1}, 5, 1},
		{[]float64{1, 1, 1}, 2, 0}, {[]float64{1, -1, 1, -1}, 3, 0},
	} {
		if got := SameSignMaxReversals(tt.values, tt.days); got != tt.want {
			t.Errorf("%v / %d = %d, want %d", tt.values, tt.days, got, tt.want)
		}
	}
}

func TestMaxSameSignChangeRatio(t *testing.T) {
	for _, tt := range []struct {
		values []float64
		days   int
		want   float64
	}{
		{[]float64{1, 2, 1, -20, -40, 0, 4}, 6, 1},
		{[]float64{1, 4, 0, 2}, 3, 3},
		{[]float64{-2, -3, -1}, 2, 2.0 / 3}, {[]float64{1, 1, 1}, 2, 0},
	} {
		wantVal(t, "same sign max change", MaxSameSignChangeRatio(tt.values, tt.days), tt.want, 0)
	}
	for _, values := range [][]float64{nil, {1, 4, 0, 2}} {
		if got := MaxSameSignChangeRatio(values, 1); !math.IsInf(got, -1) {
			t.Fatalf("empty comparisons = %v, want -Inf", got)
		}
	}
}

func TestMACDShapeCatalogValues(t *testing.T) {
	prices := make([]float64, 100)
	for i := range prices {
		prices[i] = 20 + math.Sin(float64(i)*0.4)*3 + float64(i%7)/10
	}
	ks := closes(time.Time{}, prices...)
	for _, tt := range []struct {
		kind   string
		days   int
		f      core.Factor
		series func(int) float64
	}{
		{"macd_negative_shrinking_streak", 1, MACD负柱缩短天数{}, func(end int) float64 { return float64(NegativeShorteningStreak((MACD柱值{}).Series(ks[:end]))) }},
		{"macd_same_sign_reversals", 4, MACD同号段最大反转次数{Days: 4}, func(end int) float64 { return float64(SameSignMaxReversals((MACD平滑柱值{}).Series(ks[:end]), 4)) }},
		{"macd_same_sign_max_change", 4, MACD同号最大变化比{Days: 4}, func(end int) float64 { return math.Max(0, MaxSameSignChangeRatio((MACD柱值{}).Series(ks[:end]), 4)) }},
	} {
		t.Run(tt.kind, func(t *testing.T) {
			for end := 0; end <= len(ks); end++ {
				got := tt.f.Value("", ks[:end])
				if end < macdMinSamples {
					wantNaN(t, "warmup", got)
					continue
				}
				wantVal(t, "prefix formula", got, tt.series(end), 0)
				wantVal(t, "catalog formula", Build(tt.kind, tt.days).Value("", ks[:end]), got, 0)
				wantVal(t, "research formula", BuildContext(tt.kind, tt.days).ValueAt(core.FactorContext{Klines: ks[:end]}), got, 0)
			}
		})
	}
	wantNaN(t, "long raw window", (MACD同号最大变化比{Days: 100}).Value("", ks))
	wantNaN(t, "long smoothed window", (MACD同号段最大反转次数{Days: 100}).Value("", ks))
	maxDays := int(^uint(0) >> 1)
	wantNaN(t, "oversized raw window", (MACD同号最大变化比{Days: maxDays}).Value("", ks))
	wantNaN(t, "oversized smoothed window", (MACD同号段最大反转次数{Days: maxDays}).Value("", ks))
	flat := closes(time.Time{}, make([]float64, 40)...)
	wantVal(t, "real zero shrinking", (MACD负柱缩短天数{}).Value("", flat), 0, 0)
	wantVal(t, "real zero reversals", (MACD同号段最大反转次数{}).Value("", flat), 0, 0)
	wantVal(t, "real zero change", (MACD同号最大变化比{}).Value("", flat), 0, 0)
}
