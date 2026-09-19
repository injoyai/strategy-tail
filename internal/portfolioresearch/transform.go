// transform.go 截面变换流水线（v2 设计 §6）。
//
// 固定处理顺序：
//
//	原始因子值 → PIT/股票池/可交易性掩码 → 缺失策略 → 去极值
//	→ 可选风险中性化 → 截面标准化 → 方向统一 → 合成输入
//
// 审计边界：
//   - 所有统计（中位数、分位边界、rank/z-score、中性化回归）只用当日截面；
//   - 禁止全样本 Min-Max、跨未来日期填充、NaN→0 隐式转换；
//   - 无包级可变状态；输入顺序不影响按代码对齐的输出。
package portfolioresearch

import (
	"fmt"
	"math"
	"sort"
)

// ---- 输入/输出类型 ----

// CrossSectionRow 当日截面输入行（signalAt 收盘后可见的数据，不含未来信息）。
type CrossSectionRow struct {
	Date          string  // 信号日 YYYY-MM-DD，同一截面所有行必须一致
	Code          string  // 股票代码，同一截面内必须唯一
	Value         float64 // 原始因子值；仅当 Valid 且为有限数时视为有效
	Valid         bool    // 因子值是否有效（false = 缺失/无法计算）
	Tradable      bool    // 是否可交易（PIT/股票池/可交易性掩码结果）
	MissingReason string  // 值无效时的缺失原因（见 MissingReason*）
}

// HasValue 判断该行是否携带有效因子值（Valid 且有限，NaN/±Inf 视为缺失）。
func (r CrossSectionRow) HasValue() bool {
	return r.Valid && !math.IsNaN(r.Value) && !math.IsInf(r.Value, 0)
}

// 缺失原因稳定枚举（质量元数据）。
const (
	MissingReasonNone          = ""               // 无缺失
	MissingReasonNotCalculated = "not_calculated" // 因子无法计算（如历史数据不足）
	MissingReasonNotTradable   = "not_tradable"   // 不可交易（停牌/涨跌停/未上市）
	MissingReasonOther         = "other"          // 其他原因
)

// TransformedRow 变换后单行：保留原始值与最终值的关联索引（审计抽样）。
// Final 为方向统一后的最终变换值；无最终值（掩码/仍缺失）时为 NaN。
type TransformedRow struct {
	Code             string  // 股票代码
	Original         float64 // 原始因子值（输入即无效时为 NaN）
	Final            float64 // 最终变换值（方向统一后；无最终值时为 NaN）
	Masked           bool    // 被掩码剔除（不可交易）
	Missing          bool    // 缺失策略后仍无最终值
	Filled           bool    // 经当日截面中位数填充（cross_section_median）
	FilledWith       float64 // 填充值（当日截面中位数）
	MissingIndicator float64 // 缺失指示暴露：1 = 输入值缺失，0 = 输入值有效
	Winsorized       bool    // 值被去极值截断
	Neutralized      bool    // 值经中性化残差替换
	NeutralizedFrom  float64 // 中性化前值（仅 Neutralized=true 时有意义，设计 §6.3 对照）
	MissingReason    string  // 输入缺失原因（原样透传）
}

// TransformResult 单日截面变换结果。
type TransformResult struct {
	Date        string               // 信号日
	Rows        []TransformedRow     // 按代码升序（与输入顺序无关）
	Diagnostics TransformDiagnostics // 逐步诊断
}

// QualityEvent 质量事件（供审计；Step 为事件来源步骤）。
type QualityEvent struct {
	Step   string `json:"step"`
	Kind   string `json:"kind"`
	Code   string `json:"code,omitempty"`
	Detail string `json:"detail,omitempty"`
}

// 质量事件类型。
const (
	QualityEventInsufficient      = "insufficient"       // 有效股票数不足（去极值跳过/截面无有效股票）
	QualityEventAllMissing        = "all_missing"        // 当日截面全缺失，中位数填充不可用
	QualityEventExposureMissing   = "exposure_missing"   // 风险暴露缺失（行业空串/市值 NaN）
	QualityEventExposureError     = "exposure_error"     // 风险暴露提供者出错
	QualityEventMatrixSingular    = "matrix_singular"    // 中性化设计矩阵奇异
	QualityEventNeutralizeSkipped = "neutralize_skipped" // 中性化按协议降级跳过
	QualityEventZeroStd           = "zero_std"           // 截面零方差，z-score 置 0
)

// StepStats 单步诊断：样本数/缺失数/截断数/回归秩/降级/质量事件。
type StepStats struct {
	Step             string         // 步骤名（Step* 常量）
	Samples          int            // 该步处理的股票数
	MissingCount     int            // 该步缺失/剔除数（mask=掩码数，missing=输入缺失数，neutralize=暴露缺失数）
	TruncatedCount   int            // 去极值截断股票数（仅 winsorize）
	RegressionRank   int            // 中性化设计矩阵秩（仅 neutralize；0 = 未执行）
	ResidualCoverage float64        // 中性化残差覆盖比例 [0,1]（1 = 全部有值股票获得残差）
	Degraded         bool           // 该步是否降级
	QualityEvents    []QualityEvent // 该步质量事件
}

// 步骤名常量（固定处理顺序）。
const (
	StepMask        = "mask"        // PIT/股票池/可交易性掩码
	StepMissing     = "missing"     // 缺失策略
	StepWinsorize   = "winsorize"   // 去极值
	StepNeutralize  = "neutralize"  // 可选风险中性化
	StepStandardize = "standardize" // 截面标准化
	StepDirection   = "direction"   // 方向统一
)

// TransformDiagnostics 逐步诊断（步骤顺序与流水线一致）。
type TransformDiagnostics struct {
	Steps    []StepStats
	Degraded bool // 任一步降级
}

// AllQualityEvents 合并全部质量事件（按步骤顺序保序）。
func (d TransformDiagnostics) AllQualityEvents() []QualityEvent {
	var out []QualityEvent
	for _, s := range d.Steps {
		out = append(out, s.QualityEvents...)
	}
	return out
}

// ---- 中性化接口与运行参数 ----

// RiskExposureProvider 风险暴露提供者（设计 §6.3）。
//
// 只允许返回 signalAt（date 参数，即当日）已可用的数据：行业缺失以空串表示，
// 市值缺失以 NaN/±Inf 表示。实现方不得读取任何未来数据。
type RiskExposureProvider interface {
	Exposures(date string, codes []string) (industries []string, logMktCaps []float64, err error)
}

// 中性化失败协议（设计 §6.3，矩阵奇异或覆盖不足时）。
const (
	NeutralizeFailureFail                = "fail"                  // 失败即整个变换失败（默认，fail closed）
	NeutralizeFailureSkipWithDegradation = "skip_with_degradation" // 降级跳过中性化，继续后续步骤
)

// TransformConfig 变换运行参数（运行时参数，不进入模型语义 hash；
// 语义阈值已由 TransformPipeline 持有）。
type TransformConfig struct {
	MinValidStocks    int    // 去极值所需最小有效股票数（<=0 用 DefaultMinValidStocks）
	NeutralizeFailure string // 中性化失败协议（空 = fail）
}

// DefaultMinValidStocks 去极值所需最小有效股票数默认值。
const DefaultMinValidStocks = 30

func (c TransformConfig) minValidStocks() int {
	if c.MinValidStocks <= 0 {
		return DefaultMinValidStocks
	}
	return c.MinValidStocks
}

func (c TransformConfig) neutralizeFailure() string {
	if c.NeutralizeFailure == "" {
		return NeutralizeFailureFail
	}
	return c.NeutralizeFailure
}

// ---- 变换流水线 ----

// stockState 流水线内部工作行（值随步骤演进；输入/输出类型与内部状态分离）。
type stockState struct {
	row              CrossSectionRow
	hasValue         bool
	value            float64
	masked           bool
	missing          bool
	filled           bool
	fillValue        float64
	missingIndicator float64
	winsorized       bool
	neutralized      bool
	neutralizedFrom  float64
}

// Transform 按 TransformPipeline 固定顺序处理单日截面（设计 §6）。
//
// pipeline 须先通过 Validate；direction 为冻结因子方向（测试窗反向不允许
// 自动翻转）。exposures 仅在 Neutralize.Mode != none 时必填。返回按代码
// 升序的输出行与逐步诊断；中性化失败协议为 fail 时返回错误。
func Transform(pipeline TransformPipeline, cfg TransformConfig, direction string, rows []CrossSectionRow, exposures RiskExposureProvider) (TransformResult, error) {
	if err := pipeline.Validate(); err != nil {
		return TransformResult{}, err
	}
	switch direction {
	case DirectionHigherIsBetter, DirectionLowerIsBetter:
	default:
		return TransformResult{}, fmt.Errorf("direction 非法: %q（应为 higher_is_better | lower_is_better）", direction)
	}
	if pipeline.Neutralize.Mode != TransformNeutralizeNone && exposures == nil {
		return TransformResult{}, fmt.Errorf("neutralize.mode=%s 需要 RiskExposureProvider", pipeline.Neutralize.Mode)
	}
	switch cfg.neutralizeFailure() {
	case NeutralizeFailureFail, NeutralizeFailureSkipWithDegradation:
	default:
		return TransformResult{}, fmt.Errorf("中性化失败协议非法: %q（应为 fail | skip_with_degradation）", cfg.NeutralizeFailure)
	}

	states, err := newStockStates(rows)
	if err != nil {
		return TransformResult{}, err
	}
	diag := TransformDiagnostics{}

	diag.Steps = append(diag.Steps, applyMask(states))
	diag.Steps = append(diag.Steps, applyMissing(pipeline.Missing, states))
	diag.Steps = append(diag.Steps, applyWinsorize(pipeline.Winsorize, cfg.minValidStocks(), states))
	st, err := applyNeutralize(pipeline.Neutralize, cfg.neutralizeFailure(), states, exposures)
	if err != nil {
		return TransformResult{}, err
	}
	diag.Steps = append(diag.Steps, st)
	diag.Steps = append(diag.Steps, applyStandardize(pipeline.Standardize, states))
	diag.Steps = append(diag.Steps, applyDirection(direction, states))
	for _, s := range diag.Steps {
		if s.Degraded {
			diag.Degraded = true
		}
	}

	date := ""
	if len(rows) > 0 {
		date = rows[0].Date
	}
	return TransformResult{Date: date, Rows: rowsFromStates(states), Diagnostics: diag}, nil
}

// newStockStates 校验输入（日期一致、代码非空且唯一）并按代码排序，
// 生成内部工作行。空截面返回空切片（由后续步骤产生不足事件，不报错）。
func newStockStates(rows []CrossSectionRow) ([]stockState, error) {
	states := make([]stockState, len(rows))
	if len(rows) == 0 {
		return states, nil
	}
	date := rows[0].Date
	seen := make(map[string]struct{}, len(rows))
	for i, r := range rows {
		if r.Date != date {
			return nil, fmt.Errorf("截面日期不一致: %q vs %q", r.Date, date)
		}
		if r.Code == "" {
			return nil, fmt.Errorf("rows[%d] 代码为空", i)
		}
		if _, dup := seen[r.Code]; dup {
			return nil, fmt.Errorf("截面代码重复: %q", r.Code)
		}
		seen[r.Code] = struct{}{}
		states[i] = stockState{row: r, hasValue: r.HasValue(), value: r.Value}
	}
	sort.Slice(states, func(i, j int) bool { return states[i].row.Code < states[j].row.Code })
	return states, nil
}

// applyMask 步骤 1：PIT/股票池/可交易性掩码。不可交易股票剔除出模型截面。
func applyMask(states []stockState) StepStats {
	st := StepStats{Step: StepMask, Samples: len(states)}
	for i := range states {
		if !states[i].row.Tradable {
			states[i].masked = true
			states[i].hasValue = false
			st.MissingCount++
		}
	}
	return st
}

// applyMissing 步骤 2：缺失策略（exclude | cross_section_median | renormalize_available）。
//
// 禁止跨未来日期填充、全样本均值与 NaN→0 隐式转换；中位数只用当日截面。
// 当日截面无任何有效股票时输出不足质量事件。
func applyMissing(policy string, states []stockState) StepStats {
	st := StepStats{Step: StepMissing, Samples: len(states)}
	for i := range states {
		if states[i].masked {
			continue
		}
		if !states[i].hasValue {
			st.MissingCount++
		}
	}
	median, medianOK := crossSectionMedian(states)
	switch policy {
	case TransformMissingExclude:
		// 缺失股票不进入模型截面，标记 Missing（Final=NaN），绝不填 0。
		for i := range states {
			if states[i].masked || states[i].hasValue {
				continue
			}
			states[i].missing = true
			states[i].missingIndicator = 1
		}
	case TransformMissingCrossSectionMedian:
		// 仅用当日截面中位数填充，并输出缺失指示暴露；截面全缺失时降级。
		if !medianOK {
			st.Degraded = true
			st.QualityEvents = append(st.QualityEvents, QualityEvent{
				Step: StepMissing, Kind: QualityEventAllMissing, Detail: "当日截面全缺失，中位数填充不可用",
			})
			for i := range states {
				if states[i].masked || states[i].hasValue {
					continue
				}
				states[i].missing = true
				states[i].missingIndicator = 1
			}
			return st
		}
		for i := range states {
			if states[i].masked || states[i].hasValue {
				continue
			}
			states[i].filled = true
			states[i].fillValue = median
			states[i].missingIndicator = 1
			states[i].value = median
			states[i].hasValue = true
		}
	case TransformMissingRenormalizeAvailable:
		// 显式变体：保留在截面输出中（供组合层按可用因子重归一化权重），
		// 不填充、不参与当日统计。
		for i := range states {
			if states[i].masked || states[i].hasValue {
				continue
			}
			states[i].missing = true
			states[i].missingIndicator = 1
		}
	default:
		// pipeline.Validate 已保证合法；此处防御性返回不足事件。
		st.Degraded = true
		st.QualityEvents = append(st.QualityEvents, QualityEvent{
			Step: StepMissing, Kind: QualityEventInsufficient, Detail: fmt.Sprintf("缺失策略非法: %q", policy),
		})
		return st
	}
	if countHasValue(states) == 0 {
		st.Degraded = true
		st.QualityEvents = append(st.QualityEvents, QualityEvent{
			Step: StepMissing, Kind: QualityEventInsufficient, Detail: "当日截面无有效股票",
		})
	}
	return st
}

// crossSectionMedian 当日截面中位数（只用未掩码且值有效的股票）。
// 无有效值返回 (0, false)。
func crossSectionMedian(states []stockState) (float64, bool) {
	vals := make([]float64, 0, len(states))
	for i := range states {
		if states[i].masked || !states[i].hasValue {
			continue
		}
		vals = append(vals, states[i].value)
	}
	if len(vals) == 0 {
		return 0, false
	}
	return medianOf(vals), true
}

// applyWinsorize 步骤 3：去极值（分位 | MAD）。
//
// 边界只用当日截面；有效股票数不足 minValid 时返回质量事件（降级跳过），
// 绝不沿用上一日边界。分位法用类型 7 线性插值；MAD 法用
// median ± k * 1.4826 * MAD（1.4826 为正态一致性常数）。
func applyWinsorize(spec WinsorizeSpec, minValid int, states []stockState) StepStats {
	st := StepStats{Step: StepWinsorize, Samples: countHasValue(states)}
	if spec.Mode == TransformWinsorizeNone {
		return st
	}
	idx := validValueIndexes(states)
	if len(idx) < minValid {
		st.Degraded = true
		st.QualityEvents = append(st.QualityEvents, QualityEvent{
			Step:   StepWinsorize,
			Kind:   QualityEventInsufficient,
			Detail: fmt.Sprintf("有效股票数 %d 小于最小要求 %d，去极值跳过", len(idx), minValid),
		})
		return st
	}
	vals := valuesAt(states, idx)
	var lo, hi float64
	switch spec.Mode {
	case TransformWinsorizeQuantile:
		lo = quantile(vals, spec.Quantile)
		hi = quantile(vals, 1-spec.Quantile)
	case TransformWinsorizeMAD:
		lo, hi = madBounds(vals, spec.MADK)
	default:
		// pipeline.Validate 已保证合法。
		return st
	}
	for _, i := range idx {
		if states[i].value < lo {
			states[i].value = lo
			states[i].winsorized = true
			st.TruncatedCount++
		} else if states[i].value > hi {
			states[i].value = hi
			states[i].winsorized = true
			st.TruncatedCount++
		}
	}
	return st
}

// exposureRow 中性化回归样本（行业 + 对数市值，暴露已校验完整）。
type exposureRow struct {
	stateIdx int
	industry string
	logSize  float64
}

// applyNeutralize 步骤 4：可选风险中性化（设计 §6.3）。
//
// 对 [1 | 行业哑变量（参考行业剔除）| 对数市值] 做当日截面 OLS，残差作为
// 中性化值。回归样本 = 有值且暴露完整的股票；任一有值股票缺暴露（空行业 /
// NaN 市值）、设计矩阵奇异或提供者出错时按 failurePolicy 处理：fail 返回
// 错误，skip_with_degradation 降级跳过（值保持中性化前状态）。
func applyNeutralize(spec NeutralizeSpec, failurePolicy string, states []stockState, exposures RiskExposureProvider) (StepStats, error) {
	st := StepStats{Step: StepNeutralize, Samples: countHasValue(states)}
	if spec.Mode == TransformNeutralizeNone {
		return st, nil
	}
	idx := validValueIndexes(states)
	if len(idx) == 0 {
		return st, nil
	}
	codes := make([]string, len(idx))
	for k, i := range idx {
		codes[k] = states[i].row.Code
	}
	industries, sizes, err := exposures.Exposures(states[0].row.Date, codes)
	if err != nil {
		return neutralizeFailure(failurePolicy, st, QualityEvent{
			Step: StepNeutralize, Kind: QualityEventExposureError, Detail: err.Error(),
		})
	}
	if len(industries) != len(codes) || len(sizes) != len(codes) {
		return neutralizeFailure(failurePolicy, st, QualityEvent{
			Step: StepNeutralize, Kind: QualityEventExposureError,
			Detail: fmt.Sprintf("暴露返回长度不匹配: industries=%d sizes=%d codes=%d", len(industries), len(sizes), len(codes)),
		})
	}
	rows := make([]exposureRow, 0, len(idx))
	for k, i := range idx {
		ind := industries[k]
		sz := sizes[k]
		if ind == "" || math.IsNaN(sz) || math.IsInf(sz, 0) {
			st.MissingCount++
			continue
		}
		rows = append(rows, exposureRow{stateIdx: i, industry: ind, logSize: sz})
	}
	if len(rows) != len(idx) {
		return neutralizeFailure(failurePolicy, st, QualityEvent{
			Step: StepNeutralize, Kind: QualityEventExposureMissing,
			Detail: fmt.Sprintf("风险暴露缺失 %d/%d 只股票（空行业或 NaN 市值）", len(idx)-len(rows), len(idx)),
		})
	}
	X, y := designMatrix(rows, states)
	beta, rank, err := olsFit(X, y)
	if err != nil {
		return neutralizeFailure(failurePolicy, st, QualityEvent{
			Step: StepNeutralize, Kind: QualityEventMatrixSingular, Detail: err.Error(),
		})
	}
	for k, r := range rows {
		fitted := 0.0
		for c, b := range beta {
			fitted += X[k][c] * b
		}
		i := r.stateIdx
		states[i].neutralizedFrom = states[i].value
		states[i].value -= fitted
		states[i].neutralized = true
	}
	st.RegressionRank = rank
	st.ResidualCoverage = float64(len(rows)) / float64(len(idx))
	return st, nil
}

// neutralizeFailure 按失败协议处理中性化失败：fail 返回错误；
// skip_with_degradation 降级跳过（值保持中性化前状态）并记录事件。
func neutralizeFailure(failurePolicy string, st StepStats, ev QualityEvent) (StepStats, error) {
	switch failurePolicy {
	case NeutralizeFailureSkipWithDegradation:
		st.Degraded = true
		st.QualityEvents = append(st.QualityEvents, ev)
		st.QualityEvents = append(st.QualityEvents, QualityEvent{
			Step: StepNeutralize, Kind: QualityEventNeutralizeSkipped, Detail: "中性化跳过（降级）",
		})
		return st, nil
	case NeutralizeFailureFail:
		return st, fmt.Errorf("中性化失败（%s）: %s", ev.Kind, ev.Detail)
	default:
		return st, fmt.Errorf("中性化失败协议非法: %q", failurePolicy)
	}
}

// designMatrix 构建中性化设计矩阵 [1 | 行业哑变量 | 对数市值] 与因变量 y。
// 行业按字典序排序并剔除首个（参考）行业，避免哑变量陷阱。
func designMatrix(rows []exposureRow, states []stockState) ([][]float64, []float64) {
	indSet := make(map[string]struct{}, len(rows))
	for _, r := range rows {
		indSet[r.industry] = struct{}{}
	}
	inds := make([]string, 0, len(indSet))
	for ind := range indSet {
		inds = append(inds, ind)
	}
	sort.Strings(inds)
	dummies := inds[1:] // 参考行业不设哑变量
	p := 1 + len(dummies) + 1
	X := make([][]float64, len(rows))
	y := make([]float64, len(rows))
	for k, r := range rows {
		row := make([]float64, p)
		row[0] = 1
		for j, ind := range dummies {
			if r.industry == ind {
				row[1+j] = 1
			}
		}
		row[p-1] = r.logSize
		X[k] = row
		y[k] = states[r.stateIdx].value
	}
	return X, y
}

// olsFit 最小二乘拟合：解正规方程 (X'X)β = X'y。
// 采用 Gauss-Jordan 部分主元消去；设计矩阵奇异（秩不足）时返回错误。
// 返回系数 β 与设计矩阵秩。
func olsFit(X [][]float64, y []float64) ([]float64, int, error) {
	n := len(X)
	if n == 0 {
		return nil, 0, fmt.Errorf("无回归样本")
	}
	p := len(X[0])
	xtx := make([][]float64, p)
	xty := make([]float64, p)
	for i := 0; i < p; i++ {
		xtx[i] = make([]float64, p)
		for j := 0; j < p; j++ {
			s := 0.0
			for k := 0; k < n; k++ {
				s += X[k][i] * X[k][j]
			}
			xtx[i][j] = s
		}
		s := 0.0
		for k := 0; k < n; k++ {
			s += X[k][i] * y[k]
		}
		xty[i] = s
	}
	// 增广矩阵 [X'X | X'y]。
	aug := make([][]float64, p)
	maxAbs := 0.0
	for i := 0; i < p; i++ {
		aug[i] = make([]float64, p+1)
		for j := 0; j < p; j++ {
			aug[i][j] = xtx[i][j]
			if a := math.Abs(aug[i][j]); a > maxAbs {
				maxAbs = a
			}
		}
		aug[i][p] = xty[i]
		if a := math.Abs(aug[i][p]); a > maxAbs {
			maxAbs = a
		}
	}
	tol := 1e-10 * math.Max(maxAbs, 1e-300)
	rank := 0
	for col := 0; col < p; col++ {
		pivot := col
		for r := col + 1; r < p; r++ {
			if math.Abs(aug[r][col]) > math.Abs(aug[pivot][col]) {
				pivot = r
			}
		}
		if math.Abs(aug[pivot][col]) <= tol {
			return nil, 0, fmt.Errorf("设计矩阵奇异（第 %d 列主元 %.3g <= 容差 %.3g）", col+1, aug[pivot][col], tol)
		}
		aug[col], aug[pivot] = aug[pivot], aug[col]
		pv := aug[col][col]
		for j := col; j <= p; j++ {
			aug[col][j] /= pv
		}
		for r := 0; r < p; r++ {
			if r == col {
				continue
			}
			f := aug[r][col]
			if f == 0 {
				continue
			}
			for j := col; j <= p; j++ {
				aug[r][j] -= f * aug[col][j]
			}
		}
		rank++
	}
	beta := make([]float64, p)
	for i := 0; i < p; i++ {
		beta[i] = aug[i][p]
	}
	return beta, rank, nil
}

// applyStandardize 步骤 5：截面标准化（rank | zscore）。
//
// 只用当日截面。rank = 平均秩映射 [-1,1]（score = 2*r/(n+1) - 1，并列共享
// 平均秩）；zscore 用截面总体标准差，零方差截面全部置 0 并输出质量事件。
func applyStandardize(mode string, states []stockState) StepStats {
	st := StepStats{Step: StepStandardize, Samples: countHasValue(states)}
	idx := validValueIndexes(states)
	if len(idx) == 0 {
		return st
	}
	vals := valuesAt(states, idx)
	switch mode {
	case TransformStandardizeRank:
		scores := rankScores(vals)
		for k, i := range idx {
			states[i].value = scores[k]
		}
	case TransformStandardizeZScore:
		zs, zeroStd := zScores(vals)
		if zeroStd {
			st.Degraded = true
			st.QualityEvents = append(st.QualityEvents, QualityEvent{
				Step: StepStandardize, Kind: QualityEventZeroStd, Detail: "截面零方差，z-score 全部置 0",
			})
		}
		for k, i := range idx {
			states[i].value = zs[k]
		}
	default:
		// pipeline.Validate 已保证合法。
	}
	return st
}

// applyDirection 步骤 6：方向统一（设计 §6.4）。
//
// 方向由上游验证冻结，把"越好"统一为正方向：higher_is_better 不变，
// lower_is_better 取负。测试窗出现反向不允许自动翻转。
func applyDirection(direction string, states []stockState) StepStats {
	st := StepStats{Step: StepDirection, Samples: countHasValue(states)}
	if direction == DirectionLowerIsBetter {
		for i := range states {
			if states[i].hasValue {
				states[i].value = -states[i].value
			}
		}
	}
	return st
}

// rowsFromStates 映射为输出行（保留原始值/最终值关联索引，按代码升序）。
func rowsFromStates(states []stockState) []TransformedRow {
	out := make([]TransformedRow, len(states))
	for i, s := range states {
		orig := s.row.Value
		if !s.row.HasValue() {
			orig = math.NaN()
		}
		final := math.NaN()
		if s.hasValue && !s.masked && !s.missing {
			final = s.value
		}
		out[i] = TransformedRow{
			Code:             s.row.Code,
			Original:         orig,
			Final:            final,
			Masked:           s.masked,
			Missing:          s.missing,
			Filled:           s.filled,
			FilledWith:       s.fillValue,
			MissingIndicator: s.missingIndicator,
			Winsorized:       s.winsorized,
			Neutralized:      s.neutralized,
			NeutralizedFrom:  s.neutralizedFrom,
			MissingReason:    s.row.MissingReason,
		}
	}
	return out
}

// ---- 统计辅助 ----

// countHasValue 当前有有效值的股票数。
func countHasValue(states []stockState) int {
	n := 0
	for i := range states {
		if states[i].hasValue {
			n++
		}
	}
	return n
}

// validValueIndexes 有有效值股票的下标（按代码排序顺序）。
func validValueIndexes(states []stockState) []int {
	idx := make([]int, 0, len(states))
	for i := range states {
		if states[i].hasValue {
			idx = append(idx, i)
		}
	}
	return idx
}

// valuesAt 按下标取当前值。
func valuesAt(states []stockState, idx []int) []float64 {
	vs := make([]float64, len(idx))
	for k, i := range idx {
		vs[k] = states[i].value
	}
	return vs
}

// medianOf 中位数（偶数个时取中间两值平均）。vals 非空。
func medianOf(vals []float64) float64 {
	sorted := append([]float64(nil), vals...)
	sort.Float64s(sorted)
	n := len(sorted)
	if n%2 == 1 {
		return sorted[n/2]
	}
	return (sorted[n/2-1] + sorted[n/2]) / 2
}

// quantile 分位数（类型 7 线性插值，R 默认口径）。q ∈ [0,1]。
func quantile(vals []float64, q float64) float64 {
	sorted := append([]float64(nil), vals...)
	sort.Float64s(sorted)
	n := len(sorted)
	if n == 1 {
		return sorted[0]
	}
	h := float64(n-1) * q
	lo := int(math.Floor(h))
	hi := int(math.Ceil(h))
	if lo == hi {
		return sorted[lo]
	}
	return sorted[lo] + (h-float64(lo))*(sorted[hi]-sorted[lo])
}

// madBounds MAD 去极值边界：median ± k * 1.4826 * MAD。
// MAD = median(|x - median|)；MAD=0 时边界退化为中位数。
func madBounds(vals []float64, k float64) (float64, float64) {
	med := medianOf(vals)
	devs := make([]float64, len(vals))
	for i, v := range vals {
		devs[i] = math.Abs(v - med)
	}
	mad := medianOf(devs)
	half := k * 1.4826 * mad
	return med - half, med + half
}

// rankScores 平均秩映射 [-1,1]：score = 2*r/(n+1) - 1，r 为 1 起始平均秩。
// 并列值共享平均秩；n=1 时 score=0（无离差）。只用当日截面。
func rankScores(vals []float64) []float64 {
	n := len(vals)
	order := make([]int, n)
	for i := range order {
		order[i] = i
	}
	sort.Slice(order, func(a, b int) bool { return vals[order[a]] < vals[order[b]] })
	ranks := make([]float64, n)
	for i := 0; i < n; {
		j := i + 1
		for j < n && vals[order[j]] == vals[order[i]] {
			j++
		}
		avg := float64(i+1+j) / 2 // 并列块平均秩（1 起始）
		for k := i; k < j; k++ {
			ranks[order[k]] = avg
		}
		i = j
	}
	scores := make([]float64, n)
	for i := 0; i < n; i++ {
		scores[i] = 2*ranks[i]/float64(n+1) - 1
	}
	return scores
}

// zScores 截面 z-score（总体标准差）。截面严格全等（或零方差）时返回
// 全部 0 与 zeroStd=true，避免除零与浮点伪统计（如 0.1*3 产生 1e-17 级方差）。
func zScores(vals []float64) ([]float64, bool) {
	n := len(vals)
	allEqual := true
	for i := 1; i < n; i++ {
		if vals[i] != vals[0] {
			allEqual = false
			break
		}
	}
	if allEqual {
		return make([]float64, n), true
	}
	sum := 0.0
	for _, v := range vals {
		sum += v
	}
	mean := sum / float64(n)
	vsum := 0.0
	for _, v := range vals {
		d := v - mean
		vsum += d * d
	}
	std := math.Sqrt(vsum / float64(n))
	if std == 0 {
		return make([]float64, n), true
	}
	zs := make([]float64, n)
	for i, v := range vals {
		zs[i] = (v - mean) / std
	}
	return zs, false
}
