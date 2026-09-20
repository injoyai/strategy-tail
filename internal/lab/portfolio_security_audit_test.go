package lab

// portfolio_security_audit_test.go v2 Task 11 安全检查审计（计划 Task 11 §安全）。
//
// 对 Task 1-10 的存储与 API 做渗透式测试（补充既有单测未覆盖的变体与组合）：
//   - 路径穿越（../、URL 编码、绝对路径、Unicode 变体、嵌套）；
//   - 超大参数（pageSize 超限、page 越界、日期超界、因子数超限、数组长度超限、
//     请求体超限 413）；
//   - 非有限浮点（NaN/Inf/溢出 1e400 经 JSON 拒绝；领域层 NaN 权重/收益/费用
//     拒绝且不污染）；
//   - 损坏 JSON（截断/非法 JSON → fail closed，不 panic；磁盘记录损坏）；
//   - 非法下载名（白名单外、嵌套路径、绝对路径）；
//   - 并发覆盖（并发 MarkCompleted/MarkFailed 一次生效；并发 SaveWindow；
//     并发分页读）；
//   - 取消竞态（running 中取消 vs 完成并发）；
//   - 日志敏感信息（审计运行路径是否打印绝对路径/密钥/完整模型内容）。

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/injoyai/strategy-tail/internal/portfolioresearch"
)

// auditUUID3 / auditUUID4 本文件专用幂等键（testUUID1/2 已被既有测试使用）。
const (
	auditUUID3 = "a1b2c3d4-0000-4000-8000-000000000003"
	auditUUID4 = "a1b2c3d4-0000-4000-8000-000000000004"
)

// doRawReq 发送原始字节请求体（绕过 encoding/json 的 NaN 限制，测试解码层
// 对非法 JSON 的 fail closed 行为）。
func doRawReq(t *testing.T, h http.Handler, method, path string, raw []byte, wantCode int) []byte {
	t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != wantCode {
		t.Fatalf("%s %s: 状态码 %d (期望 %d)，响应: %s", method, path, rec.Code, wantCode, rec.Body.String())
	}
	return rec.Body.Bytes()
}

// TestAuditPathTraversalVariants 路径穿越变体：白名单校验拒绝 ../、URL 编码、
// 绝对路径（含 Windows/Unix）、Unicode 全角斜杠、嵌套路径与点号变体；Store
// Get 对穿越 ID fail closed。
func TestAuditPathTraversalVariants(t *testing.T) {
	// ValidArtifactName 拒绝全部非法变体。
	for _, name := range []string{
		"", ".", "..",
		"../secret.json", "..\\secret.json",
		"..%2Fsecret.json", "%2e%2e%2fsecret.json", "..%5Csecret.json",
		"/etc/passwd", "C:\\Windows\\system32\\drivers\\etc\\hosts",
		"sub/report.json", "sub\\report.json", "report.json/..", "report.json\\..",
		"report.json/", "report.json\\", "a..b.json",
		"..／report.json", "．.／etc", // 全角斜杠 U+FF0F、全角点 U+FF0E
		"report.json ", " report.json",
	} {
		if err := ValidArtifactName(name); err == nil {
			t.Fatalf("ValidArtifactName 应拒绝非法产物名: %q", name)
		}
	}
	// 白名单五类合法。
	for _, name := range portfolioArtifactNames {
		if err := ValidArtifactName(name); err != nil {
			t.Fatalf("合法产物名 %q 被拒绝: %v", name, err)
		}
	}

	// Store 层：非法 ID（穿越/绝对路径）一律拒绝，不访问文件系统。
	env := newPortfolioTestEnv(t)
	for _, id := range []string{
		"../evil", "..\\evil", "..%2Fevil", "/tmp/x", "C:\\x", "pe_..", "sub/pe_x", "",
	} {
		if _, err := env.s.experimentStore.Get(id); err == nil {
			t.Fatalf("实验 Store Get 应拒绝非法 ID %q", id)
		}
		if _, err := env.s.validationStore.Get(id); err == nil {
			t.Fatalf("验证 Store Get 应拒绝非法 ID %q", id)
		}
		if _, err := env.s.modelStore.Get(id, 1); err == nil {
			t.Fatalf("模型 Store Get 应拒绝非法 ID %q", id)
		}
	}
}

// TestAuditOversizedParams 超大参数：分页越界、日期超界、因子数超限、窗口
// 规则超限、请求体超限。全部 fail closed（400/413），不 panic 不 500。
func TestAuditOversizedParams(t *testing.T) {
	env := newPortfolioTestEnv(t)
	h := env.s.Handler()
	m := createModelViaAPI(t, h, env.validationID, testUUID1)

	// 分页：pageSize 越界（>200 / <=0）→ 400；page<1 → 400；巨大 page → 200 空页。
	for _, q := range []string{"pageSize=201", "pageSize=0", "pageSize=-5", "page=0", "page=-1"} {
		doReq(t, h, http.MethodGet, "/api/portfolio-experiments?"+q, nil, http.StatusBadRequest)
		doReq(t, h, http.MethodGet, "/api/portfolio-validations?"+q, nil, http.StatusBadRequest)
	}
	// 非数值分页参数：容错回落默认（1/20），不报错（宽松但不构成资源风险）。
	doReq(t, h, http.MethodGet, "/api/portfolio-experiments?page=abc&pageSize=xyz", nil, http.StatusOK)
	// 巨大 page：越界返回空 items + 真实 total（200，不 500）。
	res := doReq(t, h, http.MethodGet, "/api/portfolio-experiments?page=99999999&pageSize=200", nil, http.StatusOK)
	var pg struct {
		Experiments struct {
			Items    []any `json:"items"`
			Total    int   `json:"total"`
			Page     int   `json:"page"`
			PageSize int   `json:"pageSize"`
		} `json:"experiments"`
	}
	if err := json.Unmarshal(res, &pg); err != nil {
		t.Fatal(err)
	}
	if len(pg.Experiments.Items) != 0 || pg.Experiments.Page != 99999999 {
		t.Fatalf("巨大 page 应返回空 items + 原页码: %+v", pg)
	}

	// 日期超界（设计 §15：1990-01-01 ~ 2100-12-31）。
	body := experimentBody(m, testUUID2)
	rng := body["studyRange"].(map[string]any)
	rng["start"] = "1989-12-31"
	doReq(t, h, http.MethodPost, "/api/portfolio-experiments", body, http.StatusBadRequest)
	rng["start"] = "2025-01-01"
	rng["end"] = "2101-01-01"
	doReq(t, h, http.MethodPost, "/api/portfolio-experiments", body, http.StatusBadRequest)
	rng["end"] = "2025-01-20" // 复原合法
	doReq(t, h, http.MethodPost, "/api/portfolio-experiments", body, http.StatusCreated)

	// 因子数超限（MaxFactors=20）：21 个合法格式的验证 ID → 400。
	tooMany := createModelBody(env.validationID, auditUUID3)
	vals := make([]string, 21)
	for i := range vals {
		vals[i] = fmt.Sprintf("fv_20260919T000000000Z_%08x", i)
	}
	tooMany["factorValidations"] = vals
	doReq(t, h, http.MethodPost, "/api/factor-models", tooMany, http.StatusBadRequest)

	// 窗口规则超限（WindowRule 上限 10000）。
	vbody := validationBody(m, auditUUID4)
	spec := vbody["spec"].(map[string]any)
	spec["windowRule"] = map[string]any{"trainDays": 10001, "testDays": 5, "step": 5}
	doReq(t, h, http.MethodPost, "/api/portfolio-validations", vbody, http.StatusBadRequest)

	// 请求体超限（64 KiB）→ 413。
	big := map[string]any{"requestId": testUUID1, "pad": strings.Repeat("x", 70<<10)}
	doReq(t, h, http.MethodPost, "/api/factor-models", big, http.StatusRequestEntityTooLarge)
}

// TestAuditNonFiniteFloatsRejected 非有限浮点：JSON 层拒绝 NaN/Inf/溢出数字
// （解码 fail closed）；领域层 NaN 权重/收益/费用拒绝且不污染现金与持仓。
func TestAuditNonFiniteFloatsRejected(t *testing.T) {
	env := newPortfolioTestEnv(t)
	h := env.s.Handler()

	// 1) JSON 层：NaN/Inf/溢出直接构造非法 JSON 字面量 → 400（json.Decoder
	//    拒绝非法数字，fail closed，不 panic）。
	doRawReq(t, h, http.MethodPost, "/api/factor-models",
		[]byte(`{"requestId":"`+testUUID1+`","createdBy":"t","researchQuestion":"q","hypothesis":"h",`+
			`"factorValidations":["`+env.validationID+`"],"transformPipeline":{"missing":"exclude",`+
			`"winsorize":{"mode":"quantile","quantile":NaN},"neutralize":{"mode":"none"},"standardize":"rank"},`+
			`"combination":{"method":"equal_weight_rank"},"portfolioPolicy":{"selection":"top_n","topN":2,"cashBuffer":0.05},`+
			`"execution":{"rebalance":"daily","fillAt":"next_open","sellFirst":true,"t1Restriction":true,"lotSize":100,`+
			`"cost":{"commissionRate":0.0003,"stampDutyRate":0.001,"transferFeeRate":0,"slippage":0.01,"minCommission":5}},`+
			`"benchmark":{"id":"hs300"}}`),
		http.StatusBadRequest)
	doRawReq(t, h, http.MethodPost, "/api/factor-models",
		[]byte(`{"winsorize":{"quantile":Inf}}`), http.StatusBadRequest)
	doRawReq(t, h, http.MethodPost, "/api/factor-models",
		[]byte(`{"winsorize":{"quantile":1e400}}`), http.StatusBadRequest)

	// 2) 模型 hash：NaN 成本参数被 checkFinite 拒绝（fail closed）。
	badModel := portfolioresearch.FactorModel{
		CreatedBy: "t", ResearchQuestion: "q", Hypothesis: "h",
		ValidatedFactors: []portfolioresearch.ValidatedFactorRef{{
			CandidateID: "fc_a", CandidateRevision: 1, ValidationID: "fv_a",
			FactorKind: "momentum", FactorDays: 2, ImplementationVersion: 1,
			Direction:     portfolioresearch.DirectionHigherIsBetter,
			EvidenceClass: portfolioresearch.EvidenceRetrospective, PrimaryHorizon: 1,
			DataSnapshot: portfolioresearch.DataSnapshot{UniverseMode: "x", PriceSource: "y"},
		}},
		TransformPipeline: portfolioresearch.TransformPipeline{
			Missing:     portfolioresearch.TransformMissingExclude,
			Winsorize:   portfolioresearch.WinsorizeSpec{Mode: portfolioresearch.TransformWinsorizeNone},
			Neutralize:  portfolioresearch.NeutralizeSpec{Mode: portfolioresearch.TransformNeutralizeNone},
			Standardize: portfolioresearch.TransformStandardizeRank,
		},
		Combination: portfolioresearch.CombinationSpec{Method: portfolioresearch.CombinationEqualWeightRank},
		PortfolioPolicy: portfolioresearch.PortfolioPolicy{
			Selection: portfolioresearch.PortfolioSelectionTopN, TopN: 2,
		},
		Execution: portfolioresearch.ExecutionSpec{
			Rebalance: portfolioresearch.RebalanceDaily, FillAt: portfolioresearch.FillNextOpen,
			SellFirst: true, T1Restriction: true, LotSize: 100,
			Cost: portfolioresearch.CostSpec{CommissionRate: math.NaN()},
		},
		EvidenceClass: portfolioresearch.EvidenceRetrospective,
	}
	if _, err := portfolioresearch.ModelHash(badModel); err == nil || !strings.Contains(err.Error(), "有限") {
		t.Fatalf("模型含 NaN 成本应被 ModelHash 拒绝: %v", err)
	}
	// 验证 spec 含 NaN 门禁阈值 → 拒绝。
	badSpec := portfolioresearch.PortfolioValidationSpec{
		ModelRef: portfolioresearch.ModelRef{ModelID: "fm_20260919T000000000Z_00000001", Revision: 1,
			Hash: strings.Repeat("a", 64)},
		WindowRule: portfolioresearch.WindowRule{TrainDays: 5, TestDays: 5, Step: 5},
		Gates:      portfolioresearch.GateSpec{MinNetReturn: math.Inf(1)},
	}
	if _, err := portfolioresearch.PortfolioValidationSpecHash(badSpec); err == nil || !strings.Contains(err.Error(), "有限") {
		t.Fatalf("spec 含 Inf 门禁应被 SpecHash 拒绝: %v", err)
	}

	// 3) 领域层：BuildTarget 拒绝 NaN 分数；ExecuteDay 缺价/NaN 价 fail closed
	//    且不污染现金/持仓；ComputeAttribution 拒绝 NaN 收益。
	exec := auditGoldenExec()
	if _, err := portfolioresearch.BuildTarget(auditGoldenPolicy(), exec, portfolioresearch.TargetInput{
		Date: "2026-01-05", Scores: map[string]float64{"a": math.NaN()},
		Tradable: map[string]bool{"a": true}, Prices: map[string]float64{"a": 10},
		Holdings: map[string]float64{}, CashWeight: 1, Notional: 1_000_000,
	}); err == nil || !strings.Contains(err.Error(), "非有限") {
		t.Fatalf("BuildTarget 应拒绝 NaN 分数: %v", err)
	}
	// ExecuteDay：目标持仓的代码缺开盘价 → 拒绝（suspended）+ 质量事件 + 降级，
	// 现金与持仓保持原值（NaN 永不进入状态）。
	ledger, state, err := portfolioresearch.ExecuteDay(
		portfolioresearch.PortfolioState{Cash: 100000, Lots: []portfolioresearch.HoldingLot{
			{Code: "x", Shares: 100, BuyPrice: 10, CostBasis: 1000, BuyDate: "2026-01-05"},
		}},
		portfolioresearch.DayInput{
			Target: portfolioresearch.TargetPortfolio{Constrained: portfolioresearch.PortfolioWeights{
				Shares: map[string]int{"x": 0},
			}},
			Market: portfolioresearch.DayMarket{
				Date: "2026-01-06", OpenPrice: map[string]float64{"x": math.NaN()},
				PrevClose: map[string]float64{"x": 10}, ClosePrice: map[string]float64{"x": 10},
				Tradable: map[string]bool{"x": true}, Listed: map[string]bool{"x": true},
			},
			Scores: nil,
		}, exec)
	if err != nil {
		t.Fatalf("ExecuteDay 缺价应降级而非报错: %v", err)
	}
	if len(ledger.Rejections) != 1 || ledger.Rejections[0].Reason != portfolioresearch.UnfilledSuspended {
		t.Fatalf("NaN 开盘价应拒绝卖出（suspended）: %+v", ledger.Rejections)
	}
	if !ledger.Degraded {
		t.Fatal("缺价应标记当日降级")
	}
	if !isFinite(ledger.EndCash) || !isFinite(ledger.EndEquity) || !isFinite(ledger.Pnl) ||
		!isFinite(ledger.Fees.Total()) {
		t.Fatalf("NaN 输入不得污染现金/净值/损益/费用: %+v", ledger)
	}
	if math.Abs(state.Cash-100000) > 1e-9 || len(state.Lots) != 1 {
		t.Fatalf("拒绝后现金与持仓应保持原值: %+v", state)
	}
	// ComputeAttribution：NaN 收益 → error（fail closed）。
	_, err = portfolioresearch.ComputeAttribution(portfolioresearch.AttributionInput{
		InitialEquity: 100000, RiskFreeRate: 0,
		Days: []portfolioresearch.AttributionDay{{
			Date: "2026-01-06", ActualWeights: map[string]float64{"x": 1},
			Returns: map[string]float64{"x": math.NaN()}, CashRatio: 0,
		}},
	})
	if err == nil || !strings.Contains(err.Error(), "Returns") {
		t.Fatalf("ComputeAttribution 应拒绝 NaN 收益: %v", err)
	}
}

// isFinite 浮点有限检查（审计辅助）。
func isFinite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

// TestAuditCorruptedJSON 损坏 JSON：截断/非法 JSON → 400（解码层 fail closed，
// 不 panic）；磁盘记录损坏 → Store Get fail closed。
func TestAuditCorruptedJSON(t *testing.T) {
	env := newPortfolioTestEnv(t)
	h := env.s.Handler()

	// 截断/非法 JSON 请求体 → 400。
	doRawReq(t, h, http.MethodPost, "/api/factor-models", []byte(`{"requestId":`), http.StatusBadRequest)
	doRawReq(t, h, http.MethodPost, "/api/factor-models", []byte(`{"requestId":"x",}`), http.StatusBadRequest)
	doRawReq(t, h, http.MethodPost, "/api/factor-models", []byte(`not json at all`), http.StatusBadRequest)
	doRawReq(t, h, http.MethodPost, "/api/portfolio-experiments", []byte(`{"studyRange":{"start":"2025"`), http.StatusBadRequest)
	doRawReq(t, h, http.MethodPost, "/api/portfolio-validations", []byte(`{}`), http.StatusBadRequest)
	// 未知字段（DisallowUnknownFields）→ 400。
	doRawReq(t, h, http.MethodPost, "/api/factor-models",
		[]byte(`{"requestId":"`+testUUID1+`","unknownField":1}`), http.StatusBadRequest)

	// 磁盘模型记录损坏 → Get fail closed（不 panic）。
	m := createModelViaAPI(t, h, env.validationID, testUUID1)
	// 验证记录必须先创建（Create 需读冻结模型），再损坏模型/窗口验证 fail closed。
	recV, _, err := env.s.validationStore.Create(CreatePortfolioValidationRequest{
		RequestID: auditUUID3, Spec: portfolioValidationSpecFor(m),
	}, env.s.modelStore)
	if err != nil {
		t.Fatal(err)
	}
	modelFile := filepath.Join(env.modelRoot, m.ModelID, "000001.json")
	if err := os.WriteFile(modelFile, []byte(`{"modelId":`), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := env.s.modelStore.Get(m.ModelID, 1); err == nil {
		t.Fatal("损坏模型记录应 fail closed")
	}
	// 损坏实验记录 → Get fail closed。
	res := doReq(t, h, http.MethodPost, "/api/portfolio-experiments", experimentBody(m, testUUID2), http.StatusCreated)
	var created struct {
		Experiment PortfolioExperiment `json:"experiment"`
	}
	if err := json.Unmarshal(res, &created); err != nil {
		t.Fatal(err)
	}
	recFile := filepath.Join(env.expRecords, created.Experiment.ExperimentID+".json")
	if err := os.WriteFile(recFile, []byte(`{"experimentId":`), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := env.s.experimentStore.Get(created.Experiment.ExperimentID); err == nil {
		t.Fatal("损坏实验记录应 fail closed")
	}
	// 损坏验证窗口文件 → Get fail closed（不 panic）。
	winDir := filepath.Join(env.pvRoot, recV.ID, "windows")
	if err := os.MkdirAll(winDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(winDir, "0001.json"), []byte(`{"index":`), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := env.s.validationStore.Get(recV.ID); err == nil {
		t.Fatal("损坏窗口文件应 fail closed")
	}
}

// TestAuditIllegalDownloadNames 非法下载名：白名单外、嵌套路径、绝对路径、
// 未完成实验产物 → 404/400（既有 handler 测试覆盖编码穿越，此处补未完成与
// 白名单外产物名）。
func TestAuditIllegalDownloadNames(t *testing.T) {
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
	// 未完成（queued）实验的产物 → 404（即使名字合法）。
	doReq(t, h, http.MethodGet, "/api/portfolio-experiments/"+id+"/artifacts/report.json", nil, http.StatusNotFound)
	// 白名单外名字 → 400；含路径分隔符的名字由 mux 路由层拒绝（404，同样安全）。
	for _, name := range []string{"secret.json", "report.json.bak", "index.html", "C:\\x"} {
		doReq(t, h, http.MethodGet, "/api/portfolio-experiments/"+id+"/artifacts/"+name, nil, http.StatusBadRequest)
	}
	doReq(t, h, http.MethodGet, "/api/portfolio-experiments/"+id+"/artifacts/a/c", nil, http.StatusNotFound)
}

// TestAuditConcurrencyOnceEffect 并发覆盖：并发 MarkCompleted/MarkFailed 只有
// 一次生效；并发 SaveWindow 不损坏；并发分页读不 panic。
func TestAuditConcurrencyOnceEffect(t *testing.T) {
	env := newPortfolioTestEnv(t)
	m := createModelViaAPI(t, env.s.Handler(), env.validationID, testUUID1)
	records, artifacts := t.TempDir(), t.TempDir()
	st := NewPortfolioExperimentStore(records, artifacts)

	exp, created, err := st.Create(portfolioExperimentRequest(m, testUUID1))
	if err != nil || !created {
		t.Fatalf("创建实验 = %v/%v", created, err)
	}
	if err := st.Start(exp.ExperimentID); err != nil {
		t.Fatal(err)
	}
	// 先写五类产物，使 MarkCompleted 具备成功条件。
	artDir := filepath.Join(artifacts, exp.ExperimentID)
	if err := os.MkdirAll(artDir, 0755); err != nil {
		t.Fatal(err)
	}
	for _, name := range portfolioArtifactNames {
		if err := os.WriteFile(filepath.Join(artDir, name), []byte("x-"+name), 0644); err != nil {
			t.Fatal(err)
		}
	}
	// 并发 8 个终态调用（交替 completed/failed）：互斥串行化后只有一次成功。
	const n = 8
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if i%2 == 0 {
				errs[i] = st.MarkCompleted(exp.ExperimentID)
			} else {
				errs[i] = st.MarkFailed(exp.ExperimentID, "并发失败标记")
			}
		}(i)
	}
	wg.Wait()
	success := 0
	for _, e := range errs {
		if e == nil {
			success++
		}
	}
	if success != 1 {
		t.Fatalf("并发终态调用应恰好一次生效，实际成功 %d 次: %v", success, errs)
	}
	got, err := st.Get(exp.ExperimentID)
	if err != nil {
		t.Fatalf("并发后记录应可读: %v", err)
	}
	switch got.Status {
	case portfolioresearch.RunStateCompleted:
		if got.Manifest == nil {
			t.Fatal("completed 必须携带 manifest")
		}
	case portfolioresearch.RunStateFailed:
		if got.Error == "" {
			t.Fatal("failed 应携带诊断")
		}
	default:
		t.Fatalf("终态非法: %q", got.Status)
	}

	// 并发 SaveWindow 同一窗口：完成前可重写（崩溃恢复语义），文件必须有效。
	pvs := NewPortfolioValidationStore(t.TempDir())
	rec, _, err := pvs.Create(CreatePortfolioValidationRequest{
		RequestID: testUUID2, Spec: portfolioValidationSpecFor(m),
	}, env.s.modelStore)
	if err != nil {
		t.Fatal(err)
	}
	var wg2 sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg2.Add(1)
		go func(i int) {
			defer wg2.Done()
			_ = pvs.SaveWindow(rec.ID, portfolioresearch.ValidationWindowOutcome{
				ValidationID: rec.ID, Index: 1,
				TrainStart: "2025-01-01", TrainEnd: "2025-01-05",
				TestStart: "2025-01-06", TestEnd: "2025-01-10",
				State:         portfolioresearch.WindowStateOK,
				Metrics:       &portfolioresearch.ValidationWindowMetrics{TradingDays: 5, NetReturn: float64(i) / 1000},
				EvidenceClass: portfolioresearch.EvidenceRetrospective,
			})
		}(i)
	}
	wg2.Wait()
	view, err := pvs.Get(rec.ID)
	if err != nil {
		t.Fatalf("并发 SaveWindow 后读取失败: %v", err)
	}
	if len(view.Windows) != 1 || view.Windows[0].Index != 1 || view.Windows[0].Metrics == nil {
		t.Fatalf("并发 SaveWindow 窗口损坏: %+v", view.Windows)
	}

	// 并发分页读：多协程 List 不 panic、total 一致。
	var wg3 sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg3.Add(1)
		go func(i int) {
			defer wg3.Done()
			pg, err := st.List(ExperimentFilter{}, 1, 20)
			if err != nil {
				t.Errorf("并发 List 失败: %v", err)
				return
			}
			if pg.Total != 1 {
				t.Errorf("并发 List total = %d, want 1", pg.Total)
			}
		}(i)
	}
	wg3.Wait()
}

// TestAuditCancelVsCompleteRace 取消竞态：runner 运行中取消 vs 完成并发。
// 断言终态 ∈ {cancelled, completed}（两者都合法，取决于竞态时序），且记录
// 完好可读：cancelled 无 manifest、completed 必有完整 manifest（绝无
// "completed 但产物不完整"）。
func TestAuditCancelVsCompleteRace(t *testing.T) {
	for iter := 0; iter < 5; iter++ {
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
		// 不等完成，立即取消：与完成写盘并发。
		time.Sleep(2 * time.Millisecond)
		doReq(t, h, http.MethodPost, "/api/portfolio-runs/"+id+"/stop", nil, http.StatusOK)
		waitPortfolioDone(t, env.s.portfolioRunner)
		got, err := env.s.experimentStore.Get(id)
		if err != nil {
			t.Fatalf("迭代 %d：取消竞态后记录应可读: %v", iter, err)
		}
		switch got.Status {
		case portfolioresearch.RunStateCancelled:
			if got.Manifest != nil {
				t.Fatalf("迭代 %d：cancelled 不得发布 manifest", iter)
			}
			if got.Error == "" {
				t.Fatalf("迭代 %d：cancelled 应携带诊断", iter)
			}
		case portfolioresearch.RunStateCompleted:
			if got.Manifest == nil {
				t.Fatalf("迭代 %d：completed 必须携带完整 manifest（取消 vs 完成竞态不得产生不完整完成）", iter)
			}
		case portfolioresearch.RunStateRunning, portfolioresearch.RunStateQueued:
			t.Fatalf("迭代 %d：取消/完成竞态后状态不得卡在 %q", iter, got.Status)
		default:
			t.Fatalf("迭代 %d：非法终态 %q", iter, got.Status)
		}
	}
}

// TestAuditLogSensitiveInfo 日志敏感信息：组合研究源码（存储/Runner/Handler）
// 不得出现 log 输出、fmt.Print 输出，也不得把本机绝对路径或密钥写进错误文案。
// 审计方式：源码静态扫描 + 关键错误文案断言（运行路径不打印绝对路径/密钥/
// 完整模型内容）。
func TestAuditLogSensitiveInfo(t *testing.T) {
	// 测试内 cwd 已被 t.Chdir 改到临时目录，源码路径用 runtime.Caller 从本
	// 文件绝对路径推导仓库根目录（<root>/internal/lab/<file> → 上溯三级）。
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller 失败")
	}
	repoRoot := filepath.Dir(filepath.Dir(filepath.Dir(thisFile)))
	files := []string{
		"portfolio_experiment.go", "portfolio_experiment_store.go",
		"portfolio_validation.go", "portfolio_validation_store.go",
		"portfolio_runner.go", "portfolio_handlers.go",
		"factor_model.go", "factor_model_store.go",
		"../portfolioresearch/transform.go", "../portfolioresearch/combine.go",
		"../portfolioresearch/target.go", "../portfolioresearch/execution.go",
		"../portfolioresearch/accounting.go", "../portfolioresearch/metrics.go",
		"../portfolioresearch/attribution.go", "../portfolioresearch/validation.go",
	}
	for _, rel := range files {
		data, err := os.ReadFile(filepath.Join(repoRoot, "internal", "lab", rel))
		if err != nil {
			t.Fatalf("读取 %s: %v", rel, err)
		}
		src := string(data)
		// 禁止日志/直接打印（这些文件不产生进程输出；错误经 error 返回）。
		for _, bad := range []string{"log.Print", "log.Printf", "log.Println", "fmt.Print(", "fmt.Println("} {
			if strings.Contains(src, bad) {
				t.Fatalf("%s 包含敏感输出 %q（运行路径不得直接打印）", rel, bad)
			}
		}
		// 错误文案不得内嵌绝对路径（如 filepath.Join 结果、os.Getwd）——本层
		// 错误消息只携带稳定中文文案与受控字段名。
		if strings.Contains(src, `"C:\\`) || strings.Contains(src, `"/etc/`) {
			t.Fatalf("%s 错误文案疑似含绝对路径字面量", rel)
		}
		if strings.Contains(src, "os.Getwd") {
			t.Fatalf("%s 不得在错误文案中暴露工作目录", rel)
		}
	}

	// Handler 错误响应文案检查：产物读取/实验读取失败不泄露内部路径。
	env := newPortfolioTestEnv(t)
	h := env.s.Handler()
	// 运行一个真实实验后篡改产物，验证 Get/产物读取错误文案不含绝对路径。
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
	// 篡改 nav.csv 使 Get 哈希校验失败（记录损坏 → 500 文案通用，不含路径）。
	if err := os.WriteFile(filepath.Join(env.expArtifacts, id, "nav.csv"), []byte("tampered"), 0644); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/portfolio-runs/"+id, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	body := rec.Body.String()
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("篡改产物后读取应 500，实际 %d: %s", rec.Code, body)
	}
	if strings.Contains(body, env.expArtifacts) || strings.Contains(body, `C:\`) || strings.Contains(body, "\\\\") {
		t.Fatalf("错误响应泄露内部绝对路径: %s", body)
	}
}
