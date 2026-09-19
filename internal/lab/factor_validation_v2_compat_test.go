package lab

import (
	"strings"
	"testing"
)

// factor_validation_v2_compat_test.go v2 Task 0 兼容夹具（计划
// 2026-09-18-multifactor-portfolio-v2 §Task 0）：用真实链路（v4 分析 →
// 候选 → trial → freezeValidation → ValidationStore.Create/SaveWindow/
// Complete）固定三种典型验证场景——passed、insufficient、exploratory 输入。
//
// 目的：
//  1. 证明 v1 验证层满足 v2 准入条件 1/2/5/6（稳定引用、t 收盘信号/t+1 开盘
//     成交标签、四态终态、证据等级不可升格），v2 组合层可直接消费；
//  2. 为每个夹具记录七项元数据（candidate revision、factor instance、
//     implementation version、direction、evidence class、data snapshot、hash），
//     形成逐项准入表（docs/superpowers/plans/2026-09-18-multifactor-portfolio-v2-task0-admission.md）；
//  3. 旧版本/缺字段路径返回明确 unsupported/insufficient 错误，不 panic、
//     不补造事实（exploratory 夹具的拒绝路径即准入条件 6 的反向验证）。
//
// 适配层契约：internal/portfolioresearch（v2 Task 1 创建）只允许消费
// ValidationView / ValidationSummary 派生字段，禁止直接解析 v1 JSON。

// v2CompatFixtureMetadata 夹具元数据快照（Task 0 动作 2 的七项逐项记录）。
type v2CompatFixtureMetadata struct {
	Scenario              string // 夹具场景名
	CandidateRevision     int    // 冻结的候选 revision
	FactorKind            string // 因子实例 kind
	FactorDays            int    // 因子实例 days
	FactorName            string // 因子实例 name
	FactorUnit            string // 因子实例 unit
	ImplementationVersion int    // 因子实现版本
	Direction             string // 冻结预期方向（研究协议 Hypothesis.ExpectedDirection）
	EvidenceClass         string // 验证证据等级（retrospective | prospective）或分析层 exploratory
	DataPriceSource       string // 数据快照：价格来源
	DataPriceVersion      string // 数据快照：价格数据版本
	DataPITState          string // 数据快照：PIT 状态
	ResearchProtocolHash  string // 研究协议 hash（绑定股票池/数据/标签版本）
	RequestHash           string // 冻结内容 hash（空字符串表示未冻结——拒绝路径）
}

// collectViewMetadata 从冻结请求提取七项元数据（验证层夹具用）。
func collectViewMetadata(scenario string, view ValidationView) v2CompatFixtureMetadata {
	m := v2CompatFixtureMetadata{
		Scenario:              scenario,
		CandidateRevision:     view.Request.Protocol.CandidateRevision,
		FactorKind:            view.Request.Candidate.Factor.Kind,
		FactorDays:            view.Request.Candidate.Factor.Days,
		FactorName:            view.Request.Candidate.Factor.Name,
		FactorUnit:            view.Request.Candidate.Factor.Unit,
		ImplementationVersion: view.Request.Candidate.Factor.ImplementationVersion,
		EvidenceClass:         view.Request.Protocol.EvidenceClass,
		RequestHash:           view.Request.RequestHash,
	}
	if p := view.Request.ResearchProtocol; p != nil {
		m.Direction = p.Hypothesis.ExpectedDirection
		m.DataPriceSource = p.Data.PriceSource
		m.DataPriceVersion = p.Data.PriceVersion
		m.DataPITState = p.Data.PITState
		m.ResearchProtocolHash = view.Request.ResearchProtocolHash
	}
	return m
}

// assertMetadataComplete 断言七项元数据全部可读且非空（hash 冻结后必非空）。
// requireRequestHash=false 用于 exploratory 拒绝路径：未冻结无 request hash，
// 该场景的 hash 契约由 researchProtocolHash 承担。
func assertMetadataComplete(t *testing.T, m v2CompatFixtureMetadata, requireRequestHash bool) {
	t.Helper()
	if m.CandidateRevision < 1 {
		t.Fatalf("[%s] candidate revision 缺失: %+v", m.Scenario, m)
	}
	if m.FactorKind == "" || m.FactorName == "" || m.FactorUnit == "" || m.FactorDays <= 0 {
		t.Fatalf("[%s] factor instance 缺失: %+v", m.Scenario, m)
	}
	if m.ImplementationVersion < 1 {
		t.Fatalf("[%s] implementation version 缺失: %+v", m.Scenario, m)
	}
	if m.Direction == "" {
		t.Fatalf("[%s] direction 缺失: %+v", m.Scenario, m)
	}
	if m.EvidenceClass == "" {
		t.Fatalf("[%s] evidence class 缺失: %+v", m.Scenario, m)
	}
	if m.DataPriceSource == "" || m.DataPriceVersion == "" || m.DataPITState == "" {
		t.Fatalf("[%s] data snapshot 缺失: %+v", m.Scenario, m)
	}
	if m.ResearchProtocolHash == "" {
		t.Fatalf("[%s] research protocol hash 缺失: %+v", m.Scenario, m)
	}
	if requireRequestHash && m.RequestHash == "" {
		t.Fatalf("[%s] request hash 缺失（冻结记录必须带 hash）: %+v", m.Scenario, m)
	}
}

// TestCompatV2ValidationPassedFixture passed 夹具：真实链路冻结 → 窗口进度
// → passed 终态。断言七项元数据齐全，Get 派生状态一致，终态报告不可覆盖。
func TestCompatV2ValidationPassedFixture(t *testing.T) {
	f := newValidationStoreFixture(t)
	vr, created, err := f.Store.Create(validCreateReq(f, testUUID1), f.deps())
	if err != nil || !created {
		t.Fatalf("冻结应成功: created=%v, err=%v", created, err)
	}
	if err := f.Store.SaveWindow(vr.ID, okWindowFor(vr.ID, 1)); err != nil {
		t.Fatal(err)
	}
	if err := f.Store.Complete(vr.ID, passedReportFor(vr.ID)); err != nil {
		t.Fatal(err)
	}
	view, err := f.Store.Get(vr.ID)
	if err != nil {
		t.Fatal(err)
	}
	if view.State != ValidationStatePassed || view.Report == nil || view.Report.State != ValidationStatePassed {
		t.Fatalf("派生状态应为 passed: state=%q, report=%+v", view.State, view.Report)
	}
	// 元数据快照（Task 0 动作 2）：七项逐一记录并断言。
	m := collectViewMetadata("passed", view)
	assertMetadataComplete(t, m, true)
	if m.EvidenceClass != validationEvidenceRetrospective {
		t.Fatalf("passed 夹具证据等级应为 retrospective: %q", m.EvidenceClass)
	}
	// 标签契约（准入条件 2）：冻结协议必须为 t 收盘信号/t+1 开盘成交。
	if view.Request.ResearchProtocol.Labels.Kind != labelKindNextOpenToClose {
		t.Fatalf("冻结标签应为 next_open_to_close: %q", view.Request.ResearchProtocol.Labels.Kind)
	}
	// 一次写入：完成后再写窗口或覆盖报告必须拒绝。
	if err := f.Store.SaveWindow(vr.ID, okWindowFor(vr.ID, 2)); err == nil {
		t.Fatalf("完成后写窗口应被拒绝")
	}
	if err := f.Store.Complete(vr.ID, passedReportFor(vr.ID)); err == nil {
		t.Fatalf("完成后覆盖报告应被拒绝（errValidationCompleted）")
	}
}

// TestCompatV2ValidationInsufficientFixture insufficient 夹具：窗口数据不足
// → 终态 insufficient。断言逐窗口披露非 ok 原因、窗口计数披露、Message 说明，
// 且七项元数据在非 passed 终态下同样可读。
func TestCompatV2ValidationInsufficientFixture(t *testing.T) {
	f := newValidationStoreFixture(t)
	vr, created, err := f.Store.Create(validCreateReq(f, testUUID2), f.deps())
	if err != nil || !created {
		t.Fatalf("冻结应成功: created=%v, err=%v", created, err)
	}
	// 两个窗口均数据不足：保留窗口记录并逐窗口披露原因（不补造统计）。
	for i := 1; i <= 2; i++ {
		w := okWindowFor(vr.ID, i)
		w.State = ValidationWindowInsufficient
		w.Message = "测试窗口有效观察日不足，统计无法计算"
		w.Observations = 0
		w.Horizons = nil
		if err := f.Store.SaveWindow(vr.ID, w); err != nil {
			t.Fatal(err)
		}
	}
	report := FactorValidationReport{
		SchemaVersion: validationSchemaVersion,
		ValidationID:  vr.ID,
		FinishedAt:    "2026-09-18T00:00:00Z",
		State:         ValidationStateInsufficient,
		Verdict:       ValidationVerdictInsufficient,
		Checks: []ValidationCheck{
			{Name: "min_valid_windows", Expected: ">=3", Actual: "0", Result: "unknown",
				Reason: "有效窗口不足，门禁无法评估"},
		},
		WindowCount: 2, ValidWindowCount: 0, InsufficientWindowCount: 2,
		Message: "全部测试窗口数据不足，无法给出通过/失败结论",
	}
	if err := f.Store.Complete(vr.ID, report); err != nil {
		t.Fatal(err)
	}
	view, err := f.Store.Get(vr.ID)
	if err != nil {
		t.Fatal(err)
	}
	if view.State != ValidationStateInsufficient || view.Report == nil {
		t.Fatalf("派生状态应为 insufficient: state=%q", view.State)
	}
	if view.Report.InsufficientWindowCount != 2 || view.Report.ValidWindowCount != 0 {
		t.Fatalf("窗口计数披露不符: %+v", view.Report)
	}
	if view.Report.Message == "" {
		t.Fatalf("insufficient 必须披露原因")
	}
	if len(view.Windows) != 2 {
		t.Fatalf("非 ok 窗口必须保留: %+v", view.Windows)
	}
	for _, w := range view.Windows {
		if w.State != ValidationWindowInsufficient || w.Message == "" || len(w.Horizons) != 0 {
			t.Fatalf("窗口 %d 应为 insufficient 且无补造统计: %+v", w.Index, w)
		}
	}
	assertMetadataComplete(t, collectViewMetadata("insufficient", view), true)
}

// TestCompatV2ValidationExploratoryInputFixture exploratory 输入夹具：发现期
// v4 报告因 current_static 股票池派生 evidenceClass=exploratory → retrospective
// 冻结必须被明确拒绝（准入条件 6：证据等级不可被下游提升）。断言：显式错误
// 信息、不 panic、不产生任何验证记录、七项元数据在分析层仍可读。
func TestCompatV2ValidationExploratoryInputFixture(t *testing.T) {
	candidates := newTestCandidateStore(t)
	trials := NewTrialStore(t.TempDir())
	analyses := NewAnalysisStore(t.TempDir())
	store := NewValidationStore(t.TempDir())

	// 构造 exploratory v4 报告：完整协议但股票池为当前静态池。
	p := validResearchProtocol()
	p.Universe.Mode = universeCurrentStatic
	if err := p.Validate(); err != nil {
		t.Fatalf("current_static 协议本身应合法: %v", err)
	}
	rep := analysisReportForTest(testAnalysisID, "momentum", "2026-09-17T15:00:00+08:00")
	rep.AnalysisVersion = analysisVersionMaxKnown
	h, err := protocolHash(p)
	if err != nil {
		t.Fatal(err)
	}
	rep.Protocol = &p
	rep.ProtocolHash = h
	rep.EvidenceClass = deriveEvidenceClass(p)
	rep.Horizons = append([]int(nil), p.Labels.Horizons...)
	rep.Window = p.Labels.Horizons[0]
	if rep.EvidenceClass != evidenceExploratory {
		t.Fatalf("current_static 应派生 exploratory: %q", rep.EvidenceClass)
	}
	if err := analyses.Save(rep); err != nil {
		t.Fatal(err)
	}
	cand, _, err := candidates.Create(observeReq(testUUID1, "探索候选"), rep)
	if err != nil {
		t.Fatalf("exploratory 证据可登记候选（观察用途）: %v", err)
	}
	trial, err := trials.Start(*rep.Protocol, rep.Factor, testAnalysisID)
	if err != nil {
		t.Fatal(err)
	}

	// retrospective 冻结必须被拒绝：显式错误、不 panic、不产生记录。
	req := CreateValidationRequest{
		RequestID:            testUUID2,
		CandidateID:          cand.ID,
		CandidateRevision:    cand.Revision,
		EvidenceClass:        validationEvidenceRetrospective,
		DiscoveryAnalysisIDs: []string{testAnalysisID},
		TrialIDs:             []string{trial.ID},
		Windows:              WalkForwardSpec{TrainYears: 3, TestYears: 1, StepYears: 1, PurgeDays: 20},
		Policy: ValidationPolicy{MinCoverage: 0.8, MinValidWindows: 3,
			RequiredDirectionRate: 0.6, Aggregation: validationAggregationWindowEqual},
	}
	deps := FreezeDependencies{Candidates: candidates, Trials: trials, Analyses: analyses}
	_, _, err = store.Create(req, deps)
	if err == nil {
		t.Fatalf("exploratory 证据冻结 retrospective 验证必须被拒绝")
	}
	if !strings.Contains(err.Error(), "retrospective") {
		t.Fatalf("错误信息应明确指向 retrospective 不可用: %v", err)
	}
	sums, err := store.List(ValidationFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(sums) != 0 {
		t.Fatalf("拒绝路径不得产生验证记录: %+v", sums)
	}

	// 七项元数据在分析层同样可读（candidate revision/因子实例/实现版本/
	// 方向/证据等级/数据快照/hash），供逐项准入表记录。
	m := v2CompatFixtureMetadata{
		Scenario:              "exploratory-input",
		CandidateRevision:     cand.Revision,
		FactorKind:            cand.Factor.Kind,
		FactorDays:            cand.Factor.Days,
		FactorName:            cand.Factor.Name,
		FactorUnit:            cand.Factor.Unit,
		ImplementationVersion: cand.Factor.ImplementationVersion,
		Direction:             p.Hypothesis.ExpectedDirection,
		EvidenceClass:         rep.EvidenceClass,
		DataPriceSource:       p.Data.PriceSource,
		DataPriceVersion:      p.Data.PriceVersion,
		DataPITState:          p.Data.PITState,
		ResearchProtocolHash:  h,
		RequestHash:           "", // 未冻结，无 request hash（拒绝路径）
	}
	assertMetadataComplete(t, m, false)
}
