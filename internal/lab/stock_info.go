package lab

import (
	"math"
	"net/http"
	"regexp"
	"time"

	common "github.com/injoyai/strategy-tail"
	"github.com/injoyai/strategy-tail/researchdata"
)

var stockInfoCode = regexp.MustCompile(`^(sh|sz|bj)[0-9]{6}$`)

type stockInfoMetric struct {
	Key         string   `json:"key"`
	Value       *float64 `json:"value"`
	Date        string   `json:"date,omitempty"`
	Source      string   `json:"source,omitempty"`
	AvailableAt string   `json:"availableAt,omitempty"`
	PITStatus   string   `json:"pitStatus,omitempty"`
}

type stockInfoResponse struct {
	Code    string            `json:"code"`
	AsOf    string            `json:"asOf"`
	Metrics []stockInfoMetric `json:"metrics"`
}

// Reads the existing local view only; opening a chart never initializes a feed or syncs data.
func (s *Server) handleStockInfo(w http.ResponseWriter, r *http.Request) {
	code := r.PathValue("code")
	if !stockInfoCode.MatchString(code) {
		writeErr(w, http.StatusBadRequest, "股票代码无效")
		return
	}
	asOf := time.Now()
	if raw := r.URL.Query().Get("asOf"); raw != "" {
		parsed, err := time.Parse(time.RFC3339, raw)
		if err != nil || parsed.After(asOf) {
			writeErr(w, http.StatusBadRequest, "估值截止时间无效或晚于当前时间")
			return
		}
		asOf = parsed
	}
	writeJSON(w, readStockInfo(common.ResearchData, code, asOf))
}

func readStockInfo(view researchdata.View, code string, asOf time.Time) stockInfoResponse {
	out := stockInfoResponse{Code: code, AsOf: asOf.Format(time.RFC3339), Metrics: []stockInfoMetric{}}
	var records []researchdata.Record
	if view != nil {
		for _, dataset := range []string{"tdx.valuation.daily", "valuation.daily"} {
			if record, ok := view.Latest(dataset, code, asOf); ok {
				records = append(records, record)
			}
		}
	}
	loc := time.FixedZone("Asia/Shanghai", 8*60*60)
	for _, key := range []string{"pe_ttm", "pe_static", "dividend_yield", "pb_mrq", "total_market_cap", "float_market_cap"} {
		metric := stockInfoMetric{Key: key}
		var selected time.Time
		for _, record := range records {
			value, exists := record.Values[key]
			if !exists || math.IsNaN(value) || math.IsInf(value, 0) ||
				((key == "pe_ttm" || key == "pe_static") && value == 0) ||
				(key == "dividend_yield" && value < 0) {
				continue
			}
			// Compare the same field across providers, never silently relabel an older snapshot.
			if metric.Value != nil && !record.EventAt.After(selected) {
				continue
			}
			selected = record.EventAt
			metric = stockInfoMetric{Key: key, Value: &value, Date: record.EventAt.In(loc).Format(time.DateOnly),
				Source: record.Source, AvailableAt: record.AvailableAt.Format(time.RFC3339), PITStatus: record.Attributes["pit_status"]}
		}
		out.Metrics = append(out.Metrics, metric)
	}
	return out
}
