package lab

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/injoyai/strategy-tail/internal/portfolioresearch"
)

// portfolio_experiment_store_test.go v2 Task 7 存储层测试：崩溃中断、部分
// 文件、hash 不一致、重复完成、取消竞态、非法下载名、分页边界、大列表、
// 保留元数据、幂等与完成门槛。

// experimentFixture 记录根 + 产物根分离的测试夹具。
type experimentFixture struct {
	store         *PortfolioExperimentStore
	recordsRoot   string
	artifactsRoot string
}

func newExperimentFixture(t *testing.T) *experimentFixture {
	t.Helper()
	recordsRoot := t.TempDir()
	artifactsRoot := t.TempDir()
	return &experimentFixture{
		store:         NewPortfolioExperimentStore(recordsRoot, artifactsRoot),
		recordsRoot:   recordsRoot,
		artifactsRoot: artifactsRoot,
	}
}

// uniqueRequestID 生成合法 UUID 形式幂等键。
func uniqueRequestID(i int) string {
	return fmt.Sprintf("%08d-0000-4000-8000-%012d", i, i)
}

// createSeq 自动唯一幂等键计数器。
var createSeq int32

// createOne 创建一条实验并断言成功；未显式指定 RequestID 时自动唯一化，
// 避免固定 RequestID 触发幂等命中。
func (f *experimentFixture) createOne(t *testing.T, muts ...func(*CreatePortfolioExperimentRequest)) PortfolioExperiment {
	t.Helper()
	req := validExperimentRequest()
	for _, m := range muts {
		m(&req)
	}
	if req.RequestID == "" {
		req.RequestID = uniqueRequestID(int(atomic.AddInt32(&createSeq, 1)) + 500000)
	}
	rec, created, err := f.store.Create(req)
	if err != nil {
		t.Fatalf("创建实验失败: %v", err)
	}
	if !created {
		t.Fatalf("期望首次创建 created=true")
	}
	return rec
}

// writeArtifacts 写入五类产物文件（report/nav/orders/trades/holdings）。
func (f *experimentFixture) writeArtifacts(t *testing.T, id string) {
	t.Helper()
	contents := map[string]string{
		"report.json":  `{"report":"ok"}`,
		"nav.csv":      "date,nav\n2024-01-01,1.0\n",
		"orders.csv":   "date,symbol,side,qty\n",
		"trades.csv":   "date,symbol,qty,px\n",
		"holdings.csv": "date,symbol,weight\n",
	}
	dir := filepath.Join(f.artifactsRoot, id)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("创建产物目录失败: %v", err)
	}
	for name, content := range contents {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0644); err != nil {
			t.Fatalf("写产物 %s 失败: %v", name, err)
		}
	}
}

// startAndRun 创建 → Start → 返回 running 记录。
func (f *experimentFixture) startAndRun(t *testing.T) PortfolioExperiment {
	t.Helper()
	rec := f.createOne(t)
	if err := f.store.Start(rec.ExperimentID); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	return rec
}

// markCompleted 完成一条实验：创建 → Start → 写产物 → MarkCompleted。
func (f *experimentFixture) markCompleted(t *testing.T) PortfolioExperiment {
	t.Helper()
	rec := f.startAndRun(t)
	f.writeArtifacts(t, rec.ExperimentID)
	if err := f.store.MarkCompleted(rec.ExperimentID); err != nil {
		t.Fatalf("MarkCompleted 失败: %v", err)
	}
	return rec
}

func TestExperimentStore_Create_AssignsQueued(t *testing.T) {
	f := newExperimentFixture(t)
	rec := f.createOne(t)
	if rec.Status != portfolioresearch.RunStateQueued {
		t.Fatalf("创建后状态应为 queued，实际 %q", rec.Status)
	}
	if !validExperimentID(rec.ExperimentID) {
		t.Fatalf("服务端应分配合法实验 ID: %q", rec.ExperimentID)
	}
	if rec.CreatedAt == "" || rec.UpdatedAt == "" {
		t.Fatalf("createdAt/updatedAt 应非空")
	}
	if rec.RequestHash == "" {
		t.Fatalf("requestHash 应非空")
	}
	if rec.FamilyID != "momentum-combo" || rec.ModelHash == "" {
		t.Fatalf("创建时应登记 family/model 引用")
	}
	// 再次 Get 与创建返回值一致。
	got, err := f.store.Get(rec.ExperimentID)
	if err != nil {
		t.Fatalf("Get 失败: %v", err)
	}
	if got.ExperimentID != rec.ExperimentID || got.Status != portfolioresearch.RunStateQueued {
		t.Fatalf("Get 与创建结果不一致: %+v", got)
	}
}

func TestExperimentStore_Create_Idempotent(t *testing.T) {
	f := newExperimentFixture(t)
	req := validExperimentRequest()
	req.RequestID = "11111111-1111-4111-8111-111111111111" // 固定幂等键
	first, created, err := f.store.Create(req)
	if err != nil || !created {
		t.Fatalf("首次创建失败: %v created=%v", err, created)
	}
	// 同 RequestID 同内容重试 → 返回已有记录，created=false。
	again, created, err := f.store.Create(req)
	if err != nil {
		t.Fatalf("幂等重试失败: %v", err)
	}
	if created {
		t.Fatalf("同 RequestID 重试不应新建")
	}
	if again.ExperimentID != first.ExperimentID {
		t.Fatalf("幂等重试应返回同一记录: %s ≠ %s", again.ExperimentID, first.ExperimentID)
	}
	// 同 RequestID 不同内容 → 冲突。
	conflict := validExperimentRequest()
	conflict.RequestID = "11111111-1111-4111-8111-111111111111"
	conflict.Variant.Name = "top100-equal"
	if _, _, err := f.store.Create(conflict); !errors.Is(err, errIdempotencyConflict) {
		t.Fatalf("同 RequestID 不同内容应冲突，实际 %v", err)
	}
}

func TestExperimentStore_Get_NotFoundAndInvalid(t *testing.T) {
	f := newExperimentFixture(t)
	if _, err := f.store.Get("pe_20260918T150405123Z_99aabbcc"); !errors.Is(err, errExperimentNotFound) {
		t.Fatalf("不存在应返回 errExperimentNotFound，实际 %v", err)
	}
	if _, err := f.store.Get("../evil"); err == nil {
		t.Fatalf("非法 ID 应被拒绝")
	}
}

func TestExperimentStore_Get_DamagedJSON(t *testing.T) {
	f := newExperimentFixture(t)
	rec := f.createOne(t)
	path := filepath.Join(f.recordsRoot, rec.ExperimentID+".json")
	if err := os.WriteFile(path, []byte("{broken json"), 0644); err != nil {
		t.Fatalf("写损坏记录失败: %v", err)
	}
	if _, err := f.store.Get(rec.ExperimentID); err == nil {
		t.Fatal("损坏 JSON 应 fail closed")
	}
}

func TestExperimentStore_CrashTmpLeftover(t *testing.T) {
	f := newExperimentFixture(t)
	rec := f.createOne(t)
	// 模拟崩溃中断：tmp 文件残留（半写内容）不污染正式记录。
	tmpPath := filepath.Join(f.recordsRoot, ".tmp-"+rec.ExperimentID+".json")
	if err := os.WriteFile(tmpPath, []byte("partial"), 0644); err != nil {
		t.Fatalf("写 tmp 残留失败: %v", err)
	}
	got, err := f.store.Get(rec.ExperimentID)
	if err != nil {
		t.Fatalf("tmp 残留不应影响 Get: %v", err)
	}
	if got.ExperimentID != rec.ExperimentID || got.Status != portfolioresearch.RunStateQueued {
		t.Fatalf("Get 结果异常: %+v", got)
	}
	// 仅有 tmp 而无正式记录 → 视为不存在。
	if _, err := f.store.Get("pe_20260918T150405123Z_99aabbcc"); !errors.Is(err, errExperimentNotFound) {
		t.Fatalf("仅 tmp 残留应视为不存在，实际 %v", err)
	}
	// List 同样忽略 tmp。
	if _, err := f.store.List(ExperimentFilter{}, 1, 20); err != nil {
		t.Fatalf("tmp 残留不应影响 List: %v", err)
	}
}

func TestExperimentStore_StartAndTransition(t *testing.T) {
	f := newExperimentFixture(t)
	rec := f.createOne(t)
	if err := f.store.Start(rec.ExperimentID); err != nil {
		t.Fatalf("queued→running 失败: %v", err)
	}
	got, err := f.store.Get(rec.ExperimentID)
	if err != nil {
		t.Fatalf("Get 失败: %v", err)
	}
	if got.Status != portfolioresearch.RunStateRunning {
		t.Fatalf("Start 后应为 running，实际 %q", got.Status)
	}
	// 再 Start（running→running）拒绝。
	if err := f.store.Start(rec.ExperimentID); err == nil {
		t.Fatal("running 再 Start 应拒绝")
	}
	// queued 直接进终态拒绝。
	queued := f.createOne(t)
	if err := f.store.MarkFailed(queued.ExperimentID, "x"); err == nil {
		t.Fatal("queued 直接 MarkFailed 应拒绝")
	}
}

func TestExperimentStore_UpdateProgress(t *testing.T) {
	f := newExperimentFixture(t)
	rec := f.startAndRun(t)
	if err := f.store.UpdateProgress(rec.ExperimentID, 42, "回测中"); err != nil {
		t.Fatalf("更新进度失败: %v", err)
	}
	got, err := f.store.Get(rec.ExperimentID)
	if err != nil {
		t.Fatalf("Get 失败: %v", err)
	}
	if got.Progress != 42 || got.Message != "回测中" {
		t.Fatalf("进度未保存: %+v", got)
	}
	// 越界进度拒绝。
	if err := f.store.UpdateProgress(rec.ExperimentID, -1, ""); err == nil {
		t.Fatal("负进度应拒绝")
	}
	if err := f.store.UpdateProgress(rec.ExperimentID, 101, ""); err == nil {
		t.Fatal("超 100 进度应拒绝")
	}
	// 终态后更新进度拒绝。
	if err := f.store.MarkFailed(rec.ExperimentID, "数据缺失"); err != nil {
		t.Fatalf("MarkFailed 失败: %v", err)
	}
	if err := f.store.UpdateProgress(rec.ExperimentID, 60, ""); err == nil {
		t.Fatal("终态后更新进度应拒绝")
	}
}

func TestExperimentStore_MarkCompleted_PublishesManifest(t *testing.T) {
	f := newExperimentFixture(t)
	rec := f.startAndRun(t)
	f.writeArtifacts(t, rec.ExperimentID)
	if err := f.store.MarkCompleted(rec.ExperimentID); err != nil {
		t.Fatalf("MarkCompleted 失败: %v", err)
	}
	got, err := f.store.Get(rec.ExperimentID)
	if err != nil {
		t.Fatalf("Get 失败: %v", err)
	}
	if got.Status != portfolioresearch.RunStateCompleted {
		t.Fatalf("完成门槛：状态应为 completed，实际 %q", got.Status)
	}
	if got.Manifest == nil || len(got.Manifest.Entries) != len(portfolioArtifactNames) {
		t.Fatalf("completed 必须伴随完整 manifest")
	}
	if got.ReportPath != "report.json" {
		t.Fatalf("reportPath 应为相对产物名 report.json，实际 %q", got.ReportPath)
	}
	if got.ReportHash == "" || got.ReportHash != got.Manifest.hashOf("report.json") {
		t.Fatalf("reportHash 与清单不一致")
	}
	if got.Progress != 100 {
		t.Fatalf("完成时进度应为 100，实际 %d", got.Progress)
	}
	// 清单中的 hash 与磁盘实际内容一致。
	for _, e := range got.Manifest.Entries {
		data, err := os.ReadFile(filepath.Join(f.artifactsRoot, rec.ExperimentID, e.Name))
		if err != nil {
			t.Fatalf("读产物 %s 失败: %v", e.Name, err)
		}
		if sha256HexBytes(data) != e.SHA256 {
			t.Fatalf("产物 %s hash 与清单不一致", e.Name)
		}
	}
}

// TestExperimentStore_MarkCompleted_PartialArtifacts 部分文件：五类缺一 →
// 发布失败，主记录不得呈现 completed（完成门槛）。
func TestExperimentStore_MarkCompleted_PartialArtifacts(t *testing.T) {
	f := newExperimentFixture(t)
	rec := f.startAndRun(t)
	f.writeArtifacts(t, rec.ExperimentID)
	// 删除 orders.csv。
	if err := os.Remove(filepath.Join(f.artifactsRoot, rec.ExperimentID, "orders.csv")); err != nil {
		t.Fatalf("删除产物失败: %v", err)
	}
	if err := f.store.MarkCompleted(rec.ExperimentID); err == nil {
		t.Fatal("五类产物缺一应发布失败")
	}
	got, err := f.store.Get(rec.ExperimentID)
	if err != nil {
		t.Fatalf("Get 失败: %v", err)
	}
	if got.Status == portfolioresearch.RunStateCompleted {
		t.Fatal("完成门槛：产物不完整时主记录不得 completed")
	}
	if got.Status != portfolioresearch.RunStateRunning {
		t.Fatalf("发布失败后应保持 running，实际 %q", got.Status)
	}
	if got.Manifest != nil {
		t.Fatalf("发布失败不得写入清单")
	}
}

// TestExperimentStore_MarkCompleted_NoArtifacts 无任何产物 → 发布失败。
func TestExperimentStore_MarkCompleted_NoArtifacts(t *testing.T) {
	f := newExperimentFixture(t)
	rec := f.startAndRun(t)
	if err := f.store.MarkCompleted(rec.ExperimentID); err == nil {
		t.Fatal("无产物应发布失败")
	}
	got, err := f.store.Get(rec.ExperimentID)
	if err != nil {
		t.Fatalf("Get 失败: %v", err)
	}
	if got.Status != portfolioresearch.RunStateRunning {
		t.Fatalf("发布失败后应保持 running，实际 %q", got.Status)
	}
}

// TestExperimentStore_MarkCompleted_Duplicate 重复完成 → 拒绝（终态不可迁移）。
func TestExperimentStore_MarkCompleted_Duplicate(t *testing.T) {
	f := newExperimentFixture(t)
	rec := f.markCompleted(t)
	if err := f.store.MarkCompleted(rec.ExperimentID); !errors.Is(err, errExperimentFinalized) {
		t.Fatalf("重复完成应返回 errExperimentFinalized，实际 %v", err)
	}
}

// TestExperimentStore_MarkCompleted_WrongState queued 直接完成 → 拒绝。
func TestExperimentStore_MarkCompleted_WrongState(t *testing.T) {
	f := newExperimentFixture(t)
	rec := f.createOne(t)
	f.writeArtifacts(t, rec.ExperimentID)
	if err := f.store.MarkCompleted(rec.ExperimentID); err == nil {
		t.Fatal("queued 直接完成应拒绝")
	}
}

// TestExperimentStore_HashMismatchFailClosed 产物被改 → 读取 fail closed。
func TestExperimentStore_HashMismatchFailClosed(t *testing.T) {
	f := newExperimentFixture(t)
	rec := f.markCompleted(t)
	// 篡改已发布产物。
	if err := os.WriteFile(filepath.Join(f.artifactsRoot, rec.ExperimentID, "nav.csv"), []byte("tampered"), 0644); err != nil {
		t.Fatalf("篡改产物失败: %v", err)
	}
	if _, err := f.store.Get(rec.ExperimentID); err == nil {
		t.Fatal("产物 hash 不一致应 fail closed")
	}
	// 删除已发布产物同样 fail closed。
	if err := os.Remove(filepath.Join(f.artifactsRoot, rec.ExperimentID, "report.json")); err != nil {
		t.Fatalf("删除产物失败: %v", err)
	}
	if _, err := f.store.Get(rec.ExperimentID); err == nil {
		t.Fatal("已发布产物缺失应 fail closed")
	}
}

// TestExperimentStore_MarkTerminal_KeepsMetadata 失败/取消/无有效样本保留元数据。
func TestExperimentStore_MarkTerminal_KeepsMetadata(t *testing.T) {
	f := newExperimentFixture(t)
	cases := []struct {
		name    string
		mark    func(id string) error
		state   string
		message string
	}{
		{"failed", func(id string) error { return f.store.MarkFailed(id, "行情数据缺失") }, portfolioresearch.RunStateFailed, "行情数据缺失"},
		{"cancelled", func(id string) error { return f.store.MarkCancelled(id, "用户取消") }, portfolioresearch.RunStateCancelled, "用户取消"},
		{"insufficient", func(id string) error { return f.store.MarkInsufficient(id, "有效股票不足") }, portfolioresearch.RunStateInsufficient, "有效股票不足"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := f.startAndRun(t)
			if err := c.mark(rec.ExperimentID); err != nil {
				t.Fatalf("标记终态失败: %v", err)
			}
			got, err := f.store.Get(rec.ExperimentID)
			if err != nil {
				t.Fatalf("Get 失败: %v", err)
			}
			if got.Status != c.state {
				t.Fatalf("状态应为 %s，实际 %q", c.state, got.Status)
			}
			if got.Error != c.message {
				t.Fatalf("错误信息应保留 %q，实际 %q", c.message, got.Error)
			}
			// 元数据必须保留。
			if got.ModelID != rec.ModelID || got.ModelRevision != rec.ModelRevision || got.ModelHash == "" {
				t.Fatalf("失败/取消/无样本不得丢失模型引用")
			}
			if got.StudyRange.Start != rec.StudyRange.Start || got.FamilyID != rec.FamilyID {
				t.Fatalf("失败/取消/无样本不得丢失区间/家族")
			}
			if got.Variant.Name != rec.Variant.Name || got.DataSnapshot.PriceSource == "" {
				t.Fatalf("失败/取消/无样本不得丢失变体/快照")
			}
			if got.TrialCounted != rec.TrialCounted || got.CountReason == "" {
				t.Fatalf("失败/取消/无样本不得丢失试验计数")
			}
			// 终态不允许再迁移。
			if err := c.mark(rec.ExperimentID); err == nil {
				t.Fatal("终态重复标记应拒绝")
			}
		})
	}
}

// TestExperimentStore_TerminalNoArtifactsRequired 失败/取消/无样本不需要产物。
func TestExperimentStore_TerminalNoArtifactsRequired(t *testing.T) {
	f := newExperimentFixture(t)
	rec := f.startAndRun(t)
	if err := f.store.MarkCancelled(rec.ExperimentID, "取消"); err != nil {
		t.Fatalf("MarkCancelled 失败: %v", err)
	}
	got, err := f.store.Get(rec.ExperimentID)
	if err != nil {
		t.Fatalf("Get 失败: %v", err)
	}
	if got.Status != portfolioresearch.RunStateCancelled || got.Manifest != nil {
		t.Fatalf("取消不得发布正式报告/清单: %+v", got)
	}
}

// TestExperimentStore_CancelRace 取消竞态：running 中取消与完成并发，恰好一次生效。
func TestExperimentStore_CancelRace(t *testing.T) {
	for i := 0; i < 20; i++ {
		f := newExperimentFixture(t)
		rec := f.startAndRun(t)
		f.writeArtifacts(t, rec.ExperimentID)

		var wg sync.WaitGroup
		var errCancel, errComplete error
		wg.Add(2)
		go func() {
			defer wg.Done()
			errCancel = f.store.MarkCancelled(rec.ExperimentID, "并发取消")
		}()
		go func() {
			defer wg.Done()
			errComplete = f.store.MarkCompleted(rec.ExperimentID)
		}()
		wg.Wait()

		// 恰好一个成功。
		if (errCancel == nil) == (errComplete == nil) {
			t.Fatalf("并发取消/完成必须恰好一个生效: cancel=%v complete=%v", errCancel, errComplete)
		}
		got, err := f.store.Get(rec.ExperimentID)
		if err != nil {
			t.Fatalf("Get 失败: %v", err)
		}
		// 状态与成功者一致。
		if errCancel == nil && got.Status != portfolioresearch.RunStateCancelled {
			t.Fatalf("取消成功但状态异常: %q", got.Status)
		}
		if errComplete == nil && got.Status != portfolioresearch.RunStateCompleted {
			t.Fatalf("完成成功但状态异常: %q", got.Status)
		}
	}
}

func TestExperimentStore_ArtifactPaths(t *testing.T) {
	f := newExperimentFixture(t)
	// 未完成 → 空列表。
	rec := f.startAndRun(t)
	paths, err := f.store.ArtifactPaths(rec.ExperimentID)
	if err != nil {
		t.Fatalf("ArtifactPaths 失败: %v", err)
	}
	if len(paths) != 0 {
		t.Fatalf("未完成实验不应有产物: %v", paths)
	}
	// 完成 → 五类白名单产物名（按白名单稳定顺序），不含绝对路径。
	rec = f.markCompleted(t)
	paths, err = f.store.ArtifactPaths(rec.ExperimentID)
	if err != nil {
		t.Fatalf("ArtifactPaths 失败: %v", err)
	}
	want := []string{"report.json", "nav.csv", "orders.csv", "trades.csv", "holdings.csv"}
	if len(paths) != len(want) {
		t.Fatalf("产物名数量错误: %v", paths)
	}
	for i := range want {
		if paths[i] != want[i] {
			t.Fatalf("产物名顺序错误: %v ≠ %v", paths, want)
		}
	}
	for _, p := range paths {
		if filepath.IsAbs(p) || p == ".." || p == "." {
			t.Fatalf("产物名不得是绝对路径/穿越: %q", p)
		}
		if err := ValidArtifactName(p); err != nil {
			t.Fatalf("产物名应通过白名单校验: %v", err)
		}
	}
}

// TestExperimentStore_IllegalDownloadName 非法下载名拒绝（配合 API 层下载）。
func TestExperimentStore_IllegalDownloadName(t *testing.T) {
	f := newExperimentFixture(t)
	f.markCompleted(t) // 副作用：存在一条 completed 记录
	// ArtifactPaths 结果必须全部合法（已覆盖）；显式校验非法名集合。
	for _, name := range []string{"../report.json", "/etc/passwd", `C:\evil.csv`, "sub/nav.csv", "manifest.json", "report.txt"} {
		if err := ValidArtifactName(name); err == nil {
			t.Errorf("非法下载名 %q 应拒绝", name)
		}
	}
	// 防篡改：把非白名单文件名手工写进 completed 记录 manifest → Get fail closed。
	rec2 := f.markCompleted(t)
	path := filepath.Join(f.recordsRoot, rec2.ExperimentID+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读记录失败: %v", err)
	}
	tampered := strings.Replace(string(data), `"nav.csv"`, `"../evil.csv"`, 1)
	if err := os.WriteFile(path, []byte(tampered), 0644); err != nil {
		t.Fatalf("写记录失败: %v", err)
	}
	if _, err := f.store.Get(rec2.ExperimentID); err == nil {
		t.Fatal("manifest 含非法产物名应 fail closed")
	}
}

func TestExperimentStore_List_PaginationAndSort(t *testing.T) {
	f := newExperimentFixture(t)
	const n = 15
	for i := 0; i < n; i++ {
		f.createOne(t, func(r *CreatePortfolioExperimentRequest) {
			r.RequestID = uniqueRequestID(i + 1)
		})
	}
	// 第一页 5 条：createdAt 倒序（最新在前），ID 升序兜底。
	page1, err := f.store.List(ExperimentFilter{}, 1, 5)
	if err != nil {
		t.Fatalf("List 失败: %v", err)
	}
	if page1.Total != n || len(page1.Items) != 5 {
		t.Fatalf("第一页异常: total=%d items=%d", page1.Total, len(page1.Items))
	}
	if page1.Page != 1 || page1.PageSize != 5 {
		t.Fatalf("分页元数据错误: %+v", page1)
	}
	// 稳定性：两页之间无重叠、合起来等于全集。
	page2, err := f.store.List(ExperimentFilter{}, 2, 5)
	if err != nil {
		t.Fatalf("List 失败: %v", err)
	}
	page3, err := f.store.List(ExperimentFilter{}, 3, 5)
	if err != nil {
		t.Fatalf("List 失败: %v", err)
	}
	seen := map[string]bool{}
	for _, p := range [][]PortfolioExperiment{page1.Items, page2.Items, page3.Items} {
		for _, it := range p {
			if seen[it.ExperimentID] {
				t.Fatalf("分页出现重复记录: %s", it.ExperimentID)
			}
			seen[it.ExperimentID] = true
		}
	}
	if len(seen) != n {
		t.Fatalf("分页未覆盖全集: %d ≠ %d", len(seen), n)
	}
	// 稳定排序：createdAt 倒序 + ID 兜底。
	for i := 1; i < len(page1.Items); i++ {
		if page1.Items[i-1].CreatedAt < page1.Items[i].CreatedAt {
			t.Fatalf("createdAt 应倒序")
		}
		if page1.Items[i-1].CreatedAt == page1.Items[i].CreatedAt &&
			page1.Items[i-1].ExperimentID > page1.Items[i].ExperimentID {
			t.Fatalf("同时刻应 ID 升序兜底")
		}
	}
}

// TestExperimentStore_List_PageBounds 分页边界：越界空页、上限拒绝、非法输入拒绝。
func TestExperimentStore_List_PageBounds(t *testing.T) {
	f := newExperimentFixture(t)
	for i := 0; i < 3; i++ {
		f.createOne(t, func(r *CreatePortfolioExperimentRequest) {
			r.RequestID = uniqueRequestID(i + 100)
		})
	}
	// page 越界 → 空 items + total 保留。
	page, err := f.store.List(ExperimentFilter{}, 99, 20)
	if err != nil {
		t.Fatalf("越界页不应报错: %v", err)
	}
	if page.Total != 3 || len(page.Items) != 0 {
		t.Fatalf("越界页应返回空 items + total: %+v", page)
	}
	// page < 1 拒绝。
	if _, err := f.store.List(ExperimentFilter{}, 0, 20); err == nil {
		t.Fatal("page=0 应拒绝")
	}
	// pageSize < 1 拒绝。
	if _, err := f.store.List(ExperimentFilter{}, 1, 0); err == nil {
		t.Fatal("pageSize=0 应拒绝")
	}
	// pageSize 超上限拒绝。
	if _, err := f.store.List(ExperimentFilter{}, 1, MaxExperimentPageSize+1); err == nil {
		t.Fatalf("pageSize 超上限应拒绝")
	}
	// 空目录 → 空页。
	empty := newExperimentFixture(t)
	page, err = empty.store.List(ExperimentFilter{}, 1, 20)
	if err != nil {
		t.Fatalf("空列表失败: %v", err)
	}
	if page.Total != 0 || len(page.Items) != 0 {
		t.Fatalf("空列表应返回空页: %+v", page)
	}
}

// TestExperimentStore_List_Filter 过滤：familyId/modelId/status。
func TestExperimentStore_List_Filter(t *testing.T) {
	f := newExperimentFixture(t)
	recA := f.createOne(t, func(r *CreatePortfolioExperimentRequest) {
		r.RequestID = uniqueRequestID(200)
		r.FamilyID = "alpha-family"
		r.ModelID = "fm_20260917T150100000Z_99aabbcc"
	})
	_ = recA
	f.createOne(t, func(r *CreatePortfolioExperimentRequest) {
		r.RequestID = uniqueRequestID(201)
		r.FamilyID = "beta-family"
		r.ModelID = "fm_20260917T150200000Z_88aaccee"
	})
	// familyId 过滤。
	page, err := f.store.List(ExperimentFilter{FamilyID: "alpha-family"}, 1, 20)
	if err != nil {
		t.Fatalf("List 失败: %v", err)
	}
	if page.Total != 1 || len(page.Items) != 1 || page.Items[0].FamilyID != "alpha-family" {
		t.Fatalf("familyId 过滤错误: %+v", page)
	}
	// status 过滤。
	page, err = f.store.List(ExperimentFilter{Status: portfolioresearch.RunStateQueued}, 1, 20)
	if err != nil {
		t.Fatalf("List 失败: %v", err)
	}
	if page.Total != 2 {
		t.Fatalf("queued 过滤应返回 2 条: %d", page.Total)
	}
	// modelId 过滤。
	page, err = f.store.List(ExperimentFilter{ModelID: "fm_20260917T150100000Z_99aabbcc"}, 1, 20)
	if err != nil {
		t.Fatalf("List 失败: %v", err)
	}
	if page.Total != 1 {
		t.Fatalf("modelId 过滤错误: %d", page.Total)
	}
	// 非法过滤值拒绝。
	if _, err := f.store.List(ExperimentFilter{Status: "hacked"}, 1, 20); err == nil {
		t.Fatal("非法 status 过滤应拒绝")
	}
	if _, err := f.store.List(ExperimentFilter{FamilyID: "../x"}, 1, 20); err == nil {
		t.Fatal("非法 familyId 过滤应拒绝")
	}
}

// TestExperimentStore_List_Large 大列表：千级记录分页正确、稳定排序。
func TestExperimentStore_List_Large(t *testing.T) {
	f := newExperimentFixture(t)
	const n = 1000
	for i := 0; i < n; i++ {
		f.createOne(t, func(r *CreatePortfolioExperimentRequest) {
			r.RequestID = uniqueRequestID(1000 + i)
		})
	}
	const pageSize = 100
	total := 0
	var prev *PortfolioExperiment
	for page := 1; ; page++ {
		p, err := f.store.List(ExperimentFilter{}, page, pageSize)
		if err != nil {
			t.Fatalf("List 第 %d 页失败: %v", page, err)
		}
		if page == 1 {
			if p.Total != n {
				t.Fatalf("total 错误: %d ≠ %d", p.Total, n)
			}
		}
		if len(p.Items) == 0 {
			break
		}
		total += len(p.Items)
		// 跨页稳定排序：createdAt 倒序 + ID 兜底。
		for _, it := range p.Items {
			if prev != nil {
				if prev.CreatedAt < it.CreatedAt {
					t.Fatalf("跨页 createdAt 非倒序: %s < %s", prev.CreatedAt, it.CreatedAt)
				}
				if prev.CreatedAt == it.CreatedAt && prev.ExperimentID >= it.ExperimentID {
					t.Fatalf("跨页同刻 ID 非严格升序: %s ≥ %s", prev.ExperimentID, it.ExperimentID)
				}
			}
			prev = &it
		}
		if len(p.Items) < pageSize {
			break // 最后一页
		}
	}
	if total != n {
		t.Fatalf("大列表分页未覆盖全集: %d ≠ %d", total, n)
	}
}

// TestExperimentStore_CompletionGate 完成门槛：completed 主记录必然伴随完整
// manifest 且产物 hash 一致；手工伪造不完整 completed 记录读取 fail closed。
func TestExperimentStore_CompletionGate(t *testing.T) {
	f := newExperimentFixture(t)
	// 正常发布路径（前面测试已覆盖）在此再验证一次读回。
	rec := f.markCompleted(t)
	got, err := f.store.Get(rec.ExperimentID)
	if err != nil {
		t.Fatalf("Get 失败: %v", err)
	}
	if got.Status != portfolioresearch.RunStateCompleted || got.Manifest == nil {
		t.Fatalf("完成门槛被破坏")
	}
	// 手工伪造：completed 但无 manifest → Get fail closed。
	forged := f.createOne(t, func(r *CreatePortfolioExperimentRequest) {
		r.RequestID = uniqueRequestID(3000)
	})
	if err := f.store.Start(forged.ExperimentID); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	path := filepath.Join(f.recordsRoot, forged.ExperimentID+".json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读记录失败: %v", err)
	}
	// 直接替换状态为 completed（无 manifest），模拟外部篡改/损坏。
	rawStr := string(raw)
	rawStr = replaceJSONValue(rawStr, "status", portfolioresearch.RunStateCompleted)
	if err := os.WriteFile(path, []byte(rawStr), 0644); err != nil {
		t.Fatalf("伪造记录失败: %v", err)
	}
	if _, err := f.store.Get(forged.ExperimentID); err == nil {
		t.Fatal("completed 而无完整 manifest 应 fail closed")
	}
	// 手工伪造：completed 但 reportHash 与清单不一致 → Get fail closed。
	forged2 := f.markCompleted(t)
	path2 := filepath.Join(f.recordsRoot, forged2.ExperimentID+".json")
	raw2, err := os.ReadFile(path2)
	if err != nil {
		t.Fatalf("读记录失败: %v", err)
	}
	rawStr2 := replaceJSONValue(string(raw2), "reportHash", strings.Repeat("e", 64))
	if err := os.WriteFile(path2, []byte(rawStr2), 0644); err != nil {
		t.Fatalf("伪造记录失败: %v", err)
	}
	if _, err := f.store.Get(forged2.ExperimentID); err == nil {
		t.Fatal("reportHash 与清单不一致应 fail closed")
	}
}

// replaceJSONValue 简单字符串替换测试辅助：把 JSON 中 "key": "oldValue" 的
// 值替换为 newValue（处理 MarshalIndent 冒号后空格；仅用于测试伪造记录）。
func replaceJSONValue(raw, key, newValue string) string {
	needle := `"` + key + `":`
	i := strings.Index(raw, needle)
	if i < 0 {
		return raw
	}
	rest := raw[i+len(needle):]
	rest = strings.TrimPrefix(rest, " ")
	if !strings.HasPrefix(rest, `"`) {
		return raw
	}
	end := strings.Index(rest[1:], `"`)
	if end < 0 {
		return raw
	}
	end += 1
	return raw[:i] + needle + ` "` + newValue + `"` + rest[end+1:]
}

var _ = strings.Repeat // 防止误删 import 时编译错误（replaceJSONValue 使用）
