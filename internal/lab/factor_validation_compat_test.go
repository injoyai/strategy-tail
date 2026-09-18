package lab

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// factor_validation_compat_test.go 旧合同兼容夹具测试（实施计划 Task 0）。
//
// 目的：把 v3 分析报告与候选修订的人工最小夹具纳入测试，锁定既有行为基线，
// 作为后续任务（协议字段、v4 报告、验证结论）的回归护栏：
//   - v3 报告可读取（未来派生 legacy/exploratory 的前提）；
//   - 重新保存旧报告不会伪造 v4 新字段；
//   - 旧候选可经真实存储链路读取，且不携带人工验证结论；
//   - 旧 /api/analyze JSON 仍可 Decode。
//
// 夹具全部为人工最小数据（testdata/analysis_v3.json、testdata/candidate_v1.json），
// 不复制任何真实研究报告。这些测试当前必须全部通过；后续任务破坏旧合同时最先在此失败。

// compatFixtureReport 读取 v3 分析报告夹具并解码为 AnalysisReport。
func compatFixtureReport(t *testing.T) (*AnalysisReport, []byte) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "analysis_v3.json"))
	if err != nil {
		t.Fatalf("读取 analysis_v3.json 夹具失败: %v", err)
	}
	var rep AnalysisReport
	if err := json.Unmarshal(data, &rep); err != nil {
		t.Fatalf("v3 报告夹具必须可解码为 AnalysisReport: %v", err)
	}
	return &rep, data
}

// compatFixtureCandidate 读取候选修订夹具并解码为 FactorCandidate。
func compatFixtureCandidate(t *testing.T) (FactorCandidate, []byte) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "candidate_v1.json"))
	if err != nil {
		t.Fatalf("读取 candidate_v1.json 夹具失败: %v", err)
	}
	var c FactorCandidate
	if err := json.Unmarshal(data, &c); err != nil {
		t.Fatalf("候选夹具必须可解码为 FactorCandidate: %v", err)
	}
	return c, data
}

// TestCompatV3ReportFixtureLoads 锁定 v3 报告夹具的旧合同：
// 单 Window 标量字段、ICStats 数值字段、合法 an_ ID，以及缺失
// protocol/horizons/validation（未来字段）时的零值行为。
func TestCompatV3ReportFixtureLoads(t *testing.T) {
	rep, data := compatFixtureReport(t)

	if rep.AnalysisVersion != 3 {
		t.Fatalf("夹具必须是 v3 报告，实际 analysisVersion=%d", rep.AnalysisVersion)
	}
	if rep.Window != 5 {
		t.Errorf("v3 报告只有单 Window 标量字段，期望 5，实际 %d", rep.Window)
	}
	if !validAnalysisID(rep.AnalysisID) {
		t.Errorf("夹具 analysisId %q 必须满足 an_ ID 格式", rep.AnalysisID)
	}
	if rep.Stats.Pairs != 10 || rep.Stats.Mean != 0.03 || rep.Stats.Std != 0.12 || rep.Stats.TStat != 0.78 {
		t.Errorf("ICStats 数值字段与夹具不符: %+v", rep.Stats)
	}
	if rep.Factor.Kind != "momentum" || rep.Factor.Days != 20 || rep.Factor.ImplementationVersion != 1 {
		t.Errorf("因子快照与夹具不符: %+v", rep.Factor)
	}
	if rep.FirstDataDate != "2024-01-02" || rep.LastDataDate != "2024-12-31" {
		t.Errorf("数据区间与夹具不符: %q ~ %q", rep.FirstDataDate, rep.LastDataDate)
	}

	// 缺失未来字段：v3 报告不得携带 protocol/horizons/validation 键
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("夹具应为合法 JSON 对象: %v", err)
	}
	for _, key := range []string{"protocol", "horizons", "validation"} {
		if _, ok := raw[key]; ok {
			t.Errorf("v3 报告夹具不得包含未来字段 %q", key)
		}
	}
}

// TestCompatResaveOldReportStaysV3 证明：把旧 v3 报告重新存入 AnalysisStore
// 再读回，仍是 v3，且序列化结果不出现任何 v4 新字段（不伪造新版本）。
func TestCompatResaveOldReportStaysV3(t *testing.T) {
	rep, _ := compatFixtureReport(t)

	store := NewAnalysisStore(t.TempDir())
	if err := store.Save(rep); err != nil {
		t.Fatalf("v3 报告必须可保存: %v", err)
	}
	got, err := store.Get(rep.AnalysisID)
	if err != nil {
		t.Fatalf("保存后的 v3 报告必须可按 ID 读回: %v", err)
	}
	if got.AnalysisVersion != 3 {
		t.Errorf("重新保存不得升级报告版本：期望 3，实际 %d", got.AnalysisVersion)
	}
	if got.Window != rep.Window || got.AnalysisID != rep.AnalysisID {
		t.Errorf("读回内容与原报告不符: id=%q window=%d", got.AnalysisID, got.Window)
	}

	buf, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("读回报告必须可序列化: %v", err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(buf, &raw); err != nil {
		t.Fatalf("读回报告应为合法 JSON 对象: %v", err)
	}
	for _, key := range []string{"protocol", "horizons", "validation"} {
		if _, ok := raw[key]; ok {
			t.Errorf("重新保存的 v3 报告不得出现 v4 新字段 %q（不得伪造 v4）", key)
		}
	}
}

// TestCompatCandidateV1FixtureVerifiable 证明：旧候选修订夹具自洽
// （reportSha256 等于 analysis_v3.json 字节摘要），可经真实 CandidateStore
// 读取链路（SHA-256 fail-closed 校验）还原；旧候选状态仍是 candidate，
// 且不携带任何人工验证结论字段（validation verdict 由后续任务引入）。
func TestCompatCandidateV1FixtureVerifiable(t *testing.T) {
	c, candData := compatFixtureCandidate(t)
	_, analysisData := compatFixtureReport(t)

	// 夹具自洽：证据哈希必须等于报告夹具的逐字节摘要
	sum := sha256.Sum256(analysisData)
	if got := hex.EncodeToString(sum[:]); got != c.Evidence.ReportSHA256 {
		t.Fatalf("夹具 reportSha256 与 analysis_v3.json 实际摘要不一致: %s != %s",
			got, c.Evidence.ReportSHA256)
	}
	if c.Evidence.AnalysisID != "an_20260102T030405000Z_deadbeef" || c.Evidence.AnalysisVersion != 3 {
		t.Fatalf("证据引用与报告夹具不一致: %s v%d", c.Evidence.AnalysisID, c.Evidence.AnalysisVersion)
	}

	// 放入真实存储布局后，经完整读取链路（含哈希与摘要一致性校验）还原
	root := t.TempDir()
	revDir := filepath.Join(root, c.ID, "revisions")
	if err := os.MkdirAll(revDir, 0o755); err != nil {
		t.Fatalf("构造候选目录失败: %v", err)
	}
	if err := os.WriteFile(filepath.Join(revDir, "000001.analysis.json"), analysisData, 0o644); err != nil {
		t.Fatalf("写入证据夹具失败: %v", err)
	}
	if err := os.WriteFile(filepath.Join(revDir, "000001.json"), candData, 0o644); err != nil {
		t.Fatalf("写入候选夹具失败: %v", err)
	}

	got, err := NewCandidateStore(root).Get(c.ID)
	if err != nil {
		t.Fatalf("旧候选夹具必须可经存储读取（fail-closed 校验通过）: %v", err)
	}
	if got.Status != CandidateStatusCandidate {
		t.Errorf("旧候选状态应保持 candidate，实际 %s", got.Status)
	}
	if got.Revision != 1 || got.SchemaVersion != 1 {
		t.Errorf("候选修订/模式版本与夹具不符: revision=%d schemaVersion=%d", got.Revision, got.SchemaVersion)
	}
	if got.ID != c.ID || got.Evidence.ReportSHA256 != c.Evidence.ReportSHA256 {
		t.Errorf("候选 ID 或证据哈希在读取后被改变: id=%q sha=%q", got.ID, got.Evidence.ReportSHA256)
	}

	// 旧候选记录不得携带验证结论字段（不存在人工 verdict）
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(candData, &raw); err != nil {
		t.Fatalf("候选夹具应为合法 JSON 对象: %v", err)
	}
	if _, ok := raw["validation"]; ok {
		t.Errorf("旧候选修订夹具不得包含 validation 字段")
	}
}

// TestCompatAnalyzeConfigOldJSONDecodes 证明旧 /api/analyze 请求体仍可
// Decode：只含旧字段的请求解析成功；出现未知新字段也不破坏解码。
func TestCompatAnalyzeConfigOldJSONDecodes(t *testing.T) {
	old := `{"startYear":2024,"endYear":2025,"sampleMode":"all",` +
		`"kind":"momentum","days":20,"window":5,"grouping":{"groups":5}}`
	var cfg AnalyzeConfig
	if err := json.NewDecoder(strings.NewReader(old)).Decode(&cfg); err != nil {
		t.Fatalf("旧 /api/analyze JSON 必须可解码: %v", err)
	}
	if cfg.Kind != "momentum" || cfg.Days != 20 || cfg.Window != 5 {
		t.Errorf("解码结果与旧合同不符: kind=%s days=%d window=%d", cfg.Kind, cfg.Days, cfg.Window)
	}
	if cfg.StartYear != 2024 || cfg.EndYear != 2025 || cfg.SampleMode != "all" {
		t.Errorf("RunConfig 字段与旧合同不符: %+v", cfg.RunConfig)
	}
	if cfg.Grouping.Groups != 5 {
		t.Errorf("分组配置与旧合同不符: %+v", cfg.Grouping)
	}

	withFuture := `{"kind":"momentum","days":20,"window":5,"protocol":{"version":1}}`
	var cfg2 AnalyzeConfig
	if err := json.NewDecoder(strings.NewReader(withFuture)).Decode(&cfg2); err != nil {
		t.Fatalf("携带未来字段的请求体仍必须可解码: %v", err)
	}
	if cfg2.Kind != "momentum" {
		t.Errorf("未知字段破坏了解码: kind=%s", cfg2.Kind)
	}
}
