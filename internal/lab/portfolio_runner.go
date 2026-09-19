package lab

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	common "github.com/injoyai/strategy-tail"
	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/internal/portfolioresearch"
	"github.com/injoyai/strategy-tail/internal/researchrun"
	f "github.com/injoyai/strategy-tail/strategies/factor"
)

// portfolio_runner.go v2 Task 9 组合任务执行器（设计 §9/§10/§11/§12.2/§14）。
//
// 职责：把 Task 1-6 的纯函数组装为组合实验执行链（Transform → Combine →
// BuildTarget → ExecuteDay → ComputeMetrics → ComputeAttribution），验证任务
// 复用 Task 8 的 Evaluate；复用 researchrun 数据边界逐年逐票加载（不在
// handler 重写加载循环）；支持取消检查与阶段进度；实验产物写入
// output/portfolio/<id>/ 五类文件并经 PortfolioExperimentStore 发布终态。
//
// 长任务语义：Start 同步启动 goroutine 并立即返回（accepted），终态由持久
// Store 恢复——重启后 /api/status 与详情读 Store，不依赖内存。

// 组合任务类型（/api/status task 字段）。
const (
	taskPortfolioExperiment = "portfolio"
	taskPortfolioValidation = "portfolio_validation"
)

// 组合任务阶段（/api/status phase 字段；验证任务在窗口内复用各阶段标记）。
const (
	portfolioPhaseQueued      = "queued"
	portfolioPhaseLoadingData = "loading_data"
	portfolioPhaseTransform   = "transform"
	portfolioPhaseCombine     = "combine"
	portfolioPhaseTarget      = "target"
	portfolioPhaseExecute     = "execute"
	portfolioPhaseMetrics     = "metrics"
	portfolioPhaseAttribution = "attribution"
	portfolioPhaseFinalize    = "finalize"
)

// portfolioNotional 组合名义金额（权重 → 股数换算基准，元）。
const portfolioNotional = 1_000_000.0

// portfolioValidationLoadYears 验证任务加载的最近年数（含当前年）。
const portfolioValidationLoadYears = 5

// ConfigurePortfolio 注入组合研究依赖（模型/实验/验证存储）。Server 在构造时
// 调用；nil 时组合任务 fail closed。
func (r *Runner) ConfigurePortfolio(modelStore *FactorModelStore, experimentStore *PortfolioExperimentStore, validationStore *PortfolioValidationStore) {
	r.modelStore = modelStore
	r.experimentStore = experimentStore
	r.validationStore = validationStore
}

// setPortfolioPhase 更新组合任务阶段（/api/status phase 字段）。
func (r *Runner) setPortfolioPhase(phase string) {
	r.phase.Store(&phase)
}

// setPortfolioRun 记录当前组合任务 ID（/api/status runId 字段）。
func (r *Runner) setPortfolioRun(id string) {
	r.portfolioRun.Store(&id)
}

// StartPortfolioExperiment 启动组合实验任务（长任务语义：accepted 立即返回）。
// 仅 queued 实验可启动；任务互斥（与回测/分析共用 mu）；终态由 Store 持久化。
func (r *Runner) StartPortfolioExperiment(id string) error {
	if !validExperimentID(id) {
		return fmt.Errorf("非法实验 ID: %q", id)
	}
	if r.experimentStore == nil {
		return fmt.Errorf("实验存储未注入")
	}
	exp, err := r.experimentStore.Get(id)
	if err != nil {
		return err
	}
	if exp.Status != portfolioresearch.RunStateQueued {
		return fmt.Errorf("实验状态 %q 不可启动（仅 queued 可启动）", exp.Status)
	}
	if !r.mu.TryLock() {
		return fmt.Errorf("已有任务在运行")
	}
	r.startPortfolioGoroutine(id, taskPortfolioExperiment, func(stop chan struct{}) error {
		return r.runPortfolioExperiment(id, stop)
	})
	return nil
}

// StartPortfolioValidation 启动组合验证任务（长任务语义同上）。验证无独立
// "running" 状态（created → completed 由 report.json 完成标记决定），重复
// 运行已完成验证被拒绝。
func (r *Runner) StartPortfolioValidation(id string) error {
	if !validPortfolioValidationID(id) {
		return fmt.Errorf("非法验证 ID: %q", id)
	}
	if r.validationStore == nil {
		return fmt.Errorf("验证存储未注入")
	}
	view, err := r.validationStore.Get(id)
	if err != nil {
		return err
	}
	if view.State == PortfolioValidationStateCompleted {
		return fmt.Errorf("验证已完成，不可重复运行")
	}
	if !r.mu.TryLock() {
		return fmt.Errorf("已有任务在运行")
	}
	r.startPortfolioGoroutine(id, taskPortfolioValidation, func(stop chan struct{}) error {
		return r.runPortfolioValidation(id, stop)
	})
	return nil
}

// startPortfolioGoroutine 组合任务公共启动骨架：写任务类型（先于 state）、
// 启动 goroutine，终态统一收敛（errStopped → 全局 done，精确终态在 Store）。
func (r *Runner) startPortfolioGoroutine(id, task string, run func(stop chan struct{}) error) {
	stop := make(chan struct{})
	r.stopCh = stop
	r.running.Store(true)
	r.doneCodes.Store(0)
	r.task.Store(&task)
	state := "running"
	r.state.Store(&state)

	go func() {
		defer r.mu.Unlock()
		defer r.running.Store(false)
		err := run(stop)
		if err != nil {
			if errors.Is(err, errStopped) {
				// 取消：终态（cancelled）已由 Store 持久化；全局状态置 done，
				// 精确终态由 /api/portfolio-runs/{id} 从 Store 恢复。
				st := "done"
				r.state.Store(&st)
				return
			}
			msg := err.Error()
			r.errMsg.Store(&msg)
			st := "error"
			r.state.Store(&st)
			return
		}
		st := "done"
		r.state.Store(&st)
	}()
}

// ---- 实验执行 ----

// runPortfolioExperiment 组合实验执行链（阶段进度 + 取消检查 + 终态发布）。
func (r *Runner) runPortfolioExperiment(id string, stop chan struct{}) error {
	exp, err := r.experimentStore.Get(id)
	if err != nil {
		return err
	}
	if r.modelStore == nil {
		return fmt.Errorf("模型存储未注入")
	}
	if err := r.experimentStore.Start(id); err != nil {
		return err
	}
	r.setPortfolioRun(id)
	r.setPortfolioPhase(portfolioPhaseLoadingData)

	m, err := r.modelStore.Get(exp.ModelID, exp.ModelRevision)
	if err != nil {
		return r.markExperimentFailed(id, "读取冻结模型失败: "+err.Error())
	}
	if m.ModelHash != exp.ModelHash {
		return r.markExperimentFailed(id, "模型 hash 与实验引用不一致（fail closed）")
	}

	start, err := time.Parse(time.DateOnly, exp.StudyRange.Start)
	if err != nil {
		return r.markExperimentFailed(id, "研究区间起始日非法: "+err.Error())
	}
	end, err := time.Parse(time.DateOnly, exp.StudyRange.End)
	if err != nil {
		return r.markExperimentFailed(id, "研究区间结束日非法: "+err.Error())
	}

	codes, err := r.resolvePortfolioCodes(m.DataSnapshot.UniverseMode, start, end)
	if err != nil {
		return r.markExperimentFailed(id, "解析股票池失败: "+err.Error())
	}
	if len(codes) == 0 {
		return r.markExperimentFailed(id, "股票池为空")
	}
	r.totalCodes.Store(int64(len(codes)))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		select {
		case <-stop:
			cancel()
		case <-ctx.Done():
		}
	}()

	years := yearsBetween(start, end)
	data, _, err := r.loadPortfolioData(ctx, m, codes, years, exp.StudyRange.Start, exp.StudyRange.End, stop)
	if err != nil {
		if errors.Is(err, errStopped) {
			return r.cancelExperiment(id, "运行被停止")
		}
		return r.markExperimentFailed(id, "数据加载失败: "+err.Error())
	}
	if len(data.dates) == 0 {
		return r.markExperimentInsufficient(id, "研究区间内无有效交易日")
	}
	_ = r.experimentStore.UpdateProgress(id, 30, "数据加载完成")
	r.totalCodes.Store(int64(len(data.dates)))
	r.doneCodes.Store(0)

	if stopped(stop) {
		return r.cancelExperiment(id, "运行被停止")
	}
	r.setPortfolioPhase(portfolioPhaseTransform)
	transformed, err := r.transformFactors(m, data, stop)
	if err != nil {
		return r.markExperimentFailed(id, "截面变换失败: "+err.Error())
	}
	_ = r.experimentStore.UpdateProgress(id, 45, "截面变换完成")

	if stopped(stop) {
		return r.cancelExperiment(id, "运行被停止")
	}
	r.setPortfolioPhase(portfolioPhaseCombine)
	factors := buildFactorSeries(m, transformed)
	trainDates := filterDates(data.dates, exp.TrainRange)
	weights, cashOnly, err := resolveCombineWeights(m.Combination, factors, trainDates, data.close)
	if err != nil {
		return r.markExperimentFailed(id, "合成权重解析失败: "+err.Error())
	}
	scores, err := combinePortfolio(factors, m.TransformPipeline.Missing, weights, cashOnly)
	if err != nil {
		return r.markExperimentFailed(id, "多因子合成失败: "+err.Error())
	}
	_ = r.experimentStore.UpdateProgress(id, 55, "多因子合成完成")

	r.setPortfolioPhase(portfolioPhaseTarget)
	r.doneCodes.Store(0)
	ledgers, usedTargets, err := runPortfolioDays(m, scores, data, data.dates, portfolioNotional, stop,
		func(done, total int) { r.doneCodes.Store(int64(done)) })
	if err != nil {
		if errors.Is(err, errStopped) {
			return r.cancelExperiment(id, "运行被停止")
		}
		return r.markExperimentFailed(id, "组合执行失败: "+err.Error())
	}
	_ = r.experimentStore.UpdateProgress(id, 85, "组合执行完成")

	r.setPortfolioPhase(portfolioPhaseMetrics)
	series := portfolioresearch.FromLedgers(ledgers)
	metrics, err := portfolioresearch.ComputeMetrics(series, nil, 0)
	if err != nil {
		return r.markExperimentFailed(id, "组合指标计算失败: "+err.Error())
	}
	_ = r.experimentStore.UpdateProgress(id, 92, "组合指标完成")

	r.setPortfolioPhase(portfolioPhaseAttribution)
	attribution, err := buildAttribution(ledgers, usedTargets, data, series, portfolioNotional)
	if err != nil {
		return r.markExperimentFailed(id, "组合归因失败: "+err.Error())
	}
	_ = r.experimentStore.UpdateProgress(id, 97, "组合归因完成")

	r.setPortfolioPhase(portfolioPhaseFinalize)
	rep := assembleExperimentReport(exp, m, metrics, attribution, series, data)
	if err := r.writeExperimentArtifacts(id, rep, ledgers); err != nil {
		return r.markExperimentFailed(id, "产物写入失败: "+err.Error())
	}
	if err := r.experimentStore.MarkCompleted(id); err != nil {
		return fmt.Errorf("标记完成失败: %w", err)
	}
	r.doneCodes.Store(int64(len(data.dates)))
	return nil
}

// cancelExperiment 标记取消并返回 errStopped（调用方据此置全局状态 done）。
func (r *Runner) cancelExperiment(id, msg string) error {
	_ = r.experimentStore.MarkCancelled(id, msg)
	return errStopped
}

// markExperimentFailed 标记失败并返回错误（全局状态置 error）。
func (r *Runner) markExperimentFailed(id, msg string) error {
	_ = r.experimentStore.MarkFailed(id, msg)
	return fmt.Errorf("%s", msg)
}

// markExperimentInsufficient 标记无有效样本（运行成功但样本不足，返回 nil
// 使全局状态置 done；精确终态由 Store 表达）。
func (r *Runner) markExperimentInsufficient(id, msg string) error {
	_ = r.experimentStore.MarkInsufficient(id, msg)
	return nil
}

// ---- 验证执行 ----

// runPortfolioValidation 组合验证执行：全局变换一次，逐非重叠窗口
// （BuildValidationWindows）冻结权重 → 组合执行 → 单窗指标 → SaveWindow，
// 最后 Complete 用冻结 spec 求值（Evaluate）发布最终结论。
func (r *Runner) runPortfolioValidation(id string, stop chan struct{}) error {
	if r.modelStore == nil {
		return fmt.Errorf("模型存储未注入")
	}
	view, err := r.validationStore.Get(id)
	if err != nil {
		return err
	}
	if view.State == PortfolioValidationStateCompleted {
		return fmt.Errorf("验证已完成，不可重复运行")
	}
	spec := view.Record.Spec
	m, err := r.modelStore.Get(spec.ModelRef.ModelID, spec.ModelRef.Revision)
	if err != nil {
		return err
	}
	if m.ModelHash != spec.ModelRef.Hash {
		return fmt.Errorf("模型 hash 与验证引用不一致（fail closed）")
	}
	r.setPortfolioRun(id)
	r.setPortfolioPhase(portfolioPhaseLoadingData)

	now := time.Now()
	loadStart := time.Date(now.Year()-portfolioValidationLoadYears+1, 1, 1, 0, 0, 0, 0, time.Local)
	loadEnd := time.Date(now.Year(), 12, 31, 23, 0, 0, 0, time.Local)
	codes, err := r.resolvePortfolioCodes(m.DataSnapshot.UniverseMode, loadStart, loadEnd)
	if err != nil {
		return err
	}
	if len(codes) == 0 {
		return fmt.Errorf("股票池为空")
	}
	r.totalCodes.Store(int64(len(codes)))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		select {
		case <-stop:
			cancel()
		case <-ctx.Done():
		}
	}()

	years := yearsBetween(loadStart, loadEnd)
	dateStart, dateEnd := loadStart.Format(time.DateOnly), loadEnd.Format(time.DateOnly)
	data, _, err := r.loadPortfolioData(ctx, m, codes, years, dateStart, dateEnd, stop)
	if err != nil {
		if errors.Is(err, errStopped) {
			return errStopped
		}
		return err
	}
	if len(data.dates) == 0 {
		// 无任何交易日 → 无法形成窗口，保存一个 insufficient 窗口使结论可生成。
		return r.saveValidationInsufficient(id, m.EvidenceClass, "无有效交易日")
	}

	if stopped(stop) {
		return errStopped
	}
	r.setPortfolioPhase(portfolioPhaseTransform)
	transformed, err := r.transformFactors(m, data, stop)
	if err != nil {
		return err
	}
	factors := buildFactorSeries(m, transformed)

	plans, err := portfolioresearch.BuildValidationWindows(data.dates, spec.WindowRule)
	if err != nil {
		return err
	}
	if len(plans) == 0 {
		return r.saveValidationInsufficient(id, m.EvidenceClass, "交易日不足以构成任何非重叠窗口")
	}
	r.totalCodes.Store(int64(len(plans)))
	r.doneCodes.Store(0)

	for i, plan := range plans {
		if stopped(stop) {
			return errStopped
		}
		r.setPortfolioPhase(portfolioPhaseCombine)
		trainDates, err := portfolioresearch.TrainingDatesForWindow(data.dates, plan)
		if err != nil {
			if serr := r.validationStore.SaveWindow(id, portfolioresearch.ValidationWindowOutcome{
				ValidationID: id, Index: plan.Index,
				TrainStart: plan.TrainStart, TrainEnd: plan.TrainEnd,
				TestStart: plan.TestStart, TestEnd: plan.TestEnd,
				State: portfolioresearch.WindowStateError, Message: "训练区间解析失败: " + err.Error(),
				EvidenceClass: m.EvidenceClass,
			}); serr != nil {
				return serr
			}
			continue
		}
		weights, cashOnly, err := resolveCombineWeights(m.Combination, factors, trainDates, data.close)
		if err != nil {
			return err
		}
		scores, err := combinePortfolio(factors, m.TransformPipeline.Missing, weights, cashOnly)
		if err != nil {
			return err
		}
		testDates := datesBetween(data.dates, plan.TestStart, plan.TestEnd)

		r.setPortfolioPhase(portfolioPhaseExecute)
		ledgers, _, err := runPortfolioDays(m, scores, data, testDates, portfolioNotional, stop, nil)
		if err != nil {
			if errors.Is(err, errStopped) {
				return errStopped
			}
			if serr := r.validationStore.SaveWindow(id, portfolioresearch.ValidationWindowOutcome{
				ValidationID: id, Index: plan.Index,
				TrainStart: plan.TrainStart, TrainEnd: plan.TrainEnd,
				TestStart: plan.TestStart, TestEnd: plan.TestEnd,
				State: portfolioresearch.WindowStateError, Message: "窗口执行失败: " + err.Error(),
				EvidenceClass: m.EvidenceClass,
			}); serr != nil {
				return serr
			}
			r.doneCodes.Store(int64(i + 1))
			continue
		}
		if len(ledgers) < 2 {
			if serr := r.validationStore.SaveWindow(id, portfolioresearch.ValidationWindowOutcome{
				ValidationID: id, Index: plan.Index,
				TrainStart: plan.TrainStart, TrainEnd: plan.TrainEnd,
				TestStart: plan.TestStart, TestEnd: plan.TestEnd,
				State: portfolioresearch.WindowStateInsufficient, Message: "窗口有效交易日不足",
				EvidenceClass: m.EvidenceClass,
			}); serr != nil {
				return serr
			}
			r.doneCodes.Store(int64(i + 1))
			continue
		}

		r.setPortfolioPhase(portfolioPhaseMetrics)
		wm, err := windowMetrics(ledgers)
		if err != nil {
			return err
		}
		outcome := portfolioresearch.ValidationWindowOutcome{
			ValidationID:  id,
			Index:         plan.Index,
			TrainStart:    plan.TrainStart,
			TrainEnd:      plan.TrainEnd,
			TestStart:     plan.TestStart,
			TestEnd:       plan.TestEnd,
			State:         portfolioresearch.WindowStateOK,
			Metrics:       &wm,
			EvidenceClass: m.EvidenceClass,
		}
		outcome.Gates = spec.EvaluateWindowGates(outcome)
		if serr := r.validationStore.SaveWindow(id, outcome); serr != nil {
			return serr
		}
		r.doneCodes.Store(int64(i + 1))
	}

	r.setPortfolioPhase(portfolioPhaseFinalize)
	_, err = r.validationStore.Complete(id)
	if err != nil {
		return fmt.Errorf("验证结论生成失败: %w", err)
	}
	return nil
}

// saveValidationInsufficient 保存一个 insufficient 窗口并 Complete，使结论
// 可生成（Evaluate 对无有效窗口给出 insufficient，不伪装统计失败）。
func (r *Runner) saveValidationInsufficient(id, evidenceClass, msg string) error {
	if err := r.validationStore.SaveWindow(id, portfolioresearch.ValidationWindowOutcome{
		ValidationID: id,
		Index:        1,
		TrainStart:   "2000-01-01", TrainEnd: "2000-01-01",
		TestStart: "2000-01-02", TestEnd: "2000-01-02",
		State:         portfolioresearch.WindowStateInsufficient,
		Message:       msg,
		EvidenceClass: evidenceClass,
	}); err != nil {
		return err
	}
	_, err := r.validationStore.Complete(id)
	return err
}

// ---- 数据加载 ----

// portfolioRunData 组合运行加载的逐日数据（任务局部，无包级可变状态）。
type portfolioRunData struct {
	codes []string
	dates []string // 升序交易日 YYYY-MM-DD
	// values 因子 key → 日期 → 代码 → 因子值（信号日收盘后可见）。
	values map[string]map[string]map[string]float64
	close  map[string]map[string]float64 // 日期 → 代码 → 收盘
	open   map[string]map[string]float64 // 日期 → 代码 → 开盘
	// prevClose 日期 → 代码 → 前一交易日收盘（首日无）。
	prevClose map[string]map[string]float64
}

// factorKey 因子引用的稳定身份 key（候选 ID + 实例参数；审计引用）。
func factorKey(ref portfolioresearch.ValidatedFactorRef) string {
	return fmt.Sprintf("%s/%s/%d", ref.CandidateID, ref.FactorKind, ref.FactorDays)
}

// resolvePortfolioCodes 按模型数据快照的股票池模式解析代码列表。
// historical_membership 取区间成员并集；current_static/codes 取当前列表。
func (r *Runner) resolvePortfolioCodes(mode string, start, end time.Time) ([]string, error) {
	if r.universe == nil {
		return nil, fmt.Errorf("股票池未配置（universe 为 nil）")
	}
	switch mode {
	case "historical_membership", "":
		return r.universe.CodesBetween(start, end)
	default: // current_static / codes
		return r.universe.Codes(end)
	}
}

// loadPortfolioData 复用 researchrun.ForEachCodeYearData 逐年逐票加载：
// 计算各因子值（复用 factor_observation.buildObservations，无前视），
// 收集收盘/开盘，过滤到研究区间，构建升序交易日与前收盘映射。
func (r *Runner) loadPortfolioData(ctx context.Context, m portfolioresearch.FactorModel, codes []string, years []int, startDate, endDate string, stop chan struct{}) (*portfolioRunData, researchrun.YearCoverage, error) {
	type factorCtx struct {
		key string
		fct core.ContextFactor
		ref portfolioresearch.ValidatedFactorRef
	}
	fcs := make([]factorCtx, 0, len(m.ValidatedFactors))
	maxH := 1
	for _, ref := range m.ValidatedFactors {
		fct := f.BuildContext(ref.FactorKind, ref.FactorDays)
		if fct == nil {
			return nil, researchrun.YearCoverage{}, fmt.Errorf("因子类型 %q 未注册", ref.FactorKind)
		}
		fcs = append(fcs, factorCtx{key: factorKey(ref), fct: fct, ref: ref})
		if ref.PrimaryHorizon > maxH {
			maxH = ref.PrimaryHorizon
		}
	}

	data := &portfolioRunData{
		codes:     codes,
		values:    make(map[string]map[string]map[string]float64, len(fcs)),
		close:     map[string]map[string]float64{},
		open:      map[string]map[string]float64{},
		prevClose: map[string]map[string]float64{},
	}
	for _, fc := range fcs {
		data.values[fc.key] = map[string]map[string]float64{}
	}

	var mu sync.Mutex
	coverage, err := researchrun.ForEachCodeYearData(ctx, researchrun.Config{
		Codes:        codes,
		Years:        years,
		Workers:      common.DefaultGoroutines * 2,
		DataMode:     researchrun.DailyClose,
		GetDayKlines: common.Pull.DayKlines,
		ForwardDays:  maxH,
		OnCodeDone: func(progress researchrun.Progress) {
			r.doneCodes.Store(int64(progress.Done))
		},
	}, func(code string, _ int, d researchrun.YearData) {
		localClose := map[string]float64{}
		localOpen := map[string]float64{}
		for _, k := range d.Dks {
			day := k.Time.Format(time.DateOnly)
			if day < startDate || day > endDate {
				continue
			}
			localClose[day] = k.Close.Float64()
			localOpen[day] = k.Open.Float64()
		}
		localVals := make([]map[string]float64, len(fcs))
		for i := range fcs {
			localVals[i] = map[string]float64{}
		}
		for i, fc := range fcs {
			obs := buildObservations(code, d, fc.fct, []int{fc.ref.PrimaryHorizon}, fc.ref.FactorKind, r.factorData)
			for _, o := range obs.Observations {
				day := o.Date.Format(time.DateOnly)
				if day < startDate || day > endDate {
					continue
				}
				localVals[i][day] = o.Value
			}
		}
		mu.Lock()
		defer mu.Unlock()
		for day, v := range localClose {
			if data.close[day] == nil {
				data.close[day] = map[string]float64{}
			}
			data.close[day][code] = v
		}
		for day, v := range localOpen {
			if data.open[day] == nil {
				data.open[day] = map[string]float64{}
			}
			data.open[day][code] = v
		}
		for i := range fcs {
			dm := data.values[fcs[i].key]
			for day, v := range localVals[i] {
				if dm[day] == nil {
					dm[day] = map[string]float64{}
				}
				dm[day][code] = v
			}
		}
	})
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return nil, coverage, errStopped
		}
		return nil, coverage, err
	}
	if stopped(stop) {
		return nil, coverage, errStopped
	}

	set := map[string]bool{}
	for d := range data.close {
		set[d] = true
	}
	var dates []string
	for d := range set {
		dates = append(dates, d)
	}
	sort.Strings(dates)
	data.dates = dates

	for i, d := range dates {
		if i == 0 {
			continue
		}
		data.prevClose[d] = data.close[dates[i-1]]
	}
	return data, coverage, nil
}

// transformFactors 逐因子逐日截面变换（固定顺序由 Transform 保证；中性化
// none 时无需风险暴露提供者）。
func (r *Runner) transformFactors(m portfolioresearch.FactorModel, data *portfolioRunData, stop chan struct{}) (map[string]map[string][]portfolioresearch.TransformedRow, error) {
	pipeline := m.TransformPipeline
	out := make(map[string]map[string][]portfolioresearch.TransformedRow, len(m.ValidatedFactors))
	for _, ref := range m.ValidatedFactors {
		key := factorKey(ref)
		days := map[string][]portfolioresearch.TransformedRow{}
		for _, date := range data.dates {
			if stopped(stop) {
				return nil, errStopped
			}
			vals := data.values[key][date]
			rows := make([]portfolioresearch.CrossSectionRow, 0, len(vals))
			for _, code := range data.codes {
				v, ok := vals[code]
				if !ok || math.IsNaN(v) || math.IsInf(v, 0) {
					continue
				}
				tradable := true
				if r.universe != nil {
					day, _ := time.Parse(time.DateOnly, date)
					okMember, err := r.universe.Contains(code, day)
					if err != nil {
						return nil, fmt.Errorf("股票池查询失败 %s/%s: %w", code, date, err)
					}
					tradable = okMember
				}
				rows = append(rows, portfolioresearch.CrossSectionRow{
					Date: date, Code: code, Value: v, Valid: true, Tradable: tradable,
				})
			}
			if len(rows) == 0 {
				continue
			}
			res, err := portfolioresearch.Transform(pipeline, portfolioresearch.TransformConfig{}, ref.Direction, rows, nil)
			if err != nil {
				return nil, fmt.Errorf("截面变换失败 %s/%s: %w", key, date, err)
			}
			days[date] = res.Rows
		}
		out[key] = days
	}
	return out, nil
}

// buildFactorSeries 从变换结果组装合成输入（方向统一后的 Final，越大越好）。
func buildFactorSeries(m portfolioresearch.FactorModel, transformed map[string]map[string][]portfolioresearch.TransformedRow) []portfolioresearch.FactorSeries {
	factors := make([]portfolioresearch.FactorSeries, 0, len(m.ValidatedFactors))
	for _, ref := range m.ValidatedFactors {
		key := factorKey(ref)
		factors = append(factors, portfolioresearch.FactorSeries{
			Key:       key,
			Direction: ref.Direction,
			Horizon:   ref.PrimaryHorizon,
			Days:      transformed[key],
		})
	}
	return factors
}

// resolveCombineWeights 解析合成权重：等权秩 → nil（Combine 内部等权）；
// 滚动 IC → 在训练日期上训练一个权重快照；训练日期为空或不足时按冻结
// fallback（equal_weight → 等权；cash → 全现金标记）。
func resolveCombineWeights(spec portfolioresearch.CombinationSpec, factors []portfolioresearch.FactorSeries, trainDates []string, prices map[string]map[string]float64) (map[string]float64, bool, error) {
	if spec.Method == portfolioresearch.CombinationEqualWeightRank {
		return nil, false, nil
	}
	keys := make([]string, len(factors))
	for i, f := range factors {
		keys[i] = f.Key
	}
	if len(trainDates) == 0 {
		if spec.RollingIC.Fallback == portfolioresearch.CombinationFallbackCash {
			return nil, true, nil // 全现金：无分数 → 无目标
		}
		return portfolioresearch.EqualWeights(keys), false, nil
	}
	price := make(map[string]map[string]float64, len(trainDates))
	for _, d := range trainDates {
		if pm, ok := prices[d]; ok {
			price[d] = pm
		}
	}
	snap, err := portfolioresearch.TrainRollingIC(*spec.RollingIC, portfolioresearch.TrainingView{Dates: trainDates, Price: price}, factors)
	if err != nil {
		return nil, false, err
	}
	if snap.FallbackReason != "" && spec.RollingIC.Fallback == portfolioresearch.CombinationFallbackCash {
		// 训练数据不足且冻结 fallback=cash → 全现金。
		return nil, true, nil
	}
	return snap.FinalWeights, false, nil
}

// combinePortfolio 多因子合成：cashOnly 时返回空分数（全现金组合）。
func combinePortfolio(factors []portfolioresearch.FactorSeries, missing string, weights map[string]float64, cashOnly bool) (map[string]map[string]float64, error) {
	if cashOnly {
		return map[string]map[string]float64{}, nil
	}
	report, err := portfolioresearch.Combine(factors, missing, weights)
	if err != nil {
		return nil, err
	}
	out := make(map[string]map[string]float64, len(report.Results))
	for _, res := range report.Results {
		if len(res.Scores) > 0 {
			out[res.Date] = res.Scores
		}
	}
	return out, nil
}

// runPortfolioDays 在 dates 上执行组合：目标由前一交易日收盘形成（首日无
// 目标 → 全现金跳过），返回逐日账本与每执行日实际使用的目标（供归因）。
// onDay 每处理一个执行日回调一次（进度上报；nil 跳过）。
func runPortfolioDays(m portfolioresearch.FactorModel, scores map[string]map[string]float64, data *portfolioRunData, dates []string, notional float64, stop chan struct{}, onDay func(done, total int)) ([]portfolioresearch.DailyLedger, map[string]portfolioresearch.TargetPortfolio, error) {
	exec := m.Execution
	policy := m.PortfolioPolicy
	state := portfolioresearch.PortfolioState{Cash: notional}
	ledgers := make([]portfolioresearch.DailyLedger, 0, len(dates))
	usedTargets := make(map[string]portfolioresearch.TargetPortfolio, len(dates))
	targets := make(map[string]portfolioresearch.TargetPortfolio, len(dates))

	for i, date := range dates {
		if stopped(stop) {
			return nil, nil, errStopped
		}
		var target portfolioresearch.TargetPortfolio
		var signalScores map[string]float64
		if i > 0 {
			prev := dates[i-1]
			target = targets[prev]
			signalScores = scores[prev]
		}
		ledger, next, err := portfolioresearch.ExecuteDay(state, portfolioresearch.DayInput{
			Target: target,
			Market: dayMarketFor(data, date),
			Scores: signalScores,
		}, exec)
		if err != nil {
			return nil, nil, fmt.Errorf("执行失败 %s: %w", date, err)
		}
		ledgers = append(ledgers, ledger)
		usedTargets[date] = target
		state = next
		if onDay != nil {
			onDay(i+1, len(dates))
		}

		// 收盘后形成当日目标（供下一交易日执行；末日无需形成）。
		if i == len(dates)-1 {
			continue
		}
		scoresAt := scores[date]
		if len(scoresAt) == 0 {
			continue // 空选日：无目标 → 下一交易日空目标（全现金跳过）
		}
		holdings, cashWeight := weightsFromState(state, data.close[date])
		tgt, err := portfolioresearch.BuildTarget(policy, exec, portfolioresearch.TargetInput{
			Date:       date,
			Scores:     scoresAt,
			Tradable:   tradableFor(data, date),
			Prices:     data.close[date],
			Holdings:   holdings,
			CashWeight: cashWeight,
			Notional:   notional,
		})
		if err != nil {
			return nil, nil, fmt.Errorf("目标构建失败 %s: %w", date, err)
		}
		targets[date] = tgt
	}
	return ledgers, usedTargets, nil
}

// dayMarketFor 构造执行日行情：开盘/收盘/前收盘来自加载数据；可交易性按
// 股票池成员（涨跌停/停牌状态当前未建模，见报告 Limitations）。
func dayMarketFor(data *portfolioRunData, date string) portfolioresearch.DayMarket {
	m := portfolioresearch.DayMarket{Date: date}
	codes := make([]string, 0, len(data.close[date]))
	for c := range data.close[date] {
		codes = append(codes, c)
	}
	sort.Strings(codes)
	m.OpenPrice = make(map[string]float64, len(codes))
	m.ClosePrice = make(map[string]float64, len(codes))
	m.PrevClose = make(map[string]float64, len(codes))
	m.Tradable = make(map[string]bool, len(codes))
	m.LimitUp = make(map[string]bool, len(codes))
	m.LimitDown = make(map[string]bool, len(codes))
	m.Listed = make(map[string]bool, len(codes))
	for _, c := range codes {
		m.OpenPrice[c] = data.open[date][c]
		m.ClosePrice[c] = data.close[date][c]
		m.PrevClose[c] = data.prevClose[date][c]
		m.Tradable[c] = true
		m.Listed[c] = true
	}
	return m
}

// tradableFor 信号日可交易掩码（股票池成员）。
func tradableFor(data *portfolioRunData, date string) map[string]bool {
	out := make(map[string]bool, len(data.close[date]))
	for c := range data.close[date] {
		out[c] = true
	}
	return out
}

// weightsFromState 由执行后状态（收盘估值）派生期初持仓权重与现金权重。
func weightsFromState(state portfolioresearch.PortfolioState, closes map[string]float64) (map[string]float64, float64) {
	shares := map[string]int{}
	equity := state.Cash
	for _, lot := range state.Lots {
		shares[lot.Code] += lot.Shares
		equity += float64(lot.Shares) * closes[lot.Code]
	}
	if equity <= 0 {
		return map[string]float64{}, 1
	}
	holdings := make(map[string]float64, len(shares))
	for code, s := range shares {
		holdings[code] = float64(s) * closes[code] / equity
	}
	return holdings, state.Cash / equity
}

// returnsFor 当日股票日收益（收盘对前收盘；无前收盘的代码跳过）。
func returnsFor(data *portfolioRunData, date string) map[string]float64 {
	out := map[string]float64{}
	prev := data.prevClose[date]
	for code, c := range data.close[date] {
		p, ok := prev[code]
		if !ok || p <= 0 {
			continue
		}
		out[code] = c/p - 1
	}
	return out
}

// actualWeightsFor 由期末持仓与收盘价派生实际权重。
func actualWeightsFor(ledger portfolioresearch.DailyLedger, closes map[string]float64) map[string]float64 {
	shares := map[string]int{}
	for _, lot := range ledger.EndHoldings {
		shares[lot.Code] += lot.Shares
	}
	out := make(map[string]float64, len(shares))
	if ledger.EndEquity <= 0 {
		return out
	}
	for code, s := range shares {
		out[code] = float64(s) * closes[code] / ledger.EndEquity
	}
	return out
}

// buildAttribution 组合归因输入组装（净值口径来自组合时序；rf=0）。
func buildAttribution(ledgers []portfolioresearch.DailyLedger, usedTargets map[string]portfolioresearch.TargetPortfolio, data *portfolioRunData, series portfolioresearch.PortfolioSeries, notional float64) (portfolioresearch.PortfolioAttribution, error) {
	navByDate := map[string]portfolioresearch.PortfolioDay{}
	for _, d := range series.Days {
		navByDate[d.Date] = d
	}
	days := make([]portfolioresearch.AttributionDay, 0, len(ledgers))
	for _, ledger := range ledgers {
		day := portfolioresearch.AttributionDay{
			Date:          ledger.Date,
			CashRatio:     safeRatio(ledger.EndCash, ledger.EndEquity),
			Fees:          ledger.Fees.Total(),
			Returns:       returnsFor(data, ledger.Date),
			ActualWeights: actualWeightsFor(ledger, data.close[ledger.Date]),
		}
		if tgt, ok := usedTargets[ledger.Date]; ok {
			day.TargetWeights = tgt.Constrained.Effective
			day.IdealWeights = tgt.Ideal.Weights
		}
		if nav, ok := navByDate[ledger.Date]; ok {
			day.GrossNav = nav.GrossNav
			day.NetNav = nav.NetNav
		}
		days = append(days, day)
	}
	return portfolioresearch.ComputeAttribution(portfolioresearch.AttributionInput{
		InitialEquity: notional,
		RiskFreeRate:  0,
		Days:          days,
	})
}

// windowMetrics 单窗组合指标（基准缺失 → IR/超额为 nil，不阻止绝对收益）。
func windowMetrics(ledgers []portfolioresearch.DailyLedger) (portfolioresearch.ValidationWindowMetrics, error) {
	series := portfolioresearch.FromLedgers(ledgers)
	metrics, err := portfolioresearch.ComputeMetrics(series, nil, 0)
	if err != nil {
		return portfolioresearch.ValidationWindowMetrics{}, err
	}
	return portfolioresearch.ValidationWindowMetrics{
		TradingDays:      metrics.TradingDays,
		NetReturn:        metrics.Net.CumulativeReturn,
		MaxDrawdown:      metrics.Net.MaxDrawdown,
		CostDrag:         metrics.Quality.CostDrag,
		AnnualTurnover:   metrics.Quality.AnnualTurnover,
		AvgCashRatio:     metrics.Quality.AvgCashRatio,
		UnfilledRate:     unfilledRate(ledgers),
		MaxConcentration: metrics.Quality.MaxConcentration,
		// 基准缺失：信息比率/年化超额不可用（设计 §9.5）。
		InformationRatio:  nil,
		AnnualExcess:      nil,
		BaselineIncrement: 0, // 等权秩基线 = 模型本身；滚动 IC 基线另行计算
	}, nil
}

// unfilledRate 未成交率 = 未成交意图数 / 总意图数（[0,1]）。
func unfilledRate(ledgers []portfolioresearch.DailyLedger) float64 {
	intents, rejected := 0, 0
	for _, l := range ledgers {
		intents += len(l.Intents)
		rejected += len(l.Rejections)
	}
	if intents == 0 {
		return 0
	}
	return float64(rejected) / float64(intents)
}

// ---- 报告与产物 ----

// PortfolioReport 组合实验报告（output/portfolio/<id>/report.json）。
type PortfolioReport struct {
	SchemaVersion int                            `json:"schemaVersion"`
	ExperimentID  string                         `json:"experimentId"`
	FamilyID      string                         `json:"familyId"`
	ModelID       string                         `json:"modelId"`
	ModelRevision int                            `json:"modelRevision"`
	ModelHash     string                         `json:"modelHash"`
	StudyRange    DateRange                      `json:"studyRange"`
	StartedAt     string                         `json:"startedAt"`
	FinishedAt    string                         `json:"finishedAt"`
	EvidenceClass string                         `json:"evidenceClass"`
	CodeVersion   string                         `json:"codeVersion"`
	DataSnapshot  portfolioresearch.DataSnapshot `json:"dataSnapshot"`
	// Metrics 组合指标（毛/净、质量、稳定性；基准缺失时 Benchmark.Available=false）。
	Metrics portfolioresearch.PortfolioMetrics `json:"metrics"`
	// Attribution 组合归因（统计分解，非因果证明）。
	Attribution portfolioresearch.PortfolioAttribution `json:"attribution"`
	// Limitations 数据/模型限制说明。
	Limitations []string `json:"limitations,omitempty"`
	// Nav 逐日净值点（毛/净）。
	Nav []NavPoint `json:"nav"`
}

// NavPoint 单日净值点。
type NavPoint struct {
	Date      string  `json:"date"`
	GrossNav  float64 `json:"grossNav"`
	NetNav    float64 `json:"netNav"`
	CashRatio float64 `json:"cashRatio"`
	Holdings  int     `json:"holdings"`
}

// assembleExperimentReport 组装实验报告（口径与限制自证）。
func assembleExperimentReport(exp PortfolioExperiment, m portfolioresearch.FactorModel, metrics portfolioresearch.PortfolioMetrics, attribution portfolioresearch.PortfolioAttribution, series portfolioresearch.PortfolioSeries, data *portfolioRunData) *PortfolioReport {
	rep := &PortfolioReport{
		SchemaVersion: 1,
		ExperimentID:  exp.ExperimentID,
		FamilyID:      exp.FamilyID,
		ModelID:       m.ModelID,
		ModelRevision: m.Revision,
		ModelHash:     m.ModelHash,
		StudyRange:    exp.StudyRange,
		StartedAt:     exp.CreatedAt,
		FinishedAt:    time.Now().UTC().Format(time.RFC3339),
		EvidenceClass: exp.EvidenceClass,
		CodeVersion:   exp.CodeVersion,
		DataSnapshot:  exp.DataSnapshot,
		Metrics:       metrics,
		Attribution:   attribution,
		Limitations: []string{
			"涨跌停/停牌状态当前未建模，可交易性按股票池成员判断",
			"基准数据未接入，超额收益/信息比率不可用（设计 §9.5）",
			"v2 归因是统计分解，不是因果证明，也不是完整风险模型归因",
		},
		Nav: make([]NavPoint, 0, len(series.Days)),
	}
	for _, d := range series.Days {
		rep.Nav = append(rep.Nav, NavPoint{
			Date: d.Date, GrossNav: d.GrossNav, NetNav: d.NetNav,
			CashRatio: d.CashRatio, Holdings: d.Holdings,
		})
	}
	return rep
}

// writeExperimentArtifacts 写五类产物（report.json 原子写 + 四个 CSV）。
func (r *Runner) writeExperimentArtifacts(id string, rep *PortfolioReport, ledgers []portfolioresearch.DailyLedger) error {
	if r.experimentStore == nil {
		return fmt.Errorf("实验存储未注入")
	}
	dir := filepath.Join(r.experimentStore.artifactsRoot, id)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	buf, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		return err
	}
	if err := atomicWrite(filepath.Join(dir, "report.json"), buf); err != nil {
		return err
	}
	if err := writeNavCSV(filepath.Join(dir, "nav.csv"), rep.Nav); err != nil {
		return err
	}
	if err := writeLedgerCSVs(dir, ledgers); err != nil {
		return err
	}
	return nil
}

// writeNavCSV 净值 CSV。
func writeNavCSV(path string, nav []NavPoint) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	w := csv.NewWriter(f)
	defer w.Flush()
	if err := w.Write([]string{"date", "grossNav", "netNav", "cashRatio", "holdings"}); err != nil {
		return err
	}
	for _, p := range nav {
		if err := w.Write([]string{p.Date, fmt.Sprintf("%.6f", p.GrossNav), fmt.Sprintf("%.6f", p.NetNav),
			fmt.Sprintf("%.6f", p.CashRatio), fmt.Sprintf("%d", p.Holdings)}); err != nil {
			return err
		}
	}
	return w.Error()
}

// writeLedgerCSVs 订单/成交/持仓 CSV（orders/trades/holdings）。
func writeLedgerCSVs(dir string, ledgers []portfolioresearch.DailyLedger) error {
	orders, err := os.Create(filepath.Join(dir, "orders.csv"))
	if err != nil {
		return err
	}
	trades, err := os.Create(filepath.Join(dir, "trades.csv"))
	if err != nil {
		orders.Close()
		return err
	}
	holdings, err := os.Create(filepath.Join(dir, "holdings.csv"))
	if err != nil {
		orders.Close()
		trades.Close()
		return err
	}
	ow := csv.NewWriter(orders)
	tw := csv.NewWriter(trades)
	hw := csv.NewWriter(holdings)
	_ = ow.Write([]string{"date", "code", "side", "shares", "weight"})
	_ = tw.Write([]string{"date", "code", "side", "shares", "price", "grossAmount", "fees"})
	_ = hw.Write([]string{"date", "code", "shares"})
	for _, l := range ledgers {
		for _, it := range l.Intents {
			_ = ow.Write([]string{l.Date, it.Code, it.Side, fmt.Sprintf("%d", it.Shares), fmt.Sprintf("%.6f", it.Weight)})
		}
		for _, f := range l.Fills {
			_ = tw.Write([]string{f.Date, f.Code, f.Side, fmt.Sprintf("%d", f.Shares),
				fmt.Sprintf("%.4f", f.Price), fmt.Sprintf("%.2f", f.GrossAmount), fmt.Sprintf("%.4f", f.Fees.Total())})
		}
		shares := map[string]int{}
		for _, lot := range l.EndHoldings {
			shares[lot.Code] += lot.Shares
		}
		codes := make([]string, 0, len(shares))
		for c := range shares {
			codes = append(codes, c)
		}
		sort.Strings(codes)
		for _, c := range codes {
			_ = hw.Write([]string{l.Date, c, fmt.Sprintf("%d", shares[c])})
		}
	}
	ow.Flush()
	tw.Flush()
	hw.Flush()
	orders.Close()
	trades.Close()
	holdings.Close()
	if err := ow.Error(); err != nil {
		return err
	}
	if err := tw.Error(); err != nil {
		return err
	}
	return hw.Error()
}

// ---- 辅助 ----

// stopped 取消信号检查。
func stopped(stop chan struct{}) bool {
	select {
	case <-stop:
		return true
	default:
		return false
	}
}

// yearsBetween 起止年份列表（含两端）。
func yearsBetween(start, end time.Time) []int {
	var out []int
	for y := start.Year(); y <= end.Year(); y++ {
		out = append(out, y)
	}
	return out
}

// filterDates 过滤日期到给定区间（区间为空返回 nil）。
func filterDates(dates []string, rng DateRange) []string {
	if rng.Start == "" || rng.End == "" {
		return nil
	}
	var out []string
	for _, d := range dates {
		if d >= rng.Start && d <= rng.End {
			out = append(out, d)
		}
	}
	return out
}

// datesBetween 取 [start, end] 内的日期（升序保持）。
func datesBetween(dates []string, start, end string) []string {
	var out []string
	for _, d := range dates {
		if d >= start && d <= end {
			out = append(out, d)
		}
	}
	return out
}

// safeRatio a/b，b<=0 时返回 0。
func safeRatio(a, b float64) float64 {
	if b <= 0 {
		return 0
	}
	return a / b
}
