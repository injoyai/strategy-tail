package lab

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/injoyai/strategy-tail/internal/portfolioresearch"
)

// portfolio_experiment.go v2 Task 7 实验账本领域层（设计 §5.4/§12.1/§14）。
//
// PortfolioExperiment 每次运行保存完整输入与结果引用：family、模型
// revision/hash、研究/训练/测试区间、参数变体与来源、状态机（queued →
// running → completed/failed/cancelled/insufficient）、进度与错误、数据
// 快照、代码版本、随机种子、报告路径/hash、证据等级、是否计入试验次数及
// 理由。失败、取消和无有效样本的运行同样进入账本，不得只保留漂亮结果。
//
// 状态枚举复用 internal/portfolioresearch/types.go 的 RunState（Task 1 已
// 定义稳定枚举）。本文件只承载结构、状态机与纯函数；存储与产物发布协议见
// portfolio_experiment_store.go。

// experimentIDRe 受限实验 ID：pe_<UTC yyyyMMddTHHmmssSSSZ>_<8 hex>。
var experimentIDRe = regexp.MustCompile(`^pe_\d{8}T\d{9}Z_[0-9a-f]{8}$`)

// newExperimentID 生成不可变实验 ID；random 为 nil 时使用 crypto/rand.Reader。
func newExperimentID(now time.Time, random io.Reader) (string, error) {
	return newPrefixedID("pe", now, random)
}

// validExperimentID 严格校验实验 ID 格式（同时拒绝路径穿越）。
func validExperimentID(id string) bool { return experimentIDRe.MatchString(id) }

// portfolioArtifactNames 五类正式产物文件名白名单（设计 §12.1）。清单与
// hash 必须进入主记录；产物名是唯一的下载/读取入口，不接受任意路径。
var portfolioArtifactNames = []string{
	"report.json",
	"nav.csv",
	"orders.csv",
	"trades.csv",
	"holdings.csv",
}

// hashHexRe SHA-256 十六进制格式。
var hashHexRe = regexp.MustCompile(`^[0-9a-f]{64}$`)

// ValidArtifactName 校验产物名：仅允许白名单中的顶层文件名，拒绝空、
// 路径分隔符、"."、".."（路径穿越）与绝对路径。API 层下载与读取入口复用。
func ValidArtifactName(name string) error {
	if name == "" {
		return fmt.Errorf("产物名为空")
	}
	if strings.ContainsAny(name, `/\`) || name == "." || name == ".." {
		return fmt.Errorf("非法产物名: %q（拒绝路径穿越与绝对路径）", name)
	}
	for _, allowed := range portfolioArtifactNames {
		if name == allowed {
			return nil
		}
	}
	return fmt.Errorf("非法产物名: %q（仅允许 %s）", name, strings.Join(portfolioArtifactNames, ", "))
}

// DateRange 研究/训练/测试区间（YYYY-MM-DD 闭区间；允许为空表示不适用）。
type DateRange struct {
	Start string `json:"start"`
	End   string `json:"end"`
}

var dateRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

// validate 区间校验：空区间合法（训练/测试可选）；非空时格式 YYYY-MM-DD
// 且 Start <= End（同格式可直接字典序比较）。
func (d DateRange) validate() error {
	if d.Start == "" && d.End == "" {
		return nil
	}
	if !dateRe.MatchString(d.Start) || !dateRe.MatchString(d.End) {
		return fmt.Errorf("区间日期必须为 YYYY-MM-DD，实际 %q ~ %q", d.Start, d.End)
	}
	if d.Start > d.End {
		return fmt.Errorf("区间起始晚于结束: %s > %s", d.Start, d.End)
	}
	return nil
}

// ParameterVariant 参数变体标识与说明（设计 §5.4/§11.2：只执行预先声明的
// 参数，不自动调参）。
type ParameterVariant struct {
	Name string `json:"name"`
	Desc string `json:"desc,omitempty"`
}

// 变体来源白名单（设计 §11.2/§14）：预先声明为基线/声明变体，或探索性。
const (
	ExperimentVariantBaseline    = "baseline"
	ExperimentVariantDeclared    = "declared"
	ExperimentVariantExploratory = "exploratory"
)

// validExperimentVariantSource 校验变体来源枚举。
func validExperimentVariantSource(s string) bool {
	switch s {
	case ExperimentVariantBaseline, ExperimentVariantDeclared, ExperimentVariantExploratory:
		return true
	}
	return false
}

// ArtifactEntry 单个产物的安全相对名与其 SHA-256（设计 §12.1：清单进入主记录）。
type ArtifactEntry struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
}

// ArtifactManifest 产物清单：按白名单固定顺序记录五类产物的文件名与哈希。
// 读取 completed 记录时重算磁盘产物哈希与清单比对，不匹配 fail closed。
type ArtifactManifest struct {
	Entries []ArtifactEntry `json:"entries"`
}

// Validate 清单完整性校验：非空、条目均为白名单产物名、无重复、hash 为
// 64 位 hex、五类产物齐全。任何篡改 fail closed。
func (m *ArtifactManifest) Validate() error {
	if m == nil {
		return fmt.Errorf("产物清单缺失")
	}
	seen := make(map[string]bool, len(portfolioArtifactNames))
	for _, e := range m.Entries {
		if err := ValidArtifactName(e.Name); err != nil {
			return err
		}
		if seen[e.Name] {
			return fmt.Errorf("产物清单存在重复: %s", e.Name)
		}
		seen[e.Name] = true
		if !hashHexRe.MatchString(e.SHA256) {
			return fmt.Errorf("产物 %s 哈希非法: %q", e.Name, e.SHA256)
		}
	}
	for _, allowed := range portfolioArtifactNames {
		if !seen[allowed] {
			return fmt.Errorf("产物清单缺少 %s", allowed)
		}
	}
	return nil
}

// hashOf 返回指定产物名的哈希；不存在返回空字符串。
func (m *ArtifactManifest) hashOf(name string) string {
	for _, e := range m.Entries {
		if e.Name == name {
			return e.SHA256
		}
	}
	return ""
}

// finishedExperimentStates 终态白名单：进入后不可再迁移（追加式账本）。
var finishedExperimentStates = map[string]bool{
	portfolioresearch.RunStateCompleted:    true,
	portfolioresearch.RunStateFailed:       true,
	portfolioresearch.RunStateCancelled:    true,
	portfolioresearch.RunStateInsufficient: true,
}

// 实验账本错误（API 层映射状态码）。
var (
	errExperimentNotFound     = errors.New("实验不存在")
	errExperimentFinalized    = errors.New("实验已进入终态，拒绝修改（不可变记录）")
	errExperimentIllegalState = errors.New("非法实验状态转换")
)

// experimentTransition 状态机纯函数（设计 §5.4，固定迁移表）：
//
//	queued → running → completed | failed | cancelled | insufficient
//
// 终态不可再迁移；running 允许更新进度但不允许回退。非法迁移报错，
// 终态重复迁移命中 errExperimentFinalized。
func experimentTransition(from, to string) error {
	switch {
	case from == portfolioresearch.RunStateQueued && to == portfolioresearch.RunStateRunning:
		return nil
	case from == portfolioresearch.RunStateRunning && finishedExperimentStates[to]:
		return nil
	case from == to && finishedExperimentStates[from]:
		return fmt.Errorf("%w: %s", errExperimentFinalized, from)
	default:
		return fmt.Errorf("%w: %s → %s", errExperimentIllegalState, from, to)
	}
}

// PortfolioExperiment 一次组合研究的账本记录（设计 §5.4）。状态与产物清单
// 由 Store 维护；failed/cancelled/insufficient 同样保留全部元数据，只发布
// 错误信息，不发布正式报告。
type PortfolioExperiment struct {
	ExperimentID  string `json:"experimentId"`
	FamilyID      string `json:"familyId"`
	ModelID       string `json:"modelId"`
	ModelRevision int    `json:"modelRevision"`
	ModelHash     string `json:"modelHash"`

	StudyRange DateRange `json:"studyRange"`
	TrainRange DateRange `json:"trainRange"`
	TestRange  DateRange `json:"testRange"`

	Variant       ParameterVariant `json:"variant"`
	VariantSource string           `json:"variantSource"` // baseline | declared | exploratory

	Status  string `json:"status"` // RunState 枚举（queued/running/completed/failed/cancelled/insufficient）
	Message string `json:"message,omitempty"`
	// Error 终态诊断信息（失败原因/取消说明/无有效样本原因）。
	Error    string `json:"error,omitempty"`
	Progress int    `json:"progress"`

	DataSnapshot portfolioresearch.DataSnapshot `json:"dataSnapshot"`
	CodeVersion  string                         `json:"codeVersion"`
	RandomSeed   string                         `json:"randomSeed,omitempty"`

	// ReportPath 安全相对产物名（仅 "report.json"，绝不含绝对路径）。
	ReportPath    string `json:"reportPath,omitempty"`
	ReportHash    string `json:"reportHash,omitempty"`
	EvidenceClass string `json:"evidenceClass"`

	// Manifest 五类产物清单（completed 才发布；读取时校验磁盘哈希一致）。
	Manifest *ArtifactManifest `json:"manifest,omitempty"`

	// TrialCounted 是否计入家族试验次数；CountReason 说明计入/不计入理由。
	TrialCounted bool   `json:"trialCounted"`
	CountReason  string `json:"countReason"`

	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`

	RequestID   string `json:"requestId,omitempty"`
	RequestHash string `json:"requestHash,omitempty"`
}

// validateCompleted 完成门槛校验：completed 主记录必须伴随完整清单且
// reportHash 与清单中 report.json 的哈希一致。非 completed 状态不要求。
func (e *PortfolioExperiment) validateCompleted() error {
	if e.Status != portfolioresearch.RunStateCompleted {
		return nil
	}
	if e.Manifest == nil {
		return fmt.Errorf("completed 实验缺少产物清单（完成门槛：主记录 completed 必须伴随完整 manifest）")
	}
	if err := e.Manifest.Validate(); err != nil {
		return fmt.Errorf("产物清单非法: %w", err)
	}
	h := e.Manifest.hashOf("report.json")
	if e.ReportHash == "" || e.ReportHash != h {
		return fmt.Errorf("报告哈希与清单不一致（记录不可信）")
	}
	if e.ReportPath != "report.json" {
		return fmt.Errorf("报告路径非法: %q（仅允许相对产物名 report.json）", e.ReportPath)
	}
	return nil
}

// ExperimentLimits 实验创建的资源限制（防滥用；与 ModelLimits 同模式）。
type ExperimentLimits struct {
	MaxFamilyRunes      int // familyId 字符上限
	MaxVariantNameRunes int // 变体名上限
	MaxCountReasonRunes int // 计数理由上限
	MaxCodeVersionRunes int // 代码版本上限
	MaxErrorRunes       int // 错误/诊断信息上限
}

// DefaultExperimentLimits 默认资源限制。
func DefaultExperimentLimits() ExperimentLimits {
	return ExperimentLimits{
		MaxFamilyRunes:      64,
		MaxVariantNameRunes: 80,
		MaxCountReasonRunes: 500,
		MaxCodeVersionRunes: 80,
		MaxErrorRunes:       2000,
	}
}

// CreatePortfolioExperimentRequest 创建实验请求（API 层输入）。状态由
// 服务端派生（初始 queued），不接受客户端自报。RequestID 为幂等键。
type CreatePortfolioExperimentRequest struct {
	RequestID     string `json:"requestId"`
	FamilyID      string `json:"familyId"`
	ModelID       string `json:"modelId"`
	ModelRevision int    `json:"modelRevision"`
	ModelHash     string `json:"modelHash"`

	StudyRange DateRange `json:"studyRange"`
	TrainRange DateRange `json:"trainRange"`
	TestRange  DateRange `json:"testRange"`

	Variant       ParameterVariant `json:"variant"`
	VariantSource string           `json:"variantSource"`

	DataSnapshot portfolioresearch.DataSnapshot `json:"dataSnapshot"`
	CodeVersion  string                         `json:"codeVersion"`
	RandomSeed   string                         `json:"randomSeed,omitempty"`

	EvidenceClass string `json:"evidenceClass"`

	TrialCounted bool   `json:"trialCounted"`
	CountReason  string `json:"countReason"`
}

// normalizeCreatePortfolioExperimentRequest 请求纯校验与规范化：幂等键、
// family/model 引用、区间、变体与来源、数据快照、代码版本、证据等级与
// 试验计数理由。任何字段非法都报错（fail closed）。
func normalizeCreatePortfolioExperimentRequest(req CreatePortfolioExperimentRequest) (CreatePortfolioExperimentRequest, error) {
	req.RequestID = strings.TrimSpace(req.RequestID)
	req.FamilyID = strings.TrimSpace(req.FamilyID)
	req.ModelID = strings.TrimSpace(req.ModelID)
	req.ModelHash = strings.TrimSpace(req.ModelHash)
	req.CodeVersion = strings.TrimSpace(req.CodeVersion)
	req.CountReason = strings.TrimSpace(req.CountReason)
	req.Variant.Name = strings.TrimSpace(req.Variant.Name)
	req.Variant.Desc = strings.TrimSpace(req.Variant.Desc)

	limits := DefaultExperimentLimits()
	if !validUUID(req.RequestID) {
		return req, fmt.Errorf("requestId 必须是合法 UUID")
	}
	if !protocolIDRe.MatchString(req.FamilyID) {
		return req, fmt.Errorf("familyId 非法: %q（限小写字母/数字开头，可含 - _，最长 64）", req.FamilyID)
	}
	if !validModelID(req.ModelID) {
		return req, fmt.Errorf("modelId 非法: %q", req.ModelID)
	}
	if req.ModelRevision < 1 {
		return req, fmt.Errorf("modelRevision 无效: %d（应为 >=1）", req.ModelRevision)
	}
	if !hashHexRe.MatchString(req.ModelHash) {
		return req, fmt.Errorf("modelHash 必须为 64 位十六进制")
	}
	if err := req.StudyRange.validate(); err != nil {
		return req, fmt.Errorf("studyRange: %w", err)
	}
	// 研究区间必填（运行总归有研究窗口）；训练/测试区间可选。
	if req.StudyRange.Start == "" || req.StudyRange.End == "" {
		return req, fmt.Errorf("studyRange 必填（研究区间 start/end 均为 YYYY-MM-DD）")
	}
	if err := req.TrainRange.validate(); err != nil {
		return req, fmt.Errorf("trainRange: %w", err)
	}
	if err := req.TestRange.validate(); err != nil {
		return req, fmt.Errorf("testRange: %w", err)
	}
	if n := runeLen(req.Variant.Name); n < 1 || n > limits.MaxVariantNameRunes {
		return req, fmt.Errorf("variant.name 需要 1～%d 个字符，实际 %d", limits.MaxVariantNameRunes, n)
	}
	if !validExperimentVariantSource(req.VariantSource) {
		return req, fmt.Errorf("variantSource 非法: %q（应为 %s | %s | %s）",
			req.VariantSource, ExperimentVariantBaseline, ExperimentVariantDeclared, ExperimentVariantExploratory)
	}
	if req.DataSnapshot.PriceSource == "" && req.DataSnapshot.UniverseMode == "" {
		return req, fmt.Errorf("dataSnapshot 缺少数据/股票池引用（priceSource 与 universeMode 至少一个非空）")
	}
	if n := runeLen(req.CodeVersion); n < 1 || n > limits.MaxCodeVersionRunes {
		return req, fmt.Errorf("codeVersion 需要 1～%d 个字符，实际 %d", limits.MaxCodeVersionRunes, n)
	}
	if !portfolioresearch.ValidEvidenceClass(req.EvidenceClass) {
		return req, fmt.Errorf("evidenceClass 非法: %q（应为 exploratory | retrospective | prospective）", req.EvidenceClass)
	}
	if n := runeLen(req.CountReason); n < 1 || n > limits.MaxCountReasonRunes {
		return req, fmt.Errorf("countReason 需要 1～%d 个字符（说明计入/不计入试验次数的理由），实际 %d", limits.MaxCountReasonRunes, n)
	}
	return req, nil
}

// createPortfolioExperimentRequestHash 规范化请求的 SHA-256（幂等判定）：
// 同一 RequestID 相同 hash 幂等返回，不同 hash 视为幂等键复用冲突。
func createPortfolioExperimentRequestHash(req CreatePortfolioExperimentRequest) (string, error) {
	buf, err := json.Marshal(req)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(buf)
	return hex.EncodeToString(sum[:]), nil
}

// buildExperimentRecord 从规范化请求装配初始记录（状态=queued，进度=0）。
// 不访问文件系统；ID/时间戳由调用方传入。
func buildExperimentRecord(req CreatePortfolioExperimentRequest, id, requestHash string, now time.Time) PortfolioExperiment {
	return PortfolioExperiment{
		ExperimentID:  id,
		FamilyID:      req.FamilyID,
		ModelID:       req.ModelID,
		ModelRevision: req.ModelRevision,
		ModelHash:     req.ModelHash,
		StudyRange:    req.StudyRange,
		TrainRange:    req.TrainRange,
		TestRange:     req.TestRange,
		Variant:       req.Variant,
		VariantSource: req.VariantSource,
		Status:        portfolioresearch.RunStateQueued,
		Progress:      0,
		DataSnapshot:  req.DataSnapshot,
		CodeVersion:   req.CodeVersion,
		RandomSeed:    req.RandomSeed,
		EvidenceClass: req.EvidenceClass,
		TrialCounted:  req.TrialCounted,
		CountReason:   req.CountReason,
		CreatedAt:     now.UTC().Format(time.RFC3339),
		UpdatedAt:     now.UTC().Format(time.RFC3339),
		RequestID:     req.RequestID,
		RequestHash:   requestHash,
	}
}
