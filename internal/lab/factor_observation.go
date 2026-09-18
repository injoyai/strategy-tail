package lab

import (
	"math"
	"time"

	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/internal/researchrun"
	"github.com/injoyai/strategy-tail/lib/extend"
	"github.com/injoyai/strategy-tail/researchdata"
)

// FactorObservation 单个信号日的因子取值观察。
type FactorObservation struct {
	Date       time.Time
	Code       string
	Value      float64
	KlineIndex int
}

// LabeledObservation 在观察之上投影出某个 Horizon 的收益标签。
type LabeledObservation struct {
	FactorObservation
	Horizon int
	Return  float64
}

// observationResult 一票一年的观察与按 Horizon 分组的标签集合。
type observationResult struct {
	Observations []FactorObservation
	Labeled      map[int][]LabeledObservation
	Coverage     map[int]LabelCoverage
}

// buildObservations 拼接 His+Dks 为因子可见序列：因子每天只调用一次，
// 且只看到 series[:base+i+1]（信号日以前，含当日）；Future 缓冲区只参与
// 标签取价，绝不进入因子序列。因子值非有限的交易日不产生观察、不计入覆盖。
func buildObservations(code string, data researchrun.YearData, fct core.ContextFactor, horizons []int, kind string, view researchdata.View) *observationResult {
	series := make(extend.Klines, 0, len(data.His)+len(data.Dks))
	series = append(series, data.His...)
	series = append(series, data.Dks...)
	base := len(data.His)

	res := &observationResult{
		Observations: make([]FactorObservation, 0, len(data.Dks)),
		Labeled:      make(map[int][]LabeledObservation, len(horizons)),
		Coverage:     make(map[int]LabelCoverage, len(horizons)),
	}
	for _, h := range horizons {
		res.Labeled[h] = nil
		res.Coverage[h] = LabelCoverage{}
	}

	for i, k := range data.Dks {
		v := fct.ValueAt(core.FactorContext{
			Code:   code,
			AsOf:   k.Time,
			Klines: series[:base+i+1],
			Data:   view,
		})
		if math.IsNaN(v) || math.IsInf(v, 0) {
			continue
		}
		obs := FactorObservation{Date: k.Time, Code: code, Value: v, KlineIndex: i}
		res.Observations = append(res.Observations, obs)

		for _, h := range horizons {
			ret, reason, ok := labelReturn(data.Dks, data.Future, i, h, kind)
			cov := res.Coverage[h]
			cov.Record(reason)
			res.Coverage[h] = cov
			if ok {
				res.Labeled[h] = append(res.Labeled[h], LabeledObservation{
					FactorObservation: obs,
					Horizon:           h,
					Return:            ret,
				})
			}
		}
	}
	return res
}
