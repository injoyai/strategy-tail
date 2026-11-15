package lab

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"sync"
	"time"

	common "github.com/injoyai/strategy-tail"
	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/internal/lab/factorstats"
	"github.com/injoyai/strategy-tail/internal/researchrun"
	"github.com/injoyai/strategy-tail/lib/extend"
	f "github.com/injoyai/strategy-tail/strategies/factor"
	"github.com/injoyai/tdx/v2/protocol"
)

const taskFactorValidation = "factor_validation"

var errFactorValidationNotReady = errors.New("测试区间尚未结束或指数日线尚未覆盖区间结束日")

func (r *Runner) startFactorValidation(id string, store *ValidationStore) error {
	if !validValidationID(id) {
		return fmt.Errorf("非法验证 ID")
	}
	if !r.mu.TryLock() {
		return fmt.Errorf("已有任务在运行")
	}
	unlock, err := lockResearchRun(store.root, id)
	if err != nil {
		r.mu.Unlock()
		return err
	}
	view, err := store.Get(id)
	switch {
	case err != nil:
	case view.State != ValidationStateFrozen:
		err = errValidationCompleted
	case view.Request.Protocol.TestRange == nil:
		err = fmt.Errorf("旧记录未冻结明确的测试日期，请新建验证")
	case view.Request.Protocol.TestRange.End >= time.Now().In(protocol.Location()).Format(time.DateOnly):
		err = errFactorValidationNotReady
	}
	if err != nil {
		_ = unlock()
		r.mu.Unlock()
		return err
	}
	r.startPortfolioGoroutine(id, taskFactorValidation, func(stop chan struct{}) error {
		defer unlock()
		return r.runFactorValidation(view.Request, store, stop)
	})
	return nil
}

func (r *Runner) runFactorValidation(req ValidationRequest, store *ValidationStore, stop chan struct{}) error {
	if common.Pull == nil {
		return fmt.Errorf("行情读取器未初始化")
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
	test := req.Protocol.TestRange
	start, _ := time.ParseInLocation(time.DateOnly, test.Start, protocol.Location())
	end, _ := time.ParseInLocation(time.DateOnly, test.End, protocol.Location())
	lastSignal := req.Protocol.DiscoverySignalThrough
	if lastSignal == "" {
		lastSignal = req.Candidate.Evidence.LastDataDate
	}
	discoveryEnd, err := time.ParseInLocation(time.DateOnly, lastSignal, protocol.Location())
	if err != nil {
		return fmt.Errorf("冻结记录的发现期最后信号日无效")
	}
	calendarStart := minDate(discoveryEnd, start.AddDate(-req.Protocol.Windows.TrainYears, -1, 0))
	indexBars, err := common.Pull.DayKlines(factorCalendarCode, calendarStart, end.AddDate(0, 0, 14))
	if err != nil {
		return fmt.Errorf("加载验证交易日历失败: %w", err)
	}
	calendar := newFactorSessionCalendar(indexBars)
	if len(calendar.days) == 0 || calendar.days[len(calendar.days)-1].Before(end) {
		return errFactorValidationNotReady
	}
	var codes []string
	historical := req.ResearchProtocol.Universe.Mode == universeHistoricalMembership
	if historical {
		snapshot := r.universe.Snapshot()
		if snapshot.Mode != universeHistoricalMembership || snapshot.ID != req.ResearchProtocol.Universe.ID ||
			snapshot.Version != req.ResearchProtocol.Universe.Version {
			return fmt.Errorf("历史成员股票池与冻结版本不一致")
		}
		codes, err = r.universe.CodesBetween(start, end)
	} else {
		codes = slices.Clone(req.Candidate.Evidence.Range.SampleCodes)
		if len(codes) == 0 {
			return fmt.Errorf("发现期未保留股票名单，请重新分析后保存候选")
		}
	}
	if err != nil {
		return err
	}
	entry, ok := f.Catalog(req.Candidate.Factor.Kind)
	if !ok || entry.ImplementationVersion != req.Candidate.Factor.ImplementationVersion {
		return fmt.Errorf("因子实现已变更，请重新分析后保存候选")
	}
	factor := f.BuildContext(req.Candidate.Factor.Kind, req.Candidate.Factor.Days)
	proto := req.ResearchProtocol
	windows := req.Protocol.Windows
	inputs := r.runInputs(codes, nil, factorCalendarCode)
	for _, day := range calendar.days {
		if !day.After(end) {
			inputs.Calendar = append(inputs.Calendar, day.Format(time.DateOnly))
		}
	}
	reports := []ValidationWindowReport{}
	dailyHashes := map[string]string{}
	for first := start; !first.After(end); first = first.AddDate(windows.StepYears, 0, 0) {
		last := minDate(first.AddDate(windows.TestYears, 0, -1), end)
		begin, _ := slices.BinarySearchFunc(calendar.days, first, time.Time.Compare)
		after, found := slices.BinarySearchFunc(calendar.days, last, time.Time.Compare)
		if found {
			after++
		}
		window := ValidationWindowReport{SchemaVersion: validationSchemaVersion, ValidationID: req.ID, Index: len(reports) + 1,
			StartDate: first.Format(time.DateOnly), EndDate: last.Format(time.DateOnly), State: ValidationWindowInsufficient,
			TrainStart: first.AddDate(-windows.TrainYears, 0, 0).Format(time.DateOnly), Message: "有效测试交易日或截面不足"}
		if begin >= windows.PurgeDays && begin-windows.PurgeDays > 0 {
			window.TrainEnd = calendar.days[begin-windows.PurgeDays-1].Format(time.DateOnly)
		}
		maxOffset := proto.Labels.Horizons[len(proto.Labels.Horizons)-1] + 1
		// The final discovery signal may use prices after its signal date.
		// Discard test signals until those labels could have fully matured.
		endOfDiscovery, found := slices.BinarySearchFunc(calendar.days, discoveryEnd, time.Time.Compare)
		if found {
			endOfDiscovery++
		}
		startIndex := max(begin, endOfDiscovery+maxOffset)
		if startIndex < after-maxOffset && len(codes) > 0 {
			r.setTaskProgress(taskFactorValidation, "", researchrun.Progress{Phase: "observations", Code: window.StartDate})
			boundedCalendar := factorSessionCalendar{days: calendar.days[:after], index: calendar.index}
			var hashes map[string]string
			window, hashes, err = r.measureFactorWindow(ctx, req, window, factor, codes, &boundedCalendar, calendar.days[startIndex], calendar.days[after-maxOffset-1], common.Pull.DayKlines)
			if errors.Is(err, context.Canceled) {
				return errStopped
			}
			if err != nil {
				return err
			}
			dailyHashes[window.StartDate] = marketInputHash(hashes)
		}
		if stopped(stop) {
			return errStopped
		}
		if err := store.SaveWindow(req.ID, window); err != nil {
			return err
		}
		reports = append(reports, window)
	}
	if stopped(stop) {
		return errStopped
	}
	inputs.DailyInputSHA = marketInputHash(dailyHashes)
	return store.Complete(req.ID, summarizeFactorValidation(req, reports, inputs))
}

func minDate(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

func (r *Runner) measureFactorWindow(ctx context.Context, req ValidationRequest, w ValidationWindowReport, factor core.ContextFactor,
	codes []string, calendar *factorSessionCalendar, first, last time.Time, readDays func(string, time.Time, time.Time) (extend.Klines, error),
) (ValidationWindowReport, map[string]string, error) {
	proto := req.ResearchProtocol
	horizons := proto.Labels.Horizons
	windowEnd, _ := time.ParseInLocation(time.DateOnly, w.EndDate, protocol.Location())
	years := make([]int, 0, windowEnd.Year()-first.Year()+1)
	for y := first.Year(); y <= windowEnd.Year(); y++ {
		years = append(years, y)
	}
	coverage := map[int]LabelCoverage{}
	var signalCoverage SignalCoverage
	values := map[time.Time]map[string]float64{}
	returns := map[int]map[time.Time]map[string]float64{}
	for _, h := range horizons {
		returns[h] = map[time.Time]map[string]float64{}
	}
	var mu sync.Mutex
	hashes := map[string]string{}
	var readErr error
	inRange := func(at time.Time) bool {
		day := core.DayOf(at)
		return !day.Before(first) && !day.After(last)
	}
	yearCoverage, err := researchrun.ForEachCodeYearData(ctx, researchrun.Config{Codes: codes, Years: years,
		Workers: common.DefaultGoroutines * 2, DataMode: researchrun.DailyClose,
		GetDayKlines: func(code string, start, end time.Time) (extend.Klines, error) {
			if start.Format(time.DateOnly) > w.EndDate {
				return nil, nil
			}
			if end.Format(time.DateOnly) > w.EndDate {
				end = windowEnd.Add(23 * time.Hour)
			}
			return readDays(code, start, end)
		},
		ForwardDays: horizons[len(horizons)-1] + 1,
	}, func(code string, year int, data researchrun.YearData) {
		var membershipErr error
		included := func(at time.Time) bool {
			if !inRange(at) {
				return false
			}
			if proto.Universe.Mode != universeHistoricalMembership {
				return true
			}
			ok, err := r.universe.Contains(code, at)
			if err != nil {
				membershipErr = err
			}
			return ok && err == nil
		}
		var events []string
		if proto.Universe.TradabilityPolicy == labelPolicySkipCorporateEvents && common.Pull != nil {
			events = corporateEventDates(common.Pull.CorporateEvents(code))
		}
		res := observeFactor(code, data, factor, horizons, proto.Labels.Kind, r.factorData, calendar, included, events)
		hash := factorDailyInputHash(data, events)
		mu.Lock()
		defer mu.Unlock()
		if membershipErr != nil {
			readErr = membershipErr
		}
		if data.ForwardFailure != nil {
			readErr = fmt.Errorf("%s 跨年标签行情读取失败", code)
		}
		signalCoverage.Observed += res.Signals.Observed
		signalCoverage.Valid += res.Signals.Valid
		hashes[fmt.Sprintf("%s/%d", code, year)] = hash
		for _, obs := range res.Observations {
			putFactorPoint(values, core.DayOf(obs.Date), code, obs.Value)
		}
		for _, h := range horizons {
			cov := coverage[h]
			cov.merge(res.Coverage[h])
			coverage[h] = cov
			for _, obs := range res.Labeled[h] {
				putFactorPoint(returns[h], core.DayOf(obs.Date), code, obs.Return)
			}
		}
	})
	if err != nil {
		return w, nil, err
	}
	if readErr != nil {
		return w, nil, fmt.Errorf("验证输入读取失败: %w", readErr)
	}
	w.ObservedSignals = signalCoverage.Observed
	w.ValidSignals = signalCoverage.Valid
	w.MissingCodeYears = yearCoverage.SkippedCodeYears
	for _, failure := range yearCoverage.Failures {
		if failure.Stage != "period" && failure.Message != "no day K-line data" {
			return w, nil, fmt.Errorf("%s %d 年的验证行情读取失败", failure.Code, failure.Year)
		}
	}
	days := make([]time.Time, 0, len(values))
	for day := range values {
		days = append(days, day)
	}
	slices.SortFunc(days, time.Time.Compare)
	for _, h := range horizons {
		stats, observations := factorWindowHorizon(req, h, days, values, returns[h], coverage[h])
		if w.ObservedSignals > 0 {
			stats.Coverage = float64(coverage[h].LabeledSignals) / float64(w.ObservedSignals)
		}
		if h == req.Candidate.Evidence.Window {
			w.Observations = observations
			if observations >= minPairs && stats.Coverage >= req.Protocol.Policy.MinCoverage {
				w.State, w.Message = ValidationWindowOK, ""
			} else {
				w.Message = "主要周期的有效 IC 日数或标签覆盖率未达门槛"
			}
		}
		w.Horizons = append(w.Horizons, stats)
	}
	return w, hashes, nil
}

func putFactorPoint(series map[time.Time]map[string]float64, date time.Time, code string, value float64) {
	if series[date] == nil {
		series[date] = map[string]float64{}
	}
	series[date][code] = value
}

func factorWindowHorizon(req ValidationRequest, h int, days []time.Time, values, returns map[time.Time]map[string]float64, cov LabelCoverage) (ValidationWindowHorizon, int) {
	raw := aggregateIC(days, values, returns)
	ics := []float64{}
	for _, p := range raw.Daily {
		if p.IC != nil {
			ics = append(ics, *p.IC)
		}
	}
	p := req.ResearchProtocol
	stats := factorstats.ComputeICStats(ics, req.Protocol.Direction, resolveHACLag(p.Diagnostics, h))
	result := ValidationWindowHorizon{Horizon: h, Direction: "unknown", IC: stats.Mean, HACTStat: stats.HACTStat, LabelCoverage: &cov}
	if cov.EligibleSignals > 0 {
		result.Coverage = float64(cov.LabeledSignals) / float64(cov.EligibleSignals)
	}
	if stats.Mean != nil {
		switch {
		case *stats.Mean == 0:
		case (*stats.Mean > 0) == (req.Protocol.Direction == labelDirectionPositive):
			result.Direction = "matched"
		default:
			result.Direction = "inverted"
		}
	}
	grouping := p.Diagnostics.Grouping
	if grouping.Mode == "bins" {
		grouping = GroupingConfig{} // A fixed-bin discovery still uses rank groups for validation.
	}
	g := grouping.groupCount()
	high := g - 1
	if req.Protocol.Direction == labelDirectionNegative {
		high = 0
	}
	cache := buildQuantileCache(days, values, returns)
	top, bottom := cache.dailyGroupMembers(g, high), cache.dailyGroupMembers(g, g-1-high)
	spread, excess, n := 0.0, 0.0, 0
	for i, day := range days {
		dayReturns := returns[day]
		if len(dayReturns) < minPairs {
			continue
		}
		a, b := groupLabelAverage(top[i], dayReturns), groupLabelAverage(bottom[i], dayReturns)
		if math.IsNaN(a) || math.IsNaN(b) {
			continue
		}
		spread += a - b
		excess += a - groupLabelAverage(nil, dayReturns)
		n++
	}
	if n > 0 {
		a, b := spread/float64(n), excess/float64(n)
		result.Spread, result.TopExcess = &a, &b
	}
	return result, len(ics)
}

// A nil member set means the whole matched cross-section.
func groupLabelAverage(members map[string]struct{}, returns map[string]float64) float64 {
	codes := make([]string, 0, len(returns))
	for code := range returns {
		if _, ok := members[code]; ok || members == nil {
			codes = append(codes, code)
		}
	}
	slices.Sort(codes)
	sum := 0.0
	for _, code := range codes {
		sum += returns[code]
	}
	return sum / float64(len(codes))
}

func summarizeFactorValidation(req ValidationRequest, windows []ValidationWindowReport, inputs *RunInputs) FactorValidationReport {
	policy := req.Protocol.Policy
	horizons := req.ResearchProtocol.Labels.Horizons
	rep := FactorValidationReport{SchemaVersion: validationSchemaVersion, ValidationID: req.ID, FinishedAt: time.Now().UTC().Format(time.RFC3339),
		WindowCount: len(windows), Inputs: inputs, Aggregate: &ValidationAggregate{GateAggregation: policy.Aggregation}}
	matched, observed, eligible, labeled := 0, 0, 0, 0
	for _, w := range windows {
		for _, h := range w.Horizons {
			if h.Horizon != req.Candidate.Evidence.Window {
				continue
			}
			if h.LabelCoverage != nil {
				eligible += w.ObservedSignals
				labeled += h.LabelCoverage.LabeledSignals
			}
			if w.State == ValidationWindowOK {
				rep.ValidWindowCount++
				observed += w.Observations
				if h.Direction == "matched" {
					matched++
				}
			}
		}
	}
	rep.InsufficientWindowCount = rep.WindowCount - rep.ValidWindowCount
	if eligible > 0 {
		rep.Aggregate.CoverageWeighted = float64(labeled) / float64(eligible)
	}
	if rep.ValidWindowCount > 0 {
		rep.Aggregate.DirectionRate = float64(matched) / float64(rep.ValidWindowCount)
	}
	for _, horizon := range horizons {
		rep.Aggregate.WindowEqualWeight = append(rep.Aggregate.WindowEqualWeight, aggregateFactorHorizon(windows, horizon, false))
		rep.Aggregate.ObservedWeighted = append(rep.Aggregate.ObservedWeighted, aggregateFactorHorizon(windows, horizon, true))
	}
	rep.Verdict = ValidationVerdictPassed
	check := func(name, expected, actual string, known, passes bool, quality bool) {
		status := "pass"
		switch {
		case !known || quality && !passes:
			status = "unknown"
			rep.Verdict = ValidationVerdictInsufficient
		case !passes:
			status = "fail"
			if rep.Verdict != ValidationVerdictInsufficient {
				rep.Verdict = ValidationVerdictFailed
			}
		}
		rep.Checks = append(rep.Checks, ValidationCheck{Name: name, Expected: expected, Actual: actual, Result: status})
	}
	check("有效窗口", fmt.Sprint(policy.MinValidWindows), fmt.Sprint(rep.ValidWindowCount), true, rep.ValidWindowCount >= policy.MinValidWindows, true)
	check("标签覆盖率", fmt.Sprintf("%.2f%%", policy.MinCoverage*100), fmt.Sprintf("%.2f%%", rep.Aggregate.CoverageWeighted*100),
		eligible > 0, rep.Aggregate.CoverageWeighted >= policy.MinCoverage, true)
	check("同向窗口比例", fmt.Sprintf("%.2f%%", policy.RequiredDirectionRate*100), fmt.Sprintf("%.2f%%", rep.Aggregate.DirectionRate*100),
		observed > 0, rep.Aggregate.DirectionRate >= policy.RequiredDirectionRate, false)
	if policy.RequireHistoricalUniverse {
		pool := req.ResearchProtocol.Universe
		check("历史成员股票池", universeHistoricalMembership, pool.Mode, true, pool.Mode == universeHistoricalMembership, true)
	}
	if policy.RequireVerifiedPIT {
		check("行情时点验证", pitVerified, inputs.Data.PITState, true, inputs.Data.PITState == pitVerified, true)
		pool := req.ResearchProtocol.Universe
		if pool.Mode == universeHistoricalMembership {
			check("成员时点验证", membershipPITVerified, pool.MembershipPIT, true, pool.MembershipPIT == membershipPITVerified, true)
		}
	}
	if policy.RequireTradableLabel {
		kind := req.ResearchProtocol.Labels.Kind
		check("标签时点", labelKindNextOpenHeldSessions, kind, true, kind == labelKindNextOpenHeldSessions, true)
	}
	if limit := policy.MaxICDecayRatio; limit != nil {
		aggregates := rep.Aggregate.WindowEqualWeight
		if policy.Aggregation == validationAggregationObservationEqual {
			aggregates = rep.Aggregate.ObservedWeighted
		}
		var ic *float64
		for _, agg := range aggregates {
			if agg.Horizon == req.Candidate.Evidence.Window {
				ic = agg.IC
			}
		}
		known := ic != nil && *ic != 0 && req.Candidate.Evidence.Stats.Pairs > 0
		actual := "缺少可计算的发现期或测试期 IC"
		ratio := 0.0
		if known {
			ratio = math.Abs(req.Candidate.Evidence.Stats.Mean / *ic)
			actual = fmt.Sprintf("%.4f", ratio)
		}
		check("IC 衰减倍数", fmt.Sprintf("%.4f", *limit), actual, known, ratio <= *limit, false)
	}
	rep.State = ValidationState(rep.Verdict)
	return rep
}

func aggregateFactorHorizon(windows []ValidationWindowReport, horizon int, weighted bool) HorizonAggregate {
	res := HorizonAggregate{Horizon: horizon}
	ic, spread, icWeight, spreadWeight := 0.0, 0.0, 0.0, 0.0
	for _, window := range windows {
		if window.State != ValidationWindowOK {
			continue
		}
		for _, h := range window.Horizons {
			if h.Horizon != horizon {
				continue
			}
			weight := 1.0
			if weighted {
				weight = float64(window.Observations)
			}
			if h.IC != nil {
				ic += *h.IC * weight
				icWeight += weight
			}
			if h.Spread != nil {
				spread += *h.Spread * weight
				spreadWeight += weight
			}
		}
	}
	if icWeight > 0 {
		v := ic / icWeight
		res.IC = &v
	}
	if spreadWeight > 0 {
		v := spread / spreadWeight
		res.Spread = &v
	}
	return res
}
