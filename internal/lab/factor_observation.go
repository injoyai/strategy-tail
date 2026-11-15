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
	Signals      SignalCoverage
}

// SignalCoverage counts observed stock bars in the selected universe, before labeling.
type SignalCoverage struct {
	Observed int `json:"observed"`
	Valid    int `json:"valid"`
}

// buildObservations 拼接 His+Dks 为因子可见序列：因子每天只调用一次，
// 且只看到 series[:base+i+1]（信号日以前，含当日）；Future 缓冲区只参与
// 标签取价，绝不进入因子序列。因子值非有限的交易日不产生观察、不计入覆盖。
func buildObservations(code string, data researchrun.YearData, fct core.ContextFactor, horizons []int, kind string, view researchdata.View) *observationResult {
	return observeFactor(code, data, fct, horizons, kind, view, nil, nil)
}

func observeFactor(code string, data researchrun.YearData, fct core.ContextFactor, horizons []int, kind string, view researchdata.View,
	calendar *factorSessionCalendar, included func(time.Time) bool, eventDays ...[]string) *observationResult {
	series := make(extend.Klines, 0, len(data.His)+len(data.Dks))
	series = append(series, data.His...)
	series = append(series, data.Dks...)
	base := len(data.His)
	var events []string
	if len(eventDays) != 0 {
		events = eventDays[0]
	}

	res := &observationResult{
		Observations: make([]FactorObservation, 0, len(data.Dks)),
		Labeled:      make(map[int][]LabeledObservation, len(horizons)),
		Coverage:     make(map[int]LabelCoverage, len(horizons)),
	}
	for _, h := range horizons {
		res.Labeled[h] = nil
		res.Coverage[h] = LabelCoverage{}
	}

	var bars map[time.Time]*extend.Kline
	if calendar != nil {
		bars = make(map[time.Time]*extend.Kline, len(data.Dks)+len(data.Future))
		for _, rows := range []extend.Klines{data.Dks, data.Future} {
			for _, bar := range rows {
				bars[core.DayOf(bar.Time)] = bar
			}
		}
	}

	for i, k := range data.Dks {
		if included != nil && !included(k.Time) {
			continue
		}
		res.Signals.Observed++
		v := fct.ValueAt(core.FactorContext{
			Code:   code,
			AsOf:   k.Time,
			Klines: series[:base+i+1],
			Data:   view,
		})
		if math.IsNaN(v) || math.IsInf(v, 0) {
			continue
		}
		res.Signals.Valid++
		obs := FactorObservation{Date: k.Time, Code: code, Value: v, KlineIndex: i}
		res.Observations = append(res.Observations, obs)

		for _, h := range horizons {
			var ret float64
			var reason LabelSkipReason
			var ok bool
			if calendar != nil {
				ret, reason, ok = calendar.labelWithEvents(core.DayOf(k.Time), bars, h, events)
			} else {
				ret, reason, ok = labelReturn(data.Dks, data.Future, i, h, kind)
			}
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
