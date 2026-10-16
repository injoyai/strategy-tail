package lab

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	common "github.com/injoyai/strategy-tail"
	"github.com/injoyai/strategy-tail/lib/extend"
	"github.com/injoyai/strategy-tail/strategies/util"
)

func TestKlineIndicatorsUseHistoryBeforeDisplayWindow(t *testing.T) {
	dir := t.TempDir()
	old := common.Pull
	common.Pull = &extend.PullKline{Config: extend.PullKlineConfig{Dir: dir}}
	t.Cleanup(func() { common.Pull = old })
	base := time.Date(2025, 1, 1, 0, 0, 0, 0, time.Local)
	writeDayDBCloses(t, dir, "sh600000", []float64{10, 11, 9, 12, 13, 11}, base)
	ks, err := commonPullDayKlines("sh600000", time.Time{}, base.AddDate(0, 0, 6))
	if err != nil {
		t.Fatal(err)
	}
	dif := util.MACDDIF(ks, 12, 26)
	hist := util.MACDHistogram(ks, 12, 26, 9)
	read := func(from, to string) []map[string]any {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/api/kline/sh600000?from="+from+"&to="+to, nil)
		req.SetPathValue("code", "sh600000")
		res := httptest.NewRecorder()
		new(Server).handleKline(res, req)
		if res.Code != http.StatusOK {
			t.Fatalf("kline: %d %s", res.Code, res.Body.String())
		}
		var rows []map[string]any
		if err := json.Unmarshal(res.Body.Bytes(), &rows); err != nil {
			t.Fatal(err)
		}
		return rows
	}
	rows := read("2025-01-04", "2025-01-05")
	if len(rows) != 2 || rows[0]["date"] != "2025-01-04" || rows[1]["date"] != "2025-01-05" {
		t.Fatalf("window: %v", rows)
	}
	for i, row := range rows {
		j := i + 3
		if row["dif"] != dif[j] || row["dea"] != dif[j]-hist[j] || row["macd"] != hist[j] || row["volume"] == nil {
			t.Fatalf("indicators differ from strategy at %d: %v", j, row)
		}
	}
	full := read("2025-01-01", "2025-01-06")
	if rows[0]["macd"] != full[3]["macd"] || rows[0]["macd"] == float64(0) {
		t.Fatal("display window must not reset EMA")
	}
	if empty := read("2025-02-01", "2025-02-02"); len(empty) != 0 {
		t.Fatalf("empty window: %v", empty)
	}
}
