package core

import (
	"github.com/injoyai/strategy-tail/lib/extend"
)

func MA(dks extend.Klines, n int) float64 {
	return dks.MA(n).Float64()
}

func AverageVolume(dks extend.Klines) float64 {
	if len(dks) == 0 {
		return 0
	}
	sum := 0.0
	for _, k := range dks {
		sum += float64(k.Volume)
	}
	return sum / float64(len(dks))
}
