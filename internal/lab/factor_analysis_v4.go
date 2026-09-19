package lab

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"sync"
	"time"

	common "github.com/injoyai/strategy-tail"
	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/internal/researchrun"
	f "github.com/injoyai/strategy-tail/strategies/factor"
)

// factor_analysis_v4.go：v4 研究协议分析编排（Task 5）。v3 主循环保持原行为
// 不变；带 Protocol 的分析走本文件的 runAnalysisV4。固定顺序（计划 Task 5
// Step 1）：解析并 hash Protocol → 解析股票池取得区间历史成员并集 →
// ForEachCodeYearData 加载 → 每个股票日只算一次 FactorObservation → 按
// Horizon 生成可交易标签 → 按交易日历史成员关系过滤截面 → 每日截面 raw
// IC / groups / turnover → 年度和全区间聚合 → 生成 EvidenceClass → 写不可变
// v4 报告。

// YearHorizonResult 单 Horizon 的年度拆分：与全区间同一批逐日观测按自然年
// 切段、同口径重算（交易日等权），使跨周期汇总无法掩盖某些年份失效或反向。
type YearHorizonResult struct {
	Year        int                `json:"year"`
	IC          ExtendedICStats    `json:"ic"`
	Groups      []FactorGroupStats `json:"groups,omitempty"`
	Summary     QuintileSummary    `json:"summary"`
	TradingDays int                `json:"tradingDays"`
}

// HorizonAnalysis 单 Horizon 的完整 v4 分析（设计 §8.1 canonical 合同）。
// Label 填协议标签类型（Protocol.Labels.Kind）：设计仅定义字段未给出语义，
// 取标签合同字符串——该字段紧跟 Horizon 且 v4 的核心增量正是标签合同，
// 展示层可据此区分 next_open_to_close 与 legacy 标签。
type HorizonAnalysis struct {
	Horizon       int                 `json:"horizon"`
	Label         string              `json:"label"`
	IC            ExtendedICStats     `json:"ic"`
	Groups        []FactorGroupStats  `json:"groups"`
	Summary       QuintileSummary     `json:"summary"`
	Years         []YearHorizonResult `json:"years"`
	Turnover      []TurnoverPoint     `json:"turnover"`
	LabelCoverage LabelCoverage       `json:"labelCoverage"`
}

// resolveHACLag 按 HACLagMode 解析某 Horizon 的实际 HAC 滞后：
// horizon_minus_one → max(h-1, 0)（多日重叠标签的 Newey-West 默认）；
// fixed → 协议固定值。协议 Validate 保证 mode 合法，default 不可达；
// 仍 fail closed 返回 -1（extendedICStats 对 lag<0 记 HACLag=0 且不计算 HAC）。
func resolveHACLag(spec DiagnosticSpec, h int) int {
	switch spec.HACLagMode {
	case hacLagHorizonMinusOne:
		return max(h-1, 0)
	case hacLagFixed:
		return spec.HACLag
	default:
		return -1
	}
}

// mirrorICStats 从 v4 ExtendedICStats 折算旧 v3 ICStats 镜像（deprecated 字段）：
// v3 TStat 与 NaiveTStat 同口径（Mean/(Std/√n)）；null 统计折算为 0 由
// Pairs 表达样本不足，与 v3 icStats 语义一致。
func mirrorICStats(x ExtendedICStats) ICStats {
	s := ICStats{Pairs: x.Pairs}
	if x.Mean != nil {
		s.Mean = *x.Mean
	}
	if x.Std != nil {
		s.Std = *x.Std
	}
	if x.NaiveTStat != nil {
		s.TStat = *x.NaiveTStat
	}
	return s
}

// dailyGroupMembers 按与 aggregateSet 相同的中点公式 (i+j)*g/(2n) 重建指定组
// （groupIdx 0 基）的每日成员集合序列，供换手序列与切组严格同口径。
// aggregateSet.membersOut 输出的是全区间按组索引累积的并集（组维度），无法
// 表达按日维度的成员关系，故基于排序缓存二次线性扫描（无重复排序）。
func (c *quantileCache) dailyGroupMembers(g, groupIdx int) []map[string]struct{} {
	out := make([]map[string]struct{}, len(c.vals))
	for di, ds := range c.vals {
		m := map[string]struct{}{}
		n := len(ds)
		for i := 0; i < n; {
			j := i
			for j+1 < n && ds[j+1] == ds[i] {
				j++
			}
			if gr := (i + j) * g / (2 * n); gr == groupIdx {
				for k := i; k <= j; k++ {
					m[c.codes[di][k]] = struct{}{}
				}
			}
			i = j + 1
		}
		out[di] = m
	}
	return out
}

// runAnalysisV4 v4 编排入口：多周期、可交易标签、换手与证据等级。
// 主周期 = Horizons[0]，旧 Window/Stats/Groups/Years/Daily/Quintiles 只镜像
// 主周期并标记 deprecated；v4 canonical 数据在 HorizonAnalyses。
func (r *Runner) runAnalysisV4(id string, cfg AnalyzeConfig, stop chan struct{}) (*AnalysisReport, error) {
	started := time.Now()

	// 1. 解析并 hash Protocol：Normalize 校验+深拷贝，hash 绑定协议与报告。
	proto, err := cfg.Protocol.Normalize()
	if err != nil {
		return nil, fmt.Errorf("研究协议非法: %w", err)
	}
	hash, err := protocolHash(proto)
	if err != nil {
		return nil, fmt.Errorf("协议 hash 失败: %w", err)
	}
	evidence := deriveEvidenceClass(proto)
	horizons := proto.Labels.Horizons
	maxH := horizons[len(horizons)-1]

	if id == "" {
		if id, err = generateAnalysisID(); err != nil {
			return nil, fmt.Errorf("生成分析 ID 失败: %w", err)
		}
	}

	// 试验账本（设计 §10.1，计划 Task 6 Step 3）：analysis ID 先于 Start
	// 生成；每次 v4 运行都登记，失败、取消与无样本同样保留。登记失败
	// fail closed 中止——缺账本的运行不得静默继续（生存者偏差）。
	fct := f.BuildContext(cfg.Kind, cfg.Days)
	entry, _ := f.Catalog(cfg.Kind)
	effDays := cfg.Days
	if effDays <= 0 {
		effDays = entry.DefaultDays
	}
	snapshot := FactorSnapshot{
		Kind:                  cfg.Kind,
		Name:                  fct.Name(),
		Description:           entry.Description,
		ParameterLabel:        entry.ParameterLabel,
		Days:                  effDays,
		Unit:                  entry.Unit,
		ImplementationVersion: entry.ImplementationVersion,
	}
	trials := r.trials
	if trials == nil {
		// 直接构造的 Runner（旧测试路径）使用 cwd 相对默认根；
		// 生产与 Server 路径始终显式注入。
		trials = NewTrialStore(DefaultTrialRoot())
	}
	trial, terr := trials.Start(proto, snapshot, id)
	if terr != nil {
		return nil, fmt.Errorf("试验登记失败: %w", terr)
	}
	// finishTrial 全部返回路径的收敛点：Finish 失败不影响主返回值（与
	// 兼容镜像同样的 best-effort 约定），但任何路径都不得遗漏。
	finishTrial := func(status TrialStatus, msg string) {
		_ = trials.Finish(trial.ID, TrialResult{Status: status, Message: msg})
	}

	// 2. 股票池解析：协议模式决定加载范围。historical_membership 取区间
	// 成员并集加载，随后按交易日 Contains 过滤截面；current_static 与
	// codes 复用 v3 resolveCodes 行为。
	histMembership := proto.Universe.Mode == universeHistoricalMembership
	var codes []string
	if histMembership {
		start := time.Date(cfg.RunConfig.StartYear, 1, 1, 0, 0, 0, 0, time.Local)
		end := time.Date(cfg.RunConfig.EndYear, 12, 31, 23, 59, 59, 0, time.Local)
		codes, err = r.universe.CodesBetween(start, end)
		if err != nil {
			finishTrial(TrialStatusFailed, "解析历史成员股票池失败: "+err.Error())
			return nil, fmt.Errorf("解析历史成员股票池失败: %w", err)
		}
	} else {
		codes, err = r.resolveCodes(cfg.RunConfig, stop)
		if err != nil {
			finishTrial(TrialStatusFailed, err.Error())
			return nil, err
		}
	}
	r.totalCodes.Store(int64(len(codes)))
	years := cfg.RunConfig.years()

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
	// 因子值截面与 Horizon 无关（每股票日只算一次），全局一份；
	// 收益标签与覆盖按 Horizon 各一份。
	vals := map[time.Time]map[string]float64{}
	retsByH := make(map[int]map[time.Time]map[string]float64, len(horizons))
	covByH := make(map[int]LabelCoverage, len(horizons))
	for _, h := range horizons {
		retsByH[h] = map[time.Time]map[string]float64{}
		covByH[h] = LabelCoverage{}
	}
	coverage := researchrun.Coverage{Requested: len(codes)}

	// 3-6. 逐“股票×年份”加载并构建观察：ForwardDays = max(Horizons) 保证
	// 最大周期尾部标签也能取到下一年开票价（只读缓冲区，绝不进入因子前缀）。
	yearCoverage, err := researchrun.ForEachCodeYearData(ctx, researchrun.Config{
		Codes:        codes,
		Years:        years,
		Workers:      common.DefaultGoroutines * 2,
		DataMode:     researchrun.DailyClose,
		GetDayKlines: common.Pull.DayKlines,
		ForwardDays:  maxH,
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
		res := buildObservations(code, d, fct, horizons, proto.Labels.Kind, r.factorData)
		// 局部聚合后再入锁：同票同年同日唯一，锁内纯合并无冲突
		lv := map[time.Time]float64{}
		lr := make(map[int]map[time.Time]float64, len(res.Labeled))
		for _, obs := range res.Observations {
			lv[core.DayOf(obs.Date)] = obs.Value
		}
		for h, lbls := range res.Labeled {
			lm := map[time.Time]float64{}
			for _, lo := range lbls {
				lm[core.DayOf(lo.Date)] = lo.Return
			}
			lr[h] = lm
		}
		mu.Lock()
		for day, v := range lv {
			if vals[day] == nil {
				vals[day] = map[string]float64{}
			}
			vals[day][code] = v
		}
		for h, lm := range lr {
			for day, ret := range lm {
				if retsByH[h][day] == nil {
					retsByH[h][day] = map[string]float64{}
				}
				retsByH[h][day][code] = ret
			}
			cov := covByH[h]
			cov.merge(res.Coverage[h])
			covByH[h] = cov
		}
		mu.Unlock()
	})
	if err != nil {
		if errors.Is(err, context.Canceled) {
			finishTrial(TrialStatusCanceled, "运行被停止")
			return nil, errStopped
		}
		finishTrial(TrialStatusFailed, err.Error())
		return nil, err
	}

	select {
	case <-stop:
		finishTrial(TrialStatusCanceled, "运行被停止")
		return nil, errStopped
	default:
	}

	sortFailures(coverage.Failures)
	sortFailures(yearCoverage.Failures)

	// historical_membership：按交易日历史成员关系过滤截面（fail closed：
	// 查询失败视为非成员并记录失败明细，不静默假设在池内）。收益截面与
	// 因子值截面保持同键。
	if histMembership {
		for day, cv := range vals {
			for code := range cv {
				ok, cerr := r.universe.Contains(code, day)
				if cerr != nil {
					coverage.Failures = append(coverage.Failures, researchrun.Failure{
						Code: code, Year: day.Year(), Stage: "universe_contains", Message: cerr.Error(),
					})
					delete(cv, code)
					continue
				}
				if !ok {
					delete(cv, code)
				}
			}
			for _, h := range horizons {
				for code := range retsByH[h][day] {
					if _, ok := cv[code]; !ok {
						delete(retsByH[h][day], code)
					}
				}
			}
		}
		sortFailures(coverage.Failures)
	}

	// 7-9. 每 Horizon 全区间与年度聚合。聚合期构建“因子值×标签”对齐截面：
	// aggregateIC/buildQuantileCache 契约要求 vals/rets 同键（同日同票既有
	// 因子值又有该周期标签），尾部 insufficient_horizon 的观测按周期剔除。
	grouping := proto.Diagnostics.Grouping
	isBins := grouping.Mode == "bins"
	g := grouping.groupCount()
	periods := proto.Diagnostics.TurnoverPeriods

	// 中性化（Diagnostics.Neutralization）：v4 报告合同（HorizonAnalysis
	// 八字段）尚无中性化结果承载字段，v1 按最小合同不执行计算；协议校验
	// 已保证声明合法，后续扩展承载字段时在此接入 neutralizeCrossSection。

	type hResult struct {
		analysis    HorizonAnalysis
		daily       []DailyIC
		first, last string
		quintiles   []float64
	}
	results := make([]hResult, len(horizons))
	statsByH := make(map[int]ExtendedICStats, len(horizons))
	spreadsByH := make(map[int]*float64, len(horizons))

	for hi, h := range horizons {
		select {
		case <-stop:
			finishTrial(TrialStatusCanceled, "运行被停止")
			return nil, errStopped
		default:
		}
		rets := retsByH[h]
		days := make([]time.Time, 0, len(rets))
		for day := range rets {
			if len(rets[day]) > 0 {
				days = append(days, day)
			}
		}
		sort.Slice(days, func(i, j int) bool { return days[i].Before(days[j]) })

		hv := map[time.Time]map[string]float64{}
		for _, day := range days {
			cv := map[string]float64{}
			for code := range rets[day] {
				cv[code] = vals[day][code]
			}
			hv[day] = cv
		}

		lag := resolveHACLag(proto.Diagnostics, h)
		// 年度拆分复用同一对齐截面与 lag：与全区间同口径重算，避免先算
		// 年度均值再加权（交易日较少的年份会获得过高权重）。
		icOf := func(days []time.Time) ExtendedICStats {
			agg := aggregateIC(days, hv, rets)
			ics := make([]float64, 0, len(agg.Daily))
			for _, d := range agg.Daily {
				if d.IC != nil {
					ics = append(ics, *d.IC)
				}
			}
			return extendedICStats(ics, proto.Hypothesis.ExpectedDirection, lag)
		}
		overall := aggregateIC(days, hv, rets)
		ics := make([]float64, 0, len(overall.Daily))
		for _, d := range overall.Daily {
			if d.IC != nil {
				ics = append(ics, *d.IC)
			}
		}
		icStats := extendedICStats(ics, proto.Hypothesis.ExpectedDirection, lag)
		statsByH[h] = icStats

		var groups []FactorGroupStats
		var qs []float64
		var summary QuintileSummary
		var turnover []TurnoverPoint
		if isBins {
			groups, qs, summary = aggregateGroups(days, hv, rets, grouping)
		} else {
			cache := buildQuantileCache(days, hv, rets)
			set := cache.aggregateSet(g, true, nil)
			groups, qs, summary = set.Stats, set.Quintiles, set.Summary
			// 换手序列（设计 §8.4）：等频切组报告最高组（Q_g）的每日成员
			// 关系——多空组合多头侧的常见研究口径；多间隔请求 v1 取第一个
			//（TurnoverPeriods 升序，最小周期最先反映成员变动）。bins 固定
			// 区间不走 aggregateSet 的每日成员路径，v1 不输出换手序列。
			if len(periods) > 0 {
				members := cache.dailyGroupMembers(g, g-1)
				dates := make([]string, len(cache.days))
				for i, d := range cache.days {
					dates[i] = d.Format("2006-01-02")
				}
				turnover = groupTurnoverSeries(dates, members, periods[0])
			}
		}
		spreadsByH[h] = summary.Spread

		yresults := make([]YearHorizonResult, 0, len(years))
		for _, gd := range groupDaysByYear(days) {
			yr := YearHorizonResult{
				Year:        gd[0].Year(),
				IC:          icOf(gd),
				TradingDays: len(gd),
			}
			if isBins {
				yg, _, ys := aggregateGroups(gd, hv, rets, grouping)
				yr.Groups, yr.Summary = yg, ys
			} else {
				ycache := buildQuantileCache(gd, hv, rets)
				yset := ycache.aggregateSet(g, false, nil)
				yr.Groups, yr.Summary = yset.Stats, yset.Summary
			}
			yresults = append(yresults, yr)
		}

		first, last := "", ""
		if len(days) > 0 {
			first = days[0].Format("2006-01-02")
			last = days[len(days)-1].Format("2006-01-02")
		}
		results[hi] = hResult{
			analysis: HorizonAnalysis{
				Horizon:       h,
				Label:         proto.Labels.Kind,
				IC:            icStats,
				Groups:        groups,
				Summary:       summary,
				Years:         yresults,
				Turnover:      turnover,
				LabelCoverage: covByH[h],
			},
			daily:     overall.Daily,
			first:     first,
			last:      last,
			quintiles: qs,
		}
	}

	analyses := make([]HorizonAnalysis, len(results))
	hdaily := make(map[int][]DailyIC, len(results))
	for i, res := range results {
		analyses[i] = res.analysis
		hdaily[horizons[i]] = res.daily
	}
	decay := buildDecayCurve(horizons, statsByH, spreadsByH)

	// 10. 报告组装：canonical 数据在 Horizons/HorizonAnalyses/DecayCurve；
	// 旧字段只镜像主周期（deprecated），历史报告与旧页面继续可用。
	main := results[0]
	yearly := make([]YearAnalysis, 0, len(main.analysis.Years))
	for _, yr := range main.analysis.Years {
		yqs, ysum := summarizeFromStats(yr.Groups, g)
		ya := YearAnalysis{
			Year:        yr.Year,
			Stats:       mirrorICStats(yr.IC),
			Quintiles:   yqs,
			Summary:     ysum,
			TradingDays: yr.TradingDays,
		}
		if isBins { // v3 语义：quantile 年度组统计走 AllGroupings，v4 无全档预算故留空
			ya.Groups = yr.Groups
		}
		yearly = append(yearly, ya)
	}

	rep := &AnalysisReport{
		FactorName: fct.Name(),
		Kind:       cfg.Kind,
		Window:     horizons[0],
		Range: AnalysisRange{
			StartYear:  cfg.RunConfig.StartYear,
			EndYear:    cfg.RunConfig.EndYear,
			SampleMode: cfg.RunConfig.SampleMode,
			SampleSize: len(codes),
		},
		AnalysisVersion: 4,
		Factor:          snapshot,
		Grouping:        grouping,
		Groups:          main.analysis.Groups,
		Years:           yearly,
		Stats:           mirrorICStats(main.analysis.IC),
		Summary:         main.analysis.Summary,
		Coverage:        coverage,
		YearCoverage:    yearCoverage,
		FirstDataDate:   main.first,
		LastDataDate:    main.last,
		Daily:           main.daily,
		StartedAt:       started.Format(time.RFC3339),
		FinishedAt:      time.Now().Format(time.RFC3339),

		Protocol:        &proto,
		ProtocolHash:    hash,
		EvidenceClass:   evidence,
		Horizons:        horizons,
		HorizonAnalyses: analyses,
		HorizonDaily:    hdaily,
		DecayCurve:      decay,
	}
	if main.quintiles != nil { // 旧页面兼容：主周期组完整时镜像组收益
		rep.Quintiles = main.quintiles
	}
	// 分析 ID 在导出前完成赋值：不可变历史与候选证据追溯以此为准。
	rep.AnalysisID = id

	// 无有效标签样本（设计 §10.1 insufficient）：全部 Horizon 的有效标签数
	// 为 0 时运行虽成功但无样本，报告照常落盘，账本单独标注终态。
	trialStatus, trialMsg := TrialStatusCompleted, ""
	labeled := 0
	for _, ha := range rep.HorizonAnalyses {
		labeled += ha.LabelCoverage.LabeledSignals
	}
	if labeled == 0 {
		trialStatus, trialMsg = TrialStatusInsufficient, "运行成功但无有效标签样本"
	}

	store := r.store
	if store == nil {
		// 直接构造的 Runner（旧测试路径）使用 cwd 相对默认根；
		// 生产与 Server 路径始终显式注入。
		store = NewAnalysisStore(filepath.Join("output", "factor"))
	}
	if err := store.Save(rep); err != nil {
		finishTrial(TrialStatusFailed, "分析报告落盘失败: "+err.Error())
		return nil, fmt.Errorf("分析报告落盘失败: %w", err)
	}
	finishTrial(trialStatus, trialMsg)
	return rep, nil
}
