package lab

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"testing"
	"time"

	common "github.com/injoyai/strategy-tail"
	"github.com/injoyai/strategy-tail/lib/extend"
	f "github.com/injoyai/strategy-tail/strategies/factor"
)

func setupAnalysisData(t *testing.T, dir string) {
	t.Helper()
	t.Chdir(dir)
	old := common.Pull
	common.Pull = &extend.PullKline{Config: extend.PullKlineConfig{Dir: dir}}
	t.Cleanup(func() { common.Pull = old })
}

// TestRunAnalysis 升票动量恒正、降票恒负 → 每日横截面名次恒定 → IC 恒 1。
// 每票 2024-12-24 起连续 28 根日 K：2024 年 8 根垫底（his）、2025 年 20 根（dks），
// Window=1 → 19 个有效日 ≥ minPairs(10)。
func TestRunAnalysis(t *testing.T) {
	dir := t.TempDir()
	setupAnalysisData(t, dir)

	base := time.Date(2024, 12, 24, 0, 0, 0, 0, time.Local)
	up := append([]float64{9.5, 9.5, 9.5, 9.5, 9.5, 9.5, 9.5, 9.5},
		10, 10.2, 10.4, 10.6, 10.8, 11, 11.2, 11.4, 11.6, 11.8,
		12, 12.2, 12.4, 12.6, 12.8, 13, 13.2, 13.4, 13.6, 13.8)
	down := append([]float64{21, 21, 21, 21, 21, 21, 21, 21},
		20.8, 20.6, 20.4, 20.2, 20, 19.8, 19.6, 19.4, 19.2, 19,
		18.8, 18.6, 18.4, 18.2, 18, 17.8, 17.6, 17.4, 17.2, 17)
	writeDayDBCloses(t, dir, "sh600001", up, base)
	writeDayDBCloses(t, dir, "sh600002", down, base)

	cfg := AnalyzeConfig{
		RunConfig: RunConfig{StartYear: 2025, EndYear: 2025,
			SampleMode: "codes", SampleCodes: []string{"sh600001", "sh600002"},
			ScriptName: "matrix"},
		Kind: "momentum", Days: 2, Window: 1,
	}
	rep, err := (&Runner{}).runAnalysis("", cfg, make(chan struct{}))
	if err != nil {
		t.Fatalf("runAnalysis: %v", err)
	}
	if rep.FactorName != "N日动量(2)" {
		t.Fatalf("FactorName = %q", rep.FactorName)
	}
	if len(rep.Daily) != 19 {
		t.Fatalf("len(Daily) = %d, want 19", len(rep.Daily))
	}
	if rep.Stats.Pairs != 19 {
		t.Fatalf("Pairs = %d, want 19", rep.Stats.Pairs)
	}
	if !nearlyEq(rep.Stats.Mean, 1) {
		t.Fatalf("Mean = %v, want 1", rep.Stats.Mean)
	}
	if rep.Stats.Std != 0 {
		t.Fatalf("Std = %v, want 0", rep.Stats.Std)
	}
	if rep.Quintiles != nil {
		t.Fatalf("Quintiles = %v, want nil（每日 2 票不足 5 分位）", rep.Quintiles)
	}
	// v3 合同：版本号、因子快照（含实现版本）与分组回显
	if rep.AnalysisVersion != 3 {
		t.Fatalf("AnalysisVersion = %d, want 3", rep.AnalysisVersion)
	}
	if rep.Factor.Kind != "momentum" || rep.Factor.Name != "N日动量(2)" ||
		rep.Factor.Description != "近N日涨跌幅" || rep.Factor.ParameterLabel != "回看天数" ||
		rep.Factor.Days != 2 || rep.Factor.Unit != "ratio" {
		t.Fatalf("Factor = %+v", rep.Factor)
	}
	// 因子实现版本与 registry 当前版本一致
	if cur, _ := f.Catalog("momentum"); rep.Factor.ImplementationVersion != cur.ImplementationVersion ||
		rep.Factor.ImplementationVersion <= 0 {
		t.Fatalf("Factor.ImplementationVersion = %d, want registry %d",
			rep.Factor.ImplementationVersion, cur.ImplementationVersion)
	}
	if rep.Grouping.Mode != "" || len(rep.Grouping.Cuts) != 0 {
		t.Fatalf("Grouping = %+v, want 缺省等数量五组", rep.Grouping)
	}
	// Groups：每日 2 票按中点公式整块归组（§6.3 无最少票数门槛）→
	// 负动量票归 Q1、正动量票归 Q3，五组不完整；空组统计为 null。
	if len(rep.Groups) != 5 {
		t.Fatalf("len(Groups) = %d, want 5", len(rep.Groups))
	}
	for i, g := range rep.Groups {
		if g.Index != i+1 || g.Label != fmt.Sprintf("Q%d", i+1) {
			t.Fatalf("Groups[%d] 标识 = %d/%q", i, g.Index, g.Label)
		}
		if g.Lower != nil || g.Upper != nil {
			t.Fatalf("Groups[%d] 分位模式边界应为 null = %+v", i, g)
		}
	}
	q1, q3 := rep.Groups[0], rep.Groups[2]
	if q1.Observations != 19 || q1.Dates != 19 || !nearlyEq(q1.CountPct, 0.5) {
		t.Fatalf("Q1 计数 = %+v", q1)
	}
	if q1.FactorMean == nil || q1.ForwardReturn == nil || *q1.ForwardReturn >= 0 {
		t.Fatalf("Q1 = %+v, want 有因子统计且收益为负（下降票）", q1)
	}
	if q3.Observations != 19 || q3.Dates != 19 || !nearlyEq(q3.CountPct, 0.5) {
		t.Fatalf("Q3 计数 = %+v", q3)
	}
	if q3.FactorMean == nil || q3.ForwardReturn == nil || *q3.ForwardReturn <= 0 {
		t.Fatalf("Q3 = %+v, want 有因子统计且收益为正（上升票）", q3)
	}
	for _, i := range []int{1, 3, 4} {
		g := rep.Groups[i]
		if g.Observations != 0 || g.Dates != 0 || g.CountPct != 0 ||
			g.ForwardReturn != nil || g.FactorMin != nil || g.FactorMean != nil || g.FactorStd != nil {
			t.Fatalf("Groups[%d] 应为空组 = %+v", i, g)
		}
	}
	// Coverage：请求 2、完成 2、无跳过
	if rep.Coverage.Requested != 2 || rep.Coverage.Completed != 2 || rep.Coverage.Skipped != 0 {
		t.Fatalf("Coverage = %+v", rep.Coverage)
	}
	if len(rep.Coverage.Failures) != 0 {
		t.Fatalf("Failures = %+v", rep.Coverage.Failures)
	}
	// Summary：五组数据不足 → insufficient，spread 为 null
	if rep.Summary.Direction != "insufficient" || rep.Summary.Spread != nil || rep.Summary.Monotonic {
		t.Fatalf("Summary = %+v", rep.Summary)
	}
	// report.json 落盘并携带 coverage 与 summary
	buf, err := os.ReadFile(filepath.Join("output", "factor", "momentum", "report.json"))
	if err != nil {
		t.Fatalf("report.json 不存在: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(buf, &raw); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"coverage", "summary"} {
		if _, ok := raw[k]; !ok {
			t.Fatalf("report.json 缺字段 %s", k)
		}
	}
}

// TestRunAnalysisSkipped 一票数据缺失时报告记录 Skipped 与 Failure，不静默缩减样本。
func TestRunAnalysisSkipped(t *testing.T) {
	dir := t.TempDir()
	setupAnalysisData(t, dir)

	base := time.Date(2024, 12, 24, 0, 0, 0, 0, time.Local)
	up := append([]float64{9.5, 9.5, 9.5, 9.5, 9.5, 9.5, 9.5, 9.5},
		10, 10.2, 10.4, 10.6, 10.8, 11, 11.2, 11.4, 11.6, 11.8,
		12, 12.2, 12.4, 12.6, 12.8, 13, 13.2, 13.4, 13.6, 13.8)
	writeDayDBCloses(t, dir, "sh600001", up, base)
	// sh600003 无日 K 数据 → 加载失败跳过

	cfg := AnalyzeConfig{
		RunConfig: RunConfig{StartYear: 2025, EndYear: 2025,
			SampleMode: "codes", SampleCodes: []string{"sh600001", "sh600003"},
			ScriptName: "matrix"},
		Kind: "momentum", Days: 2, Window: 1,
	}
	rep, err := (&Runner{}).runAnalysis("", cfg, make(chan struct{}))
	if err != nil {
		t.Fatalf("runAnalysis: %v", err)
	}
	if rep.Coverage.Requested != 2 || rep.Coverage.Completed != 1 || rep.Coverage.Skipped != 1 {
		t.Fatalf("Coverage = %+v", rep.Coverage)
	}
	if len(rep.Coverage.Failures) != 1 ||
		rep.Coverage.Failures[0].Code != "sh600003" ||
		rep.Coverage.Failures[0].Stage == "" {
		t.Fatalf("Failures = %+v", rep.Coverage.Failures)
	}
}

// f64ptr/f64Eq 测试辅助：JSON null 语义的边界断言。
func f64ptr(v float64) *float64 { return &v }

func f64Eq(got, want *float64) bool {
	if got == nil || want == nil {
		return got == nil && want == nil
	}
	return nearlyEq(*got, *want)
}

// TestRunAnalysisBins 固定区间模式：五票各落一个区间，锁定标签、边界
// 开放端、断点值归左侧组、空 quintiles 与组统计口径。
func TestRunAnalysisBins(t *testing.T) {
	dir := t.TempDir()
	setupAnalysisData(t, dir)

	base := time.Date(2024, 12, 24, 0, 0, 0, 0, time.Local)
	mk := func(start, step float64) []float64 {
		cs := make([]float64, 28)
		for i := range cs {
			cs[i] = start + step*float64(i)
		}
		return cs
	}
	// 2 日动量 ≈ 2×step/(序列前值)，各票全程落在一个目标区间：
	writeDayDBCloses(t, dir, "sh600001", mk(10, 0.6), base)   // ≈0.048..0.088 → B5
	writeDayDBCloses(t, dir, "sh600002", mk(10, 0.1), base)   // ≈0.016..0.019 → B4
	writeDayDBCloses(t, dir, "sh600003", mk(10, 0), base)     // 恒 0，断点值归左侧组 → B3
	writeDayDBCloses(t, dir, "sh600004", mk(10, -0.12), base) // ≈-0.026..-0.034 → B2
	writeDayDBCloses(t, dir, "sh600005", mk(10, -0.35), base) // ≤-0.05 → B1

	cfg := AnalyzeConfig{
		RunConfig: RunConfig{StartYear: 2025, EndYear: 2025,
			SampleMode:  "codes",
			SampleCodes: []string{"sh600001", "sh600002", "sh600003", "sh600004", "sh600005"},
			ScriptName:  "matrix"},
		Kind: "momentum", Days: 2, Window: 1,
		Grouping: GroupingConfig{Mode: "bins", Cuts: []float64{-0.05, -0.02, 0, 0.02}},
	}
	rep, err := (&Runner{}).runAnalysis("", cfg, make(chan struct{}))
	if err != nil {
		t.Fatalf("runAnalysis: %v", err)
	}
	if rep.Grouping.Mode != "bins" || len(rep.Grouping.Cuts) != 4 {
		t.Fatalf("Grouping = %+v", rep.Grouping)
	}
	// 固定区间模式以 groups 为唯一分组结果，不伪造 quintiles
	if rep.Quintiles != nil {
		t.Fatalf("Quintiles = %v, want nil", rep.Quintiles)
	}
	wantBounds := [5][2]*float64{
		{nil, f64ptr(-0.05)}, {f64ptr(-0.05), f64ptr(-0.02)},
		{f64ptr(-0.02), f64ptr(0)}, {f64ptr(0), f64ptr(0.02)},
		{f64ptr(0.02), nil},
	}
	for i, g := range rep.Groups {
		if g.Label != fmt.Sprintf("B%d", i+1) {
			t.Fatalf("Groups[%d].Label = %q", i, g.Label)
		}
		if !f64Eq(g.Lower, wantBounds[i][0]) || !f64Eq(g.Upper, wantBounds[i][1]) {
			t.Fatalf("Groups[%d] 边界 = %v/%v, want %v/%v",
				i, g.Lower, g.Upper, wantBounds[i][0], wantBounds[i][1])
		}
		if g.Observations != 19 || g.Dates != 19 {
			t.Fatalf("Groups[%d] 计数 = %d/%d, want 19/19", i, g.Observations, g.Dates)
		}
		if !nearlyEq(g.CountPct, 0.2) {
			t.Fatalf("Groups[%d].CountPct = %v, want 0.2", i, g.CountPct)
		}
		if g.ForwardReturn == nil {
			t.Fatalf("Groups[%d].ForwardReturn = nil, want 有值", i)
		}
	}
	// 断点值归左侧组：恒 0 动量落在 B3，组内因子统计全为 0
	b3 := rep.Groups[2]
	if !nearlyEq(*b3.FactorMin, 0) || !nearlyEq(*b3.FactorMax, 0) || !nearlyEq(*b3.FactorMean, 0) {
		t.Fatalf("B3 因子统计 = %v/%v/%v, want 全 0", *b3.FactorMin, *b3.FactorMax, *b3.FactorMean)
	}
	// 五组收益 B1<B2<B3<B4<B5 → 摘要递增
	if rep.Summary.Direction != "ascending" || !rep.Summary.Monotonic {
		t.Fatalf("Summary = %+v", rep.Summary)
	}
}

// TestRunAnalysisSevenGroups 七组等频：7 票斜率互异 → 每日 7 个互异因子值，
// 七组完整；报告 Groups/Quintiles 镜像/年度 Quintiles 长度均为 7，
// CountPct≈1/7，组收益与斜率同向（ascending）。
func TestRunAnalysisSevenGroups(t *testing.T) {
	dir := t.TempDir()
	setupAnalysisData(t, dir)

	base := time.Date(2024, 12, 24, 0, 0, 0, 0, time.Local)
	mk := func(start, step float64) []float64 {
		cs := make([]float64, 28)
		for i := range cs {
			cs[i] = start + step*float64(i)
		}
		return cs
	}
	// 7 票斜率互异 → 2 日动量互异且与斜率同向：组序 = 斜率升序
	codes := []string{"sh600001", "sh600002", "sh600003", "sh600004", "sh600005", "sh600006", "sh600007"}
	steps := []float64{0.6, 0.45, 0.3, 0.15, 0, -0.15, -0.3}
	for i, c := range codes {
		writeDayDBCloses(t, dir, c, mk(10, steps[i]), base)
	}

	cfg := AnalyzeConfig{
		RunConfig: RunConfig{StartYear: 2025, EndYear: 2025,
			SampleMode: "codes", SampleCodes: codes, ScriptName: "matrix"},
		Kind: "momentum", Days: 2, Window: 1,
		Grouping: GroupingConfig{Mode: "quantile", Groups: 7},
	}
	rep, err := (&Runner{}).runAnalysis("", cfg, make(chan struct{}))
	if err != nil {
		t.Fatalf("runAnalysis: %v", err)
	}
	if rep.Grouping.Groups != 7 {
		t.Fatalf("Grouping.Groups = %d, want 7", rep.Grouping.Groups)
	}
	if len(rep.Groups) != 7 {
		t.Fatalf("len(Groups) = %d, want 7", len(rep.Groups))
	}
	for i, g := range rep.Groups {
		if g.Index != i+1 || g.Label != fmt.Sprintf("Q%d", i+1) {
			t.Fatalf("Groups[%d] 标识 = %d/%q", i, g.Index, g.Label)
		}
		if g.Observations != 19 || g.Dates != 19 {
			t.Fatalf("Groups[%d] 计数 = %d/%d, want 19/19", i, g.Observations, g.Dates)
		}
		if !nearlyEq(g.CountPct, 1.0/7) {
			t.Fatalf("Groups[%d].CountPct = %v, want 1/7", i, g.CountPct)
		}
		if g.ForwardReturn == nil || g.FactorMedian == nil {
			t.Fatalf("Groups[%d] 统计缺失: %+v", i, g)
		}
	}
	// 组收益与斜率同向：Q7（最大斜率）收益高于 Q1（最小斜率）
	if *rep.Groups[6].ForwardReturn <= *rep.Groups[0].ForwardReturn {
		t.Fatalf("Q7 收益应高于 Q1: %v vs %v", *rep.Groups[6].ForwardReturn, *rep.Groups[0].ForwardReturn)
	}
	// 兼容镜像与年度拆分同长度
	if rep.Quintiles == nil || len(rep.Quintiles) != 7 {
		t.Fatalf("Quintiles = %v, want 7 个收益值", rep.Quintiles)
	}
	if len(rep.Years) != 1 || len(rep.Years[0].Quintiles) != 7 {
		t.Fatalf("Years Quintiles 长度错误: %+v", rep.Years)
	}
	if rep.Summary.Direction != "ascending" || !rep.Summary.Monotonic {
		t.Fatalf("Summary = %+v, want ascending", rep.Summary)
	}
}

// TestRunAnalysisAllGroupings 全部组数预算：报告 allGroupings 覆盖 2-20 且
// 与单档请求结果完全一致（排序一次+线性扫描 vs quantileAssign 同序）；
// 年度 allGroupings 携带绘制真实数值轴所需的轻量统计；bins 模式不预算。
func TestRunAnalysisAllGroupings(t *testing.T) {
	dir := t.TempDir()
	setupAnalysisData(t, dir)

	base := time.Date(2024, 12, 24, 0, 0, 0, 0, time.Local)
	mk := func(start, step float64) []float64 {
		cs := make([]float64, 28)
		for i := range cs {
			cs[i] = start + step*float64(i)
		}
		return cs
	}
	codes := []string{"sh600001", "sh600002", "sh600003", "sh600004", "sh600005", "sh600006", "sh600007"}
	steps := []float64{0.6, 0.45, 0.3, 0.15, 0, -0.15, -0.3}
	for i, c := range codes {
		writeDayDBCloses(t, dir, c, mk(10, steps[i]), base)
	}

	cfg := AnalyzeConfig{
		RunConfig: RunConfig{StartYear: 2025, EndYear: 2025,
			SampleMode: "codes", SampleCodes: codes, ScriptName: "matrix"},
		Kind: "momentum", Days: 2, Window: 1,
		Grouping: GroupingConfig{Mode: "quantile", Groups: 7},
	}
	rep, err := (&Runner{}).runAnalysis("", cfg, make(chan struct{}))
	if err != nil {
		t.Fatalf("runAnalysis: %v", err)
	}
	// 全区间 allGroupings：覆盖 2..20，组数升序
	if len(rep.AllGroupings) != 19 {
		t.Fatalf("len(AllGroupings) = %d, want 19（2-20）", len(rep.AllGroupings))
	}
	for i, s := range rep.AllGroupings {
		if s.Groups != i+2 {
			t.Fatalf("AllGroupings[%d].Groups = %d, want %d", i, s.Groups, i+2)
		}
	}
	// 与单档请求（7 组）完全一致：收益/摘要逐组相等
	seven := rep.AllGroupings[7-2]
	if len(seven.Stats) != 7 || len(seven.Quintiles) != 7 {
		t.Fatalf("7 档 Stats/Quintiles 长度 = %d/%d, want 7/7", len(seven.Stats), len(seven.Quintiles))
	}
	for i, gs := range seven.Stats {
		if gs.ForwardReturn == nil || rep.Groups[i].ForwardReturn == nil ||
			!nearlyEq(*gs.ForwardReturn, *rep.Groups[i].ForwardReturn) {
			t.Fatalf("档 7 组 %d 收益 %v ≠ 单档 %v", i, gs.ForwardReturn, rep.Groups[i].ForwardReturn)
		}
		if gs.FactorMedian == nil || rep.Groups[i].FactorMedian == nil ||
			!nearlyEq(*gs.FactorMedian, *rep.Groups[i].FactorMedian) {
			t.Fatalf("档 7 组 %d 中位数 %v ≠ 单档 %v", i, gs.FactorMedian, rep.Groups[i].FactorMedian)
		}
	}
	for i := range seven.Quintiles {
		if !nearlyEq(seven.Quintiles[i], rep.Quintiles[i]) {
			t.Fatalf("档 7 quintiles[%d] = %v, want %v", i, seven.Quintiles[i], rep.Quintiles[i])
		}
	}
	if seven.Summary.Direction != rep.Summary.Direction {
		t.Fatalf("档 7 Summary = %+v, want %+v", seven.Summary, rep.Summary)
	}
	// 5 档也预算了
	five := rep.AllGroupings[5-2]
	if len(five.Stats) != 5 || len(five.Quintiles) != 5 {
		t.Fatalf("5 档 Stats/Quintiles 长度 = %d/%d, want 5/5", len(five.Stats), len(five.Quintiles))
	}
	// 年度 allGroupings：长度 19，并携带轻量真实值轴统计（min/mean/max）；
	// 不保存中位数等完整分布，以控制报告体积。收益与单档年度一致。
	ya := rep.Years[0].AllGroupings
	if len(ya) != 19 {
		t.Fatalf("年度 len(AllGroupings) = %d, want 19", len(ya))
	}
	ySeven := ya[7-2]
	if len(ySeven.Stats) != 7 {
		t.Fatalf("年度档 Stats 长度 = %d, want 7", len(ySeven.Stats))
	}
	for i, gs := range ySeven.Stats {
		if gs.FactorMin == nil || gs.FactorMean == nil || gs.FactorMax == nil || gs.ForwardReturn == nil {
			t.Fatalf("年度档组 %d 缺少数值轴统计: %+v", i, gs)
		}
		if gs.FactorMedian != nil || gs.FactorP25 != nil || gs.FactorP75 != nil || gs.FactorStd != nil {
			t.Fatalf("年度档组 %d 不应保存完整分布统计: %+v", i, gs)
		}
		if !nearlyEq(*gs.ForwardReturn, ySeven.Quintiles[i]) {
			t.Fatalf("年度档组 %d 收益 %v != quintile %v", i, *gs.ForwardReturn, ySeven.Quintiles[i])
		}
	}
	for i := range ySeven.Quintiles {
		if !nearlyEq(ySeven.Quintiles[i], rep.Years[0].Quintiles[i]) {
			t.Fatalf("年度档 7 quintiles[%d] = %v, want %v", i, ySeven.Quintiles[i], rep.Years[0].Quintiles[i])
		}
	}
	if ySeven.Summary.Direction != rep.Years[0].Summary.Direction {
		t.Fatalf("年度档 Summary = %+v, want %+v", ySeven.Summary, rep.Years[0].Summary)
	}
}

// TestRunAnalysisBinsNoAllGroupings bins 模式不预算全部组数（断点固定）。
func TestRunAnalysisBinsNoAllGroupings(t *testing.T) {
	dir := t.TempDir()
	setupAnalysisData(t, dir)

	base := time.Date(2024, 12, 24, 0, 0, 0, 0, time.Local)
	mk := func(start, step float64) []float64 {
		cs := make([]float64, 28)
		for i := range cs {
			cs[i] = start + step*float64(i)
		}
		return cs
	}
	codes := []string{"sh600001", "sh600002", "sh600003", "sh600004", "sh600005"}
	for i, step := range []float64{0.6, 0.1, 0, -0.12, -0.35} {
		writeDayDBCloses(t, dir, codes[i], mk(10, step), base)
	}

	cfg := AnalyzeConfig{
		RunConfig: RunConfig{StartYear: 2025, EndYear: 2025,
			SampleMode: "codes", SampleCodes: codes, ScriptName: "matrix"},
		Kind: "momentum", Days: 2, Window: 1,
		Grouping: GroupingConfig{Mode: "bins", Cuts: []float64{-0.05, -0.02, 0, 0.02}},
	}
	rep, err := (&Runner{}).runAnalysis("", cfg, make(chan struct{}))
	if err != nil {
		t.Fatalf("runAnalysis: %v", err)
	}
	if rep.AllGroupings != nil {
		t.Fatalf("bins 模式 AllGroupings = %v, want nil", rep.AllGroupings)
	}
	for _, y := range rep.Years {
		if y.AllGroupings != nil {
			t.Fatalf("bins 模式年度 AllGroupings = %v, want nil", y.AllGroupings)
		}
		if len(y.Groups) != 5 {
			t.Fatalf("bins 模式年度 Groups 长度 = %d, want 5", len(y.Groups))
		}
		for i, gs := range y.Groups {
			if gs.Observations > 0 && (gs.FactorMin == nil || gs.FactorMean == nil || gs.FactorMax == nil) {
				t.Fatalf("bins 模式年度组 %d 缺少数值范围: %+v", i, gs)
			}
		}
	}
}

// TestRunAnalysisAllEqual 全部股票因子值相同 → 整体一个并列块归入中间组，
// 不强拆成看似有收益差异的五组；摘要 insufficient。
func TestRunAnalysisAllEqual(t *testing.T) {
	dir := t.TempDir()
	setupAnalysisData(t, dir)

	base := time.Date(2024, 12, 24, 0, 0, 0, 0, time.Local)
	flat := make([]float64, 28)
	for i := range flat {
		flat[i] = 10
	}
	codes := []string{"sh600001", "sh600002", "sh600003", "sh600004", "sh600005"}
	for _, c := range codes {
		writeDayDBCloses(t, dir, c, flat, base)
	}

	cfg := AnalyzeConfig{
		RunConfig: RunConfig{StartYear: 2025, EndYear: 2025,
			SampleMode: "codes", SampleCodes: codes, ScriptName: "matrix"},
		Kind: "momentum", Days: 2, Window: 1,
	}
	rep, err := (&Runner{}).runAnalysis("", cfg, make(chan struct{}))
	if err != nil {
		t.Fatalf("runAnalysis: %v", err)
	}
	if rep.Quintiles != nil {
		t.Fatalf("Quintiles = %v, want nil", rep.Quintiles)
	}
	if rep.Summary.Direction != "insufficient" || rep.Summary.Spread != nil {
		t.Fatalf("Summary = %+v", rep.Summary)
	}
	total := 0
	for i, g := range rep.Groups {
		total += g.Observations
		if i != 2 && (g.Observations != 0 || g.ForwardReturn != nil) {
			t.Fatalf("Groups[%d] 应为空组 = %+v", i, g)
		}
	}
	if total != 19*5 {
		t.Fatalf("总观测 = %d, want %d", total, 19*5)
	}
	mid := rep.Groups[2]
	if mid.Observations != 19*5 || mid.ForwardReturn == nil {
		t.Fatalf("Q3 = %+v, want 承载全部观测", mid)
	}
	if !nearlyEq(*mid.ForwardReturn, 0) {
		t.Fatalf("Q3.ForwardReturn = %v, want 0", *mid.ForwardReturn)
	}
}

// TestRunAnalysisStopped 停止信号立即中断分析并返回 errStopped。
func TestRunAnalysisStopped(t *testing.T) {
	dir := t.TempDir()
	setupAnalysisData(t, dir)

	base := time.Date(2024, 12, 24, 0, 0, 0, 0, time.Local)
	up := append([]float64{9.5, 9.5, 9.5, 9.5, 9.5, 9.5, 9.5, 9.5},
		10, 10.2, 10.4, 10.6, 10.8, 11, 11.2, 11.4, 11.6, 11.8,
		12, 12.2, 12.4, 12.6, 12.8, 13, 13.2, 13.4, 13.6, 13.8)
	writeDayDBCloses(t, dir, "sh600001", up, base)

	cfg := AnalyzeConfig{
		RunConfig: RunConfig{StartYear: 2025, EndYear: 2025,
			SampleMode: "codes", SampleCodes: []string{"sh600001"}, ScriptName: "matrix"},
		Kind: "momentum", Days: 2, Window: 1,
	}
	stop := make(chan struct{})
	close(stop)
	_, err := (&Runner{}).runAnalysis("", cfg, stop)
	if !errors.Is(err, errStopped) {
		t.Fatalf("err = %v, want errStopped", err)
	}
}

// TestSummarizeQuintiles 分组摘要纯函数合同：方向枚举、spread 与 null 语义。
func TestSummarizeQuintiles(t *testing.T) {
	// 严格递增
	s := summarizeQuintiles([]float64{-0.02, -0.01, 0, 0.01, 0.02}, 5)
	if s.Direction != "ascending" || !s.Monotonic {
		t.Fatalf("ascending: %+v", s)
	}
	if s.Spread == nil || !nearlyEq(*s.Spread, 0.04) {
		t.Fatalf("ascending spread = %+v", s.Spread)
	}
	// 严格递减
	s = summarizeQuintiles([]float64{0.02, 0.01, 0, -0.01, -0.02}, 5)
	if s.Direction != "descending" || !s.Monotonic {
		t.Fatalf("descending: %+v", s)
	}
	if s.Spread == nil || !nearlyEq(*s.Spread, -0.04) {
		t.Fatalf("descending spread = %+v", s.Spread)
	}
	// 混合
	s = summarizeQuintiles([]float64{0.01, -0.01, 0.02, 0, 0.01}, 5)
	if s.Direction != "mixed" || s.Monotonic {
		t.Fatalf("mixed: %+v", s)
	}
	// 全相等 → flat
	s = summarizeQuintiles([]float64{0.01, 0.01, 0.01, 0.01, 0.01}, 5)
	if s.Direction != "flat" || s.Monotonic {
		t.Fatalf("flat: %+v", s)
	}
	if s.Spread == nil || *s.Spread != 0 {
		t.Fatalf("flat spread = %+v", s.Spread)
	}
	// 不足五组 / NaN → insufficient，spread 为 null
	for _, q := range [][]float64{nil, {1, 2, 3}, {1, 2, 3, 4, math.NaN()}} {
		s = summarizeQuintiles(q, 5)
		if s.Direction != "insufficient" || s.Spread != nil || s.Monotonic {
			t.Fatalf("insufficient %v: %+v", q, s)
		}
	}
	// 7 组：严格递增，spread 为首末差
	s = summarizeQuintiles([]float64{-0.03, -0.02, -0.01, 0, 0.01, 0.02, 0.03}, 7)
	if s.Direction != "ascending" || !s.Monotonic || s.Spread == nil || !nearlyEq(*s.Spread, 0.06) {
		t.Fatalf("7 组 ascending: %+v", s)
	}
	// 组数不足 7 → insufficient
	s = summarizeQuintiles([]float64{1, 2, 3}, 7)
	if s.Direction != "insufficient" || s.Spread != nil {
		t.Fatalf("7 组不足: %+v", s)
	}
}

func TestAnalyzeConfigValidate(t *testing.T) {
	base := AnalyzeConfig{RunConfig: RunConfig{StartYear: 2025, EndYear: 2025,
		SampleMode: "all", ScriptName: "matrix"}, Kind: "momentum", Days: 2, Window: 1}
	if err := base.Validate(); err != nil {
		t.Fatalf("base: %v", err)
	}
	w0 := base
	w0.Window = 0
	if err := w0.Validate(); err == nil {
		t.Fatal("Window=0 应报错")
	}
	badKind := base
	badKind.Kind = "nope"
	if err := badKind.Validate(); err == nil {
		t.Fatal("未知 Kind 应报错")
	}
	empty := base
	empty.SampleMode = "codes"
	empty.SampleCodes = nil
	if err := empty.Validate(); err == nil {
		t.Fatal("codes 空样本应报错")
	}
	bins := base
	bins.Grouping = GroupingConfig{Mode: "bins", Cuts: []float64{-0.05, -0.02, 0, 0.02}}
	if err := bins.Validate(); err != nil {
		t.Fatalf("合法 bins: %v", err)
	}
	badCuts := base
	badCuts.Grouping = GroupingConfig{Mode: "bins", Cuts: []float64{0.02, -0.02}}
	if err := badCuts.Validate(); err == nil {
		t.Fatal("非法断点应报错")
	}
	quantileCuts := base
	quantileCuts.Grouping = GroupingConfig{Cuts: []float64{0, 0, 0, 0}}
	if err := quantileCuts.Validate(); err == nil {
		t.Fatal("分位模式带断点应报错")
	}
	badMode := base
	badMode.Grouping = GroupingConfig{Mode: "median"}
	if err := badMode.Validate(); err == nil {
		t.Fatal("未知分组模式应报错")
	}
	seven := base
	seven.Grouping = GroupingConfig{Groups: 7}
	if err := seven.Validate(); err != nil {
		t.Fatalf("7 组等频应合法: %v", err)
	}
	badGroups := base
	badGroups.Grouping = GroupingConfig{Groups: 1}
	if err := badGroups.Validate(); err == nil {
		t.Fatal("分组数 1 应报错")
	}
	bins7 := base
	bins7.Grouping = GroupingConfig{Mode: "bins", Groups: 7, Cuts: []float64{1, 2, 3, 4}}
	if err := bins7.Validate(); err == nil {
		t.Fatal("7 组 bins 需 6 个断点，4 个应报错")
	}
}

// TestRunAnalysisMultiYearCoverage 一只股票只有部分年份有数据时，其他年份仍参与分析：
// 缺失年份只进入 YearCoverage 失败明细，整票覆盖率不被抹掉；报告同时保存配置区间、
// 实际有效日期与年度稳定性拆分，且全区间与年度来自同一批逐日观测（交易日等权）。
func TestRunAnalysisMultiYearCoverage(t *testing.T) {
	dir := t.TempDir()
	setupAnalysisData(t, dir)

	now := time.Now().Year()
	base := time.Date(now-1, 12, 24, 0, 0, 0, 0, time.Local)
	mk := func(start, step float64) []float64 {
		cs := make([]float64, 28)
		for i := range cs {
			cs[i] = start + step*float64(i)
		}
		return cs
	}
	// 5 票不同斜率：每日因子值互不相同 → 五组完整，组收益随因子值单调递增
	codes := []string{"sh600001", "sh600002", "sh600003", "sh600004", "sh600005"}
	for i, step := range []float64{0.6, 0.1, 0, -0.12, -0.35} {
		writeDayDBCloses(t, dir, codes[i], mk(10, step), base)
	}

	cfg := AnalyzeConfig{
		RunConfig: RunConfig{StartYear: now - 2, EndYear: now,
			SampleMode: "codes", SampleCodes: codes, ScriptName: "matrix"},
		Kind: "momentum", Days: 2, Window: 1,
	}
	rep, err := (&Runner{}).runAnalysis("", cfg, make(chan struct{}))
	if err != nil {
		t.Fatalf("runAnalysis: %v", err)
	}
	// 配置区间与样本口径：报告必须自证请求范围
	if rep.Range.StartYear != now-2 || rep.Range.EndYear != now ||
		rep.Range.SampleMode != "codes" || rep.Range.SampleSize != len(codes) {
		t.Fatalf("Range = %+v", rep.Range)
	}
	// 实际有效日期来自成功生成标签的首末交易日：now-1 年首日为 12/26（前两根预热），
	// now 年尾部 Window=1 剔除最后一根 → 末日为基准第 26 天
	if rep.FirstDataDate != base.AddDate(0, 0, 2).Format("2006-01-02") ||
		rep.LastDataDate != base.AddDate(0, 0, 26).Format("2006-01-02") {
		t.Fatalf("有效日期 = %q–%q", rep.FirstDataDate, rep.LastDataDate)
	}
	// 年度拆分：now-2 无数据 → 只剩 now-1 与 now，升序
	if len(rep.Years) != 2 || rep.Years[0].Year != now-1 || rep.Years[1].Year != now {
		t.Fatalf("Years = %+v", rep.Years)
	}
	// 整票覆盖率：每票至少一个成功年份 → 无跳过、无整票失败
	if rep.Coverage.Requested != len(codes) || rep.Coverage.Completed != len(codes) ||
		rep.Coverage.Skipped != 0 || len(rep.Coverage.Failures) != 0 {
		t.Fatalf("Coverage = %+v", rep.Coverage)
	}
	// 逐年覆盖率：缺失年份必须显式披露，而不是静默作废整只股票
	yc := rep.YearCoverage
	if yc.RequestedCodes != len(codes) || yc.CompletedCodes != len(codes) ||
		yc.RequestedCodeYears != len(codes)*3 || yc.CompletedCodeYears != len(codes)*2 ||
		yc.SkippedCodeYears != len(codes) || len(yc.Failures) != len(codes) {
		t.Fatalf("YearCoverage = %+v", yc)
	}
	for _, f := range yc.Failures {
		if f.Year != now-2 || f.Code == "" || f.Stage == "" || f.Message == "" {
			t.Fatalf("Failure = %+v, want now-2 缺数据", f)
		}
	}
	// 年度口径：now-1（12/24–12/31 共 8 根）只有 5 个有效日 → 保留样本数并由 Pairs 表达不足
	if rep.Years[0].TradingDays != 5 || rep.Years[0].Stats.Pairs != 5 {
		t.Fatalf("Years[0] 计数 = %+v", rep.Years[0])
	}
	if rep.Years[0].Stats.Mean != 0 || rep.Years[0].Stats.Std != 0 {
		t.Fatalf("Years[0] 有效日不足应汇总 0: %+v", rep.Years[0].Stats)
	}
	// 年度五组与全区间同口径：五组完整且方向一致（不因有效日少而强行给方向）
	if len(rep.Years[0].Quintiles) != 5 || rep.Years[0].Summary.Direction != "ascending" {
		t.Fatalf("Years[0] = %+v", rep.Years[0])
	}
	if rep.Years[1].TradingDays != 19 || rep.Years[1].Stats.Pairs != 19 {
		t.Fatalf("Years[1] 计数 = %+v", rep.Years[1])
	}
	if len(rep.Years[1].Quintiles) != 5 || rep.Years[1].Summary.Direction != "ascending" {
		t.Fatalf("Years[1] = %+v", rep.Years[1])
	}
	// 全区间有效日 = 各年度有效日之和（同一批观测，不重复加权）
	if rep.Stats.Pairs != 24 {
		t.Fatalf("全区间 Pairs = %d, want 24", rep.Stats.Pairs)
	}
	// 落盘报告携带新增字段（前端与历史文件按字段存在性降级）
	buf, err := os.ReadFile(filepath.Join("output", "factor", "momentum", "report.json"))
	if err != nil {
		t.Fatalf("report.json 不存在: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(buf, &raw); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"range", "years", "yearCoverage", "firstDataDate", "lastDataDate"} {
		if _, ok := raw[k]; !ok {
			t.Fatalf("report.json 缺字段 %s", k)
		}
	}
}

// v4TestProtocol 构造可直接驱动 runAnalysisV4 的协议：股票池走 codes 静态
// 模式（Runner{} 无 universe store 也可运行），Horizons 按测试需要指定。
func v4TestProtocol(horizons ...int) ResearchProtocol {
	p := validResearchProtocol()
	p.Universe.Mode = universeCodes
	p.Universe.ID = ""
	p.Labels.Horizons = horizons
	return p
}

// writeV4Fixture 一升一降两票：收盘线性（动量名次每日恒定 → Spearman IC
// 恒 ±1），开盘=昨收×0.99（跳空低开 1%）使 next-open 标签可观测地不同于
// legacy close 标签。
func writeV4Fixture(t *testing.T, dir string) {
	t.Helper()
	base := time.Date(2024, 12, 24, 0, 0, 0, 0, time.Local)
	up := append([]float64{9.5, 9.5, 9.5, 9.5, 9.5, 9.5, 9.5, 9.5},
		10, 10.2, 10.4, 10.6, 10.8, 11, 11.2, 11.4, 11.6, 11.8,
		12, 12.2, 12.4, 12.6, 12.8, 13, 13.2, 13.4, 13.6, 13.8)
	down := append([]float64{21, 21, 21, 21, 21, 21, 21, 21},
		20.8, 20.6, 20.4, 20.2, 20, 19.8, 19.6, 19.4, 19.2, 19,
		18.8, 18.6, 18.4, 18.2, 18, 17.8, 17.6, 17.4, 17.2, 17)
	gapOpen := func(cs []float64) []float64 {
		os := make([]float64, len(cs))
		os[0] = cs[0]
		for i := 1; i < len(cs); i++ {
			os[i] = cs[i-1] * 0.99
		}
		return os
	}
	writeDayDBOHLC(t, dir, "sh600001", gapOpen(up), up, base)
	writeDayDBOHLC(t, dir, "sh600002", gapOpen(down), down, base)
}

// TestRunAnalysisV4 v4 端到端：多 Horizon IC、可交易标签覆盖、年度/全区间
// 同口径、衰减曲线与兼容镜像，以及默认 store 落盘后跨实例恢复。
// 数据 20 根 dks 无未来缓冲 → h 的有效信号日 = 20-h（exit=i+h 越界计入
// insufficientHorizon），h=1/5/10 → Pairs 19/15/10。
func TestRunAnalysisV4(t *testing.T) {
	dir := t.TempDir()
	setupAnalysisData(t, dir)
	writeV4Fixture(t, dir)

	p := v4TestProtocol(1, 5, 10)
	cfg := AnalyzeConfig{
		RunConfig: RunConfig{StartYear: 2025, EndYear: 2025,
			SampleMode: "codes", SampleCodes: []string{"sh600001", "sh600002"},
			ScriptName: "matrix"},
		Kind: "momentum", Days: 2,
		Protocol: &p,
	}
	rep, err := (&Runner{}).runAnalysis("", cfg, make(chan struct{}))
	if err != nil {
		t.Fatalf("runAnalysis: %v", err)
	}

	// v4 合同头：版本、证据等级与协议绑定
	if rep.AnalysisVersion != 4 {
		t.Fatalf("AnalysisVersion = %d, want 4", rep.AnalysisVersion)
	}
	if rep.EvidenceClass != "retrospective" {
		t.Fatalf("EvidenceClass = %q, want retrospective（PIT/复权/标签全部已验证）", rep.EvidenceClass)
	}
	if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(rep.ProtocolHash) {
		t.Fatalf("ProtocolHash = %q, want 64 位十六进制", rep.ProtocolHash)
	}
	if rep.Protocol == nil ||
		!reflect.DeepEqual(rep.Protocol.Labels.Horizons, []int{1, 5, 10}) ||
		rep.Protocol.Labels.Kind != labelKindNextOpenToClose {
		t.Fatalf("Protocol 回显异常: %+v", rep.Protocol)
	}

	// 主周期镜像（deprecated 字段来自 Horizons[0]=1）
	if rep.Window != 1 {
		t.Fatalf("Window = %d, want 1（镜像主周期）", rep.Window)
	}
	if rep.Stats.Pairs != 19 || !nearlyEq(rep.Stats.Mean, 1) || rep.Stats.Std != 0 {
		t.Fatalf("主周期 Stats = %+v", rep.Stats)
	}
	if rep.Quintiles != nil {
		t.Fatalf("Quintiles = %v, want nil（每日 2 票组不完整）", rep.Quintiles)
	}
	if len(rep.Daily) != 19 {
		t.Fatalf("len(Daily) = %d, want 19", len(rep.Daily))
	}

	// 各 Horizon：方向已知（升票收益恒高）→ IC 恒 1；恒定序列无波动与显著性
	wantPairs := map[int]int{1: 19, 5: 15, 10: 10}
	if len(rep.HorizonAnalyses) != 3 {
		t.Fatalf("len(HorizonAnalyses) = %d, want 3", len(rep.HorizonAnalyses))
	}
	hs := []int{1, 5, 10}
	for i, ha := range rep.HorizonAnalyses {
		h := hs[i]
		if ha.Horizon != h || ha.Label != labelKindNextOpenToClose {
			t.Fatalf("HorizonAnalyses[%d] = %d/%q, want %d/%s", i, ha.Horizon, ha.Label, h, labelKindNextOpenToClose)
		}
		ic := ha.IC
		if ic.Pairs != wantPairs[h] {
			t.Fatalf("h=%d Pairs = %d, want %d", h, ic.Pairs, wantPairs[h])
		}
		if ic.Mean == nil || !nearlyEq(*ic.Mean, 1) ||
			ic.PositiveRate == nil || !nearlyEq(*ic.PositiveRate, 1) ||
			ic.DirectionConsistentRate == nil || !nearlyEq(*ic.DirectionConsistentRate, 1) {
			t.Fatalf("h=%d IC 方向统计 = %+v, want 全 1", h, ic)
		}
		if ic.Std == nil || *ic.Std != 0 {
			t.Fatalf("h=%d Std = %v, want 0（每日 IC 恒定）", h, ic.Std)
		}
		if ic.ICIR != nil || ic.AnnualizedICIR != nil || ic.NaiveTStat != nil ||
			ic.HACTStat != nil || ic.PValue != nil {
			t.Fatalf("h=%d 恒定 IC 不应有波动/显著性统计: %+v", h, ic)
		}
		if ic.HACLag != h-1 { // horizon_minus_one → max(h-1,0)，h=1 时为 0
			t.Fatalf("h=%d HACLag = %d, want %d", h, ic.HACLag, h-1)
		}
		// 标签覆盖（按股票池聚合）：2 票 × 20 个合格信号日，尾部 h 日 horizon 不足
		cov := ha.LabelCoverage
		if cov.EligibleSignals != 40 || cov.LabeledSignals != 2*(20-h) ||
			cov.InsufficientHorizon != 2*h || cov.MissingEntryPrice != 0 ||
			cov.MissingExitPrice != 0 || cov.NonFiniteReturn != 0 {
			t.Fatalf("h=%d LabelCoverage = %+v", h, cov)
		}
		// 年度拆分与全区间同口径
		if len(ha.Years) != 1 || ha.Years[0].Year != 2025 ||
			ha.Years[0].IC.Pairs != wantPairs[h] || ha.Years[0].TradingDays != wantPairs[h] {
			t.Fatalf("h=%d 年度拆分 = %+v", h, ha.Years)
		}
	}

	// HorizonDaily：键齐全、逐日 IC 恒 1
	if len(rep.HorizonDaily) != 3 {
		t.Fatalf("len(HorizonDaily) = %d, want 3", len(rep.HorizonDaily))
	}
	for h, n := range wantPairs {
		if len(rep.HorizonDaily[h]) != n {
			t.Fatalf("len(HorizonDaily[%d]) = %d, want %d", h, len(rep.HorizonDaily[h]), n)
		}
		for _, pt := range rep.HorizonDaily[h] {
			if pt.IC == nil || !nearlyEq(*pt.IC, 1) {
				t.Fatalf("HorizonDaily[%d] 逐日 IC = %+v, want 1", h, pt)
			}
		}
	}

	// 衰减曲线：Horizon 升序、MeanIC 恒 1、无 HAC/Spread（组不完整）
	if len(rep.DecayCurve) != 3 {
		t.Fatalf("len(DecayCurve) = %d, want 3", len(rep.DecayCurve))
	}
	for i, dp := range rep.DecayCurve {
		if dp.Horizon != hs[i] || dp.MeanIC == nil || !nearlyEq(*dp.MeanIC, 1) ||
			dp.HACT != nil || dp.Spread != nil {
			t.Fatalf("DecayCurve[%d] = %+v", i, dp)
		}
	}

	// 年度镜像与分组方向：折价开盘不改变名次 → Q3 正收益、Q1 负收益
	if len(rep.Years) != 1 || rep.Years[0].Year != 2025 || rep.Years[0].Stats.Pairs != 19 {
		t.Fatalf("Years = %+v", rep.Years)
	}
	if len(rep.Groups) != 5 {
		t.Fatalf("len(Groups) = %d, want 5", len(rep.Groups))
	}
	if g := rep.Groups[2]; g.Observations != 19 || g.ForwardReturn == nil || *g.ForwardReturn <= 0 {
		t.Fatalf("Q3 = %+v, want 19 观测且 next-open 收益为正", g)
	}
	if g := rep.Groups[0]; g.Observations != 19 || g.ForwardReturn == nil || *g.ForwardReturn >= 0 {
		t.Fatalf("Q1 = %+v, want 19 观测且 next-open 收益为负", g)
	}

	// 有效日期与覆盖
	if rep.FirstDataDate != "2025-01-01" || rep.LastDataDate != "2025-01-19" {
		t.Fatalf("有效日期 = %q–%q", rep.FirstDataDate, rep.LastDataDate)
	}
	if rep.Coverage.Requested != 2 || rep.Coverage.Completed != 2 || rep.Coverage.Skipped != 0 {
		t.Fatalf("Coverage = %+v", rep.Coverage)
	}

	// 重启恢复：默认 store 落盘于 output/factor，跨实例 Get/Latest 读回
	s := NewAnalysisStore(filepath.Join("output", "factor"))
	got, err := s.Get(rep.AnalysisID)
	if err != nil {
		t.Fatalf("恢复 v4 报告: %v", err)
	}
	if got.AnalysisVersion != 4 || got.ProtocolHash != rep.ProtocolHash ||
		got.EvidenceClass != "retrospective" || !reflect.DeepEqual(got.Horizons, rep.Horizons) ||
		len(got.HorizonDaily[5]) != 15 {
		t.Fatalf("恢复的报告不一致: version=%d hash=%q class=%q horizons=%v hd5=%d",
			got.AnalysisVersion, got.ProtocolHash, got.EvidenceClass, got.Horizons, len(got.HorizonDaily[5]))
	}
	if lat, err := s.Latest("momentum"); err != nil || lat.AnalysisID != rep.AnalysisID {
		t.Fatalf("Latest = %v (err=%v), want %q", lat, err, rep.AnalysisID)
	}
}

// TestRunAnalysisV4NextOpenVsLegacy 同一份数据分别跑 v4（next-open 标签）与
// v3（legacy close 标签）：跳空低开 1% 使入场成本降低，两组收益按
// (1+r)/0.99−1 恒等式一致上移，必须可观测地不同。
func TestRunAnalysisV4NextOpenVsLegacy(t *testing.T) {
	dir := t.TempDir()
	setupAnalysisData(t, dir)
	writeV4Fixture(t, dir)

	rc := RunConfig{StartYear: 2025, EndYear: 2025,
		SampleMode: "codes", SampleCodes: []string{"sh600001", "sh600002"},
		ScriptName: "matrix"}
	p := v4TestProtocol(1)
	v4, err := (&Runner{}).runAnalysis("", AnalyzeConfig{
		RunConfig: rc, Kind: "momentum", Days: 2, Protocol: &p}, make(chan struct{}))
	if err != nil {
		t.Fatalf("v4: %v", err)
	}
	v3, err := (&Runner{}).runAnalysis("", AnalyzeConfig{
		RunConfig: rc, Kind: "momentum", Days: 2, Window: 1}, make(chan struct{}))
	if err != nil {
		t.Fatalf("v3: %v", err)
	}
	if v4.HorizonAnalyses[0].Label != labelKindNextOpenToClose {
		t.Fatalf("v4 标签 = %q", v4.HorizonAnalyses[0].Label)
	}
	q1v4, q3v4 := v4.Groups[0].ForwardReturn, v4.Groups[2].ForwardReturn
	q1v3, q3v3 := v3.Groups[0].ForwardReturn, v3.Groups[2].ForwardReturn
	if q1v4 == nil || q3v4 == nil || q1v3 == nil || q3v3 == nil {
		t.Fatalf("组收益缺失: v4 %v/%v v3 %v/%v", q1v4, q3v4, q1v3, q3v3)
	}
	// 折价开盘：每期 v4 收益 = c[i+h]/(0.99·c[i]) − 1 = (1+v3)/0.99 − 1，
	// 线性聚合下组均值满足同一恒等式 → 两组收益一致上移约 +1.01%。价格按
	// 毫元量化（Yuan=Price(f*1000)），容差取 1e-3（噪声 ≤6e-5，错标签 ~0.01）。
	for _, g := range []struct{ name string; v4, v3 float64 }{
		{"Q1", *q1v4, *q1v3}, {"Q3", *q3v4, *q3v3},
	} {
		want := (g.v3 + 1) / 0.99 - 1
		if math.Abs(g.v4-want) > 1e-3 {
			t.Fatalf("%s next-open %v 应等于 (1+legacy)/0.99−1 = %v", g.name, g.v4, want)
		}
	}
}

// TestRunAnalysisV4Exploratory current_static 股票池 → 报告必须标记
// exploratory；证据等级只影响解释、不改变计算（IC 仍完整）。
func TestRunAnalysisV4Exploratory(t *testing.T) {
	dir := t.TempDir()
	setupAnalysisData(t, dir)
	writeV4Fixture(t, dir)

	p := v4TestProtocol(1)
	p.Universe.Mode = universeCurrentStatic
	p.Universe.ID = "static-all"
	cfg := AnalyzeConfig{
		RunConfig: RunConfig{StartYear: 2025, EndYear: 2025,
			SampleMode: "codes", SampleCodes: []string{"sh600001", "sh600002"},
			ScriptName: "matrix"},
		Kind: "momentum", Days: 2,
		Protocol: &p,
	}
	rep, err := (&Runner{}).runAnalysis("", cfg, make(chan struct{}))
	if err != nil {
		t.Fatalf("runAnalysis: %v", err)
	}
	if rep.EvidenceClass != "exploratory" {
		t.Fatalf("EvidenceClass = %q, want exploratory（current_static）", rep.EvidenceClass)
	}
	if rep.ProtocolHash == "" || rep.Stats.Pairs != 19 || !nearlyEq(rep.Stats.Mean, 1) {
		t.Fatalf("计算不受证据等级影响: hash=%q stats=%+v", rep.ProtocolHash, rep.Stats)
	}
}
