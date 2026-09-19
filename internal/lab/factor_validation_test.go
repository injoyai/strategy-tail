package lab

import (
	"math"
	"testing"
	"time"
)

// factor_validation_test.go 冻结验证领域层测试（计划 Task 7 Step 3）：
// freezeValidation 八项校验（候选 revision、证据 hash、v4 协议、因子快照、
// trial 账本完整性、purge 覆盖、证据等级时间关系、policy 数值）+ 状态机
// 与报告合同。

// 冻结测试固定 ID（全部满足各自受限正则）。
const (
	fcFreezeTestID  = "fc_20260917T150100000Z_99aabbcc"
	fcFreezeOtherID = "fc_20260917T150200000Z_88aaccee"
	trFreezeTestID  = "tr_20260917T151000000Z_11223344"
	trFreezeOtherID = "tr_20260917T151100000Z_55667788"
	anFreezeOtherID = "an_20260916T150000000Z_44556677"
	fvFreezeGhostID = "fv_20260101T000000000Z_abcdef01"
)

// freezeNow 冻结时刻：晚于发现期完成时间（2026-09-17T15:00+08:00），
// 也晚于发现期最后数据日（2025-02-03）。
func freezeNow() time.Time { return time.Date(2026, 9, 17, 16, 0, 0, 0, time.Local) }

// freezeFixture 构造合法冻结夹具：v4 报告、由报告推导的候选、同家族
// trial（因子快照取自报告）、指向二者的合法请求。
func freezeFixture(t *testing.T) (FactorCandidate, AnalysisReport, FactorTrial, CreateValidationRequest) {
	t.Helper()
	rep := *v4ReportForTest(t, testAnalysisID, "momentum", "2026-09-17T15:00:00+08:00")
	cand, err := candidateFromAnalysis(candidateReq(testUUID1, "动量候选",
		CandidateUse{Mode: "observe"}), &rep, time.Date(2026, 9, 17, 15, 1, 0, 0, time.Local))
	if err != nil {
		t.Fatal(err)
	}
	cand.ID = fcFreezeTestID
	cand.Revision = 1
	trial := FactorTrial{
		ID:           trFreezeTestID,
		FamilyID:     rep.Protocol.Trial.FamilyID,
		VariantID:    rep.Protocol.Trial.VariantID,
		AnalysisID:   testAnalysisID,
		Factor:       rep.Factor,
		ProtocolHash: rep.ProtocolHash,
		StartedAt:    "2026-09-17T15:10:00+08:00",
		Status:       TrialStatusRunning,
	}
	req := CreateValidationRequest{
		RequestID:            testUUID2,
		CandidateID:          cand.ID,
		CandidateRevision:    cand.Revision,
		EvidenceClass:        validationEvidenceRetrospective,
		DiscoveryAnalysisIDs: []string{testAnalysisID},
		TrialIDs:             []string{trFreezeTestID},
		Windows:              WalkForwardSpec{TrainYears: 3, TestYears: 1, StepYears: 1, PurgeDays: 20},
		Policy: ValidationPolicy{MinCoverage: 0.8, MinValidWindows: 3,
			RequiredDirectionRate: 0.6, Aggregation: validationAggregationWindowEqual},
	}
	return cand, rep, trial, req
}

// TestFreezeValidationSuccess 正常冻结：协议、研究协议绑定、trial 快照与
// 幂等键全部就位；ID 与 RequestHash 留给 Store 分配。
func TestFreezeValidationSuccess(t *testing.T) {
	cand, rep, trial, req := freezeFixture(t)
	vr, err := freezeValidation(cand, rep, []FactorTrial{trial}, req, freezeNow())
	if err != nil {
		t.Fatal(err)
	}
	if vr.SchemaVersion != validationSchemaVersion || vr.ID != "" || vr.RequestHash != "" {
		t.Fatalf("ID/RequestHash 应由 Store 分配: %+v", vr)
	}
	if vr.FrozenAt != freezeNow().UTC().Format(time.RFC3339) {
		t.Fatalf("FrozenAt = %q", vr.FrozenAt)
	}
	if vr.Protocol.EvidenceClass != validationEvidenceRetrospective ||
		vr.Protocol.CandidateID != cand.ID || vr.Protocol.CandidateRevision != 1 {
		t.Fatalf("Protocol = %+v", vr.Protocol)
	}
	if len(vr.Protocol.DiscoveryAnalysisIDs) != 1 || vr.Protocol.DiscoveryAnalysisIDs[0] != testAnalysisID {
		t.Fatalf("DiscoveryAnalysisIDs = %v", vr.Protocol.DiscoveryAnalysisIDs)
	}
	if len(vr.Protocol.TrialIDs) != 1 || vr.Protocol.TrialIDs[0] != trFreezeTestID {
		t.Fatalf("TrialIDs = %v", vr.Protocol.TrialIDs)
	}
	if vr.ResearchProtocolHash != rep.ProtocolHash {
		t.Fatalf("研究协议 hash 应与报告一致: %s vs %s", vr.ResearchProtocolHash, rep.ProtocolHash)
	}
	if vr.ResearchProtocol == nil || vr.ResearchProtocol.Trial.FamilyID != "momentum" {
		t.Fatalf("ResearchProtocol 应完整复制: %+v", vr.ResearchProtocol)
	}
	if len(vr.Trials) != 1 || vr.Trials[0].ID != trFreezeTestID {
		t.Fatalf("Trials = %+v", vr.Trials)
	}
	if vr.CreateRequestID != testUUID2 || vr.CreateRequestHash == "" {
		t.Fatalf("幂等键 = %q/%q", vr.CreateRequestID, vr.CreateRequestHash)
	}
	if vr.Supersedes != "" {
		t.Fatalf("Supersedes = %q", vr.Supersedes)
	}
}

// TestFreezeValidationCandidateMismatch 候选 revision 精确匹配。
func TestFreezeValidationCandidateMismatch(t *testing.T) {
	cand, rep, trial, req := freezeFixture(t)
	bad := req
	bad.CandidateRevision = 2
	if _, err := freezeValidation(cand, rep, []FactorTrial{trial}, bad, freezeNow()); err == nil {
		t.Fatal("revision 不匹配应拒绝")
	}
	bad = req
	bad.CandidateID = fcFreezeOtherID
	if _, err := freezeValidation(cand, rep, []FactorTrial{trial}, bad, freezeNow()); err == nil {
		t.Fatal("候选 ID 不匹配应拒绝")
	}
}

// TestFreezeValidationEvidenceHashMismatch 证据被篡改 → hash 重算失败。
func TestFreezeValidationEvidenceHashMismatch(t *testing.T) {
	cand, rep, trial, req := freezeFixture(t)
	rep.Stats.Mean += 1
	if _, err := freezeValidation(cand, rep, []FactorTrial{trial}, req, freezeNow()); err == nil {
		t.Fatal("证据哈希不匹配应拒绝冻结")
	}
}

// TestFreezeValidationRejectsV3Evidence v3 证据（无完整协议）不可冻结，
// 必须先以完整协议重新分析。
func TestFreezeValidationRejectsV3Evidence(t *testing.T) {
	rep := *analysisReportForTest(testAnalysisID, "momentum", "2026-09-17T15:00:00+08:00")
	cand, err := candidateFromAnalysis(candidateReq(testUUID1, "动量候选",
		CandidateUse{Mode: "observe"}), &rep, time.Date(2026, 9, 17, 15, 1, 0, 0, time.Local))
	if err != nil {
		t.Fatal(err)
	}
	cand.ID = fcFreezeTestID
	cand.Revision = 1
	trial := FactorTrial{
		ID: trFreezeTestID, FamilyID: "momentum", AnalysisID: testAnalysisID,
		Factor: rep.Factor, StartedAt: "2026-09-17T15:10:00+08:00", Status: TrialStatusRunning,
	}
	req := CreateValidationRequest{
		RequestID: testUUID2, CandidateID: cand.ID, CandidateRevision: 1,
		EvidenceClass:        validationEvidenceRetrospective,
		DiscoveryAnalysisIDs: []string{testAnalysisID},
		TrialIDs:             []string{trFreezeTestID},
		Windows:              WalkForwardSpec{TrainYears: 3, TestYears: 1, StepYears: 1, PurgeDays: 20},
		Policy:               ValidationPolicy{MinCoverage: 0.8, MinValidWindows: 3, RequiredDirectionRate: 0.6},
	}
	if _, err := freezeValidation(cand, rep, []FactorTrial{trial}, req, freezeNow()); err == nil {
		t.Fatal("v3 证据应拒绝冻结")
	}
}

// TestFreezeValidationFactorVersionMismatch 因子快照五字段全比对。
func TestFreezeValidationFactorVersionMismatch(t *testing.T) {
	cand, rep, trial, req := freezeFixture(t)
	cases := map[string]func(*FactorCandidate){
		"kind":    func(c *FactorCandidate) { c.Factor.Kind = "volatility" },
		"days":    func(c *FactorCandidate) { c.Factor.Days = 5 },
		"name":    func(c *FactorCandidate) { c.Factor.Name = "别的因子" },
		"unit":    func(c *FactorCandidate) { c.Factor.Unit = "名次" },
		"version": func(c *FactorCandidate) { c.Factor.ImplementationVersion = 2 },
	}
	for name, mut := range cases {
		bad := cand
		mut(&bad)
		if _, err := freezeValidation(bad, rep, []FactorTrial{trial}, req, freezeNow()); err == nil {
			t.Fatalf("因子快照 %s 不一致应拒绝", name)
		}
	}
}

// TestFreezeValidationTrialLedger trial 账本完整性：声明清单 = 账本清单、
// 同家族、每个发现期分析有 trial、trial 因子与候选一致、无重复。
func TestFreezeValidationTrialLedger(t *testing.T) {
	cand, rep, trial, req := freezeFixture(t)
	other := trial
	other.ID = trFreezeOtherID

	cases := []struct {
		note   string
		trials []FactorTrial
		mut    func(*CreateValidationRequest)
	}{
		{"清单不完整（账本多于声明）", []FactorTrial{trial, other}, nil},
		{"声明的 trial 不在账本", []FactorTrial{trial}, func(r *CreateValidationRequest) {
			r.TrialIDs = []string{trFreezeOtherID}
		}},
		// 声明清单与账本一致，才能命中家族校验而非清单长度校验。
		{"家族不一致", []FactorTrial{trial, func() FactorTrial {
			m := other
			m.FamilyID = "momentum-2"
			return m
		}()}, func(r *CreateValidationRequest) {
			r.TrialIDs = []string{trFreezeTestID, trFreezeOtherID}
		}},
		{"账本重复", []FactorTrial{trial, trial}, nil},
		{"发现期分析无 trial", []FactorTrial{trial}, func(r *CreateValidationRequest) {
			r.DiscoveryAnalysisIDs = []string{testAnalysisID, anFreezeOtherID}
		}},
		// 另一发现期分析有对应 trial，才能命中"候选证据分析必须在清单内"。
		{"发现期清单缺候选证据分析", []FactorTrial{trial, func() FactorTrial {
			m := other
			m.AnalysisID = anFreezeOtherID
			return m
		}()}, func(r *CreateValidationRequest) {
			r.TrialIDs = []string{trFreezeTestID, trFreezeOtherID}
			r.DiscoveryAnalysisIDs = []string{anFreezeOtherID}
		}},
		// 声明清单与账本一致，才能命中 trial 因子校验。
		{"trial 因子与候选不一致", []FactorTrial{trial, func() FactorTrial {
			m := other
			m.Factor.Days = 5
			return m
		}()}, func(r *CreateValidationRequest) {
			r.TrialIDs = []string{trFreezeTestID, trFreezeOtherID}
		}},
	}
	for _, c := range cases {
		r := req
		if c.mut != nil {
			c.mut(&r)
		}
		if _, err := freezeValidation(cand, rep, c.trials, r, freezeNow()); err == nil {
			t.Fatalf("%s 应拒绝", c.note)
		}
	}
}

// TestFreezeValidationPurgeDays purgeDays 必须覆盖最大收益周期。
func TestFreezeValidationPurgeDays(t *testing.T) {
	cand, rep, trial, req := freezeFixture(t)
	// 协议 Horizons = [1,5,10,20]，max = 20。
	bad := req
	bad.Windows.PurgeDays = 19
	if _, err := freezeValidation(cand, rep, []FactorTrial{trial}, bad, freezeNow()); err == nil {
		t.Fatal("purgeDays 19 < max Horizon 20 应拒绝")
	}
}

// TestFreezeValidationEvidenceClassTime 证据等级与冻结时间关系。
func TestFreezeValidationEvidenceClassTime(t *testing.T) {
	cand, rep, trial, req := freezeFixture(t)
	// retrospective：冻结时间早于发现期完成时间 → 拒绝。
	if _, err := freezeValidation(cand, rep, []FactorTrial{trial}, req,
		time.Date(2026, 9, 17, 14, 0, 0, 0, time.Local)); err == nil {
		t.Fatal("冻结时间早于发现期完成时间应拒绝")
	}
	// prospective：冻结时间不晚于发现期最后数据日 → 拒绝。
	p := req
	p.EvidenceClass = validationEvidenceProspective
	early := time.Date(2025, 2, 3, 23, 59, 59, 0, time.Local)
	if _, err := freezeValidation(cand, rep, []FactorTrial{trial}, p, early); err == nil {
		t.Fatal("前瞻冻结未晚于最后数据日应拒绝")
	}
	// prospective：晚于最后数据日 → 允许（retrospective 证据也可前瞻累计）。
	if _, err := freezeValidation(cand, rep, []FactorTrial{trial}, p, freezeNow()); err != nil {
		t.Fatalf("合法前瞻冻结不应报错: %v", err)
	}
}

// TestFreezeValidationPolicyInvalid policy 数值非法拒绝（第 8 项校验）。
func TestFreezeValidationPolicyInvalid(t *testing.T) {
	cand, rep, trial, req := freezeFixture(t)
	cases := map[string]func(*ValidationPolicy){
		"minCoverage 越界":      func(p *ValidationPolicy) { p.MinCoverage = 1.5 },
		"directionRate 为 NaN": func(p *ValidationPolicy) { p.RequiredDirectionRate = math.NaN() },
		"minValidWindows 为 0": func(p *ValidationPolicy) { p.MinValidWindows = 0 },
		"maxIcDecayRatio 为 0": func(p *ValidationPolicy) { z := 0.0; p.MaxICDecayRatio = &z },
		"聚合口径非法":              func(p *ValidationPolicy) { p.Aggregation = "best_of" },
	}
	for note, mut := range cases {
		bad := req
		mut(&bad.Policy)
		if _, err := freezeValidation(cand, rep, []FactorTrial{trial}, bad, freezeNow()); err == nil {
			t.Fatalf("policy %s 应拒绝", note)
		}
	}
}

// TestFreezeValidationRequestNormalization 请求规范化合同：清单去重、
// 空清单、Supersedes 校验与透传。
func TestFreezeValidationRequestNormalization(t *testing.T) {
	cand, rep, trial, req := freezeFixture(t)
	// 清单重复拒绝。
	dup := req
	dup.TrialIDs = []string{trFreezeTestID, trFreezeTestID}
	if _, err := freezeValidation(cand, rep, []FactorTrial{trial}, dup, freezeNow()); err == nil {
		t.Fatal("trial 清单重复应拒绝")
	}
	// Supersedes 非法拒绝。
	badSup := req
	badSup.Supersedes = "../evil"
	if _, err := freezeValidation(cand, rep, []FactorTrial{trial}, badSup, freezeNow()); err == nil {
		t.Fatal("非法 Supersedes 应拒绝")
	}
	// 合法 Supersedes 透传到冻结记录。
	okSup := req
	okSup.Supersedes = fvFreezeGhostID
	vr, err := freezeValidation(cand, rep, []FactorTrial{trial}, okSup, freezeNow())
	if err != nil {
		t.Fatal(err)
	}
	if vr.Supersedes != fvFreezeGhostID {
		t.Fatalf("Supersedes 应透传: %q", vr.Supersedes)
	}
	// 空聚合口径归一默认值。
	def := req
	def.Policy.Aggregation = ""
	vr, err = freezeValidation(cand, rep, []FactorTrial{trial}, def, freezeNow())
	if err != nil {
		t.Fatal(err)
	}
	if vr.Protocol.Policy.Aggregation != validationAggregationWindowEqual {
		t.Fatalf("空聚合口径应归一: %q", vr.Protocol.Policy.Aggregation)
	}
}

// TestFreezeValidationLedgerSnapshotSuperset 账本与声明完全一致时冻结成功，
// trial 快照按 ID 排序且包含 running。
func TestFreezeValidationLedgerSnapshotSuperset(t *testing.T) {
	cand, rep, trial, req := freezeFixture(t)
	other := trial
	other.ID = trFreezeOtherID
	// 声明两个 trial 且账本恰为这两个（含 running）→ 完整账本快照。
	req.TrialIDs = []string{trFreezeTestID, trFreezeOtherID}
	vr, err := freezeValidation(cand, rep, []FactorTrial{other, trial}, req, freezeNow())
	if err != nil {
		t.Fatal(err)
	}
	if len(vr.Trials) != 2 || vr.Trials[0].ID != trFreezeTestID || vr.Trials[1].ID != trFreezeOtherID {
		t.Fatalf("trial 快照应按 ID 升序: %+v", vr.Trials)
	}
	if vr.Trials[0].Status != TrialStatusRunning {
		t.Fatalf("running trial 必须保留（防生存者偏差）: %+v", vr.Trials[0])
	}
}

// TestValidationTransition 状态机纯函数：仅 frozen→running 与 running→终态。
func TestValidationTransition(t *testing.T) {
	if err := validationTransition(ValidationStateFrozen, ValidationStateRunning); err != nil {
		t.Fatalf("frozen→running 应合法: %v", err)
	}
	for _, to := range []ValidationState{ValidationStatePassed, ValidationStateFailed,
		ValidationStateInsufficient, ValidationStateError} {
		if err := validationTransition(ValidationStateRunning, to); err != nil {
			t.Fatalf("running→%s 应合法: %v", to, err)
		}
	}
	for _, c := range [][2]ValidationState{
		{ValidationStateFrozen, ValidationStatePassed},
		{ValidationStateFrozen, ValidationStateFrozen},
		{ValidationStateRunning, ValidationStateRunning},
		{ValidationStatePassed, ValidationStateFailed},
		{ValidationStateFailed, ValidationStatePassed},
	} {
		if err := validationTransition(c[0], c[1]); err == nil {
			t.Fatalf("%s→%s 应拒绝", c[0], c[1])
		}
	}
}

// TestValidationReportValidate 报告合同：终态白名单、Verdict 一致、checks 非空。
func TestValidationReportValidate(t *testing.T) {
	good := FactorValidationReport{
		SchemaVersion: validationSchemaVersion, ValidationID: fvFreezeGhostID,
		FinishedAt: "2026-09-18T10:00:00+08:00",
		State:      ValidationStatePassed, Verdict: ValidationVerdictPassed,
		Checks: []ValidationCheck{{Name: "direction_rate", Expected: ">=0.6", Actual: "0.8", Result: "pass"}},
	}
	if err := good.validate(); err != nil {
		t.Fatalf("合法报告不应报错: %v", err)
	}
	mut := func(f func(*FactorValidationReport)) FactorValidationReport {
		r := good
		f(&r)
		return r
	}
	for _, c := range []struct {
		note string
		f    func(*FactorValidationReport)
	}{
		{"schema 版本未知", func(r *FactorValidationReport) { r.SchemaVersion = 99 }},
		{"ID 非法", func(r *FactorValidationReport) { r.ValidationID = "../evil" }},
		{"状态非终态", func(r *FactorValidationReport) { r.State = ValidationStateRunning }},
		{"verdict 不一致", func(r *FactorValidationReport) { r.Verdict = ValidationVerdictFailed }},
		{"finishedAt 为空", func(r *FactorValidationReport) { r.FinishedAt = "" }},
		{"checks 为空", func(r *FactorValidationReport) { r.Checks = nil }},
		{"check result 非法", func(r *FactorValidationReport) { r.Checks[0].Result = "maybe" }},
	} {
		if err := mut(c.f).validate(); err == nil {
			t.Fatalf("%s 应拒绝", c.note)
		}
	}
}
