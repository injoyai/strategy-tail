package lab

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/injoyai/strategy-tail/internal/researchrun"
	f "github.com/injoyai/strategy-tail/strategies/factor"
)

// factor_candidate.go 候选因子方案领域模型与纯校验（设计文档 §5）。
//
// 职责边界：FactorRef 与 CandidateEvidence 只能由分析报告推导，客户端不能
// 覆写；候选不序列化 core.Factor 接口实例，只引用注册因子。
// 首版状态只有 candidate/archived；validated 留待独立样本外验证能力落地。

// CandidateStatus 候选状态：首版只有 candidate 与 archived。
type CandidateStatus string

const (
	CandidateStatusCandidate CandidateStatus = "candidate"
	CandidateStatusArchived  CandidateStatus = "archived"
)

// FactorRef 注册因子引用（由报告快照推导，不含算法实现）。
type FactorRef struct {
	Kind                  string `json:"kind"`
	Days                  int    `json:"days"`
	Name                  string `json:"name"`
	Unit                  string `json:"unit"`
	ImplementationVersion int    `json:"implementationVersion"`
}

// CandidateUse 使用方式：observe=仅保存观察结果；range=配置固定区间复用。
type CandidateUse struct {
	Mode   string            `json:"mode"` // observe | range
	Filter *FactorFilterSpec `json:"filter,omitempty"`
}

// CandidateEvidence 不可变分析证据摘要。完整报告以单独 *.analysis.json
// 文件保存并校验 SHA-256；此处字段用于摘要展示与读取一致性校验。
type CandidateEvidence struct {
	AnalysisID      string                   `json:"analysisId"`
	AnalysisVersion int                      `json:"analysisVersion"`
	ReportSHA256    string                   `json:"reportSha256"`
	Window          int                      `json:"window"`
	Range           AnalysisRange            `json:"range"`
	Grouping        GroupingConfig           `json:"grouping"`
	Stats           ICStats                  `json:"stats"`
	Summary         QuintileSummary          `json:"summary"`
	Coverage        researchrun.Coverage     `json:"coverage"`
	YearCoverage    researchrun.YearCoverage `json:"yearCoverage"`
	FirstDataDate   string                   `json:"firstDataDate"`
	LastDataDate    string                   `json:"lastDataDate"`
	FinishedAt      string                   `json:"finishedAt"`
}

// FactorCandidate 候选因子方案（最新修订；历史修订按追加文件保留）。
type FactorCandidate struct {
	SchemaVersion     int               `json:"schemaVersion"`
	ID                string            `json:"id"`
	Revision          int               `json:"revision"`
	Name              string            `json:"name"`
	Status            CandidateStatus   `json:"status"`
	Factor            FactorRef         `json:"factor"`
	Use               CandidateUse      `json:"use"`
	Evidence          CandidateEvidence `json:"evidence"`
	Notes             string            `json:"notes,omitempty"`
	CreatedAt         string            `json:"createdAt"`
	UpdatedAt         string            `json:"updatedAt"`
	CreateRequestID   string            `json:"createRequestId"`
	CreateRequestHash string            `json:"createRequestHash"`
}

// CandidateCompatibility 派生兼容状态（API 返回时实时计算，不回写历史 JSON）。
type CandidateCompatibility struct {
	State          string `json:"state"` // ready | stale | missing
	CurrentVersion int    `json:"currentVersion"`
	Reason         string `json:"reason,omitempty"`
}

// CreateCandidateRequest 创建候选请求。
type CreateCandidateRequest struct {
	RequestID  string       `json:"requestId"`
	AnalysisID string       `json:"analysisId"`
	Name       string       `json:"name"`
	Use        CandidateUse `json:"use"`
	Notes      string       `json:"notes,omitempty"`
}

// UpdateCandidateRequest 追加修订请求（必须携带 expectedRevision）。
type UpdateCandidateRequest struct {
	ExpectedRevision int             `json:"expectedRevision"`
	Name             string          `json:"name"`
	Use              CandidateUse    `json:"use"`
	Status           CandidateStatus `json:"status"`
	Notes            string          `json:"notes,omitempty"`
}

// uuidRe 幂等键 UUID 格式（一次保存动作的所有重试必须复用同一值）。
var uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// validUUID 校验合法 UUID。
func validUUID(s string) bool { return uuidRe.MatchString(s) }

// normalizeCreateCandidateRequest 创建请求的纯校验与规范化：
// requestId 必须为合法 UUID；名称 trim 后 1～80 Unicode 字符；备注 ≤2000；
// observe 不得携带 Filter；range 必须携带 Filter 并通过 FactorFilterSpec 校验。
func normalizeCreateCandidateRequest(req CreateCandidateRequest) (CreateCandidateRequest, error) {
	req.RequestID = strings.TrimSpace(req.RequestID)
	req.Name = strings.TrimSpace(req.Name)
	req.Notes = strings.TrimSpace(req.Notes)
	if !validUUID(req.RequestID) {
		return req, fmt.Errorf("requestId 必须是合法 UUID")
	}
	if n := utf8.RuneCountInString(req.Name); n < 1 || n > 80 {
		return req, fmt.Errorf("候选名称需要 1～80 个字符")
	}
	if utf8.RuneCountInString(req.Notes) > 2000 {
		return req, fmt.Errorf("备注过长（上限 2000 字符）")
	}
	if err := validateCandidateUse(req.Use); err != nil {
		return req, err
	}
	return req, nil
}

// validateCandidateUse 使用方式校验：observe 不得带 Filter；range 必须带
// Filter 且通过因子过滤合同校验（含版本 fail-closed）。
func validateCandidateUse(use CandidateUse) error {
	switch use.Mode {
	case "observe":
		if use.Filter != nil {
			return fmt.Errorf("仅保存观察模式不得携带区间过滤")
		}
		return nil
	case "range":
		if use.Filter == nil {
			return fmt.Errorf("区间模式必须提供 filter")
		}
		return use.Filter.validate()
	default:
		return fmt.Errorf("使用方式无效: %s（应为 observe/range）", use.Mode)
	}
}

// candidateFromAnalysis 由分析报告推导 FactorRef 与 CandidateEvidence：
// 仅接受 v3 报告（analysisVersion>=3 且含合法 analysisId 与正实现版本）；
// range 模式额外断言 kind/days/version 与报告快照完全一致。
func candidateFromAnalysis(req CreateCandidateRequest, rep *AnalysisReport, now time.Time) (FactorCandidate, error) {
	if rep == nil {
		return FactorCandidate{}, fmt.Errorf("分析报告为空")
	}
	if rep.AnalysisVersion < 3 || !validAnalysisID(rep.AnalysisID) || rep.Factor.ImplementationVersion <= 0 {
		return FactorCandidate{}, fmt.Errorf("仅 v3 分析报告可保存为候选，请重新运行分析")
	}
	if req.Use.Mode == "range" {
		if req.Use.Filter.Kind != rep.Kind ||
			req.Use.Filter.Days != rep.Factor.Days ||
			req.Use.Filter.FactorVersion != rep.Factor.ImplementationVersion {
			return FactorCandidate{}, fmt.Errorf("过滤配置与报告快照不一致（kind/days/version 必须与报告完全一致）")
		}
	}
	reportBuf, err := json.Marshal(rep)
	if err != nil {
		return FactorCandidate{}, err
	}
	sum := sha256.Sum256(reportBuf)
	nowStr := now.Format(time.RFC3339)
	return FactorCandidate{
		SchemaVersion: 1,
		Name:          req.Name,
		Status:        CandidateStatusCandidate,
		Factor: FactorRef{
			Kind:                  rep.Kind,
			Days:                  rep.Factor.Days,
			Name:                  rep.Factor.Name,
			Unit:                  rep.Factor.Unit,
			ImplementationVersion: rep.Factor.ImplementationVersion,
		},
		Use: req.Use,
		Evidence: CandidateEvidence{
			AnalysisID:      rep.AnalysisID,
			AnalysisVersion: rep.AnalysisVersion,
			ReportSHA256:    hex.EncodeToString(sum[:]),
			Window:          rep.Window,
			Range:           rep.Range,
			Grouping:        rep.Grouping,
			Stats:           rep.Stats,
			Summary:         rep.Summary,
			Coverage:        rep.Coverage,
			YearCoverage:    rep.YearCoverage,
			FirstDataDate:   rep.FirstDataDate,
			LastDataDate:    rep.LastDataDate,
			FinishedAt:      rep.FinishedAt,
		},
		CreatedAt: nowStr,
		UpdatedAt: nowStr,
	}, nil
}

// compatibilityOf 由当前注册表实时计算候选兼容状态（不回写历史）。
func compatibilityOf(c FactorCandidate) CandidateCompatibility {
	entry, ok := f.Catalog(c.Factor.Kind)
	if !ok {
		return CandidateCompatibility{State: "missing", CurrentVersion: 0,
			Reason: "注册表已无此因子类型"}
	}
	if entry.ImplementationVersion != c.Factor.ImplementationVersion {
		return CandidateCompatibility{State: "stale", CurrentVersion: entry.ImplementationVersion,
			Reason: fmt.Sprintf("因子实现版本已从 %d 变为 %d，请重新分析",
				c.Factor.ImplementationVersion, entry.ImplementationVersion)}
	}
	return CandidateCompatibility{State: "ready", CurrentVersion: entry.ImplementationVersion}
}

// candidateRequestHash 对规范化创建请求计算 SHA-256：
// analysisId + name + use + notes。同一 requestId 与相同 hash 幂等，
// 与不同 hash 视为幂等键复用冲突。
func candidateRequestHash(req CreateCandidateRequest) (string, error) {
	canon := struct {
		AnalysisID string       `json:"analysisId"`
		Name       string       `json:"name"`
		Use        CandidateUse `json:"use"`
		Notes      string       `json:"notes"`
	}{req.AnalysisID, req.Name, req.Use, req.Notes}
	buf, err := json.Marshal(canon)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(buf)
	return hex.EncodeToString(sum[:]), nil
}
