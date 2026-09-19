package lab

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/injoyai/strategy-tail/internal/portfolioresearch"
)

// factor_model_test.go v2 Task 1 装配/存储层测试：上游验证 → FactorModel
// 装配（拒绝客户端自报证据等级、上游 hash 重校验）、追加式 revision 存储
// （幂等、并发不覆盖、旧 revision 可读、归档保留历史）、安全处理（路径
// 穿越、重复 ID、损坏 JSON、原子写失败）。

// factorModelFixture 完整依赖链：验证库（含冻结验证）+ 适配器 + 空模型库。
type factorModelFixture struct {
	Store        *FactorModelStore
	Input        ValidationInput
	ValidationID string
	Root         string
}

func newFactorModelFixture(t *testing.T) factorModelFixture {
	t.Helper()
	vf := newValidationStoreFixture(t)
	vr, _, err := vf.Store.Create(validCreateReq(vf, testUUID1), vf.deps())
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	return factorModelFixture{
		Store:        NewFactorModelStore(root),
		Input:        NewValidationStoreAdapter(vf.Store),
		ValidationID: vr.ID,
		Root:         root,
	}
}

// validModelReq 合法模型请求（指向夹具验证，等权秩基线）。
func validModelReq(validationID, requestID string) CreateFactorModelRequest {
	return CreateFactorModelRequest{
		RequestID:         requestID,
		CreatedBy:         "tester",
		ResearchQuestion:  "动量与波动因子组合是否稳定？",
		Hypothesis:        "等权秩合成能提升样本外稳定性",
		FactorValidations: []string{validationID},
		TransformPipeline: portfolioresearch.TransformPipeline{
			Missing:     portfolioresearch.TransformMissingExclude,
			Winsorize:   portfolioresearch.WinsorizeSpec{Mode: portfolioresearch.TransformWinsorizeQuantile, Quantile: 0.01},
			Neutralize:  portfolioresearch.NeutralizeSpec{Mode: portfolioresearch.TransformNeutralizeNone},
			Standardize: portfolioresearch.TransformStandardizeRank,
		},
		Combination: portfolioresearch.CombinationSpec{Method: portfolioresearch.CombinationEqualWeightRank},
		PortfolioPolicy: portfolioresearch.PortfolioPolicy{
			Selection: portfolioresearch.PortfolioSelectionTopN, TopN: 20, CashBuffer: 0.05,
		},
		Execution: portfolioresearch.ExecutionSpec{
			Rebalance: portfolioresearch.RebalanceDaily, FillAt: portfolioresearch.FillNextOpen,
			SellFirst: true, T1Restriction: true, LotSize: 100,
			Cost: portfolioresearch.CostSpec{CommissionRate: 0.0003, StampDutyRate: 0.001,
				Slippage: 0.01, MinCommission: 5},
		},
		Benchmark: portfolioresearch.BenchmarkSpec{ID: "hs300"},
	}
}

// appendReq 追加 revision 请求（基于已创建模型的最新 revision）。
func appendReq(base portfolioresearch.FactorModel, requestID string) CreateFactorModelRequest {
	req := validModelReq(base.ValidatedFactors[0].ValidationID, requestID)
	req.ModelID = base.ModelID
	req.BaseRevision = base.Revision
	return req
}

// TestFactorModelStoreCreateAssembles 装配完整：服务端生成 modelId/revision/
// modelHash；因子引用来自上游冻结记录（候选 revision/实例/版本/方向/证据/
// 快照）；模型证据等级 = 最弱输入。
func TestFactorModelStoreCreateAssembles(t *testing.T) {
	f := newFactorModelFixture(t)
	m, created, err := f.Store.Create(validModelReq(f.ValidationID, testUUID1), f.Input)
	if err != nil || !created {
		t.Fatalf("创建 = %v/%v", created, err)
	}
	if !validModelID(m.ModelID) || m.Revision != 1 || m.ModelHash == "" || m.CreatedAt == "" {
		t.Fatalf("modelId/revision/modelHash/createdAt 应由 Store 填充: %+v", m)
	}
	if m.CreateRequestID != testUUID1 || m.CreateRequestHash == "" {
		t.Fatalf("幂等键应冻结进记录: %+v", m)
	}
	if len(m.ValidatedFactors) != 1 {
		t.Fatalf("因子引用应为 1: %d", len(m.ValidatedFactors))
	}
	ref := m.ValidatedFactors[0]
	if ref.ValidationID != f.ValidationID {
		t.Fatalf("validationId = %q", ref.ValidationID)
	}
	if ref.CandidateID == "" || ref.CandidateRevision < 1 ||
		ref.FactorKind == "" || ref.FactorDays < 1 || ref.ImplementationVersion < 1 {
		t.Fatalf("候选/因子实例字段应来自上游: %+v", ref)
	}
	// 夹具协议 ExpectedDirection=positive → higher_is_better（后端显式映射）。
	if ref.Direction != portfolioresearch.DirectionHigherIsBetter {
		t.Fatalf("方向应映射为 higher_is_better: %q", ref.Direction)
	}
	if ref.EvidenceClass != portfolioresearch.EvidenceRetrospective {
		t.Fatalf("证据等级应来自上游: %q", ref.EvidenceClass)
	}
	if ref.PrimaryHorizon < 1 {
		t.Fatalf("primaryHorizon 应来自上游主周期: %d", ref.PrimaryHorizon)
	}
	if ref.DataSnapshot.PriceSource == "" || ref.DataSnapshot.UniverseMode == "" {
		t.Fatalf("数据快照应来自研究协议: %+v", ref.DataSnapshot)
	}
	if m.EvidenceClass != portfolioresearch.EvidenceRetrospective {
		t.Fatalf("模型证据等级 = 最弱输入: %q", m.EvidenceClass)
	}
}

// TestFactorModelStoreIdempotentAndConflict 幂等：同 RequestID 同内容返回
// 已有记录；同 ID 不同内容冲突。
func TestFactorModelStoreIdempotentAndConflict(t *testing.T) {
	f := newFactorModelFixture(t)
	m1, created1, err := f.Store.Create(validModelReq(f.ValidationID, testUUID1), f.Input)
	if err != nil || !created1 {
		t.Fatalf("首次创建 = %v/%v", created1, err)
	}
	m2, created2, err := f.Store.Create(validModelReq(f.ValidationID, testUUID1), f.Input)
	if err != nil || created2 || m2.ModelID != m1.ModelID || m2.Revision != m1.Revision {
		t.Fatalf("幂等重试应返回同一记录: %v/%v/%v", created2, m2.ModelID, err)
	}
	conflict := validModelReq(f.ValidationID, testUUID1)
	conflict.PortfolioPolicy.TopN = 30
	if _, _, err := f.Store.Create(conflict, f.Input); !errors.Is(err, errIdempotencyConflict) {
		t.Fatalf("幂等键复用应冲突: %v", err)
	}
}

// TestFactorModelStoreIdempotentScanAllRevisions 幂等扫描覆盖全部 revision：
// 追加 revision 会把最新记录的幂等键覆盖为追加请求 ID，旧请求重放仍必须
// 命中旧 revision 记录（created=false、同一 modelID），不得静默新建模型；
// 旧请求 ID 复用不同内容 → 幂等键冲突。
func TestFactorModelStoreIdempotentScanAllRevisions(t *testing.T) {
	f := newFactorModelFixture(t)
	// ① requestX 创建 rev1。
	m1, created, err := f.Store.Create(validModelReq(f.ValidationID, testUUID1), f.Input)
	if err != nil || !created {
		t.Fatalf("首次创建 = %v/%v", created, err)
	}
	// ② requestY 追加 rev2（最新记录的幂等键被覆盖为 Y）。
	reqY := appendReq(m1, testUUID2)
	reqY.PortfolioPolicy.TopN = 40
	m2, created, err := f.Store.Create(reqY, f.Input)
	if err != nil || !created || m2.Revision != 2 {
		t.Fatalf("追加 = %v/%v", created, err)
	}
	// ③ 重放 requestX（原内容）→ 幂等返回 rev1 记录，不得新建模型。
	m3, created, err := f.Store.Create(validModelReq(f.ValidationID, testUUID1), f.Input)
	if err != nil || created {
		t.Fatalf("重放旧请求应幂等返回（created=false）: %v/%v", created, err)
	}
	if m3.ModelID != m1.ModelID || m3.Revision != 1 || m3.CreateRequestID != testUUID1 {
		t.Fatalf("应返回 rev1 原记录: %+v", m3)
	}
	if m3.ModelHash != m1.ModelHash {
		t.Fatalf("重放应返回与首次一致的记录: hash %s vs %s", m3.ModelHash, m1.ModelHash)
	}
	// ④ 同 requestX 但不同内容 → 幂等键冲突。
	conflict := validModelReq(f.ValidationID, testUUID1)
	conflict.PortfolioPolicy.TopN = 30
	if _, _, err := f.Store.Create(conflict, f.Input); !errors.Is(err, errIdempotencyConflict) {
		t.Fatalf("旧请求 ID 复用不同内容应冲突: %v", err)
	}
	// ⑤ 模型数不变：全为幂等命中/冲突，未新建 fm2。
	got, err := f.Store.List(false)
	if err != nil || len(got) != 1 || got[0].ModelID != m1.ModelID {
		t.Fatalf("不应新建模型: %d/%v", len(got), err)
	}
}

// TestFactorModelStoreRejectsClientEvidenceClass 客户端篡改证据等级被拒绝：
// 请求结构无 evidenceClass 字段（JSON 注入被反序列化忽略），模型证据等级
// 完全由后端从上游验证派生，客户端声明不产生任何效果。
func TestFactorModelStoreRejectsClientEvidenceClass(t *testing.T) {
	f := newFactorModelFixture(t)
	req := validModelReq(f.ValidationID, testUUID1)

	// 客户端尝试在 JSON 里塞入高于输入（retrospective）的证据等级。
	buf, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(buf, &doc); err != nil {
		t.Fatal(err)
	}
	doc["evidenceClass"] = portfolioresearch.EvidenceProspective
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	var injected CreateFactorModelRequest
	if err := json.Unmarshal(raw, &injected); err != nil {
		t.Fatal(err)
	}
	m, _, err := f.Store.Create(injected, f.Input)
	if err != nil {
		t.Fatal(err)
	}
	if m.EvidenceClass != portfolioresearch.EvidenceRetrospective {
		t.Fatalf("模型证据等级应来自上游（retrospective），客户端注入的 prospective 无效: %q", m.EvidenceClass)
	}
	// 模型证据等级不得高于任一输入（= min，不接受客户端声明）。
	if m.EvidenceClass == portfolioresearch.EvidenceProspective {
		t.Fatal("客户端不得把模型证据等级升格为 prospective")
	}
}

// TestFactorModelStoreRejectsUpstreamHashMismatch 创建 revision 时重新读取
// 并校验上游验证 hash：上游冻结记录被篡改后，装配必须 fail closed。
func TestFactorModelStoreRejectsUpstreamHashMismatch(t *testing.T) {
	vf := newValidationStoreFixture(t)
	vr, _, err := vf.Store.Create(validCreateReq(vf, testUUID1), vf.deps())
	if err != nil {
		t.Fatal(err)
	}
	adapter := NewValidationStoreAdapter(vf.Store)
	store := NewFactorModelStore(t.TempDir())
	// 首次创建成功，证明装配路径可读。
	if _, _, err := store.Create(validModelReq(vr.ID, testUUID1), adapter); err != nil {
		t.Fatal(err)
	}
	// 篡改上游验证冻结请求（不重算 hash）。
	path := vf.Store.requestPath(vr.ID)
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
	// 新模型引用同一（已损坏）验证 → 拒绝。
	if _, _, err := store.Create(validModelReq(vr.ID, testUUID2), adapter); err == nil {
		t.Fatal("上游验证 hash 不匹配应阻止新模型创建")
	}
}

// TestFactorModelStoreAppendRevisionKeepsHistory 追加 revision：新 revision
// 新 modelHash；旧 revision 仍可读取；语义变化改变 hash。
func TestFactorModelStoreAppendRevisionKeepsHistory(t *testing.T) {
	f := newFactorModelFixture(t)
	m1, _, err := f.Store.Create(validModelReq(f.ValidationID, testUUID1), f.Input)
	if err != nil {
		t.Fatal(err)
	}
	req := appendReq(m1, testUUID2)
	req.PortfolioPolicy.TopN = 40
	m2, created, err := f.Store.Create(req, f.Input)
	if err != nil || !created {
		t.Fatalf("追加 = %v/%v", created, err)
	}
	if m2.Revision != 2 || m2.ModelID != m1.ModelID {
		t.Fatalf("追加 revision 应为 2: %+v", m2)
	}
	if m2.ModelHash == m1.ModelHash {
		t.Fatal("语义变化（TopN）必须产生新 modelHash")
	}
	// 旧 revision 仍可读取且内容不变。
	got1, err := f.Store.Get(m1.ModelID, 1)
	if err != nil {
		t.Fatalf("旧 revision 应可读: %v", err)
	}
	if got1.Revision != 1 || got1.ModelHash != m1.ModelHash {
		t.Fatalf("旧 revision 内容应保持: %+v", got1)
	}
	latest, err := f.Store.GetLatest(m1.ModelID)
	if err != nil || latest.Revision != 2 {
		t.Fatalf("GetLatest 应返回最新: %v/%+v", err, latest)
	}
	// 追加时基础 revision 过期 → 冲突（乐观并发）。
	stale := appendReq(m1, "9b2f1c3d-4e5f-4a7b-8c9d-0e1f2a3b4c5e")
	stale.BaseRevision = 1
	if _, _, err := f.Store.Create(stale, f.Input); !errors.Is(err, errFactorModelRevisionConflict) {
		t.Fatalf("过期 baseRevision 应冲突: %v", err)
	}
}

// TestFactorModelStoreConcurrentRevisionNoOverwrite 并发追加不覆盖：恰好一个
// 成功写入新 revision，另一个因基础 revision 过期冲突；revision 链无跳号、
// 无覆盖。
func TestFactorModelStoreConcurrentRevisionNoOverwrite(t *testing.T) {
	f := newFactorModelFixture(t)
	m1, _, err := f.Store.Create(validModelReq(f.ValidationID, testUUID1), f.Input)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	uuids := []string{testUUID2, "9b2f1c3d-4e5f-4a7b-8c9d-0e1f2a3b4c5e"}
	for _, u := range uuids {
		wg.Add(1)
		go func(u string) {
			defer wg.Done()
			_, _, err := f.Store.Create(appendReq(m1, u), f.Input)
			errs <- err
		}(u)
	}
	wg.Wait()
	close(errs)
	success, conflict := 0, 0
	for err := range errs {
		switch {
		case err == nil:
			success++
		case errors.Is(err, errFactorModelRevisionConflict):
			conflict++
		default:
			t.Fatalf("意外错误: %v", err)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatalf("并发追加应恰好一成功一冲突: success=%d conflict=%d", success, conflict)
	}
	latest, err := f.Store.GetLatest(m1.ModelID)
	if err != nil || latest.Revision != 2 {
		t.Fatalf("最新 revision 应为 2: %v/%+v", err, latest)
	}
	if _, err := f.Store.Get(m1.ModelID, 1); err != nil {
		t.Fatalf("revision 1 应保留: %v", err)
	}
	if _, err := os.Stat(filepath.Join(f.Root, m1.ModelID, "000003.json")); !os.IsNotExist(err) {
		t.Fatal("并发追加不得产生跳号 revision")
	}
}

// TestFactorModelStoreRejectsPathTraversal 路径穿越一律拒绝：非法模型 ID
// 在进入文件路径前被拦截。
func TestFactorModelStoreRejectsPathTraversal(t *testing.T) {
	f := newFactorModelFixture(t)
	store := f.Store
	bad := []string{"../evil", "..\\evil", "fm_../../evil", "a/b", "fm_x"}
	for _, id := range bad {
		if _, err := store.Get(id, 1); err == nil {
			t.Fatalf("Get(%q) 应拒绝", id)
		}
		if _, err := store.GetLatest(id); err == nil {
			t.Fatalf("GetLatest(%q) 应拒绝", id)
		}
		if err := store.Archive(id); err == nil {
			t.Fatalf("Archive(%q) 应拒绝", id)
		}
	}
	// 创建请求携带非法 ModelID（追加路径）→ normalize 拒绝。
	req := validModelReq(f.ValidationID, testUUID1)
	req.ModelID = "../evil"
	req.BaseRevision = 1
	if _, _, err := store.Create(req, f.Input); err == nil || !strings.Contains(err.Error(), "非法模型 ID") {
		t.Fatalf("Create 携带穿越 ModelID 应拒绝: %v", err)
	}
	// 请求携带非法上游验证 ID。
	badReq := validModelReq(f.ValidationID, testUUID1)
	badReq.FactorValidations = []string{"../../evil"}
	if _, _, err := store.Create(badReq, f.Input); err == nil {
		t.Fatal("非法上游验证 ID 应拒绝")
	}
}

// TestFactorModelStoreRejectsDuplicateIDs 重复 ID 拒绝：同一上游验证在
// factorValidations 中出现两次。
func TestFactorModelStoreRejectsDuplicateIDs(t *testing.T) {
	f := newFactorModelFixture(t)
	req := validModelReq(f.ValidationID, testUUID1)
	req.FactorValidations = []string{f.ValidationID, f.ValidationID}
	if _, _, err := f.Store.Create(req, f.Input); err == nil || !strings.Contains(err.Error(), "重复") {
		t.Fatalf("重复验证 ID 应拒绝: %v", err)
	}
}

// TestFactorModelStoreFailClosedOnCorruptJSON 损坏 JSON fail closed：revision
// 文件被写坏后 Get/GetLatest/List 一律报错，不回退旧 revision 冒充最新。
func TestFactorModelStoreFailClosedOnCorruptJSON(t *testing.T) {
	f := newFactorModelFixture(t)
	m, _, err := f.Store.Create(validModelReq(f.ValidationID, testUUID1), f.Input)
	if err != nil {
		t.Fatal(err)
	}
	revPath := filepath.Join(f.Root, m.ModelID, "000001.json")
	if err := os.WriteFile(revPath, []byte("{broken json"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Store.Get(m.ModelID, 1); err == nil {
		t.Fatal("损坏 JSON 应 fail closed")
	}
	if _, err := f.Store.GetLatest(m.ModelID); err == nil {
		t.Fatal("损坏 JSON 下 GetLatest 应报错，不得回退")
	}
	if _, err := f.Store.List(false); err == nil {
		t.Fatal("损坏 JSON 下 List 应 fail closed")
	}
}

// TestFactorModelStoreFailClosedOnHashTamper 语义字段被篡改（不重算 hash）
// → Get fail closed（modelHash 不匹配）；派生字段（evidenceClass）被篡改而
// hash 不覆盖 → 读取校验（证据等级 = 最弱输入）同样 fail closed。
func TestFactorModelStoreFailClosedOnHashTamper(t *testing.T) {
	f := newFactorModelFixture(t)
	m, _, err := f.Store.Create(validModelReq(f.ValidationID, testUUID1), f.Input)
	if err != nil {
		t.Fatal(err)
	}
	revPath := filepath.Join(f.Root, m.ModelID, "000001.json")
	readDoc := func() map[string]any {
		data, err := os.ReadFile(revPath)
		if err != nil {
			t.Fatal(err)
		}
		var doc map[string]any
		if err := json.Unmarshal(data, &doc); err != nil {
			t.Fatal(err)
		}
		return doc
	}
	writeDoc := func(doc map[string]any) {
		tampered, err := json.Marshal(doc)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(revPath, tampered, 0644); err != nil {
			t.Fatal(err)
		}
	}

	// 1) 语义字段篡改 → modelHash 不匹配。
	doc := readDoc()
	factors := doc["validatedFactors"].([]any)
	factors[0].(map[string]any)["factorDays"] = 999
	writeDoc(doc)
	if _, err := f.Store.Get(m.ModelID, 1); err == nil {
		t.Fatal("语义字段篡改后 Get 应报错（modelHash 不匹配）")
	}

	// 2) 还原后篡改派生字段 evidenceClass（hash 不覆盖）→ 读取校验拒绝。
	doc = readDoc()
	doc["validatedFactors"].([]any)[0].(map[string]any)["evidenceClass"] = portfolioresearch.EvidenceProspective
	writeDoc(doc)
	if _, err := f.Store.Get(m.ModelID, 1); err == nil {
		t.Fatal("证据等级被篡改后 Get 应报错（读取校验拒绝）")
	}
}

// TestFactorModelStoreAtomicWriteFailure 原子写失败安全处理：.tmp 路径被占用
// 时 Create 返回错误，不产生半成品 revision，旧 revision 不受影响。
func TestFactorModelStoreAtomicWriteFailure(t *testing.T) {
	f := newFactorModelFixture(t)
	m1, _, err := f.Store.Create(validModelReq(f.ValidationID, testUUID1), f.Input)
	if err != nil {
		t.Fatal(err)
	}
	// 把追加 revision 的 .tmp 路径预先占为目录，迫使原子写失败。
	tmpDir := filepath.Join(f.Root, m1.ModelID, ".tmp-000002.json")
	if err := os.MkdirAll(tmpDir, 0755); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.Store.Create(appendReq(m1, testUUID2), f.Input); err == nil {
		t.Fatal("原子写失败应返回错误")
	}
	latest, err := f.Store.GetLatest(m1.ModelID)
	if err != nil || latest.Revision != 1 {
		t.Fatalf("失败后最新 revision 仍应为 1: %v/%+v", err, latest)
	}
	if _, err := os.Stat(filepath.Join(f.Root, m1.ModelID, "000002.json")); !os.IsNotExist(err) {
		t.Fatal("原子写失败不得留下半成品 revision")
	}
}

// TestFactorModelStoreArchivePreservesHistory 归档只写标记、不删除历史引用：
// 归档后 List 隐藏、List(true) 可见、旧 revision 仍可读、重复归档幂等。
func TestFactorModelStoreArchivePreservesHistory(t *testing.T) {
	f := newFactorModelFixture(t)
	m, _, err := f.Store.Create(validModelReq(f.ValidationID, testUUID1), f.Input)
	if err != nil {
		t.Fatal(err)
	}
	before, err := f.Store.List(false)
	if err != nil || len(before) != 1 {
		t.Fatalf("归档前 List 应有 1 个: %d/%v", len(before), err)
	}
	if err := f.Store.Archive(m.ModelID); err != nil {
		t.Fatal(err)
	}
	at, err := f.Store.ArchivedAt(m.ModelID)
	if err != nil || at == "" {
		t.Fatalf("归档时间应非空: %q/%v", at, err)
	}
	// 归档后默认列表隐藏，includeArchived=true 可见。
	if got, err := f.Store.List(false); err != nil || len(got) != 0 {
		t.Fatalf("归档后默认 List 应为空: %d/%v", len(got), err)
	}
	if got, err := f.Store.List(true); err != nil || len(got) != 1 || got[0].ModelID != m.ModelID {
		t.Fatalf("includeArchived 应包含归档模型: %+v/%v", got, err)
	}
	// 历史引用不破坏：全部 revision 仍可读。
	if got, err := f.Store.Get(m.ModelID, m.Revision); err != nil || got.ModelHash != m.ModelHash {
		t.Fatalf("归档后历史 revision 应可读: %+v/%v", got, err)
	}
	// 重复归档幂等（保留首次归档时间）。
	if err := f.Store.Archive(m.ModelID); err != nil {
		t.Fatal(err)
	}
	at2, err := f.Store.ArchivedAt(m.ModelID)
	if err != nil || at2 != at {
		t.Fatalf("重复归档应保留首次时间: %q vs %q", at2, at)
	}
}

// TestMapExpectedDirection 方向映射显式 switch：positive/negative 映射，
// 未知值（含 two_sided）报错。
func TestMapExpectedDirection(t *testing.T) {
	if got, err := mapExpectedDirection(labelDirectionPositive); err != nil || got != portfolioresearch.DirectionHigherIsBetter {
		t.Fatalf("positive = %q/%v", got, err)
	}
	if got, err := mapExpectedDirection(labelDirectionNegative); err != nil || got != portfolioresearch.DirectionLowerIsBetter {
		t.Fatalf("negative = %q/%v", got, err)
	}
	for _, bad := range []string{labelDirectionTwoSided, "", "bogus"} {
		if _, err := mapExpectedDirection(bad); err == nil {
			t.Fatalf("方向 %q 应报错", bad)
		}
	}
}

// TestMapValidationEvidenceClass 证据等级映射：retrospective/prospective
// 转换，未知值报错。
func TestMapValidationEvidenceClass(t *testing.T) {
	if got, err := mapValidationEvidenceClass(validationEvidenceRetrospective); err != nil || got != portfolioresearch.EvidenceRetrospective {
		t.Fatalf("retrospective = %q/%v", got, err)
	}
	if got, err := mapValidationEvidenceClass(validationEvidenceProspective); err != nil || got != portfolioresearch.EvidenceProspective {
		t.Fatalf("prospective = %q/%v", got, err)
	}
	if _, err := mapValidationEvidenceClass(evidenceExploratory); err == nil {
		t.Fatal("exploratory 不是验证层证据等级，应报错")
	}
}
