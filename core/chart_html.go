package core

import (
	"github.com/injoyai/strategy-tail/lib/extend"
	"github.com/injoyai/strategy-tail/strategies/util"
)

// reportMACD uses the strategy formula; chart libraries must not reinitialize EMA.
func reportMACD(ks extend.Klines) [][3]float64 {
	dif := util.MACDDIF(ks, 12, 26)
	hist := util.MACDHistogram(ks, 12, 26, 9)
	values := make([][3]float64, len(ks))
	for i := range ks {
		values[i] = [3]float64{dif[i], dif[i] - hist[i], hist[i]}
	}
	return values
}
