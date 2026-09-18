package lab

import (
	"strings"
	"testing"
	"time"

	f "github.com/injoyai/strategy-tail/strategies/factor"
)

// 测试固定常量
const (
	testAnalysisID = "an_20260917T150000000Z_a1b2c3d4"
	testUUID1      = "e5f8ce78-914d-4e17-96bb-9269d4fef7d7"
	testUUID2      = "7dd474c2-a6e9-40af-af01-dc3c46be6dad"
)

func candidateReq(uuid, name string, use CandidateUse) CreateCandidateRequest {
	return CreateCandidateRequest{RequestID: uuid, AnalysisID: testAnalysisID, Name: name, Use: use}
}

func rangeFilter() *FactorFilterSpec {
	return &FactorFilterSpec{Kind: "momentum", Days: 2, FactorVersion: 1, Operator: "gte", Min: f64ptr(0.05)}
}

// TestCandidateFromAnalysisObserve observe 模式：FactorRef/Evidence 全部由
// 报告推导，客户端不可覆写；observe 不携带 Filter。
func TestCandidateFromAnalysisObserve(t *testing.T) {
	rep := analysisReportForTest(testAnalysisID, "momentum", "2026-09-17T15:00:00+08:00")
	now := time.Date(2026, 9, 17, 15, 1, 0, 0, time.Local)
	req := candidateReq(testUUID1, "20日动量观察候选", CandidateUse{Mode: "observe"})
	c, err := candidateFromAnalysis(req, rep, now)
	if err != nil {
		t.Fatal(err)
	}
	if c.SchemaVersion != 1 || c.Revision != 0 || c.Status != CandidateStatusCandidate {
		t.Fatalf("基础字段 = %+v", c)
	}
	if c.Factor.Kind != "momentum" || c.Factor.Days != 2 || c.Factor.ImplementationVersion != 1 ||
		c.Factor.Name != "N日动量(2)" || c.Factor.Unit != "ratio" {
		t.Fatalf("FactorRef = %+v", c.Factor)
	}
	if c.Use.Mode != "observe" || c.Use.Filter != nil {
		t.Fatalf("Use = %+v", c.Use)
	}
	if c.Evidence.AnalysisID != testAnalysisID || c.Evidence.AnalysisVersion != 3 ||
		c.Evidence.Window != 1 || c.Evidence.ReportSHA256 == "" {
		t.Fatalf("Evidence = %+v", c.Evidence)
	}
	if c.Evidence.Range.SampleMode != "codes" || c.Evidence.FirstDataDate != "2025-01-02" {
		t.Fatalf("Evidence 摘要 = %+v", c.Evidence)
	}
	if c.CreatedAt == "" || c.UpdatedAt == "" || c.CreatedAt != c.UpdatedAt {
		t.Fatalf("时间戳 = %q/%q", c.CreatedAt, c.UpdatedAt)
	}
}

// TestCandidateFromAnalysisRange range 模式：filter 与报告快照完全一致，
// 且包含用户明确的固定阈值。
func TestCandidateFromAnalysisRange(t *testing.T) {
	rep := analysisReportForTest(testAnalysisID, "momentum", "2026-09-17T15:00:00+08:00")
	now := time.Date(2026, 9, 17, 15, 1, 0, 0, time.Local)
	req := candidateReq(testUUID1, "20日动量至少5%", CandidateUse{Mode: "range", Filter: rangeFilter()})
	c, err := candidateFromAnalysis(req, rep, now)
	if err != nil {
		t.Fatal(err)
	}
	if c.Use.Mode != "range" || c.Use.Filter == nil {
		t.Fatalf("Use = %+v", c.Use)
	}
	if c.Use.Filter.Kind != "momentum" || c.Use.Filter.Days != 2 ||
		c.Use.Filter.FactorVersion != 1 || c.Use.Filter.Operator != "gte" ||
		c.Use.Filter.Min == nil || *c.Use.Filter.Min != 0.05 {
		t.Fatalf("Filter = %+v", c.Use.Filter)
	}
}

// TestCandidateRejectsQuantileAutoBoundary 纯合同：请求没有用户阈值时不能
// 生成 range（不允许从等频累计组值域自动推导固定阈值）。
func TestCandidateRejectsQuantileAutoBoundary(t *testing.T) {
	// range 必须提供 filter
	req := candidateReq(testUUID1, "x", CandidateUse{Mode: "range"})
	if _, err := normalizeCreateCandidateRequest(req); err == nil {
		t.Fatal("range 缺 filter 应拒绝")
	}
	// filter 缺少阈值（gte 无 min）→ validate 拒绝
	req = candidateReq(testUUID1, "x", CandidateUse{Mode: "range",
		Filter: &FactorFilterSpec{Kind: "momentum", Days: 2, FactorVersion: 1, Operator: "gte"}})
	if _, err := normalizeCreateCandidateRequest(req); err == nil {
		t.Fatal("gte 缺 min 应拒绝")
	}
	// observe 携带 filter 拒绝
	req = candidateReq(testUUID1, "x", CandidateUse{Mode: "observe", Filter: rangeFilter()})
	if _, err := normalizeCreateCandidateRequest(req); err == nil {
		t.Fatal("observe 携带 filter 应拒绝")
	}
}

// TestNormalizeCreateCandidateRequest 名称/UUID/备注长度合同。
func TestNormalizeCreateCandidateRequest(t *testing.T) {
	good := candidateReq(testUUID1, "候选", CandidateUse{Mode: "observe"})
	norm, err := normalizeCreateCandidateRequest(good)
	if err != nil {
		t.Fatalf("合法请求应通过: %v", err)
	}
	if norm.Name != "候选" {
		t.Fatalf("名称应保留: %q", norm.Name)
	}
	if strings.TrimSpace(norm.Name) == "" {
		t.Fatal("空名称")
	}
	cases := []struct {
		note string
		mut  func(*CreateCandidateRequest)
	}{
		{"非法 UUID", func(r *CreateCandidateRequest) { r.RequestID = "not-a-uuid" }},
		{"空名称", func(r *CreateCandidateRequest) { r.Name = "  " }},
		{"名称超长", func(r *CreateCandidateRequest) { r.Name = strings.Repeat("策", 81) }},
		{"备注超长", func(r *CreateCandidateRequest) { r.Notes = strings.Repeat("注", 2001) }},
		{"未知模式", func(r *CreateCandidateRequest) { r.Use = CandidateUse{Mode: "topn"} }},
	}
	for _, c := range cases {
		r := good
		c.mut(&r)
		if _, err := normalizeCreateCandidateRequest(r); err == nil {
			t.Fatalf("%s 应拒绝", c.note)
		}
	}
	// 空格名称 trim
	spaced := good
	spaced.Name = "  候选  "
	norm, err = normalizeCreateCandidateRequest(spaced)
	if err != nil || norm.Name != "候选" {
		t.Fatalf("名称应 trim: %q, %v", norm.Name, err)
	}
}

// TestCandidateFromAnalysisRejectsV2 非 v3 报告（缺 analysisId/版本）拒绝保存。
func TestCandidateFromAnalysisRejectsV2(t *testing.T) {
	rep := analysisReportForTest("", "momentum", "2026-09-17T15:00:00+08:00")
	rep.AnalysisVersion = 2
	rep.Factor.ImplementationVersion = 0
	req := candidateReq(testUUID1, "旧报告", CandidateUse{Mode: "observe"})
	if _, err := candidateFromAnalysis(req, rep, time.Now()); err == nil {
		t.Fatal("v2 报告应拒绝保存为候选")
	}
}

// TestCandidateFromAnalysisRangeMismatch filter 与报告快照不一致拒绝。
func TestCandidateFromAnalysisRangeMismatch(t *testing.T) {
	rep := analysisReportForTest(testAnalysisID, "momentum", "2026-09-17T15:00:00+08:00")
	now := time.Date(2026, 9, 17, 15, 1, 0, 0, time.Local)
	// days 不匹配
	req := candidateReq(testUUID1, "x", CandidateUse{Mode: "range",
		Filter: &FactorFilterSpec{Kind: "momentum", Days: 60, FactorVersion: 1, Operator: "gte", Min: f64ptr(0.05)}})
	if _, err := candidateFromAnalysis(req, rep, now); err == nil {
		t.Fatal("days 不一致应拒绝")
	}
	// kind 不匹配
	req = candidateReq(testUUID1, "x", CandidateUse{Mode: "range",
		Filter: &FactorFilterSpec{Kind: "volatility", Days: 2, FactorVersion: 1, Operator: "gte", Min: f64ptr(0.05)}})
	if _, err := candidateFromAnalysis(req, rep, now); err == nil {
		t.Fatal("kind 不一致应拒绝")
	}
	// version 不匹配
	req = candidateReq(testUUID1, "x", CandidateUse{Mode: "range",
		Filter: &FactorFilterSpec{Kind: "momentum", Days: 2, FactorVersion: 99, Operator: "gte", Min: f64ptr(0.05)}})
	if _, err := candidateFromAnalysis(req, rep, now); err == nil {
		t.Fatal("version 不一致应拒绝")
	}
}

// TestCompatibilityOf ready/stale/missing 派生状态。
func TestCompatibilityOf(t *testing.T) {
	cur, _ := f.Catalog("momentum")
	curVer := cur.ImplementationVersion
	// ready
	c := FactorCandidate{Factor: FactorRef{Kind: "momentum", ImplementationVersion: curVer}}
	if comp := compatibilityOf(c); comp.State != "ready" || comp.CurrentVersion != curVer {
		t.Fatalf("ready: %+v", comp)
	}
	// stale
	c.Factor.ImplementationVersion = curVer + 1
	if comp := compatibilityOf(c); comp.State != "stale" || comp.CurrentVersion != curVer {
		t.Fatalf("stale: %+v", comp)
	}
	// missing
	c.Factor.Kind = "no_such_kind"
	if comp := compatibilityOf(c); comp.State != "missing" {
		t.Fatalf("missing: %+v", comp)
	}
}

// TestCandidateRequestHash 相同内容同 hash，不同内容不同 hash。
func TestCandidateRequestHash(t *testing.T) {
	a := candidateReq(testUUID1, "候选A", CandidateUse{Mode: "observe"})
	b := candidateReq(testUUID1, "候选A", CandidateUse{Mode: "observe"})
	c := candidateReq(testUUID1, "候选B", CandidateUse{Mode: "observe"})
	ha, err := candidateRequestHash(a)
	if err != nil {
		t.Fatal(err)
	}
	hb, _ := candidateRequestHash(b)
	hc, _ := candidateRequestHash(c)
	if ha != hb {
		t.Fatalf("相同内容 hash 应相同: %s vs %s", ha, hb)
	}
	if ha == hc {
		t.Fatal("不同内容 hash 应不同")
	}
}
