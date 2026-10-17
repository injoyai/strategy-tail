package factorstats

import "math"

func nearlyEq(a, b float64) bool { return math.Abs(a-b) < 1e-9 }
func f64p(v float64) *float64    { return &v }
