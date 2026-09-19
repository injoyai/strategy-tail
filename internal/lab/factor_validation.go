package lab

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"regexp"
	"sort"
	"strings"
	"time"
)

// factor_validation.go 冻结验证领域层（设计 §11，计划 Task 7 Step 1/2）。
//
// 职责边界：本文件只承载类型、状态机纯函数与 freezeValidation 冻结函数；
// 存储见 factor_validation_store.go，滚动引擎见 Task 8。冻结记录不可编辑：
// 发现错误只能创建新验证并可标记 supersededBy（通过新记录的 Supersedes 派生，
// 不改写旧 request.json——设计 §12.1 要求请求文件先发布且永不修改）。

// validationSchemaVersion 验证协议 schema 版本。
const validationSchemaVersion = 1

// 验证层证据等级：只允许 retrospective | prospective（exploratory 是分析层
// 概念，validation 必须明确声明是历史回放还是冻结后前瞻）。
const (
	validationEvidenceRetrospective = "retrospective"
	validationEvidenceProspective   = "prospective"
)

// ValidationState 验证状态机（计划 Task 7 Step 1）：
//
//	frozen → running → passed | failed | insufficient | error
//
// 完成态不可重新运行；重试创建新 Validation ID。running 属于 Runner 内存
// 进度（设计 §12.1），存储层派生状态只区分 frozen 与完成态。
type ValidationState string

const (
	ValidationStateFrozen       ValidationState = "frozen"
	ValidationStateRunning      ValidationState = "running"
	ValidationStatePassed       ValidationState = "passed"
	ValidationStateFailed       ValidationState = "failed"
	ValidationStateInsufficient ValidationState = "insufficient"
	ValidationStateError        ValidationState = "error"
)

// finishedValidationStates 终态白名单：进入后不可变更（追加式，报告文件
// 只写一次）。running 不是终态。
var finishedValidationStates = map[ValidationState]bool{
	ValidationStatePassed:       true,
	ValidationStateFailed:       true,
	ValidationStateInsufficient: true,
	ValidationStateError:        true,
}

// ValidationVerdict 机器门禁结论（Task 8 evaluateValidation 产出，与终态
// 一一对应）。insufficient 表示数据/窗口不足或质量未知，不得当作 failed。
type ValidationVerdict string

const (
	ValidationVerdictPassed       ValidationVerdict = "passed"
	ValidationVerdictFailed       ValidationVerdict = "failed"
	ValidationVerdictInsufficient ValidationVerdict = "insufficient"
	ValidationVerdictError        ValidationVerdict = "error"
)

// validationTransition 状态机纯函数：仅允许 frozen → running 与
// running → 终态。完成态（或非法来源）到任何状态都拒绝。
func validationTransition(from, to ValidationState) error {
	switch {
	case from == ValidationStateFrozen && to == ValidationStateRunning:
		return nil
	case from == ValidationStateRunning && finishedValidationStates[to]:
		return nil
	default:
		return fmt.Errorf("非法验证状态转换: %s → %s", from, to)
	}
}

// validationIDRe 受限验证 ID：fv_<UTC yyyyMMddTHHmmssSSSZ>_<8 hex>。
var validationIDRe = regexp.MustCompile(`^fv_\d{8}T\d{9}Z_[0-9a-f]{8}$`)

// newValidationID 生成不可变验证 ID。
func newValidationID(now time.Time, random io.Reader) (string, error) {
	return newPrefixedID("fv", now, random)
}

// validValidationID 严格校验验证 ID 格式（同时拒绝路径穿越）。
func validValidationID(id string) bool { return validationIDRe.MatchString(id) }

// WalkForwardSpec 滚动窗口规格（设计 §11.1）。年份只是资源边界，不是金融
// 规律；PurgeDays 必须覆盖最大收益周期（freezeValidation 校验）。
type WalkForwardSpec struct {
	TrainYears int `json:"trainYears"`
	TestYears  int `json:"testYears"`
	StepYears  int `json:"stepYears"`
	PurgeDays  int `json:"purgeDays"`
}

// validate 窗口规格合法性：训练/测试/步长 1~10 年（资源保护上限），
// PurgeDays 0~60（与标签周期同界）；purgeDays >= max Horizon 由
// freezeValidation 结合发现期协议校验。
func (w WalkForwardSpec) validate() error {
	for name, v := range map[string]int{
		"trainYears": w.TrainYears,
		"testYears":  w.TestYears,
		"stepYears":  w.StepYears,
	} {
		if v < 1 || v > 10 {
			return fmt.Errorf("%s 无效: %d（应为 1-10 年）", name, v)
		}
	}
	if w.PurgeDays < 0 || w.PurgeDays > 60 {
		return fmt.Errorf("purgeDays 无效: %d（应为 0-60）", w.PurgeDays)
	}
	return nil
}

// 聚合口径白名单：机器门禁使用哪种聚合必须冻结进 policy（计划 Task 8
// Step 4），不允许评估完成后切换最有利口径。
const (
	validationAggregationWindowEqual      = "window_equal_weight"  // 按窗口等权（默认）
	validationAggregationObservationEqual = "observation_weighted" // 按观察日加权
)

// ValidationPolicy 机器门禁（设计 §11.3）。所有阈值冻结前可见、可编辑并
// 进入 hash；不提供"IC > 阈值即专业因子"之类的通用门槛。
type ValidationPolicy struct {
	MinCoverage           float64  `json:"minCoverage"`
	MinValidWindows       int      `json:"minValidWindows"`
	RequiredDirectionRate float64  `json:"requiredDirectionRate"`
	MaxICDecayRatio       *float64 `json:"maxIcDecayRatio,omitempty"`
	// Aggregation 机器门禁聚合口径：window_equal_weight（默认）|
	// observation_weighted。冻结后不可切换。
	Aggregation string `json:"aggregation,omitempty"`
	// RequireHistoricalUniverse 要求发现与验证使用历史成员股票池（拒绝
	// current_static 的生存者偏差样本）。
	RequireHistoricalUniverse bool `json:"requireHistoricalUniverse"`
	// RequireVerifiedPIT 要求数据 PIT 状态 verified。
	RequireVerifiedPIT bool `json:"requireVerifiedPit"`
	// RequireTradableLabel 要求次日开盘进收盘出的可交易标签（拒绝 legacy
	// 同收盘标签）。
	RequireTradableLabel bool `json:"requireTradableLabel"`
}

// normalizeValidationPolicy 策略规范化：空聚合口径归一为窗口等权默认值
// （透明默认模板，设计 §11.3）。
func normalizeValidationPolicy(p ValidationPolicy) ValidationPolicy {
	if p.Aggregation == "" {
		p.Aggregation = validationAggregationWindowEqual
	}
	return p
}

// validate 策略数值有限且范围合理（freezeValidation 第 8 项校验）。
func (p ValidationPolicy) validate() error {
	if math.IsNaN(p.MinCoverage) || p.MinCoverage < 0 || p.MinCoverage > 1 {
		return fmt.Errorf("minCoverage 无效: %v（应为 [0,1]）", p.MinCoverage)
	}
	if math.IsNaN(p.RequiredDirectionRate) || p.RequiredDirectionRate < 0 || p.RequiredDirectionRate > 1 {
		return fmt.Errorf("requiredDirectionRate 无效: %v（应为 [0,1]）", p.RequiredDirectionRate)
	}
	if p.MinValidWindows < 1 || p.MinValidWindows > 1024 {
		return fmt.Errorf("minValidWindows 无效: %d（应为 1-1024）", p.MinValidWindows)
	}
	if p.MaxICDecayRatio != nil {
		r := *p.MaxICDecayRatio
		if math.IsNaN(r) || r <= 0 || r > 100 {
			return fmt.Errorf("maxIcDecayRatio 无效: %v（应为 (0,100]）", r)
		}
	}
	switch p.Aggregation {
	case validationAggregationWindowEqual, validationAggregationObservationEqual:
	default:
		return fmt.Errorf("聚合口径非法: %q（应为 %s | %s）",
			p.Aggregation, validationAggregationWindowEqual, validationAggregationObservationEqual)
	}
	return nil
}

// ValidationProtocol 冻结的验证协议（设计 §11.1）：候选 revision、发现期
// 分析与 trial 清单、滚动窗口与验收政策的完整声明，冻结后不可修改。
type ValidationProtocol struct {
	SchemaVersion        int              `json:"schemaVersion"`
	EvidenceClass        string           `json:"evidenceClass"` // retrospective | prospective
	CandidateID          string           `json:"candidateId"`
	CandidateRevision    int              `json:"candidateRevision"`
	DiscoveryAnalysisIDs []string         `json:"discoveryAnalysisIds"`
	TrialIDs             []string         `json:"trialIds"`
	Windows              WalkForwardSpec  `json:"windows"`
	Policy               ValidationPolicy `json:"policy"`
}

// Validate 验证协议合法性：schema 版本、受限 ID、清单非空、窗口与政策
// 数值合理。未知枚举一律拒绝。
func (p ValidationProtocol) Validate() error {
	if p.SchemaVersion != validationSchemaVersion {
		return fmt.Errorf("未知验证协议 schema 版本: %d（仅支持 %d）", p.SchemaVersion, validationSchemaVersion)
	}
	switch p.EvidenceClass {
	case validationEvidenceRetrospective, validationEvidenceProspective:
	default:
		return fmt.Errorf("验证证据等级非法: %q（应为 retrospective | prospective）", p.EvidenceClass)
	}
	if !validCandidateID(p.CandidateID) {
		return fmt.Errorf("非法候选 ID: %q", p.CandidateID)
	}
	if p.CandidateRevision < 1 {
		return fmt.Errorf("candidateRevision 无效: %d", p.CandidateRevision)
	}
	if len(p.DiscoveryAnalysisIDs) == 0 {
		return fmt.Errorf("discoveryAnalysisIds 不能为空")
	}
	for i, id := range p.DiscoveryAnalysisIDs {
		if !validAnalysisID(id) {
			return fmt.Errorf("discoveryAnalysisIds[%d] 非法: %q", i, id)
		}
	}
	if len(p.TrialIDs) == 0 {
		return fmt.Errorf("trialIds 不能为空（trial 清单必须完整）")
	}
	for i, id := range p.TrialIDs {
		if !validTrialID(id) {
			return fmt.Errorf("trialIds[%d] 非法: %q", i, id)
		}
	}
	if err := p.Windows.validate(); err != nil {
		return err
	}
	if err := p.Policy.validate(); err != nil {
		return err
	}
	return nil
}

// CreateValidationRequest 创建验证请求（API 层输入）。RequestID 为幂等键：
// 同 ID 同内容幂等返回，同 ID 不同内容冲突。
type CreateValidationRequest struct {
	RequestID            string           `json:"requestId"`
	CandidateID          string           `json:"candidateId"`
	CandidateRevision    int              `json:"candidateRevision"`
	EvidenceClass        string           `json:"evidenceClass"` // retrospective | prospective
	DiscoveryAnalysisIDs []string         `json:"discoveryAnalysisIds"`
	TrialIDs             []string         `json:"trialIds"`
	Windows              WalkForwardSpec  `json:"windows"`
	Policy               ValidationPolicy `json:"policy"`
	// Supersedes 被替换的旧验证 ID（可选）：旧记录保留并由此派生
	// supersededBy 标记，不删除、不改写。
	Supersedes string `json:"supersedes,omitempty"`
}

// normalizeCreateValidationRequest 请求纯校验与规范化：UUID 幂等键、受限
// ID、清单去重、窗口与政策合法（聚合口径归一默认值）。
func normalizeCreateValidationRequest(req CreateValidationRequest) (CreateValidationRequest, error) {
	req.RequestID = strings.TrimSpace(req.RequestID)
	if !validUUID(req.RequestID) {
		return req, fmt.Errorf("requestId 必须是合法 UUID")
	}
	if !validCandidateID(req.CandidateID) {
		return req, fmt.Errorf("非法候选 ID: %q", req.CandidateID)
	}
	if req.CandidateRevision < 1 {
		return req, fmt.Errorf("candidateRevision 无效: %d（应为 >=1）", req.CandidateRevision)
	}
	switch req.EvidenceClass {
	case validationEvidenceRetrospective, validationEvidenceProspective:
	default:
		return req, fmt.Errorf("验证证据等级非法: %q（应为 retrospective | prospective）", req.EvidenceClass)
	}
	var err error
	if req.DiscoveryAnalysisIDs, err = normalizeIDList("discoveryAnalysisIds", req.DiscoveryAnalysisIDs, validAnalysisID); err != nil {
		return req, err
	}
	if req.TrialIDs, err = normalizeIDList("trialIds", req.TrialIDs, validTrialID); err != nil {
		return req, err
	}
	if req.Supersedes != "" && !validValidationID(req.Supersedes) {
		return req, fmt.Errorf("非法被替换验证 ID: %q", req.Supersedes)
	}
	req.Policy = normalizeValidationPolicy(req.Policy)
	if err := req.Windows.validate(); err != nil {
		return req, err
	}
	if err := req.Policy.validate(); err != nil {
		return req, err
	}
	return req, nil
}

// normalizeIDList 受限 ID 列表规范化：非空、逐项校验、拒绝重复，排序后
// 返回（冻结内容规范化，保证 hash 稳定）。
func normalizeIDList(name string, ids []string, valid func(string) bool) ([]string, error) {
	if len(ids) == 0 {
		return nil, fmt.Errorf("%s 不能为空", name)
	}
	seen := make(map[string]bool, len(ids))
	out := make([]string, 0, len(ids))
	for i, id := range ids {
		if !valid(id) {
			return nil, fmt.Errorf("%s[%d] 非法: %q", name, i, id)
		}
		if seen[id] {
			return nil, fmt.Errorf("%s 存在重复: %s", name, id)
		}
		seen[id] = true
		out = append(out, id)
	}
	sort.Strings(out)
	return out, nil
}

// createValidationRequestHash 规范化请求的 SHA-256（幂等判定）：同一
// RequestID 相同 hash 幂等返回，不同 hash 视为幂等键复用冲突。
func createValidationRequestHash(req CreateValidationRequest) (string, error) {
	canon := struct {
		CandidateID          string           `json:"candidateId"`
		CandidateRevision    int              `json:"candidateRevision"`
		EvidenceClass        string           `json:"evidenceClass"`
		DiscoveryAnalysisIDs []string         `json:"discoveryAnalysisIds"`
		TrialIDs             []string         `json:"trialIds"`
		Windows              WalkForwardSpec  `json:"windows"`
		Policy               ValidationPolicy `json:"policy"`
		Supersedes           string           `json:"supersedes"`
	}{req.CandidateID, req.CandidateRevision, req.EvidenceClass, req.DiscoveryAnalysisIDs,
		req.TrialIDs, req.Windows, req.Policy, req.Supersedes}
	buf, err := json.Marshal(canon)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(buf)
	return hex.EncodeToString(sum[:]), nil
}

// ValidationRequest 冻结的验证请求（request.json，先发布且永不修改）。
// 冻结复制并哈希（设计 §11.2）：候选指定 revision 快照（FactorRef/Use/
// 发现期证据摘要与 hash）、完整研究协议、因子实现版本、股票池/数据/标签
// 版本、全部已登记 trial、滚动窗口与验收政策。
type ValidationRequest struct {
	SchemaVersion int                `json:"schemaVersion"`
	ID            string             `json:"id"`
	FrozenAt      string             `json:"frozenAt"`
	Protocol      ValidationProtocol `json:"protocol"`
	// Candidate 冻结的候选 revision 快照（含 FactorRef/Use/Evidence 及其
	// ReportSHA256 绑定）；后续候选修订不影响本记录。
	Candidate FactorCandidate `json:"candidate"`
	// ResearchProtocol 发现期完整研究协议副本；与发现期报告 protocolHash
	// 同函数同值（ResearchProtocolHash），绑定股票池/数据/标签版本。
	ResearchProtocol     *ResearchProtocol `json:"researchProtocol,omitempty"`
	ResearchProtocolHash string            `json:"researchProtocolHash,omitempty"`
	// Trials 全部已登记 trial 快照（与 Protocol.TrialIDs 一一对应，含
	// running 与全部终态——不允许只保留成功结果的生存者偏差账本）。
	Trials []FactorTrial `json:"trials"`
	// RequestHash 冻结内容 SHA-256（对除本字段外的全部内容计算）；读取时
	// 重算校验，不匹配 fail closed。
	RequestHash string `json:"requestHash"`
	// CreateRequestID/CreateRequestHash 幂等键与请求 hash（同候选创建模式）。
	CreateRequestID   string `json:"createRequestId"`
	CreateRequestHash string `json:"createRequestHash"`
	// Supersedes 被替换的旧验证 ID（可选）。
	Supersedes string `json:"supersedes,omitempty"`
}

// validationRequestHash 冻结内容 hash：除 RequestHash 外全部字段进入规范
// 化 JSON 序列化。ID 与 FrozenAt 是记录标识与冻结时间，一并纳入。
func validationRequestHash(v ValidationRequest) (string, error) {
	canon := struct {
		SchemaVersion        int                `json:"schemaVersion"`
		ID                   string             `json:"id"`
		FrozenAt             string             `json:"frozenAt"`
		Protocol             ValidationProtocol `json:"protocol"`
		Candidate            FactorCandidate    `json:"candidate"`
		ResearchProtocol     *ResearchProtocol  `json:"researchProtocol,omitempty"`
		ResearchProtocolHash string             `json:"researchProtocolHash,omitempty"`
		Trials               []FactorTrial      `json:"trials"`
		CreateRequestID      string             `json:"createRequestId"`
		CreateRequestHash    string             `json:"createRequestHash"`
		Supersedes           string             `json:"supersedes"`
	}{v.SchemaVersion, v.ID, v.FrozenAt, v.Protocol, v.Candidate, v.ResearchProtocol,
		v.ResearchProtocolHash, v.Trials, v.CreateRequestID, v.CreateRequestHash, v.Supersedes}
	buf, err := json.Marshal(canon)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(buf)
	return hex.EncodeToString(sum[:]), nil
}

// ValidationWindowState 单窗口质量状态（Task 8 逐窗口披露，聚合不得掩盖
// 单窗口失败）。
type ValidationWindowState string

const (
	ValidationWindowOK           ValidationWindowState = "ok"
	ValidationWindowInsufficient ValidationWindowState = "insufficient"
	ValidationWindowError        ValidationWindowState = "error"
)

// ValidationWindowHorizon 单窗口单周期统计（设计 §13.2 展示要求：方向、
// IC、HAC t、覆盖率与分组 spread）。
type ValidationWindowHorizon struct {
	Horizon   int      `json:"horizon"`
	Direction string   `json:"direction"` // matched | inverted | unknown
	IC        *float64 `json:"ic,omitempty"`
	HACTStat  *float64 `json:"hacTStat,omitempty"`
	Coverage  float64  `json:"coverage"`
	// Spread 高低组收益差（Q_high − Q_low；分组方向以冻结预期方向为准）。
	Spread *float64 `json:"spread,omitempty"`
}

// ValidationWindowReport 单个测试窗口报告（Task 8 引擎写入 windows/NNNN.json）。
// 训练区只披露区间，不参与测试统计；失败与数据不足窗口同样保留。
type ValidationWindowReport struct {
	SchemaVersion int    `json:"schemaVersion"`
	ValidationID  string `json:"validationId"`
	// Index 1-based 窗口序号（与文件名 NNNN 一致，读取时校验）。
	Index int `json:"index"`
	// 测试窗口起止（YYYY-MM-DD）；TrainStart/TrainEnd 仅披露训练区间。
	StartDate  string `json:"startDate"`
	EndDate    string `json:"endDate"`
	TrainStart string `json:"trainStart,omitempty"`
	TrainEnd   string `json:"trainEnd,omitempty"`
	// State 窗口质量状态：ok | insufficient | error；非 ok 时 Message 说明
	// 原因，统计字段为空。
	State   ValidationWindowState `json:"state"`
	Message string                `json:"message,omitempty"`
	// Observations 窗口内有效观察日数（观察日加权聚合与等权披露的权重）。
	Observations int `json:"observations"`
	// Horizons 各周期统计（升序，与冻结协议发现期 Horizons 一致）。
	Horizons []ValidationWindowHorizon `json:"horizons"`
}

// ValidationCheck 逐项门禁结果（计划 Task 8 Step 3：不得只保存 bool）。
type ValidationCheck struct {
	Name     string `json:"name"`
	Expected string `json:"expected"`
	Actual   string `json:"actual"`
	Result   string `json:"result"` // pass | fail | unknown
	Reason   string `json:"reason,omitempty"`
}

// HorizonAggregate 单周期聚合披露。
type HorizonAggregate struct {
	Horizon int      `json:"horizon"`
	IC      *float64 `json:"ic,omitempty"`
	Spread  *float64 `json:"spread,omitempty"`
}

// ValidationAggregate 聚合披露（计划 Task 8 Step 4）：只使用测试窗口；
// 同时披露观察日加权与窗口等权两种口径；机器门禁口径由冻结 policy 决定。
type ValidationAggregate struct {
	// DirectionRate 方向一致窗口比例（[0,1]）。
	DirectionRate float64 `json:"directionRate"`
	// ObservedWeighted 按观察日加权；WindowEqualWeight 按窗口等权。
	ObservedWeighted  []HorizonAggregate `json:"observedWeighted,omitempty"`
	WindowEqualWeight []HorizonAggregate `json:"windowEqualWeight,omitempty"`
	// CoverageWeighted 覆盖率加权平均（[0,1]）。
	CoverageWeighted float64 `json:"coverageWeighted"`
	// GateAggregation 机器门禁实际使用的聚合口径（来自冻结 policy）。
	GateAggregation string `json:"gateAggregation"`
}

// FactorValidationReport 最终不可变验证报告（report.json，完成标记：存在
// 即终态，一次写入永不覆盖）。
type FactorValidationReport struct {
	SchemaVersion int    `json:"schemaVersion"`
	ValidationID  string `json:"validationId"`
	FinishedAt    string `json:"finishedAt"`
	// State 最终状态：passed | failed | insufficient | error（与 Verdict
	// 一一对应；State 冗余存储便于列表派生）。
	State   ValidationState   `json:"state"`
	Verdict ValidationVerdict `json:"verdict"`
	// Checks 逐项门禁（含 name/expected/actual/result/reason）。
	Checks []ValidationCheck `json:"checks"`
	// WindowCount/ValidWindowCount/InsufficientWindowCount 窗口计数披露；
	// insufficient 与 error 窗口计入 InsufficientWindowCount。
	WindowCount             int `json:"windowCount"`
	ValidWindowCount        int `json:"validWindowCount"`
	InsufficientWindowCount int `json:"insufficientWindowCount"`
	// Aggregate 聚合披露（error 状态可为 nil——执行异常不生成统计结论）。
	Aggregate *ValidationAggregate `json:"aggregate,omitempty"`
	// Message 结论说明（尤其 insufficient/error 的原因）。
	Message string `json:"message,omitempty"`
}

// validate 基本合法性：schema、ID、终态白名单、Verdict 与 State 一致、
// checks 非空。统计内容合法性由 Task 8 evaluateValidation 负责。
func (r FactorValidationReport) validate() error {
	if r.SchemaVersion != validationSchemaVersion {
		return fmt.Errorf("未知验证报告 schema 版本: %d（仅支持 %d）", r.SchemaVersion, validationSchemaVersion)
	}
	if !validValidationID(r.ValidationID) {
		return fmt.Errorf("非法验证 ID: %q", r.ValidationID)
	}
	if !finishedValidationStates[r.State] {
		return fmt.Errorf("报告状态非法: %q（running/frozen 不是完成态）", r.State)
	}
	want := ValidationVerdict(r.State)
	if r.Verdict != want {
		return fmt.Errorf("Verdict 与 State 不一致: %q vs %q", r.Verdict, want)
	}
	if r.FinishedAt == "" {
		return fmt.Errorf("finishedAt 不能为空")
	}
	if len(r.Checks) == 0 {
		return fmt.Errorf("checks 不能为空（逐项门禁必须披露）")
	}
	for i, c := range r.Checks {
		switch c.Result {
		case "pass", "fail", "unknown":
		default:
			return fmt.Errorf("checks[%d].result 非法: %q", i, c.Result)
		}
	}
	return nil
}

// freezeValidation 冻结验证（计划 Task 7 Step 2，纯函数）。校验：
//
//  1. candidate ID/revision 精确匹配；
//  2. evidence hash 仍有效（重算 SHA-256 与摘要一致性）；
//  3. FactorVersion 与候选一致（五字段全比对，fail closed）;
//  4. discovery analysis IDs 都存在且因子一致；
//  5. family trial 清单完整（声明清单 = 传入账本清单，同一家族）；
//  6. purgeDays >= max Horizon；
//  7. retrospective/prospective 与冻结时间关系合法；
//  8. policy 数值有限且范围合理。
//
// 返回的 ValidationRequest 未分配 ID 与 RequestHash（由 ValidationStore
// 生成，保持本函数纯度）；Protocol 清单规范化排序。
func freezeValidation(
	candidate FactorCandidate,
	evidence AnalysisReport,
	trials []FactorTrial,
	req CreateValidationRequest,
	now time.Time,
) (ValidationRequest, error) {
	norm, err := normalizeCreateValidationRequest(req)
	if err != nil {
		return ValidationRequest{}, err
	}

	// 1. candidate ID/revision 精确匹配。
	if norm.CandidateID != candidate.ID || norm.CandidateRevision != candidate.Revision {
		return ValidationRequest{}, fmt.Errorf(
			"候选 revision 不匹配: 请求 %s@%d，实际 %s@%d",
			norm.CandidateID, norm.CandidateRevision, candidate.ID, candidate.Revision)
	}

	// 2. evidence hash 仍有效：重算 SHA-256 并校验摘要一致性。
	evBuf, err := json.Marshal(evidence)
	if err != nil {
		return ValidationRequest{}, err
	}
	sum := sha256.Sum256(evBuf)
	if hex.EncodeToString(sum[:]) != candidate.Evidence.ReportSHA256 {
		return ValidationRequest{}, fmt.Errorf("发现期证据哈希不匹配，拒绝冻结")
	}
	if err := verifyEvidenceMatches(candidate.Evidence, &evidence); err != nil {
		return ValidationRequest{}, err
	}

	// 证据必须是带完整协议的 v4 报告：旧证据需先以完整协议重新分析
	//（设计 §14.4）。
	if evidence.AnalysisVersion < 4 || evidence.Protocol == nil {
		return ValidationRequest{}, fmt.Errorf("发现期证据缺少完整研究协议（仅 v4 报告可冻结验证；旧候选请先补充协议重新分析）")
	}
	if !validAnalysisID(evidence.AnalysisID) {
		return ValidationRequest{}, fmt.Errorf("发现期证据分析 ID 非法: %q", evidence.AnalysisID)
	}

	// 3. FactorVersion 与候选一致（五字段全比对）。
	if evidence.Kind != candidate.Factor.Kind ||
		evidence.Factor.Days != candidate.Factor.Days ||
		evidence.Factor.Name != candidate.Factor.Name ||
		evidence.Factor.Unit != candidate.Factor.Unit ||
		evidence.Factor.ImplementationVersion != candidate.Factor.ImplementationVersion {
		return ValidationRequest{}, fmt.Errorf("因子快照与候选不一致（kind/days/name/unit/version）")
	}

	// 4/5. discovery 分析与 trial 清单：按 ID 建索引。
	trialByID := make(map[string]FactorTrial, len(trials))
	analysisTrials := make(map[string][]FactorTrial)
	for _, t := range trials {
		if !validTrialID(t.ID) {
			return ValidationRequest{}, fmt.Errorf("trial 账本含非法 ID: %q", t.ID)
		}
		if _, dup := trialByID[t.ID]; dup {
			return ValidationRequest{}, fmt.Errorf("trial 账本重复: %s", t.ID)
		}
		trialByID[t.ID] = t
		analysisTrials[t.AnalysisID] = append(analysisTrials[t.AnalysisID], t)
	}
	// 5a. 传入账本清单必须与声明清单完全一致（完整家族账本快照）。
	if len(norm.TrialIDs) != len(trials) {
		return ValidationRequest{}, fmt.Errorf("trial 清单不完整: 声明 %d，账本 %d", len(norm.TrialIDs), len(trials))
	}
	for _, id := range norm.TrialIDs {
		if _, ok := trialByID[id]; !ok {
			return ValidationRequest{}, fmt.Errorf("声明的 trial 不在家族账本中: %s", id)
		}
	}
	// 5b. 全部 trial 同一家族（发现期协议 family）。
	familyID := evidence.Protocol.Trial.FamilyID
	for _, t := range trials {
		if t.FamilyID != familyID {
			return ValidationRequest{}, fmt.Errorf("trial %s 家族不一致: %s ≠ %s", t.ID, t.FamilyID, familyID)
		}
	}
	// 4a. 每个发现期分析都有对应 trial，且因子与候选一致。
	for _, analysisID := range norm.DiscoveryAnalysisIDs {
		ts, ok := analysisTrials[analysisID]
		if !ok || len(ts) == 0 {
			return ValidationRequest{}, fmt.Errorf("发现期分析无对应 trial: %s", analysisID)
		}
		for _, t := range ts {
			if t.Factor.Kind != candidate.Factor.Kind ||
				t.Factor.Days != candidate.Factor.Days ||
				t.Factor.ImplementationVersion != candidate.Factor.ImplementationVersion {
				return ValidationRequest{}, fmt.Errorf("trial %s 因子与候选不一致（kind/days/version）", t.ID)
			}
		}
	}
	// 4b. 候选自身证据分析必须在发现期清单内。
	foundSelf := false
	for _, id := range norm.DiscoveryAnalysisIDs {
		if id == candidate.Evidence.AnalysisID {
			foundSelf = true
			break
		}
	}
	if !foundSelf {
		return ValidationRequest{}, fmt.Errorf("发现期清单必须包含候选证据分析: %s", candidate.Evidence.AnalysisID)
	}

	// 6. purgeDays >= max Horizon（防标签跨训练/测试边界）。
	maxHorizon := 0
	for _, h := range evidence.Protocol.Labels.Horizons {
		if h > maxHorizon {
			maxHorizon = h
		}
	}
	if norm.Windows.PurgeDays < maxHorizon {
		return ValidationRequest{}, fmt.Errorf("purgeDays(%d) 必须覆盖最大收益周期(%d)", norm.Windows.PurgeDays, maxHorizon)
	}

	// 7. 证据等级与冻结时间关系。
	if err := validateFreezeEvidenceClass(norm.EvidenceClass, evidence, now); err != nil {
		return ValidationRequest{}, err
	}

	protocolHashValue, err := protocolHash(*evidence.Protocol)
	if err != nil {
		return ValidationRequest{}, fmt.Errorf("研究协议 hash 失败: %w", err)
	}

	protocol := ValidationProtocol{
		SchemaVersion:        validationSchemaVersion,
		EvidenceClass:        norm.EvidenceClass,
		CandidateID:          norm.CandidateID,
		CandidateRevision:    norm.CandidateRevision,
		DiscoveryAnalysisIDs: append([]string(nil), norm.DiscoveryAnalysisIDs...),
		TrialIDs:             append([]string(nil), norm.TrialIDs...),
		Windows:              norm.Windows,
		Policy:               norm.Policy,
	}
	if err := protocol.Validate(); err != nil {
		return ValidationRequest{}, err
	}
	trialSnap := make([]FactorTrial, len(trials))
	copy(trialSnap, trials)
	sort.Slice(trialSnap, func(i, j int) bool { return trialSnap[i].ID < trialSnap[j].ID })

	return ValidationRequest{
		SchemaVersion:        validationSchemaVersion,
		FrozenAt:             now.UTC().Format(time.RFC3339),
		Protocol:             protocol,
		Candidate:            candidate,
		ResearchProtocol:     evidence.Protocol,
		ResearchProtocolHash: protocolHashValue,
		Trials:               trialSnap,
		CreateRequestID:      norm.RequestID,
		CreateRequestHash:    mustCreateValidationRequestHash(norm),
		Supersedes:           norm.Supersedes,
	}, nil
}

// mustCreateValidationRequestHash 规范化请求 hash（norm 已通过校验，marshal
// 不可能失败；失败即内部错误）。
func mustCreateValidationRequestHash(req CreateValidationRequest) string {
	h, err := createValidationRequestHash(req)
	if err != nil {
		panic(fmt.Sprintf("createValidationRequestHash: %v", err))
	}
	return h
}

// validateFreezeEvidenceClass 证据等级与冻结时间关系（校验第 7 项）：
//   - retrospective：发现期证据必须本身为 retrospective（协议完整且数据可
//     验证），且冻结时间不早于发现期分析完成时间；
//   - prospective：冻结日期必须晚于发现期最后数据日（设计 §11.4：只有
//     FrozenAt 之后新进入系统的日期才能累计）。
func validateFreezeEvidenceClass(class string, evidence AnalysisReport, now time.Time) error {
	finishedAt, err := time.Parse(time.RFC3339, evidence.FinishedAt)
	if err != nil {
		return fmt.Errorf("发现期分析完成时间非法: %q", evidence.FinishedAt)
	}
	switch class {
	case validationEvidenceRetrospective:
		if evidence.EvidenceClass != evidenceRetrospective {
			return fmt.Errorf("发现期证据为 %s，无法进行 retrospective 验证（请改用 prospective 并冻结后累计新数据）",
				evidenceClassLabel(evidence.EvidenceClass))
		}
		if now.Before(finishedAt) {
			return fmt.Errorf("冻结时间(%s)早于发现期分析完成时间(%s)", now.Format(time.RFC3339), evidence.FinishedAt)
		}
	case validationEvidenceProspective:
		if evidence.LastDataDate == "" {
			return fmt.Errorf("发现期证据缺少最后数据日期，无法冻结前瞻验证")
		}
		lastDate, err := time.Parse("2006-01-02", evidence.LastDataDate)
		if err != nil {
			return fmt.Errorf("发现期最后数据日期非法: %q", evidence.LastDataDate)
		}
		// 冻结日必须晚于最后数据日（ISO 日期字典序与时间序一致）。
		if !now.After(lastDate.AddDate(0, 0, 1).Add(-time.Nanosecond)) {
			return fmt.Errorf("前瞻验证冻结时间必须晚于发现期最后数据日 %s", evidence.LastDataDate)
		}
	default:
		return fmt.Errorf("验证证据等级非法: %q", class)
	}
	return nil
}

// evidenceClassLabel 证据等级展示标签（未知值原样返回）。
func evidenceClassLabel(class string) string {
	if class == "" {
		return "legacy（无协议）"
	}
	return class
}
