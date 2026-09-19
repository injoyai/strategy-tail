package main

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
)

func TestGroupStatMarshalJSONEncodesInfiniteProfitFactorAsNull(t *testing.T) {
	data, err := json.Marshal(GroupStat{ProfitFactor: math.Inf(1)})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"profitFactor":null`) {
		t.Fatalf("unexpected JSON: %s", data)
	}
}

func TestExportHTMLReturnsJSONError(t *testing.T) {
	err := ExportHTML(&AnalysisResult{
		Years: []int{2026},
		DimensionResults: []DimensionResult{{
			Groups: []GroupStat{{AvgProfit: math.NaN()}},
		}},
	})
	if err == nil || !strings.Contains(err.Error(), "序列化维度统计") {
		t.Fatalf("ExportHTML() error = %v", err)
	}
}

func TestReportExportRejectsMissingYears(t *testing.T) {
	if err := ExportHTML(&AnalysisResult{}); err == nil || !strings.Contains(err.Error(), "报告年份为空") {
		t.Fatalf("ExportHTML() error = %v", err)
	}
	if err := ExportPDF(&AnalysisResult{}); err == nil || !strings.Contains(err.Error(), "报告年份为空") {
		t.Fatalf("ExportPDF() error = %v", err)
	}
}
