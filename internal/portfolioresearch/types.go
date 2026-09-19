// Package portfolioresearch 多因子组合研究领域层（v2 设计 §5）。
//
// 职责边界：本包只承载纯领域类型、稳定枚举、规范化 modelHash 与证据降级
// 规则；不依赖 HTTP、DOM 或本地文件布局。v1 上游验证的适配、装配与存储由
// internal/lab 完成（见 factor_model.go / factor_model_store.go）。
package portfolioresearch

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"unicode/utf8"
)

// ---- 稳定枚举 ----

// 证据等级（三态，语义与 v1 验证层一致但独立定义，避免与上游类型耦合；
// 由 lab 适配层在装配时转换）。模型证据等级 = 最弱输入（见 CombineEvidenceClass）。
const (
	EvidenceExploratory   = "exploratory"   // 存在关键限制（静态池/旧标签/未验证数据）
	EvidenceRetrospective = "retrospective" // 协议完整且数据可验证的历史回放
	EvidenceProspective   = "prospective"   // 冻结后前瞻观察
)

// 因子方向（设计 §5.2）：冻结后不可自动翻转。
const (
	DirectionHigherIsBetter = "higher_is_better"
	DirectionLowerIsBetter  = "lower_is_better"
)

// 缺失策略（设计 §6.1）。
const (
	TransformMissingExclude              = "exclude"               // 缺失则股票不进入模型截面（默认）
	TransformMissingCrossSectionMedian   = "cross_section_median"  // 仅用当日截面中位数填充
	TransformMissingRenormalizeAvailable = "renormalize_available" // 按可用因子重归一化权重（显式变体）
)

// 去极值方式（设计 §6.2）。
const (
	TransformWinsorizeNone     = "none"
	TransformWinsorizeQuantile = "quantile"
	TransformWinsorizeMAD      = "mad"
)

// 中性化方式（设计 §6.3）。
const (
	TransformNeutralizeNone         = "none"
	TransformNeutralizeIndustrySize = "industry_size"
)

// 截面标准化（设计 §6.4）。
const (
	TransformStandardizeRank   = "rank"
	TransformStandardizeZScore = "zscore"
)

// 合成方法（设计 §7）：等权秩为默认基线；滚动 IC 权重只读训练窗。
const (
	CombinationEqualWeightRank = "equal_weight_rank"
	CombinationRollingICWeight = "rolling_ic_weight"
)

// 滚动权重训练数据不足时的退回策略（设计 §7.2，协议预先选择）。
const (
	CombinationFallbackEqualWeight = "equal_weight"
	CombinationFallbackCash        = "cash"
)

// 调仓频率（设计 §8.1）。
const (
	RebalanceDaily   = "daily"
	RebalanceWeekly  = "weekly"
	RebalanceMonthly = "monthly"
)

// 成交时点（设计 §4.2 / §9.2）：信号 t 日收盘后形成，最早 t+1 开盘成交。
const (
	FillNextOpen = "next_open"
)

// 选股方式（设计 §8.1）。
const (
	PortfolioSelectionTopN        = "top_n"
	PortfolioSelectionTopQuantile = "top_quantile"
)

// 运行状态（计划 Task 7，本 Task 先定义稳定枚举）。
const (
	RunStateQueued       = "queued"
	RunStateRunning      = "running"
	RunStateCompleted    = "completed"
	RunStateFailed       = "failed"
	RunStateCancelled    = "cancelled"
	RunStateInsufficient = "insufficient"
)

// 门禁结论（设计 §11.3）：passed 只表示通过冻结协议，不表示未来盈利保证。
const (
	GateStatusPassed       = "passed"
	GateStatusFailed       = "failed"
	GateStatusInsufficient = "insufficient"
	GateStatusError        = "error"
)

// 未成交原因（设计 §9.3，稳定枚举；默认未成交意图日终失效）。
const (
	UnfilledLimitUp          = "limit_up"          // 涨停买不到
	UnfilledLimitDown        = "limit_down"        // 跌停卖不掉
	UnfilledSuspended        = "suspended"         // 停牌
	UnfilledNotListed        = "not_listed"        // 未上市/退市
	UnfilledT1Restricted     = "t1_restricted"     // 当日买入不可卖出（T+1）
	UnfilledInsufficientCash = "insufficient_cash" // 可用现金不足
	UnfilledLotRounding      = "lot_rounding"      // 整手向下取整
	UnfilledTargetExpired    = "target_expired"    // 未成交意图跨日失效
)

// ---- 领域结构 ----

// DataSnapshot 数据、股票池与质量快照引用（身份的一部分；显示名称不能作为身份）。
type DataSnapshot struct {
	UniverseID      string `json:"universeId,omitempty"`
	UniverseVersion string `json:"universeVersion,omitempty"`
	UniverseMode    string `json:"universeMode,omitempty"`
	PriceSource     string `json:"priceSource,omitempty"`
	PriceVersion    string `json:"priceVersion,omitempty"`
	Adjustment      string `json:"adjustment,omitempty"`
	PITState        string `json:"pitState,omitempty"`
	SnapshotAt      string `json:"snapshotAt,omitempty"`
	Quality         string `json:"quality,omitempty"`
}

// ValidatedFactorRef 冻结的上游验证因子引用（设计 §5.2）。身份由候选
// revision、验证 ID、实例参数与实现版本共同确定，不包含显示名称。
type ValidatedFactorRef struct {
	CandidateID           string       `json:"candidateId"`
	CandidateRevision     int          `json:"candidateRevision"`
	ValidationID          string       `json:"validationId"`
	FactorKind            string       `json:"factorKind"`
	FactorDays            int          `json:"factorDays"`
	ImplementationVersion int          `json:"implementationVersion"`
	Direction             string       `json:"direction"`      // higher_is_better | lower_is_better
	EvidenceClass         string       `json:"evidenceClass"`  // exploratory | retrospective | prospective
	PrimaryHorizon        int          `json:"primaryHorizon"` // 上游主要预测周期（交易日）
	DataSnapshot          DataSnapshot `json:"dataSnapshot"`
}

// WinsorizeSpec 去极值参数（阈值写入模型 hash）。
type WinsorizeSpec struct {
	Mode     string  `json:"mode"`               // none | quantile | mad
	Quantile float64 `json:"quantile,omitempty"` // 分位阈值（双侧，如 0.01）
	MADK     float64 `json:"madK,omitempty"`     // MAD 倍数
}

// NeutralizeSpec 可选风险中性化（行业/市值当日截面回归）。
type NeutralizeSpec struct {
	Mode            string `json:"mode"` // none | industry_size
	IndustryDataset string `json:"industryDataset,omitempty"`
	SizeDataset     string `json:"sizeDataset,omitempty"`
}

// TransformPipeline 截面变换流水线（设计 §6，顺序固定）。
type TransformPipeline struct {
	Missing     string         `json:"missing"`     // exclude | cross_section_median | renormalize_available
	Winsorize   WinsorizeSpec  `json:"winsorize"`   // 去极值
	Neutralize  NeutralizeSpec `json:"neutralize"`  // 中性化
	Standardize string         `json:"standardize"` // rank | zscore
}

// RollingICSpec 滚动 IC 权重参数（只读取训练窗，权重快照保存起止与样本数）。
type RollingICSpec struct {
	WindowYears  int     `json:"windowYears"`  // 固定滚动训练窗年数
	Shrinkage    float64 `json:"shrinkage"`    // 显式收缩系数 [0,1]
	MaxAbsWeight float64 `json:"maxAbsWeight"` // 单因子绝对权重上限 (0,1]
	Fallback     string  `json:"fallback"`     // equal_weight | cash
}

// CombinationSpec 多因子合成（设计 §7）。
type CombinationSpec struct {
	Method    string         `json:"method"` // equal_weight_rank | rolling_ic_weight
	RollingIC *RollingICSpec `json:"rollingIc,omitempty"`
}

// PortfolioPolicy 目标组合政策（设计 §8）。
type PortfolioPolicy struct {
	Selection         string  `json:"selection"`      // top_n | top_quantile
	TopN              int     `json:"topN,omitempty"` // Top N 持仓数
	TopQuantile       float64 `json:"topQuantile,omitempty"`
	MaxStockWeight    float64 `json:"maxStockWeight,omitempty"`    // 单票上限
	MaxIndustryWeight float64 `json:"maxIndustryWeight,omitempty"` // 行业上限
	MaxTurnover       float64 `json:"maxTurnover,omitempty"`       // 换手上限
	MinHoldings       int     `json:"minHoldings,omitempty"`       // 最小持仓数（约束冲突不得静默放宽）
	CashBuffer        float64 `json:"cashBuffer"`                  // 现金缓冲 [0,1]
}

// CostSpec 成本模型参数（设计 §9.4，全部进入模型 hash）。
type CostSpec struct {
	CommissionRate  float64 `json:"commissionRate"`
	StampDutyRate   float64 `json:"stampDutyRate"`
	TransferFeeRate float64 `json:"transferFeeRate"`
	Slippage        float64 `json:"slippage"` // 每股滑点（绝对价格单位）
	MinCommission   float64 `json:"minCommission"`
}

// ExecutionSpec 可交易执行契约（设计 §9）。
type ExecutionSpec struct {
	Rebalance     string   `json:"rebalance"`     // daily | weekly | monthly
	FillAt        string   `json:"fillAt"`        // next_open
	SellFirst     bool     `json:"sellFirst"`     // 先卖后买
	T1Restriction bool     `json:"t1Restriction"` // 当日买入当日不可卖出
	CarryUnfilled bool     `json:"carryUnfilled"` // 未成交意图是否跨日保留（v2 默认 false）
	LotSize       int      `json:"lotSize"`       // 整手股数
	Cost          CostSpec `json:"cost"`
}

// BenchmarkSpec 基准绑定（缺基准不阻止绝对收益报告，但阻止超额归因过门禁）。
type BenchmarkSpec struct {
	ID      string `json:"id,omitempty"`
	Source  string `json:"source,omitempty"`
	Version string `json:"version,omitempty"`
}

// FactorModel 不可变模型配置快照（设计 §5.3）。保存后修改任何因子/变换/
// 权重/股票池/调仓/执行参数都必须创建新 revision 与新 modelHash。
// modelId/revision/createdAt 等由 Store 分配；ModelHash 只覆盖语义字段。
// CreateRequestID/CreateRequestHash 为幂等键（v1 同模式），不进入语义 hash。
type FactorModel struct {
	ModelID           string               `json:"modelId"`
	Revision          int                  `json:"revision"`
	CreatedAt         string               `json:"createdAt"`
	CreatedBy         string               `json:"createdBy"`
	ResearchQuestion  string               `json:"researchQuestion"`
	Hypothesis        string               `json:"hypothesis"`
	ValidatedFactors  []ValidatedFactorRef `json:"validatedFactors"`
	TransformPipeline TransformPipeline    `json:"transformPipeline"`
	Combination       CombinationSpec      `json:"combination"`
	PortfolioPolicy   PortfolioPolicy      `json:"portfolioPolicy"`
	Execution         ExecutionSpec        `json:"execution"`
	Benchmark         BenchmarkSpec        `json:"benchmark"`
	EvidenceClass     string               `json:"evidenceClass"` // 后端派生：最弱输入证据
	ModelHash         string               `json:"modelHash"`
	CodeVersion       string               `json:"codeVersion"`
	DataSnapshot      DataSnapshot         `json:"dataSnapshot"`

	CreateRequestID   string `json:"createRequestId,omitempty"`
	CreateRequestHash string `json:"createRequestHash,omitempty"`
}

// ---- 证据等级降级规则 ----

// evidenceClassRank 证据等级强弱（用于取最弱降级）。
var evidenceClassRank = map[string]int{
	EvidenceExploratory:   0,
	EvidenceRetrospective: 1,
	EvidenceProspective:   2,
}

// ValidEvidenceClass 校验证据等级枚举。
func ValidEvidenceClass(class string) bool {
	_, ok := evidenceClassRank[class]
	return ok
}

// CombineEvidenceClass 模型证据等级 = 最弱输入证据等级（设计 §4.1）。
// 任一输入为 exploratory 时结果必须为 exploratory；不允许客户端声明高于
// 最弱输入的证据等级。证据等级由后端唯一生成。
func CombineEvidenceClass(classes []string) (string, error) {
	if len(classes) == 0 {
		return "", fmt.Errorf("证据等级列表不能为空")
	}
	worst := evidenceClassRank[EvidenceProspective]
	for i, c := range classes {
		r, ok := evidenceClassRank[c]
		if !ok {
			return "", fmt.Errorf("classes[%d] 证据等级非法: %q（应为 exploratory | retrospective | prospective）", i, c)
		}
		if r < worst {
			worst = r
		}
	}
	for c, r := range evidenceClassRank {
		if r == worst {
			return c, nil
		}
	}
	return "", fmt.Errorf("证据等级推导失败（内部错误）")
}

// ---- 规范化 modelHash ----

// modelSemantic 参与 modelHash 的语义字段（排序后规范化序列化）。
// 因子显示名称、researchQuestion/hypothesis 等描述性文本不进入语义身份。
type modelSemantic struct {
	ValidatedFactors  []ValidatedFactorRef `json:"validatedFactors"`
	TransformPipeline TransformPipeline    `json:"transformPipeline"`
	Combination       CombinationSpec      `json:"combination"`
	PortfolioPolicy   PortfolioPolicy      `json:"portfolioPolicy"`
	Execution         ExecutionSpec        `json:"execution"`
	Benchmark         BenchmarkSpec        `json:"benchmark"`
	DataSnapshot      DataSnapshot         `json:"dataSnapshot"`
}

// normalizeSemantic 深拷贝并规范化语义字段：因子按稳定 key 排序、空 slice
// 归一为 nil、RollingIC 指针复制为独立值、拒绝非有限浮点并把 -0.0 归一为
// +0.0。返回可直接序列化的规范副本，不修改入参。
func normalizeSemantic(m FactorModel) (modelSemantic, error) {
	factors := make([]ValidatedFactorRef, len(m.ValidatedFactors))
	copy(factors, m.ValidatedFactors)
	sort.SliceStable(factors, func(i, j int) bool { return factorRefKey(factors[i]) < factorRefKey(factors[j]) })
	if len(factors) == 0 {
		factors = nil
	}
	sem := modelSemantic{
		ValidatedFactors:  factors,
		TransformPipeline: m.TransformPipeline,
		Combination:       m.Combination,
		PortfolioPolicy:   m.PortfolioPolicy,
		Execution:         m.Execution,
		Benchmark:         m.Benchmark,
		DataSnapshot:      m.DataSnapshot,
	}
	// RollingIC 为指针字段：深拷贝为独立值。checkFinite 的 -0.0 归一化会写回
	// 该指针指向的值，浅拷贝共享会污染入参模型；复制后保证入参不变。
	if m.Combination.RollingIC != nil {
		r := *m.Combination.RollingIC
		sem.Combination.RollingIC = &r
	}
	if err := sem.checkFinite(); err != nil {
		return modelSemantic{}, err
	}
	return sem, nil
}

// factorRefKey 因子的稳定排序/身份 key（规范化 JSON，不依赖字段顺序）。
func factorRefKey(f ValidatedFactorRef) string {
	buf, err := json.Marshal(f)
	if err != nil {
		// 所有字段均可序列化，失败即内部错误。
		panic("factorRefKey: " + err.Error())
	}
	return string(buf)
}

// checkFinite 拒绝语义字段中的 NaN/Inf（防 hash 不稳定与 JSON 序列化失败），
// 并把 -0.0 归一为 +0.0：二者语义相同但序列化字节不同，不归一会导致同一
// 语义产生不同 modelHash。归一化会写回字段，调用方须保证 s 为独立副本
// （normalizeSemantic 已深拷贝，含 RollingIC）。
func (s *modelSemantic) checkFinite() error {
	s.TransformPipeline.Winsorize.Quantile = normalizeZero(s.TransformPipeline.Winsorize.Quantile)
	if err := checkFiniteFloat("winsorize.quantile", s.TransformPipeline.Winsorize.Quantile); err != nil {
		return err
	}
	s.TransformPipeline.Winsorize.MADK = normalizeZero(s.TransformPipeline.Winsorize.MADK)
	if err := checkFiniteFloat("winsorize.madK", s.TransformPipeline.Winsorize.MADK); err != nil {
		return err
	}
	if s.Combination.RollingIC != nil {
		s.Combination.RollingIC.Shrinkage = normalizeZero(s.Combination.RollingIC.Shrinkage)
		if err := checkFiniteFloat("combination.rollingIc.shrinkage", s.Combination.RollingIC.Shrinkage); err != nil {
			return err
		}
		s.Combination.RollingIC.MaxAbsWeight = normalizeZero(s.Combination.RollingIC.MaxAbsWeight)
		if err := checkFiniteFloat("combination.rollingIc.maxAbsWeight", s.Combination.RollingIC.MaxAbsWeight); err != nil {
			return err
		}
	}
	s.PortfolioPolicy.TopQuantile = normalizeZero(s.PortfolioPolicy.TopQuantile)
	if err := checkFiniteFloat("portfolioPolicy.topQuantile", s.PortfolioPolicy.TopQuantile); err != nil {
		return err
	}
	s.PortfolioPolicy.MaxStockWeight = normalizeZero(s.PortfolioPolicy.MaxStockWeight)
	if err := checkFiniteFloat("portfolioPolicy.maxStockWeight", s.PortfolioPolicy.MaxStockWeight); err != nil {
		return err
	}
	s.PortfolioPolicy.MaxIndustryWeight = normalizeZero(s.PortfolioPolicy.MaxIndustryWeight)
	if err := checkFiniteFloat("portfolioPolicy.maxIndustryWeight", s.PortfolioPolicy.MaxIndustryWeight); err != nil {
		return err
	}
	s.PortfolioPolicy.MaxTurnover = normalizeZero(s.PortfolioPolicy.MaxTurnover)
	if err := checkFiniteFloat("portfolioPolicy.maxTurnover", s.PortfolioPolicy.MaxTurnover); err != nil {
		return err
	}
	s.PortfolioPolicy.CashBuffer = normalizeZero(s.PortfolioPolicy.CashBuffer)
	if err := checkFiniteFloat("portfolioPolicy.cashBuffer", s.PortfolioPolicy.CashBuffer); err != nil {
		return err
	}
	c := s.Execution.Cost
	c.CommissionRate = normalizeZero(c.CommissionRate)
	c.StampDutyRate = normalizeZero(c.StampDutyRate)
	c.TransferFeeRate = normalizeZero(c.TransferFeeRate)
	c.Slippage = normalizeZero(c.Slippage)
	c.MinCommission = normalizeZero(c.MinCommission)
	s.Execution.Cost = c
	for name, v := range map[string]float64{
		"execution.cost.commissionRate":  c.CommissionRate,
		"execution.cost.stampDutyRate":   c.StampDutyRate,
		"execution.cost.transferFeeRate": c.TransferFeeRate,
		"execution.cost.slippage":        c.Slippage,
		"execution.cost.minCommission":   c.MinCommission,
	} {
		if err := checkFiniteFloat(name, v); err != nil {
			return err
		}
	}
	return nil
}

// normalizeZero -0.0 与 +0.0 语义相同，统一归一为 +0.0（-0.0 序列化为
// "-0"，字节与 "0" 不同，会导致同一语义产生不同 modelHash）。
func normalizeZero(v float64) float64 {
	if v == 0 {
		return 0
	}
	return v
}

// checkFiniteFloat 校验浮点有限（NaN/±Inf 拒绝）。
func checkFiniteFloat(name string, v float64) error {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return fmt.Errorf("%s 必须为有限数值，实际 %v", name, v)
	}
	return nil
}

// ModelHash 对模型全部语义字段计算规范化 SHA-256（设计 §12.1）。
// 相同语义、不同 JSON 键序/浮点表示得到同一 hash；任一因子/方向/变换/
// 约束/成本变化得到不同 hash。因子按稳定 key 排序，因子集合顺序不改变身份。
func ModelHash(m FactorModel) (string, error) {
	sem, err := normalizeSemantic(m)
	if err != nil {
		return "", err
	}
	buf, err := json.Marshal(sem)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(buf)
	return hex.EncodeToString(sum[:]), nil
}

// ---- 资源限制 ----

// ModelLimits 模型创建的可配置资源限制（Task 1 动作 6）。错误信息必须包含
// 具体字段名与允许范围。
type ModelLimits struct {
	MaxFactors         int // validatedFactors 数量上限
	MaxHorizonDays     int // 因子主预测周期上限（交易日）
	MaxTopN            int // PortfolioPolicy.TopN 上限
	MaxCreatedByRunes  int // createdBy 字符上限
	MaxQuestionRunes   int // researchQuestion 字符上限
	MaxHypothesisRunes int // hypothesis 字符上限
}

// DefaultModelLimits 默认资源限制。
func DefaultModelLimits() ModelLimits {
	return ModelLimits{
		MaxFactors:         20,
		MaxHorizonDays:     365,
		MaxTopN:            500,
		MaxCreatedByRunes:  80,
		MaxQuestionRunes:   200,
		MaxHypothesisRunes: 2000,
	}
}

// ---- 校验 ----

// Validate 完整校验 FactorModel：文本 rune 上限、枚举白名单、数值范围与
// 资源限制（创建与读取 fail closed 的共用入口）。
func (m FactorModel) Validate(limits ModelLimits) error {
	if n := utf8.RuneCountInString(m.CreatedBy); n < 1 || n > limits.MaxCreatedByRunes {
		return fmt.Errorf("createdBy 需要 1～%d 个字符，实际 %d", limits.MaxCreatedByRunes, n)
	}
	if n := utf8.RuneCountInString(m.ResearchQuestion); n < 1 || n > limits.MaxQuestionRunes {
		return fmt.Errorf("researchQuestion 需要 1～%d 个字符，实际 %d", limits.MaxQuestionRunes, n)
	}
	if n := utf8.RuneCountInString(m.Hypothesis); n < 1 || n > limits.MaxHypothesisRunes {
		return fmt.Errorf("hypothesis 需要 1～%d 个字符，实际 %d", limits.MaxHypothesisRunes, n)
	}
	if len(m.ValidatedFactors) == 0 {
		return fmt.Errorf("validatedFactors 不能为空")
	}
	if len(m.ValidatedFactors) > limits.MaxFactors {
		return fmt.Errorf("factors: 最多 %d 个，实际 %d", limits.MaxFactors, len(m.ValidatedFactors))
	}
	classes := make([]string, len(m.ValidatedFactors))
	for i, f := range m.ValidatedFactors {
		if err := f.validate(limits); err != nil {
			return fmt.Errorf("validatedFactors[%d]: %w", i, err)
		}
		classes[i] = f.EvidenceClass
	}
	ec, err := CombineEvidenceClass(classes)
	if err != nil {
		return err
	}
	if m.EvidenceClass != ec {
		return fmt.Errorf("evidenceClass 与最弱输入不一致: %q（应为后端派生 %q，不接受客户端自报）", m.EvidenceClass, ec)
	}
	if err := m.TransformPipeline.Validate(); err != nil {
		return err
	}
	if err := m.Combination.Validate(); err != nil {
		return err
	}
	if err := m.PortfolioPolicy.Validate(limits); err != nil {
		return err
	}
	if err := m.Execution.Validate(); err != nil {
		return err
	}
	if err := m.Benchmark.Validate(); err != nil {
		return err
	}
	if m.DataSnapshot.PriceSource == "" && m.DataSnapshot.UniverseMode == "" {
		return fmt.Errorf("dataSnapshot 缺少数据/股票池引用（priceSource 与 universeMode 至少一个非空）")
	}
	return nil
}

// validate 校验单个因子引用（枚举白名单 + 资源限制）。
func (f ValidatedFactorRef) validate(limits ModelLimits) error {
	if f.CandidateID == "" {
		return fmt.Errorf("candidateId 不能为空")
	}
	if f.CandidateRevision < 1 {
		return fmt.Errorf("candidateRevision 无效: %d（应为 >=1）", f.CandidateRevision)
	}
	if f.ValidationID == "" {
		return fmt.Errorf("validationId 不能为空")
	}
	if f.FactorKind == "" {
		return fmt.Errorf("factorKind 不能为空")
	}
	if f.FactorDays < 1 {
		return fmt.Errorf("factorDays 无效: %d（应为 >=1）", f.FactorDays)
	}
	if f.ImplementationVersion < 1 {
		return fmt.Errorf("implementationVersion 无效: %d（应为 >=1）", f.ImplementationVersion)
	}
	switch f.Direction {
	case DirectionHigherIsBetter, DirectionLowerIsBetter:
	default:
		return fmt.Errorf("direction 非法: %q（应为 higher_is_better | lower_is_better）", f.Direction)
	}
	if !ValidEvidenceClass(f.EvidenceClass) {
		return fmt.Errorf("evidenceClass 非法: %q（应为 exploratory | retrospective | prospective）", f.EvidenceClass)
	}
	if f.PrimaryHorizon < 1 {
		return fmt.Errorf("primaryHorizon 无效: %d（应为 >=1）", f.PrimaryHorizon)
	}
	if f.PrimaryHorizon > limits.MaxHorizonDays {
		return fmt.Errorf("primaryHorizon 超出范围: %d（最多 %d 个交易日）", f.PrimaryHorizon, limits.MaxHorizonDays)
	}
	return nil
}

// Validate 校验变换流水线：枚举白名单与参数一致性（顺序固定，见设计 §6）。
func (p TransformPipeline) Validate() error {
	switch p.Missing {
	case TransformMissingExclude, TransformMissingCrossSectionMedian, TransformMissingRenormalizeAvailable:
	default:
		return fmt.Errorf("missing 非法: %q（应为 exclude | cross_section_median | renormalize_available）", p.Missing)
	}
	switch p.Winsorize.Mode {
	case TransformWinsorizeNone:
		if p.Winsorize.Quantile != 0 || p.Winsorize.MADK != 0 {
			return fmt.Errorf("winsorize.mode=none 不得携带阈值参数")
		}
	case TransformWinsorizeQuantile:
		if p.Winsorize.Quantile <= 0 || p.Winsorize.Quantile > 0.5 {
			return fmt.Errorf("winsorize.quantile 无效: %v（应为 (0,0.5]）", p.Winsorize.Quantile)
		}
		if p.Winsorize.MADK != 0 {
			return fmt.Errorf("winsorize.mode=quantile 不得携带 madK")
		}
	case TransformWinsorizeMAD:
		if p.Winsorize.MADK <= 0 || p.Winsorize.MADK > 100 {
			return fmt.Errorf("winsorize.madK 无效: %v（应为 (0,100]）", p.Winsorize.MADK)
		}
		if p.Winsorize.Quantile != 0 {
			return fmt.Errorf("winsorize.mode=mad 不得携带 quantile")
		}
	default:
		return fmt.Errorf("winsorize.mode 非法: %q（应为 none | quantile | mad）", p.Winsorize.Mode)
	}
	switch p.Neutralize.Mode {
	case TransformNeutralizeNone:
		if p.Neutralize.IndustryDataset != "" || p.Neutralize.SizeDataset != "" {
			return fmt.Errorf("neutralize.mode=none 不得携带数据集")
		}
	case TransformNeutralizeIndustrySize:
		if p.Neutralize.IndustryDataset == "" || p.Neutralize.SizeDataset == "" {
			return fmt.Errorf("neutralize.mode=industry_size 必须声明行业与市值数据集")
		}
	default:
		return fmt.Errorf("neutralize.mode 非法: %q（应为 none | industry_size）", p.Neutralize.Mode)
	}
	switch p.Standardize {
	case TransformStandardizeRank, TransformStandardizeZScore:
	default:
		return fmt.Errorf("standardize 非法: %q（应为 rank | zscore）", p.Standardize)
	}
	return nil
}

// Validate 校验合成规格（设计 §7）。
func (c CombinationSpec) Validate() error {
	switch c.Method {
	case CombinationEqualWeightRank:
		if c.RollingIC != nil {
			return fmt.Errorf("combination.method=equal_weight_rank 不得携带 rollingIc")
		}
	case CombinationRollingICWeight:
		if c.RollingIC == nil {
			return fmt.Errorf("combination.method=rolling_ic_weight 必须提供 rollingIc")
		}
		r := c.RollingIC
		if r.WindowYears < 1 || r.WindowYears > 10 {
			return fmt.Errorf("combination.rollingIc.windowYears 无效: %d（应为 1-10）", r.WindowYears)
		}
		if r.Shrinkage < 0 || r.Shrinkage > 1 {
			return fmt.Errorf("combination.rollingIc.shrinkage 无效: %v（应为 [0,1]）", r.Shrinkage)
		}
		if r.MaxAbsWeight <= 0 || r.MaxAbsWeight > 1 {
			return fmt.Errorf("combination.rollingIc.maxAbsWeight 无效: %v（应为 (0,1]）", r.MaxAbsWeight)
		}
		switch r.Fallback {
		case CombinationFallbackEqualWeight, CombinationFallbackCash:
		default:
			return fmt.Errorf("combination.rollingIc.fallback 非法: %q（应为 equal_weight | cash）", r.Fallback)
		}
	default:
		return fmt.Errorf("combination.method 非法: %q（应为 equal_weight_rank | rolling_ic_weight）", c.Method)
	}
	return nil
}

// Validate 校验目标组合政策（设计 §8.2，约束冲突不得静默放宽）。
func (p PortfolioPolicy) Validate(limits ModelLimits) error {
	if p.CashBuffer < 0 || p.CashBuffer > 1 {
		return fmt.Errorf("portfolioPolicy.cashBuffer 无效: %v（应为 [0,1]）", p.CashBuffer)
	}
	switch p.Selection {
	case PortfolioSelectionTopN:
		if p.TopN < 1 {
			return fmt.Errorf("portfolioPolicy.topN 无效: %d（应为 >=1）", p.TopN)
		}
		if p.TopN > limits.MaxTopN {
			return fmt.Errorf("portfolioPolicy.topN 超出范围: %d（最多 %d）", p.TopN, limits.MaxTopN)
		}
		if p.TopQuantile != 0 {
			return fmt.Errorf("portfolioPolicy.selection=top_n 不得携带 topQuantile")
		}
	case PortfolioSelectionTopQuantile:
		if p.TopQuantile <= 0 || p.TopQuantile > 1 {
			return fmt.Errorf("portfolioPolicy.topQuantile 无效: %v（应为 (0,1]）", p.TopQuantile)
		}
		if p.TopN != 0 {
			return fmt.Errorf("portfolioPolicy.selection=top_quantile 不得携带 topN")
		}
	default:
		return fmt.Errorf("portfolioPolicy.selection 非法: %q（应为 top_n | top_quantile）", p.Selection)
	}
	for name, v := range map[string]float64{
		"maxStockWeight":    p.MaxStockWeight,
		"maxIndustryWeight": p.MaxIndustryWeight,
		"maxTurnover":       p.MaxTurnover,
	} {
		if v < 0 || v > 1 {
			return fmt.Errorf("portfolioPolicy.%s 无效: %v（应为 [0,1]，0 表示不设限）", name, v)
		}
	}
	if p.MinHoldings < 0 {
		return fmt.Errorf("portfolioPolicy.minHoldings 无效: %d（应为 >=0）", p.MinHoldings)
	}
	return nil
}

// Validate 校验执行规格（设计 §9）。
func (e ExecutionSpec) Validate() error {
	switch e.Rebalance {
	case RebalanceDaily, RebalanceWeekly, RebalanceMonthly:
	default:
		return fmt.Errorf("execution.rebalance 非法: %q（应为 daily | weekly | monthly）", e.Rebalance)
	}
	if e.FillAt != FillNextOpen {
		return fmt.Errorf("execution.fillAt 非法: %q（应为 next_open）", e.FillAt)
	}
	if e.LotSize < 1 || e.LotSize > 10000 {
		return fmt.Errorf("execution.lotSize 无效: %d（应为 1-10000 股）", e.LotSize)
	}
	c := e.Cost
	for name, v := range map[string]float64{
		"commissionRate":  c.CommissionRate,
		"stampDutyRate":   c.StampDutyRate,
		"transferFeeRate": c.TransferFeeRate,
		"slippage":        c.Slippage,
		"minCommission":   c.MinCommission,
	} {
		if v < 0 {
			return fmt.Errorf("execution.cost.%s 无效: %v（应为 >=0）", name, v)
		}
	}
	if c.CommissionRate > 0.01 || c.StampDutyRate > 0.01 || c.TransferFeeRate > 0.01 {
		return fmt.Errorf("execution.cost 费率超出范围（佣金/印花税/过户费上限 0.01）")
	}
	if c.Slippage > 1 {
		return fmt.Errorf("execution.cost.slippage 超出范围: %v（上限 1 元/股）", c.Slippage)
	}
	return nil
}

// Validate 校验基准引用：允许为空（无基准）；非空时至少声明 ID 或来源。
func (b BenchmarkSpec) Validate() error {
	if b.ID == "" && b.Source == "" && b.Version == "" {
		return nil
	}
	if b.ID == "" && b.Source == "" {
		return fmt.Errorf("benchmark 非空时必须声明 id 或 source")
	}
	return nil
}

// ValidRunState 校验运行状态枚举。
func ValidRunState(s string) bool {
	switch s {
	case RunStateQueued, RunStateRunning, RunStateCompleted, RunStateFailed, RunStateCancelled, RunStateInsufficient:
		return true
	}
	return false
}

// ValidGateStatus 校验门禁状态枚举。
func ValidGateStatus(s string) bool {
	switch s {
	case GateStatusPassed, GateStatusFailed, GateStatusInsufficient, GateStatusError:
		return true
	}
	return false
}

// ValidUnfilledReason 校验未成交原因枚举。
func ValidUnfilledReason(s string) bool {
	switch s {
	case UnfilledLimitUp, UnfilledLimitDown, UnfilledSuspended, UnfilledNotListed,
		UnfilledT1Restricted, UnfilledInsufficientCash, UnfilledLotRounding, UnfilledTargetExpired:
		return true
	}
	return false
}
