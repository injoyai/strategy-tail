package lab

// release_v2_test.go v2 Task 12 发布门禁测试（实施计划 Task 12 发布门禁）。
//
// 运行全部门禁项：真实调用（t.Run 嵌套）既有测试或引用其通过性，构造门禁
// 结果表并断言：
//   - 门禁清单完整（13 项固定顺序，六类全覆盖）；
//   - 无 CRITICAL/未解释门禁（fail 未解释 → 阻止发布）；
//   - v1 不可变历史可读（既有产物与 testdata 夹具可读可测）；
//   - 发布候选冻结字段齐全（候选 hash/门禁表/限制/建议决定）。
//
// 门禁评估是只读检查（不写产物、不改变状态）；本测试失败 = 发布被阻止，
// 但已写产物与已发布状态不受影响（见 doc/v2-multifactor-portfolio.md
// §发布门禁语义）。

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// releaseKeyFiles 发布候选 hash 关键文件清单（相对仓库根；v2 交付核心 + 契约
// 文档。候选 hash 随代码冻结：任一文件变更 → hash 变化。
//
// 注意：doc/v2-multifactor-portfolio.md（本文档）不参与候选 hash——发布文档
// 描述候选并记录 hash，若 hash 覆盖文档自身会形成自引用环；代码与契约变更会
// 反映在 hash 中，文档变更不改变代码候选身份。
var releaseKeyFiles = []string{
	"internal/portfolioresearch/types.go",
	"internal/portfolioresearch/transform.go",
	"internal/portfolioresearch/combine.go",
	"internal/portfolioresearch/redundancy.go",
	"internal/portfolioresearch/target.go",
	"internal/portfolioresearch/execution.go",
	"internal/portfolioresearch/accounting.go",
	"internal/portfolioresearch/metrics.go",
	"internal/portfolioresearch/attribution.go",
	"internal/portfolioresearch/validation.go",
	"internal/lab/factor_model.go",
	"internal/lab/factor_model_store.go",
	"internal/lab/portfolio_experiment.go",
	"internal/lab/portfolio_experiment_store.go",
	"internal/lab/portfolio_validation.go",
	"internal/lab/portfolio_validation_store.go",
	"internal/lab/portfolio_runner.go",
	"internal/lab/portfolio_handlers.go",
	"internal/lab/portfolio_adapter_draft.go",
	"internal/lab/runner.go",
	"internal/lab/server.go",
	"internal/lab/gates_v2.go",
	"internal/lab/web/lab/index.html",
	"DESIGN.md",
	"UX-CONTRACT.md",
}

// TestReleaseV2Gates 发布门禁主入口。
func TestReleaseV2Gates(t *testing.T) {
	inputs := collectReleaseGateInputs(t)
	gates := EvaluateReleaseGates(inputs)

	// 打印门禁结果表（发布文档 §门禁结果表 的机器可读来源）。
	t.Logf("== 发布门禁结果表（%d 项） ==", len(gates))
	for _, g := range gates {
		notes := ""
		if g.Notes != "" {
			notes = " | notes=" + g.Notes
		}
		t.Logf("%s | %s | %s | %s%s", g.Category, g.Name, g.Status, g.Evidence, notes)
	}

	// 断言 1：门禁清单完整（固定 13 项，顺序与规范清单一致）。
	if len(gates) != len(releaseGateDefs) {
		t.Fatalf("门禁结果表项数 %d，期望 %d", len(gates), len(releaseGateDefs))
	}
	for i, g := range gates {
		if g.Name != releaseGateDefs[i].Name {
			t.Fatalf("门禁顺序/清单与规范不符：第 %d 项 %q vs %q", i, g.Name, releaseGateDefs[i].Name)
		}
	}

	// 断言 2：无 CRITICAL/未解释（fail 未解释 → 阻止发布）。
	for _, g := range gates {
		if g.Status == ReleaseGateFail {
			t.Errorf("门禁 %s fail（未解释则阻止发布）：%s", g.Name, g.Notes)
		}
		if g.Status == ReleaseGateNotApplicable && g.Notes == "" {
			t.Errorf("门禁 %s not_applicable 缺少解释", g.Name)
		}
	}

	// 断言 3：v1 不可变历史可读（真实调用既有测试 + 显式读 testdata 夹具）。
	assertV1ProductsReadable(t)

	// 候选冻结。
	hash := computeReleaseCandidateHash(t)
	cand := FreezeReleaseCandidate(hash, "v2-task12", "2026-09-19", gates, ReleaseLimitations)
	t.Logf("== 发布候选 ==")
	t.Logf("候选 hash: %s", cand.Hash)
	t.Logf("建议发布决定: %s", cand.Recommendation)
	t.Logf("理由: %s", cand.Reason)

	// 断言 4：候选冻结字段齐全。
	if cand.Hash == "" || cand.CodeVersion == "" || cand.FrozenAt == "" {
		t.Fatalf("候选 hash/版本/冻结时间缺失: %+v", cand)
	}
	if len(cand.Gates) == 0 || len(cand.Limitations) == 0 {
		t.Fatalf("候选门禁表或限制清单为空")
	}
	if cand.Recommendation == "" || cand.Reason == "" {
		t.Fatalf("候选建议决定/理由缺失")
	}
	if cand.Recommendation == RecommendBlock {
		t.Fatalf("发布候选 BLOCK（存在未通过门禁）")
	}
	// 冻结输出必须隔离输入（不共享底层切片）。
	cand.Limitations[0] = "mutated"
	if ReleaseLimitations[0] == "mutated" {
		t.Fatal("FreezeReleaseCandidate 必须复制限制清单（防别名污染）")
	}
}

// collectReleaseGateInputs 收集全部门禁检查输入（真实调用既有测试作为证据）。
func collectReleaseGateInputs(t *testing.T) []ReleaseCheckInput {
	var inputs []ReleaseCheckInput
	inputs = append(inputs, collectMigrationInputs(t)...)
	inputs = append(inputs, collectCorrectnessInputs(t)...)
	inputs = append(inputs, collectRepeatableInputs(t)...)
	inputs = append(inputs, collectAuditInputs(t)...)
	inputs = append(inputs, collectPerformanceInput(t))
	inputs = append(inputs, collectDocsInput(t))
	return inputs
}

// runGateSubtest 在子测试中调用既有测试函数并返回是否通过（引用/调用既有
// 测试作为门禁证据，不复制其逻辑；子测试失败会传播到本测试）。
func runGateSubtest(t *testing.T, name string, fn func(tt *testing.T)) bool {
	return t.Run(name, func(tt *testing.T) { fn(tt) })
}

// collectMigrationInputs 可迁移性门禁：v1 产物可读 + 旧客户端 API 兼容。
func collectMigrationInputs(t *testing.T) []ReleaseCheckInput {
	// v1 产物可读：分析报告 v3 夹具、候选 v1 夹具、分析 store 历史、v1 验证 store 读回。
	ok := true
	if !runGateSubtest(t, "CompatV3ReportFixtureLoads", TestCompatV3ReportFixtureLoads) {
		ok = false
	}
	if !runGateSubtest(t, "CompatCandidateV1FixtureVerifiable", TestCompatCandidateV1FixtureVerifiable) {
		ok = false
	}
	if !runGateSubtest(t, "AnalysisStoreSavesV3AndV4", TestAnalysisStoreSavesV3AndV4) {
		ok = false
	}
	if !runGateSubtest(t, "ValidationStoreGetView", TestValidationStoreGetView) {
		ok = false
	}
	ev := "TestCompatV3ReportFixtureLoads / TestCompatCandidateV1FixtureVerifiable / TestAnalysisStoreSavesV3AndV4 / TestValidationStoreGetView（v1 分析/候选/分析 store/v1 验证 store 读回，含 testdata 夹具）"
	v1 := gateInput("v1_products_readable", ok, ev, "v1 不可变历史不受影响：既有产物与测试全部可读可测")

	// 旧客户端 API 兼容：/api/status、/api/script、候选/策略路由行为不变。
	ok2 := true
	if !runGateSubtest(t, "PortfolioLegacyCompat", TestPortfolioLegacyCompat) {
		ok2 = false
	}
	if !runGateSubtest(t, "ServerScriptAPI", TestServerScriptAPI) {
		ok2 = false
	}
	ev2 := "TestPortfolioLegacyCompat（/api/status、/api/stop、/api/factors）+ TestServerScriptAPI（脚本 API）+ 引用 TestServerRunLifecycle / TestServerStrategyPresets / TestServerStrategyRunValidation / TestServerCandidateCreateObserve / TestLabPageSupportsAnnualGroupChart / TestLabPageGroupsFactorSelectors"
	api := gateInput("legacy_api_compatible", ok2, ev2, "组合路由为新增，不改变既有 /api/status、/api/run、/api/script、/api/factors 与候选/策略路由行为")

	return []ReleaseCheckInput{v1, api}
}

// collectCorrectnessInputs 正确性门禁：金标准/会计恒等式/确定性。
func collectCorrectnessInputs(t *testing.T) []ReleaseCheckInput {
	// 端到端金标准：手算固定案例 + 真实 runner 全链 + 执行边界 + 泄漏复证。
	ok := true
	if !runGateSubtest(t, "AuditGoldenFixedCase", TestAuditGoldenFixedCase) {
		ok = false
	}
	if !runGateSubtest(t, "AuditE2EFullChain", TestAuditE2EFullChain) {
		ok = false
	}
	if !runGateSubtest(t, "AuditExecutionEdgeScenarios", TestAuditExecutionEdgeScenarios) {
		ok = false
	}
	if !runGateSubtest(t, "AuditLeakIsolationFutureReturns", TestAuditLeakIsolationFutureReturns) {
		ok = false
	}
	ev := "TestAuditGoldenFixedCase（金标准手算全链）+ TestAuditE2EFullChain（模型→实验→产物→验证完整链路）+ TestAuditExecutionEdgeScenarios（停牌/涨跌停/T+1/行业缺失）+ TestAuditLeakIsolationFutureReturns（测试窗泄漏隔离复证）"
	golden := gateInput("e2e_golden_case", ok, ev, "固定案例全部期望值为手工推导字面量，非引擎回读")

	// 会计恒等式：对账恒等式 + 归因加总（金标准测试内含 Reconciled/ReconResidual 断言）。
	ok2 := true
	if !runGateSubtest(t, "AuditGoldenFixedCaseAccounting", TestAuditGoldenFixedCase) {
		ok2 = false
	}
	ev2 := "TestAuditGoldenFixedCase（对账恒等式 Reconciled/ReconResidual ≤1e-6 + 归因 StockCashSum/NetReturn 构造性）+ 引用 internal/portfolioresearch TestMultiDayInvariants / TestAttributionSumIdentity / TestAttributionSelectionExecution"
	accounting := gateInput("accounting_identity", ok2, ev2, "对账恒等式由现金流+市值变化代换恒成立，容差 1e-6；归因残差显式披露")

	// 确定性：同输入两次运行逐位一致。
	ok3 := true
	if !runGateSubtest(t, "AuditE2EDeterminismAndRestart", TestAuditE2EDeterminismAndRestart) {
		ok3 = false
	}
	ev3 := "TestAuditE2EDeterminismAndRestart（Metrics/Attribution/Nav/CSV 两次运行 DeepEqual）+ 引用 internal/portfolioresearch TestDeterminism（ExecuteDay 逐位一致）"
	determinism := gateInput("determinism", ok3, ev3, "无系统时间/随机/并发；map 迭代全部排序，同一输入重复运行逐位一致")

	return []ReleaseCheckInput{golden, accounting, determinism}
}

// collectRepeatableInputs 可重复性门禁：重复运行产物 hash 一致 + 重启读取一致。
func collectRepeatableInputs(t *testing.T) []ReleaseCheckInput {
	// 同输入重复运行产物 hash 一致（排除时间戳字段，文档化）。
	ok := true
	if !runGateSubtest(t, "AuditE2EDeterminismAndRestartHash", TestAuditE2EDeterminismAndRestart) {
		ok = false
	}
	ev := "TestAuditE2EDeterminismAndRestart（nav/orders/trades/holdings CSV 两次运行 hash 一致 + manifest 产物名一致）"
	repeat := gateInput("repeat_hash_consistent", ok, ev,
		"report.json 的 StartedAt/FinishedAt 为运行时刻信息，不参与确定性比对（文档化在发布文档 §可重复性）")

	// 重启后 Store 读取 hash 校验一致（篡改 fail closed）。
	ok2 := true
	if !runGateSubtest(t, "AuditE2EFullChainRestart", TestAuditE2EFullChain) {
		ok2 = false
	}
	if !runGateSubtest(t, "E2ERestartHash", TestAuditE2EDeterminismAndRestart) {
		ok2 = false
	}
	ev2 := "TestAuditE2EFullChain（重启后 verdict/证据等级/逐项门禁重算一致）+ TestAuditE2EDeterminismAndRestart（重启后 Get 重算磁盘 hash 比对，篡改 fail closed）+ 引用 TestPortfolioValidationStore_GetRecomputeVerdictFailClosed / TestFactorModelStoreFailClosedOnHashTamper"
	restart := gateInput("restart_hash_consistent", ok2, ev2, "模型/实验/验证 Store 读取均重算 hash/spec 与存储比对，不匹配 fail closed")

	return []ReleaseCheckInput{repeat, restart}
}

// collectAuditInputs 审计门禁：manifest/后端结论/产物 hash/路径安全。
func collectAuditInputs(t *testing.T) []ReleaseCheckInput {
	// completed 必有完整 manifest。
	ok := true
	if !runGateSubtest(t, "MarkCompletedPublishesManifest", TestExperimentStore_MarkCompleted_PublishesManifest) {
		ok = false
	}
	if !runGateSubtest(t, "ExperimentCompletionGate", TestExperimentStore_CompletionGate) {
		ok = false
	}
	ev := "TestExperimentStore_MarkCompleted_PublishesManifest + TestExperimentStore_CompletionGate（缺一类/读失败→发布失败，主记录保持 running，绝无 completed 但产物不完整）"
	manifest := gateInput("completed_has_manifest", ok, ev, "completed 必有完整五类产物 manifest；取消/失败不发布 manifest")

	// verdict 后端生成（客户端不能指定；读取重算比对）。
	ok2 := true
	if !runGateSubtest(t, "CompleteBackendVerdicts", TestPortfolioValidationStore_CompleteBackendVerdicts) {
		ok2 = false
	}
	if !runGateSubtest(t, "GetRecomputeVerdictFailClosed", TestPortfolioValidationStore_GetRecomputeVerdictFailClosed) {
		ok2 = false
	}
	ev2 := "TestPortfolioValidationStore_CompleteBackendVerdicts（passed/failed/insufficient/error 后端生成）+ TestPortfolioValidationStore_GetRecomputeVerdictFailClosed（读取重算比对）"
	verdict := gateInput("verdict_backend_generated", ok2, ev2, "结论由冻结 spec 唯一决定（纯函数确定性）；Create 请求无 verdict 字段")

	// 产物含 SHA-256 hash。
	ok3 := true
	if !runGateSubtest(t, "HashMismatchFailClosed", TestExperimentStore_HashMismatchFailClosed) {
		ok3 = false
	}
	if !runGateSubtest(t, "ArtifactManifestValidate", TestArtifactManifestValidate) {
		ok3 = false
	}
	ev3 := "TestExperimentStore_HashMismatchFailClosed（读取重算磁盘 hash 不匹配 fail closed）+ TestArtifactManifestValidate（manifest 校验）+ 引用 TestFactorModelStoreFailClosedOnHashTamper / TestCandidateStoreEvidenceHashMismatch"
	hashed := gateInput("artifacts_hashed", ok3, ev3, "manifest 文件名字 SHA-256；报告 hash 与 manifest 绑定")

	// 无路径穿越。
	ok4 := true
	if !runGateSubtest(t, "AuditPathTraversalVariants", TestAuditPathTraversalVariants) {
		ok4 = false
	}
	if !runGateSubtest(t, "ArtifactPathTraversal", TestPortfolioArtifactPathTraversal) {
		ok4 = false
	}
	if !runGateSubtest(t, "ValidationPathTraversalRejected", TestPortfolioValidationStore_PathTraversalRejected) {
		ok4 = false
	}
	ev4 := "TestAuditPathTraversalVariants（../、URL 编码、绝对路径、Unicode 变体）+ TestPortfolioArtifactPathTraversal + TestPortfolioValidationStore_PathTraversalRejected + 引用 TestFactorModelStoreRejectsPathTraversal / TestCandidateStoreRejectsTraversalID / TestAnalysisStoreRejectsTraversalID"
	path := gateInput("no_path_traversal", ok4, ev4, "产物名/ID 白名单校验拒绝穿越；URL 编码穿越段由 handler 400 拒绝")

	return []ReleaseCheckInput{manifest, verdict, hashed, path}
}

// collectPerformanceInput 性能门禁：记录基准绝对值 + 机器环境（无预设阈值，
// 不判定好坏 → not_applicable，Notes 附记录值）。
func collectPerformanceInput(t *testing.T) ReleaseCheckInput {
	elapsed, peak, size, days, endNav := runAuditBenchmark(t)
	env := fmt.Sprintf("go=%s os=%s arch=%s cpu=%d", runtime.Version(), runtime.GOOS, runtime.GOARCH, runtime.NumCPU())
	notes := fmt.Sprintf("负载 20 因子×300 股票×750 交易日（%d 个有效交易日）；总时长 %.3f s；峰值内存(HeapAlloc 采样) %.1f MB；产物总大小估算 %.2f MB（report.json 序列化 + 四类 CSV 行估算）；期末净值(净口径) %.6f；机器环境 %s；无预设阈值，仅记录绝对值不判定好坏", days, elapsed.Seconds(), float64(peak)/1024/1024, float64(size)/1024/1024, endNav, env)
	return ReleaseCheckInput{
		Name:     "benchmark_recorded",
		Check:    CheckNotApplicable,
		Evidence: "runAuditBenchmark（与 TestAuditBenchmarkEnv 同负载同函数）",
		Notes:    notes,
	}
}

// collectDocsInput 文档门禁：UX-CONTRACT.md、DESIGN.md §7、
// doc/v2-multifactor-portfolio.md 存在且记录限制。
func collectDocsInput(t *testing.T) ReleaseCheckInput {
	root := repoRootDir(t)
	checks := []struct {
		name string
		path string
		need string // 需要包含的子串（空 = 仅存在）
	}{
		{name: "UX-CONTRACT.md", path: filepath.Join(root, "UX-CONTRACT.md"), need: ""},
		{name: "DESIGN.md §7", path: filepath.Join(root, "DESIGN.md"), need: "## 7. 06 组合研究页"},
		{name: "doc/v2-multifactor-portfolio.md", path: filepath.Join(root, "doc", "v2-multifactor-portfolio.md"), need: "## 3. 限制与已接受剩余风险"},
	}
	var missing []string
	for _, c := range checks {
		data, err := os.ReadFile(c.path)
		if err != nil {
			missing = append(missing, c.name+"（读取失败："+err.Error()+"）")
			continue
		}
		if c.need != "" && !strings.Contains(string(data), c.need) {
			missing = append(missing, c.name+"（缺少必需章节 "+c.need+"）")
		}
	}
	ev := "UX-CONTRACT.md（06 组合研究 UI 契约，根目录）+ DESIGN.md §7（06 组合研究页设计上下文）+ doc/v2-multifactor-portfolio.md（Task 12 发布文档，含 §限制 与已接受剩余风险）"
	if len(missing) > 0 {
		return ReleaseCheckInput{Name: "docs_complete", Check: CheckFail, Evidence: ev, Notes: "缺失：" + strings.Join(missing, "；")}
	}
	return ReleaseCheckInput{Name: "docs_complete", Check: CheckPass, Evidence: ev, Notes: "发布文档记录限制（涨跌停/停牌缺失、市值/行业依赖、市场阶段、基准缺失 IR 语义、归因限制）与已接受剩余风险"}
}

// gateInput 便捷构造单项门禁输入（Check 由 bool 推导 pass/fail）。
func gateInput(name string, ok bool, evidence, notes string) ReleaseCheckInput {
	check := CheckPass
	if !ok {
		check = CheckFail
	}
	return ReleaseCheckInput{Name: name, Check: check, Evidence: evidence, Notes: notes}
}

// assertV1ProductsReadable v1 不可变历史可读：testdata 夹具可解码且为合法
// JSON；analysis v3 / candidate v1 均存在。
func assertV1ProductsReadable(t *testing.T) {
	t.Helper()
	for _, f := range []string{"analysis_v3.json", "candidate_v1.json"} {
		data, err := os.ReadFile(filepath.Join("testdata", f))
		if err != nil {
			t.Fatalf("v1 产物夹具 %s 不可读: %v", f, err)
		}
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(data, &raw); err != nil {
			t.Fatalf("v1 产物夹具 %s 不是合法 JSON: %v", f, err)
		}
	}
}

// computeReleaseCandidateHash 由关键文件指纹计算发布候选冻结 hash。
func computeReleaseCandidateHash(t *testing.T) string {
	t.Helper()
	root := repoRootDir(t)
	files := make([]KeyFileHash, 0, len(releaseKeyFiles))
	for _, rel := range releaseKeyFiles {
		files = append(files, KeyFileHash{Path: rel, SHA256: sha256File(t, filepath.Join(root, rel))})
	}
	return ReleaseCandidateHash(files)
}

// sha256File 读取文件并计算 SHA-256（hex）。
func sha256File(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读关键文件 %s 失败: %v", path, err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// repoRootDir 从本测试文件绝对路径推导仓库根目录（上溯三级）。
func repoRootDir(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller 失败")
	}
	return filepath.Dir(filepath.Dir(filepath.Dir(thisFile)))
}

// TestReleaseGateEvaluation 门禁评估纯函数单元测试：三态映射、缺输入 fail、
// 冻结决定规则（BLOCK/CONDITIONAL/PASS）与候选 hash 排序无关性。
func TestReleaseGateEvaluation(t *testing.T) {
	// 三态映射 + 缺输入 fail（未解释阻止发布）。
	inputs := []ReleaseCheckInput{
		{Name: "v1_products_readable", Check: CheckPass, Evidence: "e1"},
		{Name: "docs_complete", Check: CheckNotApplicable, Evidence: "e2", Notes: "无预设阈值仅记录"},
	}
	gates := EvaluateReleaseGates(inputs)
	if len(gates) != len(releaseGateDefs) {
		t.Fatalf("门禁表项数 %d，期望 %d", len(gates), len(releaseGateDefs))
	}
	byName := map[string]ReleaseGateResult{}
	for _, g := range gates {
		byName[g.Name] = g
	}
	if byName["v1_products_readable"].Status != ReleaseGatePass {
		t.Fatalf("pass 项状态异常: %s", byName["v1_products_readable"].Status)
	}
	if byName["docs_complete"].Status != ReleaseGateNotApplicable {
		t.Fatalf("na 项状态异常: %s", byName["docs_complete"].Status)
	}
	if byName["e2e_golden_case"].Status != ReleaseGateFail || !strings.Contains(byName["e2e_golden_case"].Notes, "未提供") {
		t.Fatalf("缺输入项应为 fail（未解释）: %s/%s", byName["e2e_golden_case"].Status, byName["e2e_golden_case"].Notes)
	}

	// 冻结决定规则：fail → BLOCK。
	blockGates := []ReleaseGateResult{{Name: "x", Status: ReleaseGateFail, Evidence: "e"}}
	if rec := recommendationOf(blockGates, nil); rec != RecommendBlock {
		t.Fatalf("存在 fail 应 BLOCK，实际 %s", rec)
	}
	// 全 pass + 限制 → CONDITIONAL；全 pass + na → CONDITIONAL；全 pass 无限制 → PASS。
	passGates := []ReleaseGateResult{{Name: "x", Status: ReleaseGatePass, Evidence: "e"}}
	if rec := recommendationOf(passGates, []string{"lim"}); rec != RecommendConditional {
		t.Fatalf("有已记录限制应 CONDITIONAL，实际 %s", rec)
	}
	if rec := recommendationOf([]ReleaseGateResult{{Name: "p", Status: ReleaseGateNotApplicable}}, nil); rec != RecommendConditional {
		t.Fatalf("存在 na 应 CONDITIONAL，实际 %s", rec)
	}
	if rec := recommendationOf(passGates, nil); rec != RecommendPass {
		t.Fatalf("全 pass 无限制应 PASS，实际 %s", rec)
	}

	// 候选 hash 排序无关 + 内容敏感。
	filesA := []KeyFileHash{{Path: "b.go", SHA256: "b"}, {Path: "a.go", SHA256: "a"}}
	filesB := []KeyFileHash{{Path: "a.go", SHA256: "a"}, {Path: "b.go", SHA256: "b"}}
	if ReleaseCandidateHash(filesA) != ReleaseCandidateHash(filesB) {
		t.Fatal("候选 hash 应排序无关")
	}
	filesC := []KeyFileHash{{Path: "a.go", SHA256: "a"}, {Path: "b.go", SHA256: "c"}}
	if ReleaseCandidateHash(filesA) == ReleaseCandidateHash(filesC) {
		t.Fatal("候选 hash 应对内容敏感")
	}
}
