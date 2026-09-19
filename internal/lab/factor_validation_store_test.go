package lab

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// factor_validation_store_test.go 冻结验证存储测试（计划 Task 7 Step 3）：
// Create 冻结与幂等、依赖校验、Get 派生视图、SaveWindow 进度语义、Complete
// 一次写入、List 过滤排序与 supersededBy 派生、损坏 fail closed、空库行为。

// validationStoreFixture 完整依赖链：候选库（含 v4 证据候选）+ 试验账本
// （含同家族 trial）+ 分析库（含 v4 报告）+ 空验证库。
type validationStoreFixture struct {
	Candidates *CandidateStore
	Trials     *TrialStore
	Analyses   *AnalysisStore
	Store      *ValidationStore
	Candidate  FactorCandidate
	Trial      FactorTrial
}

// newValidationStoreFixture 构造真实依赖链：v4 报告入分析库，候选由报告
// 推导，trial 由 TrialStore.Start 登记（真实 ID/时间戳由各 Store 生成）。
func newValidationStoreFixture(t *testing.T) validationStoreFixture {
	t.Helper()
	candidates := newTestCandidateStore(t)
	trials := NewTrialStore(t.TempDir())
	analyses := NewAnalysisStore(t.TempDir())
	rep := v4ReportForTest(t, testAnalysisID, "momentum", "2026-09-17T15:00:00+08:00")
	if err := analyses.Save(rep); err != nil {
		t.Fatal(err)
	}
	cand, _, err := candidates.Create(observeReq(testUUID1, "动量候选"), rep)
	if err != nil {
		t.Fatal(err)
	}
	trial, err := trials.Start(*rep.Protocol, rep.Factor, testAnalysisID)
	if err != nil {
		t.Fatal(err)
	}
	return validationStoreFixture{
		Candidates: candidates, Trials: trials, Analyses: analyses,
		Store:     NewValidationStore(t.TempDir()),
		Candidate: cand, Trial: trial,
	}
}

func (f validationStoreFixture) deps() FreezeDependencies {
	return FreezeDependencies{Candidates: f.Candidates, Trials: f.Trials, Analyses: f.Analyses}
}

// validCreateReq 合法冻结请求（指向夹具候选与 trial）。
func validCreateReq(f validationStoreFixture, requestID string) CreateValidationRequest {
	return CreateValidationRequest{
		RequestID:            requestID,
		CandidateID:          f.Candidate.ID,
		CandidateRevision:    f.Candidate.Revision,
		EvidenceClass:        validationEvidenceRetrospective,
		DiscoveryAnalysisIDs: []string{testAnalysisID},
		TrialIDs:             []string{f.Trial.ID},
		Windows:              WalkForwardSpec{TrainYears: 3, TestYears: 1, StepYears: 1, PurgeDays: 20},
		Policy: ValidationPolicy{MinCoverage: 0.8, MinValidWindows: 3,
			RequiredDirectionRate: 0.6, Aggregation: validationAggregationWindowEqual},
	}
}

// passedReportFor 构造合法终态报告（passed，逐项门禁齐全）。
func passedReportFor(id string) FactorValidationReport {
	return FactorValidationReport{
		SchemaVersion: validationSchemaVersion,
		ValidationID:  id,
		FinishedAt:    time.Now().UTC().Format(time.RFC3339),
		State:         ValidationStatePassed,
		Verdict:       ValidationVerdictPassed,
		Checks: []ValidationCheck{
			{Name: "direction_rate", Expected: ">=0.6", Actual: "0.9", Result: "pass"},
		},
		WindowCount: 1, ValidWindowCount: 1,
		Aggregate: &ValidationAggregate{
			DirectionRate: 0.9, CoverageWeighted: 0.95,
			GateAggregation: validationAggregationWindowEqual,
		},
	}
}

// okWindowFor 构造合法窗口进度报告。
func okWindowFor(validationID string, index int) ValidationWindowReport {
	return ValidationWindowReport{
		SchemaVersion: validationSchemaVersion,
		ValidationID:  validationID,
		Index:         index,
		StartDate:     "2025-01-02", EndDate: "2026-01-02",
		State: ValidationWindowOK, Observations: 100,
		Horizons: []ValidationWindowHorizon{
			{Horizon: 1, Direction: "matched", Coverage: 0.95},
		},
	}
}

// TestValidationStoreCreateFreezesAndIdempotent 冻结记录完整、同幂等键重试
// 返回同一记录、同键不同内容冲突。
func TestValidationStoreCreateFreezesAndIdempotent(t *testing.T) {
	f := newValidationStoreFixture(t)
	vr, created, err := f.Store.Create(validCreateReq(f, testUUID2), f.deps())
	if err != nil || !created {
		t.Fatalf("首次冻结 = %v/%v", created, err)
	}
	if !validValidationID(vr.ID) || vr.RequestHash == "" || vr.FrozenAt == "" {
		t.Fatalf("ID/RequestHash/FrozenAt 应由 Store 填充: %+v", vr)
	}
	if vr.SchemaVersion != validationSchemaVersion ||
		vr.CreateRequestID != testUUID2 || vr.CreateRequestHash == "" {
		t.Fatalf("幂等键应冻结进记录: %+v", vr)
	}
	if vr.Protocol.CandidateID != f.Candidate.ID ||
		vr.Protocol.CandidateRevision != f.Candidate.Revision {
		t.Fatalf("Protocol = %+v", vr.Protocol)
	}
	if vr.Candidate.ID != f.Candidate.ID || vr.Candidate.Revision != f.Candidate.Revision {
		t.Fatalf("候选快照应冻结指定 revision: %+v", vr.Candidate)
	}
	if len(vr.Trials) != 1 || vr.Trials[0].ID != f.Trial.ID {
		t.Fatalf("trial 账本快照应冻结: %+v", vr.Trials)
	}
	if vr.ResearchProtocol == nil || vr.ResearchProtocolHash == "" {
		t.Fatalf("研究协议应冻结: %+v", vr.ResearchProtocol)
	}
	// 幂等重试：同 RequestID 同内容返回已有记录（created=false）
	vr2, created2, err := f.Store.Create(validCreateReq(f, testUUID2), f.deps())
	if err != nil || created2 || vr2.ID != vr.ID {
		t.Fatalf("幂等重试应返回同一记录: %v/%v/%v", created2, vr2.ID, err)
	}
	// 同 RequestID 不同内容 → 冲突
	conflict := validCreateReq(f, testUUID2)
	conflict.Windows.TrainYears = 2
	if _, _, err := f.Store.Create(conflict, f.deps()); !errors.Is(err, errIdempotencyConflict) {
		t.Fatalf("幂等键复用应冲突: %v", err)
	}
}

// TestValidationStoreCreateRejectsBadDeps 依赖缺失与未知候选拒绝。
func TestValidationStoreCreateRejectsBadDeps(t *testing.T) {
	f := newValidationStoreFixture(t)
	req := validCreateReq(f, testUUID2)
	for name, deps := range map[string]FreezeDependencies{
		"缺候选库":  {Trials: f.Trials, Analyses: f.Analyses},
		"缺试验账本": {Candidates: f.Candidates, Analyses: f.Analyses},
		"缺分析库":  {Candidates: f.Candidates, Trials: f.Trials},
	} {
		if _, _, err := f.Store.Create(req, deps); err == nil {
			t.Fatalf("%s 应拒绝", name)
		}
	}
	// 候选不存在（合法 ID 格式但库里没有）
	missing := req
	missing.CandidateID = fcFreezeTestID
	if _, _, err := f.Store.Create(missing, f.deps()); err == nil {
		t.Fatal("不存在的候选应拒绝")
	}
}

// TestValidationStoreGetView Get 返回冻结请求与派生状态；非法 ID 与缺失
// 记录拒绝。
func TestValidationStoreGetView(t *testing.T) {
	f := newValidationStoreFixture(t)
	vr, _, err := f.Store.Create(validCreateReq(f, testUUID2), f.deps())
	if err != nil {
		t.Fatal(err)
	}
	view, err := f.Store.Get(vr.ID)
	if err != nil {
		t.Fatal(err)
	}
	if view.Request.ID != vr.ID || view.Request.RequestHash != vr.RequestHash {
		t.Fatalf("冻结请求应一致: %+v", view.Request)
	}
	if view.State != ValidationStateFrozen {
		t.Fatalf("无报告时派生状态应为 frozen: %q", view.State)
	}
	if len(view.Windows) != 0 || view.Report != nil || len(view.SupersededBy) != 0 {
		t.Fatalf("初始视图不应有窗口/报告/替换标记: %+v", view)
	}
	if _, err := f.Store.Get("fv_bad"); err == nil {
		t.Fatal("非法 ID 应拒绝")
	}
	if _, err := f.Store.Get(fvFreezeGhostID); !errors.Is(err, errValidationNotFound) {
		t.Fatalf("不存在的验证应 NotFound: %v", err)
	}
}

// TestValidationStoreSaveWindow 进度语义：完成前可重写同一窗口；Index 范围、
// ID 一致性与状态白名单校验；读取按 Index 升序。
func TestValidationStoreSaveWindow(t *testing.T) {
	f := newValidationStoreFixture(t)
	vr, _, err := f.Store.Create(validCreateReq(f, testUUID2), f.deps())
	if err != nil {
		t.Fatal(err)
	}
	// 完成前重写同一窗口（崩溃恢复重跑属进度语义）
	for i := 0; i < 2; i++ {
		if err := f.Store.SaveWindow(vr.ID, okWindowFor(vr.ID, 2)); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.Store.SaveWindow(vr.ID, okWindowFor(vr.ID, 1)); err != nil {
		t.Fatal(err)
	}
	view, err := f.Store.Get(vr.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Windows) != 2 || view.Windows[0].Index != 1 || view.Windows[1].Index != 2 {
		t.Fatalf("窗口应按 Index 升序: %+v", view.Windows)
	}
	// Index 越界
	if err := f.Store.SaveWindow(vr.ID, okWindowFor(vr.ID, 0)); err == nil {
		t.Fatal("Index 0 应拒绝")
	}
	if err := f.Store.SaveWindow(vr.ID, okWindowFor(vr.ID, 10000)); err == nil {
		t.Fatal("Index 10000 应拒绝")
	}
	// ValidationID 不一致
	bad := okWindowFor(fvFreezeGhostID, 3)
	if err := f.Store.SaveWindow(vr.ID, bad); err == nil {
		t.Fatal("ValidationID 不一致应拒绝")
	}
	// 非法窗口状态
	illegal := okWindowFor(vr.ID, 3)
	illegal.State = "bogus"
	if err := f.Store.SaveWindow(vr.ID, illegal); err == nil {
		t.Fatal("非法窗口状态应拒绝")
	}
	// 未发布的验证拒绝
	if err := f.Store.SaveWindow(fvFreezeGhostID, okWindowFor(fvFreezeGhostID, 1)); err == nil {
		t.Fatal("未发布验证应拒绝")
	}
}

// TestValidationStoreCompleteOnce 报告一次写入：完成后二次 Complete 与
// SaveWindow 均拒绝；Get 派生终态；非终态报告拒绝写入。
func TestValidationStoreCompleteOnce(t *testing.T) {
	f := newValidationStoreFixture(t)
	vr, _, err := f.Store.Create(validCreateReq(f, testUUID2), f.deps())
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Store.SaveWindow(vr.ID, okWindowFor(vr.ID, 1)); err != nil {
		t.Fatal(err)
	}
	if err := f.Store.Complete(vr.ID, passedReportFor(vr.ID)); err != nil {
		t.Fatal(err)
	}
	if err := f.Store.Complete(vr.ID, passedReportFor(vr.ID)); !errors.Is(err, errValidationCompleted) {
		t.Fatalf("二次完成应拒绝: %v", err)
	}
	if err := f.Store.SaveWindow(vr.ID, okWindowFor(vr.ID, 1)); !errors.Is(err, errValidationCompleted) {
		t.Fatalf("完成后写窗口应拒绝: %v", err)
	}
	view, err := f.Store.Get(vr.ID)
	if err != nil {
		t.Fatal(err)
	}
	if view.State != ValidationStatePassed || view.Report == nil ||
		view.Report.ValidationID != vr.ID || len(view.Windows) != 1 {
		t.Fatalf("完成视图应派生终态并保留窗口: %+v", view)
	}
	// 非终态报告拒绝
	frozenRep := passedReportFor(vr.ID)
	frozenRep.State = ValidationStateFrozen
	if err := f.Store.Complete(vr.ID, frozenRep); err == nil {
		t.Fatal("非终态报告应拒绝")
	}
}

// TestValidationStoreListFilterAndSupersede 过滤、排序不变量与 supersededBy
// 派生；完成标记影响派生状态。
func TestValidationStoreListFilterAndSupersede(t *testing.T) {
	f := newValidationStoreFixture(t)
	a, _, err := f.Store.Create(validCreateReq(f, testUUID2), f.deps())
	if err != nil {
		t.Fatal(err)
	}
	// B 替换 A（同候选同账本，新幂等键）
	reqB := validCreateReq(f, "9b2f1c3d-4e5f-4a7b-8c9d-0e1f2a3b4c5e")
	reqB.Supersedes = a.ID
	b, _, err := f.Store.Create(reqB, f.deps())
	if err != nil {
		t.Fatal(err)
	}
	// 完成标记影响派生状态
	if err := f.Store.Complete(a.ID, passedReportFor(a.ID)); err != nil {
		t.Fatal(err)
	}
	all, err := f.Store.List(ValidationFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("应返回 2 条: %d", len(all))
	}
	// 排序不变量：FrozenAt 倒序，同刻 ID 升序兜底
	for i := 1; i < len(all); i++ {
		prev, cur := all[i-1], all[i]
		if prev.FrozenAt < cur.FrozenAt {
			t.Fatalf("FrozenAt 应倒序: %s < %s", prev.FrozenAt, cur.FrozenAt)
		}
		if prev.FrozenAt == cur.FrozenAt && prev.ID > cur.ID {
			t.Fatalf("同 FrozenAt 应 ID 升序: %s > %s", prev.ID, cur.ID)
		}
	}
	// SupersededBy 派生（不改写旧记录）
	byID := map[string]ValidationSummary{}
	for _, s := range all {
		byID[s.ID] = s
	}
	if got := byID[a.ID].SupersededBy; len(got) != 1 || got[0] != b.ID {
		t.Fatalf("A 应被 B 替换: %v", got)
	}
	if len(byID[b.ID].SupersededBy) != 0 {
		t.Fatalf("B 不应被替换: %v", byID[b.ID].SupersededBy)
	}
	if byID[a.ID].State != ValidationStatePassed || byID[b.ID].State != ValidationStateFrozen {
		t.Fatalf("派生状态应区分: %+v vs %+v", byID[a.ID], byID[b.ID])
	}
	if byID[a.ID].TrialCount != 1 || byID[a.ID].CandidateID != f.Candidate.ID {
		t.Fatalf("摘要字段应正确: %+v", byID[a.ID])
	}
	// 候选过滤
	if got, err := f.Store.List(ValidationFilter{CandidateID: f.Candidate.ID}); err != nil || len(got) != 2 {
		t.Fatalf("候选过滤 = %d/%v", len(got), err)
	}
	if got, err := f.Store.List(ValidationFilter{CandidateID: fcFreezeTestID}); err != nil || len(got) != 0 {
		t.Fatalf("未知候选过滤应空: %d/%v", len(got), err)
	}
	// 状态过滤
	if got, err := f.Store.List(ValidationFilter{State: ValidationStatePassed}); err != nil ||
		len(got) != 1 || got[0].ID != a.ID {
		t.Fatalf("passed 过滤 = %v/%v", got, err)
	}
	if got, err := f.Store.List(ValidationFilter{State: ValidationStateFrozen}); err != nil ||
		len(got) != 1 || got[0].ID != b.ID {
		t.Fatalf("frozen 过滤 = %v/%v", got, err)
	}
	// 证据等级过滤
	if got, err := f.Store.List(ValidationFilter{EvidenceClass: validationEvidenceProspective}); err != nil || len(got) != 0 {
		t.Fatalf("prospective 过滤应空: %d/%v", len(got), err)
	}
	if got, err := f.Store.List(ValidationFilter{EvidenceClass: validationEvidenceRetrospective}); err != nil || len(got) != 2 {
		t.Fatalf("retrospective 过滤 = %d/%v", len(got), err)
	}
	// 非法过滤拒绝
	if _, err := f.Store.List(ValidationFilter{State: "bogus"}); err == nil {
		t.Fatal("非法状态过滤应拒绝")
	}
	if _, err := f.Store.List(ValidationFilter{CandidateID: "fc_bad"}); err == nil {
		t.Fatal("非法候选 ID 过滤应拒绝")
	}
	if _, err := f.Store.List(ValidationFilter{EvidenceClass: "exploratory"}); err == nil {
		t.Fatal("exploratory 不是验证层证据等级，应拒绝")
	}
}

// TestValidationStoreEmptyAndMissing 空库与根目录缺失时 List 返回空、Get
// 返回 NotFound。
func TestValidationStoreEmptyAndMissing(t *testing.T) {
	f := newValidationStoreFixture(t)
	got, err := f.Store.List(ValidationFilter{})
	if err != nil || len(got) != 0 {
		t.Fatalf("空库 List 应为空: %d/%v", len(got), err)
	}
	if _, err := f.Store.Get(fvFreezeGhostID); !errors.Is(err, errValidationNotFound) {
		t.Fatalf("空库 Get 应 NotFound: %v", err)
	}
	missing := NewValidationStore(filepath.Join(t.TempDir(), "no-such-dir"))
	if got, err := missing.List(ValidationFilter{}); err != nil || len(got) != 0 {
		t.Fatalf("缺失根目录 List 应为空: %d/%v", len(got), err)
	}
	if _, err := missing.Get(fvFreezeGhostID); !errors.Is(err, errValidationNotFound) {
		t.Fatalf("缺失根目录 Get 应 NotFound: %v", err)
	}
}

// TestValidationStoreFailClosedOnTamper 冻结请求被篡改后 Get/List/SaveWindow
// 一律 fail closed（冻结 hash 重算不匹配），不静默跳过。
func TestValidationStoreFailClosedOnTamper(t *testing.T) {
	f := newValidationStoreFixture(t)
	vr, _, err := f.Store.Create(validCreateReq(f, testUUID2), f.deps())
	if err != nil {
		t.Fatal(err)
	}
	// 篡改冻结请求内容（不重算 hash）
	path := f.Store.requestPath(vr.ID)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	doc["frozenAt"] = "2000-01-01T00:00:00Z"
	tampered, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, tampered, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Store.Get(vr.ID); err == nil {
		t.Fatal("篡改后 Get 应报错（冻结 hash 不匹配）")
	}
	if _, err := f.Store.List(ValidationFilter{}); err == nil {
		t.Fatal("篡改后 List 应 fail closed")
	}
	if err := f.Store.SaveWindow(vr.ID, okWindowFor(vr.ID, 1)); err == nil {
		t.Fatal("篡改后 SaveWindow 应 fail closed")
	}
	if err := f.Store.Complete(vr.ID, passedReportFor(vr.ID)); err == nil {
		t.Fatal("篡改后 Complete 应 fail closed")
	}
}
