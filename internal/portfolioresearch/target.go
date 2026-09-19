// target.go 目标组合与约束管线（v2 设计 §8）。
//
// 固定约束顺序（§8.2）：
//
//	候选排名 → Top N / top quantile → 初始等权 → 单票上限 → 行业上限
//	→ 可交易性预检查 → 换手预算 → 整手与现金可行化 → 目标权重与目标股数
//
// 文档化口径与决策（实现契约）：
//   - 并列分数按代码字典序升序破平（确定性选择，不依赖输入顺序）；
//   - top_quantile 数量 = ceil(q×n)，至少 1，最多 n；
//   - 候选不足 TopN 时全选；全选仍不足 MinHoldings → insufficient（selection 步）；
//   - 空选日 / 零可交易候选：MinHoldings=0 时输出空目标（全额现金，表示当日跳过），
//     否则 insufficient（selection / tradability 步）；
//   - 单票/行业上限释放的权重按"剩余空间"水填充分配，保持权重和 = 1 - CashBuffer；
//     上限过紧无法达到权重和 → insufficient（不静默放宽）；
//   - 换手口径：单边换手 = Σ|Δw| / 2（纯权重口径，§8.3）；换手预算不足以达到目标
//     权重和，或现有持仓超上限且预算不足以修正 → insufficient（turnover 步）；
//   - 换手算法（两阶段）：先以最小换手 |ΣΔ|/2 对齐权重和，再在剩余预算内沿理想
//     方向线性混合；总换手 ≤ 预算（三角不等式上界）；期初不可交易持仓
//     （Tradable=false，停牌/涨跌停/上市状态等）在换手求解中受保护：保持期初权重
//     不变，不产生"虚拟卖出"，可交易股票在扣除锁定权重后的预算内调整；
//     锁定权重超过目标权重和或最小换手仍超预算 → insufficient（turnover 步）；
//   - 换手步收尾校验单票/行业上限（前序步骤优先级更高）：期初锁定持仓合并回目标
//     后不得突破更前序的上限约束，超限且预算不足以修正 → insufficient（turnover 步）；
//   - 纯现金配置（CashBuffer=1 → targetSum=0）：无可投资权重，换手步直接跳过，
//     输出全零权重目标（全额现金），避免 targetSum 除零产生 NaN；
//   - 现金可行性：权重口径下买入恒可行（整手向下取整只减少买入成本）；"现金不足"
//     指目标金额不足一手的买入，按分数优先（分数低者先放弃）缩减到 0 股；
//   - 整手向下取整：Shares = floor(w×Notional/(Price×Lot))×Lot；可执行权重
//     Effective = Shares×Price/Notional；取整差额计入现金；
//   - 输出两套目标（§8.3）：理想目标（无换手约束）与约束后目标，供归因比较；
//   - 输出不变量：Weights 无 NaN/负权重、Σ ≈ 1-CashBuffer；Shares ≥ 0 且为整手
//     倍数；Cash = 1 - ΣEffective ≥ CashBuffer（现金残留只会增加现金）。
//
// 约束冲突返回结构化 *InsufficientError（含约束步骤与原因），不自动放宽。
package portfolioresearch

import (
	"fmt"
	"math"
	"sort"
)

// sumTolerance 权重和/上限校验容差（水填充与两阶段混合的浮点累积误差）。
const sumTolerance = 1e-6

// ---- 输入类型 ----

// TargetInput 单日目标组合输入（signalAt 收盘后可见数据，不含未来信息）。
// Tradable 缺失 = 不可交易（fail closed，与步骤 6 同口径）；期初不可交易持仓
// （Holdings 中 Tradable=false）在换手求解中受保护：保持期初权重不变，不产生"虚拟卖出"。
type TargetInput struct {
	Date       string             // 信号日 YYYY-MM-DD
	Scores     map[string]float64 // 代码 → 合成分数（CombineResult.Scores；NaN/±Inf 视为非法输入）
	Tradable   map[string]bool    // 代码 → 当日可交易（停牌/涨跌停/上市状态等，调用方标记；缺失 = 不可交易，fail closed）
	Industries map[string]string  // 代码 → 行业（当日只读数据；仅行业上限启用时需要，缺行业 = 输入错误）
	Prices     map[string]float64 // 代码 → 参考价（元，>0 有限数；选中股票缺价格 = 输入错误）
	Holdings   map[string]float64 // 期初持仓权重（代码 → 权重，用于换手预算；ΣHoldings + CashWeight ≈ 1）
	CashWeight float64            // 期初现金权重 [0,1]（与 Holdings 共同描述期初组合）
	Notional   float64            // 组合名义金额（元，>0；权重 → 股数换算基准）
}

// ---- 输出类型 ----

// ConstraintAdjustment 单步约束调整记录（每步必须输出前值/后值/原因/受影响股票）。
// 选股阶段（ranking/selection）无权重变化，Before/After 为空 map。
type ConstraintAdjustment struct {
	Step           string             // 步骤名（Step* 常量）
	Before         map[string]float64 // 调整前权重（代码 → 权重，快照）
	After          map[string]float64 // 调整后权重（代码 → 权重，快照）
	Reason         string             // 调整原因
	AffectedStocks []string           // 受影响股票（代码升序；无则空切片）
}

// 约束步骤常量（固定顺序，见文件头注释；整手与现金可行化为一步的两个子记录）。
const (
	StepRanking         = "ranking"          // 候选排名
	StepSelection       = "selection"        // Top N / top quantile
	StepEqualWeight     = "equal_weight"     // 初始等权
	StepStockCap        = "stock_cap"        // 单票上限
	StepIndustryCap     = "industry_cap"     // 行业上限
	StepTradability     = "tradability"      // 可交易性预检查
	StepTurnover        = "turnover"         // 换手预算
	StepLotRounding     = "lot_rounding"     // 整手与现金可行化：整手向下取整
	StepCashFeasibility = "cash_feasibility" // 整手与现金可行化：现金可行性（按分数优先缩减）
	StepTarget          = "target"           // 目标权重与目标股数
)

// PortfolioWeights 一套目标权重与目标股数。
//
// Weights 为约束求解后的分析权重（研究意图，Σ ≈ 1-CashBuffer，无 NaN/负权重）；
// Shares/Effective 为整手向下取整后的可执行目标（Effective = Shares×Price/Notional，
// Σ(Effective) ≤ Σ(Weights)，差额为整手现金残留）。两份口径并存，供归因比较
// （设计 §4.5：目标权重与实际可执行是两个事实）。
type PortfolioWeights struct {
	Weights   map[string]float64 // 分析目标权重（代码 → 权重）
	Shares    map[string]int     // 目标股数（整手向下取整，≥0 且为整手倍数）
	Effective map[string]float64 // 可执行权重（股数×价格/名义金额）
	Cash      float64            // 现金权重 = 1 - ΣEffective（含现金缓冲与整手残留）
}

// TargetPortfolio 单日目标组合：同时输出理想目标（无换手约束）与约束后目标（§8.3）。
// IdealAdjustments 为理想目标流水线的逐步调整（不含换手预算步）；
// Adjustments 为约束后目标流水线的逐步调整（含换手预算步）。
type TargetPortfolio struct {
	Date             string
	Ideal            PortfolioWeights
	Constrained      PortfolioWeights
	IdealAdjustments []ConstraintAdjustment
	Adjustments      []ConstraintAdjustment
}

// InsufficientError 约束无解的结构化错误（设计 §8.2：不能静默放宽约束）。
// Step 为导致无解的固定约束步骤，Reason 为可解释原因。
type InsufficientError struct {
	Step   string
	Reason string
}

// Error 实现 error 接口。
func (e *InsufficientError) Error() string {
	return fmt.Sprintf("目标组合无解（步骤 %s）: %s", e.Step, e.Reason)
}

// ---- 求解器 ----

// targetSolver 单日目标组合求解上下文（BuildTarget 每次调用的局部状态，无包级可变状态）。
type targetSolver struct {
	policy    PortfolioPolicy
	exec      ExecutionSpec
	input     TargetInput
	targetSum float64 // 目标权重和 = 1 - CashBuffer
}

// BuildTarget 生成单日目标组合（v2 设计 §8）。
//
// 按 §8.2 固定顺序执行约束；同时输出理想目标（无换手约束）与约束后目标。
// 输入/参数非法返回普通 error；约束冲突无法满足（最小持仓数或权重和）返回
// 结构化 *InsufficientError。
func BuildTarget(policy PortfolioPolicy, exec ExecutionSpec, input TargetInput) (TargetPortfolio, error) {
	if err := policy.Validate(DefaultModelLimits()); err != nil {
		return TargetPortfolio{}, err
	}
	if err := exec.Validate(); err != nil {
		return TargetPortfolio{}, err
	}
	if err := validateTargetInput(input); err != nil {
		return TargetPortfolio{}, err
	}
	// 行业上限启用时期初持仓必须有行业（否则换手步把不可交易持仓合并回组合时，
	// 行业汇总会把缺行业股票误归入空行业，导致上限校验失真），缺行业 = 输入错误。
	if policy.MaxIndustryWeight > 0 {
		if err := validateHoldingsIndustries(input.Holdings, input.Industries); err != nil {
			return TargetPortfolio{}, err
		}
	}
	s := &targetSolver{
		policy:    policy,
		exec:      exec,
		input:     input,
		targetSum: 1 - policy.CashBuffer,
	}
	tp := TargetPortfolio{Date: input.Date}

	ideal, idealAdj, err := s.solve(false)
	if err != nil {
		return tp, err
	}
	tp.Ideal, tp.IdealAdjustments = ideal, idealAdj

	constrained, adj, err := s.solve(true)
	if err != nil {
		return tp, err
	}
	tp.Constrained, tp.Adjustments = constrained, adj
	return tp, nil
}

// validateTargetInput 输入校验（fail closed）。
func validateTargetInput(in TargetInput) error {
	if in.Date == "" {
		return fmt.Errorf("目标组合输入缺少日期")
	}
	if math.IsNaN(in.Notional) || math.IsInf(in.Notional, 0) || in.Notional <= 0 {
		return fmt.Errorf("notional 无效: %v（应为 >0 的有限金额）", in.Notional)
	}
	for c, sc := range in.Scores {
		if c == "" {
			return fmt.Errorf("scores 存在空代码")
		}
		if math.IsNaN(sc) || math.IsInf(sc, 0) {
			return fmt.Errorf("scores[%s] 非有限: %v", c, sc)
		}
	}
	holdingsSum := 0.0
	for c, h := range in.Holdings {
		if c == "" {
			return fmt.Errorf("holdings 存在空代码")
		}
		if math.IsNaN(h) || math.IsInf(h, 0) || h < 0 {
			return fmt.Errorf("holdings[%s] 非法: %v（应为有限非负权重）", c, h)
		}
		holdingsSum += h
	}
	if math.IsNaN(in.CashWeight) || math.IsInf(in.CashWeight, 0) || in.CashWeight < 0 || in.CashWeight > 1 {
		return fmt.Errorf("cashWeight 非法: %v（应为 [0,1]）", in.CashWeight)
	}
	if math.Abs(holdingsSum+in.CashWeight-1) > 1e-6 {
		return fmt.Errorf("期初持仓权重和 %.6g + 现金权重 %.6g ≠ 1（换手预算需要一致期初组合）", holdingsSum, in.CashWeight)
	}
	return nil
}

// validateHoldingsIndustries 校验期初持仓股票的行业（仅在行业上限启用时由
// BuildTarget 调用）：期初持仓缺少行业会把行业汇总误归入空行业，导致行业上限
// 校验失真（含换手步受保护的不可交易持仓），缺行业 = 输入错误（fail closed）。
func validateHoldingsIndustries(holdings map[string]float64, industries map[string]string) error {
	for c := range holdings {
		if industries[c] == "" {
			return fmt.Errorf("行业上限启用时期初持仓股票 %s 缺少行业数据（Holdings[%s]=%.6g）", c, c, holdings[c])
		}
	}
	return nil
}

// solve 执行完整约束流水线。applyTurnover=false 时跳过换手预算步（理想目标）。
func (s *targetSolver) solve(applyTurnover bool) (PortfolioWeights, []ConstraintAdjustment, error) {
	var adj []ConstraintAdjustment

	// 步骤 1：候选排名（分数降序，并列按代码字典序升序破平）。
	ranked := s.rankCandidates()
	adj = append(adj, newAdjustment(StepRanking, nil, nil,
		"候选按合成分数降序排名，并列按代码字典序升序破平", ranked))

	// 步骤 2：Top N / top quantile 选择。
	selected := s.selectNames(ranked)
	if len(selected) < s.policy.MinHoldings {
		return PortfolioWeights{}, adj, &InsufficientError{
			Step: StepSelection,
			Reason: fmt.Sprintf("入选股票数 %d 小于最小持仓数 %d（候选不足 TopN 时全选，仍不足即无解）",
				len(selected), s.policy.MinHoldings),
		}
	}
	adj = append(adj, newAdjustment(StepSelection, nil, nil,
		fmt.Sprintf("选择 TopN=%d / topQuantile=%.4g，入选 %d 只", s.policy.TopN, s.policy.TopQuantile, len(selected)),
		selected))

	// 步骤 3：初始等权（权重和 = 1 - CashBuffer）。
	w := make(map[string]float64, len(selected))
	if len(selected) > 0 {
		ew := s.targetSum / float64(len(selected))
		for _, c := range selected {
			w[c] = ew
		}
	}
	adj = append(adj, newAdjustment(StepEqualWeight, nil, copyMap(w),
		fmt.Sprintf("初始等权：每只 %.6g，权重和 = 1 - CashBuffer = %.6g", s.targetSum/float64(max(1, len(selected))), s.targetSum),
		selected))

	// 步骤 4：单票上限（0 = 不设限）。
	before := copyMap(w)
	w, err := s.capSolve(w, nil, s.policy.MaxStockWeight, 0, StepStockCap)
	if err != nil {
		return PortfolioWeights{}, adj, err
	}
	adj = append(adj, newAdjustment(StepStockCap, before, copyMap(w),
		capReason("单票上限", s.policy.MaxStockWeight, before, w), diffCodes(before, w)))

	// 步骤 5：行业上限（0 = 不设限；启用时选中股票必须有行业）。
	if s.policy.MaxIndustryWeight > 0 {
		for _, c := range sortedKeys(w) {
			if s.input.Industries[c] == "" {
				return PortfolioWeights{}, adj, fmt.Errorf("行业上限启用时选中股票 %s 缺少行业数据", c)
			}
		}
	}
	before = copyMap(w)
	w, err = s.capSolve(w, s.input.Industries, s.policy.MaxStockWeight, s.policy.MaxIndustryWeight, StepIndustryCap)
	if err != nil {
		return PortfolioWeights{}, adj, err
	}
	adj = append(adj, newAdjustment(StepIndustryCap, before, copyMap(w),
		capReason("行业上限", s.policy.MaxIndustryWeight, before, w), diffCodes(before, w)))

	// 步骤 6：可交易性预检查（缺失标记 = 不可交易，fail closed；释放权重重新分配）。
	before = copyMap(w)
	removed := s.removeUntradable(w)
	if len(w) < s.policy.MinHoldings {
		return PortfolioWeights{}, adj, &InsufficientError{
			Step: StepTradability,
			Reason: fmt.Sprintf("可交易股票数 %d 小于最小持仓数 %d（不可交易剔除 %d 只）",
				len(w), s.policy.MinHoldings, len(removed)),
		}
	}
	if len(removed) > 0 {
		w, err = s.capSolve(w, s.input.Industries, s.policy.MaxStockWeight, s.policy.MaxIndustryWeight, StepTradability)
		if err != nil {
			return PortfolioWeights{}, adj, err
		}
	}
	reason := "全部候选可交易，无剔除"
	if len(removed) > 0 {
		reason = fmt.Sprintf("剔除不可交易 %d 只（停牌/涨跌停/上市状态等），释放权重按剩余空间重新分配", len(removed))
	}
	adj = append(adj, newAdjustment(StepTradability, before, copyMap(w), reason, removed))

	// 步骤 7：换手预算（仅约束后目标执行；0 = 不设限）。
	if applyTurnover {
		before = copyMap(w)
		w, err = s.applyTurnover(w)
		if err != nil {
			return PortfolioWeights{}, adj, err
		}
		reason := "换手预算未设置（0 = 不设限），无调整"
		if s.policy.MaxTurnover > 0 {
			reason = fmt.Sprintf("换手预算 %.4g（单边 = Σ|Δw|/2），两阶段求解（先对齐权重和，再向理想靠拢）", s.policy.MaxTurnover)
		}
		adj = append(adj, newAdjustment(StepTurnover, before, copyMap(w), reason, diffCodes(before, w)))
	}

	// 步骤 8：整手与现金可行化（两个子记录）。
	before = copyMap(w)
	shares, effective, residueCodes, err := s.roundToLots(w)
	if err != nil {
		return PortfolioWeights{}, adj, err
	}
	reason = "全部目标股数可直接按整手持有"
	if len(residueCodes) > 0 {
		reason = fmt.Sprintf("整手向下取整 %d 只，取整差额计入现金（分析权重不变，可执行权重见 Effective）", len(residueCodes))
	}
	adj = append(adj, newAdjustment(StepLotRounding, before, copyMap(w), reason, residueCodes))

	// 现金可行性子记录：Before/After 记录可执行权重（Effective）快照，
	// 反映本步"按分数优先缩减买入、可执行权重归 0"的实际变化（分析权重 Weights 不变）。
	effBefore := copyMap(effective)
	dropped := s.shrinkUnaffordableBuys(w, effective)
	reason = "无现金不足（目标金额均达到一手成本）"
	if len(dropped) > 0 {
		reason = "现金不足：目标金额不足一手成本的买入按分数优先缩减（分数低者先放弃），可执行权重归 0"
	}
	adj = append(adj, newAdjustment(StepCashFeasibility, effBefore, copyMap(effective), reason, dropped))
	held := 0
	for _, c := range sortedKeys(w) {
		if effective[c] > sumTolerance {
			held++
		}
	}
	if held < s.policy.MinHoldings {
		return PortfolioWeights{}, adj, &InsufficientError{
			Step: StepCashFeasibility,
			Reason: fmt.Sprintf("整手/现金可行化后可执行持仓数 %d 小于最小持仓数 %d（现金不足放弃 %d 只买入）",
				held, s.policy.MinHoldings, len(dropped)),
		}
	}

	// 步骤 9：目标权重与目标股数（汇总并校验不变量）。
	if len(w) > 0 {
		if ssum := sumMap(w); math.Abs(ssum-s.targetSum) > sumTolerance {
			return PortfolioWeights{}, adj, &InsufficientError{
				Step:   StepTarget,
				Reason: fmt.Sprintf("权重和 %.6g 无法达到目标 %.6g（上限/换手约束冲突）", ssum, s.targetSum),
			}
		}
	}
	for c, v := range w {
		if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
			return PortfolioWeights{}, adj, fmt.Errorf("求解结果权重非法 %s=%v（内部错误）", c, v)
		}
	}
	for c, v := range effective {
		if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
			return PortfolioWeights{}, adj, fmt.Errorf("求解结果可执行权重非法 %s=%v（内部错误）", c, v)
		}
	}
	cash := 1 - sumMap(effective)
	adj = append(adj, newAdjustment(StepTarget, copyMap(w), copyMap(w),
		fmt.Sprintf("输出目标权重与目标股数：持仓 %d 只，权重和 %.6g，现金 %.6g", len(w), sumMap(w), cash),
		sortedKeys(w)))

	return PortfolioWeights{Weights: w, Shares: shares, Effective: effective, Cash: cash}, adj, nil
}

// ---- 步骤 1-2：排名与选择 ----

// rankCandidates 按分数降序、代码升序排名（确定性，并列按代码字典序破平）。
func (s *targetSolver) rankCandidates() []string {
	codes := sortedKeys(s.input.Scores)
	sort.SliceStable(codes, func(i, j int) bool {
		si, sj := s.input.Scores[codes[i]], s.input.Scores[codes[j]]
		if si != sj {
			return si > sj
		}
		return codes[i] < codes[j]
	})
	return codes
}

// selectNames 按选股方式取入选集合（TopN 不足时全选；分位 = ceil(q×n)，至少 1）。
func (s *targetSolver) selectNames(ranked []string) []string {
	n := len(ranked)
	if n == 0 {
		return nil
	}
	k := n
	switch s.policy.Selection {
	case PortfolioSelectionTopN:
		if s.policy.TopN < n {
			k = s.policy.TopN
		}
	case PortfolioSelectionTopQuantile:
		k = int(math.Ceil(s.policy.TopQuantile * float64(n)))
		if k < 1 {
			k = 1
		}
		if k > n {
			k = n
		}
	default:
		// policy.Validate 已保证合法。
		k = n
	}
	return append([]string(nil), ranked[:k]...)
}

// ---- 步骤 4-6：上限水填充 ----

// capSolve 在单票/行业上限下把权重补齐到 targetSum（水填充：按剩余空间比例分配
// 超限释放的权重）。返回新 map（不改入参）。stockCap/industryCap 为 0 表示对应
// 上限未启用。industries 仅在行业上限启用时使用。
// 上限过紧无法达到 targetSum 时返回 *InsufficientError（step 为调用步骤）。
func (s *targetSolver) capSolve(w map[string]float64, industries map[string]string, stockCap, industryCap float64, step string) (map[string]float64, error) {
	cur := copyMap(w)
	stockActive := stockCap > 0
	indActive := industryCap > 0
	if !stockActive && !indActive {
		return cur, nil
	}
	if len(cur) == 0 {
		// 空目标（空选日）：跳过求解，后续步骤按空目标处理。
		return cur, nil
	}
	for iter := 0; iter < 100; iter++ {
		// 1) 单票上限截断。
		if stockActive {
			for _, c := range sortedKeys(cur) {
				if cur[c] > stockCap+sumTolerance {
					cur[c] = stockCap
				}
			}
		}
		// 2) 行业上限截断（行业内按比例缩放，保持行业内相对结构）。
		if indActive {
			for _, ind := range sortedKeys(industryTotals(cur, industries)) {
				tot := industryTotals(cur, industries)[ind]
				if tot > industryCap+sumTolerance {
					scale := industryCap / tot
					for _, c := range sortedKeys(cur) {
						if industries[c] == ind {
							cur[c] *= scale
						}
					}
				}
			}
		}
		ssum := sumMap(cur)
		if math.Abs(ssum-s.targetSum) <= sumTolerance {
			return cur, nil
		}
		if ssum > s.targetSum {
			// 防御：截断只减不加，此分支不应出现。
			return cur, nil
		}
		// 3) 剩余空间水填充：room = min(单票剩余空间, 行业剩余空间)。
		E := s.targetSum - ssum
		indTot := industryTotals(cur, industries)
		room := make(map[string]float64, len(cur))
		roomTotal := 0.0
		for _, c := range sortedKeys(cur) {
			r := math.Inf(1)
			if stockActive {
				r = math.Min(r, stockCap-cur[c])
			}
			if indActive {
				r = math.Min(r, industryCap-indTot[industries[c]])
			}
			if r < 0 {
				r = 0
			}
			room[c] = r
			roomTotal += r
		}
		if roomTotal <= sumTolerance {
			return nil, &InsufficientError{
				Step: step,
				Reason: fmt.Sprintf("上限约束下最大可实现权重和 %.6g 小于目标 %.6g（上限过紧，无法静默放宽）",
					ssum, s.targetSum),
			}
		}
		for _, c := range sortedKeys(cur) {
			cur[c] += E * room[c] / roomTotal
		}
	}
	return nil, &InsufficientError{Step: step, Reason: "上限求解未收敛（内部错误）"}
}

// industryTotals 行业 → 行业权重合计（只统计出现在 w 中的代码）。
func industryTotals(w map[string]float64, industries map[string]string) map[string]float64 {
	tot := make(map[string]float64)
	for c, v := range w {
		tot[industries[c]] += v
	}
	return tot
}

// capReason 上限步骤的调整原因（区分未设置/未触限/已调整）。
func capReason(name string, cap float64, before, after map[string]float64) string {
	if cap <= 0 {
		return fmt.Sprintf("%s未设置（0 = 不设限），无调整", name)
	}
	if len(diffCodes(before, after)) == 0 {
		return fmt.Sprintf("%s %.4g：无股票/行业超限，无调整", name, cap)
	}
	return fmt.Sprintf("%s %.4g：超限权重截断并按剩余空间重新分配", name, cap)
}

// removeUntradable 剔除不可交易股票（原地删除 w 中的键），返回被剔除代码（升序）。
func (s *targetSolver) removeUntradable(w map[string]float64) []string {
	var removed []string
	for _, c := range sortedKeys(w) {
		if !s.input.Tradable[c] { // 缺失标记 = 不可交易（fail closed）
			delete(w, c)
			removed = append(removed, c)
		}
	}
	return removed
}

// ---- 步骤 7：换手预算 ----

// applyTurnover 两阶段换手求解（设计 §8.3，口径：单边换手 = Σ|Δw|/2，纯权重口径）。
//
// 期初不可交易持仓（Holdings 中 Tradable=false，停牌/涨跌停/上市状态等）当日无法
// 交易，在换手求解中受保护：阶段一/二均保持其期初权重不变（不产生"虚拟卖出"），
// 与步骤 6"当日可交易性参与权重求解"语义一致；可交易股票在扣除锁定权重后的
// 目标权重和 targetT = targetSum - Σlock 内调整。
//
// 阶段一（权重和对齐）：以最小换手 |targetT-ΣhT|/2 从可交易持仓向目标投资水平移动；
// 阶段二（结构靠拢）：剩余预算内沿理想方向线性混合（两端点权重和相同，混合保持权重和）。
// 总换手 ≤ 预算（三角不等式上界）。返回新 map（不改入参）。
func (s *targetSolver) applyTurnover(wStar map[string]float64) (map[string]float64, error) {
	if s.policy.MaxTurnover <= 0 {
		return copyMap(wStar), nil
	}
	if len(wStar) == 0 {
		// 空目标（空选日）：换手约束不适用，保持空目标（全额现金）。
		return copyMap(wStar), nil
	}
	T := s.policy.MaxTurnover
	h := s.input.Holdings
	// 锁定集合：期初不可交易持仓，换手求解中保持期初权重不变。
	lock, lockSum := s.lockedHoldings()
	// 换手可调整集合 = 可交易期初持仓（Tradable=true，含 wStar 中的可交易候选）。
	hT := make(map[string]float64, len(h))
	for c, v := range h {
		if s.input.Tradable[c] {
			hT[c] = v
		}
	}
	// 可交易部分的目标权重和 = 总目标权重和 - 锁定权重和。
	targetT := s.targetSum - lockSum
	if targetT < -sumTolerance {
		return nil, &InsufficientError{
			Step: StepTurnover,
			Reason: fmt.Sprintf("不可交易持仓权重和 %.6g 已超过目标权重和 %.6g（换手预算被不可交易持仓锁死，无可交易部分可分配权重，不静默放宽）",
				lockSum, s.targetSum),
		}
	}
	// 容差内负值按 0 处理（锁定恰好占满目标权重和，可交易部分无权重可分配，
	// 避免缩放产生微负权重导致后续"内部错误"误报）。
	if targetT < 0 {
		targetT = 0
	}
	// 纯现金配置（CashBuffer=1 → targetSum=0）：无可投资权重，换手步无调整，
	// 直接返回当前目标（全零权重）。避免下方 wT 缩放处 targetSum 除零产生 NaN，
	// 导致步骤 9 误报"内部错误"。（存在锁定持仓时 targetT<0 已在上方返回
	// insufficient；此处仅剩 targetT=0 的全零目标。）
	if s.targetSum <= sumTolerance {
		return copyMap(wStar), nil
	}
	// 理想可交易目标按锁定占用比例缩放（可交易部分权重和 = targetT）。
	wT := make(map[string]float64, len(wStar))
	for c, v := range wStar {
		wT[c] = v * targetT / s.targetSum
	}
	// 合并锁定持仓后的理想组合（换手口径的最终落点，权重和 = targetSum）。
	ideal := copyMap(wT)
	for c, v := range lock {
		ideal[c] = v
	}
	if tw := turnover(ideal, h); tw <= T+sumTolerance {
		// 直达分支（换手预算充足、直接返回理想落点）也必须校验单票/行业上限：
		// 期初不可交易锁定持仓来自期初组合，不经过步骤 4/5 上限水填充，合并进
		// ideal 后可能突破更前序（优先级更高）的上限约束；与合并后路径共用
		// checkCaps，超限即返回 *InsufficientError（不静默放宽）。
		if err := s.checkCaps(ideal); err != nil {
			return nil, err
		}
		return ideal, nil
	}
	// 可交易部分权重和差 = targetT - ΣhT = targetSum - Σh（锁定权重和抵消）。
	D := s.targetSum - sumMap(h)
	if math.Abs(D)/2 > T+sumTolerance {
		reason := fmt.Sprintf("换手预算 %.4g 不足以从当前持仓（权重和 %.6g）达到目标权重和 %.6g（至少需要 %.6g）",
			T, sumMap(h), s.targetSum, math.Abs(D)/2)
		if lockSum > 0 {
			reason += fmt.Sprintf("；其中不可交易持仓 %d 只（权重和 %.6g）受保护保持不动，不参与换手",
				len(lock), lockSum)
		}
		return nil, &InsufficientError{Step: StepTurnover, Reason: reason}
	}
	// 阶段一：以最小换手对齐可交易部分权重和。
	g := s.alignSum(wT, hT, D)
	// 阶段二：剩余预算内向理想靠拢。
	t2 := T - math.Abs(D)/2
	tw2 := turnover(wT, g)
	w := copyMap(g)
	if tw2 > sumTolerance && t2 > 0 {
		s2 := math.Min(1, t2/tw2)
		for _, c := range sortedKeys(w) {
			if gv, sv := g[c], wT[c]; math.Abs(sv-gv) > sumTolerance {
				w[c] = gv + s2*(sv-gv)
			}
		}
	}
	// 合并锁定持仓：不可交易持仓保持期初权重，不参与换手调整。
	for c, v := range lock {
		w[c] = v
	}
	// 上限校验：换手步不得破坏更前序（优先级更高）的上限约束；
	// 现有持仓超上限且预算不足以修正时无解（fail closed）。
	if err := s.checkCaps(w); err != nil {
		return nil, err
	}
	return w, nil
}

// lockedHoldings 期初不可交易持仓（换手求解中受保护的股票）。
// 返回 锁定权重 map（代码 → 期初权重）与锁定权重和。
// Tradable 缺失 = 不可交易（fail closed，与步骤 6 removeUntradable 同口径）。
func (s *targetSolver) lockedHoldings() (map[string]float64, float64) {
	lock := make(map[string]float64)
	sum := 0.0
	for c, v := range s.input.Holdings {
		if !s.input.Tradable[c] {
			lock[c] = v
			sum += v
		}
	}
	return lock, sum
}

// alignSum 阶段一：以最小换手 |D|/2 把持仓权重和从 Σh 对齐到 targetSum。
// D>0 净买入（按 (w*-h)+ 比例投入）；D<0 净卖出（按 (h-w*)+ 比例卖出）。
func (s *targetSolver) alignSum(wStar, h map[string]float64, D float64) map[string]float64 {
	g := copyMap(wStar)
	for c := range h {
		if _, ok := g[c]; !ok {
			g[c] = 0
		}
	}
	if D > 0 {
		up := 0.0
		for _, c := range sortedKeys(g) {
			up += math.Max(0, wStar[c]-h[c])
		}
		if up <= sumTolerance {
			// 防御：Σ(w*-h)+ ≥ D 恒成立，此分支不应出现；退回按 w* 比例投入。
			// 分母用 w* 自身权重和（换手阶段传入的可交易目标可能已按锁定权重缩放）。
			wt := sumMap(wStar)
			if wt <= sumTolerance {
				return g
			}
			for _, c := range sortedKeys(g) {
				g[c] = h[c] + D*wStar[c]/wt
			}
			return g
		}
		for _, c := range sortedKeys(g) {
			g[c] = h[c] + D*math.Max(0, wStar[c]-h[c])/up
		}
		return g
	}
	down := 0.0
	for _, c := range sortedKeys(g) {
		down += math.Max(0, h[c]-wStar[c])
	}
	if down <= sumTolerance {
		return g
	}
	for _, c := range sortedKeys(g) {
		g[c] = h[c] + D*math.Max(0, h[c]-wStar[c])/down
	}
	return g
}

// checkCaps 换手后校验单票/行业上限（前序步骤优先级更高，违反即无解）。
func (s *targetSolver) checkCaps(w map[string]float64) error {
	if s.policy.MaxStockWeight > 0 {
		for _, c := range sortedKeys(w) {
			if w[c] > s.policy.MaxStockWeight+sumTolerance {
				return &InsufficientError{
					Step: StepTurnover,
					Reason: fmt.Sprintf("现有持仓 %s 权重 %.4g 超过单票上限 %.4g，且换手预算不足以修正",
						c, w[c], s.policy.MaxStockWeight),
				}
			}
		}
	}
	if s.policy.MaxIndustryWeight > 0 {
		for ind, tot := range industryTotals(w, s.input.Industries) {
			if tot > s.policy.MaxIndustryWeight+sumTolerance {
				return &InsufficientError{
					Step: StepTurnover,
					Reason: fmt.Sprintf("现有持仓行业 %s 权重 %.4g 超过行业上限 %.4g，且换手预算不足以修正",
						ind, tot, s.policy.MaxIndustryWeight),
				}
			}
		}
	}
	return nil
}

// ---- 步骤 8：整手与现金可行化 ----

// roundToLots 整手向下取整：Shares = floor(w×Notional/(Price×Lot))×Lot；
// Effective = Shares×Price/Notional。返回股数、可执行权重与产生取整残留的代码。
// 选中股票缺有效价格 = 输入错误（fail closed）。
func (s *targetSolver) roundToLots(w map[string]float64) (map[string]int, map[string]float64, []string, error) {
	shares := make(map[string]int, len(w))
	effective := make(map[string]float64, len(w))
	var residueCodes []string
	for _, c := range sortedKeys(w) {
		if w[c] <= sumTolerance {
			shares[c] = 0
			effective[c] = 0
			continue
		}
		price, ok := s.input.Prices[c]
		if !ok || math.IsNaN(price) || math.IsInf(price, 0) || price <= 0 {
			return nil, nil, nil, fmt.Errorf("选中股票 %s 缺少有效价格数据（fail closed）", c)
		}
		lots := math.Floor(w[c] * s.input.Notional / (price * float64(s.exec.LotSize)))
		sh := int(lots) * s.exec.LotSize
		shares[c] = sh
		eff := float64(sh) * price / s.input.Notional
		effective[c] = eff
		if eff < w[c]-sumTolerance {
			residueCodes = append(residueCodes, c)
		}
	}
	return shares, effective, residueCodes, nil
}

// shrinkUnaffordableBuys 现金可行性：目标金额不足一手成本的买入按分数优先级
// （分数降序、代码升序，分数低者先放弃）缩减，可执行权重归 0。
// 返回被放弃的代码（升序）。分析权重 Weights 保留研究意图，不在此处缩减
// （设计 §4.5：目标权重与可执行是两套口径）。
func (s *targetSolver) shrinkUnaffordableBuys(w, effective map[string]float64) []string {
	buys := make([]string, 0, len(w))
	for _, c := range sortedKeys(w) {
		if w[c] > s.input.Holdings[c]+sumTolerance {
			buys = append(buys, c)
		}
	}
	sort.Slice(buys, func(i, j int) bool {
		si, sj := s.input.Scores[buys[i]], s.input.Scores[buys[j]]
		if si != sj {
			return si > sj
		}
		return buys[i] < buys[j]
	})
	var dropped []string
	for _, c := range buys {
		price := s.input.Prices[c]
		if w[c]*s.input.Notional < price*float64(s.exec.LotSize) {
			effective[c] = 0
			dropped = append(dropped, c)
		}
	}
	sort.Strings(dropped)
	return dropped
}

// ---- map/权重辅助 ----

// newAdjustment 构造调整记录（AffectedStocks 保证非 nil）。
func newAdjustment(step string, before, after map[string]float64, reason string, affected []string) ConstraintAdjustment {
	if before == nil {
		before = map[string]float64{}
	}
	if after == nil {
		after = map[string]float64{}
	}
	if affected == nil {
		affected = []string{}
	}
	return ConstraintAdjustment{Step: step, Before: before, After: after, Reason: reason, AffectedStocks: affected}
}

// copyMap 深拷贝权重 map。
func copyMap(m map[string]float64) map[string]float64 {
	out := make(map[string]float64, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// sortedKeys map 键升序（确定性迭代，避免 map 随机遍历顺序影响结果）。
func sortedKeys(m map[string]float64) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// sumMap 权重和。
func sumMap(m map[string]float64) float64 {
	s := 0.0
	for _, v := range m {
		s += v
	}
	return s
}

// diffCodes 调整前后有差异（超过容差或存在性变化）的代码（升序）。
func diffCodes(before, after map[string]float64) []string {
	keys := map[string]bool{}
	for c := range before {
		keys[c] = true
	}
	for c := range after {
		keys[c] = true
	}
	var diff []string
	for c := range keys {
		bv, av := before[c], after[c]
		if math.Abs(bv-av) > sumTolerance {
			diff = append(diff, c)
		}
	}
	sort.Strings(diff)
	return diff
}

// turnover 单边换手 = Σ|Δw| / 2（权重口径，§8.3 文档化）。
func turnover(w, h map[string]float64) float64 {
	keys := map[string]bool{}
	for c := range w {
		keys[c] = true
	}
	for c := range h {
		keys[c] = true
	}
	s := 0.0
	for c := range keys {
		s += math.Abs(w[c] - h[c])
	}
	return s / 2
}
