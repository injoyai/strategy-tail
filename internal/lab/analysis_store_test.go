package lab

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// analysisReportForTest 构造最小可用分析报告（v3）。
func analysisReportForTest(id, kind, finished string) *AnalysisReport {
	return &AnalysisReport{
		AnalysisID:      id,
		FactorName:      "N日动量(2)",
		Kind:            kind,
		Window:          1,
		Range:           AnalysisRange{StartYear: 2025, EndYear: 2025, SampleMode: "codes", SampleSize: 2},
		AnalysisVersion: 3,
		Factor: FactorSnapshot{
			Kind: kind, Name: "N日动量(2)", Description: "近N日涨跌幅",
			ParameterLabel: "回看天数", Days: 2, Unit: "ratio", ImplementationVersion: 1,
		},
		Stats:         ICStats{Pairs: 19, Mean: 1, Std: 0, TStat: 0},
		FinishedAt:    finished,
		FirstDataDate: "2025-01-02",
		LastDataDate:  "2025-02-03",
	}
}

// TestNewAnalysisID ID 格式、随机后缀唯一性与非法字符拒绝。
func TestNewAnalysisID(t *testing.T) {
	now := time.Date(2026, 9, 17, 15, 30, 12, 123_000_000, time.UTC)
	id, err := newAnalysisID(now, strings.NewReader("\x01\x02\x03\x04"))
	if err != nil {
		t.Fatal(err)
	}
	if id != "an_20260917T153012123Z_01020304" {
		t.Fatalf("id = %q, want an_20260917T153012123Z_01020304", id)
	}
	if !validAnalysisID(id) {
		t.Fatalf("合法 ID 未通过校验: %q", id)
	}
	// 不同随机字节 → 不同后缀
	id2, err := newAnalysisID(now, strings.NewReader("\x01\x02\x03\x05"))
	if err != nil {
		t.Fatal(err)
	}
	if id2 == id || !validAnalysisID(id2) {
		t.Fatalf("随机后缀应唯一: %q vs %q", id, id2)
	}
	// 非法路径字符与格式拒绝
	for _, bad := range []string{
		"", "an_20260917T153012123Z_xyz", "../evil", "an_20260917", "an_20260917T153012123Z_01020304/../x",
		"an_20260917T153012123Z_0102030g", "xan_20260917T153012123Z_01020304",
	} {
		if validAnalysisID(bad) {
			t.Fatalf("非法 ID 应被拒绝: %q", bad)
		}
	}
}

// TestAnalysisStoreKeepsHistory 同 kind 两次保存均保留，可分别读取；
// latest 指向第二份；兼容镜像仍写入固定路径。
func TestAnalysisStoreKeepsHistory(t *testing.T) {
	dir := t.TempDir()
	s := NewAnalysisStore(dir)
	id1 := "an_20260917T150000000Z_a1b2c3d4"
	id2 := "an_20260917T160000000Z_e5f6a7b8"
	if err := s.Save(analysisReportForTest(id1, "momentum", "2026-09-17T15:00:00+08:00")); err != nil {
		t.Fatal(err)
	}
	if err := s.Save(analysisReportForTest(id2, "momentum", "2026-09-17T16:00:00+08:00")); err != nil {
		t.Fatal(err)
	}
	got1, err := s.Get(id1)
	if err != nil || got1.AnalysisID != id1 {
		t.Fatalf("Get(id1) = %v, %v", got1, err)
	}
	got2, err := s.Get(id2)
	if err != nil || got2.AnalysisID != id2 {
		t.Fatalf("Get(id2) = %v, %v", got2, err)
	}
	list, err := s.List("momentum")
	if err != nil || len(list) != 2 {
		t.Fatalf("List = %v, %v", list, err)
	}
	// 列表时间倒序
	if list[0].AnalysisID != id2 || list[1].AnalysisID != id1 {
		t.Fatalf("列表顺序 = %v", list)
	}
	// latest 指向第二份
	latest, err := s.Latest("momentum")
	if err != nil || latest == nil || latest.AnalysisID != id2 {
		t.Fatalf("Latest = %v, %v", latest, err)
	}
	// 全局 latest 与 kind latest 一致
	global, err := s.Latest("")
	if err != nil || global == nil || global.AnalysisID != id2 {
		t.Fatalf("全局 Latest = %v, %v", global, err)
	}
	// 不可变目录与兼容镜像并存
	if _, err := os.Stat(filepath.Join(dir, "momentum", id1, "report.json")); err != nil {
		t.Fatalf("id1 不可变目录缺失: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "momentum", "report.json")); err != nil {
		t.Fatalf("兼容镜像缺失: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "momentum", "ic.csv")); err != nil {
		t.Fatalf("兼容 ic.csv 缺失: %v", err)
	}
}

// TestAnalysisStoreLatest kind 与全局 latest 指针语义。
func TestAnalysisStoreLatest(t *testing.T) {
	dir := t.TempDir()
	s := NewAnalysisStore(dir)
	idA := "an_20260917T150000000Z_a1b2c3d4"
	idB := "an_20260917T160000000Z_e5f6a7b8"
	if err := s.Save(analysisReportForTest(idA, "momentum", "2026-09-17T15:00:00+08:00")); err != nil {
		t.Fatal(err)
	}
	if err := s.Save(analysisReportForTest(idB, "ma_bias", "2026-09-17T16:00:00+08:00")); err != nil {
		t.Fatal(err)
	}
	// kind 各自独立
	if latest, _ := s.Latest("momentum"); latest == nil || latest.AnalysisID != idA {
		t.Fatalf("momentum latest 应指向 idA")
	}
	if latest, _ := s.Latest("ma_bias"); latest == nil || latest.AnalysisID != idB {
		t.Fatalf("ma_bias latest 应指向 idB")
	}
	// 全局 latest 指向最近一次保存（跨 kind）
	if latest, _ := s.Latest(""); latest == nil || latest.AnalysisID != idB {
		t.Fatalf("全局 latest 应指向 idB")
	}
	// 空 store：latest 与 get 均返回 nil/not found
	empty := NewAnalysisStore(t.TempDir())
	if latest, _ := empty.Latest("momentum"); latest != nil {
		t.Fatalf("空 store latest 应为 nil")
	}
	if _, err := empty.Get(idA); err != errAnalysisNotFound {
		t.Fatalf("空 store Get 应返回 not found: %v", err)
	}
}

// TestAnalysisStoreIgnoresTemp 扫描忽略临时文件、非目录文件与非法 ID 目录。
func TestAnalysisStoreIgnoresTemp(t *testing.T) {
	dir := t.TempDir()
	s := NewAnalysisStore(dir)
	id := "an_20260917T150000000Z_a1b2c3d4"
	if err := s.Save(analysisReportForTest(id, "momentum", "2026-09-17T15:00:00+08:00")); err != nil {
		t.Fatal(err)
	}
	kindDir := filepath.Join(dir, "momentum")
	// 残留临时文件（原子写中断）
	_ = os.WriteFile(filepath.Join(kindDir, ".tmp-report.json"), []byte("{bad"), 0644)
	_ = os.WriteFile(filepath.Join(kindDir, ".report.json.tmp"), []byte("{bad"), 0644)
	// 非目录文件与非法 ID 目录（模拟旧路径/垃圾目录）
	_ = os.WriteFile(filepath.Join(kindDir, "somefile.txt"), []byte("x"), 0644)
	if err := os.MkdirAll(filepath.Join(kindDir, "not_an_id"), 0755); err != nil {
		t.Fatal(err)
	}
	list, err := s.List("momentum")
	if err != nil {
		t.Fatalf("List 应忽略临时/非法目录: %v", err)
	}
	if len(list) != 1 || list[0].AnalysisID != id {
		t.Fatalf("List = %v, want 仅 1 条", list)
	}
}

// TestAnalysisStoreRejectsTraversalID ID/kind 派生路径越界拒绝。
func TestAnalysisStoreRejectsTraversalID(t *testing.T) {
	dir := t.TempDir()
	s := NewAnalysisStore(dir)
	if err := s.Save(analysisReportForTest("../../evil", "momentum", "2026-09-17T15:00:00+08:00")); err == nil {
		t.Fatal("穿越 ID 应被拒绝")
	}
	if err := s.Save(analysisReportForTest("an_20260917T150000000Z_a1b2c3d4", "../evil", "2026-09-17T15:00:00+08:00")); err == nil {
		t.Fatal("穿越 kind 应被拒绝")
	}
	if _, err := s.Get("../evil"); err == nil {
		t.Fatal("Get 穿越 ID 应被拒绝")
	}
	if _, err := s.List("../evil"); err == nil {
		t.Fatal("List 穿越 kind 应被拒绝")
	}
	if _, err := s.Latest(".."); err == nil {
		t.Fatal("Latest 穿越 kind 应被拒绝")
	}
}

// TestAnalysisStoreRejectsInvalidIDFormat 格式非法但未穿越的 ID 拒绝。
func TestAnalysisStoreRejectsInvalidIDFormat(t *testing.T) {
	dir := t.TempDir()
	s := NewAnalysisStore(dir)
	if err := s.Save(analysisReportForTest("an_bad", "momentum", "2026-09-17T15:00:00+08:00")); err == nil {
		t.Fatal("格式非法 ID 应被拒绝")
	}
}

// TestAnalysisStoreCorruptReport List/Get 遇损坏正式报告 fail closed。
func TestAnalysisStoreCorruptReport(t *testing.T) {
	dir := t.TempDir()
	s := NewAnalysisStore(dir)
	id := "an_20260917T150000000Z_a1b2c3d4"
	if err := s.Save(analysisReportForTest(id, "momentum", "2026-09-17T15:00:00+08:00")); err != nil {
		t.Fatal(err)
	}
	// 损坏正式报告
	if err := os.WriteFile(filepath.Join(dir, "momentum", id, "report.json"), []byte("{bad"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.List("momentum"); err == nil {
		t.Fatal("损坏报告 List 应报错")
	}
	if _, err := s.Get(id); err == nil {
		t.Fatal("损坏报告 Get 应报错")
	}
}

// TestServerAnalysisHistoryAPI 历史列表、指定报告与 latest 恢复 API 合同。
func TestServerAnalysisHistoryAPI(t *testing.T) {
	dir := t.TempDir()
	srv := NewServer()
	srv.analysisStore = NewAnalysisStore(dir)
	h := srv.Handler()

	id1 := "an_20260917T150000000Z_a1b2c3d4"
	id2 := "an_20260917T160000000Z_e5f6a7b8"
	store := srv.analysisStore
	if err := store.Save(analysisReportForTest(id1, "momentum", "2026-09-17T15:00:00+08:00")); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(analysisReportForTest(id2, "momentum", "2026-09-17T16:00:00+08:00")); err != nil {
		t.Fatal(err)
	}

	// 列表
	res := doReq(t, h, http.MethodGet, "/api/analyses?kind=momentum", nil, http.StatusOK)
	var list []AnalysisSummary
	if err := json.Unmarshal(res, &list); err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].AnalysisID != id2 || list[1].AnalysisID != id1 {
		t.Fatalf("列表 = %+v", list)
	}
	// 缺少 kind → 400
	doReq(t, h, http.MethodGet, "/api/analyses", nil, http.StatusBadRequest)
	// 指定报告
	res = doReq(t, h, http.MethodGet, "/api/analysis/"+id1, nil, http.StatusOK)
	var rep AnalysisReport
	if err := json.Unmarshal(res, &rep); err != nil || rep.AnalysisID != id1 {
		t.Fatalf("指定报告 = %+v, %v", rep, err)
	}
	// 未知报告 → 404
	doReq(t, h, http.MethodGet, "/api/analysis/an_20260917T000000000Z_ffffffff", nil, http.StatusNotFound)
	// 非法 ID 格式 → 404（未命中；穿越 ID 由 Store 层拒绝，HTTP 客户端会规范化 ..）
}

// TestServerLatestAnalysisAfterRestart 重启后 latest 从磁盘恢复（无内存状态）。
func TestServerLatestAnalysisAfterRestart(t *testing.T) {
	dir := t.TempDir()
	store := NewAnalysisStore(dir)
	id := "an_20260917T150000000Z_a1b2c3d4"
	if err := store.Save(analysisReportForTest(id, "momentum", "2026-09-17T15:00:00+08:00")); err != nil {
		t.Fatal(err)
	}

	// 全新 Server（等价重启）：runner 无内存报告，从磁盘恢复
	srv := NewServer()
	srv.analysisStore = NewAnalysisStore(dir)
	h := srv.Handler()
	res := doReq(t, h, http.MethodGet, "/api/analysis/latest", nil, http.StatusOK)
	var rep AnalysisReport
	if err := json.Unmarshal(res, &rep); err != nil || rep.AnalysisID != id {
		t.Fatalf("重启后 latest = %+v, %v", rep, err)
	}

	// 空 store：暂无报告 404
	empty := NewServer()
	empty.analysisStore = NewAnalysisStore(t.TempDir())
	doReq(t, empty.Handler(), http.MethodGet, "/api/analysis/latest", nil, http.StatusNotFound)
}

// TestAnalysisStoreLatestWriteOrder 全局指针晚于 kind 指针写入：
// 即使 kind 指针缺失（模拟中断），全局指针仍可恢复报告。
func TestAnalysisStoreLatestWriteOrder(t *testing.T) {
	dir := t.TempDir()
	s := NewAnalysisStore(dir)
	id := "an_20260917T150000000Z_a1b2c3d4"
	if err := s.Save(analysisReportForTest(id, "momentum", "2026-09-17T15:00:00+08:00")); err != nil {
		t.Fatal(err)
	}
	// 删除 kind 指针，仅保留全局指针
	if err := os.Remove(filepath.Join(dir, "momentum", latestFile)); err != nil {
		t.Fatal(err)
	}
	global, err := s.Latest("")
	if err != nil || global == nil || global.AnalysisID != id {
		t.Fatalf("全局指针应独立恢复: %v, %v", global, err)
	}
}
