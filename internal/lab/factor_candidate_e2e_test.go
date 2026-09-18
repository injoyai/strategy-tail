package lab

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	f "github.com/injoyai/strategy-tail/strategies/factor"
)

// waitServerDone 轮询 /api/status 直到 done/error（模板同 server_test.go）。
func waitServerDone(t *testing.T, h http.Handler, wantTask string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		res := doReq(t, h, http.MethodGet, "/api/status", nil, http.StatusOK)
		var st map[string]any
		if err := json.Unmarshal(res, &st); err != nil {
			t.Fatal(err)
		}
		if st["state"] == "done" || st["state"] == "error" {
			if st["state"] != "done" {
				t.Fatalf("任务失败: %v", st)
			}
			if wantTask != "" && st["task"] != wantTask {
				t.Fatalf("task = %v, want %s", st["task"], wantTask)
			}
			return st
		}
		if time.Now().After(deadline) {
			t.Fatalf("任务超时未完成: %v", st)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// e2eAnalysisRun 通过 API 运行一次 momentum 分析并返回 v3 报告。
func e2eAnalysisRun(t *testing.T, h http.Handler) AnalysisReport {
	t.Helper()
	doReq(t, h, http.MethodPost, "/api/analyze", map[string]any{
		"startYear": 2025, "endYear": 2025,
		"sampleMode": "codes", "sampleCodes": []string{"sh600001", "sh600002"},
		"kind": "momentum", "days": 2, "window": 1,
	}, http.StatusOK)
	waitServerDone(t, h, "analysis")
	res := doReq(t, h, http.MethodGet, "/api/analysis/latest", nil, http.StatusOK)
	var rep AnalysisReport
	if err := json.Unmarshal(res, &rep); err != nil {
		t.Fatal(err)
	}
	if rep.AnalysisVersion != 3 || rep.AnalysisID == "" || rep.Factor.ImplementationVersion <= 0 {
		t.Fatalf("分析报告应为 v3: version=%d id=%q impl=%d",
			rep.AnalysisVersion, rep.AnalysisID, rep.Factor.ImplementationVersion)
	}
	return rep
}

// writeE2EMomentumData 写入升降票动量数据（同 TestRunAnalysis）。
func writeE2EMomentumData(t *testing.T, dir string) {
	t.Helper()
	base := time.Date(2024, 12, 24, 0, 0, 0, 0, time.Local)
	up := append([]float64{9.5, 9.5, 9.5, 9.5, 9.5, 9.5, 9.5, 9.5},
		10, 10.2, 10.4, 10.6, 10.8, 11, 11.2, 11.4, 11.6, 11.8,
		12, 12.2, 12.4, 12.6, 12.8, 13, 13.2, 13.4, 13.6, 13.8)
	down := append([]float64{21, 21, 21, 21, 21, 21, 21, 21},
		20.8, 20.6, 20.4, 20.2, 20, 19.8, 19.6, 19.4, 19.2, 19,
		18.8, 18.6, 18.4, 18.2, 18, 17.8, 17.6, 17.4, 17.2, 17)
	writeDayDBCloses(t, dir, "sh600001", up, base)
	writeDayDBCloses(t, dir, "sh600002", down, base)
}

// TestCandidateFullChain 完整链路：分析 → v3 analysisId → 保存 range 候选 →
// 列表 → 转 FactorFilterSpec → 策略回测 → 报告保留 kind/days/factorVersion/阈值。
func TestCandidateFullChain(t *testing.T) {
	dir := t.TempDir()
	setupAnalysisData(t, dir)
	writeE2EMomentumData(t, dir)

	ana := NewAnalysisStore(filepath.Join(dir, "ana"))
	cand := NewCandidateStore(filepath.Join(dir, "cand"))
	s := newServerWithStores(ana, cand)
	h := s.Handler()

	// 1) 运行 momentum 分析 → v3
	rep := e2eAnalysisRun(t, h)

	// 2) 保存 range 候选（原始比例 0.05）
	body := map[string]any{
		"requestId":  testUUID1,
		"analysisId": rep.AnalysisID,
		"name":       "20日动量至少5%",
		"use": map[string]any{"mode": "range", "filter": map[string]any{
			"kind": "momentum", "days": 2, "factorVersion": rep.Factor.ImplementationVersion,
			"operator": "gte", "min": 0.05}},
	}
	res := doReq(t, h, http.MethodPost, "/api/factor-candidates", body, http.StatusCreated)
	var created struct {
		Candidate candResp `json:"candidate"`
	}
	if err := json.Unmarshal(res, &created); err != nil {
		t.Fatal(err)
	}
	if created.Candidate.Compatibility.State != "ready" {
		t.Fatalf("候选兼容状态 = %+v", created.Candidate.Compatibility)
	}

	// 3) 列表可查
	res = doReq(t, h, http.MethodGet, "/api/factor-candidates", nil, http.StatusOK)
	if !strings.Contains(string(res), created.Candidate.ID) {
		t.Fatalf("候选列表缺少候选: %s", res)
	}

	// 4) 候选 Filter 转 StrategySpec 并运行
	flt := created.Candidate.Use.Filter
	spec := map[string]any{
		"version": 1,
		"factorFilters": []map[string]any{
			{"kind": flt.Kind, "days": flt.Days, "factorVersion": flt.FactorVersion,
				"operator": flt.Operator, "min": *flt.Min},
		},
		"exit": map[string]any{"holdingDays": 1},
		"run": map[string]any{
			"startYear": 2025, "endYear": 2025,
			"sampleMode": "codes", "sampleCodes": []string{"sh600001", "sh600002"},
		},
	}
	doReq(t, h, http.MethodPost, "/api/strategy/run", spec, http.StatusOK)
	waitServerDone(t, h, "backtest")

	// 5) 报告 strategySpec 保留 kind/days/factorVersion/阈值
	res = doReq(t, h, http.MethodGet, "/api/report/latest", nil, http.StatusOK)
	var report Report
	if err := json.Unmarshal(res, &report); err != nil {
		t.Fatal(err)
	}
	if report.StrategySpec == nil || len(report.StrategySpec.FactorFilters) != 1 {
		t.Fatalf("报告缺 StrategySpec: %+v", report.StrategySpec)
	}
	got := report.StrategySpec.FactorFilters[0]
	if got.Kind != "momentum" || got.Days != 2 || got.FactorVersion != 1 ||
		got.Operator != "gte" || got.Min == nil || *got.Min != 0.05 {
		t.Fatalf("报告保留的过滤条件异常: %+v", got)
	}
}

// TestCandidateHistoryAndRestart 历史不覆盖 + 重启恢复：
// 两次分析分别可读、latest 指向第二次、候选绑定第一次、兼容镜像指向第二次。
func TestCandidateHistoryAndRestart(t *testing.T) {
	dir := t.TempDir()
	setupAnalysisData(t, dir)
	writeE2EMomentumData(t, dir)

	ana := NewAnalysisStore(filepath.Join(dir, "ana"))
	cand := NewCandidateStore(filepath.Join(dir, "cand"))
	s := newServerWithStores(ana, cand)
	h := s.Handler()

	// 两次同 kind 分析
	rep1 := e2eAnalysisRun(t, h)
	rep2 := e2eAnalysisRun(t, h)
	if rep1.AnalysisID == rep2.AnalysisID {
		t.Fatal("两次分析 analysisId 应不同")
	}

	// 保存第一次为候选
	body := map[string]any{
		"requestId": testUUID1, "analysisId": rep1.AnalysisID, "name": "绑定第一份",
		"use": map[string]any{"mode": "observe"},
	}
	res := doReq(t, h, http.MethodPost, "/api/factor-candidates", body, http.StatusCreated)
	var created struct {
		Candidate candResp `json:"candidate"`
	}
	if err := json.Unmarshal(res, &created); err != nil {
		t.Fatal(err)
	}
	hash1 := created.Candidate.Evidence.ReportSHA256

	// 重建 Server（等价重启）：两个 Store 指向同一根
	s2 := newServerWithStores(NewAnalysisStore(filepath.Join(dir, "ana")),
		NewCandidateStore(filepath.Join(dir, "cand")))
	h2 := s2.Handler()

	// 两份分析均可按 ID 读取
	doReq(t, h2, http.MethodGet, "/api/analysis/"+rep1.AnalysisID, nil, http.StatusOK)
	doReq(t, h2, http.MethodGet, "/api/analysis/"+rep2.AnalysisID, nil, http.StatusOK)
	// latest 指向第二份
	res = doReq(t, h2, http.MethodGet, "/api/analysis/latest", nil, http.StatusOK)
	var latest AnalysisReport
	if err := json.Unmarshal(res, &latest); err != nil {
		t.Fatal(err)
	}
	if latest.AnalysisID != rep2.AnalysisID {
		t.Fatalf("latest 应指向第二份: %q", latest.AnalysisID)
	}
	// 候选仍绑定第一份且哈希一致
	res = doReq(t, h2, http.MethodGet, "/api/factor-candidates/"+created.Candidate.ID, nil, http.StatusOK)
	var got struct {
		Candidate candResp `json:"candidate"`
	}
	if err := json.Unmarshal(res, &got); err != nil {
		t.Fatal(err)
	}
	if got.Candidate.Evidence.AnalysisID != rep1.AnalysisID ||
		got.Candidate.Evidence.ReportSHA256 != hash1 {
		t.Fatalf("候选证据绑定漂移: %+v", got.Candidate.Evidence)
	}
	// 候选列表可恢复
	res = doReq(t, h2, http.MethodGet, "/api/factor-candidates", nil, http.StatusOK)
	if !strings.Contains(string(res), created.Candidate.ID) {
		t.Fatalf("重启后候选列表缺失: %s", res)
	}
	// 兼容镜像指向第二份
	mirror, err := os.ReadFile(filepath.Join(dir, "ana", "momentum", "report.json"))
	if err != nil {
		t.Fatal(err)
	}
	var mirrorRep AnalysisReport
	if err := json.Unmarshal(mirror, &mirrorRep); err != nil {
		t.Fatal(err)
	}
	if mirrorRep.AnalysisID != rep2.AnalysisID {
		t.Fatalf("兼容镜像应指向第二份: %q", mirrorRep.AnalysisID)
	}
}

// TestCandidateLegacyCompatibility 旧合同兼容：
// v2 报告可展示不可保存；无 factorVersion 的旧策略仍运行；候选严格策略版本漂移拒绝。
func TestCandidateLegacyCompatibility(t *testing.T) {
	dir := t.TempDir()
	setupAnalysisData(t, dir)
	writeE2EMomentumData(t, dir)

	ana := NewAnalysisStore(filepath.Join(dir, "ana"))
	cand := NewCandidateStore(filepath.Join(dir, "cand"))
	s := newServerWithStores(ana, cand)
	h := s.Handler()

	// v2 fixture：合法 ID 目录 + 无 analysisId/版本 的报告
	v2id := "an_20260917T100000000Z_11223344"
	rdir := filepath.Join(dir, "ana", "momentum", v2id)
	if err := os.MkdirAll(rdir, 0755); err != nil {
		t.Fatal(err)
	}
	v2 := analysisReportForTest("", "momentum", "2026-09-17T10:00:00+08:00")
	v2.AnalysisVersion = 2
	buf, _ := json.Marshal(v2)
	if err := os.WriteFile(filepath.Join(rdir, "report.json"), buf, 0644); err != nil {
		t.Fatal(err)
	}
	// 可展示
	doReq(t, h, http.MethodGet, "/api/analysis/"+v2id, nil, http.StatusOK)
	// 不可保存为候选 → 409
	res := doReq(t, h, http.MethodPost, "/api/factor-candidates", map[string]any{
		"requestId": testUUID1, "analysisId": v2id, "name": "旧报告",
		"use": map[string]any{"mode": "observe"},
	}, http.StatusConflict)
	if !strings.Contains(string(res), "重新运行") {
		t.Fatalf("v2 拒绝响应 = %s", res)
	}

	// 无 factorVersion 的旧策略仍运行
	legacySpec := map[string]any{
		"version": 1,
		"factorFilters": []map[string]any{
			{"kind": "momentum", "days": 2, "operator": "gte", "min": 0.05},
		},
		"exit": map[string]any{"holdingDays": 1},
		"run": map[string]any{"startYear": 2025, "endYear": 2025,
			"sampleMode": "codes", "sampleCodes": []string{"sh600001", "sh600002"}},
	}
	doReq(t, h, http.MethodPost, "/api/strategy/run", legacySpec, http.StatusOK)
	waitServerDone(t, h, "backtest")

	// 候选严格策略：版本相同运行，版本漂移拒绝
	cur, _ := f.Catalog("momentum")
	strictOK := map[string]any{
		"version": 1,
		"factorFilters": []map[string]any{
			{"kind": "momentum", "days": 2, "factorVersion": cur.ImplementationVersion,
				"operator": "gte", "min": 0.05},
		},
		"exit": map[string]any{"holdingDays": 1},
		"run": map[string]any{"startYear": 2025, "endYear": 2025,
			"sampleMode": "codes", "sampleCodes": []string{"sh600001", "sh600002"}},
	}
	doReq(t, h, http.MethodPost, "/api/strategy/run", strictOK, http.StatusOK)
	waitServerDone(t, h, "backtest")
	// 版本漂移：factorVersion 不匹配当前注册版本 → 400
	drift := map[string]any{
		"version": 1,
		"factorFilters": []map[string]any{
			{"kind": "momentum", "days": 2, "factorVersion": cur.ImplementationVersion + 1,
				"operator": "gte", "min": 0.05},
		},
		"exit": map[string]any{"holdingDays": 1},
		"run": map[string]any{"startYear": 2025, "endYear": 2025,
			"sampleMode": "codes", "sampleCodes": []string{"sh600001"}},
	}
	res = doReq(t, h, http.MethodPost, "/api/strategy/run", drift, http.StatusBadRequest)
	if !strings.Contains(string(res), "版本") {
		t.Fatalf("版本漂移拒绝响应 = %s", res)
	}
}

// TestCandidateE2EBoundaries 损坏与边界：
// 证据篡改失败、.tmp 忽略、非法 ID 400、并发同版本仅一个成功、精确重复同 ID。
func TestCandidateE2EBoundaries(t *testing.T) {
	dir := t.TempDir()
	setupAnalysisData(t, dir)
	writeE2EMomentumData(t, dir)

	ana := NewAnalysisStore(filepath.Join(dir, "ana"))
	cand := NewCandidateStore(filepath.Join(dir, "cand"))
	s := newServerWithStores(ana, cand)
	h := s.Handler()
	rep := e2eAnalysisRun(t, h)

	create := func(uuid string) candResp {
		t.Helper()
		res := doReq(t, h, http.MethodPost, "/api/factor-candidates", map[string]any{
			"requestId": uuid, "analysisId": rep.AnalysisID, "name": "候选" + uuid[:4],
			"use": map[string]any{"mode": "observe"},
		}, http.StatusCreated)
		var out struct {
			Candidate candResp `json:"candidate"`
		}
		if err := json.Unmarshal(res, &out); err != nil {
			t.Fatal(err)
		}
		return out.Candidate
	}

	// 精确重复创建 → 同一 ID（幂等）
	c1 := create(testUUID1)
	res := doReq(t, h, http.MethodPost, "/api/factor-candidates", map[string]any{
		"requestId": testUUID1, "analysisId": rep.AnalysisID, "name": "候选" + testUUID1[:4],
		"use": map[string]any{"mode": "observe"},
	}, http.StatusOK)
	var dup struct {
		Candidate candResp `json:"candidate"`
	}
	if err := json.Unmarshal(res, &dup); err != nil {
		t.Fatal(err)
	}
	if dup.Candidate.ID != c1.ID {
		t.Fatalf("精确重复应同一 ID: %s vs %s", dup.Candidate.ID, c1.ID)
	}

	// 篡改证据一个字节 → 候选读取失败（列表 500）
	evPath := filepath.Join(cand.root, c1.ID, "revisions", "000001.analysis.json")
	orig, _ := os.ReadFile(evPath)
	tampered := append([]byte(nil), orig...)
	tampered[0] ^= 0xff
	if err := os.WriteFile(evPath, tampered, 0644); err != nil {
		t.Fatal(err)
	}
	doReq(t, h, http.MethodGet, "/api/factor-candidates", nil, http.StatusInternalServerError)
	doReq(t, h, http.MethodGet, "/api/factor-candidates/"+c1.ID, nil, http.StatusBadRequest)

	// 恢复证据字节，制造 .tmp 残留 → 列表正常且忽略
	if err := os.WriteFile(evPath, orig, 0644); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(cand.root, c1.ID, "revisions", ".tmp-000002.json"), []byte("{bad"), 0644)
	res = doReq(t, h, http.MethodGet, "/api/factor-candidates", nil, http.StatusOK)
	if !strings.Contains(string(res), c1.ID) {
		t.Fatalf(".tmp 不应影响列表: %s", res)
	}

	// 非法 ID / 穿越 → 400（候选创建 analysisId 非法、GET 格式非法 ID）
	doReq(t, h, http.MethodPost, "/api/factor-candidates", map[string]any{
		"requestId": testUUID1, "analysisId": "../evil", "name": "x",
		"use": map[string]any{"mode": "observe"},
	}, http.StatusBadRequest)
	doReq(t, h, http.MethodGet, "/api/factor-candidates/fc_bad", nil, http.StatusBadRequest)

	// 并发两个相同 expectedRevision 更新 → 只允许一个成功
	c2 := create(testUUID2)
	updBody := mustJSON(t, map[string]any{"expectedRevision": 1, "name": "并发",
		"use": map[string]any{"mode": "observe"}, "status": "candidate"})
	var wg sync.WaitGroup
	codes := make([]int, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodPut, "/api/factor-candidates/"+c2.ID, strings.NewReader(updBody))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			codes[i] = rec.Code
		}(i)
	}
	wg.Wait()
	okCount := 0
	for _, c := range codes {
		if c == http.StatusOK {
			okCount++
		}
	}
	if okCount != 1 {
		t.Fatalf("并发同版本更新应恰有一个成功: %v", codes)
	}
}
