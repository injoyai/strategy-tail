package lab

import (
	"bytes"
	"encoding/json"
	"github.com/xuri/excelize/v2"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReportExcelIncludesAllTradesAndFrozenStats(t *testing.T) {
	t.Chdir(t.TempDir())
	rep := writeHistoryReport(t, "2026-10-20T15:30:00+08:00")
	rep.Variants[0].Name = `=HYPERLINK("https://example.invalid")`
	rep.Variants[0].Stats = TradeStatsJSON{Total: 2505, Win: 7, WinRate: 12.34, WinSum: 10}
	for i := range 2505 {
		rep.Variants[0].Trades = append(rep.Variants[0].Trades, TradeJSON{Code: "000001", BuyTime: "2025-01-02 15:00:00",
			BuyPrice: 10.123, SellTime: "2025-01-03 09:31:00", SellPrice: 11.789, Quantity: 100,
			Profit: -123.45, Rate: -1.2345, HoldingDays: 1, Virtual: i == 2504})
	}
	rep.Variants = append(rep.Variants, VariantReport{Name: "空变体"})
	data, err := json.Marshal(rep)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join("output", "trades", rep.RunID(), "report.json"), data, 0644); err != nil {
		t.Fatal(err)
	}
	s := NewServer()
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/report/"+rep.RunID()+"/export.xlsx", nil))
	if w.Code != http.StatusOK || !strings.Contains(w.Header().Get("Content-Type"), "spreadsheetml") || !strings.Contains(w.Header().Get("Content-Disposition"), "attachment") {
		t.Fatalf("download status=%d headers=%v", w.Code, w.Header())
	}
	f, err := excelize.OpenReader(bytes.NewReader(w.Body.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if got := strings.Join(f.GetSheetList(), ","); got != "策略参数,绩效汇总,交易明细" {
		t.Fatalf("sheets=%s", got)
	}
	for _, check := range []struct{ sheet, cell, want string }{
		{"绩效汇总", "C2", "7"}, {"绩效汇总", "E2", "12.34"}, {"绩效汇总", "F2", "∞"},
		{"绩效汇总", "A3", "空变体"}, {"交易明细", "B2506", "000001"},
		{"交易明细", "D2506", "10.123"}, {"交易明细", "H2506", "-123.45"},
		{"交易明细", "K2506", "期末估值（Virtual）"}, {"交易明细", "K2", "实际成交"},
	} {
		value, err := f.GetCellValue(check.sheet, check.cell, excelize.Options{RawCellValue: true})
		if err != nil || value != check.want {
			t.Errorf("%s!%s=%q want=%q err=%v", check.sheet, check.cell, value, check.want, err)
		}
	}
	if formula, err := f.GetCellFormula("交易明细", "A2"); err != nil || formula != "" {
		t.Fatalf("strategy name interpreted as formula: %q, %v", formula, err)
	}
	if value, _ := f.GetCellValue("交易明细", "A2"); value != rep.Variants[0].Name {
		t.Fatalf("strategy name changed: %q", value)
	}
	rows, err := f.GetRows("交易明细")
	if err != nil || len(rows) != 2506 {
		t.Fatalf("rows=%d err=%v", len(rows), err)
	}
	panes, err := f.GetPanes("交易明细")
	if err != nil || !panes.Freeze || panes.YSplit != 1 {
		t.Fatalf("panes=%+v err=%v", panes, err)
	}
	if after, err := os.ReadFile(filepath.Join("output", "trades", rep.RunID(), "report.json")); err != nil || !bytes.Equal(data, after) {
		t.Fatal("export changed persisted report")
	}
}

func TestReportExcelEmptyAndLongSnapshot(t *testing.T) {
	name := strings.Repeat("策略😀", 10000)
	f, err := reportWorkbook(&Report{Config: RunConfig{ScriptName: name}, Variants: []VariantReport{{Name: "无交易"}}})
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	rows, err := f.GetRows("策略参数")
	if err != nil {
		t.Fatal(err)
	}
	var config strings.Builder
	for _, row := range rows {
		if strings.HasPrefix(row[0], "运行配置（") {
			config.WriteString(row[1])
		}
	}
	var restored RunConfig
	if err := json.Unmarshal([]byte(config.String()), &restored); err != nil || restored.ScriptName != name {
		t.Fatalf("long snapshot truncated: %v", err)
	}
	trades, err := f.GetRows("交易明细")
	if err != nil || len(trades) != 1 {
		t.Fatalf("empty trades=%d err=%v", len(trades), err)
	}
	value, err := f.GetCellValue("绩效汇总", "F2")
	if err != nil || value != "—" {
		t.Fatalf("empty stats profitFactor=%s err=%v", value, err)
	}
}

func TestReportExcelGuards(t *testing.T) {
	t.Chdir(t.TempDir())
	h := NewServer().Handler()
	for _, id := range []string{"..%2Fbuilder", ".hidden", "latest", "foo%5Cbar"} {
		doReq(t, h, http.MethodGet, "/api/report/"+id+"/export.xlsx", nil, http.StatusBadRequest)
	}
	doReq(t, h, http.MethodGet, "/api/report/missing/export.xlsx", nil, http.StatusNotFound)
}

func TestReportExcelAccountAndBenchmarkSnapshots(t *testing.T) {
	f, err := reportWorkbook(&Report{Variants: []VariantReport{{Name: "账户策略", Account: &AccountReport{
		MissingMinuteDays: 2, StaleValuationDays: 3,
		Points: []AccountPoint{{Date: "2025-01-02", Equity: 100123.456, Cash: 80000, Holdings: 1, Return: 0.123456}},
	}}}, Benchmark: []BenchmarkPoint{{Date: "2025-01-02", Return: -0.25}}})
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for _, check := range []struct{ sheet, cell, want string }{
		{"账户净值", "C2", "100123.456"}, {"账户净值", "F2", "0.123456"},
		{"账户净值", "G2", "2"}, {"账户净值", "H2", "3"}, {"上证基准", "B2", "-0.25"},
	} {
		value, err := f.GetCellValue(check.sheet, check.cell, excelize.Options{RawCellValue: true})
		if err != nil || value != check.want {
			t.Errorf("%s!%s=%q want=%q err=%v", check.sheet, check.cell, value, check.want, err)
		}
	}
}
