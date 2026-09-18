package lab

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	common "github.com/injoyai/strategy-tail"
	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/internal/researchrun"
	"github.com/injoyai/strategy-tail/researchdata"
	sb "github.com/injoyai/strategy-tail/strategies/buy"
	ss "github.com/injoyai/strategy-tail/strategies/sell"
	"github.com/injoyai/tdx/protocol"
)

// runner.go 回测任务执行器：单任务互斥 + 进度上报 + 落盘。
//
// 复用 backtest_tail 的性能模式：每股数据只读一次、内存循环全部变体
// （每变体 cloneKlines 隔离，防引擎 Do() 指针覆写互相污染）。

// RunConfig 运行配置（页面表单提交）。
type RunConfig struct {
	StartYear   int      `json:"startYear"`   // 回测起始年（含）
	EndYear     int      `json:"endYear"`     // 回测结束年（含）
	SampleMode  string   `json:"sampleMode"`  // all=全部 | random=随机N只 | codes=指定代码
	SampleSize  int      `json:"sampleSize"`  // random 模式的 N
	SampleCodes []string `json:"sampleCodes"` // codes 模式的代码列表
	// 卖出规则（页面表单 → sell.Or 组合）
	HoldingDays int     `json:"holdingDays"` // 持仓N天强制平仓；0=不启用
	TakeProfit  float64 `json:"takeProfit"`  // 止盈比例（0.10=10%）；0=不启用
	StopLoss    float64 `json:"stopLoss"`    // 止损比例；0=不启用
	// 脚本文件名（报告目录标识，取脚本名去扩展名）
	ScriptName string `json:"scriptName"`
}

// Validate 配置合法性检查。
func (c RunConfig) Validate() error {
	if err := c.validateYears(); err != nil {
		return err
	}
	if err := c.validateSample(); err != nil {
		return err
	}
	if c.HoldingDays <= 0 && c.TakeProfit <= 0 && c.StopLoss <= 0 {
		return fmt.Errorf("卖出规则至少启用一项（持仓天数/止盈/止损）")
	}
	if c.HoldingDays < 0 || c.TakeProfit < 0 || c.StopLoss < 0 {
		return fmt.Errorf("卖出参数不能为负")
	}
	return nil
}

// validateYears 年份范围检查（回测/因子分析共用）。
func (c RunConfig) validateYears() error {
	if c.StartYear <= 0 || c.EndYear <= 0 || c.StartYear > c.EndYear {
		return fmt.Errorf("年份范围无效: %d-%d", c.StartYear, c.EndYear)
	}
	if c.EndYear > time.Now().Year() {
		return fmt.Errorf("结束年份 %d 超过当前年份", c.EndYear)
	}
	return nil
}

// validateSample 样本配置检查（回测/因子分析共用）。
func (c RunConfig) validateSample() error {
	switch c.SampleMode {
	case "all":
	case "random":
		if c.SampleSize <= 0 {
			return fmt.Errorf("随机样本数无效: %d", c.SampleSize)
		}
	case "codes":
		if len(c.SampleCodes) == 0 {
			return fmt.Errorf("指定代码样本为空")
		}
	default:
		return fmt.Errorf("样本模式无效: %s", c.SampleMode)
	}
	return nil
}

// years 生成回测年份列表。
func (c RunConfig) years() []int {
	ys := make([]int, 0, c.EndYear-c.StartYear+1)
	for y := c.StartYear; y <= c.EndYear; y++ {
		ys = append(ys, y)
	}
	return ys
}

// seller 按配置构造卖出规则。
func (c RunConfig) seller() core.Seller {
	sellers := []core.Seller(nil)
	if c.HoldingDays > 0 {
		sellers = append(sellers, ss.A持仓N天{Days: c.HoldingDays})
	}
	if c.TakeProfit > 0 || c.StopLoss > 0 {
		sellers = append(sellers, ss.A止盈止损{TakeProfit: c.TakeProfit, StopLoss: c.StopLoss})
	}
	if len(sellers) == 1 {
		return sellers[0]
	}
	return ss.Or(sellers)
}

// 报告来源：script=高级脚本模式，simple=声明式简单模式。
const (
	sourceScript = "script"
	sourceSimple = "simple"
)

// VariantReport 单变体结果（report.json 与 API 直出共用结构）。
type VariantReport struct {
	Name   string         `json:"name"`
	Stats  TradeStatsJSON `json:"stats"`
	Trades []TradeJSON    `json:"trades"`
}

// TradeStatsJSON core.TradeStats 的可序列化形态（camelCase tag）。
// ProfitFactor 用指针：无亏损且有盈利时为 +Inf，JSON 不支持 Inf，输出 null；
// 旧报告中的大写键（Total/WinRate/...）依赖 encoding/json 大小写不敏感
// 匹配仍可反序列化读取。
type TradeStatsJSON struct {
	Total        int      `json:"total"`
	Win          int      `json:"win"`
	Loss         int      `json:"loss"`
	WinRate      float64  `json:"winRate"`
	WinSum       float64  `json:"winSum"`
	LossSum      float64  `json:"lossSum"`
	ProfitFactor *float64 `json:"profitFactor"`
	AvgProfit    float64  `json:"avgProfit"`
	MaxProfit    float64  `json:"maxProfit"`
	MaxLoss      float64  `json:"maxLoss"`
}

// newTradeStatsJSON core.TradeStats → TradeStatsJSON。
func newTradeStatsJSON(s core.TradeStats) TradeStatsJSON {
	out := TradeStatsJSON{
		Total:     s.Total,
		Win:       s.Win,
		Loss:      s.Loss,
		WinRate:   s.WinRate,
		WinSum:    s.WinSum,
		LossSum:   s.LossSum,
		AvgProfit: s.AvgProfit,
		MaxProfit: s.MaxProfit,
		MaxLoss:   s.MaxLoss,
	}
	if !math.IsInf(s.ProfitFactor, 1) && !math.IsNaN(s.ProfitFactor) {
		pf := s.ProfitFactor
		out.ProfitFactor = &pf
	}
	return out
}

// TradeJSON 交易明细的可序列化形态（time.Time 不直接 JSON）。
type TradeJSON struct {
	Code        string  `json:"code"`
	BuyTime     string  `json:"buyTime"`
	BuyPrice    float64 `json:"buyPrice"`
	SellTime    string  `json:"sellTime"`
	SellPrice   float64 `json:"sellPrice"`
	Quantity    int     `json:"quantity"`
	Profit      float64 `json:"profit"` // 盈亏额（元）
	Rate        float64 `json:"rate"`   // 收益率（%）
	HoldingDays int     `json:"holdingDays"`
	Virtual     bool    `json:"virtual"`
}

// Report 完整报告（落盘 + API）。
// Source 区分高级脚本（script）与声明式简单（simple）模式；
// 简单模式额外携带原始 StrategySpec 与后端生成的 Comparison 摘要。
type Report struct {
	Config       RunConfig            `json:"config"`
	StartedAt    string               `json:"startedAt"`
	FinishedAt   string               `json:"finishedAt"`
	Variants     []VariantReport      `json:"variants"`
	Coverage     researchrun.Coverage `json:"coverage"`
	Source       string               `json:"source"`
	StrategySpec *StrategySpec        `json:"strategySpec,omitempty"`
	Comparison   *ComparisonSummary   `json:"comparison,omitempty"`
}

// ComparisonSummary 简单模式对比摘要（实施文档 §4.4）：
// 基准/组合增强对照 + 逐个单条件摘要，全部由后端在保存报告前生成，
// 页面不按数组位置临时计算。
type ComparisonSummary struct {
	BaselineVariant string                 `json:"baselineVariant"`
	CombinedVariant string                 `json:"combinedVariant"`
	BaselineTrades  int                    `json:"baselineTrades"`
	CombinedTrades  int                    `json:"combinedTrades"`
	RetentionRate   *float64               `json:"retentionRate"` // 基准交易为 0 时为 null
	FactorVariants  []FactorVariantSummary `json:"factorVariants"`
}

// FactorVariantSummary 单条件变体摘要（与 StrategySpec.FactorFilters 顺序对应）。
type FactorVariantSummary struct {
	Index         int      `json:"index"`
	Kind          string   `json:"kind"`
	Days          int      `json:"days"`
	Variant       string   `json:"variant"`
	Trades        int      `json:"trades"`
	RetentionRate *float64 `json:"retentionRate"` // 基准交易为 0 时为 null
}

// buildComparison 由变体结果生成简单模式对比摘要。
// 变体数与 Variants() 合同不符时返回 nil，防止高级多变体脚本被误当作
// 多条件对照：N≥2 时为 N+2（N 个条件 + 基准 + 组合增强）；N=1 时
// 组合增强与单条件等价未重复生成，为 N+1=2，组合字段指向单条件变体。
func buildComparison(spec StrategySpec, variants []VariantReport) *ComparisonSummary {
	want := len(spec.FactorFilters) + 2
	if len(spec.FactorFilters) == 1 {
		want = 2
	}
	if len(variants) != want {
		return nil
	}
	base, combined := variants[0], variants[len(variants)-1]
	cmp := &ComparisonSummary{
		BaselineVariant: base.Name,
		CombinedVariant: combined.Name,
		BaselineTrades:  base.Stats.Total,
		CombinedTrades:  combined.Stats.Total,
		FactorVariants:  make([]FactorVariantSummary, 0, len(spec.FactorFilters)),
	}
	if base.Stats.Total > 0 {
		r := float64(combined.Stats.Total) / float64(base.Stats.Total)
		cmp.RetentionRate = &r
	}
	for i := range spec.FactorFilters {
		vr := variants[i+1]
		fs := FactorVariantSummary{
			Index:   i + 1,
			Kind:    spec.FactorFilters[i].Kind,
			Days:    spec.FactorFilters[i].Days,
			Variant: vr.Name,
			Trades:  vr.Stats.Total,
		}
		if base.Stats.Total > 0 {
			r := float64(vr.Stats.Total) / float64(base.Stats.Total)
			fs.RetentionRate = &r
		}
		cmp.FactorVariants = append(cmp.FactorVariants, fs)
	}
	return cmp
}

// Runner 单任务回测执行器。
type Runner struct {
	mu      sync.Mutex // 互斥：同时只允许 1 个任务
	running atomic.Bool
	stopCh  chan struct{} // 关闭即请求停止（只允许运行方 close 一次）

	// 进度上报（atomic，无锁读）
	totalCodes  atomic.Int64
	doneCodes   atomic.Int64
	currentCode atomic.Pointer[string]

	state        atomic.Pointer[string] // idle/running/error/done
	errMsg       atomic.Pointer[string]
	lastRun      atomic.Pointer[RunConfig]
	lastReport   atomic.Pointer[Report]
	runID        atomic.Pointer[string] // 本次运行目录名
	task         atomic.Pointer[string] // 当前任务类型 backtest/analysis
	lastAnalysis atomic.Pointer[AnalysisReport]

	// store 不可变分析历史存储（生产默认 output/factor；测试注入临时根）。
	store      *AnalysisStore
	factorData researchdata.View
	universe   researchdata.Universe // 历史成员只读接口；nil 时回退 current_static
}

// NewRunner 创建执行器。
func NewRunner() *Runner {
	return NewRunnerWithDeps(common.ResearchData, common.DefaultUniverse)
}

// NewRunnerWithData 创建带 PIT 数据视图的执行器。nil 保持现有纯价量因子行为；
// 财务、基本面、公告等上下文因子由调用方显式注入视图。股票池回退 current_static。
func NewRunnerWithData(data researchdata.View) *Runner {
	return NewRunnerWithDeps(data, nil)
}

// NewRunnerWithDeps 注入数据视图与股票池。universe 为 nil 时回退 current_static
// 静态池——它存在生存者偏差、PIT 恒为 unverified，证据等级上限 exploratory。
func NewRunnerWithDeps(data researchdata.View, universe researchdata.Universe) *Runner {
	if universe == nil {
		universe = researchdata.NewStaticUniverse(researchdata.StaticUniverseConfig{
			ID:     "current_static",
			Source: "tdx_local",
		})
	}
	r := &Runner{
		store:      NewAnalysisStore(filepath.Join("output", "factor")),
		factorData: data,
		universe:   universe,
	}
	idle := "idle"
	r.state.Store(&idle)
	return r
}

// dataProvenance 从当前 Runner 依赖生成数据来源快照。价格版本与复权口径当前
// 不可得，显式写 unknown，不允许前端填写后伪装系统已验证；TDX 本地库只有当前
// 快照、无发布日期与修订历史，PITState 恒为 unverified（设计 6.2）。
func (r *Runner) dataProvenance() DataProvenance {
	view := "none"
	if r.factorData != nil {
		view = "injected"
	}
	return DataProvenance{
		PriceSource:      "tdx_local",
		PriceVersion:     "unknown",
		Adjustment:       adjustmentUnknown,
		ResearchDataView: view,
		PITState:         pitUnverified,
		SnapshotAt:       time.Now().UTC().Format(time.RFC3339),
	}
}

// Start 启动高级模式回测（已在运行则报错）。variants 由调用方经 Yaegi 加载并校验通过。
func (r *Runner) Start(cfg RunConfig, variants []core.Variant) error {
	return r.startBacktest(cfg, variants, nil)
}

// StartStrategy 启动简单模式回测：spec 为声明式配置快照，随报告保存
// 并生成 Comparison 摘要。variants 由 spec.Variants() 构建（N≥2 为
// N+2 顺序，N=1 为 基准+单条件 2 个）。
func (r *Runner) StartStrategy(cfg RunConfig, variants []core.Variant, spec StrategySpec) error {
	if err := spec.Validate(); err != nil {
		return err
	}
	return r.startBacktest(cfg, variants, &spec)
}

// startBacktest 内部启动路径：spec 为 nil 时按高级模式（source=script）处理。
func (r *Runner) startBacktest(cfg RunConfig, variants []core.Variant, spec *StrategySpec) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	if len(variants) == 0 {
		return fmt.Errorf("脚本未返回任何策略变体")
	}
	if !r.mu.TryLock() {
		return fmt.Errorf("已有回测任务在运行")
	}

	stop := make(chan struct{})
	r.stopCh = stop
	r.running.Store(true)
	r.doneCodes.Store(0)
	// task 先于 state 写入：读侧见 state=running 必见当前 task
	task := "backtest"
	r.task.Store(&task)
	state := "running"
	r.state.Store(&state)
	rc := cfg
	r.lastRun.Store(&rc)

	go func() {
		defer r.mu.Unlock()
		defer r.running.Store(false)

		report, err := r.run(cfg, variants, spec, stop)
		if err != nil {
			if err == errStopped {
				st := "idle"
				r.state.Store(&st)
				return
			}
			msg := err.Error()
			r.errMsg.Store(&msg)
			st := "error"
			r.state.Store(&st)
			return
		}
		r.lastReport.Store(report)
		id := report.RunID()
		r.runID.Store(&id)
		st := "done"
		r.state.Store(&st)
	}()
	return nil
}

// errStopped 用户主动停止。
var errStopped = fmt.Errorf("已停止")

// Stop 请求停止当前任务（无任务时为空操作）。
func (r *Runner) Stop() {
	if r.running.Load() {
		select {
		case <-r.stopCh:
		default:
			close(r.stopCh)
		}
	}
}

// Status 进度查询。
func (r *Runner) Status() map[string]any {
	st := *r.state.Load()
	m := map[string]any{
		"state":      st,
		"progress":   0.0,
		"doneCodes":  r.doneCodes.Load(),
		"totalCodes": r.totalCodes.Load(),
		"task":       "backtest",
	}
	if p := r.currentCode.Load(); p != nil {
		m["currentCode"] = *p
	}
	if st == "error" {
		if e := r.errMsg.Load(); e != nil {
			m["error"] = *e
		}
	}
	if total := r.totalCodes.Load(); total > 0 {
		m["progress"] = float64(r.doneCodes.Load()) / float64(total) * 100
	}
	if cfg := r.lastRun.Load(); cfg != nil {
		m["config"] = *cfg
	}
	if p := r.task.Load(); p != nil {
		m["task"] = *p
	}
	return m
}

// LatestReport 最新完成报告（无则 nil）。
func (r *Runner) LatestReport() *Report {
	return r.lastReport.Load()
}

// LatestAnalysis 最近一次完成的因子分析报告（无则 nil）。
func (r *Runner) LatestAnalysis() *AnalysisReport {
	return r.lastAnalysis.Load()
}

// StartAnalysis 启动因子分析（与回测共用 mu 互斥）。
// 分析 ID 在获取互斥锁后由服务端生成（客户端不得注入）；ID 生成失败时
// 释放锁并返回，不启动 goroutine。
func (r *Runner) StartAnalysis(cfg AnalyzeConfig) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	if !r.mu.TryLock() {
		return fmt.Errorf("已有任务在运行")
	}
	id, err := generateAnalysisID()
	if err != nil {
		r.mu.Unlock()
		return fmt.Errorf("生成分析 ID 失败: %w", err)
	}

	stop := make(chan struct{})
	r.stopCh = stop
	r.running.Store(true)
	r.doneCodes.Store(0)
	// task 先于 state 写入：读侧见 state=running 必见当前 task
	task := "analysis"
	r.task.Store(&task)
	state := "running"
	r.state.Store(&state)
	rc := cfg.RunConfig
	r.lastRun.Store(&rc)

	go func() {
		defer r.mu.Unlock()
		defer r.running.Store(false)

		rep, err := r.runAnalysis(id, cfg, stop)
		if err != nil {
			if err == errStopped {
				st := "idle"
				r.state.Store(&st)
				return
			}
			msg := err.Error()
			r.errMsg.Store(&msg)
			st := "error"
			r.state.Store(&st)
			return
		}
		r.lastAnalysis.Store(rep)
		st := "done"
		r.state.Store(&st)
	}()
	return nil
}

// RunID 本次/最近一次运行的目录名。
func (r *Runner) RunID() string {
	if p := r.runID.Load(); p != nil {
		return *p
	}
	return ""
}

// RunID 报告目录名（时间戳_脚本名，永不覆盖；经 TradesExportName 清洗 Windows 非法字符）。
func (rep *Report) RunID() string {
	name := rep.Config.ScriptName
	if name == "" {
		name = "script"
	}
	return core.TradesExportName(rep.FinishedAt[:19] + "_" + name)
}

// run 执行回测主循环（复用 backtest_tail 模式）。
// spec 非 nil 时按简单模式填充 Source/StrategySpec/Comparison。
func (r *Runner) run(cfg RunConfig, variants []core.Variant, spec *StrategySpec, stop chan struct{}) (*Report, error) {
	started := time.Now()

	codes, err := r.resolveCodes(cfg, stop)
	if err != nil {
		return nil, err
	}
	r.totalCodes.Store(int64(len(codes)))

	years := cfg.years()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		select {
		case <-stop:
			cancel()
		case <-ctx.Done():
		}
	}()

	// TopN 横截面快照：回测前统一填充（无快照时 A因子TopN 恒 false）。
	// 无请求零开销跳过，普通策略路径不受影响；快照仅本进程内存，
	// 任务结束清理（无论填充成败，防部分写入残留）；填充中 ctx 取消
	// （用户停止）→ errStopped。
	if reqs := collectTopN(variants); len(reqs) > 0 {
		defer core.ClearCrossSection()
		if err := fillCrossSection(ctx, codes, years, reqs); err != nil {
			if errors.Is(err, context.Canceled) {
				return nil, errStopped
			}
			return nil, err
		}
		r.doneCodes.Store(0) // 填充进度不计入回测进度，从头计
	}

	seller := cfg.seller()
	cost, pos, _, _, _ := common.LoadBacktestConfig()
	runVariants := make([]researchrun.Variant, len(variants))
	for i, variant := range variants {
		runVariants[i] = researchrun.Variant{
			Name: variant.Name, Buyer: sb.Strategy(variant.Name, variant.Buyer),
		}
	}

	run, err := researchrun.Run(ctx, researchrun.Config{
		Codes:         codes,
		Years:         years,
		Variants:      runVariants,
		DefaultSeller: seller,
		Cost:          cost,
		Position:      pos,
		Workers:       common.DefaultGoroutines * 2,
		DataMode:      researchrun.Intraday,
		GetDayKlines:  common.Pull.DayKlines,
		GetMinKlines:  common.Pull.MinKlines,
		OnCodeDone: func(progress researchrun.Progress) {
			r.doneCodes.Store(int64(progress.Done))
			current := progress.Code
			r.currentCode.Store(&current)
		},
	})
	if errors.Is(err, context.Canceled) {
		return nil, errStopped
	}
	if err != nil {
		return nil, err
	}
	researchrun.LogCoverage(run.Coverage)

	// 汇总报告（变体顺序保持脚本返回顺序，页面按需排序）
	report := &Report{
		Config:     cfg,
		StartedAt:  started.Format(time.RFC3339),
		FinishedAt: time.Now().Format(time.RFC3339),
		Variants:   make([]VariantReport, 0, len(variants)),
		Coverage:   run.Coverage,
		Source:     sourceScript,
	}
	for _, result := range run.Results {
		vr := VariantReport{Name: result.Variant.Name, Stats: newTradeStatsJSON(core.Stats(result.Trades))}
		vr.Trades = toTradeJSON(result.Trades)
		report.Variants = append(report.Variants, vr)
	}
	if spec != nil {
		sp := *spec
		report.Source = sourceSimple
		report.StrategySpec = &sp
		report.Comparison = buildComparison(sp, report.Variants)
	}

	if err := saveReport(report); err != nil {
		return nil, fmt.Errorf("报告落盘失败: %w", err)
	}
	return report, nil
}
func (r *Runner) resolveCodes(cfg RunConfig, stop chan struct{}) ([]string, error) {
	all := common.GetNoPriceLimitCodes()
	switch cfg.SampleMode {
	case "all":
		return all, nil
	case "codes":
		return cfg.SampleCodes, nil
	case "random":
		// 洗牌取前 N
		shuffled := make([]string, len(all))
		copy(shuffled, all)
		rand.Shuffle(len(shuffled), func(i, j int) {
			shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
		})
		n := cfg.SampleSize
		if n > len(shuffled) {
			n = len(shuffled)
		}
		return shuffled[:n], nil
	}
	return nil, fmt.Errorf("样本模式无效: %s", cfg.SampleMode)
}

// toTradeJSON 交易明细转可序列化结构（按买入时间排序）。
func toTradeJSON(trades []core.Trade) []TradeJSON {
	sorted := make([]core.Trade, len(trades))
	copy(sorted, trades)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].BuyTime.Before(sorted[j].BuyTime) })
	out := make([]TradeJSON, 0, len(sorted))
	for _, t := range sorted {
		out = append(out, TradeJSON{
			Code:        t.Code,
			BuyTime:     t.BuyTime.Format(time.DateTime),
			BuyPrice:    t.BuyPrice.Float64(),
			SellTime:    t.SellTime.Format(time.DateTime),
			SellPrice:   t.SellPrice.Float64(),
			Quantity:    t.Quantity,
			Profit:      t.ProfitAmount(),
			Rate:        t.Profit(),
			HoldingDays: t.HoldingDays(),
			Virtual:     t.Virtual,
		})
	}
	return out
}

// saveReport 落盘报告（AGENTS.md 6.1）：report.json（原子写）+ 每变体 CSV + 汇总 HTML。
func saveReport(rep *Report) error {
	dir := filepath.Join("output", "trades", rep.RunID())
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	// report.json 临时文件 + rename 防半写
	buf, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(dir, ".report.json.tmp")
	if err := os.WriteFile(tmp, buf, 0644); err != nil {
		return err
	}
	if err := os.Rename(tmp, filepath.Join(dir, "report.json")); err != nil {
		return err
	}

	// 每变体 CSV + 全部变体汇总 HTML（core.ExportTradesCSV/HTML 按 AGENTS.md 6.1）
	getDayKlines := common.Pull.DayKlines
	var allTrades []core.Trade
	for _, vr := range rep.Variants {
		trades := fromTradeJSON(vr.Trades)
		_ = core.ExportTradesCSV(rep.Config.ScriptName, rep.RunID()+"_"+vr.Name, trades)
		allTrades = append(allTrades, trades...)
	}
	_ = core.ExportTradesHTML(rep.Config.ScriptName, rep.RunID()+"_summary", allTrades, getDayKlines)
	return nil
}

// fromTradeJSON 由报告 JSON 还原 core.Trade（供 CSV/HTML 导出复用口径）。
func fromTradeJSON(ts []TradeJSON) []core.Trade {
	out := make([]core.Trade, 0, len(ts))
	for _, t := range ts {
		buyTime, _ := time.Parse(time.DateTime, t.BuyTime)
		sellTime, _ := time.Parse(time.DateTime, t.SellTime)
		buyPrice := protocol.Yuan(t.BuyPrice)
		sellPrice := protocol.Yuan(t.SellPrice)
		out = append(out, core.Trade{
			Code:      t.Code,
			BuyTime:   buyTime,
			SellTime:  sellTime,
			BuyPrice:  buyPrice,
			SellPrice: sellPrice,
			// 成交价/成本按原价近似还原（报告明细展示用，Stats 以 Profit() 为准）
			BuyExecPrice:  buyPrice,
			SellExecPrice: sellPrice,
			BuyCost:       float64(t.Quantity) * t.BuyPrice,
			SellIncome:    float64(t.Quantity) * t.SellPrice,
			Quantity:      t.Quantity,
			Virtual:       t.Virtual,
		})
	}
	return out
}
