package core

import "github.com/injoyai/tdx/extend"

func MA(dks extend.Klines, n int) float64 {
	if len(dks) < n {
		return 0
	}
	sum := 0.0
	for _, k := range dks[len(dks)-n:] {
		sum += k.Close.Float64()
	}
	return sum / float64(n)
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
