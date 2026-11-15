package lab

import (
	"math"
	"strings"
	"time"

	"github.com/injoyai/strategy-tail/lib/extend"
	"github.com/injoyai/tdx/v2/protocol"
)

func validExecutionPrice(price float64) bool {
	return price > 0 && price <= math.MaxFloat64
}

func corporateEventDates(events protocol.XRXDs) []string {
	dates := make([]string, 0, len(events))
	for _, event := range events {
		dates = append(dates, event.Time.Format(time.DateOnly))
	}
	return dates
}

func priorStockClose(ks extend.Klines) float64 {
	if last := ks[len(ks)-1].Last; validExecutionPrice(last) {
		return last
	}
	if len(ks) > 1 {
		return ks[len(ks)-2].Close
	}
	return 0
}

// atNominalStockLimit detects a candidate execution price at the normal board
// limit. The daily source has no historical limit prices or security status.
// In particular, this does not infer special ST, IPO or ex-rights limits.
func atNominalStockLimit(code string, day time.Time, previous, price float64, buy bool) bool {
	if !validExecutionPrice(previous) || !validExecutionPrice(price) {
		return false
	}
	rate := 0.10
	switch {
	case strings.HasPrefix(code, "sh68"):
		rate = 0.20
	case strings.HasPrefix(code, "sz30") && day.Format(time.DateOnly) >= "2020-08-24":
		rate = 0.20
	case strings.HasPrefix(code, "sh60"), strings.HasPrefix(code, "sz00"), strings.HasPrefix(code, "sz30"):
	default:
		return false
	}
	if !buy {
		rate = -rate
	}
	// China A-share prices use a CNY 0.01 tick. Feed prices are floating point.
	limit := math.Round(previous*(1+rate)*100 + 1e-9)
	if limit == math.Round(previous*100) {
		limit += math.Copysign(1, rate)
	}
	return math.Abs(price*100-limit) < 1e-6
}

func canFillStockBar(code string, bar *protocol.Kline, previous, price float64, buy bool) bool {
	return bar.Volume > 0 && validExecutionPrice(price) && !atNominalStockLimit(code, bar.Time, previous, price, buy)
}
