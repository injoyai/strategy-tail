package lab

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/injoyai/strategy-tail/internal/portfolioresearch"
)

// portfolio_experiment_test.go v2 Task 7 领域层测试：实验 ID、状态机迁移表、
// 产物名白名单、manifest 完整性、completed 记录校验与创建请求规范化/hash。

// validExperimentRequest 构造一份语义合法的实验创建请求。
// RequestID 留空：经 fixture.createOne 时自动唯一化；需要固定幂等键的
// 测试显式赋值。
func validExperimentRequest() CreatePortfolioExperimentRequest {
	return CreatePortfolioExperimentRequest{
		FamilyID:      "momentum-combo",
		ModelID:       "fm_20260917T150100000Z_99aabbcc",
		ModelRevision: 3,
		ModelHash:     strings.Repeat("a", 64),
		StudyRange:    DateRange{Start: "2020-01-01", End: "2024-12-31"},
		TrainRange:    DateRange{Start: "2020-01-01", End: "2022-12-31"},
		TestRange:     DateRange{Start: "2023-01-01", End: "2024-12-31"},
		Variant:       ParameterVariant{Name: "top50-equal-cost-high", Desc: "Top50 等权、高成本"},
		VariantSource: ExperimentVariantDeclared,
		DataSnapshot: portfolioresearch.DataSnapshot{
			UniverseMode: "historical_membership",
			PriceSource:  "tdx",
			PriceVersion: "v1",
			PITState:     "verified",
		},
		CodeVersion:   "v2-task7",
		EvidenceClass: portfolioresearch.EvidenceRetrospective,
		TrialCounted:  true,
		CountReason:   "参数变体为预声明组合，计入家族试验次数",
	}
}

// sha256HexBytes 测试辅助：计算字节 SHA-256 十六进制。
func sha256HexBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func TestNewExperimentID(t *testing.T) {
	now := time.Date(2026, 9, 18, 15, 4, 5, 123456000, time.UTC)
	id, err := newExperimentID(now, nil)
	if err != nil {
		t.Fatalf("生成实验 ID 失败: %v", err)
	}
	if !validExperimentID(id) {
		t.Fatalf("生成的实验 ID 格式非法: %q", id)
	}
	if !strings.HasPrefix(id, "pe_20260918T150405123Z_") {
		t.Fatalf("实验 ID 前缀/时间戳错误: %q", id)
	}
	// 两个 ID 随机段不同。
	id2, err := newExperimentID(now, nil)
	if err != nil {
		t.Fatalf("生成实验 ID 失败: %v", err)
	}
	if id == id2 {
		t.Fatalf("实验 ID 随机段重复: %q", id)
	}
}

func TestValidExperimentID(t *testing.T) {
	valid := []string{
		"pe_20260918T150405123Z_99aabbcc",
		"pe_20200101T000000000Z_00000000",
	}
	for _, id := range valid {
		if !validExperimentID(id) {
			t.Errorf("期望合法: %q", id)
		}
	}
	invalid := []string{
		"",
		"pe_20260918T150405123Z_99aabbc",       // hex 太短
		"pe_20260918T150405123Z_99AABBCC",      // 大写 hex
		"pe_20260918T150405123Z_99aabbccd",     // hex 太长
		"px_20260918T150405123Z_99aabbcc",      // 前缀错误
		"pe_20260918T15040512Z_99aabbcc",       // 时间戳缺毫秒
		"../pe_20260918T150405123Z_99aabbcc",   // 路径穿越
		"pe_20260918T150405123Z_99aabbcc/..",   // 子路径
		"/abs/pe_20260918T150405123Z_99aabbcc", // 绝对路径
		"pe_20260918T150405123Z_99aabbcc.json", // 带后缀
	}
	for _, id := range invalid {
		if validExperimentID(id) {
			t.Errorf("期望非法: %q", id)
		}
	}
}

func TestExperimentTransition(t *testing.T) {
	// 合法迁移：queued → running → 各终态。
	if err := experimentTransition(portfolioresearch.RunStateQueued, portfolioresearch.RunStateRunning); err != nil {
		t.Fatalf("queued→running 应合法: %v", err)
	}
	for _, to := range []string{
		portfolioresearch.RunStateCompleted,
		portfolioresearch.RunStateFailed,
		portfolioresearch.RunStateCancelled,
		portfolioresearch.RunStateInsufficient,
	} {
		if err := experimentTransition(portfolioresearch.RunStateRunning, to); err != nil {
			t.Errorf("running→%s 应合法: %v", to, err)
		}
	}
	// 非法迁移表。
	for _, c := range [][2]string{
		{portfolioresearch.RunStateQueued, portfolioresearch.RunStateCompleted}, // 跳级
		{portfolioresearch.RunStateQueued, portfolioresearch.RunStateCancelled}, // 跳级
		{portfolioresearch.RunStateQueued, portfolioresearch.RunStateFailed},    // 跳级
		{portfolioresearch.RunStateQueued, portfolioresearch.RunStateQueued},    // 自环
		{portfolioresearch.RunStateRunning, portfolioresearch.RunStateQueued},   // 回退
		{portfolioresearch.RunStateCompleted, portfolioresearch.RunStateRunning},
		{portfolioresearch.RunStateFailed, portfolioresearch.RunStateRunning},
		{portfolioresearch.RunStateCancelled, portfolioresearch.RunStateCancelled},
		{portfolioresearch.RunStateInsufficient, portfolioresearch.RunStateCompleted},
		{"", portfolioresearch.RunStateRunning},
		{portfolioresearch.RunStateRunning, "unknown"},
	} {
		if err := experimentTransition(c[0], c[1]); err == nil {
			t.Errorf("迁移 %s→%s 应被拒绝", c[0], c[1])
		}
	}
	// 终态重复迁移必须命中 errExperimentFinalized 哨兵。
	err := experimentTransition(portfolioresearch.RunStateCompleted, portfolioresearch.RunStateCompleted)
	if !errors.Is(err, errExperimentFinalized) {
		t.Errorf("终态重复迁移应返回 errExperimentFinalized，实际 %v", err)
	}
}

func TestValidArtifactName(t *testing.T) {
	for _, name := range portfolioArtifactNames {
		if err := ValidArtifactName(name); err != nil {
			t.Errorf("白名单产物名 %q 应合法: %v", name, err)
		}
	}
	invalid := []string{
		"",
		"../report.json",      // 父目录穿越
		"a/../report.json",    // 含穿越
		"/etc/passwd",         // 绝对路径
		`C:\evil\report.json`, // Windows 绝对路径
		"sub/report.json",     // 子目录
		"report.json/extra",   // 尾路径
		"report.txt",          // 非白名单
		"manifest.json",       // 非白名单（manifest 内嵌主记录，不单独下载）
		".",                   // 当前目录
		"..",                  // 父目录
		"report.json\x00x",    // NUL 注入
	}
	for _, name := range invalid {
		if err := ValidArtifactName(name); err == nil {
			t.Errorf("非法产物名 %q 应被拒绝", name)
		}
	}
}

func TestArtifactManifestValidate(t *testing.T) {
	// 构造完整合法清单（按白名单顺序）。
	full := &ArtifactManifest{}
	for _, name := range portfolioArtifactNames {
		full.Entries = append(full.Entries, ArtifactEntry{Name: name, SHA256: strings.Repeat("b", 64)})
	}
	if err := full.Validate(); err != nil {
		t.Fatalf("完整清单应合法: %v", err)
	}
	if h := full.hashOf("report.json"); h != strings.Repeat("b", 64) {
		t.Fatalf("hashOf(report.json) 错误: %q", h)
	}
	if h := full.hashOf("nav.csv"); h != strings.Repeat("b", 64) {
		t.Fatalf("hashOf(nav.csv) 错误: %q", h)
	}
	if h := full.hashOf("missing.csv"); h != "" {
		t.Fatalf("不存在的产物应返回空 hash: %q", h)
	}

	// 缺一类。
	missingOne := &ArtifactManifest{}
	for _, name := range portfolioArtifactNames {
		if name == "holdings.csv" {
			continue
		}
		missingOne.Entries = append(missingOne.Entries, ArtifactEntry{Name: name, SHA256: strings.Repeat("b", 64)})
	}
	if err := missingOne.Validate(); err == nil {
		t.Fatal("缺 holdings.csv 的清单应被拒绝")
	}

	// 重复条目。
	dup := &ArtifactManifest{}
	for _, name := range portfolioArtifactNames {
		dup.Entries = append(dup.Entries, ArtifactEntry{Name: name, SHA256: strings.Repeat("b", 64)})
	}
	dup.Entries = append(dup.Entries, ArtifactEntry{Name: "report.json", SHA256: strings.Repeat("b", 64)})
	if err := dup.Validate(); err == nil {
		t.Fatal("重复条目的清单应被拒绝")
	}

	// 非法产物名。
	badName := &ArtifactManifest{}
	for _, name := range portfolioArtifactNames {
		badName.Entries = append(badName.Entries, ArtifactEntry{Name: name, SHA256: strings.Repeat("b", 64)})
	}
	badName.Entries[0].Name = "../evil.json"
	if err := badName.Validate(); err == nil {
		t.Fatal("含非法产物名的清单应被拒绝")
	}

	// 非法 hash。
	badHash := &ArtifactManifest{}
	for _, name := range portfolioArtifactNames {
		badHash.Entries = append(badHash.Entries, ArtifactEntry{Name: name, SHA256: strings.Repeat("b", 64)})
	}
	badHash.Entries[1].SHA256 = "zzz"
	if err := badHash.Validate(); err == nil {
		t.Fatal("含非法 hash 的清单应被拒绝")
	}

	// nil 清单。
	var nilM *ArtifactManifest
	if err := nilM.Validate(); err == nil {
		t.Fatal("nil 清单应被拒绝")
	}
}

func TestValidateCompletedExperiment(t *testing.T) {
	rec := PortfolioExperiment{
		ExperimentID: "pe_20260918T150405123Z_99aabbcc",
		Status:       portfolioresearch.RunStateCompleted,
	}
	if err := rec.validateCompleted(); err == nil {
		t.Fatal("completed 且无清单应被拒绝")
	}
	m := &ArtifactManifest{}
	for _, name := range portfolioArtifactNames {
		m.Entries = append(m.Entries, ArtifactEntry{Name: name, SHA256: strings.Repeat("c", 64)})
	}
	rec.Manifest = m
	// reportHash 与清单不一致。
	rec.ReportHash = strings.Repeat("d", 64)
	if err := rec.validateCompleted(); err == nil {
		t.Fatal("reportHash 与清单不一致应被拒绝")
	}
	// reportPath 缺失/非法。
	rec.ReportHash = strings.Repeat("c", 64)
	if err := rec.validateCompleted(); err == nil {
		t.Fatal("reportPath 缺失应被拒绝")
	}
	rec.ReportPath = "/abs/report.json"
	if err := rec.validateCompleted(); err == nil {
		t.Fatal("reportPath 含绝对路径应被拒绝")
	}
	rec.ReportPath = "report.json"
	if err := rec.validateCompleted(); err != nil {
		t.Fatalf("完整 completed 记录应合法: %v", err)
	}
	// 非 completed 状态不要求清单。
	rec.Status = portfolioresearch.RunStateCancelled
	rec.Manifest = nil
	rec.ReportHash = ""
	rec.ReportPath = ""
	if err := rec.validateCompleted(); err != nil {
		t.Fatalf("非 completed 状态不校验清单: %v", err)
	}
}

func TestNormalizeCreatePortfolioExperimentRequest(t *testing.T) {
	req := validExperimentRequest()
	req.RequestID = "11111111-1111-4111-8111-111111111111"
	norm, err := normalizeCreatePortfolioExperimentRequest(req)
	if err != nil {
		t.Fatalf("合法请求规范化失败: %v", err)
	}
	if norm.FamilyID != req.FamilyID || norm.ModelID != req.ModelID {
		t.Fatalf("规范化改变了核心字段")
	}

	// 缺失/非法字段逐个拒绝。
	cases := []struct {
		name string
		mut  func(r *CreatePortfolioExperimentRequest)
	}{
		{"requestId 非法", func(r *CreatePortfolioExperimentRequest) { r.RequestID = "not-a-uuid" }},
		{"familyId 为空", func(r *CreatePortfolioExperimentRequest) { r.FamilyID = "" }},
		{"familyId 非法", func(r *CreatePortfolioExperimentRequest) { r.FamilyID = "../family" }},
		{"modelId 为空", func(r *CreatePortfolioExperimentRequest) { r.ModelID = "" }},
		{"modelId 非法", func(r *CreatePortfolioExperimentRequest) { r.ModelID = "evil" }},
		{"modelRevision 非法", func(r *CreatePortfolioExperimentRequest) { r.ModelRevision = 0 }},
		{"modelHash 为空", func(r *CreatePortfolioExperimentRequest) { r.ModelHash = "" }},
		{"modelHash 非 hex", func(r *CreatePortfolioExperimentRequest) { r.ModelHash = "zz" }},
		{"研究区间为空", func(r *CreatePortfolioExperimentRequest) { r.StudyRange = DateRange{} }},
		{"研究区间格式非法", func(r *CreatePortfolioExperimentRequest) {
			r.StudyRange = DateRange{Start: "2020/01/01", End: "2024-12-31"}
		}},
		{"研究区间倒置", func(r *CreatePortfolioExperimentRequest) {
			r.StudyRange = DateRange{Start: "2024-12-31", End: "2020-01-01"}
		}},
		{"训练区间格式非法", func(r *CreatePortfolioExperimentRequest) {
			r.TrainRange = DateRange{Start: "2020-1-1", End: "2022-12-31"}
		}},
		{"测试区间倒置", func(r *CreatePortfolioExperimentRequest) {
			r.TestRange = DateRange{Start: "2024-01-01", End: "2023-01-01"}
		}},
		{"变体名为空", func(r *CreatePortfolioExperimentRequest) { r.Variant.Name = "" }},
		{"变体来源非法", func(r *CreatePortfolioExperimentRequest) { r.VariantSource = "hacked" }},
		{"数据快照为空", func(r *CreatePortfolioExperimentRequest) {
			r.DataSnapshot = portfolioresearch.DataSnapshot{}
		}},
		{"codeVersion 为空", func(r *CreatePortfolioExperimentRequest) { r.CodeVersion = "" }},
		{"证据等级非法", func(r *CreatePortfolioExperimentRequest) { r.EvidenceClass = "legendary" }},
		{"计数理由为空", func(r *CreatePortfolioExperimentRequest) { r.CountReason = "" }},
	}
	for _, c := range cases {
		bad := validExperimentRequest()
		c.mut(&bad)
		if _, err := normalizeCreatePortfolioExperimentRequest(bad); err == nil {
			t.Errorf("%s: 应被拒绝", c.name)
		}
	}

	// 空训练/测试区间允许（可选）。
	opt := validExperimentRequest()
	opt.RequestID = "11111111-1111-4111-8111-111111111111"
	opt.TrainRange = DateRange{}
	opt.TestRange = DateRange{}
	if _, err := normalizeCreatePortfolioExperimentRequest(opt); err != nil {
		t.Fatalf("空训练/测试区间应允许: %v", err)
	}

	// 不计入试验次数时计数理由仍需给出（说明理由）。
	notCounted := validExperimentRequest()
	notCounted.RequestID = "11111111-1111-4111-8111-111111111111"
	notCounted.TrialCounted = false
	if _, err := normalizeCreatePortfolioExperimentRequest(notCounted); err != nil {
		t.Fatalf("不计入试验次数应允许（理由仍必填）: %v", err)
	}
}

func TestCreatePortfolioExperimentRequestHash(t *testing.T) {
	a := validExperimentRequest()
	a.RequestID = "11111111-1111-4111-8111-111111111111"
	b := validExperimentRequest()
	b.RequestID = "11111111-1111-4111-8111-111111111111"
	ha, err := createPortfolioExperimentRequestHash(a)
	if err != nil {
		t.Fatalf("请求 hash 失败: %v", err)
	}
	hb, err := createPortfolioExperimentRequestHash(b)
	if err != nil {
		t.Fatalf("请求 hash 失败: %v", err)
	}
	if ha != hb {
		t.Fatalf("相同请求 hash 应一致")
	}
	// 任意语义字段变化 → hash 不同。
	b.Variant.Name = "top100-equal"
	hb, err = createPortfolioExperimentRequestHash(b)
	if err != nil {
		t.Fatalf("请求 hash 失败: %v", err)
	}
	if ha == hb {
		t.Fatalf("不同请求 hash 应不同")
	}
}

func TestDateRangeValidate(t *testing.T) {
	if err := (DateRange{}).validate(); err != nil {
		t.Fatalf("空区间应合法: %v", err)
	}
	if err := (DateRange{Start: "2020-01-01", End: "2020-01-01"}).validate(); err != nil {
		t.Fatalf("同日区间应合法: %v", err)
	}
	if err := (DateRange{Start: "2020-01-01", End: "2024-12-31"}).validate(); err != nil {
		t.Fatalf("正常区间应合法: %v", err)
	}
	bad := []DateRange{
		{Start: "2020-1-1", End: "2024-12-31"},
		{Start: "2020-01-01", End: "20241231"},
		{Start: "2024-12-31", End: "2020-01-01"},
		{Start: "2020-01-01", End: ""},
	}
	for _, d := range bad {
		if err := d.validate(); err == nil {
			t.Errorf("非法区间 %+v 应被拒绝", d)
		}
	}
}
