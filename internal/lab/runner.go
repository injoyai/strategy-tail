package lab

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	if c.StartYear <= 0 || c.EndYear <= 0 || c.StartYear > c.EndYear {
		return fmt.Errorf("年份范围无效: %d-%d", c.StartYear, c.EndYear)
	}
	if c.EndYear > time.Now().Year() {
		return fmt.Errorf("结束年份 %d 超过当前年份", c.EndYear)
	}
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
	if c.HoldingDays <= 0 && c.TakeProfit <= 0 && c.StopLoss <= 0 {
		return fmt.Errorf("卖出规则至少启用一项（持仓天数/止盈/止损）")
	}
	if c.HoldingDays < 0 || c.TakeProfit < 0 || c.StopLoss < 0 {
		return fmt.Errorf("卖出参数不能为负")
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

// VariantReport 单变体结果（report.json 与 API 直出共用结构）。
type VariantReport struct {
	Name   string          `json:"name"`
	Stats  core.TradeStats `json:"stats"`
	Trades []TradeJSON     `json:"trades"`
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
type Report struct {
	Config     RunConfig            `json:"config"`
	StartedAt  string               `json:"startedAt"`
	FinishedAt string               `json:"finishedAt"`
	Variants   []VariantReport      `json:"variants"`
	Coverage   researchrun.Coverage `json:"coverage"`
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

	state      atomic.Pointer[string] // idle/running/error/done
	errMsg     atomic.Pointer[string]
	lastRun    atomic.Pointer[RunConfig]
	lastReport atomic.Pointer[Report]
	runID      atomic.Pointer[string] // 本次运行目录名
}

// NewRunner 创建执行器。
func NewRunner() *Runner {
	r := &Runner{}
	idle := "idle"
	r.state.Store(&idle)
	return r
}

// Start 启动回测（已在运行则报错）。variants 由调用方经 Yaegi 加载并校验通过。
func (r *Runner) Start(cfg RunConfig, variants []core.Variant) error {
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
	state := "running"
	r.state.Store(&state)
	rc := cfg
	r.lastRun.Store(&rc)

	go func() {
		defer r.mu.Unlock()
		defer r.running.Store(false)

		report, err := r.run(cfg, variants, stop)
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
	return m
}

// LatestReport 最新完成报告（无则 nil）。
func (r *Runner) LatestReport() *Report {
	return r.lastReport.Load()
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
func (r *Runner) run(cfg RunConfig, variants []core.Variant, stop chan struct{}) (*Report, error) {
	started := time.Now()

	codes, err := r.resolveCodes(cfg, stop)
	if err != nil {
		return nil, err
	}
	r.totalCodes.Store(int64(len(codes)))

	years := cfg.years()
	seller := cfg.seller()
	cost, pos, _, _, _ := common.LoadBacktestConfig()
	runVariants := make([]researchrun.Variant, len(variants))
	for i, variant := range variants {
		runVariants[i] = researchrun.Variant{
			Name: variant.Name, Buyer: sb.Strategy(variant.Name, variant.Buyer),
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		select {
		case <-stop:
			cancel()
		case <-ctx.Done():
		}
	}()

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
	}
	for _, result := range run.Results {
		vr := VariantReport{Name: result.Variant.Name, Stats: core.Stats(result.Trades)}
		vr.Trades = toTradeJSON(result.Trades)
		report.Variants = append(report.Variants, vr)
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
