package lab

import (
	"context"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"sort"
	"sync"
	"time"

	common "github.com/injoyai/strategy-tail"
	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/internal/researchrun"
	"github.com/injoyai/strategy-tail/lib/extend"
	f "github.com/injoyai/strategy-tail/strategies/factor"
)

// analysis.go 因子研究：统计层（Task 12）与编排导出（Task 13）。
//
// 统计全部纯函数无 IO。单期 IC 用 Spearman 秩相关——对量纲不敏感，
// 只关心截面单调性；样本不足 minPairs 时汇总归零（无推断意义）。

// minPairs IC 汇总的最小有效样本数。
const minPairs = 10

// ICStats 一组逐期 IC 的汇总统计。
type ICStats struct {
	Pairs int     `json:"pairs"` // 有效样本数
	Mean  float64 `json:"mean"`  // 均值
	Std   float64 `json:"std"`   // 总体标准差
	TStat float64 `json:"tStat"` // Mean/(Std/√n)；Std=0 时 0
}

// avgRanks 升序平均名次（值最小名次 1，并列取均值），Spearman 基础。
func avgRanks(xs []float64) []float64 {
	n := len(xs)
	idx := make([]int, n)
	for i := range idx {
		idx[i] = i
	}
	sort.Slice(idx, func(i, j int) bool { return xs[idx[i]] < xs[idx[j]] })
	ranks := make([]float64, n)
	for i := 0; i < n; {
		j := i
		for j+1 < n && xs[idx[j+1]] == xs[idx[i]] {
			j++
		}
		avg := float64(i+j+2) / 2
		for k := i; k <= j; k++ {
			ranks[idx[k]] = avg
		}
		i = j + 1
	}
	return ranks
}

// spearmanIC 单期 IC：因子值名次与收益名次的 Pearson 相关。
// NaN 由调用方剔除（Pearson 遇 NaN 结果不可用）。
func spearmanIC(vals, rets []float64) float64 {
	return f.Pearson(avgRanks(vals), avgRanks(rets))
}

// QuintileSummary 分组收益摘要：方向与 spread 由后端计算，前端不做二次推断。
type QuintileSummary struct {
	Direction string   `json:"direction"` // ascending|descending|mixed|flat|insufficient
	Spread    *float64 `json:"spread"`    // 首末组收益差；组不完整为 null
	Monotonic bool     `json:"monotonic"`
}

// summarizeQuintiles n 组收益摘要（纯函数）：组数不足 n 或含 NaN 视为
// insufficient（spread null）；严格单调给方向；全相等为 flat。
func summarizeQuintiles(qs []float64, n int) QuintileSummary {
	if len(qs) < n {
		return QuintileSummary{Direction: "insufficient"}
	}
	for _, v := range qs {
		if math.IsNaN(v) {
			return QuintileSummary{Direction: "insufficient"}
		}
	}
	asc, desc := true, true
	for i := 1; i < len(qs); i++ {
		if qs[i] <= qs[i-1] {
			asc = false
		}
		if qs[i] >= qs[i-1] {
			desc = false
		}
	}
	s := QuintileSummary{}
	switch {
	case asc:
		s.Direction, s.Monotonic = "ascending", true
	case desc:
		s.Direction, s.Monotonic = "descending", true
	default:
		flat := true
		for _, v := range qs {
			if v != qs[0] {
				flat = false
				break
			}
		}
		if flat {
			s.Direction = "flat"
		} else {
			s.Direction = "mixed"
		}
	}
	spread := qs[len(qs)-1] - qs[0]
	s.Spread = &spread
	return s
}

// icStats 汇总逐期 IC：剔除 NaN，有效样本 < minPairs 时全 0。
func icStats(ics []float64) ICStats {
	valid := make([]float64, 0, len(ics))
	for _, v := range ics {
		if !math.IsNaN(v) {
			valid = append(valid, v)
		}
	}
	var s ICStats
	s.Pairs = len(valid)
	if s.Pairs < minPairs {
		return s
	}
	sum := 0.0
	for _, v := range valid {
		sum += v
	}
	mean := sum / float64(s.Pairs)
	ss := 0.0
	for _, v := range valid {
		ss += (v - mean) * (v - mean)
	}
	s.Mean = mean
	s.Std = math.Sqrt(ss / float64(s.Pairs))
	if s.Std > 0 {
		s.TStat = s.Mean / (s.Std / math.Sqrt(float64(s.Pairs)))
	}
	return s
}

// AnalyzeConfig 因子分析配置：内嵌 RunConfig 复用年份/样本解析；
// 卖出规则与分析无关（不校验）。
type AnalyzeConfig struct {
	RunConfig
	Kind string `json:"kind"` // 因子类型（registry kind）
	Days int    `json:"days"` // 因子窗口参数（≤0 用各因子默认）
	// Window 未来收益窗口（交易日），legacy 字段：仅 Protocol=nil 的 v3 分析
	// 使用（1-60）。带协议的 v4 分析以 Protocol.Labels.Horizons 为准，
	// Window 必须为 0，报告中的 Window 只镜像主周期（horizons[0]）。
	Window   int            `json:"window"`
	Grouping GroupingConfig `json:"grouping,omitempty"` // 分组方式；缺省等数量 5 组
	// Protocol 研究协议（可选）：nil 保持 v3 执行与响应；非 nil 输出
	// AnalysisVersion=4 并保存协议、hash 与证据等级。
	Protocol *ResearchProtocol `json:"protocol,omitempty"`
}

// GroupingConfig 分组方式配置：mode 缺省（空）或 quantile 为每日等数量分组，
// 组数由 Groups 决定（缺省 5）；bins 为固定数值区间，cuts 必须恰好 Groups-1
// 个严格递增断点，形成首末开放、中间左开右闭的分组。
type GroupingConfig struct {
	Mode   string    `json:"mode,omitempty"`   // quantile | bins
	Groups int       `json:"groups,omitempty"` // 分组数；0=缺省 5；有效 2-20
	Cuts   []float64 `json:"cuts,omitempty"`   // bins 模式必须恰好 Groups-1 个
}

// 分组数允许范围（Groups=0 视为缺省 5，不参与该范围校验）。
const (
	minGroups = 2
	maxGroups = 20
)

// groupCount 生效分组数：Groups=0 视为默认 5。Validate 与聚合统计共用
// 同一解析，禁止两处各写一遍缺省逻辑。
func (c GroupingConfig) groupCount() int {
	if c.Groups == 0 {
		return 5
	}
	return c.Groups
}

// Validate 校验分组配置：未知模式报错；分组数缺省（0）或 2-20；
// quantile 不接受断点；bins 要求恰好 groupCount()-1 个有限且严格递增的
// 断点（-0 与 0 视为重复）。
func (c GroupingConfig) Validate() error {
	if c.Groups != 0 && (c.Groups < minGroups || c.Groups > maxGroups) {
		return fmt.Errorf("分组数无效: %d（应为 %d-%d 或缺省 5）", c.Groups, minGroups, maxGroups)
	}
	switch c.Mode {
	case "", "quantile":
		if len(c.Cuts) > 0 {
			return fmt.Errorf("等数量分组模式不接受断点")
		}
	case "bins":
		if want := c.groupCount() - 1; len(c.Cuts) != want {
			return fmt.Errorf("固定区间模式需要恰好 %d 个断点, 得到 %d 个", want, len(c.Cuts))
		}
		for i, v := range c.Cuts {
			if math.IsNaN(v) || math.IsInf(v, 0) {
				return fmt.Errorf("断点 %d 不是有限值", i+1)
			}
			if i > 0 && v <= c.Cuts[i-1] {
				return fmt.Errorf("断点必须严格递增: %v", c.Cuts)
			}
		}
	default:
		return fmt.Errorf("未知分组模式: %s", c.Mode)
	}
	return nil
}

// factorObs 单条因子观测：股票与因子值。分组只需值与 code tie-break，
// 收益与统计由调用方按返回的组号归集。
type factorObs struct {
	Code  string
	Value float64
}

// quantileAssign 等数量 g 组（分位）分组：观测按因子值升序、code 升序排序，
// 相同因子值的连续观测为并列块，整块按排序位置中点归组（避免拆块产生
// 虚假组间差异），公式 floor(((i+j)/2)×g/n) 以整数倍增 (i+j)×g/(2n) 计算。
// 返回与输入同序的组号 0..g-1（0=因子值最低组）；相同输入结果恒定。
func quantileAssign(obs []factorObs, g int) []int {
	n := len(obs)
	idx := make([]int, n)
	for i := range idx {
		idx[i] = i
	}
	sort.Slice(idx, func(a, b int) bool {
		if obs[idx[a]].Value != obs[idx[b]].Value {
			return obs[idx[a]].Value < obs[idx[b]].Value
		}
		return obs[idx[a]].Code < obs[idx[b]].Code
	})
	out := make([]int, n)
	for i := 0; i < n; {
		j := i
		for j+1 < n && obs[idx[j+1]].Value == obs[idx[i]].Value {
			j++
		}
		gr := (i + j) * g / (2 * n) // 中点公式整数形式；结果天然在 0..g-1
		for k := i; k <= j; k++ {
			out[idx[k]] = gr
		}
		i = j + 1
	}
	return out
}

// binGroup 固定区间分组：断点值归左侧组，即 v≤b1→B1、b1<v≤b2→B2、…、
// v>b4→B5；调用方保证 cuts 为 4 个严格递增有限值。
func binGroup(v float64, cuts []float64) int {
	for i, c := range cuts {
		if v <= c {
			return i
		}
	}
	return 4
}

// percentileSorted Type-7 线性插值百分位（输入须升序）：h=(n-1)p，
// q=x[⌊h⌋]+(h-⌊h⌋)×(x[⌈h⌉]-x[⌊h⌋])，与常见数据分析工具默认口径一致。
func percentileSorted(sorted []float64, p float64) float64 {
	n := len(sorted)
	if n == 0 {
		return math.NaN()
	}
	h := float64(n-1) * p
	lo := int(math.Floor(h))
	hi := int(math.Ceil(h))
	if hi > n-1 {
		hi = n - 1
	}
	return sorted[lo] + (h-math.Floor(h))*(sorted[hi]-sorted[lo])
}

// groupValueStats 组内因子值分布统计（原始值）。
type groupValueStats struct {
	Min, P25, Median, Mean, P75, Max, Std float64
}

// valueStats 汇总一组因子值：P25/Median/P75 用 Type-7 插值，
// Std 为总体标准差（除以 n）；空输入返回 nil（空组 JSON null 语义）。
func valueStats(vals []float64) *groupValueStats {
	n := len(vals)
	if n == 0 {
		return nil
	}
	x := append([]float64(nil), vals...)
	sort.Float64s(x)
	sum := 0.0
	for _, v := range x {
		sum += v
	}
	mean := sum / float64(n)
	ss := 0.0
	for _, v := range x {
		ss += (v - mean) * (v - mean)
	}
	return &groupValueStats{
		Min:    x[0],
		P25:    percentileSorted(x, 0.25),
		Median: percentileSorted(x, 0.5),
		Mean:   mean,
		P75:    percentileSorted(x, 0.75),
		Max:    x[n-1],
		Std:    math.Sqrt(ss / float64(n)),
	}
}

// Validate 校验分析配置（复用回测的年份/样本检查）。
// Protocol 非 nil 时进入 v4 合同：协议本身必须合法，Window 必须为 0
// （未来收益窗口由 Protocol.Labels.Horizons 承担），其余检查不变。
func (c AnalyzeConfig) Validate() error {
	if err := c.validateYears(); err != nil {
		return err
	}
	if err := c.validateSample(); err != nil {
		return err
	}
	if c.Protocol != nil {
		if err := c.Protocol.Validate(); err != nil {
			return fmt.Errorf("研究协议非法: %w", err)
		}
		if c.Window != 0 {
			return fmt.Errorf("带协议的 v4 分析不接受 legacy window 字段: %d（请使用 protocol.labels.horizons）", c.Window)
		}
	} else if c.Window < 1 || c.Window > 60 {
		return fmt.Errorf("未来收益窗口无效: %d（应为 1-60 交易日）", c.Window)
	}
	if f.BuildContext(c.Kind, c.Days) == nil {
		return fmt.Errorf("未知因子类型: %s", c.Kind)
	}
	if err := c.Grouping.Validate(); err != nil {
		return err
	}
	return nil
}

// FactorSnapshot 因子元数据快照：报告自包含，前端与历史文件无需再查因子目录。
type FactorSnapshot struct {
	Kind           string `json:"kind"`
	Name           string `json:"name"`
	Description    string `json:"description"`
	ParameterLabel string `json:"parameterLabel"`
	Days           int    `json:"days"`
	Unit           string `json:"unit"` // ratio | multiple | score | correlation
	// ImplementationVersion 因子实现版本快照（来自 registry.Catalog）。候选保存
	// 与旧报告展示依赖该字段；缺失时反序列化为 0，仅用于展示、不可保存为候选。
	ImplementationVersion int `json:"implementationVersion"`
}

// FactorGroupStats 单组统计：因子值分布为全样本累计（原始值），
// ForwardReturn 为每日组均值再按日期等权；空组数值字段为 null。
type FactorGroupStats struct {
	Index         int      `json:"index"` // 1..N
	Label         string   `json:"label"` // Q1..QN 或 B1..BN
	Lower         *float64 `json:"lower"` // 固定区间边界；开放端为 null
	Upper         *float64 `json:"upper"`
	FactorMin     *float64 `json:"factorMin"`
	FactorP25     *float64 `json:"factorP25"`
	FactorMedian  *float64 `json:"factorMedian"`
	FactorMean    *float64 `json:"factorMean"`
	FactorP75     *float64 `json:"factorP75"`
	FactorMax     *float64 `json:"factorMax"`
	FactorStd     *float64 `json:"factorStd"`
	Observations  int      `json:"observations"` // 股票×日期有效观测数
	Dates         int      `json:"dates"`        // 该组非空日期数
	CountPct      float64  `json:"countPct"`     // 占全部有效观测比例，0..1
	ForwardReturn *float64 `json:"forwardReturn"`
}

// f64p 数值指针（null 语义：不存在为 nil，不以 0 冒充）。
func f64p(v float64) *float64 { return &v }

// GroupingSet 某一组数下的完整分组结果：Quintiles 镜像组收益（组不完整为
// null），Stats 为每组因子值分布与计数（仅全区间填充；年度只填
// Quintiles/Summary 以控制报告体积），Summary 为方向摘要。
type GroupingSet struct {
	Groups    int                `json:"groups"`
	Quintiles []float64          `json:"quintiles"`
	Stats     []FactorGroupStats `json:"stats,omitempty"`
	Summary   QuintileSummary    `json:"summary"`
}

// summarizeFromStats 从组统计提取组收益并求摘要：任一组无收益即 insufficient。
// 全区间与年度共用，保证两种口径一致。
func summarizeFromStats(stats []FactorGroupStats, g int) ([]float64, QuintileSummary) {
	qs := make([]float64, 0, g)
	complete := true
	for _, gs := range stats {
		if gs.ForwardReturn == nil {
			complete = false
			break
		}
		qs = append(qs, *gs.ForwardReturn)
	}
	if !complete {
		qs = nil
	}
	return qs, summarizeQuintiles(qs, g)
}

// AnalysisRange 本次分析实际使用的配置区间与样本口径。报告必须能自证区间，
// 因为“配置区间”与“实际有效日期”（见 AnalysisReport.FirstDataDate）可能不同：
// 早期年份无数据、后上市股票不参与早期统计、未来收益窗口尾部被剔除等都会缩短
// 实际有效区间。
type AnalysisRange struct {
	StartYear  int    `json:"startYear"`
	EndYear    int    `json:"endYear"`
	SampleMode string `json:"sampleMode"`           // all | random | codes
	SampleSize int    `json:"sampleSize,omitempty"` // 实际请求代码数
}

// YearAnalysis 单年度统计，与全区间使用同一批逐日观测、同口径（有效交易日
// 等权）。有效日不足 minPairs 时 Stats.Pairs 保留样本数、其余字段为 0，
// Quintiles 为 nil、Summary 为 insufficient：零值不得被解释为“真实 IC 为零”。
type YearAnalysis struct {
	Year        int             `json:"year"`
	Stats       ICStats         `json:"stats"`
	Quintiles   []float64       `json:"quintiles"` // 该年分组收益（长度=组数）；组不完整为 null
	Summary     QuintileSummary `json:"summary"`
	TradingDays int             `json:"tradingDays"` // 该年有横截面观测的交易日数
	// AllGroupings 该年全部组数（2-20）的等频预算（仅 quantile 模式；只含
	// Quintiles/Summary，无组内统计，控制体积）；bins 或历史报告为 nil。
	AllGroupings []GroupingSet `json:"allGroupings"`
}

// AnalysisReport 因子分析报告。
type AnalysisReport struct {
	// AnalysisID 不可变分析标识（v3）：an_<UTC时间>_<8hex>，服务端生成，
	// 用于不可变历史目录与候选证据追溯；旧报告缺省为空串。
	AnalysisID string        `json:"analysisId"`
	FactorName string        `json:"factorName"` // 因子中文名（如 N日动量(2)）
	Kind       string        `json:"kind"`
	Window     int           `json:"window"`
	Range      AnalysisRange `json:"range"`
	// v3 新增：不可变 analysisId（Task 2）与因子实现版本快照；
	// v2 新增：固定区间模式以 Groups 为唯一分组结果；分位模式组完整时
	// Quintiles 镜像 Groups 的 ForwardReturn（旧页面兼容），否则为 null。
	AnalysisVersion int                `json:"analysisVersion"`
	Factor          FactorSnapshot     `json:"factor"`
	Grouping        GroupingConfig     `json:"grouping"`
	Groups          []FactorGroupStats `json:"groups"`
	// AllGroupings 全部组数（2-20）的等频预算结果，前端切组零重算（即时切换
	// 无需重新分析）。仅 quantile 模式填充；bins 模式或历史报告为 nil。
	AllGroupings []GroupingSet `json:"allGroupings"`
	// Years 为年度拆分（升序）：跨周期汇总不得掩盖某些年份失效或反向。
	// 历史报告无此字段时为空切片，前端显示“历史报告未记录”。
	Years     []YearAnalysis       `json:"years"`
	Stats     ICStats              `json:"stats"`
	Quintiles []float64            `json:"quintiles"` // 分位模式分组收益（长度=组数）；组不完整或 bins 模式为 nil
	Summary   QuintileSummary      `json:"summary"`
	Coverage  researchrun.Coverage `json:"coverage"`
	// YearCoverage 逐“股票×年份”覆盖率：回测按整票判定，分析保留同一股票的
	// 其他可用年份，缺失年份只在此披露，不静默作废整只股票。
	YearCoverage researchrun.YearCoverage `json:"yearCoverage"`
	// FirstDataDate/LastDataDate 为成功生成标签的首末交易日；无有效日为空串。
	FirstDataDate string    `json:"firstDataDate"`
	LastDataDate  string    `json:"lastDataDate"`
	Daily         []DailyIC `json:"daily"`
	StartedAt     string    `json:"startedAt"`
	FinishedAt    string    `json:"finishedAt"`

	// —— v4 新增字段（Protocol 非 nil 的分析才填充；均 omitempty，旧报告
	// 序列化不出现这些键）——
	// Protocol 冻结的研究协议副本；改变方向、窗口、标签或股票池构成新变体。
	Protocol *ResearchProtocol `json:"protocol,omitempty"`
	// ProtocolHash 协议内容 SHA-256（64 hex），绑定协议与报告。
	ProtocolHash string `json:"protocolHash,omitempty"`
	// EvidenceClass 证据等级：exploratory | retrospective。旧报告（无协议）
	// 派生 exploratory + same_close_to_close_legacy；prospective 仅属于验证层。
	EvidenceClass string `json:"evidenceClass,omitempty"`
	// Horizons 多周期观察的周期列表（升序），与 Protocol.Labels.Horizons 一致。
	// v3 镜像字段（Window/Stats/Groups/Years/Daily）在 v4 中只镜像主周期
	//（horizons[0]），不作为 v4 的 canonical 数据。
	Horizons []int `json:"horizons,omitempty"`
}

// DailyIC 单日横截面 IC；IC=nil 表示当日无效（NaN，JSON 序列化为 null）。
type DailyIC struct {
	Date string   `json:"date"`
	IC   *float64 `json:"ic"`
}

// dayIC 一组交易日的逐日 IC 与汇总。
type dayIC struct {
	Daily []DailyIC
	Stats ICStats
}

// aggregateIC 对一组交易日计算逐日 Spearman IC 与该组汇总（交易日等权）。
// days 须按日期升序且 vals/rets 覆盖其中日期；全区间与年度共用本函数，
// 避免“先算年度均值再加权”——那样交易日较少的年份会获得过高权重。
func aggregateIC(days []time.Time, vals, rets map[time.Time]map[string]float64) dayIC {
	daily := make([]DailyIC, 0, len(days))
	ics := make([]float64, 0, len(days))
	for _, day := range days {
		vs := make([]float64, 0, len(vals[day]))
		rs := make([]float64, 0, len(vals[day]))
		for c, v := range vals[day] {
			vs = append(vs, v)
			rs = append(rs, rets[day][c])
		}
		di := DailyIC{Date: day.Format("2006-01-02")}
		if ic := spearmanIC(vs, rs); !math.IsNaN(ic) {
			ics = append(ics, ic)
			v := ic
			di.IC = &v
		}
		daily = append(daily, di)
	}
	return dayIC{Daily: daily, Stats: icStats(ics)}
}

// groupDaysByYear 把升序交易日按自然年切成连续分组（组内仍升序），供年度
// 统计使用；空输入返回 nil。
func groupDaysByYear(days []time.Time) [][]time.Time {
	var groups [][]time.Time
	for i, day := range days {
		if i == 0 || day.Year() != days[i-1].Year() {
			groups = append(groups, nil)
		}
		groups[len(groups)-1] = append(groups[len(groups)-1], day)
	}
	return groups
}

// sortFailures 失败明细按 code/year/stage 排序，保证报告与测试稳定。
func sortFailures(fs []researchrun.Failure) {
	sort.Slice(fs, func(i, j int) bool {
		if fs[i].Code != fs[j].Code {
			return fs[i].Code < fs[j].Code
		}
		if fs[i].Year != fs[j].Year {
			return fs[i].Year < fs[j].Year
		}
		return fs[i].Stage < fs[j].Stage
	})
}

// aggregateGroups 对一组交易日聚合分组统计：分位模式按并列块整块归组
// （确定性 tie-break），bins 模式按固定区间；组数由 grouping.groupCount()
// 决定。组内因子值为全样本累计（原始值），组收益先算日内组均值、再对
// 非空交易日等权。返回全部组统计、组收益（任一组无收益即为 nil，不以 0
// 冒充）与摘要。全区间与年度共用本函数，保证两者口径一致。
func aggregateGroups(days []time.Time, vals, rets map[time.Time]map[string]float64,
	grouping GroupingConfig) ([]FactorGroupStats, []float64, QuintileSummary) {
	isBins := grouping.Mode == "bins"
	g := grouping.groupCount()
	accVals := make([][]float64, g) // 组内因子值（全样本累计）
	accObs := make([]int, g)        // 有效观测数
	dayCnts := make([]int, g)       // 组非空日期数
	dayRets := make([]float64, g)   // 组非空日期的日内组均值之和
	for _, day := range days {
		obs := make([]factorObs, 0, len(vals[day]))
		for c, v := range vals[day] {
			obs = append(obs, factorObs{Code: c, Value: v})
		}
		assign := make([]int, len(obs))
		if isBins {
			for i, o := range obs {
				assign[i] = binGroup(o.Value, grouping.Cuts)
			}
		} else {
			assign = quantileAssign(obs, g)
		}
		gSums, gCnts := make([]float64, g), make([]int, g)
		for i, o := range obs {
			k := assign[i]
			accVals[k] = append(accVals[k], o.Value)
			accObs[k]++
			gSums[k] += rets[day][o.Code]
			gCnts[k]++
		}
		for k := 0; k < g; k++ {
			if gCnts[k] > 0 {
				dayCnts[k]++
				dayRets[k] += gSums[k] / float64(gCnts[k])
			}
		}
	}

	totalObs := 0
	for _, n := range accObs {
		totalObs += n
	}
	groups := make([]FactorGroupStats, g)
	for k := range groups {
		gs := FactorGroupStats{Index: k + 1}
		if isBins {
			gs.Label = fmt.Sprintf("B%d", k+1)
			if k > 0 {
				gs.Lower = f64p(grouping.Cuts[k-1])
			}
			if k < g-1 {
				gs.Upper = f64p(grouping.Cuts[k])
			}
		} else {
			gs.Label = fmt.Sprintf("Q%d", k+1)
		}
		if st := valueStats(accVals[k]); st != nil {
			gs.FactorMin, gs.FactorP25, gs.FactorMedian = f64p(st.Min), f64p(st.P25), f64p(st.Median)
			gs.FactorMean, gs.FactorP75, gs.FactorMax, gs.FactorStd =
				f64p(st.Mean), f64p(st.P75), f64p(st.Max), f64p(st.Std)
		}
		gs.Observations = accObs[k]
		gs.Dates = dayCnts[k]
		if totalObs > 0 {
			gs.CountPct = float64(accObs[k]) / float64(totalObs)
		}
		if dayCnts[k] > 0 {
			gs.ForwardReturn = f64p(dayRets[k] / float64(dayCnts[k]))
		}
		groups[k] = gs
	}

	// 摘要两种模式同口径：基于全部组 ForwardReturn，任一组无收益即 insufficient。
	summaryQs, summary := summarizeFromStats(groups, g)
	return groups, summaryQs, summary
}

// aggregateQuantileSets 对一组交易日预算全部组数（minGroups..maxGroups）的
// 等频分组结果。分位排序与组数无关：每日横截面按 value/code 只排序一次并
// 缓存（值与收益切片对齐），各档按中点公式 (i+j)*g/(2n) 线性扫描归组
// （tie block 整块不拆分）。按档逐个处理，fullStats 时单档组内值算完即
// 释放，内存峰值 = 排序缓存 + 单档组桶，避免 19 档同时驻留。返回组数升序
// 的 19 个 GroupingSet；fullStats=true 时填充每组因子值分布（全区间用），
// 否则只填 Quintiles/Summary（年度用，控制体积）。仅 quantile 模式调用
// （bins 断点随组数变化，不参与切组预算）。
func aggregateQuantileSets(days []time.Time, vals, rets map[time.Time]map[string]float64,
	fullStats bool) []GroupingSet {
	// 每日横截面排序一次并预取收益切片：后续各档按索引访问，避免逐观测
	// map 查找（与 quantileAssign 同序，保证切组结果与单档请求完全一致）。
	type daySlice struct {
		vals []float64
		rets []float64
	}
	cache := make([]daySlice, 0, len(days))
	for _, day := range days {
		obs := make([]factorObs, 0, len(vals[day]))
		for c, v := range vals[day] {
			obs = append(obs, factorObs{Code: c, Value: v})
		}
		sort.Slice(obs, func(a, b int) bool {
			if obs[a].Value != obs[b].Value {
				return obs[a].Value < obs[b].Value
			}
			return obs[a].Code < obs[b].Code
		})
		n := len(obs)
		ds := daySlice{vals: make([]float64, n), rets: make([]float64, n)}
		for i, o := range obs {
			ds.vals[i] = o.Value
			ds.rets[i] = rets[day][o.Code]
		}
		cache = append(cache, ds)
	}

	allG := make([]int, 0, maxGroups-minGroups+1)
	for g := minGroups; g <= maxGroups; g++ {
		allG = append(allG, g)
	}
	sets := make([]GroupingSet, len(allG))
	for si, g := range allG {
		accVals := make([][]float64, g) // 组内因子值（仅 fullStats 使用）
		accObs := make([]int, g)
		dayCnts := make([]int, g)
		dayRets := make([]float64, g)
		for _, ds := range cache {
			n := len(ds.vals)
			if n == 0 {
				continue
			}
			gSums, gCnts := make([]float64, g), make([]int, g)
			for i := 0; i < n; {
				j := i
				for j+1 < n && ds.vals[j+1] == ds.vals[i] {
					j++
				}
				gr := (i + j) * g / (2 * n) // 中点公式整数形式
				for k := i; k <= j; k++ {
					if fullStats {
						accVals[gr] = append(accVals[gr], ds.vals[k])
					}
					accObs[gr]++
					gSums[gr] += ds.rets[k]
					gCnts[gr]++
				}
				i = j + 1
			}
			for gr := 0; gr < g; gr++ {
				if gCnts[gr] > 0 {
					dayCnts[gr]++
					dayRets[gr] += gSums[gr] / float64(gCnts[gr])
				}
			}
		}

		set := GroupingSet{Groups: g}
		if fullStats {
			totalObs := 0
			for _, n := range accObs {
				totalObs += n
			}
			set.Stats = make([]FactorGroupStats, g)
			for k := 0; k < g; k++ {
				gs := FactorGroupStats{Index: k + 1, Label: fmt.Sprintf("Q%d", k+1)}
				if st := valueStats(accVals[k]); st != nil {
					gs.FactorMin, gs.FactorP25, gs.FactorMedian = f64p(st.Min), f64p(st.P25), f64p(st.Median)
					gs.FactorMean, gs.FactorP75, gs.FactorMax, gs.FactorStd =
						f64p(st.Mean), f64p(st.P75), f64p(st.Max), f64p(st.Std)
				}
				gs.Observations = accObs[k]
				gs.Dates = dayCnts[k]
				if totalObs > 0 {
					gs.CountPct = float64(accObs[k]) / float64(totalObs)
				}
				if dayCnts[k] > 0 {
					gs.ForwardReturn = f64p(dayRets[k] / float64(dayCnts[k]))
				}
				set.Stats[k] = gs
			}
			set.Quintiles, set.Summary = summarizeFromStats(set.Stats, g)
		} else {
			// 年度：不做组内值统计，只算组收益与摘要
			qs := make([]float64, 0, g)
			complete := true
			for k := 0; k < g; k++ {
				if dayCnts[k] == 0 {
					complete = false
					break
				}
				qs = append(qs, dayRets[k]/float64(dayCnts[k]))
			}
			if !complete {
				qs = nil
			}
			set.Quintiles = qs
			set.Summary = summarizeQuintiles(qs, g)
		}
		sets[si] = set
	}
	return sets
}

// runAnalysis 因子分析主循环：逐日横截面 因子值名次 vs 未来收益名次 → Spearman IC。
// 口径与 fillCrossSection 一致：series=his+dks，前缀无前视；未来收益
// dks[i+Window].Close/dks[i].Close−1，尾部 Window 日无未来收益剔除。
// id 为服务端生成的不可变分析 ID；为空时（直接构造 Runner 的测试路径）
// 在此生成，AnalysisReport.AnalysisID 导出前完成赋值。
func (r *Runner) runAnalysis(id string, cfg AnalyzeConfig, stop chan struct{}) (*AnalysisReport, error) {
	started := time.Now()

	if id == "" {
		var err error
		if id, err = generateAnalysisID(); err != nil {
			return nil, fmt.Errorf("生成分析 ID 失败: %w", err)
		}
	}

	codes, err := r.resolveCodes(cfg.RunConfig, stop)
	if err != nil {
		return nil, err
	}
	r.totalCodes.Store(int64(len(codes)))
	years := cfg.RunConfig.years()
	fct := f.BuildContext(cfg.Kind, cfg.Days)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		select {
		case <-stop:
			cancel()
		case <-ctx.Done():
		}
	}()

	var mu sync.Mutex
	var mergedV, mergedR []map[time.Time]map[string]float64 // day → code → 值/收益
	// OnCodeDone 串行上报（mu 保护），直接汇总无需额外锁
	coverage := researchrun.Coverage{Requested: len(codes)}

	// 逐“股票×年份”加载：单年缺失只记录该代码年份，不抹掉同一股票的其他年份
	// （回测仍走 ForEachCodeData 的整票一致性语义，不受影响）。
	yearCoverage, err := researchrun.ForEachCodeYearData(ctx, researchrun.Config{
		Codes:        codes,
		Years:        years,
		Workers:      common.DefaultGoroutines * 2,
		DataMode:     researchrun.DailyClose,
		GetDayKlines: common.Pull.DayKlines,
		OnCodeDone: func(progress researchrun.Progress) {
			r.doneCodes.Store(int64(progress.Done))
			current := progress.Code
			r.currentCode.Store(&current)
			if progress.Failure == nil {
				coverage.Completed++
			} else {
				coverage.Skipped++
				coverage.Failures = append(coverage.Failures, *progress.Failure)
			}
		},
	}, func(code string, _ int, d researchrun.YearData) {
		lv := map[time.Time]map[string]float64{}
		lr := map[time.Time]map[string]float64{}
		series := make(extend.Klines, 0, len(d.His)+len(d.Dks))
		series = append(series, d.His...)
		series = append(series, d.Dks...)
		base := len(d.His)
		for i := 0; i+cfg.Window < len(d.Dks); i++ {
			v := fct.ValueAt(core.FactorContext{
				Code:   code,
				AsOf:   d.Dks[i].Time,
				Klines: series[:base+i+1],
				Data:   r.factorData,
			})
			ret := d.Dks[i+cfg.Window].Close.Float64()/d.Dks[i].Close.Float64() - 1
			// NaN/±Inf 值与该票该日一并剔除：spearmanIC/quantileAssign 契约要求入参无 NaN，
			// 且 Inf 会传入 Quintiles 使 report.json 序列化失败（退化数据如收盘价 0）
			if math.IsNaN(v) || math.IsInf(v, 0) || math.IsNaN(ret) || math.IsInf(ret, 0) {
				continue
			}
			day := core.DayOf(d.Dks[i].Time)
			if lv[day] == nil {
				lv[day] = map[string]float64{}
				lr[day] = map[string]float64{}
			}
			lv[day][code] = v
			lr[day][code] = ret
		}
		mu.Lock()
		mergedV = append(mergedV, lv)
		mergedR = append(mergedR, lr)
		mu.Unlock()
	})
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return nil, errStopped
		}
		return nil, err
	}

	select {
	case <-stop:
		return nil, errStopped
	default:
	}

	// Failure 按 code/year/stage 排序，保证报告与测试稳定
	sortFailures(coverage.Failures)
	sortFailures(yearCoverage.Failures)

	// 合并各票数据（单协程，无竞争）
	vals := map[time.Time]map[string]float64{}
	rets := map[time.Time]map[string]float64{}
	for i, m := range mergedV {
		for day, cv := range m {
			if vals[day] == nil {
				vals[day] = map[string]float64{}
				rets[day] = map[string]float64{}
			}
			for c, v := range cv {
				vals[day][c] = v
				rets[day][c] = mergedR[i][day][c]
			}
		}
	}

	// 逐日计算 IC（日期升序输出）
	days := make([]time.Time, 0, len(vals))
	for day := range vals {
		days = append(days, day)
	}
	sort.Slice(days, func(i, j int) bool { return days[i].Before(days[j]) })

	// 全区间：逐日 IC 与分组统计均按交易日等权。禁止先算年度均值再加权——
	// 那样交易日较少的年份会获得过高权重。
	overall := aggregateIC(days, vals, rets)
	isBins := cfg.Grouping.Mode == "bins"
	// quantile 预算 2-20 全档（前端切组零重算），请求档直接从对应档位取，
	// 避免再跑一遍单档聚合（每天只排序一次）；bins 断点固定，只算请求档。
	var groups []FactorGroupStats
	var summaryQs []float64
	var summary QuintileSummary
	var allGroupings []GroupingSet
	if isBins {
		groups, summaryQs, summary = aggregateGroups(days, vals, rets, cfg.Grouping)
	} else {
		allGroupings = aggregateQuantileSets(days, vals, rets, true)
		req := allGroupings[cfg.Grouping.groupCount()-minGroups]
		groups, summaryQs, summary = req.Stats, req.Quintiles, req.Summary
	}

	// 年度拆分：同一批逐日观测按自然年切段、以与全区间相同的口径重算，
	// 使跨周期汇总无法掩盖某些年份失效或反向。
	yearly := make([]YearAnalysis, 0, len(years))
	for _, gd := range groupDaysByYear(days) {
		yic := aggregateIC(gd, vals, rets)
		var yqs []float64
		var ysum QuintileSummary
		var yaAll []GroupingSet
		if isBins {
			_, yqs, ysum = aggregateGroups(gd, vals, rets, cfg.Grouping)
		} else {
			yaAll = aggregateQuantileSets(gd, vals, rets, false)
			yreq := yaAll[cfg.Grouping.groupCount()-minGroups]
			yqs, ysum = yreq.Quintiles, yreq.Summary
		}
		yearly = append(yearly, YearAnalysis{
			Year:         gd[0].Year(),
			Stats:        yic.Stats,
			Quintiles:    yqs,
			Summary:      ysum,
			TradingDays:  len(gd),
			AllGroupings: yaAll,
		})
	}

	// 实际有效日期：成功生成标签的首末交易日，数据不足时短于配置区间
	first, last := "", ""
	if len(days) > 0 {
		first = days[0].Format("2006-01-02")
		last = days[len(days)-1].Format("2006-01-02")
	}

	// 因子快照：Days 解析为生效窗口（≤0 时用因子目录默认值）。
	entry, _ := f.Catalog(cfg.Kind)
	effDays := cfg.Days
	if effDays <= 0 {
		effDays = entry.DefaultDays
	}

	rep := &AnalysisReport{
		FactorName: fct.Name(),
		Kind:       cfg.Kind,
		Window:     cfg.Window,
		Range: AnalysisRange{
			StartYear:  cfg.RunConfig.StartYear,
			EndYear:    cfg.RunConfig.EndYear,
			SampleMode: cfg.RunConfig.SampleMode,
			SampleSize: len(codes),
		},
		AnalysisVersion: 3,
		Factor: FactorSnapshot{
			Kind:                  cfg.Kind,
			Name:                  fct.Name(),
			Description:           entry.Description,
			ParameterLabel:        entry.ParameterLabel,
			Days:                  effDays,
			Unit:                  entry.Unit,
			ImplementationVersion: entry.ImplementationVersion,
		},
		Grouping:      cfg.Grouping,
		Groups:        groups,
		AllGroupings:  allGroupings,
		Years:         yearly,
		Stats:         overall.Stats,
		Summary:       summary,
		Coverage:      coverage,
		YearCoverage:  yearCoverage,
		FirstDataDate: first,
		LastDataDate:  last,
		Daily:         overall.Daily,
		StartedAt:     started.Format(time.RFC3339),
		FinishedAt:    time.Now().Format(time.RFC3339),
	}
	if !isBins && summaryQs != nil { // 旧页面兼容：分位模式组完整时镜像组收益
		rep.Quintiles = summaryQs
	}
	// 分析 ID 在导出前完成赋值：不可变历史与候选证据追溯以此为准。
	rep.AnalysisID = id

	store := r.store
	if store == nil {
		// 直接构造的 Runner（旧测试路径）使用 cwd 相对默认根；
		// 生产与 Server 路径始终显式注入。
		store = NewAnalysisStore(filepath.Join("output", "factor"))
	}
	if err := store.Save(rep); err != nil {
		return nil, fmt.Errorf("分析报告落盘失败: %w", err)
	}
	return rep, nil
}

// analysisHTML 分析报告页模板（ECharts 逐日 IC 折线；%% 为 CSS 转义）。
// 渲染逻辑在 analysis_store.go（不可变版本目录与兼容镜像共用）。
const analysisHTML = `<!DOCTYPE html>
<html>
<head>
<meta charset="utf-8">
<title>%s IC 分析</title>
<script src="https://cdn.jsdelivr.net/npm/echarts@5/dist/echarts.min.js"></script>
</head>
<body>
<h2>%s · 未来 %d 交易日 IC</h2>
<p>均值 %.4f · 标准差 %.4f · t 值 %.2f · 有效样本 %d 日</p>
<div id="chart" style="width:100%%;height:420px"></div>
<script>
const days = %s, ics = %s;
echarts.init(document.getElementById('chart')).setOption({
  tooltip: {trigger: 'axis'},
  xAxis: {type: 'category', data: days},
  yAxis: {type: 'value', min: -1, max: 1},
  series: [{type: 'line', data: ics, connectNulls: true, markLine: {data: [{yAxis: 0}]}}]
});
</script>
</body>
</html>`
