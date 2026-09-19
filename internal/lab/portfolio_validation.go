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

// portfolio_validation.go v2 Task 8 组合验证领域层（设计 §11）。
//
// 契约：
//   - 创建验证时冻结模型引用（modelId/revision/hash）、窗口规则、门禁、
//     基准与证据要求；spec 全部字段进入验证 hash（不可变身份），读取时
//     重算 spec hash，不匹配 fail closed；
//   - 服务端 ID pv_<ts>_<hex>；修改模型或门禁 → 新验证 ID，旧验证保留并
//     由新记录的 Supersedes 派生 SupersededBy 标记（旧测试窗视为已见数据，
//     同 v1 派生模式）；
//   - 最终结论（passed | failed | insufficient | error）由后端按冻结 spec
//     从窗口结果生成（portfolioresearch.PortfolioValidationSpec.Evaluate），
//     客户端不能指定；
//   - 证据等级由后端从冻结模型派生（ModelEvidenceClass），请求不接受客户端
//     自报。
//
// 存储布局与窗口/报告协议见 portfolio_validation_store.go。

// portfolioValidationIDRe 受限验证 ID：pv_<UTC yyyyMMddTHHmmssSSSZ>_<8 hex>。
var portfolioValidationIDRe = regexp.MustCompile(`^pv_\d{8}T\d{9}Z_[0-9a-f]{8}$`)

// newPortfolioValidationID 生成不可变验证 ID；random 为 nil 时使用 crypto/rand.Reader。
func newPortfolioValidationID(now time.Time, random io.Reader) (string, error) {
	return newPrefixedID("pv", now, random)
}

// validPortfolioValidationID 严格校验验证 ID 格式（同时拒绝路径穿越）。
func validPortfolioValidationID(id string) bool { return portfolioValidationIDRe.MatchString(id) }

// portfolioValidationSchemaVersion 组合验证记录 schema 版本。
const portfolioValidationSchemaVersion = 1

// 验证状态（派生：报告文件存在即 completed，只允许一次完成）。
const (
	PortfolioValidationStateCreated   = "created"
	PortfolioValidationStateCompleted = "completed"
)

// 组合验证库错误（API 层映射状态码）。
var (
	errPortfolioValidationNotFound  = errors.New("组合验证不存在")
	errPortfolioValidationCompleted = errors.New("组合验证已完成，拒绝修改（不可变记录）")
)

// PortfolioModelSource 验证冻结时的模型读取契约：按 revision 读取模型并
// 校验 modelHash（读取时 hash 不匹配 fail closed）。实现方为
// *FactorModelStore；测试可注入桩实现。
type PortfolioModelSource interface {
	Get(modelID string, revision int) (portfolioresearch.FactorModel, error)
}

var _ PortfolioModelSource = (*FactorModelStore)(nil)

// CreatePortfolioValidationRequest 创建验证请求（API 层输入）。RequestID
// 为幂等键；Spec 携带全部冻结内容（模型引用/窗口规则/门禁/基准/证据要求）。
// 请求刻意不提供 evidenceClass 字段——证据等级由后端从冻结模型派生。
type CreatePortfolioValidationRequest struct {
	RequestID string `json:"requestId"`
	// Spec 冻结的验证规格（全部字段进入验证 hash，任一变化产生新验证）。
	Spec portfolioresearch.PortfolioValidationSpec `json:"spec"`
	// Supersedes 被替换的旧验证 ID（可选）：旧记录保留并由此派生
	// SupersededBy 标记（旧测试窗从此视为已见数据），不删除、不改写。
	Supersedes string `json:"supersedes,omitempty"`
}

// normalizeCreatePortfolioValidationRequest 请求纯校验与规范化：UUID 幂等
// 键、模型 ID 格式、冻结规格完整校验、被替换验证 ID 格式。任何字段非法都
// 报错（fail closed）。
func normalizeCreatePortfolioValidationRequest(req CreatePortfolioValidationRequest) (CreatePortfolioValidationRequest, error) {
	req.RequestID = strings.TrimSpace(req.RequestID)
	if !validUUID(req.RequestID) {
		return req, fmt.Errorf("requestId 必须是合法 UUID")
	}
	if !validModelID(req.Spec.ModelRef.ModelID) {
		return req, fmt.Errorf("modelId 非法: %q", req.Spec.ModelRef.ModelID)
	}
	if err := req.Spec.Validate(); err != nil {
		return req, fmt.Errorf("spec: %w", err)
	}
	if req.Supersedes != "" && !validPortfolioValidationID(req.Supersedes) {
		return req, fmt.Errorf("非法被替换验证 ID: %q", req.Supersedes)
	}
	return req, nil
}

// createPortfolioValidationRequestHash 规范化请求的 SHA-256（幂等判定）：
// 同一 RequestID 相同 hash 幂等返回，不同 hash 视为幂等键复用冲突。Spec
// 先经 Normalized 归一（-0.0 → +0.0、空证据要求归一默认），使语义相同的
// 请求（如浮点 ±0.0 表示差异）得到同一 hash。
func createPortfolioValidationRequestHash(req CreatePortfolioValidationRequest) (string, error) {
	spec, err := req.Spec.Normalized()
	if err != nil {
		return "", err
	}
	req.Spec = spec
	buf, err := json.Marshal(req)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(buf)
	return hex.EncodeToString(sum[:]), nil
}

// PortfolioValidationRecord 冻结的验证主记录（request.json）。Spec 全部字段
// 进入 SpecHash（不可变身份）；ModelEvidenceClass 为后端从冻结模型派生的
// 证据等级（展示与列表过滤用，不进入 spec hash——模型身份已由 hash 引用）。
type PortfolioValidationRecord struct {
	SchemaVersion int                                       `json:"schemaVersion"`
	ID            string                                    `json:"id"`
	CreatedAt     string                                    `json:"createdAt"`
	Spec          portfolioresearch.PortfolioValidationSpec `json:"spec"`
	SpecHash      string                                    `json:"specHash"`
	// ModelEvidenceClass 冻结模型的证据等级（后端派生，不接受客户端自报）。
	ModelEvidenceClass string `json:"modelEvidenceClass"`
	RequestID          string `json:"requestId"`
	RequestHash        string `json:"requestHash"`
	Supersedes         string `json:"supersedes,omitempty"`
}

// PortfolioValidationReport 最终不可变验证报告（report.json，完成标记：
// 存在即终态，一次写入永不覆盖）。Verdict 由后端按冻结 spec 从窗口结果
// 生成（passes/failed/insufficient/error），客户端不能指定。
type PortfolioValidationReport struct {
	SchemaVersion    int                            `json:"schemaVersion"`
	ValidationID     string                         `json:"validationId"`
	FinishedAt       string                         `json:"finishedAt"`
	Verdict          string                         `json:"verdict"`
	EvidenceClass    string                         `json:"evidenceClass"`
	Gates            []portfolioresearch.GateResult `json:"gates"`
	WindowCount      int                            `json:"windowCount"`
	ValidWindowCount int                            `json:"validWindowCount"`
	Message          string                         `json:"message,omitempty"`
}

// validate 报告基本合法性：schema、ID、终态白名单、证据等级、逐项门禁披露。
// 统计内容与 verdict 一致性由读取时的重算校验承担（Get）。
func (r *PortfolioValidationReport) validate() error {
	if r.SchemaVersion != portfolioValidationSchemaVersion {
		return fmt.Errorf("未知组合验证报告 schema 版本: %d（仅支持 %d）", r.SchemaVersion, portfolioValidationSchemaVersion)
	}
	if !validPortfolioValidationID(r.ValidationID) {
		return fmt.Errorf("非法验证 ID: %q", r.ValidationID)
	}
	if r.FinishedAt == "" {
		return fmt.Errorf("finishedAt 不能为空")
	}
	if !portfolioresearch.ValidGateStatus(r.Verdict) {
		return fmt.Errorf("报告结论非法: %q（应为 passed | failed | insufficient | error）", r.Verdict)
	}
	if !portfolioresearch.ValidEvidenceClass(r.EvidenceClass) {
		return fmt.Errorf("报告证据等级非法: %q", r.EvidenceClass)
	}
	if len(r.Gates) == 0 {
		return fmt.Errorf("gates 不能为空（逐项门禁必须披露）")
	}
	for i, g := range r.Gates {
		switch g.Result {
		case "pass", "fail", "unknown":
		default:
			return fmt.Errorf("gates[%d].result 非法: %q", i, g.Result)
		}
	}
	return nil
}

// PortfolioValidationView 验证详情：冻结记录 + 派生状态 + 窗口结果 + 最终
// 报告。SupersededBy 由其他记录的 Supersedes 派生，不改写本记录。
type PortfolioValidationView struct {
	Record       PortfolioValidationRecord                   `json:"record"`
	State        string                                      `json:"state"` // created | completed
	Windows      []portfolioresearch.ValidationWindowOutcome `json:"windows,omitempty"`
	Report       *PortfolioValidationReport                  `json:"report,omitempty"`
	SupersededBy []string                                    `json:"supersededBy,omitempty"`
}

// PortfolioValidationSummary 验证列表摘要（不含窗口与报告详情）。
type PortfolioValidationSummary struct {
	ID            string                       `json:"id"`
	CreatedAt     string                       `json:"createdAt"`
	State         string                       `json:"state"` // created | completed
	Verdict       string                       `json:"verdict,omitempty"`
	EvidenceClass string                       `json:"evidenceClass"`
	ModelID       string                       `json:"modelId"`
	ModelRevision int                          `json:"modelRevision"`
	ModelHash     string                       `json:"modelHash"`
	WindowRule    portfolioresearch.WindowRule `json:"windowRule"`
	Supersedes    string                       `json:"supersedes,omitempty"`
	// SupersededBy 替换本记录的验证 ID 列表（升序；旧测试窗视为已见数据）。
	SupersededBy []string `json:"supersededBy,omitempty"`
}

// PortfolioValidationFilter 列表筛选（全部字段可选；白名单枚举，未知值拒绝）。
type PortfolioValidationFilter struct {
	ModelID       string
	Verdict       string
	EvidenceClass string
}

func (f PortfolioValidationFilter) validate() error {
	if f.ModelID != "" && !validModelID(f.ModelID) {
		return fmt.Errorf("筛选 modelId 非法: %q", f.ModelID)
	}
	if f.Verdict != "" && !portfolioresearch.ValidGateStatus(f.Verdict) {
		return fmt.Errorf("筛选 verdict 非法: %q", f.Verdict)
	}
	if f.EvidenceClass != "" && !portfolioresearch.ValidEvidenceClass(f.EvidenceClass) {
		return fmt.Errorf("筛选 evidenceClass 非法: %q", f.EvidenceClass)
	}
	return nil
}

// PortfolioValidationPage 服务端分页结果：稳定排序（CreatedAt 倒序 + ID
// 升序兜底），页码可恢复（设计 §12.2）。
type PortfolioValidationPage struct {
	Items    []PortfolioValidationSummary `json:"items"`
	Total    int                          `json:"total"`
	Page     int                          `json:"page"`
	PageSize int                          `json:"pageSize"`
}
