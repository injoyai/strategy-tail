// validation.go v2 样本外组合验证领域层（设计 §11，计划 Task 8）。
//
// 审计边界（证据完整性核心）：
//   - 非重叠外层测试窗：训练窗 → 冻结权重/规则 → 测试窗。因子集合、组合
//     规则与门禁在验证开始前冻结（BuildValidationWindows 只消费窗口规则，
//     不感知任何测试结果）；研究者根据测试结果修改模型必须创建新验证，
//     旧测试窗从此视为已见数据。
//   - 每窗只使用其开始前允许的数据训练滚动权重：TrainingDatesForWindow 在
//     结构上保证返回的日期切片不可能包含测试窗日期；配合 TrainRollingIC
//     只接受 TrainingView 的类型隔离（combine.go）防止泄漏。
//   - 门禁结论由后端按冻结 spec 唯一生成（Evaluate / EvaluateWindowGates），
//     客户端不能指定 verdict；passed 只表示通过冻结协议，不表示未来盈利保证。
//   - 证据等级只降不升（设计 §4.1）：任一输入证据降级时输出取最弱值（min）；
//     passed 要求聚合证据不低于 EvidenceRequirement，exploratory 输入不可能
//     得到正式 passed。
//   - 结论确定性与可解释性：相同 windows + 相同 spec 得到相同结论（无测试后
//     选择路径）；每窗逐项 GateResult 可查。
//
// 门禁阈值约定：数值阈值 0（或空）表示该门禁未配置——不参与结论，报告该
// 门禁为 unknown（Expected=未配置）；配置后按方向比较。比例字段以小数表示
// （0.5 = 50%）。需要严格零边界时用最小可表示阈值（如 1e-9）表达。
package portfolioresearch

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
)

// ---- 稳定枚举 ----

// 窗口状态（设计 §14：失败与数据不足窗口同样保留，聚合不得掩盖单窗失败）。
const (
	WindowStateOK           = "ok"
	WindowStateInsufficient = "insufficient"
	WindowStateError        = "error"
)

// gatePass / gateFail / gateUnknown 逐项门禁结果枚举（与 v1 ValidationCheck 同约定）。
const (
	gatePass    = "pass"
	gateFail    = "fail"
	gateUnknown = "unknown"
)

// validationDateRe YYYY-MM-DD（字典序 = 时间序）。
var validationDateRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

// ---- 冻结的验证规格 ----

// ModelRef 冻结的模型引用（设计 §11.1：modelId/revision/hash 三者全部冻结）。
type ModelRef struct {
	ModelID  string `json:"modelId"`
	Revision int    `json:"revision"`
	Hash     string `json:"hash"`
}

// WindowRule 非重叠外层测试窗规则（设计 §11.1）。
//
// 每窗：训练窗（TrainDays 日）紧接测试窗（TestDays 日）；下一窗从当前训练
// 窗起点前移 Step 日。Step >= TestDays 保证测试窗互不重叠（Step 更大时测试
// 窗之间存在空隙，允许；训练窗允许跨窗重叠——滚动训练）。
type WindowRule struct {
	TrainDays int `json:"trainDays"`
	TestDays  int `json:"testDays"`
	Step      int `json:"step"`
}

// Validate 校验窗口规则：训练/测试/步长 1-10000（资源保护上限），
// Step >= TestDays 保证测试窗互不重叠。
func (w WindowRule) Validate() error {
	for name, v := range map[string]int{"trainDays": w.TrainDays, "testDays": w.TestDays, "step": w.Step} {
		if v < 1 || v > 10000 {
			return fmt.Errorf("windowRule.%s 无效: %d（应为 1-10000）", name, v)
		}
	}
	if w.Step < w.TestDays {
		return fmt.Errorf("windowRule.step 无效: %d（必须 >= testDays %d，否则测试窗互相重叠）", w.Step, w.TestDays)
	}
	return nil
}

// GateSpec 门禁协议字段（设计 §11.3）。所有阈值在验证开始前冻结并进入验证
// hash，不是硬编码金融真理。数值阈值 0（或空）表示该门禁未配置（跳过，
// 报告 unknown）；MaxDrawdown 为回撤边界（回撤 ≤ 0，配置时为负数）。
type GateSpec struct {
	MinValidWindows         int     `json:"minValidWindows"`         // 有效测试窗数下限
	MinTradingDays          int     `json:"minTradingDays"`          // 有效交易日数下限
	MinNetReturn            float64 `json:"minNetReturn"`            // 净收益下限（有效窗等权均值）
	MaxDrawdown             float64 `json:"maxDrawdown"`             // 最大回撤边界（最差窗，≤0）
	MaxCostDrag             float64 `json:"maxCostDrag"`             // 成本拖累上限（有效窗等权均值）
	MinInformationRatio     float64 `json:"minInformationRatio"`     // 信息比率下限（有效窗等权均值）
	MinExcessStability      float64 `json:"minExcessStability"`      // 超额收益稳定性：超额为正的有效窗占比下限
	MaxTurnover             float64 `json:"maxTurnover"`             // 年换手上限（有效窗等权均值）
	MaxCashResidual         float64 `json:"maxCashResidual"`         // 现金残留上限（有效窗等权均值）
	MaxUnfilledRate         float64 `json:"maxUnfilledRate"`         // 未成交率上限（有效窗等权均值）
	MaxConcentration        float64 `json:"maxConcentration"`        // 单票集中度上限（有效窗最大）
	MinDirectionConsistency float64 `json:"minDirectionConsistency"` // 多数窗口方向一致性：净收益为正的有效窗占比下限
	MinBaselineIncrement    float64 `json:"minBaselineIncrement"`    // 相对等权秩基线净收益增量下限（有效窗等权均值）
	MaxDegradedWindows      int     `json:"maxDegradedWindows"`      // 允许携带降级质量事件的窗口数上限
}

// Validate 门禁数值有限且范围合理（freeze 与读取共用，fail closed）。
func (g GateSpec) Validate() error {
	for name, v := range map[string]int{
		"minValidWindows":    g.MinValidWindows,
		"minTradingDays":     g.MinTradingDays,
		"maxDegradedWindows": g.MaxDegradedWindows,
	} {
		if v < 0 || v > 1024 {
			return fmt.Errorf("gates.%s 无效: %d（应为 0-1024）", name, v)
		}
	}
	for name, v := range map[string]float64{
		"minNetReturn":            g.MinNetReturn,
		"maxDrawdown":             g.MaxDrawdown,
		"maxCostDrag":             g.MaxCostDrag,
		"minInformationRatio":     g.MinInformationRatio,
		"minExcessStability":      g.MinExcessStability,
		"maxTurnover":             g.MaxTurnover,
		"maxCashResidual":         g.MaxCashResidual,
		"maxUnfilledRate":         g.MaxUnfilledRate,
		"maxConcentration":        g.MaxConcentration,
		"minDirectionConsistency": g.MinDirectionConsistency,
		"minBaselineIncrement":    g.MinBaselineIncrement,
	} {
		if err := checkFiniteFloat("gates."+name, v); err != nil {
			return err
		}
	}
	if g.MaxDrawdown > 0 {
		return fmt.Errorf("gates.maxDrawdown 无效: %v（回撤 ≤ 0，配置时为负数；0 = 未配置）", g.MaxDrawdown)
	}
	for name, v := range map[string]float64{
		"minExcessStability":      g.MinExcessStability,
		"maxCashResidual":         g.MaxCashResidual,
		"maxUnfilledRate":         g.MaxUnfilledRate,
		"maxConcentration":        g.MaxConcentration,
		"minDirectionConsistency": g.MinDirectionConsistency,
	} {
		if v < 0 || v > 1 {
			return fmt.Errorf("gates.%s 无效: %v（应为 [0,1]）", name, v)
		}
	}
	if g.MaxCostDrag < 0 || g.MaxTurnover < 0 {
		return fmt.Errorf("gates.maxCostDrag/maxTurnover 无效（应为 >=0）")
	}
	return nil
}

// PortfolioValidationSpec 冻结的组合验证规格（设计 §11.1/§11.3）。全部字段
// 进入验证 hash——模型引用、窗口规则、门禁、基准或证据要求任一变化都产生
// 新验证身份（新 validation ID）。
type PortfolioValidationSpec struct {
	ModelRef            ModelRef      `json:"modelRef"`
	WindowRule          WindowRule    `json:"windowRule"`
	Gates               GateSpec      `json:"gates"`
	Benchmark           BenchmarkSpec `json:"benchmark"`
	EvidenceRequirement string        `json:"evidenceRequirement"` // 空 = retrospective（见 ResolveEvidenceRequirement）
}

// DefaultEvidenceRequirement 默认最低证据等级：retrospective。exploratory
// 不允许作为正式验证的证据要求（exploratory 输入不可能得到正式 passed）。
func DefaultEvidenceRequirement() string { return EvidenceRetrospective }

// ResolveEvidenceRequirement 解析证据要求：空值归一为默认 retrospective。
func (s PortfolioValidationSpec) ResolveEvidenceRequirement() string {
	if s.EvidenceRequirement == "" {
		return DefaultEvidenceRequirement()
	}
	return s.EvidenceRequirement
}

// Validate 完整校验冻结规格：模型引用、窗口规则、门禁、基准与证据要求。
func (s PortfolioValidationSpec) Validate() error {
	if s.ModelRef.ModelID == "" {
		return fmt.Errorf("modelRef.modelId 不能为空")
	}
	if s.ModelRef.Revision < 1 {
		return fmt.Errorf("modelRef.revision 无效: %d（应为 >=1）", s.ModelRef.Revision)
	}
	if !validHashHex(s.ModelRef.Hash) {
		return fmt.Errorf("modelRef.hash 必须为 64 位十六进制")
	}
	if err := s.WindowRule.Validate(); err != nil {
		return err
	}
	if err := s.Gates.Validate(); err != nil {
		return err
	}
	if err := s.Benchmark.Validate(); err != nil {
		return err
	}
	switch s.EvidenceRequirement {
	case "", EvidenceRetrospective, EvidenceProspective:
	default:
		return fmt.Errorf("evidenceRequirement 非法: %q（应为 retrospective | prospective；exploratory 不允许作为正式验证要求）", s.EvidenceRequirement)
	}
	return nil
}

// validHashHex 64 位十六进制校验（模型 hash / 幂等请求 hash 共用）。
func validHashHex(h string) bool {
	if len(h) != 64 {
		return false
	}
	_, err := hex.DecodeString(h)
	return err == nil
}

// ---- 验证 hash（不可变身份） ----

// validationSpecSemantic 参与验证 hash 的语义副本（规范化后序列化）。
// EvidenceRequirement 空值归一为 retrospective：空与显式默认是同一身份。
type validationSpecSemantic struct {
	ModelRef            ModelRef      `json:"modelRef"`
	WindowRule          WindowRule    `json:"windowRule"`
	Gates               GateSpec      `json:"gates"`
	Benchmark           BenchmarkSpec `json:"benchmark"`
	EvidenceRequirement string        `json:"evidenceRequirement"`
}

// canonical 深拷贝并规范化语义字段（-0.0 → +0.0、拒绝 NaN/Inf），
// 返回可直接序列化的规范副本，不修改入参。
func (s PortfolioValidationSpec) canonical() (validationSpecSemantic, error) {
	sem := validationSpecSemantic{
		ModelRef:            s.ModelRef,
		WindowRule:          s.WindowRule,
		Gates:               s.Gates,
		Benchmark:           s.Benchmark,
		EvidenceRequirement: s.EvidenceRequirement,
	}
	if sem.EvidenceRequirement == "" {
		sem.EvidenceRequirement = DefaultEvidenceRequirement()
	}
	if err := sem.checkFinite(); err != nil {
		return validationSpecSemantic{}, err
	}
	return sem, nil
}

// Normalized 返回规范化深拷贝（-0.0 → +0.0、空证据要求归一为默认
// retrospective；拒绝 NaN/Inf），不修改入参。用于幂等请求 hash 等需要语义
// 等价归一化的场景，与 PortfolioValidationSpecHash 同一规范化口径。
func (s PortfolioValidationSpec) Normalized() (PortfolioValidationSpec, error) {
	sem, err := s.canonical()
	if err != nil {
		return PortfolioValidationSpec{}, err
	}
	return PortfolioValidationSpec{
		ModelRef:            sem.ModelRef,
		WindowRule:          sem.WindowRule,
		Gates:               sem.Gates,
		Benchmark:           sem.Benchmark,
		EvidenceRequirement: sem.EvidenceRequirement,
	}, nil
}

// checkFinite 规范化门禁浮点（-0.0 → +0.0）并拒绝 NaN/Inf。写回独立副本，
// 调用方保证 sem 为 canonical 深拷贝。
func (sem *validationSpecSemantic) checkFinite() error {
	g := sem.Gates
	g.MinNetReturn = normalizeZero(g.MinNetReturn)
	g.MaxDrawdown = normalizeZero(g.MaxDrawdown)
	g.MaxCostDrag = normalizeZero(g.MaxCostDrag)
	g.MinInformationRatio = normalizeZero(g.MinInformationRatio)
	g.MinExcessStability = normalizeZero(g.MinExcessStability)
	g.MaxTurnover = normalizeZero(g.MaxTurnover)
	g.MaxCashResidual = normalizeZero(g.MaxCashResidual)
	g.MaxUnfilledRate = normalizeZero(g.MaxUnfilledRate)
	g.MaxConcentration = normalizeZero(g.MaxConcentration)
	g.MinDirectionConsistency = normalizeZero(g.MinDirectionConsistency)
	g.MinBaselineIncrement = normalizeZero(g.MinBaselineIncrement)
	sem.Gates = g
	for name, v := range map[string]float64{
		"gates.minNetReturn":            sem.Gates.MinNetReturn,
		"gates.maxDrawdown":             sem.Gates.MaxDrawdown,
		"gates.maxCostDrag":             sem.Gates.MaxCostDrag,
		"gates.minInformationRatio":     sem.Gates.MinInformationRatio,
		"gates.minExcessStability":      sem.Gates.MinExcessStability,
		"gates.maxTurnover":             sem.Gates.MaxTurnover,
		"gates.maxCashResidual":         sem.Gates.MaxCashResidual,
		"gates.maxUnfilledRate":         sem.Gates.MaxUnfilledRate,
		"gates.maxConcentration":        sem.Gates.MaxConcentration,
		"gates.minDirectionConsistency": sem.Gates.MinDirectionConsistency,
		"gates.minBaselineIncrement":    sem.Gates.MinBaselineIncrement,
	} {
		if err := checkFiniteFloat(name, v); err != nil {
			return err
		}
	}
	return nil
}

// PortfolioValidationSpecHash 对冻结规格全部字段计算规范化 SHA-256
// （设计 §12.1）。相同语义、不同 JSON 键序/浮点表示得到同一 hash；任一
// 模型/窗口/门禁/基准/证据要求变化得到不同 hash（不可变身份）。
func PortfolioValidationSpecHash(s PortfolioValidationSpec) (string, error) {
	sem, err := s.canonical()
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

// ---- 非重叠测试窗构建 ----

// ValidationWindowPlan 单窗计划：训练窗紧接测试窗（训练结束日 < 测试开始日）。
type ValidationWindowPlan struct {
	Index      int    `json:"index"`
	TrainStart string `json:"trainStart"`
	TrainEnd   string `json:"trainEnd"`
	TestStart  string `json:"testStart"`
	TestEnd    string `json:"testEnd"`
}

// BuildValidationWindows 构建非重叠外层测试窗序列（设计 §11.1，纯函数、
// 确定性）。dates 为升序 YYYY-MM-DD 交易日；第 k 窗（k 从 0 起）：
//
//	训练 = dates[k*step : k*step+train]，测试 = dates[k*step+train : +test]
//
// Step >= TestDays（WindowRule.Validate 保证）⇒ 测试窗互不重叠（可相邻或
// 留空隙）；训练窗只使用该测试窗开始前的数据。日期不足以构成完整窗口时
// 自然停止，窗口数变少（由门禁 MinValidWindows 判定 insufficient）。
func BuildValidationWindows(dates []string, rule WindowRule) ([]ValidationWindowPlan, error) {
	if err := validateDates(dates); err != nil {
		return nil, err
	}
	if err := rule.Validate(); err != nil {
		return nil, err
	}
	var out []ValidationWindowPlan
	for pos := 0; pos+rule.TrainDays+rule.TestDays <= len(dates); pos += rule.Step {
		out = append(out, ValidationWindowPlan{
			Index:      len(out) + 1,
			TrainStart: dates[pos],
			TrainEnd:   dates[pos+rule.TrainDays-1],
			TestStart:  dates[pos+rule.TrainDays],
			TestEnd:    dates[pos+rule.TrainDays+rule.TestDays-1],
		})
	}
	return out, nil
}

// validateDates 校验日期列表：非空、严格升序、YYYY-MM-DD 格式。
func validateDates(dates []string) error {
	if len(dates) == 0 {
		return fmt.Errorf("日期列表不能为空")
	}
	for i, d := range dates {
		if !validationDateRe.MatchString(d) {
			return fmt.Errorf("Dates[%d] 非法日期: %q（应为 YYYY-MM-DD）", i, d)
		}
		if i > 0 && d <= dates[i-1] {
			return fmt.Errorf("日期必须严格升序: %s <= %s", d, dates[i-1])
		}
	}
	return nil
}

// TrainingDatesForWindow 返回该窗训练期日期（只含该窗开始前允许的数据，
// 泄漏隔离）。按训练起止在 dates 中的位置截取，测试窗日期在结构上不可能
// 进入返回切片；配合 TrainRollingIC 只接受 TrainingView 的类型隔离，测试窗
// 收益数据无法进入训练器。
func TrainingDatesForWindow(dates []string, plan ValidationWindowPlan) ([]string, error) {
	if err := validateDates(dates); err != nil {
		return nil, err
	}
	// TrainStart==TrainEnd（单日训练窗，WindowRule.Validate 允许 1-10000 日）
	// 时，同一日期必须同时命中 start 与 end：两条判断互不遮蔽（若用 switch
	// 互斥 case，首条命中后第二条不再评估，end 恒为 -1 误报"区间不在列表"）。
	start, end := -1, -1
	for i, d := range dates {
		if d == plan.TrainEnd {
			end = i
		}
		if d == plan.TrainStart && start < 0 {
			start = i
		}
	}
	if start < 0 || end < 0 || end < start {
		return nil, fmt.Errorf("窗口训练区间 %s~%s 不在日期列表中", plan.TrainStart, plan.TrainEnd)
	}
	if plan.TestStart <= plan.TrainEnd {
		return nil, fmt.Errorf("窗口测试开始日 %s 不晚于训练结束日 %s（泄漏）", plan.TestStart, plan.TrainEnd)
	}
	train := make([]string, end-start+1)
	copy(train, dates[start:end+1])
	return train, nil
}

// ---- 单窗结果 ----

// WindowQualityEvent 窗口内数据质量事件（证据降级标记，进入每窗与汇总披露）。
type WindowQualityEvent struct {
	Date     string `json:"date"`
	Type     string `json:"type"` // missing_market_data | delisted | corporate_action | capacity | ...
	Detail   string `json:"detail,omitempty"`
	Degraded bool   `json:"degraded"`
}

// ValidationWindowMetrics 单窗统计（引擎按 ComputeMetrics 等口径填充）。
// InformationRatio / AnnualExcess 为 nil 表示基准缺失或对齐不足（设计 §9.5：
// 缺基准不阻止绝对收益报告，但阻止超额归因通过门禁）。
type ValidationWindowMetrics struct {
	TradingDays       int      `json:"tradingDays"`                // 有效交易日数
	NetReturn         float64  `json:"netReturn"`                  // 窗内净收益
	MaxDrawdown       float64  `json:"maxDrawdown"`                // 最大回撤（≤0，0 = 无回撤）
	CostDrag          float64  `json:"costDrag"`                   // 成本拖累
	AnnualTurnover    float64  `json:"annualTurnover"`             // 年换手
	AvgCashRatio      float64  `json:"avgCashRatio"`               // 平均现金比例 [0,1]
	UnfilledRate      float64  `json:"unfilledRate"`               // 未成交率 = 未成交意图 / 总意图 [0,1]
	MaxConcentration  float64  `json:"maxConcentration"`           // 单票最大权重 [0,1]
	InformationRatio  *float64 `json:"informationRatio,omitempty"` // 信息比率（nil = 基准缺失/对齐不足）
	AnnualExcess      *float64 `json:"annualExcess,omitempty"`     // 年化超额收益（相对基准，nil = 不可用）
	BaselineIncrement float64  `json:"baselineIncrement"`          // 相对等权秩基线净收益增量
}

// Validate 校验单窗指标：交易日数、有限浮点与比例范围。
func (m ValidationWindowMetrics) Validate() error {
	if m.TradingDays < 1 {
		return fmt.Errorf("tradingDays 无效: %d（应为 >=1）", m.TradingDays)
	}
	for name, v := range map[string]float64{
		"netReturn":         m.NetReturn,
		"maxDrawdown":       m.MaxDrawdown,
		"costDrag":          m.CostDrag,
		"annualTurnover":    m.AnnualTurnover,
		"avgCashRatio":      m.AvgCashRatio,
		"unfilledRate":      m.UnfilledRate,
		"maxConcentration":  m.MaxConcentration,
		"baselineIncrement": m.BaselineIncrement,
	} {
		if err := checkFiniteFloat("metrics."+name, v); err != nil {
			return err
		}
	}
	if m.MaxDrawdown > 1e-12 {
		return fmt.Errorf("metrics.maxDrawdown 无效: %v（回撤 ≤ 0）", m.MaxDrawdown)
	}
	for name, v := range map[string]float64{
		"avgCashRatio":     m.AvgCashRatio,
		"unfilledRate":     m.UnfilledRate,
		"maxConcentration": m.MaxConcentration,
	} {
		if v < 0 || v > 1 {
			return fmt.Errorf("metrics.%s 无效: %v（应为 [0,1]）", name, v)
		}
	}
	for name, v := range map[string]*float64{
		"informationRatio": m.InformationRatio,
		"annualExcess":     m.AnnualExcess,
	} {
		if v != nil {
			if err := checkFiniteFloat("metrics."+name, *v); err != nil {
				return err
			}
		}
	}
	return nil
}

// ValidationWindowOutcome 单窗输出（设计 §11：模型/权重快照引用、报告引用、
// 质量事件、逐项门禁结果与该窗证据等级）。Snapshot 保存冻结权重内容，
// SnapshotRef 为权重快照的安全引用（lab 存储可只保留引用）。
type ValidationWindowOutcome struct {
	ValidationID string `json:"validationId,omitempty"` // lab 存储一致性字段
	Index        int    `json:"index"`
	TrainStart   string `json:"trainStart"`
	TrainEnd     string `json:"trainEnd"`
	TestStart    string `json:"testStart"`
	TestEnd      string `json:"testEnd"`
	State        string `json:"state"` // ok | insufficient | error
	Message      string `json:"message,omitempty"`

	SnapshotRef   string                   `json:"snapshotRef,omitempty"` // 权重快照引用
	Snapshot      *WeightSnapshot          `json:"snapshot,omitempty"`    // 冻结权重快照内容
	ReportRef     string                   `json:"reportRef,omitempty"`   // 报告引用
	Metrics       *ValidationWindowMetrics `json:"metrics,omitempty"`
	QualityEvents []WindowQualityEvent     `json:"qualityEvents,omitempty"`
	Gates         []GateResult             `json:"gates,omitempty"` // 逐项门禁结果（窗级，EvaluateWindowGates 产出）
	EvidenceClass string                   `json:"evidenceClass"`   // 该窗证据等级（最弱输入）
}

// Validate 校验窗口结果：状态枚举、区间次序（训练严格早于测试，泄漏隔离）、
// 证据等级；ok 窗口必须携带有效指标。
func (o ValidationWindowOutcome) Validate() error {
	if o.Index < 1 {
		return fmt.Errorf("index 无效: %d（应为 >=1）", o.Index)
	}
	switch o.State {
	case WindowStateOK, WindowStateInsufficient, WindowStateError:
	default:
		return fmt.Errorf("state 非法: %q（应为 ok | insufficient | error）", o.State)
	}
	for name, d := range map[string]string{
		"trainStart": o.TrainStart, "trainEnd": o.TrainEnd,
		"testStart": o.TestStart, "testEnd": o.TestEnd,
	} {
		if !validationDateRe.MatchString(d) {
			return fmt.Errorf("%s 非法日期: %q（应为 YYYY-MM-DD）", name, d)
		}
	}
	if o.TrainStart > o.TrainEnd {
		return fmt.Errorf("训练区间颠倒: %s > %s", o.TrainStart, o.TrainEnd)
	}
	if o.TestStart > o.TestEnd {
		return fmt.Errorf("测试区间颠倒: %s > %s", o.TestStart, o.TestEnd)
	}
	if o.TrainEnd >= o.TestStart {
		return fmt.Errorf("训练结束 %s 必须早于测试开始 %s（泄漏隔离）", o.TrainEnd, o.TestStart)
	}
	if !ValidEvidenceClass(o.EvidenceClass) {
		return fmt.Errorf("evidenceClass 非法: %q（应为 exploratory | retrospective | prospective）", o.EvidenceClass)
	}
	if o.State == WindowStateOK {
		if o.Metrics == nil {
			return fmt.Errorf("ok 窗口必须携带指标（metrics）")
		}
	}
	if o.Metrics != nil {
		if err := o.Metrics.Validate(); err != nil {
			return err
		}
	}
	return nil
}

// ValidWindowState 校验窗口状态枚举。
func ValidWindowState(s string) bool {
	switch s {
	case WindowStateOK, WindowStateInsufficient, WindowStateError:
		return true
	}
	return false
}

// ---- 逐项门禁结果 ----

// GateResult 逐项门禁结果（name/expected/actual/result/reason，v1 同约定）。
// result：pass | fail | unknown（unknown = 未配置阈值跳过或结论不评估统计门禁）。
type GateResult struct {
	Name     string `json:"name"`
	Expected string `json:"expected"`
	Actual   string `json:"actual"`
	Result   string `json:"result"`
	Reason   string `json:"reason,omitempty"`
}

// gateNames 聚合结论逐项披露的规范门禁名清单（固定顺序，所有结论路径一致，
// 保证可解释性）。
var gateNames = []string{
	"min_valid_windows",
	"min_trading_days",
	"net_return",
	"max_drawdown",
	"cost_drag",
	"information_ratio",
	"excess_stability",
	"turnover",
	"cash_residual",
	"unfilled_rate",
	"concentration",
	"direction_consistency",
	"baseline_increment",
	"max_degraded_windows",
	"evidence_requirement",
}

// gateResult 构造单项门禁结果。
func gateResult(name, expected, actual, result, reason string) GateResult {
	return GateResult{Name: name, Expected: expected, Actual: actual, Result: result, Reason: reason}
}

// unconfiguredGate 未配置阈值的门禁（result=unknown）。
func unconfiguredGate(name, actual string) GateResult {
	return gateResult(name, "未配置", actual, gateUnknown, "未配置阈值，跳过")
}

// gateCountGE 整数下限门禁（>= expected；expected<=0 视为未配置）。
func gateCountGE(name string, expected, actual int) GateResult {
	if expected <= 0 {
		return unconfiguredGate(name, fmt.Sprintf("%d", actual))
	}
	if actual >= expected {
		return gateResult(name, fmt.Sprintf(">= %d", expected), fmt.Sprintf("%d", actual), gatePass, "")
	}
	return gateResult(name, fmt.Sprintf(">= %d", expected), fmt.Sprintf("%d", actual), gateFail,
		fmt.Sprintf("%d < %d", actual, expected))
}

// gateCountLE 整数上限门禁（<= expected；expected<=0 视为未配置）。
func gateCountLE(name string, expected, actual int) GateResult {
	if expected <= 0 {
		return unconfiguredGate(name, fmt.Sprintf("%d", actual))
	}
	if actual <= expected {
		return gateResult(name, fmt.Sprintf("<= %d", expected), fmt.Sprintf("%d", actual), gatePass, "")
	}
	return gateResult(name, fmt.Sprintf("<= %d", expected), fmt.Sprintf("%d", actual), gateFail,
		fmt.Sprintf("%d > %d", actual, expected))
}

// gateFloatGE 浮点下限门禁（actual >= expected；expected==0 视为未配置）。
func gateFloatGE(name string, expected, actual float64) GateResult {
	if expected == 0 {
		return unconfiguredGate(name, fmtFloat(actual))
	}
	if actual >= expected {
		return gateResult(name, ">= "+fmtFloat(expected), fmtFloat(actual), gatePass, "")
	}
	return gateResult(name, ">= "+fmtFloat(expected), fmtFloat(actual), gateFail,
		fmt.Sprintf("%s < %s", fmtFloat(actual), fmtFloat(expected)))
}

// gateFloatLE 浮点上限门禁（actual <= expected；expected==0 视为未配置）。
func gateFloatLE(name string, expected, actual float64) GateResult {
	if expected == 0 {
		return unconfiguredGate(name, fmtFloat(actual))
	}
	if actual <= expected {
		return gateResult(name, "<= "+fmtFloat(expected), fmtFloat(actual), gatePass, "")
	}
	return gateResult(name, "<= "+fmtFloat(expected), fmtFloat(actual), gateFail,
		fmt.Sprintf("%s > %s", fmtFloat(actual), fmtFloat(expected)))
}

// gateIR 聚合信息比率门禁：任一有效窗基准缺失（IR 为 nil）→ fail（设计
// §9.5：缺基准阻止超额归因通过门禁）。
func gateIR(expected float64, sum float64, irWindows, n int) GateResult {
	if expected == 0 {
		return unconfiguredGate("information_ratio", "—")
	}
	if irWindows < n {
		return gateResult("information_ratio", ">= "+fmtFloat(expected), fmt.Sprintf("%d/%d 窗", irWindows, n), gateFail,
			fmt.Sprintf("%d/%d 有效窗信息比率缺失（基准缺失或对齐不足），超额门禁不通过", n-irWindows, n))
	}
	mean := sum / float64(n)
	return gateFloatGE("information_ratio", expected, mean)
}

// gateIRWindow 单窗信息比率门禁（IR 为 nil → fail）。
func gateIRWindow(expected float64, actual *float64) GateResult {
	if expected == 0 {
		return unconfiguredGate("information_ratio", "—")
	}
	if actual == nil {
		return gateResult("information_ratio", ">= "+fmtFloat(expected), "unavailable", gateFail,
			"信息比率未定义（基准缺失或对齐不足）")
	}
	return gateFloatGE("information_ratio", expected, *actual)
}

// gateEvidence 证据等级门禁：实际聚合证据等级必须不低于要求（exploratory
// 输入不可能通过；不允许后端提升）。
func gateEvidence(expected, actual string) GateResult {
	er, okE := evidenceClassRank[expected]
	ar, okA := evidenceClassRank[actual]
	if !okE || !okA {
		return gateResult("evidence_requirement", ">= "+expected, actual, gateFail, "证据等级非法")
	}
	if ar >= er {
		return gateResult("evidence_requirement", ">= "+expected, actual, gatePass, "")
	}
	return gateResult("evidence_requirement", ">= "+expected, actual, gateFail,
		fmt.Sprintf("%s 低于要求 %s（证据等级只降不升）", actual, expected))
}

// fmtFloat 数值披露格式（短格式，比例以小数表示，0.5 = 50%）。
func fmtFloat(v float64) string {
	return fmt.Sprintf("%g", v)
}

// ---- 门禁求值 ----

// ValidationResult 后端生成的最终结论（Evaluate 输出）。Verdict 只能由
// 后端按冻结 spec 生成，客户端不能指定。
type ValidationResult struct {
	Verdict          string       `json:"verdict"` // passed | failed | insufficient | error
	EvidenceClass    string       `json:"evidenceClass"`
	Gates            []GateResult `json:"gates"`
	WindowCount      int          `json:"windowCount"`
	ValidWindowCount int          `json:"validWindowCount"`
	Message          string       `json:"message,omitempty"`
}

// Evaluate 后端门禁结论生成（设计 §11.3，纯函数、确定性）：给定每窗结果 +
// 冻结 spec → verdict + 逐项 GateResult。规则：
//
//  1. 任一窗口执行错误 → error（执行错误不伪装统计失败）；
//  2. 有效窗口不足（0 个有效窗口，或 < MinValidWindows，或交易日数 <
//     MinTradingDays）→ insufficient（不是 failed）；
//  3. 其余：全部已配置门禁通过 → passed；任一失败 → failed；
//  4. passed 要求聚合证据等级不低于 EvidenceRequirement（exploratory 输入
//     不可能得到正式 passed）。
//
// 结论由冻结 spec 唯一决定，无测试后选择路径。
func (s PortfolioValidationSpec) Evaluate(windows []ValidationWindowOutcome) (ValidationResult, error) {
	if err := s.Validate(); err != nil {
		return ValidationResult{}, err
	}
	if len(windows) == 0 {
		return ValidationResult{}, fmt.Errorf("窗口结果列表不能为空")
	}
	for i, w := range windows {
		if err := w.Validate(); err != nil {
			return ValidationResult{}, fmt.Errorf("windows[%d]: %w", i, err)
		}
	}
	aggEvidence, err := AggregateEvidenceClass(windows)
	if err != nil {
		return ValidationResult{}, err
	}

	// 1. 执行错误优先：error，不评估统计门禁。
	var errIdx []int
	for _, w := range windows {
		if w.State == WindowStateError {
			errIdx = append(errIdx, w.Index)
		}
	}
	valid := validWindows(windows)
	if len(errIdx) > 0 {
		// 门禁披露传入真实有效窗数/交易日数（如有已知值），与 insufficient
		// 路径一致：error 结论也逐项披露已知 Actual，不伪装统计失败。
		tradingDays := 0
		for _, w := range valid {
			tradingDays += w.Metrics.TradingDays
		}
		gates := s.gatesForNonStatVerdict(len(valid), tradingDays, fmt.Sprintf("存在窗口执行错误（窗 %v），结论为 error，不评估统计门禁", errIdx))
		return ValidationResult{
			Verdict: GateStatusError, EvidenceClass: aggEvidence, Gates: gates,
			WindowCount: len(windows), ValidWindowCount: len(valid),
			Message: fmt.Sprintf("存在窗口执行错误（%d 个窗口），结论为 error", len(errIdx)),
		}, nil
	}

	if len(valid) == 0 {
		gates := s.gatesForNonStatVerdict(0, 0, "无有效窗口，结论为 insufficient，不评估统计门禁")
		return ValidationResult{
			Verdict: GateStatusInsufficient, EvidenceClass: aggEvidence, Gates: gates,
			WindowCount: len(windows), ValidWindowCount: 0,
			Message: "无有效测试窗口（可用窗口不足），结论为 insufficient",
		}, nil
	}

	// 2. 有效窗口不足 → insufficient（不是 failed）。
	tradingDays := 0
	for _, w := range valid {
		tradingDays += w.Metrics.TradingDays
	}
	if s.Gates.MinValidWindows > 0 && len(valid) < s.Gates.MinValidWindows {
		gates := s.gatesForNonStatVerdict(len(valid), tradingDays,
			fmt.Sprintf("有效窗口 %d < 要求 %d，结论为 insufficient，不评估统计门禁", len(valid), s.Gates.MinValidWindows))
		return ValidationResult{
			Verdict: GateStatusInsufficient, EvidenceClass: aggEvidence, Gates: gates,
			WindowCount: len(windows), ValidWindowCount: len(valid),
			Message: fmt.Sprintf("有效窗口 %d 不足（要求 >= %d），结论为 insufficient", len(valid), s.Gates.MinValidWindows),
		}, nil
	}
	if s.Gates.MinTradingDays > 0 && tradingDays < s.Gates.MinTradingDays {
		gates := s.gatesForNonStatVerdict(len(valid), tradingDays,
			fmt.Sprintf("有效交易日 %d < 要求 %d，结论为 insufficient，不评估统计门禁", tradingDays, s.Gates.MinTradingDays))
		return ValidationResult{
			Verdict: GateStatusInsufficient, EvidenceClass: aggEvidence, Gates: gates,
			WindowCount: len(windows), ValidWindowCount: len(valid),
			Message: fmt.Sprintf("有效交易日 %d 不足（要求 >= %d），结论为 insufficient", tradingDays, s.Gates.MinTradingDays),
		}, nil
	}

	// 3. 聚合门禁求值：全部已配置门禁通过 → passed；任一失败 → failed。
	gates := s.evaluateAggregateGates(valid, aggEvidence)
	if anyGateFailed(gates) {
		return ValidationResult{
			Verdict: GateStatusFailed, EvidenceClass: aggEvidence, Gates: gates,
			WindowCount: len(windows), ValidWindowCount: len(valid),
			Message: "存在未通过的门禁，结论为 failed",
		}, nil
	}
	return ValidationResult{
		Verdict: GateStatusPassed, EvidenceClass: aggEvidence, Gates: gates,
		WindowCount: len(windows), ValidWindowCount: len(valid),
		Message: "全部冻结门禁通过（passed 只表示通过冻结协议，不表示未来盈利保证）",
	}, nil
}

// EvaluateWindowGates 单窗逐项门禁（结论可逐窗解释）。只评估窗级可解释的
// 门禁（净收益/回撤/成本/换手/现金/未成交/集中度/信息比率/基线增量/证据）；
// 聚合级门禁（窗口数/交易日/方向一致性/超额稳定性/降级窗口数）由 Evaluate
// 在汇总层评估。窗口状态非 ok 时全部 unknown。
func (s PortfolioValidationSpec) EvaluateWindowGates(w ValidationWindowOutcome) []GateResult {
	req := s.ResolveEvidenceRequirement()
	if w.State != WindowStateOK {
		reason := fmt.Sprintf("窗口状态 %s（%s），不评估窗级门禁", w.State, w.Message)
		names := []string{
			"net_return", "max_drawdown", "cost_drag", "turnover",
			"cash_residual", "unfilled_rate", "concentration",
			"information_ratio", "baseline_increment", "evidence_requirement",
		}
		out := make([]GateResult, 0, len(names))
		for _, n := range names {
			out = append(out, gateResult(n, "—", "—", gateUnknown, reason))
		}
		return out
	}
	m := w.Metrics
	return []GateResult{
		gateFloatGE("net_return", s.Gates.MinNetReturn, m.NetReturn),
		gateFloatGE("max_drawdown", s.Gates.MaxDrawdown, m.MaxDrawdown),
		gateFloatLE("cost_drag", s.Gates.MaxCostDrag, m.CostDrag),
		gateFloatLE("turnover", s.Gates.MaxTurnover, m.AnnualTurnover),
		gateFloatLE("cash_residual", s.Gates.MaxCashResidual, m.AvgCashRatio),
		gateFloatLE("unfilled_rate", s.Gates.MaxUnfilledRate, m.UnfilledRate),
		gateFloatLE("concentration", s.Gates.MaxConcentration, m.MaxConcentration),
		gateIRWindow(s.Gates.MinInformationRatio, m.InformationRatio),
		gateFloatGE("baseline_increment", s.Gates.MinBaselineIncrement, m.BaselineIncrement),
		gateEvidence(req, w.EvidenceClass),
	}
}

// gatesForNonStatVerdict error/insufficient 结论的门禁披露：全部 unknown +
// 统一原因；已知的窗口数/交易日数写入 Actual（结论可逐窗解释，不伪装统计
// 失败）。
func (s PortfolioValidationSpec) gatesForNonStatVerdict(validCount, tradingDays int, reason string) []GateResult {
	out := make([]GateResult, 0, len(gateNames))
	for _, name := range gateNames {
		actual := "—"
		switch name {
		case "min_valid_windows":
			actual = fmt.Sprintf("%d", validCount)
		case "min_trading_days":
			actual = fmt.Sprintf("%d", tradingDays)
		}
		out = append(out, gateResult(name, "—", actual, gateUnknown, reason))
	}
	return out
}

// windowAggregate 有效窗聚合统计（结论口径：等权均值 / 最差回撤 / 最大集中度）。
type windowAggregate struct {
	TradingDays          int
	NetReturn            float64
	MaxDrawdown          float64
	CostDrag             float64
	Turnover             float64
	CashResidual         float64
	UnfilledRate         float64
	Concentration        float64
	DirectionConsistency float64
	ExcessStability      float64
	BaselineIncrement    float64
	IRSum                float64
	IRWindows            int
	DegradedWindows      int
}

// aggregateWindowMetrics 从有效窗聚合统计（纯函数；n >= 1 由调用方保证）。
func aggregateWindowMetrics(valid []ValidationWindowOutcome) windowAggregate {
	n := len(valid)
	agg := windowAggregate{MaxDrawdown: 0}
	for _, w := range valid {
		m := w.Metrics
		agg.TradingDays += m.TradingDays
		agg.NetReturn += m.NetReturn
		if m.MaxDrawdown < agg.MaxDrawdown {
			agg.MaxDrawdown = m.MaxDrawdown
		}
		agg.CostDrag += m.CostDrag
		agg.Turnover += m.AnnualTurnover
		agg.CashResidual += m.AvgCashRatio
		agg.UnfilledRate += m.UnfilledRate
		if m.MaxConcentration > agg.Concentration {
			agg.Concentration = m.MaxConcentration
		}
		if m.NetReturn > 0 {
			agg.DirectionConsistency++
		}
		if m.AnnualExcess != nil && *m.AnnualExcess > 0 {
			agg.ExcessStability++
		}
		agg.BaselineIncrement += m.BaselineIncrement
		if m.InformationRatio != nil {
			agg.IRSum += *m.InformationRatio
			agg.IRWindows++
		}
		for _, ev := range w.QualityEvents {
			if ev.Degraded {
				agg.DegradedWindows++
				break
			}
		}
	}
	agg.NetReturn /= float64(n)
	agg.CostDrag /= float64(n)
	agg.Turnover /= float64(n)
	agg.CashResidual /= float64(n)
	agg.UnfilledRate /= float64(n)
	agg.DirectionConsistency /= float64(n)
	agg.ExcessStability /= float64(n)
	agg.BaselineIncrement /= float64(n)
	return agg
}

// evaluateAggregateGates 聚合门禁求值（只在使用足够有效窗时调用）。
// 全部 15 项规范门禁逐项披露：已配置 → pass/fail，未配置 → unknown。
func (s PortfolioValidationSpec) evaluateAggregateGates(valid []ValidationWindowOutcome, aggEvidence string) []GateResult {
	g := s.Gates
	req := s.ResolveEvidenceRequirement()
	agg := aggregateWindowMetrics(valid)
	return []GateResult{
		gateCountGE("min_valid_windows", g.MinValidWindows, len(valid)),
		gateCountGE("min_trading_days", g.MinTradingDays, agg.TradingDays),
		gateFloatGE("net_return", g.MinNetReturn, agg.NetReturn),
		gateFloatGE("max_drawdown", g.MaxDrawdown, agg.MaxDrawdown),
		gateFloatLE("cost_drag", g.MaxCostDrag, agg.CostDrag),
		gateIR(g.MinInformationRatio, agg.IRSum, agg.IRWindows, len(valid)),
		gateFloatGE("excess_stability", g.MinExcessStability, agg.ExcessStability),
		gateFloatLE("turnover", g.MaxTurnover, agg.Turnover),
		gateFloatLE("cash_residual", g.MaxCashResidual, agg.CashResidual),
		gateFloatLE("unfilled_rate", g.MaxUnfilledRate, agg.UnfilledRate),
		gateFloatLE("concentration", g.MaxConcentration, agg.Concentration),
		gateFloatGE("direction_consistency", g.MinDirectionConsistency, agg.DirectionConsistency),
		gateFloatGE("baseline_increment", g.MinBaselineIncrement, agg.BaselineIncrement),
		gateCountLE("max_degraded_windows", g.MaxDegradedWindows, agg.DegradedWindows),
		gateEvidence(req, aggEvidence),
	}
}

// ---- 汇总辅助 ----

// AggregateEvidenceClass 证据等级汇总（设计 §4.1）：任一输入证据降级时输出
// 取最弱值（min），后端不允许提升。
func AggregateEvidenceClass(windows []ValidationWindowOutcome) (string, error) {
	classes := make([]string, len(windows))
	for i, w := range windows {
		classes[i] = w.EvidenceClass
	}
	return CombineEvidenceClass(classes)
}

// validWindows 过滤有效（ok）窗口。
func validWindows(windows []ValidationWindowOutcome) []ValidationWindowOutcome {
	var out []ValidationWindowOutcome
	for _, w := range windows {
		if w.State == WindowStateOK {
			out = append(out, w)
		}
	}
	return out
}

// anyGateFailed 任一已配置门禁失败（unknown 不视为失败——未配置门禁不参与
// 结论）。
func anyGateFailed(gates []GateResult) bool {
	for _, g := range gates {
		if g.Result == gateFail {
			return true
		}
	}
	return false
}
