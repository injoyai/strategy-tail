package lab

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/injoyai/strategy-tail/internal/portfolioresearch"
)

// factor_model.go v2 Task 1 适配/装配层（计划 2026-09-18-multifactor-portfolio-v2
// §Task 1，设计 §5.2/§5.3/§12）。
//
// 职责：把 v1 ValidationInput 类型化视图装配成 FactorModel 创建请求。契约：
//   - 创建 revision 时重新读取并校验所有上游 validation hash（ValidationInput.Get
//     内部经 readRequest 重算 hash，不匹配 fail closed）；
//   - 不接受客户端自报证据等级——请求结构无 evidenceClass 字段（JSON 未知字段
//     反序列化时被忽略），证据等级与方向由后端从上游验证唯一派生；
//   - 方向映射用显式 switch，未知值报错。
//
// 存储见 factor_model_store.go；领域类型见 internal/portfolioresearch/types.go。

// modelIDRe 模型 ID 格式：fm_<UTC yyyyMMddTHHmmssSSSZ>_<8 hex>。
var modelIDRe = regexp.MustCompile(`^fm_\d{8}T\d{9}Z_[0-9a-f]{8}$`)

// newModelID 生成不可变模型 ID。
func newModelID(now time.Time, random io.Reader) (string, error) {
	return newPrefixedID("fm", now, random)
}

// validModelID 严格校验模型 ID 格式（同时拒绝路径穿越）。
func validModelID(id string) bool { return modelIDRe.MatchString(id) }

// CreateFactorModelRequest 创建模型/追加 revision 请求（API 层输入）。
//
// 注意：本结构刻意不提供 evidenceClass 字段——证据等级由后端从上游验证
// 派生（模型 = 最弱输入），客户端声明一律被忽略/拒绝。ModelID 为空表示
// 新建模型（服务端生成 ID）；非空表示向该模型追加 revision（必须提供
// BaseRevision 作为乐观并发基数）。
type CreateFactorModelRequest struct {
	RequestID         string                              `json:"requestId"`
	ModelID           string                              `json:"modelId,omitempty"`
	BaseRevision      int                                 `json:"baseRevision,omitempty"`
	CreatedBy         string                              `json:"createdBy"`
	ResearchQuestion  string                              `json:"researchQuestion"`
	Hypothesis        string                              `json:"hypothesis"`
	FactorValidations []string                            `json:"factorValidations"`
	TransformPipeline portfolioresearch.TransformPipeline `json:"transformPipeline"`
	Combination       portfolioresearch.CombinationSpec   `json:"combination"`
	PortfolioPolicy   portfolioresearch.PortfolioPolicy   `json:"portfolioPolicy"`
	Execution         portfolioresearch.ExecutionSpec     `json:"execution"`
	Benchmark         portfolioresearch.BenchmarkSpec     `json:"benchmark"`
}

// normalizeCreateFactorModelRequest 请求纯校验与规范化：UUID 幂等键、模型
// ID/revision 关系、文本长度、上游验证 ID 去重排序、领域校验与资源限制。
func normalizeCreateFactorModelRequest(req CreateFactorModelRequest) (CreateFactorModelRequest, error) {
	req.RequestID = strings.TrimSpace(req.RequestID)
	req.CreatedBy = strings.TrimSpace(req.CreatedBy)
	req.ResearchQuestion = strings.TrimSpace(req.ResearchQuestion)
	req.Hypothesis = strings.TrimSpace(req.Hypothesis)
	if !validUUID(req.RequestID) {
		return req, fmt.Errorf("requestId 必须是合法 UUID")
	}
	if req.ModelID != "" && !validModelID(req.ModelID) {
		return req, fmt.Errorf("非法模型 ID: %q", req.ModelID)
	}
	if req.ModelID == "" && req.BaseRevision != 0 {
		return req, fmt.Errorf("新建模型不得携带 baseRevision: %d", req.BaseRevision)
	}
	if req.ModelID != "" && req.BaseRevision < 1 {
		return req, fmt.Errorf("追加 revision 必须提供 baseRevision（应为 >=1）")
	}

	limits := portfolioresearch.DefaultModelLimits()
	if n := runeLen(req.CreatedBy); n < 1 || n > limits.MaxCreatedByRunes {
		return req, fmt.Errorf("createdBy 需要 1～%d 个字符，实际 %d", limits.MaxCreatedByRunes, n)
	}
	if n := runeLen(req.ResearchQuestion); n < 1 || n > limits.MaxQuestionRunes {
		return req, fmt.Errorf("researchQuestion 需要 1～%d 个字符，实际 %d", limits.MaxQuestionRunes, n)
	}
	if n := runeLen(req.Hypothesis); n < 1 || n > limits.MaxHypothesisRunes {
		return req, fmt.Errorf("hypothesis 需要 1～%d 个字符，实际 %d", limits.MaxHypothesisRunes, n)
	}
	if len(req.FactorValidations) == 0 {
		return req, fmt.Errorf("factorValidations 不能为空")
	}
	if len(req.FactorValidations) > limits.MaxFactors {
		return req, fmt.Errorf("factors: 最多 %d 个，实际 %d", limits.MaxFactors, len(req.FactorValidations))
	}
	seen := make(map[string]bool, len(req.FactorValidations))
	for i, id := range req.FactorValidations {
		if !validValidationID(id) {
			return req, fmt.Errorf("factorValidations[%d] 非法: %q", i, id)
		}
		if seen[id] {
			return req, fmt.Errorf("factorValidations 存在重复: %s", id)
		}
		seen[id] = true
	}
	sort.Strings(req.FactorValidations)

	if err := req.TransformPipeline.Validate(); err != nil {
		return req, fmt.Errorf("transformPipeline: %w", err)
	}
	if err := req.Combination.Validate(); err != nil {
		return req, fmt.Errorf("combination: %w", err)
	}
	if err := req.PortfolioPolicy.Validate(limits); err != nil {
		return req, fmt.Errorf("portfolioPolicy: %w", err)
	}
	if err := req.Execution.Validate(); err != nil {
		return req, fmt.Errorf("execution: %w", err)
	}
	if err := req.Benchmark.Validate(); err != nil {
		return req, fmt.Errorf("benchmark: %w", err)
	}
	return req, nil
}

// createFactorModelRequestHash 规范化请求的 SHA-256（幂等判定）：同一
// RequestID 相同 hash 幂等返回，不同 hash 视为幂等键复用冲突。
func createFactorModelRequestHash(req CreateFactorModelRequest) (string, error) {
	buf, err := json.Marshal(req)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(buf)
	return hex.EncodeToString(sum[:]), nil
}

// assembleFactorModel 从上游验证装配模型内容（纯装配：不分配 modelId/
// revision/modelHash/幂等键）。对每个上游验证 ID 重新读取并校验（hash
// 重算 fail closed），证据等级与方向全部由后端派生。
func assembleFactorModel(req CreateFactorModelRequest, input ValidationInput, now time.Time) (portfolioresearch.FactorModel, error) {
	if input == nil {
		return portfolioresearch.FactorModel{}, fmt.Errorf("上游验证输入缺失")
	}
	factors := make([]portfolioresearch.ValidatedFactorRef, 0, len(req.FactorValidations))
	for i, vid := range req.FactorValidations {
		view, err := input.Get(vid)
		if err != nil {
			return portfolioresearch.FactorModel{}, fmt.Errorf("读取上游验证 %s 失败: %w", vid, err)
		}
		ref, err := buildValidatedFactorRef(view)
		if err != nil {
			return portfolioresearch.FactorModel{}, fmt.Errorf("factorValidations[%d] %s: %w", i, vid, err)
		}
		factors = append(factors, ref)
	}
	classes := make([]string, len(factors))
	for i := range factors {
		classes[i] = factors[i].EvidenceClass
	}
	ec, err := portfolioresearch.CombineEvidenceClass(classes)
	if err != nil {
		return portfolioresearch.FactorModel{}, fmt.Errorf("模型证据等级推导失败: %w", err)
	}
	return portfolioresearch.FactorModel{
		CreatedAt:         now.UTC().Format(time.RFC3339),
		CreatedBy:         req.CreatedBy,
		ResearchQuestion:  req.ResearchQuestion,
		Hypothesis:        req.Hypothesis,
		ValidatedFactors:  factors,
		TransformPipeline: req.TransformPipeline,
		Combination:       req.Combination,
		PortfolioPolicy:   req.PortfolioPolicy,
		Execution:         req.Execution,
		Benchmark:         req.Benchmark,
		EvidenceClass:     ec,
		CodeVersion:       "v2-task1",
		DataSnapshot:      factors[0].DataSnapshot,
	}, nil
}

// buildValidatedFactorRef 从上游 ValidationView 派生 ValidatedFactorRef。
// 候选 revision、实例参数、实现版本、方向、证据等级与数据快照全部来自冻结
// 记录；显示名称不作为身份。方向经显式 switch 映射，未知值报错。
func buildValidatedFactorRef(view ValidationView) (portfolioresearch.ValidatedFactorRef, error) {
	vr := view.Request
	if !validValidationID(vr.ID) {
		return portfolioresearch.ValidatedFactorRef{}, fmt.Errorf("上游验证 ID 非法: %q", vr.ID)
	}
	if vr.ResearchProtocol == nil {
		return portfolioresearch.ValidatedFactorRef{}, fmt.Errorf("上游验证 %s 缺少研究协议，无法派生方向/数据快照", vr.ID)
	}
	direction, err := mapExpectedDirection(vr.ResearchProtocol.Hypothesis.ExpectedDirection)
	if err != nil {
		return portfolioresearch.ValidatedFactorRef{}, fmt.Errorf("上游验证 %s: %w", vr.ID, err)
	}
	ec, err := mapValidationEvidenceClass(vr.Protocol.EvidenceClass)
	if err != nil {
		return portfolioresearch.ValidatedFactorRef{}, fmt.Errorf("上游验证 %s: %w", vr.ID, err)
	}
	horizon := vr.Candidate.Evidence.Window
	if horizon < 1 && len(vr.ResearchProtocol.Labels.Horizons) > 0 {
		horizon = vr.ResearchProtocol.Labels.Horizons[0]
	}
	return portfolioresearch.ValidatedFactorRef{
		CandidateID:           vr.Candidate.ID,
		CandidateRevision:     vr.Candidate.Revision,
		ValidationID:          vr.ID,
		FactorKind:            vr.Candidate.Factor.Kind,
		FactorDays:            vr.Candidate.Factor.Days,
		ImplementationVersion: vr.Candidate.Factor.ImplementationVersion,
		Direction:             direction,
		EvidenceClass:         ec,
		PrimaryHorizon:        horizon,
		DataSnapshot:          snapshotFromProtocol(*vr.ResearchProtocol),
	}, nil
}

// mapExpectedDirection v1 研究协议预期方向 → 领域方向（设计 §5.2）。显式
// switch，未知值（含 two_sided）一律报错，不做静默映射。
func mapExpectedDirection(expected string) (string, error) {
	switch expected {
	case labelDirectionPositive:
		return portfolioresearch.DirectionHigherIsBetter, nil
	case labelDirectionNegative:
		return portfolioresearch.DirectionLowerIsBetter, nil
	default:
		return "", fmt.Errorf("方向非法: %q（仅支持 %s→%s、%s→%s）",
			expected, labelDirectionPositive, portfolioresearch.DirectionHigherIsBetter,
			labelDirectionNegative, portfolioresearch.DirectionLowerIsBetter)
	}
}

// mapValidationEvidenceClass v1 验证层证据等级 → 领域证据等级。验证层只有
// retrospective | prospective；exploratory 属于分析层，装配时不会出现。
func mapValidationEvidenceClass(ec string) (string, error) {
	switch ec {
	case validationEvidenceRetrospective:
		return portfolioresearch.EvidenceRetrospective, nil
	case validationEvidenceProspective:
		return portfolioresearch.EvidenceProspective, nil
	default:
		return "", fmt.Errorf("上游验证证据等级非法: %q（应为 retrospective | prospective）", ec)
	}
}

// snapshotFromProtocol 从研究协议提取数据/股票池快照引用（设计 §5.2）。
func snapshotFromProtocol(p ResearchProtocol) portfolioresearch.DataSnapshot {
	return portfolioresearch.DataSnapshot{
		UniverseID:      p.Universe.ID,
		UniverseVersion: p.Universe.Version,
		UniverseMode:    p.Universe.Mode,
		PriceSource:     p.Data.PriceSource,
		PriceVersion:    p.Data.PriceVersion,
		Adjustment:      p.Data.Adjustment,
		PITState:        p.Data.PITState,
		SnapshotAt:      p.Data.SnapshotAt,
	}
}
