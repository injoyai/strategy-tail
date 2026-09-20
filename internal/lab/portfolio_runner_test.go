package lab

import (
	"encoding/csv"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	common "github.com/injoyai/strategy-tail"
	"github.com/injoyai/strategy-tail/internal/portfolioresearch"
	"github.com/injoyai/strategy-tail/lib/extend"
	"github.com/injoyai/strategy-tail/researchdata"
)

// portfolio_runner_test.go v2 Task 9 组合任务执行器测试：
// 完整实验运行（产物 + manifest）、取消、忙碌冲突、状态进度契约、
// 重启后从 Store 恢复、验证运行（Evaluate 结论）与证据降级（exploratory
// 不可能 passed）。

// portfolioRunFixture 组合运行测试夹具：v1 验证 → 模型库 + 实验库 + 验证库
// + 注入静态股票池与伪造日K 的 Runner（完整可运行链路）。
type portfolioRunFixture struct {
	runner       *Runner
	models       *FactorModelStore
	experiments  *PortfolioExperimentStore
	validations  *PortfolioValidationStore
	model        portfolioresearch.FactorModel
	modelRoot    string
	expRecords   string
	expArtifacts string
	pvRoot       string
}

// newPortfolioRunFixture 构造完整可运行的组合链路。
func newPortfolioRunFixture(t *testing.T) *portfolioRunFixture {
	t.Helper()
	dir := t.TempDir()
	t.Chdir(dir)
	old := common.Pull
	common.Pull = &extend.PullKline{Config: extend.PullKlineConfig{Dir: dir}}
	t.Cleanup(func() { common.Pull = old })
	writePortfolioMomentumData(t, dir)

	fm := newFactorModelFixture(t)
	m, created, err := fm.Store.Create(validModelReq(fm.ValidationID, testUUID1), fm.Input)
	if err != nil || !created {
		t.Fatalf("创建模型 = %v/%v", created, err)
	}
	expRecords, expArtifacts := t.TempDir(), t.TempDir()
	pvRoot := t.TempDir()
	experiments := NewPortfolioExperimentStore(expRecords, expArtifacts)
	validations := NewPortfolioValidationStore(pvRoot)

	r := NewRunner()
	r.universe = researchdata.NewStaticUniverse(researchdata.StaticUniverseConfig{
		ID: "test-static", Source: "test", Codes: func() []string {
			return []string{"sh600001", "sh600002"}
		},
	})
	r.ConfigurePortfolio(fm.Store, experiments, validations)
	return &portfolioRunFixture{
		runner: r, models: fm.Store, experiments: experiments, validations: validations,
		model: m, modelRoot: fm.Root, expRecords: expRecords,
		expArtifacts: expArtifacts, pvRoot: pvRoot,
	}
}

// writePortfolioMomentumData 一升一降两票（同 e2e 动量夹具）：名次恒定，
// 组合等权持有净收益为正。
func writePortfolioMomentumData(t *testing.T, dir string) {
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

// portfolioExperimentRequest 指向给定模型的合法实验请求（研究区间与测试
// 数据一致：2025-01-01 ~ 2025-01-20）。
func portfolioExperimentRequest(m portfolioresearch.FactorModel, requestID string) CreatePortfolioExperimentRequest {
	return CreatePortfolioExperimentRequest{
		RequestID:     requestID,
		FamilyID:      "portfolio-run-test",
		ModelID:       m.ModelID,
		ModelRevision: m.Revision,
		ModelHash:     m.ModelHash,
		StudyRange:    DateRange{Start: "2025-01-01", End: "2025-01-20"},
		Variant:       ParameterVariant{Name: "top2-equal", Desc: "测试变体"},
		VariantSource: ExperimentVariantDeclared,
		DataSnapshot: portfolioresearch.DataSnapshot{
			UniverseMode: "historical_membership", PriceSource: "local-klines",
			PriceVersion: "2026-01", PITState: "verified",
		},
		CodeVersion:   "v2-task9-test",
		EvidenceClass: m.EvidenceClass,
		TrialCounted:  true,
		CountReason:   "测试变体，计入试验次数",
	}
}

// waitPortfolioDone 轮询 Runner 状态直到 done/error（同既有 waitServerDone 模式）。
func waitPortfolioDone(t *testing.T, r *Runner) map[string]any {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for {
		st := r.Status()
		if st["state"] == "done" || st["state"] == "error" {
			return st
		}
		if time.Now().After(deadline) {
			t.Fatalf("组合任务超时未完成: %v", st)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// TestPortfolioExperimentRunCompletes 完整实验运行：产物五类落盘、Store 终态
// completed 且携带 manifest 与报告 hash，report.json 可反序列化为组合报告。
func TestPortfolioExperimentRunCompletes(t *testing.T) {
	f := newPortfolioRunFixture(t)
	exp, created, err := f.experiments.Create(portfolioExperimentRequest(f.model, testUUID1))
	if err != nil || !created {
		t.Fatalf("创建实验 = %v/%v", created, err)
	}
	if err := f.runner.StartPortfolioExperiment(exp.ExperimentID); err != nil {
		t.Fatalf("启动: %v", err)
	}
	st := waitPortfolioDone(t, f.runner)
	if st["state"] != "done" {
		t.Fatalf("组合任务应为 done: %v", st)
	}
	got, err := f.experiments.Get(exp.ExperimentID)
	if err != nil {
		t.Fatalf("读取实验: %v", err)
	}
	if got.Status != portfolioresearch.RunStateCompleted {
		t.Fatalf("终态 = %q, want completed", got.Status)
	}
	if got.Manifest == nil || len(got.Manifest.Entries) != 5 {
		t.Fatalf("completed 必须携带五类产物 manifest: %+v", got.Manifest)
	}
	if got.ReportHash == "" || got.ReportPath != "report.json" {
		t.Fatalf("报告 hash/路径异常: %q/%q", got.ReportHash, got.ReportPath)
	}
	// 五类产物磁盘存在
	for _, name := range []string{"report.json", "nav.csv", "orders.csv", "trades.csv", "holdings.csv"} {
		if _, err := os.Stat(filepath.Join(f.expArtifacts, exp.ExperimentID, name)); err != nil {
			t.Fatalf("产物 %s 未落盘: %v", name, err)
		}
	}
	// report.json 可反序列化且携带核心语义
	data, err := os.ReadFile(filepath.Join(f.expArtifacts, exp.ExperimentID, "report.json"))
	if err != nil {
		t.Fatalf("读 report.json: %v", err)
	}
	var rep PortfolioReport
	if err := json.Unmarshal(data, &rep); err != nil {
		t.Fatalf("report.json 反序列化失败: %v", err)
	}
	if rep.ExperimentID != exp.ExperimentID || rep.ModelHash != exp.ModelHash ||
		rep.EvidenceClass != exp.EvidenceClass {
		t.Fatalf("报告语义字段异常: %+v", rep)
	}
	if rep.Metrics.TradingDays <= 0 || rep.Attribution.NetReturn == 0 {
		t.Fatalf("报告应含指标与归因: metrics=%+v attribution=%+v", rep.Metrics, rep.Attribution)
	}
}

// TestPortfolioCancel 取消后 Store 终态 cancelled（设计 §14：保存 cancelled 与
// 已有诊断，不发布正式报告）。
func TestPortfolioCancel(t *testing.T) {
	f := newPortfolioRunFixture(t)
	exp, _, err := f.experiments.Create(portfolioExperimentRequest(f.model, testUUID1))
	if err != nil {
		t.Fatalf("创建实验: %v", err)
	}
	if err := f.runner.StartPortfolioExperiment(exp.ExperimentID); err != nil {
		t.Fatalf("启动: %v", err)
	}
	f.runner.Stop()
	waitPortfolioDone(t, f.runner)
	got, err := f.experiments.Get(exp.ExperimentID)
	if err != nil {
		t.Fatalf("读取实验: %v", err)
	}
	if got.Status != portfolioresearch.RunStateCancelled {
		t.Fatalf("取消后终态 = %q, want cancelled", got.Status)
	}
	if got.Error == "" {
		t.Fatal("取消记录应携带诊断信息")
	}
	if got.Manifest != nil {
		t.Fatal("取消不得发布正式产物清单")
	}
}

// TestPortfolioBusyConflict 任务互斥：已有任务占用时启动组合任务报错（409 语义）。
func TestPortfolioBusyConflict(t *testing.T) {
	f := newPortfolioRunFixture(t)
	exp, _, err := f.experiments.Create(portfolioExperimentRequest(f.model, testUUID1))
	if err != nil {
		t.Fatalf("创建实验: %v", err)
	}
	f.runner.mu.Lock()
	defer f.runner.mu.Unlock()
	if err := f.runner.StartPortfolioExperiment(exp.ExperimentID); err == nil {
		t.Fatal("已有任务运行时启动应报错（忙碌冲突）")
	}
}

// TestPortfolioStatusProgress /api/status 组合任务契约：task=portfolio、
// phase 非空、runId 指向当前实验、done/total 表达进度（总量未知为 -1）。
func TestPortfolioStatusProgress(t *testing.T) {
	f := newPortfolioRunFixture(t)
	exp, _, err := f.experiments.Create(portfolioExperimentRequest(f.model, testUUID1))
	if err != nil {
		t.Fatalf("创建实验: %v", err)
	}
	if err := f.runner.StartPortfolioExperiment(exp.ExperimentID); err != nil {
		t.Fatalf("启动: %v", err)
	}
	st := waitPortfolioDone(t, f.runner)
	if st["task"] != "portfolio" {
		t.Fatalf("task = %v, want portfolio", st["task"])
	}
	if st["phase"] == "" {
		t.Fatalf("组合任务必须报告阶段: %v", st)
	}
	if st["runId"] != exp.ExperimentID {
		t.Fatalf("runId = %v, want %s", st["runId"], exp.ExperimentID)
	}
	if _, ok := st["done"]; !ok {
		t.Fatalf("组合任务必须报告 done: %v", st)
	}
	if _, ok := st["total"]; !ok {
		t.Fatalf("组合任务必须报告 total: %v", st)
	}
	// 总量未知（done=-1）或已知（0<=done<=total）二选一
	if d, ok := st["done"].(float64); ok {
		if t2, ok2 := st["total"].(float64); ok2 && t2 > 0 && (d < 0 || d > t2) {
			t.Fatalf("进度越界: done=%v total=%v", d, t2)
		}
	}
}

// TestPortfolioRestartRecovery 重启后从持久 Store 恢复：同一根目录新建
// 存储与 Runner，实验详情（completed + manifest + 报告 hash）不依赖内存。
func TestPortfolioRestartRecovery(t *testing.T) {
	f := newPortfolioRunFixture(t)
	exp, _, err := f.experiments.Create(portfolioExperimentRequest(f.model, testUUID1))
	if err != nil {
		t.Fatalf("创建实验: %v", err)
	}
	if err := f.runner.StartPortfolioExperiment(exp.ExperimentID); err != nil {
		t.Fatalf("启动: %v", err)
	}
	waitPortfolioDone(t, f.runner)

	// 等价重启：同一根目录新建全部存储与 Runner
	models2 := NewFactorModelStore(f.modelRoot)
	exps2 := NewPortfolioExperimentStore(f.expRecords, f.expArtifacts)
	pvs2 := NewPortfolioValidationStore(f.pvRoot)
	got, err := exps2.Get(exp.ExperimentID)
	if err != nil {
		t.Fatalf("重启后读取实验: %v", err)
	}
	if got.Status != portfolioresearch.RunStateCompleted || got.Manifest == nil || got.ReportHash == "" {
		t.Fatalf("重启后终态应从 Store 恢复: %+v", got)
	}
	// 产物哈希深度校验也通过（completed 读取重算磁盘 hash）
	paths, err := exps2.ArtifactPaths(exp.ExperimentID)
	if err != nil || len(paths) != 5 {
		t.Fatalf("重启后产物索引 = %v/%v", paths, err)
	}
	_ = models2
	_ = pvs2
}

// portfolioValidationSpecFor 指向给定模型的验证规格（5 训练/5 测试/步长 5，
// 20 个交易日 → 3 个非重叠窗口）。
func portfolioValidationSpecFor(m portfolioresearch.FactorModel) portfolioresearch.PortfolioValidationSpec {
	return portfolioresearch.PortfolioValidationSpec{
		ModelRef:   portfolioresearch.ModelRef{ModelID: m.ModelID, Revision: m.Revision, Hash: m.ModelHash},
		WindowRule: portfolioresearch.WindowRule{TrainDays: 5, TestDays: 5, Step: 5},
		Gates: portfolioresearch.GateSpec{
			MinValidWindows: 1,
			MinTradingDays:  10,
			MinNetReturn:    0.01,
		},
		Benchmark: portfolioresearch.BenchmarkSpec{ID: "hs300"},
	}
}

// TestPortfolioValidationRun 验证运行：3 个非重叠窗口全部保存，后端按冻结
// spec 求值（Evaluate），retrospective 等权秩模型 → passed。
func TestPortfolioValidationRun(t *testing.T) {
	f := newPortfolioRunFixture(t)
	rec, created, err := f.validations.Create(CreatePortfolioValidationRequest{
		RequestID: testUUID2, Spec: portfolioValidationSpecFor(f.model),
	}, f.models)
	if err != nil || !created {
		t.Fatalf("创建验证 = %v/%v", created, err)
	}
	if rec.ModelEvidenceClass != portfolioresearch.EvidenceRetrospective {
		t.Fatalf("模型证据等级 = %q, want retrospective", rec.ModelEvidenceClass)
	}
	if err := f.runner.StartPortfolioValidation(rec.ID); err != nil {
		t.Fatalf("启动验证: %v", err)
	}
	waitPortfolioDone(t, f.runner)
	view, err := f.validations.Get(rec.ID)
	if err != nil {
		t.Fatalf("读取验证: %v", err)
	}
	if view.State != PortfolioValidationStateCompleted {
		t.Fatalf("验证状态 = %q, want completed", view.State)
	}
	if view.Report == nil {
		t.Fatal("验证报告缺失")
	}
	if view.Report.Verdict != portfolioresearch.GateStatusPassed {
		t.Fatalf("retrospective 等权秩应 passed，实际 %q（message=%s）", view.Report.Verdict, view.Report.Message)
	}
	if len(view.Windows) != 4 {
		t.Fatalf("窗口数 = %d, want 4（28 个交易日 × 规则{5,5,5}）", len(view.Windows))
	}
	for i, w := range view.Windows {
		if w.State != portfolioresearch.WindowStateOK || w.Metrics == nil {
			t.Fatalf("窗口 %d 应 ok 且带指标: %+v", i, w)
		}
	}
}

// writeExploratoryModel 直接落盘一个证据等级为 exploratory 的合法模型。
// v1 验证链当前只能产出 retrospective/prospective，故绕过装配层直接构造
// 模型文件（ModelHash 与 Validate 均通过）；用于验证组合层证据降级：
// exploratory 输入允许创建验证但不可能 passed。
func writeExploratoryModel(t *testing.T, store *FactorModelStore) portfolioresearch.FactorModel {
	t.Helper()
	m := portfolioresearch.FactorModel{
		ModelID:          "fm_20260919T000000000Z_00000001",
		Revision:         1,
		CreatedAt:        "2026-09-19T00:00:00Z",
		CreatedBy:        "tester",
		ResearchQuestion: "探索性因子组合测试",
		Hypothesis:       "exploratory 输入只允许 sandbox，组合证据必须降级",
		ValidatedFactors: []portfolioresearch.ValidatedFactorRef{{
			CandidateID:           "fc_20260919T000000000Z_00000001",
			CandidateRevision:     1,
			ValidationID:          "fv_20260919T000000000Z_00000001",
			FactorKind:            "momentum",
			FactorDays:            2,
			ImplementationVersion: 1,
			Direction:             portfolioresearch.DirectionHigherIsBetter,
			EvidenceClass:         portfolioresearch.EvidenceExploratory,
			PrimaryHorizon:        1,
			DataSnapshot: portfolioresearch.DataSnapshot{
				UniverseMode: "current_static", PriceSource: "local-klines",
			},
		}},
		TransformPipeline: portfolioresearch.TransformPipeline{
			Missing:     portfolioresearch.TransformMissingExclude,
			Winsorize:   portfolioresearch.WinsorizeSpec{Mode: portfolioresearch.TransformWinsorizeNone},
			Neutralize:  portfolioresearch.NeutralizeSpec{Mode: portfolioresearch.TransformNeutralizeNone},
			Standardize: portfolioresearch.TransformStandardizeRank,
		},
		Combination: portfolioresearch.CombinationSpec{Method: portfolioresearch.CombinationEqualWeightRank},
		PortfolioPolicy: portfolioresearch.PortfolioPolicy{
			Selection: portfolioresearch.PortfolioSelectionTopN, TopN: 2, CashBuffer: 0,
		},
		Execution: portfolioresearch.ExecutionSpec{
			Rebalance: portfolioresearch.RebalanceDaily, FillAt: portfolioresearch.FillNextOpen,
			SellFirst: true, T1Restriction: true, LotSize: 100,
			Cost: portfolioresearch.CostSpec{CommissionRate: 0.0003, StampDutyRate: 0.001, MinCommission: 5},
		},
		Benchmark:     portfolioresearch.BenchmarkSpec{ID: "hs300"},
		EvidenceClass: portfolioresearch.EvidenceExploratory,
		CodeVersion:   "v2-task9-test",
		DataSnapshot: portfolioresearch.DataSnapshot{
			UniverseMode: "current_static", PriceSource: "local-klines",
		},
	}
	hash, err := portfolioresearch.ModelHash(m)
	if err != nil {
		t.Fatalf("ModelHash: %v", err)
	}
	m.ModelHash = hash
	if err := m.Validate(portfolioresearch.DefaultModelLimits()); err != nil {
		t.Fatalf("exploratory 模型应可校验: %v", err)
	}
	dir := filepath.Join(store.root, m.ModelID)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	buf, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "000001.json"), buf, 0644); err != nil {
		t.Fatal(err)
	}
	return m
}

// TestPortfolioValidationEvidenceDegradation 证据降级：exploratory 模型允许
// 创建验证（ModelEvidenceClass=exploratory），但后端 Evaluate 保证不可能
// passed（证据门禁 fail）。
func TestPortfolioValidationEvidenceDegradation(t *testing.T) {
	f := newPortfolioRunFixture(t)
	em := writeExploratoryModel(t, f.models)
	rec, created, err := f.validations.Create(CreatePortfolioValidationRequest{
		RequestID: testUUID2, Spec: portfolioValidationSpecFor(em),
	}, f.models)
	if err != nil || !created {
		t.Fatalf("exploratory 输入应允许创建验证 = %v/%v", created, err)
	}
	if rec.ModelEvidenceClass != portfolioresearch.EvidenceExploratory {
		t.Fatalf("ModelEvidenceClass = %q, want exploratory", rec.ModelEvidenceClass)
	}
	if err := f.runner.StartPortfolioValidation(rec.ID); err != nil {
		t.Fatalf("启动验证: %v", err)
	}
	waitPortfolioDone(t, f.runner)
	view, err := f.validations.Get(rec.ID)
	if err != nil {
		t.Fatalf("读取验证: %v", err)
	}
	if view.State != PortfolioValidationStateCompleted || view.Report == nil {
		t.Fatalf("验证应完成并发布报告: state=%q report=%v", view.State, view.Report)
	}
	if view.Report.Verdict == portfolioresearch.GateStatusPassed {
		t.Fatal("exploratory 输入不可能 passed（证据等级只降不升）")
	}
	if view.Report.EvidenceClass != portfolioresearch.EvidenceExploratory {
		t.Fatalf("报告证据等级 = %q, want exploratory", view.Report.EvidenceClass)
	}
}

// ---- v2 Task 9 质量审查修复测试 ----

// writePortfolioModel 落盘一个直接构造的合法模型（绕过装配层，便于构造
// 滚动 IC 等装配请求路径未覆盖的合成方法；ModelHash 与 Validate 均通过）。
func writePortfolioModel(t *testing.T, store *FactorModelStore, modelID string, refs []portfolioresearch.ValidatedFactorRef, combo portfolioresearch.CombinationSpec) portfolioresearch.FactorModel {
	t.Helper()
	m := portfolioresearch.FactorModel{
		ModelID:          modelID,
		Revision:         1,
		CreatedAt:        "2026-09-19T00:00:00Z",
		CreatedBy:        "tester",
		ResearchQuestion: "滚动 IC 权重 vs 等权秩基线测试",
		Hypothesis:       "合成方法应如实披露相对等权秩基线的增量收益",
		ValidatedFactors: refs,
		TransformPipeline: portfolioresearch.TransformPipeline{
			Missing:     portfolioresearch.TransformMissingExclude,
			Winsorize:   portfolioresearch.WinsorizeSpec{Mode: portfolioresearch.TransformWinsorizeNone},
			Neutralize:  portfolioresearch.NeutralizeSpec{Mode: portfolioresearch.TransformNeutralizeNone},
			Standardize: portfolioresearch.TransformStandardizeRank,
		},
		Combination: combo,
		PortfolioPolicy: portfolioresearch.PortfolioPolicy{
			Selection: portfolioresearch.PortfolioSelectionTopN, TopN: 2, CashBuffer: 0.05,
		},
		Execution: portfolioresearch.ExecutionSpec{
			Rebalance: portfolioresearch.RebalanceDaily, FillAt: portfolioresearch.FillNextOpen,
			SellFirst: true, T1Restriction: true, LotSize: 100,
			Cost: portfolioresearch.CostSpec{CommissionRate: 0.0003, StampDutyRate: 0.001,
				Slippage: 0.01, MinCommission: 5},
		},
		Benchmark:     portfolioresearch.BenchmarkSpec{ID: "hs300"},
		EvidenceClass: portfolioresearch.EvidenceRetrospective,
		CodeVersion:   "v2-task9-test",
		DataSnapshot: portfolioresearch.DataSnapshot{
			UniverseMode: "current_static", PriceSource: "local-klines",
		},
	}
	hash, err := portfolioresearch.ModelHash(m)
	if err != nil {
		t.Fatalf("ModelHash: %v", err)
	}
	m.ModelHash = hash
	if err := m.Validate(portfolioresearch.DefaultModelLimits()); err != nil {
		t.Fatalf("模型应可校验: %v", err)
	}
	dir := filepath.Join(store.root, m.ModelID)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	buf, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "000001.json"), buf, 0644); err != nil {
		t.Fatal(err)
	}
	return m
}

// factorRef 便捷构造因子引用（retrospective，方向 higher_is_better，主周期 1）。
func factorRef(candidateID, validationID, kind string, days int) portfolioresearch.ValidatedFactorRef {
	return portfolioresearch.ValidatedFactorRef{
		CandidateID: candidateID, CandidateRevision: 1, ValidationID: validationID,
		FactorKind: kind, FactorDays: days, ImplementationVersion: 1,
		Direction:      portfolioresearch.DirectionHigherIsBetter,
		EvidenceClass:  portfolioresearch.EvidenceRetrospective,
		PrimaryHorizon: 1,
		DataSnapshot:   portfolioresearch.DataSnapshot{UniverseMode: "current_static", PriceSource: "local-klines"},
	}
}

// writeBaselineModelPair 落盘一对仅合成方法不同的合法模型（等权秩 vs 滚动
// IC，相同因子/政策/执行），供基线增量对比：等权秩模型窗口净收益即滚动 IC
// 模型的等权秩基线净收益（同一数据/执行/会计/指标链）。
func writeBaselineModelPair(t *testing.T, store *FactorModelStore, refs []portfolioresearch.ValidatedFactorRef, icFallback string) (eq, ic portfolioresearch.FactorModel) {
	t.Helper()
	eq = writePortfolioModel(t, store, "fm_20260919T000000000Z_90000001", refs,
		portfolioresearch.CombinationSpec{Method: portfolioresearch.CombinationEqualWeightRank})
	ic = writePortfolioModel(t, store, "fm_20260919T000000000Z_90000002", refs,
		portfolioresearch.CombinationSpec{
			Method:    portfolioresearch.CombinationRollingICWeight,
			RollingIC: &portfolioresearch.RollingICSpec{WindowYears: 1, Shrinkage: 0, MaxAbsWeight: 1, Fallback: icFallback},
		})
	return eq, ic
}

// portfolioValidationSpecIC 指向给定模型的验证规格：8 训练/8 测试/步长 8
// （28 交易日 → 2 个非重叠窗口；训练窗 ≥ 6 日使滚动 IC 训练可成功，
// 配合 H=1 有 ≥5 个有效 IC 样本日）。
func portfolioValidationSpecIC(m portfolioresearch.FactorModel, minBaselineIncrement float64) portfolioresearch.PortfolioValidationSpec {
	return portfolioresearch.PortfolioValidationSpec{
		ModelRef:   portfolioresearch.ModelRef{ModelID: m.ModelID, Revision: m.Revision, Hash: m.ModelHash},
		WindowRule: portfolioresearch.WindowRule{TrainDays: 8, TestDays: 8, Step: 8},
		Gates: portfolioresearch.GateSpec{
			MinValidWindows: 1, MinTradingDays: 1, MinNetReturn: -1,
			MinBaselineIncrement: minBaselineIncrement,
		},
		Benchmark: portfolioresearch.BenchmarkSpec{ID: "hs300"},
	}
}

// runPortfolioValidationAndWait 创建并运行验证，返回完成视图。
func runPortfolioValidationAndWait(t *testing.T, f *portfolioRunFixture, requestID string, spec portfolioresearch.PortfolioValidationSpec) PortfolioValidationView {
	t.Helper()
	rec, created, err := f.validations.Create(CreatePortfolioValidationRequest{RequestID: requestID, Spec: spec}, f.models)
	if err != nil || !created {
		t.Fatalf("创建验证 = %v/%v", created, err)
	}
	if err := f.runner.StartPortfolioValidation(rec.ID); err != nil {
		t.Fatalf("启动验证: %v", err)
	}
	waitPortfolioDone(t, f.runner)
	view, err := f.validations.Get(rec.ID)
	if err != nil {
		t.Fatalf("读取验证: %v", err)
	}
	if view.State != PortfolioValidationStateCompleted {
		t.Fatalf("验证状态 = %q, want completed", view.State)
	}
	return view
}

// baselineGate 取窗口的 baseline_increment 门禁结果。
func baselineGate(t *testing.T, w portfolioresearch.ValidationWindowOutcome) portfolioresearch.GateResult {
	t.Helper()
	for _, g := range w.Gates {
		if g.Name == "baseline_increment" {
			return g
		}
	}
	t.Fatalf("窗口 %d 缺 baseline_increment 门禁: %+v", w.Index, w.Gates)
	return portfolioresearch.GateResult{}
}

// TestPortfolioValidationBaselineIncrementCashFallback 基线增量按真实值求值
// （<0 fail 场景）：滚动 IC 训练数据不足回退现金 → 主组合全现金净收益 0，
// 等权秩基线净收益为正 → 增量 < 0；MinBaselineIncrement=-0.0001 门禁按真实
// 增量 fail（旧实现恒 0 会误判 pass）。同时断言增量 = 两模型窗口净收益差。
func TestPortfolioValidationBaselineIncrementCashFallback(t *testing.T) {
	f := newPortfolioRunFixture(t)
	// 两票（sh600001 升 / sh600002 降）不满足 IC 最少配对（minPairsPerDay=3）
	// → 滚动 IC 恒回退；fallback=cash → 主组合全现金。
	eq, ic := writeBaselineModelPair(t, f.models,
		[]portfolioresearch.ValidatedFactorRef{f.model.ValidatedFactors[0]},
		portfolioresearch.CombinationFallbackCash)

	icSpec := portfolioValidationSpecIC(ic, -0.0001)
	eqSpec := icSpec
	eqSpec.ModelRef = portfolioresearch.ModelRef{ModelID: eq.ModelID, Revision: eq.Revision, Hash: eq.ModelHash}
	icView := runPortfolioValidationAndWait(t, f, testUUID1, icSpec)
	eqView := runPortfolioValidationAndWait(t, f, testUUID2, eqSpec)

	if len(icView.Windows) != len(eqView.Windows) || len(icView.Windows) == 0 {
		t.Fatalf("两模型窗口数应一致且非空: ic=%d eq=%d", len(icView.Windows), len(eqView.Windows))
	}
	for i := range icView.Windows {
		icw, eqw := icView.Windows[i], eqView.Windows[i]
		if icw.State != portfolioresearch.WindowStateOK || eqw.State != portfolioresearch.WindowStateOK {
			t.Fatalf("窗口 %d 应 ok: ic=%s eq=%s", i+1, icw.State, eqw.State)
		}
		// 手算值：增量 = IC 权重组合净收益 − 等权秩基线净收益（两模型同链独立计算）。
		want := icw.Metrics.NetReturn - eqw.Metrics.NetReturn
		if math.Abs(icw.Metrics.BaselineIncrement-want) > 1e-6 {
			t.Fatalf("窗口 %d 基线增量 = %v, want 净收益差 %v（ic=%v eq=%v）",
				i+1, icw.Metrics.BaselineIncrement, want, icw.Metrics.NetReturn, eqw.Metrics.NetReturn)
		}
		if icw.Metrics.BaselineIncrement >= 0 {
			t.Fatalf("窗口 %d 增量应 < 0（全现金主组合 0 − 正基线），实际 %v",
				i+1, icw.Metrics.BaselineIncrement)
		}
		// 门禁按真实值求值：阈值 -0.0001 高于真实增量 → fail（旧恒 0 会误判 pass）。
		g := baselineGate(t, icw)
		if g.Result != "fail" {
			t.Fatalf("窗口 %d baseline_increment 门禁 = %s（actual %s），want fail", i+1, g.Result, g.Actual)
		}
	}
}

// writePortfolioBaselineData 三票数据：sh600001 平滑上行、sh600002 平盘、
// sh600003 交替跌幅下行（波动率显著、动量恒负），28 个交易日自 2024-12-24。
func writePortfolioBaselineData(t *testing.T, dir string) {
	t.Helper()
	base := time.Date(2024, 12, 24, 0, 0, 0, 0, time.Local)
	up := make([]float64, 28)
	for i := range up {
		up[i] = 10 + 0.2*float64(i) // 10.0 → 15.4，恒正日收益
	}
	flat := make([]float64, 28)
	for i := range flat {
		flat[i] = 15
	}
	down := make([]float64, 28)
	p := 21.0
	for i := range down {
		down[i] = p
		if i%2 == 0 {
			p -= 0.3
		} else {
			p -= 0.2
		}
	}
	writeDayDBCloses(t, dir, "sh600001", up, base)
	writeDayDBCloses(t, dir, "sh600002", flat, base)
	writeDayDBCloses(t, dir, "sh600003", down, base)
}

// newPortfolioRunFixture3 三票组合运行夹具（基线增量正场景）：静态股票池
// 含三只代码，写一升一平一降三只日K。
func newPortfolioRunFixture3(t *testing.T) *portfolioRunFixture {
	t.Helper()
	dir := t.TempDir()
	t.Chdir(dir)
	old := common.Pull
	common.Pull = &extend.PullKline{Config: extend.PullKlineConfig{Dir: dir}}
	t.Cleanup(func() { common.Pull = old })
	writePortfolioBaselineData(t, dir)

	fm := newFactorModelFixture(t)
	m, created, err := fm.Store.Create(validModelReq(fm.ValidationID, testUUID1), fm.Input)
	if err != nil || !created {
		t.Fatalf("创建模型 = %v/%v", created, err)
	}
	expRecords, expArtifacts := t.TempDir(), t.TempDir()
	pvRoot := t.TempDir()
	experiments := NewPortfolioExperimentStore(expRecords, expArtifacts)
	validations := NewPortfolioValidationStore(pvRoot)

	r := NewRunner()
	r.universe = researchdata.NewStaticUniverse(researchdata.StaticUniverseConfig{
		ID: "test-static", Source: "test", Codes: func() []string {
			return []string{"sh600001", "sh600002", "sh600003"}
		},
	})
	r.ConfigurePortfolio(fm.Store, experiments, validations)
	return &portfolioRunFixture{
		runner: r, models: fm.Store, experiments: experiments, validations: validations,
		model: m, modelRoot: fm.Root, expRecords: expRecords,
		expArtifacts: expArtifacts, pvRoot: pvRoot,
	}
}

// TestPortfolioValidationBaselineIncrementPositive 基线增量 >0 场景：三票 +
// 两因子（动量 IC≈+1、波动 IC≈−1）→ 滚动 IC 权重集中于动量；IC 组合选
// 升票+平票，等权秩基线选升票+降票 → 增量 > 0；MinBaselineIncrement=0.005
// 门禁按真实增量 pass（旧实现恒 0 会误判 fail）。
func TestPortfolioValidationBaselineIncrementPositive(t *testing.T) {
	f := newPortfolioRunFixture3(t)
	refs := []portfolioresearch.ValidatedFactorRef{
		factorRef("fc_20260919T000000000Z_00000011", "fv_20260919T000000000Z_00000011", "momentum", 2),
		factorRef("fc_20260919T000000000Z_00000012", "fv_20260919T000000000Z_00000012", "volatility", 2),
	}
	// fallback=equal_weight：训练成功则用真实 IC 权重；防御回退也不会全现金。
	eq, ic := writeBaselineModelPair(t, f.models, refs, portfolioresearch.CombinationFallbackEqualWeight)

	icSpec := portfolioValidationSpecIC(ic, 0.005)
	eqSpec := icSpec
	eqSpec.ModelRef = portfolioresearch.ModelRef{ModelID: eq.ModelID, Revision: eq.Revision, Hash: eq.ModelHash}
	icView := runPortfolioValidationAndWait(t, f, testUUID1, icSpec)
	eqView := runPortfolioValidationAndWait(t, f, testUUID2, eqSpec)

	if len(icView.Windows) != len(eqView.Windows) || len(icView.Windows) == 0 {
		t.Fatalf("两模型窗口数应一致且非空: ic=%d eq=%d", len(icView.Windows), len(eqView.Windows))
	}
	for i := range icView.Windows {
		icw, eqw := icView.Windows[i], eqView.Windows[i]
		if icw.State != portfolioresearch.WindowStateOK || eqw.State != portfolioresearch.WindowStateOK {
			t.Fatalf("窗口 %d 应 ok: ic=%s eq=%s", i+1, icw.State, eqw.State)
		}
		want := icw.Metrics.NetReturn - eqw.Metrics.NetReturn
		if math.Abs(icw.Metrics.BaselineIncrement-want) > 1e-6 {
			t.Fatalf("窗口 %d 基线增量 = %v, want 净收益差 %v（ic=%v eq=%v）",
				i+1, icw.Metrics.BaselineIncrement, want, icw.Metrics.NetReturn, eqw.Metrics.NetReturn)
		}
		if icw.Metrics.BaselineIncrement <= 0 {
			t.Fatalf("窗口 %d 增量应 > 0（IC 集中动量胜过等权基线混入降票），实际 %v",
				i+1, icw.Metrics.BaselineIncrement)
		}
		g := baselineGate(t, icw)
		if g.Result != "pass" {
			t.Fatalf("窗口 %d baseline_increment 门禁 = %s（actual %s），want pass", i+1, g.Result, g.Actual)
		}
	}
}

// TestPortfolioOrderCSVWeightMatchesTarget orders.csv weight 列必须回填当日
// 目标权重（Constrained.Effective，与归因 TargetWeights 同口径），不得恒 0。
func TestPortfolioOrderCSVWeightMatchesTarget(t *testing.T) {
	f := newPortfolioRunFixture(t)
	exp, _, err := f.experiments.Create(portfolioExperimentRequest(f.model, testUUID1))
	if err != nil {
		t.Fatalf("创建实验: %v", err)
	}
	if err := f.runner.StartPortfolioExperiment(exp.ExperimentID); err != nil {
		t.Fatalf("启动: %v", err)
	}
	st := waitPortfolioDone(t, f.runner)
	if st["state"] != "done" {
		t.Fatalf("组合任务应为 done: %v", st)
	}

	ordersData, err := os.ReadFile(filepath.Join(f.expArtifacts, exp.ExperimentID, "orders.csv"))
	if err != nil {
		t.Fatalf("读 orders.csv: %v", err)
	}
	repData, err := os.ReadFile(filepath.Join(f.expArtifacts, exp.ExperimentID, "report.json"))
	if err != nil {
		t.Fatalf("读 report.json: %v", err)
	}
	var rep PortfolioReport
	if err := json.Unmarshal(repData, &rep); err != nil {
		t.Fatalf("report.json 反序列化失败: %v", err)
	}
	// 归因 WeightSeries 逐日 Target = 当日执行目标 Constrained.Effective（与订单同口径）。
	tw := make(map[string]map[string]float64, len(rep.Attribution.WeightSeries))
	for _, d := range rep.Attribution.WeightSeries {
		tw[d.Date] = d.Target
	}

	rows, err := csv.NewReader(strings.NewReader(string(ordersData))).ReadAll()
	if err != nil {
		t.Fatalf("orders.csv 解析失败: %v", err)
	}
	if len(rows) < 2 || rows[0][0] != "date" || rows[0][4] != "weight" {
		t.Fatalf("orders.csv 头/行异常: %v", rows[0])
	}
	nonZero := 0
	for _, row := range rows[1:] {
		if len(row) != 5 {
			t.Fatalf("订单行字段数异常: %v", row)
		}
		date, code := row[0], row[1]
		w, err := strconv.ParseFloat(row[4], 64)
		if err != nil {
			t.Fatalf("订单行 weight 解析失败 %v: %v", row, err)
		}
		// 当日目标权重：归因 TargetWeights 同口径；清仓卖出目标无该代码 → 0。
		want := 0.0
		if m, ok := tw[date]; ok {
			want = m[code]
		}
		if math.Abs(w-want) > 1e-9 {
			t.Fatalf("%s/%s 订单 weight = %.6f, want 当日目标权重 %.6f", date, code, w, want)
		}
		if w != 0 {
			nonZero++
		}
	}
	if nonZero == 0 {
		t.Fatal("orders.csv weight 列应回填真实目标权重，不得恒 0")
	}
}

// TestResolveCombineWeightsNilRollingIC 手工构造 method=rolling_ic_weight 且
// rollingIc 缺失的规格：resolveCombineWeights 返回明确错误而非 nil 解引用
// panic（读取路径 m.Validate 之外的防御）。
func TestResolveCombineWeightsNilRollingIC(t *testing.T) {
	spec := portfolioresearch.CombinationSpec{Method: portfolioresearch.CombinationRollingICWeight}
	// 训练日期非空路径：原代码在 *spec.RollingIC 解引用处 panic。
	if _, _, err := resolveCombineWeights(spec, nil, []string{"2026-01-05"}, nil); err == nil ||
		!strings.Contains(err.Error(), "rollingIc") {
		t.Fatalf("rollingIc 缺失应返回明确错误而非 panic: %v", err)
	}
	// 训练日期为空路径：原代码在 spec.RollingIC.Fallback 处 panic。
	if _, _, err := resolveCombineWeights(spec, nil, nil, nil); err == nil ||
		!strings.Contains(err.Error(), "rollingIc") {
		t.Fatalf("rollingIc 缺失（空训练期）应返回明确错误而非 panic: %v", err)
	}
}

// TestRollingICModelMissingIcFailsClosed 手工构造 method=rolling_ic_weight 且
// rollingIc 缺失的模型文件（hash 自洽）：读取返回明确错误而非 panic——磁盘
// 手工构造可通过 hash 校验的非法模型在 readRevisionLocked 的 m.Validate 即
// fail closed（CombinationSpec.Validate 拒绝 nil rollingIc），配合
// resolveCombineWeights 的显式防护双保险。
func TestRollingICModelMissingIcFailsClosed(t *testing.T) {
	f := newPortfolioRunFixture(t)
	m := f.model
	m.ModelID = "fm_20260919T000000000Z_90000003"
	m.Revision = 1
	m.CreatedAt = "2026-09-19T00:00:00Z"
	m.CreateRequestID = ""
	m.CreateRequestHash = ""
	m.ModelHash = ""
	m.Combination = portfolioresearch.CombinationSpec{Method: portfolioresearch.CombinationRollingICWeight}
	hash, err := portfolioresearch.ModelHash(m)
	if err != nil {
		t.Fatalf("ModelHash: %v", err)
	}
	m.ModelHash = hash
	dir := filepath.Join(f.models.root, m.ModelID)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	buf, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "000001.json"), buf, 0644); err != nil {
		t.Fatal(err)
	}
	_, err = f.models.Get(m.ModelID, 1)
	if err == nil || !strings.Contains(err.Error(), "rollingIc") {
		t.Fatalf("rollingIc 缺失模型读取应返回明确错误而非 panic: %v", err)
	}
}
