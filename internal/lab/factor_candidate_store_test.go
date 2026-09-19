package lab

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// newTestCandidateStore 创建指向临时目录的候选存储：时钟逐次递增、随机
// 字节确定，保证多次创建得到不同候选 ID，不写真实 data/。
func newTestCandidateStore(t *testing.T) *CandidateStore {
	t.Helper()
	dir := t.TempDir()
	now := time.Date(2026, 9, 17, 15, 0, 0, 0, time.Local)
	clock := func() time.Time { now = now.Add(time.Minute); return now }
	rd := strings.NewReader("\x01\x02\x03\x04\x05\x06\x07\x08\x09\x0a\x0b\x0c\x0d\x0e\x0f\x10")
	return &CandidateStore{root: dir, now: clock, rand: rd}
}

func observeReq(uuid, name string) CreateCandidateRequest {
	return candidateReq(uuid, name, CandidateUse{Mode: "observe"})
}

// TestCandidateStoreCreateIdempotent 相同 requestId + 相同内容幂等：
// 第二次返回已有记录（created=false，同一 ID），不产生副本。
func TestCandidateStoreCreateIdempotent(t *testing.T) {
	store := newTestCandidateStore(t)
	rep := analysisReportForTest(testAnalysisID, "momentum", "2026-09-17T15:00:00+08:00")
	req := observeReq(testUUID1, "观察候选")
	c1, created, err := store.Create(req, rep)
	if err != nil || !created {
		t.Fatalf("首次创建 = %v/%v", created, err)
	}
	c2, created2, err := store.Create(req, rep)
	if err != nil {
		t.Fatal(err)
	}
	if created2 {
		t.Fatal("幂等重试不应新建")
	}
	if c2.ID != c1.ID || c2.Revision != c1.Revision {
		t.Fatalf("幂等返回应一致: %+v vs %+v", c1, c2)
	}
}

// TestCandidateStoreRejectsIdempotencyKeyReuse 同一 requestId 携带不同内容 → 409。
func TestCandidateStoreRejectsIdempotencyKeyReuse(t *testing.T) {
	store := newTestCandidateStore(t)
	rep := analysisReportForTest(testAnalysisID, "momentum", "2026-09-17T15:00:00+08:00")
	if _, _, err := store.Create(observeReq(testUUID1, "候选A"), rep); err != nil {
		t.Fatal(err)
	}
	_, _, err := store.Create(observeReq(testUUID1, "候选B"), rep)
	if err != errIdempotencyConflict {
		t.Fatalf("幂等键复用应冲突: %v", err)
	}
}

// TestCandidateStoreCreatesDistinctUses 不同 request ID 可从同一分析创建多条候选。
func TestCandidateStoreCreatesDistinctUses(t *testing.T) {
	store := newTestCandidateStore(t)
	rep := analysisReportForTest(testAnalysisID, "momentum", "2026-09-17T15:00:00+08:00")
	c1, _, err := store.Create(observeReq(testUUID1, "观察"), rep)
	if err != nil {
		t.Fatal(err)
	}
	c2, _, err := store.Create(candidateReq(testUUID2, "区间",
		CandidateUse{Mode: "range", Filter: rangeFilter()}), rep)
	if err != nil {
		t.Fatal(err)
	}
	if c1.ID == c2.ID {
		t.Fatalf("两条候选 ID 不应相同")
	}
	list, err := store.List(false)
	if err != nil || len(list) != 2 {
		t.Fatalf("List = %v, %v", list, err)
	}
}

// TestCandidateStoreAppendsRevision 更新追加修订：revision+1，旧文件保留。
func TestCandidateStoreAppendsRevision(t *testing.T) {
	store := newTestCandidateStore(t)
	rep := analysisReportForTest(testAnalysisID, "momentum", "2026-09-17T15:00:00+08:00")
	c, _, err := store.Create(observeReq(testUUID1, "原名"), rep)
	if err != nil {
		t.Fatal(err)
	}
	upd := UpdateCandidateRequest{ExpectedRevision: 1, Name: "新名称",
		Use: CandidateUse{Mode: "observe"}, Status: CandidateStatusCandidate}
	c2, err := store.Update(c.ID, upd)
	if err != nil {
		t.Fatal(err)
	}
	if c2.Revision != 2 || c2.Name != "新名称" || c2.ID != c.ID {
		t.Fatalf("更新结果 = %+v", c2)
	}
	if c2.Factor.Kind != c.Factor.Kind || c2.Evidence.AnalysisID != c.Evidence.AnalysisID {
		t.Fatalf("FactorRef/Evidence 不得变化: %+v", c2)
	}
	// revision 1 文件保留
	if _, err := os.Stat(filepath.Join(store.root, c.ID, "revisions", "000001.json")); err != nil {
		t.Fatalf("revision 1 缺失: %v", err)
	}
	if _, err := os.Stat(filepath.Join(store.root, c.ID, "revisions", "000002.analysis.json")); err != nil {
		t.Fatalf("revision 2 证据缺失: %v", err)
	}
	got, err := store.Get(c.ID)
	if err != nil || got.Revision != 2 {
		t.Fatalf("Get 应返回最新修订: %+v, %v", got, err)
	}
}

// TestCandidateStoreRevisionConflict expectedRevision 不匹配 → 409 冲突。
func TestCandidateStoreRevisionConflict(t *testing.T) {
	store := newTestCandidateStore(t)
	rep := analysisReportForTest(testAnalysisID, "momentum", "2026-09-17T15:00:00+08:00")
	c, _, err := store.Create(observeReq(testUUID1, "候选"), rep)
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.Update(c.ID, UpdateCandidateRequest{ExpectedRevision: 99, Name: "x",
		Use: CandidateUse{Mode: "observe"}, Status: CandidateStatusCandidate})
	if err != errRevisionConflict {
		t.Fatalf("应返回修订冲突: %v", err)
	}
}

// TestCandidateStoreArchiveRestore candidate→archived→candidate 合法；
// 归档默认隐藏，恢复后重新出现。
func TestCandidateStoreArchiveRestore(t *testing.T) {
	store := newTestCandidateStore(t)
	rep := analysisReportForTest(testAnalysisID, "momentum", "2026-09-17T15:00:00+08:00")
	c, _, err := store.Create(observeReq(testUUID1, "候选"), rep)
	if err != nil {
		t.Fatal(err)
	}
	// 归档
	c2, err := store.Update(c.ID, UpdateCandidateRequest{ExpectedRevision: 1,
		Name: c.Name, Use: c.Use, Status: CandidateStatusArchived})
	if err != nil {
		t.Fatal(err)
	}
	if c2.Status != CandidateStatusArchived || c2.Revision != 2 {
		t.Fatalf("归档 = %+v", c2)
	}
	if list, _ := store.List(false); len(list) != 0 {
		t.Fatalf("归档默认应隐藏: %+v", list)
	}
	if list, _ := store.List(true); len(list) != 1 || list[0].Status != CandidateStatusArchived {
		t.Fatalf("归档可见列表 = %+v", list)
	}
	// 恢复
	c3, err := store.Update(c.ID, UpdateCandidateRequest{ExpectedRevision: 2,
		Name: c.Name, Use: c.Use, Status: CandidateStatusCandidate})
	if err != nil {
		t.Fatal(err)
	}
	if c3.Status != CandidateStatusCandidate || c3.Revision != 3 {
		t.Fatalf("恢复 = %+v", c3)
	}
	if list, _ := store.List(false); len(list) != 1 {
		t.Fatalf("恢复后应出现在活动列表: %+v", list)
	}
}

// TestCandidateStoreEvidenceHashMismatch 证据被篡改 → 读取 fail closed。
func TestCandidateStoreEvidenceHashMismatch(t *testing.T) {
	store := newTestCandidateStore(t)
	rep := analysisReportForTest(testAnalysisID, "momentum", "2026-09-17T15:00:00+08:00")
	c, _, err := store.Create(observeReq(testUUID1, "候选"), rep)
	if err != nil {
		t.Fatal(err)
	}
	evPath := filepath.Join(store.root, c.ID, "revisions", "000001.analysis.json")
	if err := os.WriteFile(evPath, []byte("tampered"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(c.ID); err == nil {
		t.Fatal("证据哈希不匹配应报错")
	}
	if _, err := store.List(false); err == nil {
		t.Fatal("证据哈希不匹配时 List 应 fail closed")
	}
}

// TestCandidateStoreRejectsTraversalID 非法/穿越候选 ID 拒绝。
func TestCandidateStoreRejectsTraversalID(t *testing.T) {
	store := newTestCandidateStore(t)
	rep := analysisReportForTest(testAnalysisID, "momentum", "2026-09-17T15:00:00+08:00")
	if _, _, err := store.Create(observeReq(testUUID1, "候选"), rep); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"../evil", "..\\evil", "fc_bad", "an_20260917T150000000Z_a1b2c3d4"} {
		if _, err := store.Get(id); err == nil {
			t.Fatalf("非法 ID %q 应被拒绝", id)
		}
		if _, err := store.Update(id, UpdateCandidateRequest{ExpectedRevision: 1, Name: "x",
			Use: CandidateUse{Mode: "observe"}, Status: CandidateStatusCandidate}); err == nil {
			t.Fatalf("非法 ID %q Update 应被拒绝", id)
		}
	}
}

// TestCandidateStoreRestartRecovery 重建 Store（重启）后候选仍可读取与修订。
func TestCandidateStoreRestartRecovery(t *testing.T) {
	store := newTestCandidateStore(t)
	rep := analysisReportForTest(testAnalysisID, "momentum", "2026-09-17T15:00:00+08:00")
	c, _, err := store.Create(observeReq(testUUID1, "重启候选"), rep)
	if err != nil {
		t.Fatal(err)
	}
	// 重建指向同一根目录
	restarted := NewCandidateStore(store.root)
	got, err := restarted.Get(c.ID)
	if err != nil || got.ID != c.ID || got.Revision != 1 || got.Name != "重启候选" {
		t.Fatalf("重启后 Get = %+v, %v", got, err)
	}
	if got.Evidence.ReportSHA256 != c.Evidence.ReportSHA256 {
		t.Fatal("重启后证据哈希应一致")
	}
	if list, err := restarted.List(false); err != nil || len(list) != 1 {
		t.Fatalf("重启后 List = %v, %v", list, err)
	}
	// 重启后仍可追加修订
	c2, err := restarted.Update(c.ID, UpdateCandidateRequest{ExpectedRevision: 1, Name: "重启后改名",
		Use: CandidateUse{Mode: "observe"}, Status: CandidateStatusCandidate})
	if err != nil || c2.Revision != 2 || c2.Name != "重启后改名" {
		t.Fatalf("重启后 Update = %+v, %v", c2, err)
	}
}

// TestCandidateStoreListSort 活动在前，同状态 updatedAt 倒序。
func TestCandidateStoreListSort(t *testing.T) {
	store := newTestCandidateStore(t)
	rep := analysisReportForTest(testAnalysisID, "momentum", "2026-09-17T15:00:00+08:00")
	c1, _, err := store.Create(observeReq(testUUID1, "先"), rep)
	if err != nil {
		t.Fatal(err)
	}
	c2, _, err := store.Create(observeReq(testUUID2, "后"), rep)
	if err != nil {
		t.Fatal(err)
	}
	// 归档 c1（其最新 updatedAt 晚于 c2）
	if _, err := store.Update(c1.ID, UpdateCandidateRequest{ExpectedRevision: 1,
		Name: c1.Name, Use: c1.Use, Status: CandidateStatusArchived}); err != nil {
		t.Fatal(err)
	}
	list, err := store.List(true)
	if err != nil || len(list) != 2 {
		t.Fatalf("List = %v, %v", list, err)
	}
	// 活动候选在前
	if list[0].ID != c2.ID || list[1].ID != c1.ID {
		t.Fatalf("排序错误: %s, %s", list[0].ID, list[1].ID)
	}
}

// TestCandidateStoreGetRevision 按 revision 读取历史修订（冻结验证依赖）：
// 历史修订保持可读、非法输入拒绝、单修订损坏只影响该修订。
func TestCandidateStoreGetRevision(t *testing.T) {
	store := newTestCandidateStore(t)
	rep := analysisReportForTest(testAnalysisID, "momentum", "2026-09-17T15:00:00+08:00")
	c, _, err := store.Create(observeReq(testUUID1, "原名"), rep)
	if err != nil {
		t.Fatal(err)
	}
	// revision 1 直接可读
	got, err := store.GetRevision(c.ID, 1)
	if err != nil || got.Revision != 1 || got.Name != "原名" {
		t.Fatalf("GetRevision(1) = %+v, %v", got, err)
	}
	if got.Evidence.ReportSHA256 != c.Evidence.ReportSHA256 {
		t.Fatal("revision 证据哈希应一致")
	}
	// 追加修订后：最新修订可读，历史修订保持原样
	if _, err := store.Update(c.ID, UpdateCandidateRequest{ExpectedRevision: 1,
		Name: "新名称", Use: CandidateUse{Mode: "observe"}, Status: CandidateStatusCandidate}); err != nil {
		t.Fatal(err)
	}
	latest, err := store.GetRevision(c.ID, 2)
	if err != nil || latest.Revision != 2 || latest.Name != "新名称" {
		t.Fatalf("GetRevision(2) = %+v, %v", latest, err)
	}
	old, err := store.GetRevision(c.ID, 1)
	if err != nil || old.Name != "原名" || old.Revision != 1 {
		t.Fatalf("历史修订应保持原样: %+v, %v", old, err)
	}
	// 不存在的 revision 拒绝
	if _, err := store.GetRevision(c.ID, 3); err == nil {
		t.Fatal("不存在的 revision 应报错")
	}
	// revision < 1 拒绝
	if _, err := store.GetRevision(c.ID, 0); err == nil {
		t.Fatal("revision 0 应拒绝")
	}
	// 非法/穿越 ID 拒绝
	for _, id := range []string{"../evil", "..\\evil", "fc_bad"} {
		if _, err := store.GetRevision(id, 1); err == nil {
			t.Fatalf("非法 ID %q 应被拒绝", id)
		}
	}
	// 单修订证据损坏只 fail closed 该修订，不波及其它修订
	evPath := filepath.Join(store.root, c.ID, "revisions", "000002.analysis.json")
	if err := os.WriteFile(evPath, []byte("tampered"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetRevision(c.ID, 2); err == nil {
		t.Fatal("revision 2 证据损坏应报错")
	}
	if _, err := store.GetRevision(c.ID, 1); err != nil {
		t.Fatalf("revision 1 不应受 revision 2 损坏影响: %v", err)
	}
}
