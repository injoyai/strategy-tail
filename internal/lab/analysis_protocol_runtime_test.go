package lab

import (
	"encoding/json"
	"math"
	"net/http"
	"testing"
	"time"

	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/tdx/v2/protocol"
)

func TestServerHoldingSessionsAnalysis(t *testing.T) {
	dir := t.TempDir()
	setupAnalysisData(t, dir)
	base := time.Date(2024, 12, 29, 15, 0, 0, 0, protocol.Location())
	writeDayDBCloses(t, dir, factorCalendarCode, []float64{10, 10, 10, 10, 10, 10}, base)
	writeDayDBOHLC(t, dir, "sh600001", []float64{10, 10, 10, 12, 12, 12}, []float64{10, 11, 12, 13, 15, 16}, base)
	// 600002 没有 Jan 1 的入场 K 线。它仍进入 Dec 31 的信号日排名。
	writeDayDBCloses(t, dir, "sh600002", []float64{10, 10, 9}, base)
	s := NewServer()
	h := s.Handler()
	doReq(t, h, http.MethodPost, "/api/analyze", map[string]any{
		"startYear": 2024, "endYear": 2024, "sampleMode": "codes",
		"sampleCodes": []string{"sh600002", "sh600001", "sh600002"},
		"kind":        "momentum", "days": 1, "holdingSessions": 1,
		"grouping": map[string]any{"mode": "quantile", "groups": 2},
	}, http.StatusOK)
	waitRunnerDone(t, s.runner)
	var report AnalysisReport
	if err := json.Unmarshal(doReq(t, h, http.MethodGet, "/api/analysis/latest", nil, http.StatusOK), &report); err != nil {
		t.Fatal(err)
	}
	if report.AnalysisVersion != 4 || report.Protocol == nil || report.EvidenceClass != evidenceExploratory ||
		report.LastDataDate != "2024-12-31" || report.Range.SampleSize != 2 || len(report.Range.SampleCodes) != 2 ||
		report.Protocol.Data.PITState != pitUnverified || report.Protocol.Labels.Kind != labelKindNextOpenHeldSessions ||
		len(report.AllGroupings) != maxGroups-minGroups+1 || len(report.Years[0].AllGroupings) != len(report.AllGroupings) {
		t.Fatalf("报告头不符: %+v", report)
	}
	ha := report.HorizonAnalyses[0]
	if ha.LabelCoverage.MissingExitBar != 1 || ha.LabelCoverage.MissingEntryBar != 1 ||
		ha.LabelCoverage.EligibleSignals != 4 || ha.LabelCoverage.LabeledSignals != 2 {
		t.Fatalf("标签覆盖 = %+v", ha.LabelCoverage)
	}
	// 两天的 Q2 都是 600001。Dec 31 的标签取 Jan 1 开盘 12 和 Jan 2 收盘 15。
	want := ((13.0/10 - 1) + (15.0/12 - 1)) / 2
	if ha.Groups[1].ForwardReturn == nil || math.Abs(*ha.Groups[1].ForwardReturn-want) > 1e-12 ||
		ha.Groups[0].Labeled != 0 || ha.Groups[0].ForwardReturn != nil || ha.Groups[0].Observations != 2 {
		t.Fatalf("分组或跨年收益不符: %+v", ha.Groups)
	}
	trials, err := s.runner.trials.ListFamily("momentum")
	if err != nil || len(trials) != 1 || trials[0].Status != TrialStatusCompleted ||
		trials[0].ProtocolHash != report.ProtocolHash || trials[0].AnalysisID != report.AnalysisID {
		t.Fatalf("试验账本 = %+v，错误 %v", trials, err)
	}
}

func TestFactorGroupingDoesNotUseFutureAvailability(t *testing.T) {
	day := core.DayOf(labelDay(0))
	days := []time.Time{day}
	vals := map[time.Time]map[string]float64{day: {"a": 1, "b": 2, "c": 3, "d": 4}}
	rets := map[time.Time]map[string]float64{day: {"b": 0, "c": 0.3, "d": 0.1}}
	groups := buildQuantileCache(days, vals, rets).aggregateSet(2, false, nil).Stats
	if groups[0].Observations != 2 || groups[0].Labeled != 1 || *groups[0].ForwardReturn != 0 ||
		groups[1].Observations != 2 || groups[1].Labeled != 2 || *groups[1].ForwardReturn != 0.2 {
		t.Fatalf("按未来数据重排了截面，或将缺失标签当成零收益: %+v", groups)
	}
}

func TestBindAnalysisProtocolUsesServerData(t *testing.T) {
	r := NewRunnerWithData(nil)
	caller := v4TestProtocol(1)
	p, err := r.bindAnalysisProtocol(AnalyzeConfig{
		RunConfig: RunConfig{SampleMode: "codes"}, Protocol: &caller,
	})
	if err != nil || p.Data.PITState != pitUnverified || p.Data.Adjustment != adjustmentUnknown ||
		deriveEvidenceClass(p) != evidenceExploratory || caller.Data.PITState != pitVerified {
		t.Fatalf("输入 %+v，服务端协议 %+v，错误 %v", caller, p, err)
	}
}
