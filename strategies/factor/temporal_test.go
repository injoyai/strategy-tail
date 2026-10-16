package factor

import (
	"math"
	"testing"
	"time"
)

func TestSeriesStreaks(t *testing.T) {
	for _, tt := range []struct {
		name                                string
		values                              []float64
		negative, positive, rising, falling int
	}{
		{"empty", nil, 0, 0, 0, 0},
		{"single", []float64{-1}, 1, 0, 0, 0},
		{"zero breaks sign", []float64{-2, -1, 0}, 0, 0, 2, 0},
		{"equal breaks direction", []float64{1, 2, 2}, 0, 3, 0, 0},
		{"negative rising", []float64{0, -3, -2, -1}, 3, 0, 2, 0},
		{"cross zero falling", []float64{2, 1, 0, -1}, 1, 0, 0, 3},
		{"missing breaks streak", []float64{math.NaN(), 1, 2}, 0, 2, 1, 0},
		{"missing today", []float64{1, math.NaN()}, 0, 0, 0, 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := [4]int{NegativeStreak(tt.values), PositiveStreak(tt.values), RisingStreak(tt.values), FallingStreak(tt.values)}
			want := [4]int{tt.negative, tt.positive, tt.rising, tt.falling}
			if got != want {
				t.Fatalf("streaks = %v, want %v", got, want)
			}
		})
	}
}

func TestMATemporalFactors(t *testing.T) {
	ks := closes(time.Time{}, 1, 2, 3, 4, 5, 6, 7)
	slope := MA最弱日斜率{Period: 3, Lookback: 2}
	delta := MA最弱日增量{Period: 3, Lookback: 2}
	wantVal(t, slope.Name(), slope.Value("", ks), 1.0/5, 0)
	wantVal(t, delta.Name(), delta.Value("", ks), 1, 0)
	wantNaN(t, "relative warmup", slope.Value("", ks[:4]))
	wantNaN(t, "absolute warmup", delta.Value("", ks[:4]))
	zeroBase := closes(time.Time{}, 0, 0, 3)
	wantNaN(t, "relative requires positive base", (MA最弱日斜率{Period: 2, Lookback: 1}).Value("", zeroBase))
	wantVal(t, "absolute permits zero base", (MA最弱日增量{Period: 2, Lookback: 1}).Value("", zeroBase), 1.5, 0)
	negativeBase := closes(time.Time{}, -3, -2, -1)
	wantNaN(t, "relative rejects negative base", (MA最弱日斜率{Period: 2, Lookback: 1}).Value("", negativeBase))
	wantVal(t, "absolute permits negative base", (MA最弱日增量{Period: 2, Lookback: 1}).Value("", negativeBase), 1, 0)
	flat := closes(time.Time{}, 2, 2, 2, 2, 2)
	wantVal(t, "flat slope", slope.Value("", flat), 0, 0)
	wantVal(t, "flat delta", delta.Value("", flat), 0, 0)
	accelerating := closes(time.Time{}, 1, 2, 3, 4, 5, 10)
	wantVal(t, "weakest is not necessarily latest", (MA最弱日斜率{Period: 2, Lookback: 2}).Value("", accelerating), 1.0/3.5, 0)
	wantVal(t, "weakest absolute step", (MA最弱日增量{Period: 2, Lookback: 2}).Value("", accelerating), 1, 0)
	for _, f := range []MA最弱日斜率{{Period: -1, Lookback: 2}, {Period: 2, Lookback: -1}} {
		wantNaN(t, "invalid slope window", f.Value("", ks))
	}
	if slope.Name() == (MA最弱日斜率{Period: 3, Lookback: 3}).Name() || delta.Name() == slope.Name() {
		t.Fatal("temporal names must distinguish formulas and windows")
	}
}

func TestMAPositionStreaks(t *testing.T) {
	up := MA上方连续天数{Period: 2, Days: 3}
	down := MA下方连续天数{Period: 2, Days: 3}
	ks := closes(time.Time{}, 1, 2, 3, 4, 5)
	wantVal(t, "capped above", up.Value("", ks), 3, 0)
	wantVal(t, "not below", down.Value("", ks), 0, 0)
	wantVal(t, "available prefix", up.Value("", ks[:2]), 1, 0)
	wantNaN(t, "missing MA", up.Value("", ks[:1]))
	flat := closes(time.Time{}, 2, 2, 2, 2, 2)
	wantVal(t, "above is strict", up.Value("", flat), 0, 0)
	wantVal(t, "below includes equality", down.Value("", flat), 3, 0)
	falling := closes(time.Time{}, 5, 4, 3, 2, 1)
	wantVal(t, "below falling", down.Value("", falling), 3, 0)
	wantVal(t, "break at latest", up.Value("", closes(time.Time{}, 1, 2, 3, 4, 1)), 0, 0)
	wantVal(t, "only latest run", up.Value("", closes(time.Time{}, 5, 4, 3, 4, 5)), 2, 0)
}
