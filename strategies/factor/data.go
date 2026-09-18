package factor

import (
	"fmt"
	"math"
	"time"

	"github.com/injoyai/strategy-tail/core"
)

// 最新字段 reads the newest point-in-time-visible numeric field. It can back
// valuation, market-cap, financial statement and normalized alternative-data
// factors without coupling factor code to a vendor schema.
type 最新字段 struct {
	Dataset string
	Field   string
	Label   string
}

func (f 最新字段) Name() string {
	if f.Label != "" {
		return f.Label
	}
	return fmt.Sprintf("最新字段(%s.%s)", f.Dataset, f.Field)
}

func (f 最新字段) ValueAt(input core.FactorContext) float64 {
	if input.Data == nil || input.AsOf.IsZero() || f.Dataset == "" || f.Field == "" {
		return math.NaN()
	}
	record, ok := input.Data.Latest(f.Dataset, input.Code, input.AsOf)
	if !ok {
		return math.NaN()
	}
	value, ok := record.Values[f.Field]
	if !ok || math.IsNaN(value) || math.IsInf(value, 0) {
		return math.NaN()
	}
	return value
}

// 字段变化率 compares the newest value with the Nth prior business period.
// Periods=1 means adjacent available periods. It works for quarterly financial
// statements, daily fundamentals and other ordered point-in-time series.
type 字段变化率 struct {
	Dataset string
	Field   string
	Periods int
	Label   string
}

func (f 字段变化率) Name() string {
	if f.Label != "" {
		return f.Label
	}
	return fmt.Sprintf("字段变化率(%s.%s,%d)", f.Dataset, f.Field, f.periods())
}

func (f 字段变化率) ValueAt(input core.FactorContext) float64 {
	if input.Data == nil || input.AsOf.IsZero() || f.Dataset == "" || f.Field == "" {
		return math.NaN()
	}
	records := input.Data.Range(f.Dataset, input.Code, time.Time{}, input.AsOf, input.AsOf)
	n := f.periods()
	if len(records) <= n {
		return math.NaN()
	}
	latest, okLatest := records[len(records)-1].Values[f.Field]
	previous, okPrevious := records[len(records)-1-n].Values[f.Field]
	if !okLatest || !okPrevious || previous == 0 || math.IsNaN(latest) || math.IsNaN(previous) ||
		math.IsInf(latest, 0) || math.IsInf(previous, 0) {
		return math.NaN()
	}
	return latest/previous - 1
}

func (f 字段变化率) periods() int {
	if f.Periods <= 0 {
		return 1
	}
	return f.Periods
}

// 事件计数 counts visible, de-duplicated events in the previous N calendar
// days. Attribute/Equals optionally filters event categories, for example
// announcement_type=earnings_forecast.
type 事件计数 struct {
	Dataset   string
	Days      int
	Attribute string
	Equals    string
	Label     string
}

func (f 事件计数) Name() string {
	if f.Label != "" {
		return f.Label
	}
	return fmt.Sprintf("事件计数(%s,%d日)", f.Dataset, f.days())
}

func (f 事件计数) ValueAt(input core.FactorContext) float64 {
	if input.Data == nil || input.AsOf.IsZero() || f.Dataset == "" {
		return math.NaN()
	}
	start := input.AsOf.AddDate(0, 0, -f.days()+1)
	records := input.Data.Range(f.Dataset, input.Code, start, input.AsOf, input.AsOf)
	if f.Attribute == "" {
		return float64(len(records))
	}
	count := 0
	for _, record := range records {
		if record.Attributes[f.Attribute] == f.Equals {
			count++
		}
	}
	return float64(count)
}

func (f 事件计数) days() int {
	if f.Days <= 0 {
		return 30
	}
	return f.Days
}
