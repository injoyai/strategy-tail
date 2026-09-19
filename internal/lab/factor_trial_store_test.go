package lab

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// factor_trial_store_test.go 试验账本存储测试（计划 Task 6 Step 2/3）：
// Start 不可变请求、Finish 幂等拒绝、全状态保留、ListFamily 过滤与稳定排序。

func testFactorSnapshot() FactorSnapshot {
	return FactorSnapshot{
		Kind: "momentum", Name: "N日动量(2)", Description: "测试因子",
		ParameterLabel: "2日", Days: 2, Unit: "名次",
		ImplementationVersion: 1,
	}
}

func TestTrialStoreStartWritesRequest(t *testing.T) {
	s := NewTrialStore(filepath.Join(t.TempDir(), "factor-trials"))
	p := validResearchProtocol()
	trial, err := s.Start(p, testFactorSnapshot(), "an_20260102T030405678Z_deadbeef")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !validTrialID(trial.ID) {
		t.Fatalf("trial ID 非法: %q", trial.ID)
	}
	if trial.Status != TrialStatusRunning || trial.FinishedAt != "" {
		t.Fatalf("新登记 trial = %+v", trial)
	}
	if trial.FamilyID != "momentum" || trial.VariantID != "mom-20-v1" {
		t.Fatalf("Family/Variant 未取自协议: %+v", trial)
	}
	// ProtocolHash 与报告同一函数：trial↔报告可互相对账。
	hash, err := protocolHash(p)
	if err != nil || trial.ProtocolHash != hash {
		t.Fatalf("ProtocolHash = %q, want %q (err=%v)", trial.ProtocolHash, hash, err)
	}
	// 请求文件存在且内容为 running 状态。
	data, err := os.ReadFile(filepath.Join(s.root, trial.ID+trialRequestSuffix))
	if err != nil {
		t.Fatalf("请求文件缺失: %v", err)
	}
	var got FactorTrial
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("请求文件损坏: %v", err)
	}
	if got.Status != TrialStatusRunning || got.StartedAt == "" || got.Factor.Kind != "momentum" {
		t.Fatalf("请求文件内容 = %+v", got)
	}
}

func TestTrialStoreStartRejectsInvalid(t *testing.T) {
	s := NewTrialStore(t.TempDir())
	bad := validResearchProtocol()
	bad.Hypothesis.Thesis = ""
	if _, err := s.Start(bad, testFactorSnapshot(), "an_20260102T030405678Z_deadbeef"); err == nil {
		t.Fatalf("非法协议应拒绝")
	}
	p := validResearchProtocol()
	if _, err := s.Start(p, testFactorSnapshot(), "not-an-id"); err == nil {
		t.Fatalf("非法分析 ID 应拒绝")
	}
	entries, err := os.ReadDir(s.root)
	if err == nil && len(entries) > 0 {
		t.Fatalf("拒绝路径不应产生文件: %v", entries)
	}
}

func TestTrialStoreFinishCompletes(t *testing.T) {
	s := NewTrialStore(t.TempDir())
	trial, err := s.Start(validResearchProtocol(), testFactorSnapshot(), "an_20260102T030405678Z_deadbeef")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	// 调用方传入的 FinishedAt 被服务端时间戳覆盖。
	if err := s.Finish(trial.ID, TrialResult{Status: TrialStatusCompleted, FinishedAt: "fake", Message: "ok"}); err != nil {
		t.Fatalf("Finish: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(s.root, trial.ID+trialResultSuffix))
	if err != nil {
		t.Fatalf("结果文件缺失: %v", err)
	}
	var res TrialResult
	if err := json.Unmarshal(data, &res); err != nil {
		t.Fatalf("结果文件损坏: %v", err)
	}
	if res.Status != TrialStatusCompleted || res.Message != "ok" {
		t.Fatalf("结果 = %+v", res)
	}
	if res.FinishedAt == "fake" || res.FinishedAt == "" {
		t.Fatalf("FinishedAt 应为服务端时间戳: %q", res.FinishedAt)
	}
	family, err := s.ListFamily("momentum")
	if err != nil || len(family) != 1 {
		t.Fatalf("ListFamily = %v, %v", family, err)
	}
	if family[0].Status != TrialStatusCompleted || family[0].FinishedAt == "" || family[0].Message != "ok" {
		t.Fatalf("组合读取结果 = %+v", family[0])
	}
}

func TestTrialStoreFinishGuards(t *testing.T) {
	s := NewTrialStore(t.TempDir())
	trial, err := s.Start(validResearchProtocol(), testFactorSnapshot(), "an_20260102T030405678Z_deadbeef")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	// running 不是完成态。
	if err := s.Finish(trial.ID, TrialResult{Status: TrialStatusRunning}); err == nil {
		t.Fatalf("running 状态应拒绝")
	}
	// 未知 ID（合法格式但无请求记录）拒绝。
	ghost, _ := newTrialID(time.Now(), nil)
	if err := s.Finish(ghost, TrialResult{Status: TrialStatusFailed}); err == nil {
		t.Fatalf("无请求记录应拒绝")
	}
	// 非法 ID 拒绝。
	if err := s.Finish("../escape", TrialResult{Status: TrialStatusFailed}); err == nil {
		t.Fatalf("路径穿越应拒绝")
	}
	// 首次完成后再 Finish 拒绝覆盖，结果文件保持首次内容。
	if err := s.Finish(trial.ID, TrialResult{Status: TrialStatusCompleted, Message: "first"}); err != nil {
		t.Fatalf("首次 Finish: %v", err)
	}
	if err := s.Finish(trial.ID, TrialResult{Status: TrialStatusFailed, Message: "second"}); err == nil {
		t.Fatalf("重复 Finish 应拒绝覆盖")
	}
	data, _ := os.ReadFile(filepath.Join(s.root, trial.ID+trialResultSuffix))
	if !strings.Contains(string(data), "first") {
		t.Fatalf("结果文件被覆盖: %s", data)
	}
}

func TestTrialStoreListFamilyFiltersAllStatuses(t *testing.T) {
	s := NewTrialStore(t.TempDir())
	mk := func() FactorTrial {
		t2, err := s.Start(validResearchProtocol(), testFactorSnapshot(), "an_20260102T030405678Z_deadbeef")
		if err != nil {
			t.Fatalf("Start: %v", err)
		}
		return t2
	}
	a1, a2, a3 := mk(), mk(), mk()
	pb := validResearchProtocol()
	pb.Trial.FamilyID = "momentum-2"
	b1, err := s.Start(pb, testFactorSnapshot(), "an_20260102T030405678Z_deadbeef")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	mustFinish := func(id string, r TrialResult) {
		if err := s.Finish(id, r); err != nil {
			t.Fatalf("Finish: %v", err)
		}
	}
	mustFinish(a1.ID, TrialResult{Status: TrialStatusCompleted, Message: "通过"})
	mustFinish(a2.ID, TrialResult{Status: TrialStatusFailed, Message: "数据缺失"})
	// a3 保持 running；b1 canceled（不同家族）。
	mustFinish(b1.ID, TrialResult{Status: TrialStatusCanceled, Message: "用户停止"})

	family, err := s.ListFamily("momentum")
	if err != nil {
		t.Fatalf("ListFamily: %v", err)
	}
	if len(family) != 3 {
		t.Fatalf("family 数 = %d, want 3（含 running）", len(family))
	}
	// 稳定排序：StartedAt 升序，同刻以 ID 兜底——与写入次序无关可复现。
	for i := 1; i < len(family); i++ {
		prev, cur := family[i-1], family[i]
		if prev.StartedAt > cur.StartedAt || (prev.StartedAt == cur.StartedAt && prev.ID >= cur.ID) {
			t.Fatalf("排序不稳定: [%d]=%+v [%d]=%+v", i-1, prev, i, cur)
		}
	}
	statusByID := map[string]TrialStatus{}
	for _, tr := range family {
		statusByID[tr.ID] = tr.Status
	}
	if statusByID[a1.ID] != TrialStatusCompleted || statusByID[a2.ID] != TrialStatusFailed ||
		statusByID[a3.ID] != TrialStatusRunning {
		t.Fatalf("状态映射 = %v", statusByID)
	}
	// 其它家族只含自身（b1 已 canceled）。
	other, err := s.ListFamily("momentum-2")
	if err != nil || len(other) != 1 || other[0].ID != b1.ID || other[0].Status != TrialStatusCanceled {
		t.Fatalf("momentum-2 家族 = %+v, %v", other, err)
	}
}

func TestTrialStoreListFamilyMissingRoot(t *testing.T) {
	s := NewTrialStore(filepath.Join(t.TempDir(), "missing"))
	family, err := s.ListFamily("momentum")
	if family != nil || err != nil {
		t.Fatalf("缺失目录 = %v, %v, want nil,nil", family, err)
	}
}

func TestTrialStoreListFamilyCorruptFailsClosed(t *testing.T) {
	s := NewTrialStore(t.TempDir())
	if err := os.WriteFile(filepath.Join(s.root, "tr_20260102T030405678Z_deadbeef.request.json"),
		[]byte("{broken"), 0644); err != nil {
		t.Fatalf("写损坏请求: %v", err)
	}
	if _, err := s.ListFamily("momentum"); err == nil {
		t.Fatalf("损坏请求应 fail closed")
	}
	// 合法请求 + 损坏结果同样 fail closed。
	id := "tr_20260102T030405679Z_deadbeef"
	req := `{"id":"` + id + `","familyId":"momentum","variantId":"v","analysisId":"an_20260102T030405678Z_deadbeef","status":"running"}`
	if err := os.WriteFile(filepath.Join(s.root, id+trialRequestSuffix), []byte(req), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.root, id+trialResultSuffix), []byte("{broken"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ListFamily("momentum"); err == nil {
		t.Fatalf("损坏结果应 fail closed")
	}
}
