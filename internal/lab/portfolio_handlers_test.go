package lab

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	common "github.com/injoyai/strategy-tail"
	"github.com/injoyai/strategy-tail/internal/portfolioresearch"
	"github.com/injoyai/strategy-tail/lib/extend"
	"github.com/injoyai/strategy-tail/researchdata"
)

// portfolio_handlers_test.go v2 Task 9 HTTP API 测试（设计 §12.2/§14/§15）：
// model/experiment/validation 创建·列表·详情、非法验证（缺模型引用/hash
// 不匹配/窗口非法）、证据降级（exploratory 创建成功但不能 passed）、幂等
// 重放、忙碌冲突 409、运行生命周期（accepted→done）、取消、重启恢复、
// 分页/排序、路径穿越、旧客户端兼容。handler 只做参数解析/校验/调用 Store
// 与 Runner，不含统计或交易核心逻辑（统计在 portfolioresearch/lab 领域层）。

// portfolioTestEnv 测试环境：真实 v1 验证链 + 各组合存储 + 注入静态股票池
// 与伪造日K 的 Server；记录全部根目录以支持"等价重启"。
type portfolioTestEnv struct {
	s            *Server
	validationID string
	modelRoot    string
	expRecords   string
	expArtifacts string
	pvRoot       string
	v1Root       string
}

// newPortfolioTestEnv 构造完整可运行的组合 API 测试环境（时钟单调递增，
// 保证列表排序确定性）。
func newPortfolioTestEnv(t *testing.T) *portfolioTestEnv {
	t.Helper()
	dir := t.TempDir()
	t.Chdir(dir)
	old := common.Pull
	common.Pull = &extend.PullKline{Config: extend.PullKlineConfig{Dir: dir}}
	t.Cleanup(func() { common.Pull = old })
	writePortfolioMomentumData(t, dir)

	vf := newValidationStoreFixture(t)
	vr, _, err := vf.Store.Create(validCreateReq(vf, testUUID1), vf.deps())
	if err != nil {
		t.Fatalf("冻结 v1 验证: %v", err)
	}

	modelRoot := t.TempDir()
	expRecords, expArtifacts := t.TempDir(), t.TempDir()
	pvRoot := t.TempDir()
	models := NewFactorModelStore(modelRoot)
	now := time.Date(2026, 9, 18, 0, 0, 0, 0, time.UTC)
	exps := NewPortfolioExperimentStore(expRecords, expArtifacts)
	exps.now = func() time.Time { now = now.Add(time.Second); return now }
	pvs := NewPortfolioValidationStore(pvRoot)

	r := NewRunner()
	r.universe = researchdata.NewStaticUniverse(researchdata.StaticUniverseConfig{
		ID: "test-static", Source: "test", Codes: func() []string {
			return []string{"sh600001", "sh600002"}
		},
	})
	r.ConfigurePortfolio(models, exps, pvs)

	s := &Server{
		runner:          r,
		portfolioRunner: r,
		mux:             http.NewServeMux(),
		analysisStore:   NewAnalysisStore(t.TempDir()),
		candidateStore:  NewCandidateStore(t.TempDir()),
		modelStore:      models,
		experimentStore: exps,
		validationStore: pvs,
		validationInput: NewValidationStoreAdapter(vf.Store),
	}
	s.registerRoutes()
	return &portfolioTestEnv{
		s: s, validationID: vr.ID, modelRoot: modelRoot,
		expRecords: expRecords, expArtifacts: expArtifacts, pvRoot: pvRoot,
		v1Root: vf.Store.root,
	}
}

// rebuild 等价重启：同一根目录新建全部存储与 Runner（v1 验证链复用原根）。
func (e *portfolioTestEnv) rebuild(t *testing.T) *Server {
	t.Helper()
	models := NewFactorModelStore(e.modelRoot)
	exps := NewPortfolioExperimentStore(e.expRecords, e.expArtifacts)
	pvs := NewPortfolioValidationStore(e.pvRoot)
	r := NewRunner()
	r.universe = researchdata.NewStaticUniverse(researchdata.StaticUniverseConfig{
		ID: "test-static", Source: "test", Codes: func() []string {
			return []string{"sh600001", "sh600002"}
		},
	})
	r.ConfigurePortfolio(models, exps, pvs)
	s := &Server{
		runner:          r,
		portfolioRunner: r,
		mux:             http.NewServeMux(),
		analysisStore:   NewAnalysisStore(t.TempDir()),
		candidateStore:  NewCandidateStore(t.TempDir()),
		modelStore:      models,
		experimentStore: exps,
		validationStore: pvs,
		validationInput: NewValidationStoreAdapter(NewValidationStore(e.v1Root)),
	}
	s.registerRoutes()
	return s
}

// createModelBody 指向给定 v1 验证的合法模型请求体。
func createModelBody(validationID, requestID string) map[string]any {
	return map[string]any{
		"requestId":         requestID,
		"createdBy":         "tester",
		"researchQuestion":  "动量组合是否稳定？",
		"hypothesis":        "等权秩合成能提升样本外稳定性",
		"factorValidations": []string{validationID},
		"transformPipeline": map[string]any{
			"missing":     "exclude",
			"winsorize":   map[string]any{"mode": "quantile", "quantile": 0.01},
			"neutralize":  map[string]any{"mode": "none"},
			"standardize": "rank",
		},
		"combination": map[string]any{"method": "equal_weight_rank"},
		"portfolioPolicy": map[string]any{
			"selection": "top_n", "topN": 20, "cashBuffer": 0.05,
		},
		"execution": map[string]any{
			"rebalance": "daily", "fillAt": "next_open", "sellFirst": true,
			"t1Restriction": true, "lotSize": 100,
			"cost": map[string]any{"commissionRate": 0.0003, "stampDutyRate": 0.001,
				"slippage": 0.01, "minCommission": 5},
		},
		"benchmark": map[string]any{"id": "hs300"},
	}
}

// createModelViaAPI 创建模型并返回记录。
func createModelViaAPI(t *testing.T, h http.Handler, validationID, requestID string) portfolioresearch.FactorModel {
	t.Helper()
	res := doReq(t, h, http.MethodPost, "/api/factor-models", createModelBody(validationID, requestID), http.StatusCreated)
	var out struct {
		Created bool                          `json:"created"`
		Model   portfolioresearch.FactorModel `json:"model"`
	}
	if err := json.Unmarshal(res, &out); err != nil {
		t.Fatal(err)
	}
	if !out.Created || out.Model.ModelID == "" || out.Model.ModelHash == "" {
		t.Fatalf("创建模型响应异常: %s", res)
	}
	return out.Model
}

// experimentBody 指向给定模型的合法实验请求体。
func experimentBody(m portfolioresearch.FactorModel, requestID string) map[string]any {
	return map[string]any{
		"requestId":     requestID,
		"familyId":      "portfolio-api-test",
		"modelId":       m.ModelID,
		"modelRevision": m.Revision,
		"modelHash":     m.ModelHash,
		"studyRange":    map[string]any{"start": "2025-01-01", "end": "2025-01-20"},
		"variant":       map[string]any{"name": "top2-equal", "desc": "测试变体"},
		"variantSource": "declared",
		"dataSnapshot": map[string]any{
			"universeMode": "historical_membership", "priceSource": "local-klines",
			"priceVersion": "2026-01", "pitState": "verified",
		},
		"codeVersion":   "v2-task9-test",
		"evidenceClass": m.EvidenceClass,
		"trialCounted":  true,
		"countReason":   "测试变体，计入试验次数",
	}
}

// validationBody 指向给定模型的合法验证请求体。
func validationBody(m portfolioresearch.FactorModel, requestID string) map[string]any {
	return map[string]any{
		"requestId": requestID,
		"spec": map[string]any{
			"modelRef":   map[string]any{"modelId": m.ModelID, "revision": m.Revision, "hash": m.ModelHash},
			"windowRule": map[string]any{"trainDays": 5, "testDays": 5, "step": 5},
			"gates":      map[string]any{"minValidWindows": 1, "minTradingDays": 10, "minNetReturn": 0.01},
			"benchmark":  map[string]any{"id": "hs300"},
		},
	}
}

// waitStatus 轮询 /api/status 到 done/error。
func waitStatus(t *testing.T, h http.Handler) map[string]any {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for {
		res := doReq(t, h, http.MethodGet, "/api/status", nil, http.StatusOK)
		var st map[string]any
		if err := json.Unmarshal(res, &st); err != nil {
			t.Fatal(err)
		}
		if st["state"] == "done" || st["state"] == "error" {
			return st
		}
		if time.Now().After(deadline) {
			t.Fatalf("任务超时未完成: %v", st)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// TestPortfolioModelHandlers 模型 API：创建 201、幂等 200、列表、详情（含
// revision）、非法上游验证 400、幂等键复用不同内容 409。
func TestPortfolioModelHandlers(t *testing.T) {
	env := newPortfolioTestEnv(t)
	h := env.s.Handler()
	m := createModelViaAPI(t, h, env.validationID, testUUID1)

	// 幂等重放：同 RequestID 同内容 → 200 且返回同一模型。
	res := doReq(t, h, http.MethodPost, "/api/factor-models", createModelBody(env.validationID, testUUID1), http.StatusOK)
	var out struct {
		Created bool                          `json:"created"`
		Model   portfolioresearch.FactorModel `json:"model"`
	}
	if err := json.Unmarshal(res, &out); err != nil {
		t.Fatal(err)
	}
	if out.Created || out.Model.ModelID != m.ModelID {
		t.Fatalf("幂等重放应返回已有记录: %s", res)
	}

	// 列表包含模型。
	res = doReq(t, h, http.MethodGet, "/api/factor-models", nil, http.StatusOK)
	if !bytes.Contains(res, []byte(m.ModelID)) {
		t.Fatalf("模型列表缺少模型: %s", res)
	}
	// 详情（最新 revision）与按 revision 读取。
	doReq(t, h, http.MethodGet, "/api/factor-models/"+m.ModelID, nil, http.StatusOK)
	doReq(t, h, http.MethodGet, "/api/factor-models/"+m.ModelID+"?revision=1", nil, http.StatusOK)
	// 不存在的模型 → 404。
	doReq(t, h, http.MethodGet, "/api/factor-models/fm_20260917T150100000Z_00000000", nil, http.StatusNotFound)
	// 非法上游验证 ID → 400。
	bad := createModelBody(env.validationID, testUUID2)
	bad["factorValidations"] = []string{"../../evil"}
	doReq(t, h, http.MethodPost, "/api/factor-models", bad, http.StatusBadRequest)
	// 幂等键复用不同内容 → 409。
	conflict := createModelBody(env.validationID, testUUID1)
	conflict["portfolioPolicy"] = map[string]any{"selection": "top_n", "topN": 30}
	doReq(t, h, http.MethodPost, "/api/factor-models", conflict, http.StatusConflict)
}

// TestPortfolioModelPagination 模型列表服务端分页（Task 10 规格缺口修复）：
// 无分页参数旧客户端兼容（models 全量 + total）、总数/页大小/越界页/稳定
// 排序、非法分页参数 400、只带单参数时缺省补全。
func TestPortfolioModelPagination(t *testing.T) {
	env := newPortfolioTestEnv(t)
	h := env.s.Handler()
	// 创建 3 个模型（不同 requestId → 不同模型）。
	for _, rid := range []string{testUUID1, testUUID2, "9b2f1c3d-4e5f-4a7b-8c9d-0e1f2a3b4c5e"} {
		createModelViaAPI(t, h, env.validationID, rid)
	}

	// 旧客户端兼容：不带分页参数 → models 全量 + total。
	res := doReq(t, h, http.MethodGet, "/api/factor-models", nil, http.StatusOK)
	var all struct {
		Models []portfolioresearch.FactorModel `json:"models"`
		Items  []portfolioresearch.FactorModel `json:"items"`
		Total  int                             `json:"total"`
	}
	if err := json.Unmarshal(res, &all); err != nil {
		t.Fatal(err)
	}
	if len(all.Models) != 3 || len(all.Items) != 3 || all.Total != 3 {
		t.Fatalf("无分页参数应返回全量 3 条 + total=3, got models=%d items=%d total=%d",
			len(all.Models), len(all.Items), all.Total)
	}

	type pageResp struct {
		Models   []portfolioresearch.FactorModel `json:"models"`
		Items    []portfolioresearch.FactorModel `json:"items"`
		Total    int                             `json:"total"`
		Page     int                             `json:"page"`
		PageSize int                             `json:"pageSize"`
	}
	// 分页：page=1&pageSize=2 → 2 条；page=2 → 1 条。
	res = doReq(t, h, http.MethodGet, "/api/factor-models?page=1&pageSize=2", nil, http.StatusOK)
	var p1 pageResp
	if err := json.Unmarshal(res, &p1); err != nil {
		t.Fatal(err)
	}
	if p1.Total != 3 || len(p1.Items) != 2 || p1.Page != 1 || p1.PageSize != 2 {
		t.Fatalf("第 1 页 total/items/page/pageSize = %d/%d/%d/%d, want 3/2/1/2",
			p1.Total, len(p1.Items), p1.Page, p1.PageSize)
	}
	// models 兼容字段与 items 一致。
	if len(p1.Models) != 2 || p1.Models[0].ModelID != p1.Items[0].ModelID {
		t.Fatalf("分页响应 models 应与 items 一致")
	}
	res = doReq(t, h, http.MethodGet, "/api/factor-models?page=2&pageSize=2", nil, http.StatusOK)
	var p2 pageResp
	if err := json.Unmarshal(res, &p2); err != nil {
		t.Fatal(err)
	}
	if len(p2.Items) != 1 {
		t.Fatalf("第 2 页 items = %d, want 1", len(p2.Items))
	}
	// 稳定排序：分页合并后与全量列表逐位一致（无重复无遗漏）。
	combined := append(append([]portfolioresearch.FactorModel{}, p1.Items...), p2.Items...)
	if len(combined) != len(all.Models) {
		t.Fatalf("分页合并 %d != 全量 %d", len(combined), len(all.Models))
	}
	for i := range combined {
		if combined[i].ModelID != all.Models[i].ModelID {
			t.Fatalf("分页顺序与全量不一致: 第 %d 个 %s != %s", i, combined[i].ModelID, all.Models[i].ModelID)
		}
	}

	// 越界页：空 items + 真实 total（页码可恢复）。
	res = doReq(t, h, http.MethodGet, "/api/factor-models?page=3&pageSize=2", nil, http.StatusOK)
	var p3 pageResp
	if err := json.Unmarshal(res, &p3); err != nil {
		t.Fatal(err)
	}
	if len(p3.Items) != 0 || p3.Total != 3 || p3.Page != 3 {
		t.Fatalf("越界页 items/total/page = %d/%d/%d, want 0/3/3", len(p3.Items), p3.Total, p3.Page)
	}

	// 非法分页参数 → 400（与实验/验证列表同款校验）。
	doReq(t, h, http.MethodGet, "/api/factor-models?page=0", nil, http.StatusBadRequest)
	doReq(t, h, http.MethodGet, "/api/factor-models?pageSize=9999", nil, http.StatusBadRequest)
	// 只带单参数时缺省补全：pageSize 指定 → page 缺省 1；page 指定 → pageSize 缺省 20。
	res = doReq(t, h, http.MethodGet, "/api/factor-models?pageSize=2", nil, http.StatusOK)
	var ps pageResp
	if err := json.Unmarshal(res, &ps); err != nil {
		t.Fatal(err)
	}
	if ps.Page != 1 || ps.PageSize != 2 || ps.Total != 3 {
		t.Fatalf("只带 pageSize: page/pageSize/total = %d/%d/%d, want 1/2/3", ps.Page, ps.PageSize, ps.Total)
	}
}

// TestPortfolioExperimentHandlers 实验 API：创建 201、幂等 200、列表分页/
// 稳定排序、详情、非法输入 400。
func TestPortfolioExperimentHandlers(t *testing.T) {
	env := newPortfolioTestEnv(t)
	h := env.s.Handler()
	m := createModelViaAPI(t, h, env.validationID, testUUID1)

	// 创建三个实验（时钟单调 → 排序确定）。
	var ids []string
	for i, rid := range []string{testUUID1, testUUID2, "9b2f1c3d-4e5f-4a7b-8c9d-0e1f2a3b4c5e"} {
		res := doReq(t, h, http.MethodPost, "/api/portfolio-experiments", experimentBody(m, rid), http.StatusCreated)
		var out struct {
			Experiment PortfolioExperiment `json:"experiment"`
		}
		if err := json.Unmarshal(res, &out); err != nil {
			t.Fatal(err)
		}
		if i == 0 && out.Experiment.Status != portfolioresearch.RunStateQueued {
			t.Fatalf("创建后状态应为 queued: %q", out.Experiment.Status)
		}
		ids = append(ids, out.Experiment.ExperimentID)
	}

	// 幂等重放 → 200 同一实验。
	doReq(t, h, http.MethodPost, "/api/portfolio-experiments", experimentBody(m, testUUID1), http.StatusOK)

	// 分页：page=1 size=2 两条、page=2 一条、total=3，无重复无遗漏。
	res := doReq(t, h, http.MethodGet, "/api/portfolio-experiments?page=1&pageSize=2", nil, http.StatusOK)
	var page1 struct {
		Experiments ExperimentPage `json:"experiments"`
	}
	if err := json.Unmarshal(res, &page1); err != nil {
		t.Fatal(err)
	}
	if page1.Experiments.Total != 3 || len(page1.Experiments.Items) != 2 {
		t.Fatalf("第 1 页 = total %d / items %d, want 3/2", page1.Experiments.Total, len(page1.Experiments.Items))
	}
	res = doReq(t, h, http.MethodGet, "/api/portfolio-experiments?page=2&pageSize=2", nil, http.StatusOK)
	var page2 struct {
		Experiments ExperimentPage `json:"experiments"`
	}
	if err := json.Unmarshal(res, &page2); err != nil {
		t.Fatal(err)
	}
	if len(page2.Experiments.Items) != 1 {
		t.Fatalf("第 2 页 items = %d, want 1", len(page2.Experiments.Items))
	}
	// 稳定排序：CreatedAt 倒序（第 1 页两条 = 第 2、3 次创建）。
	if page1.Experiments.Items[0].CreatedAt < page1.Experiments.Items[1].CreatedAt {
		t.Fatalf("列表应 CreatedAt 倒序: %s < %s",
			page1.Experiments.Items[0].CreatedAt, page1.Experiments.Items[1].CreatedAt)
	}
	// 分页无重复无遗漏。
	seen := map[string]bool{}
	for _, it := range append(page1.Experiments.Items, page2.Experiments.Items...) {
		if seen[it.ExperimentID] {
			t.Fatalf("分页出现重复: %s", it.ExperimentID)
		}
		seen[it.ExperimentID] = true
	}
	for _, id := range ids {
		if !seen[id] {
			t.Fatalf("分页遗漏实验: %s", id)
		}
	}

	// 详情可读；非法 page/pageSize → 400。
	doReq(t, h, http.MethodGet, "/api/portfolio-experiments/"+ids[0], nil, http.StatusOK)
	doReq(t, h, http.MethodGet, "/api/portfolio-experiments?page=0", nil, http.StatusBadRequest)
	doReq(t, h, http.MethodGet, "/api/portfolio-experiments?pageSize=9999", nil, http.StatusBadRequest)
	// 不存在 → 404。
	doReq(t, h, http.MethodGet, "/api/portfolio-experiments/pe_20260918T000000000Z_00000000", nil, http.StatusNotFound)
}

// TestPortfolioValidationHandlers 验证 API：创建 201、列表/详情、非法窗口
// 400、缺模型引用 400、模型不存在 404、hash 不匹配 409、幂等 200。
func TestPortfolioValidationHandlers(t *testing.T) {
	env := newPortfolioTestEnv(t)
	h := env.s.Handler()
	m := createModelViaAPI(t, h, env.validationID, testUUID1)

	res := doReq(t, h, http.MethodPost, "/api/portfolio-validations", validationBody(m, testUUID1), http.StatusCreated)
	var out struct {
		Validation PortfolioValidationRecord `json:"validation"`
	}
	if err := json.Unmarshal(res, &out); err != nil {
		t.Fatal(err)
	}
	if out.Validation.ModelEvidenceClass != portfolioresearch.EvidenceRetrospective {
		t.Fatalf("ModelEvidenceClass = %q, want retrospective", out.Validation.ModelEvidenceClass)
	}
	// 幂等 → 200。
	doReq(t, h, http.MethodPost, "/api/portfolio-validations", validationBody(m, testUUID1), http.StatusOK)
	// 详情与列表。
	doReq(t, h, http.MethodGet, "/api/portfolio-validations/"+out.Validation.ID, nil, http.StatusOK)
	res = doReq(t, h, http.MethodGet, "/api/portfolio-validations?page=1&pageSize=20", nil, http.StatusOK)
	if !bytes.Contains(res, []byte(out.Validation.ID)) {
		t.Fatalf("验证列表缺少记录: %s", res)
	}

	// 非法窗口（step < testDays → 窗口重叠）→ 400。
	bad := validationBody(m, testUUID2)
	bad["spec"].(map[string]any)["windowRule"] = map[string]any{"trainDays": 5, "testDays": 10, "step": 5}
	doReq(t, h, http.MethodPost, "/api/portfolio-validations", bad, http.StatusBadRequest)

	// 缺模型引用（modelId 空）→ 400。
	missingRef := validationBody(m, testUUID2)
	missingRef["spec"].(map[string]any)["modelRef"] = map[string]any{"revision": 1, "hash": strings.Repeat("a", 64)}
	doReq(t, h, http.MethodPost, "/api/portfolio-validations", missingRef, http.StatusBadRequest)

	// 模型不存在 → 404。
	notFound := validationBody(m, testUUID2)
	notFound["spec"].(map[string]any)["modelRef"] = map[string]any{
		"modelId": "fm_20260917T150100000Z_00000000", "revision": 1, "hash": strings.Repeat("a", 64)}
	doReq(t, h, http.MethodPost, "/api/portfolio-validations", notFound, http.StatusNotFound)

	// hash 不匹配 → 409（fail closed，须以模型存储为准）。
	hashBad := validationBody(m, testUUID2)
	hashBad["spec"].(map[string]any)["modelRef"] = map[string]any{
		"modelId": m.ModelID, "revision": m.Revision, "hash": strings.Repeat("b", 64)}
	doReq(t, h, http.MethodPost, "/api/portfolio-validations", hashBad, http.StatusConflict)
}

// TestPortfolioEvidenceDegradationAPI 证据降级：exploratory 模型允许创建
// 验证（ModelEvidenceClass=exploratory），运行后结论不可能 passed。
func TestPortfolioEvidenceDegradationAPI(t *testing.T) {
	env := newPortfolioTestEnv(t)
	h := env.s.Handler()
	em := writeExploratoryModel(t, env.s.modelStore)

	res := doReq(t, h, http.MethodPost, "/api/portfolio-validations", validationBody(em, testUUID1), http.StatusCreated)
	var out struct {
		Validation PortfolioValidationRecord `json:"validation"`
	}
	if err := json.Unmarshal(res, &out); err != nil {
		t.Fatal(err)
	}
	if out.Validation.ModelEvidenceClass != portfolioresearch.EvidenceExploratory {
		t.Fatalf("ModelEvidenceClass = %q, want exploratory（API 如实返回）", out.Validation.ModelEvidenceClass)
	}
	doReq(t, h, http.MethodPost, "/api/portfolio-runs/"+out.Validation.ID+"/start", nil, http.StatusAccepted)
	waitStatus(t, h)
	res = doReq(t, h, http.MethodGet, "/api/portfolio-validations/"+out.Validation.ID, nil, http.StatusOK)
	var got struct {
		Validation PortfolioValidationView `json:"validation"`
	}
	if err := json.Unmarshal(res, &got); err != nil {
		t.Fatal(err)
	}
	view := got.Validation
	if view.Report == nil {
		t.Fatal("验证报告缺失")
	}
	if view.Report.Verdict == portfolioresearch.GateStatusPassed {
		t.Fatal("exploratory 输入不可能 passed")
	}
}

// TestPortfolioRunLifecycleAPI 运行生命周期：创建实验 → start(202) →
// 轮询 /api/status → run 详情从 Store → 产物安全读取。
func TestPortfolioRunLifecycleAPI(t *testing.T) {
	env := newPortfolioTestEnv(t)
	h := env.s.Handler()
	m := createModelViaAPI(t, h, env.validationID, testUUID1)
	res := doReq(t, h, http.MethodPost, "/api/portfolio-experiments", experimentBody(m, testUUID1), http.StatusCreated)
	var created struct {
		Experiment PortfolioExperiment `json:"experiment"`
	}
	if err := json.Unmarshal(res, &created); err != nil {
		t.Fatal(err)
	}
	id := created.Experiment.ExperimentID

	// 启动 → 202 accepted + runId。
	res = doReq(t, h, http.MethodPost, "/api/portfolio-runs/"+id+"/start", nil, http.StatusAccepted)
	if !bytes.Contains(res, []byte(`"accepted":true`)) || !bytes.Contains(res, []byte(id)) {
		t.Fatalf("start 响应应 accepted + runId: %s", res)
	}
	st := waitStatus(t, h)
	if st["task"] != "portfolio" {
		t.Fatalf("task = %v, want portfolio", st["task"])
	}
	// run 详情从 Store 恢复终态。
	res = doReq(t, h, http.MethodGet, "/api/portfolio-runs/"+id, nil, http.StatusOK)
	var run map[string]any
	if err := json.Unmarshal(res, &run); err != nil {
		t.Fatal(err)
	}
	runObj := run["run"].(map[string]any)
	if runObj["status"] != "completed" {
		t.Fatalf("run 终态 = %v, want completed", runObj["status"])
	}
	// 实验详情与产物读取（report.json 为 JSON；csv 为文本）。
	res = doReq(t, h, http.MethodGet, "/api/portfolio-experiments/"+id+"/artifacts/report.json", nil, http.StatusOK)
	if !bytes.Contains(res, []byte(`"experimentId"`)) {
		t.Fatalf("report.json 产物异常: %.200s", res)
	}
	doReq(t, h, http.MethodGet, "/api/portfolio-experiments/"+id+"/artifacts/nav.csv", nil, http.StatusOK)
}

// TestPortfolioCancelAPI 取消：start → stop → 终态 cancelled 从 Store 可读。
func TestPortfolioCancelAPI(t *testing.T) {
	env := newPortfolioTestEnv(t)
	h := env.s.Handler()
	m := createModelViaAPI(t, h, env.validationID, testUUID1)
	res := doReq(t, h, http.MethodPost, "/api/portfolio-experiments", experimentBody(m, testUUID1), http.StatusCreated)
	var created struct {
		Experiment PortfolioExperiment `json:"experiment"`
	}
	if err := json.Unmarshal(res, &created); err != nil {
		t.Fatal(err)
	}
	id := created.Experiment.ExperimentID
	doReq(t, h, http.MethodPost, "/api/portfolio-runs/"+id+"/start", nil, http.StatusAccepted)
	doReq(t, h, http.MethodPost, "/api/portfolio-runs/"+id+"/stop", nil, http.StatusOK)
	waitStatus(t, h)
	res = doReq(t, h, http.MethodGet, "/api/portfolio-runs/"+id, nil, http.StatusOK)
	var run map[string]any
	if err := json.Unmarshal(res, &run); err != nil {
		t.Fatal(err)
	}
	if run["run"].(map[string]any)["status"] != "cancelled" {
		t.Fatalf("取消后终态 = %v, want cancelled", run["run"].(map[string]any)["status"])
	}
}

// TestPortfolioBusyConflictAPI 忙碌冲突：任务互斥时 start → 409。
func TestPortfolioBusyConflictAPI(t *testing.T) {
	env := newPortfolioTestEnv(t)
	h := env.s.Handler()
	m := createModelViaAPI(t, h, env.validationID, testUUID1)
	res := doReq(t, h, http.MethodPost, "/api/portfolio-experiments", experimentBody(m, testUUID1), http.StatusCreated)
	var created struct {
		Experiment PortfolioExperiment `json:"experiment"`
	}
	if err := json.Unmarshal(res, &created); err != nil {
		t.Fatal(err)
	}
	env.s.runner.mu.Lock()
	defer env.s.runner.mu.Unlock()
	doReq(t, h, http.MethodPost, "/api/portfolio-runs/"+created.Experiment.ExperimentID+"/start", nil, http.StatusConflict)
}

// TestPortfolioRestartAPI 重启后读取：新 Server 同根目录，run 详情与实验
// 详情从 Store 恢复（不依赖内存）。
func TestPortfolioRestartAPI(t *testing.T) {
	env := newPortfolioTestEnv(t)
	h := env.s.Handler()
	m := createModelViaAPI(t, h, env.validationID, testUUID1)
	res := doReq(t, h, http.MethodPost, "/api/portfolio-experiments", experimentBody(m, testUUID1), http.StatusCreated)
	var created struct {
		Experiment PortfolioExperiment `json:"experiment"`
	}
	if err := json.Unmarshal(res, &created); err != nil {
		t.Fatal(err)
	}
	id := created.Experiment.ExperimentID
	doReq(t, h, http.MethodPost, "/api/portfolio-runs/"+id+"/start", nil, http.StatusAccepted)
	waitStatus(t, h)

	// 等价重启：新 Server 同根。
	h2 := env.rebuild(t).Handler()
	doReq(t, h2, http.MethodGet, "/api/portfolio-runs/"+id, nil, http.StatusOK)
	doReq(t, h2, http.MethodGet, "/api/portfolio-experiments/"+id, nil, http.StatusOK)
	doReq(t, h2, http.MethodGet, "/api/portfolio-experiments/"+id+"/artifacts/report.json", nil, http.StatusOK)
}

// TestPortfolioArtifactPathTraversal 路径穿越：产物名 ../、绝对路径、非白
// 名单一律拒绝。
func TestPortfolioArtifactPathTraversal(t *testing.T) {
	env := newPortfolioTestEnv(t)
	h := env.s.Handler()
	m := createModelViaAPI(t, h, env.validationID, testUUID1)
	res := doReq(t, h, http.MethodPost, "/api/portfolio-experiments", experimentBody(m, testUUID1), http.StatusCreated)
	var created struct {
		Experiment PortfolioExperiment `json:"experiment"`
	}
	if err := json.Unmarshal(res, &created); err != nil {
		t.Fatal(err)
	}
	id := created.Experiment.ExperimentID
	doReq(t, h, http.MethodPost, "/api/portfolio-runs/"+id+"/start", nil, http.StatusAccepted)
	waitStatus(t, h)
	// URL 编码的穿越段会到达 handler 并经 ValidArtifactName 拒绝（400）；
	// 裸 ../ 被 mux 路径归一化重定向（301 后 404，同样安全拒绝）。
	for _, name := range []string{
		"..%2Fsecret", "%2Fetc%2Fpasswd", "report.json%2F..%2Fx",
		"..%5Csecret", "nav.csv%2F..%2F..", "evil.json",
	} {
		doReq(t, h, http.MethodGet, "/api/portfolio-experiments/"+id+"/artifacts/"+name, nil, http.StatusBadRequest)
	}
	doReq(t, h, http.MethodGet, "/api/portfolio-experiments/"+id+"/artifacts/../secret", nil, http.StatusMovedPermanently)
	// 未完成实验的产物 → 404。
	res = doReq(t, h, http.MethodPost, "/api/portfolio-experiments", experimentBody(m, testUUID2), http.StatusCreated)
	var second struct {
		Experiment PortfolioExperiment `json:"experiment"`
	}
	if err := json.Unmarshal(res, &second); err != nil {
		t.Fatal(err)
	}
	doReq(t, h, http.MethodGet, "/api/portfolio-experiments/"+second.Experiment.ExperimentID+"/artifacts/report.json", nil, http.StatusNotFound)
}

// TestPortfolioLegacyCompat 旧客户端兼容：新增组合路由后 /api/status 与
// /api/stop 仍工作，注册表路由不受影响。
func TestPortfolioLegacyCompat(t *testing.T) {
	env := newPortfolioTestEnv(t)
	h := env.s.Handler()
	res := doReq(t, h, http.MethodGet, "/api/status", nil, http.StatusOK)
	var st map[string]any
	if err := json.Unmarshal(res, &st); err != nil {
		t.Fatal(err)
	}
	if st["state"] != "idle" {
		t.Fatalf("初始状态应为 idle: %v", st)
	}
	doReq(t, h, http.MethodPost, "/api/stop", nil, http.StatusOK)
	// 因子目录路由仍在。
	doReq(t, h, http.MethodGet, "/api/factors", nil, http.StatusOK)
}
