// metrics.go 组合报告指标（v2 设计 §10.1）。
//
// 输入 = 逐日基础时序（PortfolioSeries：毛/净净值、费用、换手、现金比例、持仓数、
// 目标/实际权重、未成交记录，可由 Task 5 Simulate 的每日账本经 FromLedgers 派生），
// 基准时序可选（BenchmarkSeries）。输出 PortfolioMetrics：毛/净风险收益指标、
// 基准相对指标、组合质量与稳定性。
//
// 数值口径（文档化，输出 Conventions 亦携带）：
//   - 交易日尺度：年化因子 = 252 个交易日/年；
//   - 年化收益 = (1+累计收益)^(252/n) - 1，n = 日收益样本数（净值点数 - 1）；
//   - 年化波动 = sqrt(252) × 日收益样本标准差（n-1 分母）；
//   - Sharpe = (年化收益 - rf) / 年化波动；Sortino 下行波动只计 r<0 的平方均值
//     （n-1 分母），年化同乘 sqrt(252)；Calmar = 年化收益 / |最大回撤|；
//     rf 为年化无风险利率参数（默认 0）；
//   - 最大回撤 = min(净值/峰值 - 1)（≤0，0 = 无回撤）；回撤持续期 = 从跌破峰值到
//     恢复到峰值/新高（含持平）的最大交易日差；未恢复的回撤持续到序列末尾；
//   - 缺失处理：基准缺失 → BenchmarkMetrics.Available=false + 原因
//     （missing_benchmark / benchmark_insufficient_alignment），不阻止绝对收益报告；
//     零方差或样本不足（<2 个收益样本）→ Sharpe/Sortino/Calmar 为 nil + 原因；
//   - 成本拖累 = Σ费用 / 期初净净值（净净值 = 期末权益；毛净值 = 期末权益 + 截至当日
//     累计费用，故首日费用为 0 时 毛累计收益 - 净累计收益 = 成本拖累 精确成立）；
//   - 换手口径 = 输入 Turnover（单边，权重口径；FromLedgers 为单边成交金额口径
//     (Σ买入毛额+Σ卖出毛额)/2/期初权益）；年换手 = 日换手均值 × 252；
//   - 单票集中度 = 跨日最大单票权重；HHI = Σw²（实际权重）逐日均值；
//     目标-实际偏离 = Σ|实际 - 目标|（代码并集）的均值/最大；
//   - 稳定性 = 年度收益表（按年复利日收益）；
//   - 舍入容差：指标计算内部容差 1e-12；归因恒等式断言容差 1e-9（attribution.go）。
package portfolioresearch

import (
	"fmt"
	"math"
	"sort"
)

// annualTradingDays 年化交易日数（A 股常用，指标年化/归因 rf 折算使用）。
// 注意：combine.go 的 tradingDaysPerYear=250 用于训练窗截取，两者用途不同，勿混用。
const annualTradingDays = 252

// ---- 输入类型 ----

// PortfolioDay 单日基础时序点（Task 5 Simulate 每日账本的派生，见 FromLedgers）。
type PortfolioDay struct {
	Date          string             `json:"date"`
	GrossNav      float64            `json:"grossNav"` // 毛净值（= 净净值 + 截至当日累计费用，见文件头口径）
	NetNav        float64            `json:"netNav"`   // 净净值（期末权益，含费用扣减）
	Fees          float64            `json:"fees"`     // 当日费用合计
	Turnover      float64            `json:"turnover"` // 当日换手（单边；口径见文件头，FromLedgers 为成交金额口径）
	CashRatio     float64            `json:"cashRatio"`
	Holdings      int                `json:"holdings"`
	TargetWeights map[string]float64 `json:"targetWeights,omitempty"` // 目标权重（约束后，可空）
	ActualWeights map[string]float64 `json:"actualWeights,omitempty"` // 实际权重（可空）
	Rejections    []Rejection        `json:"rejections,omitempty"`    // 当日未成交记录
}

// PortfolioSeries 组合报告输入：逐日基础时序。
type PortfolioSeries struct {
	Days []PortfolioDay `json:"days"`
}

// Validate 校验组合时序：日期非空且严格升序、毛/净净值有限正数（毛 ≥ 净）、
// 费用/换手有限非负、现金比例 [0,1]、持仓数非负。
func (s PortfolioSeries) Validate() error {
	if len(s.Days) == 0 {
		return fmt.Errorf("组合时序不能为空")
	}
	for i, d := range s.Days {
		if d.Date == "" {
			return fmt.Errorf("Days[%d] 缺少日期", i)
		}
		if i > 0 && d.Date <= s.Days[i-1].Date {
			return fmt.Errorf("组合时序日期必须严格升序: Days[%d]=%s <= Days[%d]=%s", i, d.Date, i-1, s.Days[i-1].Date)
		}
		if math.IsNaN(d.GrossNav) || math.IsInf(d.GrossNav, 0) || d.GrossNav <= 0 {
			return fmt.Errorf("Days[%d] 毛净值非法: %v（应为有限正数）", i, d.GrossNav)
		}
		if math.IsNaN(d.NetNav) || math.IsInf(d.NetNav, 0) || d.NetNav <= 0 {
			return fmt.Errorf("Days[%d] 净净值非法: %v（应为有限正数）", i, d.NetNav)
		}
		if d.GrossNav+1e-9 < d.NetNav {
			return fmt.Errorf("Days[%d] 毛净值 %.6g < 净净值 %.6g（毛净值 = 净净值 + 累计费用，不得小于）", i, d.GrossNav, d.NetNav)
		}
		if math.IsNaN(d.Fees) || math.IsInf(d.Fees, 0) || d.Fees < 0 {
			return fmt.Errorf("Days[%d] 费用非法: %v（应为有限非负）", i, d.Fees)
		}
		if math.IsNaN(d.Turnover) || math.IsInf(d.Turnover, 0) || d.Turnover < 0 {
			return fmt.Errorf("Days[%d] 换手非法: %v（应为有限非负）", i, d.Turnover)
		}
		if math.IsNaN(d.CashRatio) || math.IsInf(d.CashRatio, 0) || d.CashRatio < 0 || d.CashRatio > 1 {
			return fmt.Errorf("Days[%d] 现金比例非法: %v（应为 [0,1]）", i, d.CashRatio)
		}
		if d.Holdings < 0 {
			return fmt.Errorf("Days[%d] 持仓数非法: %d（应为 >=0）", i, d.Holdings)
		}
	}
	return nil
}

// BenchmarkDay 基准单日净值。
type BenchmarkDay struct {
	Date string  `json:"date"`
	Nav  float64 `json:"nav"`
}

// BenchmarkSeries 基准时序（净值点至少 2 个，日期严格升序；与组合收益日期对齐，
// 缺失日期不计入超额统计）。缺基准不阻止绝对收益报告（见 ComputeMetrics）。
type BenchmarkSeries struct {
	Days []BenchmarkDay `json:"days"`
}

// Validate 校验基准时序：至少 2 个净值点、日期严格升序、净值有限正数。
func (b BenchmarkSeries) Validate() error {
	if len(b.Days) < 2 {
		return fmt.Errorf("基准时序至少需要 2 个净值点")
	}
	for i, d := range b.Days {
		if d.Date == "" {
			return fmt.Errorf("基准 Days[%d] 缺少日期", i)
		}
		if i > 0 && d.Date <= b.Days[i-1].Date {
			return fmt.Errorf("基准时序日期必须严格升序: Days[%d]=%s <= Days[%d]=%s", i, d.Date, i-1, b.Days[i-1].Date)
		}
		if math.IsNaN(d.Nav) || math.IsInf(d.Nav, 0) || d.Nav <= 0 {
			return fmt.Errorf("基准 Days[%d] 净值非法: %v（应为有限正数）", i, d.Nav)
		}
	}
	return nil
}

// ---- 输出类型 ----

// MetricConventions 数值口径说明（交易日尺度、无风险利率、缺失处理与容差）。
type MetricConventions struct {
	TradingDaysPerYear int     `json:"tradingDaysPerYear"`
	RiskFreeRate       float64 `json:"riskFreeRate"`
	AnnualizeFormula   string  `json:"annualizeFormula"`
	VolatilityFormula  string  `json:"volatilityFormula"`
	DrawdownDefinition string  `json:"drawdownDefinition"`
	Tolerance          float64 `json:"tolerance"`
	Note               string  `json:"note"`
}

// ReturnRiskMetrics 一套毛/净口径的风险收益指标。
// Sharpe/Sortino/Calmar 为 *float64：nil = 未定义（零方差/样本不足/无回撤），
// 原因见对应 *Reason 字段。
type ReturnRiskMetrics struct {
	CumulativeReturn    float64  `json:"cumulativeReturn"`
	AnnualReturn        float64  `json:"annualReturn"`
	AnnualVolatility    float64  `json:"annualVolatility"`
	Sharpe              *float64 `json:"sharpe,omitempty"`
	Sortino             *float64 `json:"sortino,omitempty"`
	MaxDrawdown         float64  `json:"maxDrawdown"`         // ≤0，0 = 无回撤
	MaxDrawdownDuration int      `json:"maxDrawdownDuration"` // 回撤持续期（交易日）
	Calmar              *float64 `json:"calmar,omitempty"`
	SharpeReason        string   `json:"sharpeReason,omitempty"`
	SortinoReason       string   `json:"sortinoReason,omitempty"`
	CalmarReason        string   `json:"calmarReason,omitempty"`
	SampleDays          int      `json:"sampleDays"` // 有效日收益样本数（净值点数 - 1）
}

// BenchmarkMetrics 基准相对指标。Available=false 时 UnavailableReason 说明原因
// （missing_benchmark = 未提供基准；benchmark_insufficient_alignment = 对齐样本不足）。
// TrackingError 为 0（组合与基准完全同步）时 InformationRatio=nil + IRReason 说明
// （与 Sharpe/Sortino/Calmar 的 nil+原因约定一致）。
type BenchmarkMetrics struct {
	Available         bool     `json:"available"`
	UnavailableReason string   `json:"unavailableReason,omitempty"`
	AnnualExcess      *float64 `json:"annualExcess,omitempty"` // 年化超额 = mean(日超额)×252
	TrackingError     *float64 `json:"trackingError,omitempty"`
	InformationRatio  *float64 `json:"informationRatio,omitempty"`
	IRReason          string   `json:"irReason,omitempty"` // IR 为 nil 的原因（TE=0 时说明）
	SampleDays        int      `json:"sampleDays"`
}

// DeviationStat 目标-实际权重偏离统计。
type DeviationStat struct {
	Mean float64 `json:"mean"`
	Max  float64 `json:"max"`
}

// UnfilledStat 未成交统计（原因 → 次数，稳定枚举）。
type UnfilledStat struct {
	Count    int            `json:"count"`
	ByReason map[string]int `json:"byReason,omitempty"`
}

// QualityMetrics 组合质量指标。
type QualityMetrics struct {
	DailyTurnover    float64       `json:"dailyTurnover"`    // 日换手均值
	AnnualTurnover   float64       `json:"annualTurnover"`   // 年换手 = 日换手均值 × 252
	CostDrag         float64       `json:"costDrag"`         // 成本拖累 = Σ费用/期初净净值
	AvgCashRatio     float64       `json:"avgCashRatio"`     // 现金比例均值
	AvgHoldings      float64       `json:"avgHoldings"`      // 持仓数均值
	MaxConcentration float64       `json:"maxConcentration"` // 单票最大权重（跨日最大）
	AvgHHI           float64       `json:"avgHhi"`           // HHI = Σw²（实际权重）均值
	TargetDeviation  DeviationStat `json:"targetDeviation"`
	Unfilled         UnfilledStat  `json:"unfilled"`
}

// YearReturn 单年度收益（按年复利日收益）。
type YearReturn struct {
	Year        string  `json:"year"`
	GrossReturn float64 `json:"grossReturn"`
	NetReturn   float64 `json:"netReturn"`
	TradingDays int     `json:"tradingDays"`
}

// StabilityMetrics 稳定性：年度收益表。
type StabilityMetrics struct {
	ByYear []YearReturn `json:"byYear"`
}

// PortfolioMetrics 组合报告指标（设计 §10.1）。
type PortfolioMetrics struct {
	Conventions MetricConventions `json:"conventions"`
	Gross       ReturnRiskMetrics `json:"gross"`
	Net         ReturnRiskMetrics `json:"net"`
	Benchmark   BenchmarkMetrics  `json:"benchmark"`
	Quality     QualityMetrics    `json:"quality"`
	Stability   StabilityMetrics  `json:"stability"`
	TradingDays int               `json:"tradingDays"` // 有效日收益样本数
}

// ---- 计算入口 ----

// ComputeMetrics 计算组合报告指标（设计 §10.1）。
// benchmark 可空：缺基准不阻止绝对收益报告，超额/跟踪误差/信息比率置 unavailable
// + 原因（missing_benchmark）。rf 为年化无风险利率（默认 0，口径见文件头）。
func ComputeMetrics(series PortfolioSeries, benchmark *BenchmarkSeries, rf float64) (PortfolioMetrics, error) {
	if err := series.Validate(); err != nil {
		return PortfolioMetrics{}, err
	}
	if math.IsNaN(rf) || math.IsInf(rf, 0) {
		return PortfolioMetrics{}, fmt.Errorf("无风险利率非法: %v（应为有限数）", rf)
	}
	if benchmark != nil {
		if err := benchmark.Validate(); err != nil {
			return PortfolioMetrics{}, err
		}
	}
	n := len(series.Days)
	navs := make([]float64, n)
	gross := make([]float64, n)
	dates := make([]string, n)
	for i, d := range series.Days {
		navs[i] = d.NetNav
		gross[i] = d.GrossNav
		dates[i] = d.Date
	}
	netRets := dailyReturns(navs)
	grossRets := dailyReturns(gross)

	m := PortfolioMetrics{
		Conventions: metricConventions(rf),
		TradingDays: len(netRets),
	}
	m.Net = riskMetrics(netRets, navs, rf)
	m.Gross = riskMetrics(grossRets, gross, rf)
	m.Benchmark = computeBenchmark(dates, netRets, benchmark)
	m.Quality = computeQuality(series)
	m.Stability = computeStability(dates, grossRets, netRets)
	return m, nil
}

// metricConventions 口径说明。
func metricConventions(rf float64) MetricConventions {
	return MetricConventions{
		TradingDaysPerYear: annualTradingDays,
		RiskFreeRate:       rf,
		AnnualizeFormula:   "(1+累计收益)^(252/n)-1，n=日收益样本数（净值点数-1）",
		VolatilityFormula:  "sqrt(252) × 日收益样本标准差（n-1 分母）；Sortino 下行波动只计 r<0 的平方均值（n-1 分母）",
		DrawdownDefinition: "最大回撤 = min(净值/峰值-1)（≤0）；回撤持续期 = 峰值日到恢复到峰值/新高的交易日差，未恢复计到序列末",
		Tolerance:          attributionTolerance,
		Note:               "归因恒等式断言容差 1e-9；基准缺失（missing_benchmark）不阻止绝对收益报告",
	}
}

// dailyReturns 净值 → 日收益（长度 n-1；输入校验已保证净值正数）。
func dailyReturns(navs []float64) []float64 {
	if len(navs) < 2 {
		return nil
	}
	out := make([]float64, 0, len(navs)-1)
	for i := 1; i < len(navs); i++ {
		out = append(out, navs[i]/navs[i-1]-1)
	}
	return out
}

// riskMetrics 一套口径的风险收益指标。
func riskMetrics(rets []float64, navs []float64, rf float64) ReturnRiskMetrics {
	m := ReturnRiskMetrics{SampleDays: len(rets)}
	if len(navs) == 0 {
		return m
	}
	m.CumulativeReturn = navs[len(navs)-1]/navs[0] - 1
	if len(rets) > 0 {
		m.AnnualReturn = annualize(m.CumulativeReturn, len(rets))
	}
	if len(rets) >= 2 {
		vol := stdDev(rets)
		m.AnnualVolatility = vol * math.Sqrt(annualTradingDays)
		if vol > 0 {
			s := (m.AnnualReturn - rf) / m.AnnualVolatility
			m.Sharpe = &s
		} else {
			m.SharpeReason = "日收益零方差（无风险暴露），Sharpe 未定义"
		}
		if dd := downsideDev(rets); dd > 0 {
			s := (m.AnnualReturn - rf) / (dd * math.Sqrt(annualTradingDays))
			m.Sortino = &s
		} else {
			m.SortinoReason = "无下行收益（下行波动为 0），Sortino 未定义"
		}
	} else {
		m.SharpeReason = "收益样本不足（少于 2 个交易日），波动/Sharpe/Sortino 未定义"
		m.SortinoReason = m.SharpeReason
	}
	m.MaxDrawdown, m.MaxDrawdownDuration = drawdown(navs)
	if m.MaxDrawdown < 0 {
		c := m.AnnualReturn / math.Abs(m.MaxDrawdown)
		m.Calmar = &c
	} else {
		m.CalmarReason = "无回撤（最大回撤为 0），Calmar 未定义"
	}
	return m
}

// annualize 年化收益 = (1+r)^(252/n) - 1（n 个日收益样本，交易日尺度）。
func annualize(cum float64, n int) float64 {
	if n <= 0 {
		return 0
	}
	return math.Pow(1+cum, float64(annualTradingDays)/float64(n)) - 1
}

// stdDev 样本标准差（n-1 分母）；样本 < 2 返回 0。
func stdDev(vals []float64) float64 {
	n := len(vals)
	if n < 2 {
		return 0
	}
	mean := meanOf(vals)
	var s float64
	for _, v := range vals {
		d := v - mean
		s += d * d
	}
	return math.Sqrt(s / float64(n-1))
}

// downsideDev 下行偏差（样本口径 n-1）：只计 r < 0 的平方均值开方；无下行返回 0。
func downsideDev(rets []float64) float64 {
	n := len(rets)
	if n < 2 {
		return 0
	}
	var s float64
	for _, r := range rets {
		if r < 0 {
			s += r * r
		}
	}
	if s <= 0 {
		return 0
	}
	return math.Sqrt(s / float64(n-1))
}

// drawdown 最大回撤（≤0，0 = 无回撤）与回撤持续期（交易日）。
// 持续期口径：从跌破峰值到恢复到峰值/新高（含持平）的最大交易日差；
// 未恢复的回撤持续到序列末尾（末位索引 - 峰值索引）。
func drawdown(navs []float64) (float64, int) {
	if len(navs) < 2 {
		return 0, 0
	}
	peakIdx := 0
	maxDD := 0.0
	inDD := false
	maxDur := 0
	for i := 1; i < len(navs); i++ {
		if navs[i] < navs[peakIdx] {
			inDD = true
			if dd := navs[i]/navs[peakIdx] - 1; dd < maxDD {
				maxDD = dd
			}
			continue
		}
		if inDD {
			if d := i - peakIdx; d > maxDur {
				maxDur = d
			}
			inDD = false
		}
		if navs[i] > navs[peakIdx] {
			peakIdx = i
		}
	}
	if inDD {
		if d := len(navs) - 1 - peakIdx; d > maxDur {
			maxDur = d
		}
	}
	return maxDD, maxDur
}

// ---- 基准 ----

// computeBenchmark 基准统计：组合与基准日收益按日期对齐。
// 缺基准 → unavailable + missing_benchmark；对齐样本 < 2 → unavailable + 原因。
// 跟踪误差为 0（组合与基准完全同步）时信息比率为 nil + IRReason（除零保护，
// 与 Sharpe/Sortino/Calmar 的 nil+原因约定一致）。
func computeBenchmark(dates []string, portRets []float64, benchmark *BenchmarkSeries) BenchmarkMetrics {
	if benchmark == nil {
		return BenchmarkMetrics{Available: false, UnavailableReason: "missing_benchmark"}
	}
	excess := alignedExcess(dates, portRets, benchmark)
	if len(excess) < 2 {
		return BenchmarkMetrics{Available: false, UnavailableReason: "benchmark_insufficient_alignment",
			SampleDays: len(excess)}
	}
	mean := meanOf(excess)
	annualExcess := mean * annualTradingDays
	te := stdDev(excess) * math.Sqrt(annualTradingDays)
	if te > 0 {
		ir := annualExcess / te
		return BenchmarkMetrics{Available: true, AnnualExcess: &annualExcess, TrackingError: &te,
			InformationRatio: &ir, SampleDays: len(excess)}
	}
	// 跟踪误差为 0（组合与基准完全同步）：信息比率未定义（除零保护，nil + 原因）。
	return BenchmarkMetrics{Available: true, AnnualExcess: &annualExcess, TrackingError: &te,
		IRReason: "跟踪误差为 0（组合与基准完全同步），信息比率未定义", SampleDays: len(excess)}
}

// alignedExcess 组合与基准日收益按日期对齐：基准日收益 = 基准净值当日/前一基准日 - 1。
// dates 为完整净值日期序列（长度 = 收益样本数 + 1），收益 i 归属日期 dates[i+1]。
// 组合收益日期在基准中缺失或是基准首日时跳过该日。
func alignedExcess(dates []string, portRets []float64, benchmark *BenchmarkSeries) []float64 {
	pos := make(map[string]int, len(benchmark.Days))
	navs := make([]float64, len(benchmark.Days))
	for i, d := range benchmark.Days {
		pos[d.Date] = i
		navs[i] = d.Nav
	}
	var out []float64
	for i := range portRets {
		j, ok := pos[dates[i+1]]
		if !ok || j == 0 {
			continue
		}
		out = append(out, portRets[i]-(navs[j]/navs[j-1]-1))
	}
	return out
}

// ---- 组合质量 ----

// computeQuality 组合质量指标（换手/成本拖累/现金/持仓/集中度/目标偏离/未成交）。
func computeQuality(series PortfolioSeries) QualityMetrics {
	q := QualityMetrics{}
	n := len(series.Days)
	if n == 0 {
		return q
	}
	var turnover, cash, holdings, hhi float64
	maxConc := 0.0
	cost := 0.0
	devSum := 0.0
	devMax := 0.0
	unfilled := UnfilledStat{ByReason: map[string]int{}}
	for _, d := range series.Days {
		turnover += d.Turnover
		cash += d.CashRatio
		holdings += float64(d.Holdings)
		cost += d.Fees
		if mw := maxWeight(d.ActualWeights); mw > maxConc {
			maxConc = mw
		}
		hhi += hhiOf(d.ActualWeights)
		dev := targetDeviation(d.TargetWeights, d.ActualWeights)
		devSum += dev
		if dev > devMax {
			devMax = dev
		}
		for _, r := range d.Rejections {
			unfilled.Count++
			unfilled.ByReason[r.Reason]++
		}
	}
	q.DailyTurnover = turnover / float64(n)
	q.AnnualTurnover = q.DailyTurnover * annualTradingDays
	if series.Days[0].NetNav > 0 {
		q.CostDrag = cost / series.Days[0].NetNav
	}
	q.AvgCashRatio = cash / float64(n)
	q.AvgHoldings = holdings / float64(n)
	q.MaxConcentration = maxConc
	q.AvgHHI = hhi / float64(n)
	q.TargetDeviation = DeviationStat{Mean: devSum / float64(n), Max: devMax}
	q.Unfilled = unfilled
	return q
}

// maxWeight 权重 map 的最大值（空 = 0）。
func maxWeight(w map[string]float64) float64 {
	m := 0.0
	for _, v := range w {
		if v > m {
			m = v
		}
	}
	return m
}

// hhiOf 权重 map 的 HHI = Σw²（空 = 0）。
func hhiOf(w map[string]float64) float64 {
	s := 0.0
	for _, v := range w {
		s += v * v
	}
	return s
}

// targetDeviation 单日目标-实际偏离 = Σ|实际 - 目标|（代码并集）。
func targetDeviation(target, actual map[string]float64) float64 {
	codes := map[string]bool{}
	for c := range target {
		codes[c] = true
	}
	for c := range actual {
		codes[c] = true
	}
	s := 0.0
	for c := range codes {
		s += math.Abs(actual[c] - target[c])
	}
	return s
}

// ---- 稳定性 ----

// computeStability 年度收益表：按年分组复利日收益（每年独立复利）。
// dates 为完整净值日期序列（长度 = 收益样本数 + 1），收益 i 归属日期 dates[i+1]。
func computeStability(dates []string, grossRets, netRets []float64) StabilityMetrics {
	byYear := map[string]*YearReturn{}
	order := []string{}
	for i := range grossRets {
		y := yearOf(dates[i+1])
		yr, ok := byYear[y]
		if !ok {
			yr = &YearReturn{Year: y}
			byYear[y] = yr
			order = append(order, y)
		}
		yr.GrossReturn = (1+yr.GrossReturn)*(1+grossRets[i]) - 1
		yr.NetReturn = (1+yr.NetReturn)*(1+netRets[i]) - 1
		yr.TradingDays++
	}
	sort.Strings(order)
	out := make([]YearReturn, 0, len(order))
	for _, y := range order {
		out = append(out, *byYear[y])
	}
	return StabilityMetrics{ByYear: out}
}

// yearOf 从 YYYY-MM-DD 取年份（前 4 位）。
func yearOf(date string) string {
	if len(date) >= 4 {
		return date[:4]
	}
	return date
}

// ---- 账本派生 ----

// FromLedgers 从 Task 5 每日账本构建组合时序。
// 口径（文档化）：
//   - 净净值 = 期末权益；毛净值 = 期末权益 + 截至当日累计费用
//     （beginEquity + 当日毛利口径；首日费用为 0 时 毛累计 - 净累计 = 成本拖累 精确）；
//   - 换手 = 单边成交金额口径 = (Σ买入毛额 + Σ卖出毛额)/2 / 期初权益；
//   - 现金比例 = 期末现金/期末权益；持仓数 = 期末持仓代码数；
//   - 未成交记录 = 账本拒绝（未成交原因稳定枚举）。
//
// 目标/实际权重序列账本不含（仅供偏离统计），需由调用方补充。
func FromLedgers(ledgers []DailyLedger) PortfolioSeries {
	out := PortfolioSeries{Days: make([]PortfolioDay, 0, len(ledgers))}
	cumFees := 0.0
	for _, l := range ledgers {
		cumFees += l.Fees.Total()
		out.Days = append(out.Days, PortfolioDay{
			Date:       l.Date,
			GrossNav:   l.EndEquity + cumFees,
			NetNav:     l.EndEquity,
			Fees:       l.Fees.Total(),
			Turnover:   turnoverFromLedger(l),
			CashRatio:  cashRatioFromLedger(l),
			Holdings:   holdingCodes(l.EndHoldings),
			Rejections: l.Rejections,
		})
	}
	return out
}

// turnoverFromLedger 单边成交金额口径换手。
func turnoverFromLedger(l DailyLedger) float64 {
	var buy, sell float64
	for _, f := range l.Fills {
		if f.Side == SideBuy {
			buy += f.GrossAmount
		} else {
			sell += f.GrossAmount
		}
	}
	if l.BeginEquity <= 0 {
		return 0
	}
	return (buy + sell) / 2 / l.BeginEquity
}

// cashRatioFromLedger 期末现金比例（期末权益非正时 0）。
func cashRatioFromLedger(l DailyLedger) float64 {
	if l.EndEquity <= 0 {
		return 0
	}
	return l.EndCash / l.EndEquity
}

// holdingCodes 期末持仓代码数（去重）。
func holdingCodes(lots []HoldingLot) int {
	set := map[string]bool{}
	for _, l := range lots {
		set[l.Code] = true
	}
	return len(set)
}
