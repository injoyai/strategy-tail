package lab

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
)

// 研究协议（ResearchProtocol）：v4 因子分析的研究合同。协议随报告冻结保存，
// 改变预期方向、因子窗口、收益标签或股票池都构成新的研究变体。本文件只承载
// 协议结构、校验、规范化、hash 与证据等级派生等纯函数；执行编排见 Task 5。

// protocolSchemaVersion 研究协议 schema 版本；当前唯一已知版本。
const protocolSchemaVersion = 1

// 枚举白名单：未知值一律拒绝，Normalize 不得静默升级为更优值。
const (
	// HypothesisSpec.ExpectedDirection
	labelDirectionPositive = "positive"
	labelDirectionNegative = "negative"
	labelDirectionTwoSided = "two_sided"

	// UniverseSpec.Mode
	universeCurrentStatic        = "current_static"        // 当前静态股票池：证据等级上限 exploratory
	universeHistoricalMembership = "historical_membership" // 历史成员关系
	universeCodes                = "codes"                 // 指定代码个案研究

	// UniverseSpec.MembershipPIT
	membershipPITVerified   = "verified"
	membershipPITUnverified = "unverified"

	// DataProvenance.Adjustment
	adjustmentNone     = "none"
	adjustmentForward  = "forward"
	adjustmentBackward = "backward"
	adjustmentUnknown  = "unknown" // 未知口径：证据等级上限 exploratory

	// DataProvenance.PITState
	pitVerified   = "verified"
	pitPartial    = "partial"
	pitUnverified = "unverified"

	// SignalSpec
	signalFrequencyDaily = "daily" // v1 仅支持日线
	signalFormedAtClose  = "close" // v1 仅支持收盘形成信号

	// LabelSpec.Kind
	labelKindNextOpenToClose        = "next_open_to_close"         // 次日开盘进、第 h 日收盘出
	labelKindSameCloseToCloseLegacy = "same_close_to_close_legacy" // 旧同收盘标签：上限 exploratory

	// DiagnosticSpec.HACLagMode
	hacLagHorizonMinusOne = "horizon_minus_one" // 默认：lag = h-1（多日重叠标签）
	hacLagFixed           = "fixed"             // 固定 lag，0~60

	// NeutralizationSpec.Mode
	neutralizationNone         = "none"
	neutralizationIndustrySize = "industry_size"

	// NeutralizationSpec.Winsorization
	winsorizationNone = "none"
	winsorizationMAD  = "mad"

	// NeutralizationSpec.Standardization
	standardizationRank   = "rank"
	standardizationZScore = "zscore"
)

// 证据等级（分析报告层）：prospective 仅属于验证层（冻结后新数据），不在
// 本函数值域内。
const (
	evidenceExploratory   = "exploratory"   // 存在关键限制（静态池/旧标签/未验证数据）
	evidenceRetrospective = "retrospective" // 协议完整且数据可验证的历史回放
)

// protocolIDRe 受限 ID：小写字母或数字开头，仅小写字母、数字、下划线、连字符，
// 最长 64；天然拒绝路径分隔符、点、空格、大写与非 ASCII。
var protocolIDRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

// ResearchProtocol 研究协议：假设、股票池、数据来源、信号、标签、诊断与试验
// 归属的完整声明。分析保存后不可修改。
type ResearchProtocol struct {
	SchemaVersion int            `json:"schemaVersion"`
	Hypothesis    HypothesisSpec `json:"hypothesis"`
	Universe      UniverseSpec   `json:"universe"`
	Data          DataProvenance `json:"data"`
	Signal        SignalSpec     `json:"signal"`
	Labels        LabelSpec      `json:"labels"`
	Diagnostics   DiagnosticSpec `json:"diagnostics"`
	Trial         TrialSpec      `json:"trial"`
}

// HypothesisSpec 研究假设：不是展示备注；保存后不可修改。
type HypothesisSpec struct {
	ID                string `json:"id"`
	Thesis            string `json:"thesis"`
	ExpectedDirection string `json:"expectedDirection"`
	FailureCondition  string `json:"failureCondition"`
}

// UniverseSpec 股票池声明。current_static 与现有 all 行为兼容但存在生存者
// 偏差；historical_membership 按交易日成员关系回放并可包含已退市代码。
type UniverseSpec struct {
	Mode              string `json:"mode"`
	ID                string `json:"id,omitempty"`
	Version           string `json:"version,omitempty"`
	IncludeDelisted   bool   `json:"includeDelisted"`
	MembershipPIT     string `json:"membershipPit"`
	TradabilityPolicy string `json:"tradabilityPolicy,omitempty"`
}

// DataProvenance 数据来源与质量声明；决定证据等级上限。
type DataProvenance struct {
	PriceSource      string `json:"priceSource"`
	PriceVersion     string `json:"priceVersion"`
	Adjustment       string `json:"adjustment"`
	ResearchDataView string `json:"researchDataView"`
	PITState         string `json:"pitState"`
	SnapshotAt       string `json:"snapshotAt,omitempty"`
}

// SignalSpec 信号形成声明：v1 仅 daily + close。
type SignalSpec struct {
	Frequency string `json:"frequency"`
	FormedAt  string `json:"formedAt"`
}

// LabelSpec 收益标签合同。
type LabelSpec struct {
	Kind     string `json:"kind"`
	Horizons []int  `json:"horizons"`
}

// DiagnosticSpec 诊断配置：分组复用现有 GroupingConfig；HAC lag 服务于多日
// 重叠标签的 Newey-West 修正。
type DiagnosticSpec struct {
	Grouping        GroupingConfig     `json:"grouping,omitempty"`
	HACLagMode      string             `json:"hacLagMode"`
	HACLag          int                `json:"hacLag,omitempty"`
	TurnoverPeriods []int              `json:"turnoverPeriods,omitempty"`
	Neutralization  NeutralizationSpec `json:"neutralization"`
}

// NeutralizationSpec 可选中性化；industry_size 需要行业与市值数据集标识。
type NeutralizationSpec struct {
	Mode            string `json:"mode"`
	IndustryDataset string `json:"industryDataset,omitempty"`
	SizeDataset     string `json:"sizeDataset,omitempty"`
	Winsorization   string `json:"winsorization"`
	Standardization string `json:"standardization"`
}

// TrialSpec 试验账本归属：家族与变体用于后续多重检验校正。
type TrialSpec struct {
	FamilyID  string `json:"familyId"`
	VariantID string `json:"variantId"`
	Rationale string `json:"rationale"`
}

// Validate 校验协议：schema 版本、受限 ID、枚举白名单、Horizons 合同与 HAC
// lag 范围。任何未知枚举值都报错，不做静默升级。
func (p ResearchProtocol) Validate() error {
	if p.SchemaVersion != protocolSchemaVersion {
		return fmt.Errorf("未知研究协议 schema 版本: %d（仅支持 %d）", p.SchemaVersion, protocolSchemaVersion)
	}
	if !protocolIDRe.MatchString(p.Hypothesis.ID) {
		return fmt.Errorf("假设 ID 非法: %q（限小写字母/数字开头，可含 - _，最长 64）", p.Hypothesis.ID)
	}
	if p.Hypothesis.Thesis == "" {
		return fmt.Errorf("假设论点不能为空")
	}
	switch p.Hypothesis.ExpectedDirection {
	case labelDirectionPositive, labelDirectionNegative, labelDirectionTwoSided:
	default:
		return fmt.Errorf("预期方向非法: %q（positive | negative | two_sided）", p.Hypothesis.ExpectedDirection)
	}
	if p.Hypothesis.FailureCondition == "" {
		return fmt.Errorf("假设失败条件不能为空")
	}

	switch p.Universe.Mode {
	case universeCurrentStatic, universeHistoricalMembership, universeCodes:
	default:
		return fmt.Errorf("股票池模式非法: %q", p.Universe.Mode)
	}
	if p.Universe.Mode == universeHistoricalMembership && p.Universe.ID == "" {
		return fmt.Errorf("历史成员股票池必须声明来源 ID")
	}
	switch p.Universe.MembershipPIT {
	case membershipPITVerified, membershipPITUnverified:
	default:
		return fmt.Errorf("成员关系 PIT 非法: %q（verified | unverified）", p.Universe.MembershipPIT)
	}

	switch p.Data.Adjustment {
	case adjustmentNone, adjustmentForward, adjustmentBackward, adjustmentUnknown:
	default:
		return fmt.Errorf("复权口径非法: %q", p.Data.Adjustment)
	}
	switch p.Data.PITState {
	case pitVerified, pitPartial, pitUnverified:
	default:
		return fmt.Errorf("数据 PIT 状态非法: %q", p.Data.PITState)
	}
	for field, v := range map[string]string{
		"priceSource":      p.Data.PriceSource,
		"priceVersion":     p.Data.PriceVersion,
		"researchDataView": p.Data.ResearchDataView,
	} {
		if v == "" {
			return fmt.Errorf("数据来源 %s 不能为空", field)
		}
	}

	if p.Signal.Frequency != signalFrequencyDaily {
		return fmt.Errorf("信号频率非法: %q（v1 仅支持 daily）", p.Signal.Frequency)
	}
	if p.Signal.FormedAt != signalFormedAtClose {
		return fmt.Errorf("信号形成时点非法: %q（v1 仅支持 close）", p.Signal.FormedAt)
	}

	switch p.Labels.Kind {
	case labelKindNextOpenToClose, labelKindSameCloseToCloseLegacy:
	default:
		return fmt.Errorf("标签类型非法: %q", p.Labels.Kind)
	}
	if len(p.Labels.Horizons) == 0 {
		return fmt.Errorf("horizons 不能为空（至少一个周期）")
	}
	if err := validateHorizons("horizons", p.Labels.Horizons); err != nil {
		return err
	}

	if err := validateHorizons("turnoverPeriods", p.Diagnostics.TurnoverPeriods); err != nil {
		return err
	}
	switch p.Diagnostics.HACLagMode {
	case hacLagHorizonMinusOne:
		if p.Diagnostics.HACLag != 0 {
			return fmt.Errorf("hacLagMode=horizon_minus_one 不接受固定 lag: %d", p.Diagnostics.HACLag)
		}
	case hacLagFixed:
		if p.Diagnostics.HACLag < 0 || p.Diagnostics.HACLag > 60 {
			return fmt.Errorf("固定 HAC lag 无效: %d（应为 0-60）", p.Diagnostics.HACLag)
		}
	default:
		return fmt.Errorf("HAC lag 模式非法: %q", p.Diagnostics.HACLagMode)
	}
	if err := p.Diagnostics.Grouping.Validate(); err != nil {
		return fmt.Errorf("分组配置非法: %w", err)
	}

	switch p.Diagnostics.Neutralization.Mode {
	case neutralizationNone:
	case neutralizationIndustrySize:
		if p.Diagnostics.Neutralization.IndustryDataset == "" || p.Diagnostics.Neutralization.SizeDataset == "" {
			return fmt.Errorf("industry_size 中性化必须声明行业与市值数据集")
		}
	default:
		return fmt.Errorf("中性化模式非法: %q", p.Diagnostics.Neutralization.Mode)
	}
	switch p.Diagnostics.Neutralization.Winsorization {
	case winsorizationNone, winsorizationMAD:
	default:
		return fmt.Errorf("去极值方式非法: %q", p.Diagnostics.Neutralization.Winsorization)
	}
	switch p.Diagnostics.Neutralization.Standardization {
	case standardizationRank, standardizationZScore:
	default:
		return fmt.Errorf("标准化方式非法: %q", p.Diagnostics.Neutralization.Standardization)
	}

	if !protocolIDRe.MatchString(p.Trial.FamilyID) {
		return fmt.Errorf("试验家族 ID 非法: %q", p.Trial.FamilyID)
	}
	if !protocolIDRe.MatchString(p.Trial.VariantID) {
		return fmt.Errorf("试验变体 ID 非法: %q", p.Trial.VariantID)
	}
	if p.Trial.Rationale == "" {
		return fmt.Errorf("试验理由不能为空")
	}
	return nil
}

// validateHorizons 周期列表合同（Horizons 与 TurnoverPeriods 共用）：允许空
// （TurnoverPeriods 缺省不计算；Horizons 由调用方要求非空），非空时去重、
// 严格升序、每项 1~60、最多 8 个。上限是资源保护，不是金融规律。
func validateHorizons(name string, vs []int) error {
	if len(vs) == 0 {
		return nil
	}
	if len(vs) > 8 {
		return fmt.Errorf("%s 数量超限: %d（最多 8 个）", name, len(vs))
	}
	seen := make(map[int]bool, len(vs))
	for i, h := range vs {
		if h < 1 || h > 60 {
			return fmt.Errorf("%s[%d] 超出范围: %d（应为 1-60）", name, i, h)
		}
		if seen[h] {
			return fmt.Errorf("%s 存在重复: %d", name, h)
		}
		seen[h] = true
		if i > 0 && h <= vs[i-1] {
			return fmt.Errorf("%s 必须严格升序: %v", name, vs)
		}
	}
	return nil
}

// Normalize 返回校验后的协议副本（slice 深拷贝，避免共享底层数组）。
// Normalize 只做拷贝与校验，绝不把未知枚举升级为 verified 等更优值。
func (p ResearchProtocol) Normalize() (ResearchProtocol, error) {
	if err := p.Validate(); err != nil {
		return ResearchProtocol{}, err
	}
	// Horizons 为必填；TurnoverPeriods 允许空，空时归一为 nil。
	out := p
	out.Labels.Horizons = append([]int(nil), p.Labels.Horizons...)
	if len(p.Diagnostics.TurnoverPeriods) == 0 {
		out.Diagnostics.TurnoverPeriods = nil
	} else {
		out.Diagnostics.TurnoverPeriods = append([]int(nil), p.Diagnostics.TurnoverPeriods...)
	}
	return out, nil
}

// protocolHash 对协议计算 SHA-256（64 位十六进制）。结构体字段顺序固定，
// JSON 编码确定性成立；保存的 v4 报告用该 hash 绑定协议内容。
func protocolHash(p ResearchProtocol) (string, error) {
	buf, err := json.Marshal(p)
	if err != nil {
		return "", fmt.Errorf("协议序列化失败: %w", err)
	}
	sum := sha256.Sum256(buf)
	return hex.EncodeToString(sum[:]), nil
}

// deriveEvidenceClass 从协议派生分析报告层证据等级。以下任一关键限制存在时
// 上限为 exploratory：当前静态股票池（生存者偏差）、旧同收盘标签（收盘信号
// 与收盘成交冲突）、数据 PIT 未完全验证、复权口径未知。协议完整且数据可验证
// 时为 retrospective（历史回放）；prospective 只属于验证层。
func deriveEvidenceClass(p ResearchProtocol) string {
	if p.Universe.Mode == universeCurrentStatic ||
		p.Labels.Kind == labelKindSameCloseToCloseLegacy ||
		p.Data.PITState != pitVerified ||
		p.Data.Adjustment == adjustmentUnknown {
		return evidenceExploratory
	}
	return evidenceRetrospective
}
