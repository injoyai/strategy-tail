package lab

import (
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/injoyai/strategy-tail/internal/portfolioresearch"
)

// portfolio_validation_store_test.go v2 Task 8 存储层测试：冻结 spec（hash
// fail closed、模型 hash 校验、证据后端派生）、幂等、逐窗写入（完成前可
// 重写/完成后拒绝）、后端生成结论（passed/failed/insufficient/error）、
// 重复完成拒绝、读取重算结论 fail closed、Supersedes/SupersededBy 派生
// （修改门禁 → 新验证 ID、旧窗标已见数据）、分页/过滤、路径穿越与损坏
// fail closed。

// stubModelSource 测试桩：按 revision 返回固定模型。
type stubModelSource struct {
	model portfolioresearch.FactorModel
	err   error
}

func (s *stubModelSource) Get(modelID string, revision int) (portfolioresearch.FactorModel, error) {
	if s.err != nil {
		return portfolioresearch.FactorModel{}, s.err
	}
	if s.model.ModelID != modelID || s.model.Revision != revision {
		return portfolioresearch.FactorModel{}, fmt.Errorf("模型不存在: %s@%d", modelID, revision)
	}
	return s.model, nil
}

var _ PortfolioModelSource = (*stubModelSource)(nil)

// pvModelID / pvModelHash 冻结模型引用常量（与桩模型一致）。
const pvModelID = "fm_20260917T150100000Z_99aabbcc"

func pvModelHash() string { return strings.Repeat("ab", 32) }

// pvStubModel 合法模型桩（ModelHash 与 pvModelHash 一致，证据 retrospective）。
func pvStubModel() portfolioresearch.FactorModel {
	return portfolioresearch.FactorModel{
		ModelID:       pvModelID,
		Revision:      1,
		ModelHash:     pvModelHash(),
		EvidenceClass: portfolioresearch.EvidenceRetrospective,
	}
}

// pvFixture 存储夹具：临时根目录 + 模型桩 + 单调时钟（保证 CreatedAt 严格
// 递增，列表排序断言稳定）。
type pvFixture struct {
	store  *PortfolioValidationStore
	root   string
	models *stubModelSource
	now    time.Time
}

func newPvFixture(t *testing.T) *pvFixture {
	t.Helper()
	root := t.TempDir()
	f := &pvFixture{
		root:   root,
		models: &stubModelSource{model: pvStubModel()},
		now:    time.Date(2026, 9, 18, 0, 0, 0, 0, time.UTC),
	}
	f.store = NewPortfolioValidationStore(root)
	f.store.now = func() time.Time {
		f.now = f.now.Add(time.Second)
		return f.now
	}
	return f
}

// pvSpec 全门禁配置的合法冻结规格（默认窗口可 passed）。
func pvSpec() portfolioresearch.PortfolioValidationSpec {
	return portfolioresearch.PortfolioValidationSpec{
		ModelRef:   portfolioresearch.ModelRef{ModelID: pvModelID, Revision: 1, Hash: pvModelHash()},
		WindowRule: portfolioresearch.WindowRule{TrainDays: 20, TestDays: 10, Step: 10},
		Gates: portfolioresearch.GateSpec{
			MinValidWindows: 1,
			MinTradingDays:  10,
			MinNetReturn:    0.01,
		},
		Benchmark: portfolioresearch.BenchmarkSpec{ID: "hs300"},
	}
}

// createOne 创建一条验证（自动唯一幂等键）并断言成功。
func (f *pvFixture) createOne(t *testing.T, muts ...func(*CreatePortfolioValidationRequest)) PortfolioValidationRecord {
	t.Helper()
	req := CreatePortfolioValidationRequest{RequestID: uniqueRequestID(int(atomic.AddInt32(&createSeq, 1)) + 900000), Spec: pvSpec()}
	for _, m := range muts {
		m(&req)
	}
	rec, created, err := f.store.Create(req, f.models)
	if err != nil {
		t.Fatalf("创建验证失败: %v", err)
	}
	if !created {
		t.Fatalf("期望首次创建 created=true")
	}
	return rec
}

// pvWindow 构造一个可写入的窗口结果（ID 由调用方覆盖为真实验证 ID）。
func pvWindow(idx int, muts ...func(*portfolioresearch.ValidationWindowOutcome)) portfolioresearch.ValidationWindowOutcome {
	ir := 1.0
	excess := 0.05
	o := portfolioresearch.ValidationWindowOutcome{
		ValidationID: "pv_x",
		Index:        idx,
		TrainStart:   "2024-01-01",
		TrainEnd:     "2024-01-20",
		TestStart:    "2024-01-21",
		TestEnd:      "2024-01-30",
		State:        portfolioresearch.WindowStateOK,
		Metrics: &portfolioresearch.ValidationWindowMetrics{
			TradingDays:       10,
			NetReturn:         0.05,
			MaxDrawdown:       -0.02,
			CostDrag:          0.005,
			AnnualTurnover:    1.0,
			AvgCashRatio:      0.05,
			UnfilledRate:      0.01,
			MaxConcentration:  0.1,
			InformationRatio:  &ir,
			AnnualExcess:      &excess,
			BaselineIncrement: 0.01,
		},
		EvidenceClass: portfolioresearch.EvidenceRetrospective,
	}
	for _, m := range muts {
		m(&o)
	}
	return o
}

// saveWindow 保存窗口并把结果 ID 绑定到验证。
func (f *pvFixture) saveWindow(t *testing.T, id string, w portfolioresearch.ValidationWindowOutcome) {
	t.Helper()
	w.ValidationID = id
	if err := f.store.SaveWindow(id, w); err != nil {
		t.Fatalf("保存窗口失败: %v", err)
	}
}

// complete 完成一条验证（保存 1 个达标窗 + Complete）并断言成功。
func (f *pvFixture) complete(t *testing.T, id string, muts ...func(*portfolioresearch.ValidationWindowOutcome)) PortfolioValidationReport {
	t.Helper()
	f.saveWindow(t, id, pvWindow(1, muts...))
	rep, err := f.store.Complete(id)
	if err != nil {
		t.Fatalf("Complete 失败: %v", err)
	}
	return rep
}

// ---- 创建与冻结 ----

func TestPortfolioValidationStore_CreateFreezesSpec(t *testing.T) {
	f := newPvFixture(t)
	rec := f.createOne(t)
	if !validPortfolioValidationID(rec.ID) {
		t.Fatalf("服务端应分配合法验证 ID: %q", rec.ID)
	}
	if rec.SchemaVersion != portfolioValidationSchemaVersion || rec.CreatedAt == "" {
		t.Fatalf("schema/createdAt 应由 Store 填充: %+v", rec)
	}
	if len(rec.SpecHash) != 64 {
		t.Fatalf("specHash 应为 64 位十六进制: %q", rec.SpecHash)
	}
	// 证据等级由后端从冻结模型派生（请求无 evidenceClass 字段）。
	if rec.ModelEvidenceClass != portfolioresearch.EvidenceRetrospective {
		t.Fatalf("模型证据等级应后端派生: %q", rec.ModelEvidenceClass)
	}
	if rec.RequestID == "" || rec.RequestHash == "" {
		t.Fatalf("幂等键应冻结进记录: %+v", rec)
	}
	// 磁盘主记录存在且读取重算一致。
	view, err := f.store.Get(rec.ID)
	if err != nil {
		t.Fatalf("Get 失败: %v", err)
	}
	if view.State != PortfolioValidationStateCreated {
		t.Fatalf("未完成验证状态应为 created，实际 %q", view.State)
	}
	if view.Report != nil || len(view.Windows) != 0 {
		t.Fatalf("未完成验证不应有报告/窗口")
	}
}

func TestPortfolioValidationStore_CreateRejectsModelHashMismatch(t *testing.T) {
	f := newPvFixture(t)
	badHash := strings.Repeat("cd", 32)
	if _, _, err := f.store.Create(
		CreatePortfolioValidationRequest{
			RequestID: uniqueRequestID(int(atomic.AddInt32(&createSeq, 1)) + 950000),
			Spec: func() portfolioresearch.PortfolioValidationSpec {
				s := pvSpec()
				s.ModelRef.Hash = badHash
				return s
			}(),
		}, f.models); err == nil {
		t.Fatalf("模型 hash 不匹配应拒绝（fail closed）")
	}
	// 模型不存在同样拒绝。
	f2 := newPvFixture(t)
	f2.models.err = fmt.Errorf("模型不存在")
	if _, _, err := f2.store.Create(
		CreatePortfolioValidationRequest{
			RequestID: uniqueRequestID(int(atomic.AddInt32(&createSeq, 1)) + 960000),
			Spec:      pvSpec(),
		}, f2.models); err == nil {
		t.Fatalf("模型读取失败应拒绝")
	}
}

func TestPortfolioValidationStore_CreateIdempotentAndConflict(t *testing.T) {
	f := newPvFixture(t)
	req := CreatePortfolioValidationRequest{RequestID: testUUID1, Spec: pvSpec()}
	rec1, created1, err := f.store.Create(req, f.models)
	if err != nil || !created1 {
		t.Fatalf("首次创建 = %v/%v", created1, err)
	}
	rec2, created2, err := f.store.Create(req, f.models)
	if err != nil || created2 {
		t.Fatalf("同 ID 同内容应幂等返回: %v/%v", created2, err)
	}
	if rec2.ID != rec1.ID || rec2.SpecHash != rec1.SpecHash {
		t.Fatalf("幂等返回应为同一记录: %v vs %v", rec1.ID, rec2.ID)
	}
	// 同 ID 不同内容（改门禁）→ 冲突。
	conflict := req
	conflict.Spec.Gates.MinNetReturn = 0.99
	if _, _, err := f.store.Create(conflict, f.models); err != errIdempotencyConflict {
		t.Fatalf("同幂等键不同内容应冲突，实际 %v", err)
	}
}

// TestCreatePortfolioValidationRequestHash_NormalizesSpec 幂等请求 hash 归一
// 化：-0.0 与 +0.0 语义相同必须同 hash（否则同幂等键重复提交冲突）；语义
// 不同（改门禁）必须不同 hash。
func TestCreatePortfolioValidationRequestHash_NormalizesSpec(t *testing.T) {
	base := pvSpec()
	a := CreatePortfolioValidationRequest{RequestID: testUUID1, Spec: base}
	ha, err := createPortfolioValidationRequestHash(a)
	if err != nil {
		t.Fatalf("hash 失败: %v", err)
	}
	// -0.0 与 +0.0 语义相同 → 同请求 hash。
	b := a
	b.Spec.Gates.MinBaselineIncrement = math.Copysign(0, -1)
	hb, err := createPortfolioValidationRequestHash(b)
	if err != nil {
		t.Fatalf("hash 失败: %v", err)
	}
	if hb != ha {
		t.Fatalf("-0.0 应归一为 +0.0，同一请求 hash: %s vs %s", ha, hb)
	}
	// 门禁语义变化 → 不同请求 hash（幂等键复用冲突）。
	c := a
	c.Spec.Gates.MinNetReturn = 0.02
	hc, err := createPortfolioValidationRequestHash(c)
	if err != nil {
		t.Fatalf("hash 失败: %v", err)
	}
	if hc == ha {
		t.Fatalf("门禁变化应产生不同请求 hash（幂等键复用冲突）")
	}
	// 空证据要求 ≡ 默认 retrospective → 同请求 hash。
	d := a
	d.Spec.EvidenceRequirement = ""
	hd, err := createPortfolioValidationRequestHash(d)
	if err != nil {
		t.Fatalf("hash 失败: %v", err)
	}
	if hd != ha {
		t.Fatalf("空证据要求应归一为默认 retrospective，同一请求 hash")
	}
}

func TestPortfolioValidationStore_SpecHashFailClosed(t *testing.T) {
	f := newPvFixture(t)
	rec := f.createOne(t)
	// 篡改 request.json 中的规格（改门禁）→ 读取重算 spec hash 不匹配。
	path := filepath.Join(f.root, rec.ID, "request.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取主记录失败: %v", err)
	}
	tampered := strings.Replace(string(data), `"minNetReturn": 0.01`, `"minNetReturn": 0.02`, 1)
	if tampered == string(data) {
		t.Fatalf("测试数据未命中替换点（规格字段变化应使 hash 不匹配）")
	}
	if err := os.WriteFile(path, []byte(tampered), 0644); err != nil {
		t.Fatalf("写回失败: %v", err)
	}
	if _, err := f.store.Get(rec.ID); err == nil {
		t.Fatalf("规格被篡改应 fail closed")
	}
}

// ---- 窗口写入与完成 ----

func TestPortfolioValidationStore_SaveWindowAppendOnly(t *testing.T) {
	f := newPvFixture(t)
	rec := f.createOne(t)
	w1 := pvWindow(1)
	f.saveWindow(t, rec.ID, w1)
	f.saveWindow(t, rec.ID, pvWindow(2))
	// 完成前允许重写单窗（崩溃恢复语义）。
	w1b := pvWindow(1, func(o *portfolioresearch.ValidationWindowOutcome) { o.Metrics.NetReturn = 0.08 })
	f.saveWindow(t, rec.ID, w1b)

	if _, err := f.store.Complete(rec.ID); err != nil {
		t.Fatalf("Complete 失败: %v", err)
	}
	// 完成后拒绝写入（不可变记录）。
	w3 := pvWindow(3)
	w3.ValidationID = rec.ID
	if err := f.store.SaveWindow(rec.ID, w3); !errors.Is(err, errPortfolioValidationCompleted) {
		t.Fatalf("完成后保存窗口应拒绝: %v", err)
	}
	// 窗口结果 ID 与验证不一致 → 拒绝。
	if err := f.store.SaveWindow(rec.ID, func() portfolioresearch.ValidationWindowOutcome {
		w := pvWindow(9)
		w.ValidationID = "pv_20260917T150000000Z_ffffffff"
		return w
	}()); err == nil {
		t.Fatalf("窗口 ID 与验证不一致应拒绝")
	}
}

func TestPortfolioValidationStore_CompleteBackendVerdicts(t *testing.T) {
	t.Run("passed", func(t *testing.T) {
		f := newPvFixture(t)
		rec := f.createOne(t)
		rep := f.complete(t, rec.ID)
		if rep.Verdict != portfolioresearch.GateStatusPassed {
			t.Fatalf("达标窗应 passed，实际 %q（%s）", rep.Verdict, rep.Message)
		}
		if rep.EvidenceClass != portfolioresearch.EvidenceRetrospective || len(rep.Gates) == 0 {
			t.Fatalf("报告证据/门禁披露异常: %+v", rep)
		}
		if rep.ValidationID != rec.ID || rep.FinishedAt == "" {
			t.Fatalf("报告标识/时间应由 Store 填充: %+v", rep)
		}
	})
	t.Run("failed_by_gate", func(t *testing.T) {
		f := newPvFixture(t)
		rec := f.createOne(t, func(r *CreatePortfolioValidationRequest) { r.Spec.Gates.MinNetReturn = 0.99 })
		rep := f.complete(t, rec.ID)
		if rep.Verdict != portfolioresearch.GateStatusFailed {
			t.Fatalf("净收益低于门禁应 failed，实际 %q", rep.Verdict)
		}
	})
	t.Run("insufficient_windows", func(t *testing.T) {
		f := newPvFixture(t)
		rec := f.createOne(t, func(r *CreatePortfolioValidationRequest) { r.Spec.Gates.MinValidWindows = 3 })
		f.saveWindow(t, rec.ID, pvWindow(1))
		rep, err := f.store.Complete(rec.ID)
		if err != nil {
			t.Fatalf("Complete 失败: %v", err)
		}
		if rep.Verdict != portfolioresearch.GateStatusInsufficient {
			t.Fatalf("有效窗口不足应 insufficient（不是 failed），实际 %q", rep.Verdict)
		}
	})
	t.Run("error_window", func(t *testing.T) {
		f := newPvFixture(t)
		rec := f.createOne(t)
		f.saveWindow(t, rec.ID, pvWindow(1))
		f.saveWindow(t, rec.ID, func() portfolioresearch.ValidationWindowOutcome {
			w := pvWindow(2)
			w.State = portfolioresearch.WindowStateError
			w.Message = "行情数据读取失败"
			w.Metrics = nil
			return w
		}())
		rep, err := f.store.Complete(rec.ID)
		if err != nil {
			t.Fatalf("Complete 失败: %v", err)
		}
		if rep.Verdict != portfolioresearch.GateStatusError {
			t.Fatalf("执行错误应 error（不伪装统计失败），实际 %q", rep.Verdict)
		}
	})
}

func TestPortfolioValidationStore_CompleteSingleTimeAndNoWindows(t *testing.T) {
	f := newPvFixture(t)
	rec := f.createOne(t)
	// 无窗口结果 → 拒绝生成结论。
	if _, err := f.store.Complete(rec.ID); err == nil {
		t.Fatalf("无窗口结果 Complete 应报错")
	}
	f.saveWindow(t, rec.ID, pvWindow(1))
	if _, err := f.store.Complete(rec.ID); err != nil {
		t.Fatalf("Complete 失败: %v", err)
	}
	// 重复完成拒绝（只允许一次）。
	if _, err := f.store.Complete(rec.ID); !errors.Is(err, errPortfolioValidationCompleted) {
		t.Fatalf("重复完成应拒绝: %v", err)
	}
}

func TestPortfolioValidationStore_GetRecomputeVerdictFailClosed(t *testing.T) {
	f := newPvFixture(t)
	rec := f.createOne(t)
	f.complete(t, rec.ID)
	// 篡改窗口结果（净收益降至门禁以下）→ 读取重算结论与存储不一致。
	path := filepath.Join(f.root, rec.ID, "windows", "0001.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取窗口失败: %v", err)
	}
	tampered := strings.Replace(string(data), `"netReturn": 0.05`, `"netReturn": -0.5`, 1)
	if tampered == string(data) {
		t.Fatalf("测试数据未命中替换点")
	}
	if err := os.WriteFile(path, []byte(tampered), 0644); err != nil {
		t.Fatalf("写回失败: %v", err)
	}
	if _, err := f.store.Get(rec.ID); err == nil {
		t.Fatalf("窗口篡改后 Get 应 fail closed（最终结论重算不一致）")
	}
	// 窗口文件损坏同样 fail closed。
	f2 := newPvFixture(t)
	rec2 := f2.createOne(t)
	f2.complete(t, rec2.ID)
	if err := os.WriteFile(filepath.Join(f2.root, rec2.ID, "windows", "0001.json"), []byte("{broken"), 0644); err != nil {
		t.Fatalf("写坏窗口失败: %v", err)
	}
	if _, err := f2.store.Get(rec2.ID); err == nil {
		t.Fatalf("损坏窗口文件应 fail closed")
	}
}

// ---- Supersedes：修改门禁 → 新验证 ID，旧窗标已见数据 ----

func TestPortfolioValidationStore_SupersedeCreatesNewID(t *testing.T) {
	f := newPvFixture(t)
	// 旧验证 A：门禁 MinNetReturn=0.01，达标通过。
	a := f.createOne(t)
	f.complete(t, a.ID)
	// 测试后修改门禁（MinNetReturn 调高）→ 必须创建新验证 B，旧验证保留。
	b := f.createOne(t, func(r *CreatePortfolioValidationRequest) {
		r.Spec.Gates.MinNetReturn = 0.99 // 修改门禁
		r.Supersedes = a.ID
	})
	if b.ID == a.ID {
		t.Fatalf("修改门禁必须产生新验证 ID")
	}
	if b.SpecHash == a.SpecHash {
		t.Fatalf("修改门禁必须产生新 spec hash")
	}
	// 旧验证保留并标记 supersededBy（旧测试窗从此视为已见数据）。
	viewA, err := f.store.Get(a.ID)
	if err != nil {
		t.Fatalf("旧验证应保留可读: %v", err)
	}
	if viewA.Report == nil || viewA.Report.Verdict != portfolioresearch.GateStatusPassed {
		t.Fatalf("旧验证报告应保留: %+v", viewA.Report)
	}
	if len(viewA.SupersededBy) != 1 || viewA.SupersededBy[0] != b.ID {
		t.Fatalf("旧验证应派生 SupersededBy=[%s]，实际 %v", b.ID, viewA.SupersededBy)
	}
	viewB, err := f.store.Get(b.ID)
	if err != nil {
		t.Fatalf("新验证读取失败: %v", err)
	}
	if len(viewB.SupersededBy) != 0 {
		t.Fatalf("新验证不应被替换: %v", viewB.SupersededBy)
	}
}

// ---- 列表 ----

func TestPortfolioValidationStore_ListPaginationAndFilter(t *testing.T) {
	f := newPvFixture(t)
	// 3 条记录：2 条 completed（passed/failed 各一），1 条 created。
	r1 := f.createOne(t)
	f.complete(t, r1.ID)
	r2 := f.createOne(t, func(r *CreatePortfolioValidationRequest) { r.Spec.Gates.MinNetReturn = 0.99 })
	f.complete(t, r2.ID)
	r3 := f.createOne(t)

	page, err := f.store.List(PortfolioValidationFilter{}, 1, 2)
	if err != nil {
		t.Fatalf("List 失败: %v", err)
	}
	if page.Total != 3 || len(page.Items) != 2 || page.Page != 1 || page.PageSize != 2 {
		t.Fatalf("分页边界错误: %+v", page)
	}
	if page.Items[0].ID != r3.ID {
		t.Fatalf("稳定排序最新在前: 最新创建 %s，实际 %s", r3.ID, page.Items[0].ID)
	}
	page2, err := f.store.List(PortfolioValidationFilter{}, 2, 2)
	if err != nil {
		t.Fatalf("List 失败: %v", err)
	}
	if len(page2.Items) != 1 || page2.Total != 3 {
		t.Fatalf("第二页应剩 1 条: %+v", page2)
	}
	// 越界页返回空 items + 真实 total。
	page3, err := f.store.List(PortfolioValidationFilter{}, 9, 2)
	if err != nil || len(page3.Items) != 0 || page3.Total != 3 {
		t.Fatalf("越界页应空: %+v/%v", page3, err)
	}
	// 过滤：模型。
	byModel, err := f.store.List(PortfolioValidationFilter{ModelID: pvModelID}, 1, 20)
	if err != nil || byModel.Total != 3 {
		t.Fatalf("模型过滤错误: %+v/%v", byModel, err)
	}
	byWrong, err := f.store.List(PortfolioValidationFilter{ModelID: "fm_20260917T150200000Z_88cceeaa"}, 1, 20)
	if err != nil || byWrong.Total != 0 {
		t.Fatalf("无关模型不应命中: %+v/%v", byWrong, err)
	}
	// 过滤：结论（仅 completed）。
	byPassed, err := f.store.List(PortfolioValidationFilter{Verdict: portfolioresearch.GateStatusPassed}, 1, 20)
	if err != nil || byPassed.Total != 1 || byPassed.Items[0].ID != r1.ID {
		t.Fatalf("passed 过滤错误: %+v/%v", byPassed, err)
	}
	byFailed, err := f.store.List(PortfolioValidationFilter{Verdict: portfolioresearch.GateStatusFailed}, 1, 20)
	if err != nil || byFailed.Total != 1 || byFailed.Items[0].ID != r2.ID {
		t.Fatalf("failed 过滤错误: %+v/%v", byFailed, err)
	}
	// 过滤：证据等级（completed 用报告证据；created 用模型证据）。
	byEvidence, err := f.store.List(PortfolioValidationFilter{EvidenceClass: portfolioresearch.EvidenceRetrospective}, 1, 20)
	if err != nil || byEvidence.Total != 3 {
		t.Fatalf("证据过滤错误: %+v/%v", byEvidence, err)
	}
	// 分页参数校验。
	if _, err := f.store.List(PortfolioValidationFilter{}, 0, 10); err == nil {
		t.Fatalf("page<1 应拒绝")
	}
	if _, err := f.store.List(PortfolioValidationFilter{}, 1, 0); err == nil {
		t.Fatalf("pageSize<1 应拒绝")
	}
	if _, err := f.store.List(PortfolioValidationFilter{}, 1, MaxPortfolioValidationPageSize+1); err == nil {
		t.Fatalf("pageSize 超上限应拒绝")
	}
}

// ---- 安全与损坏 ----

func TestPortfolioValidationStore_PathTraversalRejected(t *testing.T) {
	f := newPvFixture(t)
	if _, err := f.store.Get("../evil"); err == nil {
		t.Fatalf("路径穿越 ID 应拒绝")
	}
	if _, err := f.store.Get("pv_x"); err == nil {
		t.Fatalf("非法 ID 应拒绝")
	}
	w := pvWindow(1)
	w.ValidationID = "../evil"
	if err := f.store.SaveWindow("../evil", w); err == nil {
		t.Fatalf("路径穿越窗口写入应拒绝")
	}
	w2 := pvWindow(1)
	w2.ValidationID = "pv_20260917T150000000Z_ffffffff"
	if err := f.store.SaveWindow("pv_20260917T150000000Z_ffffffff", w2); !errors.Is(err, errPortfolioValidationNotFound) {
		t.Fatalf("不存在验证保存窗口应报不存在，实际 %v", err)
	}
}
