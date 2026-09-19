// attribution.go 预测归因与组合归因（v2 设计 §10.2/§10.3）。
//
// 限制声明（每个归因输出携带，页面与导出必须展示）：
// v2 归因是统计分解（收益来源的算术/复利分解），不是因果证明，也不是完整风险
// 模型归因（无因子暴露、协方差与风格风险模型）。
//
// 预测归因（§10.2，回答"分数为什么有效"）：
//   - 复用 Task 3 RedundancyReport（相关/覆盖/单因子与合成 IC/边际 IC/留一 IC）；
//   - Task 6 接入 leave-one-factor-out 的组合维度（收益/换手/回撤/IC 变化）：
//     对每因子移除后经 ChainRunner 重跑 合成→目标→执行→会计（或近似：权重变化×收益，
//     方法由调用方文档化），差值 = LOO - Full（正 = 移除后改善）。
//
// 组合归因（§10.3，回答"收益从哪来"）：
//   - 股票贡献（Σ_t 期初权重×当日收益，算术累计）、行业贡献（按行业聚合股票贡献，
//     缺行业归 "unknown"）、现金贡献（期初现金 × 每日无风险利率，rf 年化折算
//     (1+rf)^(1/252)-1）、成本拖累（Σ费用/期初净权益）；
//   - 信号选择收益（理想目标权重下收益）与执行偏离损失（实际-理想，含不可交易/
//     整手偏离，体现在实际权重与理想权重的差异上）；
//   - 目标权重/实际权重/偏离时间序列（偏离 = 实际 - 目标，正 = 超配）。
//
// 恒等式（文档化，算术累计口径，容差 attributionTolerance=1e-9）：
//
//	StockCashSum = Σ股票贡献 + 现金贡献（构造性）；
//	NetReturn    = StockCashSum - CostDrag（构造性）；
//	ExecutionDeviation = StockCashSum - SelectionReturn（IdealWeights 缺失时 nil）。
//
// 与净值口径（复利）的关系：提供 GrossNav/NetNav 时输出
//
//	PortfolioGrossReturn = GrossNav[末]/期初净权益 - 1、
//	PortfolioNetReturn = NetNav[末]/期初净权益 - 1、
//	Residual = PortfolioNetReturn - NetReturn（复利交叉项 + 成交时点/整手偏离；
//	无交易、费用按期初基准且单期时 = 0，显式披露，不静默抹平）。
package portfolioresearch

import (
	"fmt"
	"math"
	"sort"
)

// attributionTolerance 归因恒等式断言容差（相对 1e-9；metrics.go 口径说明引用）。
const attributionTolerance = 1e-9

// attributionLimitation 归因限制声明（设计 §10.3）。
const attributionLimitation = "v2 归因是统计分解（收益来源的算术/复利分解），不是因果证明，也不是完整风险模型归因（无因子暴露、协方差与风格风险模型）"

// ---- 预测归因 ----

// PredictionAttribution 预测归因视图（§10.2：分数为什么有效）。
// Redundancy 为 Task 3 冗余诊断报告（IC 维度）；LOOImpact 为 Task 6 接入的
// 留一法组合维度（收益/换手/回撤/IC 变化）。
type PredictionAttribution struct {
	Redundancy RedundancyReport     `json:"redundancy"`
	LOOImpact  []LOOPortfolioImpact `json:"looImpact"`
	Limitation string               `json:"limitation"`
}

// BuildPredictionAttribution 组装预测归因视图（设计 §10.2）。
func BuildPredictionAttribution(red RedundancyReport, loo []LOOPortfolioImpact) PredictionAttribution {
	return PredictionAttribution{Redundancy: red, LOOImpact: loo, Limitation: attributionLimitation}
}

// PortfolioRunResult 单次组合链路运行的表现结果（留一法组合维度比较用，净口径）。
type PortfolioRunResult struct {
	OutOfSampleIC  *float64 `json:"outOfSampleIc,omitempty"` // 样本外 IC（nil = 不可计算）
	AnnualReturn   float64  `json:"annualReturn"`            // 年化收益
	AnnualTurnover float64  `json:"annualTurnover"`          // 年化换手
	MaxDrawdown    float64  `json:"maxDrawdown"`             // 最大回撤（≤0）
}

// LOOPortfolioImpact 留一因子移除后的组合维度变化（差值 = LOO - Full，正 = 移除后改善）。
type LOOPortfolioImpact struct {
	RemovedFactor      string   `json:"removedFactor"`
	FullIC             *float64 `json:"fullIc,omitempty"`
	LOOIC              *float64 `json:"looIc,omitempty"`
	ICChange           *float64 `json:"icChange,omitempty"`
	FullAnnualReturn   float64  `json:"fullAnnualReturn"`
	LOOAnnualReturn    float64  `json:"looAnnualReturn"`
	AnnualReturnChange float64  `json:"annualReturnChange"`
	FullTurnover       float64  `json:"fullTurnover"`
	LOOTurnover        float64  `json:"looTurnover"`
	TurnoverChange     float64  `json:"turnoverChange"`
	FullMaxDrawdown    float64  `json:"fullMaxDrawdown"`
	LOOMaxDrawdown     float64  `json:"looMaxDrawdown"`
	MaxDrawdownChange  float64  `json:"maxDrawdownChange"`
}

// ChainRunner 留一法组合重放回调：调用方用给定因子集合重跑
// 合成→目标→执行→会计链路（或近似：权重变化×收益），返回组合表现。
// 方法由调用方文档化；本包只做留一编排与差异聚合（差值 = LOO - Full）。
type ChainRunner func(factors []FactorSeries) (PortfolioRunResult, error)

// LeaveOneOutPortfolioImpact 留一法组合维度（设计 §7.3/§10.2，Task 6 接入）：
// 先跑全因子组合，再对每因子移除后经 runner 重跑，聚合收益/换手/回撤/IC 变化。
// 差值符号 = LOO - Full（正 = 移除后改善）。
func LeaveOneOutPortfolioImpact(factors []FactorSeries, runner ChainRunner) ([]LOOPortfolioImpact, error) {
	if err := validateFactors(factors); err != nil {
		return nil, err
	}
	if runner == nil {
		return nil, fmt.Errorf("留一法重放回调不能为 nil")
	}
	full, err := runner(factors)
	if err != nil {
		return nil, fmt.Errorf("全因子组合重放失败: %w", err)
	}
	out := make([]LOOPortfolioImpact, 0, len(factors))
	for j := range factors {
		remaining := make([]FactorSeries, 0, len(factors)-1)
		for i := range factors {
			if i != j {
				remaining = append(remaining, factors[i])
			}
		}
		loo, err := runner(remaining)
		if err != nil {
			return nil, fmt.Errorf("移除因子 %s 后重放失败: %w", factors[j].Key, err)
		}
		imp := LOOPortfolioImpact{
			RemovedFactor:      factors[j].Key,
			FullAnnualReturn:   full.AnnualReturn,
			LOOAnnualReturn:    loo.AnnualReturn,
			AnnualReturnChange: loo.AnnualReturn - full.AnnualReturn,
			FullTurnover:       full.AnnualTurnover,
			LOOTurnover:        loo.AnnualTurnover,
			TurnoverChange:     loo.AnnualTurnover - full.AnnualTurnover,
			FullMaxDrawdown:    full.MaxDrawdown,
			LOOMaxDrawdown:     loo.MaxDrawdown,
			MaxDrawdownChange:  loo.MaxDrawdown - full.MaxDrawdown,
		}
		imp.FullIC, imp.LOOIC, imp.ICChange = icDiff(full.OutOfSampleIC, loo.OutOfSampleIC)
		out = append(out, imp)
	}
	return out, nil
}

// icDiff 两个可空 IC 的差异（任一 nil → 对应字段 nil，差值 nil）。
func icDiff(full, loo *float64) (*float64, *float64, *float64) {
	if full == nil || loo == nil {
		return full, loo, nil
	}
	d := *loo - *full
	return full, loo, &d
}

// RunResultFromLedgers 从每日账本计算留一法比较用表现结果（复用 ComputeMetrics，
// 净口径：年化收益/年化换手/最大回撤）。ic 为调用方计算的样本外 IC（nil = 不可计算）。
func RunResultFromLedgers(ledgers []DailyLedger, ic *float64) (PortfolioRunResult, error) {
	m, err := ComputeMetrics(FromLedgers(ledgers), nil, 0)
	if err != nil {
		return PortfolioRunResult{}, err
	}
	return PortfolioRunResult{
		OutOfSampleIC:  ic,
		AnnualReturn:   m.Net.AnnualReturn,
		AnnualTurnover: m.Quality.AnnualTurnover,
		MaxDrawdown:    m.Net.MaxDrawdown,
	}, nil
}

// ---- 组合归因 ----

// AttributionDay 单日组合归因数据（调用方从每日账本与行情装配）。
// 口径（文档化）：
//   - ActualWeights 为期初实际权重（代码 → 前一日末持仓市值/期初净权益，Σ + CashRatio ≈ 1）；
//   - Returns 为当日收盘收益（code → close[t]/close[t-1] - 1）；
//   - TargetWeights/IdealWeights 为约束后/理想目标权重（Effective 可执行口径，可空）；
//   - Industries 代码 → 行业（可空，缺行业归 "unknown"）；
//   - GrossNav/NetNav 为期末毛/净净值（>0 时输出净值口径对照与残差）。
type AttributionDay struct {
	Date          string             `json:"date"`
	ActualWeights map[string]float64 `json:"actualWeights"`
	CashRatio     float64            `json:"cashRatio"`
	Returns       map[string]float64 `json:"returns"`
	Fees          float64            `json:"fees"`
	TargetWeights map[string]float64 `json:"targetWeights,omitempty"`
	IdealWeights  map[string]float64 `json:"idealWeights,omitempty"`
	Industries    map[string]string  `json:"industries,omitempty"`
	GrossNav      float64            `json:"grossNav,omitempty"`
	NetNav        float64            `json:"netNav,omitempty"`
}

// AttributionInput 组合归因输入。
type AttributionInput struct {
	InitialEquity float64          `json:"initialEquity"` // 期初净权益（成本拖累分母，>0）
	RiskFreeRate  float64          `json:"riskFreeRate"`  // 年化无风险利率（0 = 现金零收益）
	Days          []AttributionDay `json:"days"`
}

// StockContribution 单股票贡献（算术累计：Σ_t 期初权重×当日收益）。
type StockContribution struct {
	Code         string  `json:"code"`
	Contribution float64 `json:"contribution"`
}

// IndustryContribution 行业贡献（按行业聚合股票贡献；缺行业归 "unknown"）。
type IndustryContribution struct {
	Industry     string  `json:"industry"`
	Contribution float64 `json:"contribution"`
	StockCount   int     `json:"stockCount"`
}

// WeightDeviationDay 目标/实际权重与偏离时间序列（偏离 = 实际 - 目标，正 = 超配）。
type WeightDeviationDay struct {
	Date            string             `json:"date"`
	Target          map[string]float64 `json:"target"`
	Actual          map[string]float64 `json:"actual"`
	Deviation       map[string]float64 `json:"deviation"`
	SumAbsDeviation float64            `json:"sumAbsDeviation"`
}

// PortfolioAttribution 组合归因结果（设计 §10.3：收益从哪来）。
// 恒等式见文件头注释（算术累计口径，容差 attributionTolerance）。
type PortfolioAttribution struct {
	Stocks               []StockContribution    `json:"stocks"`
	Industries           []IndustryContribution `json:"industries"`
	CashContribution     float64                `json:"cashContribution"`
	StockCashSum         float64                `json:"stockCashSum"` // 毛归因收益 = Σ股票 + 现金（算术累计）
	CostDrag             float64                `json:"costDrag"`
	NetReturn            float64                `json:"netReturn"` // = StockCashSum - CostDrag
	SelectionReturn      *float64               `json:"selectionReturn,omitempty"`
	ExecutionDeviation   *float64               `json:"executionDeviation,omitempty"` // = StockCashSum - SelectionReturn
	WeightSeries         []WeightDeviationDay   `json:"weightSeries"`
	PortfolioGrossReturn *float64               `json:"portfolioGrossReturn,omitempty"` // 净值口径毛累计（复利）
	PortfolioNetReturn   *float64               `json:"portfolioNetReturn,omitempty"`   // 净值口径净累计（复利）
	Residual             *float64               `json:"residual,omitempty"`             // PortfolioNetReturn - NetReturn（披露）
	Reconciled           bool                   `json:"reconciled"`
	Limitation           string                 `json:"limitation"`
}

// validateAttributionInput 校验归因输入（fail closed，与 ComputeMetrics 对 rf 的
// 显式校验口径一致）：
//   - RiskFreeRate 必须有限且 > -1：否则 (1+rf)^(1/252) 无定义（rf<-1 底数非正，
//     非整数次幂返回 NaN），NaN 会经现金贡献污染 CashContribution/StockCashSum/
//     NetReturn/Residual，故拒绝而非静默；
//   - 权重（Actual/Target/Ideal）/收益/现金比例/费用必须有限（NaN/Inf 拒绝），
//     不得静默抹平或传播进归因输出。
//
// 错误信息含具体字段与允许范围。
func validateAttributionInput(in AttributionInput) error {
	if math.IsNaN(in.RiskFreeRate) || math.IsInf(in.RiskFreeRate, 0) {
		return fmt.Errorf("RiskFreeRate 非法: %v（应为有限数）", in.RiskFreeRate)
	}
	if in.RiskFreeRate <= -1 {
		return fmt.Errorf("RiskFreeRate 非法: %v（必须 > -1，否则 (1+rf)^(1/252) 无定义）", in.RiskFreeRate)
	}
	for i, d := range in.Days {
		if math.IsNaN(d.CashRatio) || math.IsInf(d.CashRatio, 0) {
			return fmt.Errorf("Days[%d].CashRatio 非法: %v（应为有限数）", i, d.CashRatio)
		}
		if math.IsNaN(d.Fees) || math.IsInf(d.Fees, 0) || d.Fees < 0 {
			return fmt.Errorf("Days[%d].Fees 非法: %v（应为有限非负）", i, d.Fees)
		}
		for code, w := range d.ActualWeights {
			if math.IsNaN(w) || math.IsInf(w, 0) {
				return fmt.Errorf("Days[%d].ActualWeights[%s] 非法: %v（应为有限数）", i, code, w)
			}
		}
		for code, w := range d.TargetWeights {
			if math.IsNaN(w) || math.IsInf(w, 0) {
				return fmt.Errorf("Days[%d].TargetWeights[%s] 非法: %v（应为有限数）", i, code, w)
			}
		}
		for code, w := range d.IdealWeights {
			if math.IsNaN(w) || math.IsInf(w, 0) {
				return fmt.Errorf("Days[%d].IdealWeights[%s] 非法: %v（应为有限数）", i, code, w)
			}
		}
		for code, r := range d.Returns {
			if math.IsNaN(r) || math.IsInf(r, 0) {
				return fmt.Errorf("Days[%d].Returns[%s] 非法: %v（应为有限数）", i, code, r)
			}
		}
	}
	return nil
}

// ComputeAttribution 计算组合归因（设计 §10.3）。纯函数，不修改输入；
// 输入非法（rf 不满足要求或权重/收益等非有限）返回 error（fail closed）。
func ComputeAttribution(in AttributionInput) (PortfolioAttribution, error) {
	if err := validateAttributionInput(in); err != nil {
		return PortfolioAttribution{}, err
	}
	pa := PortfolioAttribution{Limitation: attributionLimitation}
	if len(in.Days) == 0 {
		pa.Reconciled = true
		return pa, nil
	}
	stockCum := map[string]float64{}
	indCum := map[string]IndustryContribution{}
	indCodes := map[string]map[string]bool{} // 行业 → 代码集合（StockCount 去重）
	var indNames []string
	cashCum := 0.0
	fees := 0.0
	selection := 0.0
	hasSelection := false
	var weightSeries []WeightDeviationDay
	hasNav := false
	grossNavN, netNavN := 0.0, 0.0

	// 每日无风险收益（年化 rf 折算到交易日）。
	rfDaily := 0.0
	if in.RiskFreeRate != 0 {
		rfDaily = math.Pow(1+in.RiskFreeRate, 1.0/float64(annualTradingDays)) - 1
	}

	for _, d := range in.Days {
		// 股票贡献（期初权重 × 当日收益）与行业聚合（缺行业归 "unknown"）。
		for code, w := range d.ActualWeights {
			r := d.Returns[code]
			if math.IsNaN(r) || math.IsInf(r, 0) {
				r = 0
			}
			c := w * r
			stockCum[code] += c
			ind := d.Industries[code]
			if ind == "" {
				ind = "unknown"
			}
			ic, ok := indCum[ind]
			if !ok {
				ic = IndustryContribution{Industry: ind}
				indNames = append(indNames, ind)
				indCodes[ind] = map[string]bool{}
			}
			ic.Contribution += c
			if !indCodes[ind][code] {
				indCodes[ind][code] = true
				ic.StockCount++
			}
			indCum[ind] = ic
		}
		// 现金贡献（期初现金 × 每日无风险收益）。
		cashC := d.CashRatio * rfDaily
		cashCum += cashC

		fees += d.Fees

		// 信号选择收益（理想目标权重下收益；理想现金 = 1 - Σ理想权重）。
		if d.IdealWeights != nil {
			r := 0.0
			for code, w := range d.IdealWeights {
				ret := d.Returns[code]
				if math.IsNaN(ret) || math.IsInf(ret, 0) {
					ret = 0
				}
				r += w * ret
			}
			r += (1 - sumMap(d.IdealWeights)) * rfDaily
			selection += r
			hasSelection = true
		}

		// 权重偏离时间序列（仅当提供目标权重；Target/Actual 深拷贝，输出与输入隔离）。
		if d.TargetWeights != nil {
			ws := WeightDeviationDay{
				Date:      d.Date,
				Target:    cloneMap(d.TargetWeights),
				Actual:    cloneMap(d.ActualWeights),
				Deviation: map[string]float64{},
			}
			for _, c := range unionKeys(d.TargetWeights, d.ActualWeights) {
				ws.Deviation[c] = d.ActualWeights[c] - d.TargetWeights[c]
				ws.SumAbsDeviation += math.Abs(ws.Deviation[c])
			}
			weightSeries = append(weightSeries, ws)
		}

		// 净值口径（复利）对照。
		if d.GrossNav > 0 && d.NetNav > 0 {
			grossNavN, netNavN = d.GrossNav, d.NetNav
			hasNav = true
		}
	}

	// 组装输出（确定性：股票按代码升序、行业按行业名升序）。
	pa.CashContribution = cashCum
	pa.StockCashSum = sumMap(stockCum) + cashCum
	if in.InitialEquity > 0 {
		pa.CostDrag = fees / in.InitialEquity
	}
	pa.NetReturn = pa.StockCashSum - pa.CostDrag
	pa.WeightSeries = weightSeries
	for _, c := range sortedKeys(stockCum) {
		pa.Stocks = append(pa.Stocks, StockContribution{Code: c, Contribution: stockCum[c]})
	}
	sort.Strings(indNames)
	for _, name := range indNames {
		pa.Industries = append(pa.Industries, indCum[name])
	}
	if hasSelection {
		s := selection
		pa.SelectionReturn = &s
		ed := pa.StockCashSum - s
		pa.ExecutionDeviation = &ed
	}

	// 恒等式断言（构造性，容差内）：
	//   StockCashSum = Σ股票贡献 + 现金贡献；NetReturn = StockCashSum - CostDrag。
	stockSum := 0.0
	for _, sc := range pa.Stocks {
		stockSum += sc.Contribution
	}
	pa.Reconciled = math.Abs(stockSum+pa.CashContribution-pa.StockCashSum) <= attributionTolerance &&
		math.Abs(pa.StockCashSum-pa.CostDrag-pa.NetReturn) <= attributionTolerance

	// 净值口径对照（复利）与残差披露（复利交叉项 + 成交时点/整手偏离，不静默抹平）。
	if hasNav && in.InitialEquity > 0 {
		gc := grossNavN/in.InitialEquity - 1
		nc := netNavN/in.InitialEquity - 1
		pa.PortfolioGrossReturn = &gc
		pa.PortfolioNetReturn = &nc
		res := nc - pa.NetReturn
		pa.Residual = &res
	}
	return pa, nil
}

// unionKeys 两个权重 map 的键并集（升序，确定性）。
func unionKeys(a, b map[string]float64) []string {
	set := map[string]bool{}
	for k := range a {
		set[k] = true
	}
	for k := range b {
		set[k] = true
	}
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// cloneMap 权重 map 深拷贝（输出与输入隔离，防别名污染；nil 输入返回 nil）。
func cloneMap(m map[string]float64) map[string]float64 {
	if m == nil {
		return nil
	}
	out := make(map[string]float64, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
