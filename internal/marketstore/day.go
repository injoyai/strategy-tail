package marketstore

import "github.com/injoyai/tdx/v2/extend/model"

// DayRecord carries the legacy strategy columns separately from the SDK's raw bar.
type DayRecord struct {
	model.KlineDay
	Turnover   float64
	FloatStock float64
	TotalStock float64
}
