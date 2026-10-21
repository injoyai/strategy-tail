package lab

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	common "github.com/injoyai/strategy-tail"
	"github.com/injoyai/strategy-tail/researchdata"
)

func TestStockInfoPointInTimeAndUnits(t *testing.T) {
	store := researchdata.NewStore()
	for _, spec := range []researchdata.DatasetSpec{
		{ID: "tdx.valuation.daily", Source: "tdx", Kind: researchdata.KindFundamental, Version: "1"},
		{ID: "valuation.daily", Source: "eastmoney", Kind: researchdata.KindFundamental, Version: "1"},
	} {
		if err := store.RegisterDataset(spec); err != nil {
			t.Fatal(err)
		}
	}
	at := func(s string) time.Time {
		v, err := time.Parse(time.RFC3339, s)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	rows := []researchdata.Record{
		{Dataset: "tdx.valuation.daily", Source: "tdx", Code: "sz000100", Key: "old", EventAt: at("2026-10-16T15:00:00+08:00"), AvailableAt: at("2026-10-16T16:00:00+08:00"), Values: map[string]float64{"pe_ttm": -12, "pe_static": 0, "dividend_yield": 0}},
		{Dataset: "tdx.valuation.daily", Source: "tdx", Code: "sz000100", Key: "new", EventAt: at("2026-10-19T15:00:00+08:00"), AvailableAt: at("2026-10-19T16:00:00+08:00"), Values: map[string]float64{"pe_ttm": 15.3, "dividend_yield": 1.9}},
		{Dataset: "valuation.daily", Source: "eastmoney", Code: "sz000100", Key: "em", EventAt: at("2026-10-15T15:00:00+08:00"), AvailableAt: at("2026-10-15T15:00:00+08:00"), Values: map[string]float64{"pe_ttm": 20, "pb_mrq": 2.5, "total_market_cap": 12000000000}, Attributes: map[string]string{"pit_status": "unverified_revision_history"}},
	}
	if err := store.Ingest(rows); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		cutoff       string
		pe, dividend float64
		date         string
	}{
		{"2026-10-19T15:00:00+08:00", -12, 0, "2026-10-16"},
		{"2026-10-19T17:00:00+08:00", 15.3, 1.9, "2026-10-19"},
	} {
		got := readStockInfo(store, "sz000100", at(tc.cutoff))
		if len(got.Metrics) != 6 {
			t.Fatal(got)
		}
		if *got.Metrics[0].Value != tc.pe || got.Metrics[0].Date != tc.date || got.Metrics[0].Source != "tdx" {
			t.Fatal(got.Metrics[0])
		}
		if *got.Metrics[2].Value != tc.dividend {
			t.Fatal("股息率不得再次乘100或把0当缺失", got.Metrics[2])
		}
		if got.Metrics[1].Value != nil || got.Metrics[5].Value != nil {
			t.Fatal("缺失值不得变成0", got)
		}
		if *got.Metrics[3].Value != 2.5 || got.Metrics[3].Date != "2026-10-15" || got.Metrics[3].PITStatus != "unverified_revision_history" {
			t.Fatal(got.Metrics[3])
		}
	}
	for _, view := range []researchdata.View{nil, store} {
		for _, metric := range readStockInfo(view, "sh600000", at("2026-10-19T17:00:00+08:00")).Metrics {
			if metric.Value != nil {
				t.Fatal("不应跨股票返回数据")
			}
		}
	}
}

func TestStockInfoAPI(t *testing.T) {
	previous := common.ResearchData
	common.ResearchData = nil
	t.Cleanup(func() { common.ResearchData = previous })
	s := &Server{mux: http.NewServeMux()}
	s.registerRoutes()
	for _, tc := range []struct {
		path   string
		status int
	}{
		{"/api/stock-info/sz000100", 200},
		{"/api/stock-info/sh000001?asOf=2025-01-07T15:00:00%2B08:00", 200},
		{"/api/stock-info/600000", 400},
		{"/api/stock-info/sh%20600", 400},
		{"/api/stock-info/sh600000?asOf=2025-01-07", 400},
		{"/api/stock-info/sh600000?asOf=2999-01-01T00:00:00Z", 400},
	} {
		r := httptest.NewRecorder()
		s.Handler().ServeHTTP(r, httptest.NewRequest(http.MethodGet, tc.path, nil))
		if r.Code != tc.status {
			t.Fatalf("%s: %d %s", tc.path, r.Code, r.Body.String())
		}
		if tc.status == 200 {
			var got stockInfoResponse
			if err := json.Unmarshal(r.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if len(got.Metrics) != 6 {
				t.Fatal(got)
			}
		}
	}
	if common.ResearchData != nil {
		t.Fatal("读取估值不得初始化数据源")
	}
}
