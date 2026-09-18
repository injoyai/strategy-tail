package lab

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// candResp 候选 API 响应（候选 + 实时兼容状态）。
type candResp struct {
	FactorCandidate
	Compatibility CandidateCompatibility `json:"compatibility"`
}

// newCandidateTestServer 注入独立临时根的候选 API 测试服务。
func newCandidateTestServer(t *testing.T) (*Server, *AnalysisStore) {
	t.Helper()
	ana := NewAnalysisStore(t.TempDir())
	cand := NewCandidateStore(t.TempDir())
	return newServerWithStores(ana, cand), ana
}

func saveV3Report(t *testing.T, ana *AnalysisStore) {
	t.Helper()
	if err := ana.Save(analysisReportForTest(testAnalysisID, "momentum", "2026-09-17T15:00:00+08:00")); err != nil {
		t.Fatal(err)
	}
}

func candidateBody(uuid, name string, use map[string]any) map[string]any {
	return map[string]any{
		"requestId":  uuid,
		"analysisId": testAnalysisID,
		"name":       name,
		"use":        use,
	}
}

func postCandidate(t *testing.T, h http.Handler, body map[string]any, wantCode int) (bool, candResp) {
	t.Helper()
	res := doReq(t, h, http.MethodPost, "/api/factor-candidates", body, wantCode)
	var out struct {
		Created   bool     `json:"created"`
		Candidate candResp `json:"candidate"`
	}
	if err := json.Unmarshal(res, &out); err != nil {
		t.Fatalf("解析创建响应: %v, %s", err, res)
	}
	return out.Created, out.Candidate
}

// TestServerCandidateCreateObserve 创建 observe 候选：201、FactorRef/Evidence
// 由报告推导、compatibility ready。
func TestServerCandidateCreateObserve(t *testing.T) {
	s, ana := newCandidateTestServer(t)
	saveV3Report(t, ana)
	created, c := postCandidate(t, s.Handler(),
		candidateBody(testUUID1, "观察候选", map[string]any{"mode": "observe"}), http.StatusCreated)
	if !created {
		t.Fatal("首次创建应返回 created=true")
	}
	if c.ID == "" || c.Revision != 1 || c.Status != CandidateStatusCandidate {
		t.Fatalf("候选基础字段 = %+v", c)
	}
	if c.Factor.Kind != "momentum" || c.Factor.Days != 2 || c.Factor.ImplementationVersion != 1 {
		t.Fatalf("FactorRef = %+v", c.Factor)
	}
	if c.Evidence.AnalysisID != testAnalysisID || c.Evidence.AnalysisVersion != 3 {
		t.Fatalf("Evidence = %+v", c.Evidence)
	}
	if c.Compatibility.State != "ready" {
		t.Fatalf("compatibility = %+v", c.Compatibility)
	}
}

// TestServerCandidateCreateRange range 候选：页面 5% → JSON 原始比例 0.05。
func TestServerCandidateCreateRange(t *testing.T) {
	s, ana := newCandidateTestServer(t)
	saveV3Report(t, ana)
	use := map[string]any{
		"mode": "range",
		"filter": map[string]any{
			"kind": "momentum", "days": 2, "factorVersion": 1,
			"operator": "gte", "min": 0.05,
		},
	}
	_, c := postCandidate(t, s.Handler(), candidateBody(testUUID1, "至少5%", use), http.StatusCreated)
	if c.Use.Mode != "range" || c.Use.Filter == nil {
		t.Fatalf("Use = %+v", c.Use)
	}
	if c.Use.Filter.Min == nil || *c.Use.Filter.Min != 0.05 {
		t.Fatalf("阈值应为原始比例 0.05: %+v", c.Use.Filter)
	}
}

// TestServerCandidateIdempotent 相同 requestId + 内容幂等：第二次 200 同一 ID。
func TestServerCandidateIdempotent(t *testing.T) {
	s, ana := newCandidateTestServer(t)
	saveV3Report(t, ana)
	body := candidateBody(testUUID1, "观察", map[string]any{"mode": "observe"})
	created1, c1 := postCandidate(t, s.Handler(), body, http.StatusCreated)
	created2, c2 := postCandidate(t, s.Handler(), body, http.StatusOK)
	if !created1 || created2 {
		t.Fatalf("幂等标记 = %v/%v", created1, created2)
	}
	if c1.ID != c2.ID {
		t.Fatalf("幂等应返回同一候选: %s vs %s", c1.ID, c2.ID)
	}
}

// TestServerCandidateIdempotencyReuseConflict 同一 requestId 不同内容 → 409。
func TestServerCandidateIdempotencyReuseConflict(t *testing.T) {
	s, ana := newCandidateTestServer(t)
	saveV3Report(t, ana)
	postCandidate(t, s.Handler(), candidateBody(testUUID1, "A", map[string]any{"mode": "observe"}), http.StatusCreated)
	res := doReq(t, s.Handler(), http.MethodPost, "/api/factor-candidates",
		candidateBody(testUUID1, "B", map[string]any{"mode": "observe"}), http.StatusConflict)
	if !strings.Contains(string(res), "error") {
		t.Fatalf("冲突响应缺 error: %s", res)
	}
}

// TestServerCandidateDistinctUses 同一 analysis 不同 requestId 可创建多条。
func TestServerCandidateDistinctUses(t *testing.T) {
	s, ana := newCandidateTestServer(t)
	saveV3Report(t, ana)
	_, c1 := postCandidate(t, s.Handler(), candidateBody(testUUID1, "观察", map[string]any{"mode": "observe"}), http.StatusCreated)
	_, c2 := postCandidate(t, s.Handler(), candidateBody(testUUID2, "区间",
		map[string]any{"mode": "range", "filter": map[string]any{
			"kind": "momentum", "days": 2, "factorVersion": 1, "operator": "gte", "min": 0.05}}), http.StatusCreated)
	if c1.ID == c2.ID {
		t.Fatal("不同用途应创建不同候选")
	}
	res := doReq(t, s.Handler(), http.MethodGet, "/api/factor-candidates", nil, http.StatusOK)
	if !strings.Contains(string(res), `"candidates"`) || !strings.Contains(string(res), c1.ID) || !strings.Contains(string(res), c2.ID) {
		t.Fatalf("列表应包含两条候选: %s", res)
	}
}

// TestServerCandidateV2Rejected 旧版本报告（v2，无 analysisId）→ 409。
func TestServerCandidateV2Rejected(t *testing.T) {
	s, ana := newCandidateTestServer(t)
	// 手工写入 v2 报告（合法 ID 目录，但内容无 analysisId/实现版本）
	id := testAnalysisID
	dir := filepath.Join(ana.root, "momentum", id)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	v2 := analysisReportForTest("", "momentum", "2026-09-17T15:00:00+08:00")
	v2.AnalysisVersion = 2
	buf, _ := json.Marshal(v2)
	if err := os.WriteFile(filepath.Join(dir, "report.json"), buf, 0644); err != nil {
		t.Fatal(err)
	}
	res := doReq(t, s.Handler(), http.MethodPost, "/api/factor-candidates",
		candidateBody(testUUID1, "旧报告", map[string]any{"mode": "observe"}), http.StatusConflict)
	if !strings.Contains(string(res), "重新运行") {
		t.Fatalf("v2 拒绝响应 = %s", res)
	}
}

// TestServerCandidateUnknownAnalysis 未知 analysisId → 404。
func TestServerCandidateUnknownAnalysis(t *testing.T) {
	s, _ := newCandidateTestServer(t)
	body := candidateBody(testUUID1, "x", map[string]any{"mode": "observe"})
	body["analysisId"] = "an_20260917T000000000Z_ffffffff"
	doReq(t, s.Handler(), http.MethodPost, "/api/factor-candidates", body, http.StatusNotFound)
}

// TestServerCandidateBodyTooLarge 请求体超过 64 KiB → 413。
func TestServerCandidateBodyTooLarge(t *testing.T) {
	s, ana := newCandidateTestServer(t)
	saveV3Report(t, ana)
	// 合法 JSON 但超过 64 KiB（notes 超长）
	body := candidateBody(testUUID1, "x", map[string]any{"mode": "observe"})
	body["notes"] = strings.Repeat("n", 70<<10)
	req := httptest.NewRequest(http.MethodPost, "/api/factor-candidates", strings.NewReader(mustJSON(t, body)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("状态码 = %d, want 413，响应: %s", rec.Code, rec.Body.String())
	}
}

// mustJSON 测试辅助：序列化 map 为 JSON 字符串。
func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestServerCandidateExtraField 未知字段 → 400（拒绝多余 JSON）。
func TestServerCandidateExtraField(t *testing.T) {
	s, ana := newCandidateTestServer(t)
	saveV3Report(t, ana)
	body := candidateBody(testUUID1, "x", map[string]any{"mode": "observe"})
	body["extraField"] = 1
	doReq(t, s.Handler(), http.MethodPost, "/api/factor-candidates", body, http.StatusBadRequest)
}

// TestServerCandidateListArchived 默认隐藏归档；includeArchived=true 可见并可恢复。
func TestServerCandidateListArchived(t *testing.T) {
	s, ana := newCandidateTestServer(t)
	saveV3Report(t, ana)
	_, c := postCandidate(t, s.Handler(), candidateBody(testUUID1, "候选", map[string]any{"mode": "observe"}), http.StatusCreated)
	// 归档
	upd := map[string]any{"expectedRevision": 1, "name": "候选", "use": map[string]any{"mode": "observe"},
		"status": "archived"}
	doReq(t, s.Handler(), http.MethodPut, "/api/factor-candidates/"+c.ID, upd, http.StatusOK)
	// 默认隐藏
	res := doReq(t, s.Handler(), http.MethodGet, "/api/factor-candidates", nil, http.StatusOK)
	if strings.Contains(string(res), c.ID) {
		t.Fatalf("归档默认应隐藏: %s", res)
	}
	// includeArchived=true 可见
	res = doReq(t, s.Handler(), http.MethodGet, "/api/factor-candidates?includeArchived=true", nil, http.StatusOK)
	if !strings.Contains(string(res), c.ID) || !strings.Contains(string(res), "archived") {
		t.Fatalf("includeArchived 应显示归档: %s", res)
	}
	// 非法值 400
	doReq(t, s.Handler(), http.MethodGet, "/api/factor-candidates?includeArchived=yes", nil, http.StatusBadRequest)
	// 恢复
	upd["expectedRevision"] = 2
	upd["status"] = "candidate"
	doReq(t, s.Handler(), http.MethodPut, "/api/factor-candidates/"+c.ID, upd, http.StatusOK)
	res = doReq(t, s.Handler(), http.MethodGet, "/api/factor-candidates", nil, http.StatusOK)
	if !strings.Contains(string(res), c.ID) {
		t.Fatalf("恢复后应重新出现在活动列表: %s", res)
	}
}

// TestServerCandidateGetUpdateRevision 详情、更新、修订冲突。
func TestServerCandidateGetUpdateRevision(t *testing.T) {
	s, ana := newCandidateTestServer(t)
	saveV3Report(t, ana)
	_, c := postCandidate(t, s.Handler(), candidateBody(testUUID1, "原名", map[string]any{"mode": "observe"}), http.StatusCreated)

	// GET 详情
	res := doReq(t, s.Handler(), http.MethodGet, "/api/factor-candidates/"+c.ID, nil, http.StatusOK)
	var got struct {
		Candidate candResp `json:"candidate"`
	}
	if err := json.Unmarshal(res, &got); err != nil {
		t.Fatal(err)
	}
	if got.Candidate.ID != c.ID || got.Candidate.Revision != 1 {
		t.Fatalf("详情 = %+v", got.Candidate)
	}

	// 更新名称（正确 revision）
	upd := map[string]any{"expectedRevision": 1, "name": "新名称",
		"use": map[string]any{"mode": "observe"}, "status": "candidate"}
	res = doReq(t, s.Handler(), http.MethodPut, "/api/factor-candidates/"+c.ID, upd, http.StatusOK)
	if err := json.Unmarshal(res, &got); err != nil {
		t.Fatal(err)
	}
	if got.Candidate.Revision != 2 || got.Candidate.Name != "新名称" {
		t.Fatalf("更新结果 = %+v", got.Candidate)
	}

	// 错误 expectedRevision → 409
	upd["expectedRevision"] = 1
	res = doReq(t, s.Handler(), http.MethodPut, "/api/factor-candidates/"+c.ID, upd, http.StatusConflict)
	if !strings.Contains(string(res), "重新加载") {
		t.Fatalf("修订冲突响应 = %s", res)
	}

	// 未知候选 → 404
	doReq(t, s.Handler(), http.MethodPut, "/api/factor-candidates/fc_20260917T000000000Z_ffffffff", upd, http.StatusNotFound)
}

// TestServerCandidateVersionDrift 报告因子版本漂移 → 创建 409；
// 候选记录版本被篡改 → 兼容状态 stale。
func TestServerCandidateVersionDrift(t *testing.T) {
	s, ana := newCandidateTestServer(t)
	saveV3Report(t, ana)

	// 手工写入实现版本=99 的 v3 报告（模拟代码升级后的旧证据）
	id := "an_20260917T200000000Z_01020304"
	dir := filepath.Join(ana.root, "momentum", id)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	drifted := analysisReportForTest(id, "momentum", "2026-09-17T20:00:00+08:00")
	drifted.Factor.ImplementationVersion = 99
	buf, _ := json.Marshal(drifted)
	if err := os.WriteFile(filepath.Join(dir, "report.json"), buf, 0644); err != nil {
		t.Fatal(err)
	}
	body := candidateBody(testUUID1, "漂移", map[string]any{"mode": "observe"})
	body["analysisId"] = id
	res := doReq(t, s.Handler(), http.MethodPost, "/api/factor-candidates", body, http.StatusConflict)
	if !strings.Contains(string(res), "重新分析") {
		t.Fatalf("版本漂移响应 = %s", res)
	}

	// 篡改候选记录版本 → 详情 compatibility stale
	_, c := postCandidate(t, s.Handler(),
		candidateBody(testUUID2, "正常", map[string]any{"mode": "observe"}), http.StatusCreated)
	candPath := filepath.Join(candDirOf(t, s), c.ID, "revisions", "000001.json")
	var rec map[string]any
	raw, _ := os.ReadFile(candPath)
	if err := json.Unmarshal(raw, &rec); err != nil {
		t.Fatal(err)
	}
	factor := rec["factor"].(map[string]any)
	factor["implementationVersion"] = float64(99)
	raw2, _ := json.MarshalIndent(rec, "", "  ")
	if err := os.WriteFile(candPath, raw2, 0644); err != nil {
		t.Fatal(err)
	}
	res = doReq(t, s.Handler(), http.MethodGet, "/api/factor-candidates/"+c.ID, nil, http.StatusOK)
	var got struct {
		Candidate candResp `json:"candidate"`
	}
	if err := json.Unmarshal(res, &got); err != nil {
		t.Fatal(err)
	}
	if got.Candidate.Compatibility.State != "stale" {
		t.Fatalf("兼容状态应为 stale: %+v", got.Candidate.Compatibility)
	}
}

// candDirOf 候选 store 根目录。
func candDirOf(t *testing.T, s *Server) string {
	t.Helper()
	return s.candidateStore.root
}

// TestServerCandidateCorruptStore 证据损坏 → 列表 500 且错误体不泄露绝对路径。
func TestServerCandidateCorruptStore(t *testing.T) {
	s, ana := newCandidateTestServer(t)
	saveV3Report(t, ana)
	_, c := postCandidate(t, s.Handler(), candidateBody(testUUID1, "候选", map[string]any{"mode": "observe"}), http.StatusCreated)
	evPath := filepath.Join(candDirOf(t, s), c.ID, "revisions", "000001.analysis.json")
	if err := os.WriteFile(evPath, []byte("tampered"), 0644); err != nil {
		t.Fatal(err)
	}
	res := doReq(t, s.Handler(), http.MethodGet, "/api/factor-candidates", nil, http.StatusInternalServerError)
	body := string(res)
	if strings.Contains(body, s.candidateStore.root) || strings.Contains(body, `:\`) || strings.Contains(body, `/`) {
		t.Fatalf("错误体泄露本地路径: %s", body)
	}
	if !strings.Contains(body, "error") {
		t.Fatalf("错误响应缺 error 字段: %s", body)
	}
}
