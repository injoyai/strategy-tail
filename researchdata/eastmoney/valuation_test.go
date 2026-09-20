package eastmoney

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/injoyai/strategy-tail/researchdata"
)

func TestClientFetchesAndNormalizesPagedValuationHistory(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("reportName") != "RPT_VALUEANALYSIS_DET" {
			t.Errorf("reportName = %q", q.Get("reportName"))
		}
		filter := q.Get("filter")
		for _, want := range []string{`SECURITY_CODE="600519"`, `TRADE_DATE>='2020-01-01'`, `TRADE_DATE<='2020-01-03'`} {
			if !strings.Contains(filter, want) {
				t.Errorf("filter %q missing %q", filter, want)
			}
		}
		page := q.Get("pageNumber")
		date := "2020-01-02 00:00:00"
		pe := "34.5"
		if page == "2" {
			date = "2020-01-03 00:00:00"
			pe = "33.5"
		}
		_, _ = fmt.Fprintf(w, `{"success":true,"message":"ok","result":{"pages":2,"count":2,"data":[{"SECURITY_CODE":"600519","SECURITY_NAME_ABBR":"贵州茅台","BOARD_NAME":"白酒","TRADE_DATE":"%s","PE_TTM":%s,"PE_LAR":34,"PB_MRQ":10,"PS_TTM":16,"PCF_OCF_TTM":35,"PCF_OCF_LAR":31,"PEG_CAR":2.2,"CLOSE_PRICE":1130,"TOTAL_MARKET_CAP":1000,"NOTLIMITED_MARKETCAP_A":900,"TOTAL_SHARES":100,"FREE_SHARES_A":90}]}}`, date, pe)
	}))
	defer server.Close()

	client := New()
	client.BaseURL = server.URL
	client.PageSize = 1
	loc := client.Location
	query := researchdata.Query{
		Dataset: DatasetValuationDaily,
		Codes:   []string{"sh600519"},
		Start:   time.Date(2020, 1, 1, 0, 0, 0, 0, loc),
		End:     time.Date(2020, 1, 3, 23, 59, 0, 0, loc),
		AsOf:    time.Date(2020, 1, 3, 23, 59, 0, 0, loc),
	}
	records, err := client.Fetch(context.Background(), query)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 {
		t.Fatalf("records = %d", len(records))
	}
	if records[0].Code != "sh600519" || records[0].Key != "2020-01-02" || records[0].Values["pe_ttm"] != 34.5 {
		t.Fatalf("first record = %+v", records[0])
	}
	if records[0].EventAt.Hour() != 15 || !records[0].EventAt.Equal(records[0].AvailableAt) {
		t.Fatalf("availability = %s / %s", records[0].EventAt, records[0].AvailableAt)
	}
	if records[0].Attributes["pit_status"] != "unverified_revision_history" {
		t.Fatalf("attributes = %+v", records[0].Attributes)
	}
}

func TestClientRejectsFutureAndImplicitFullMarketQueries(t *testing.T) {
	client := New()
	now := time.Now()
	if _, err := client.Fetch(context.Background(), researchdata.Query{Dataset: DatasetValuationDaily, AsOf: now}); err == nil {
		t.Fatal("query without explicit codes should fail")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, `{"success":true,"result":{"pages":1,"count":1,"data":[{"SECURITY_CODE":"600519","TRADE_DATE":"2025-01-03 00:00:00","PE_TTM":10}]}}`)
	}))
	defer server.Close()
	client.BaseURL = server.URL
	loc := client.Location
	_, err := client.Fetch(context.Background(), researchdata.Query{
		Dataset: DatasetValuationDaily,
		Codes:   []string{"sh600519"},
		AsOf:    time.Date(2025, 1, 2, 23, 59, 0, 0, loc),
	})
	if err == nil {
		t.Fatal("future record should fail closed")
	}
}
