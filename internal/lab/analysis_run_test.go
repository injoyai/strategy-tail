package lab

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	common "github.com/injoyai/strategy-tail"
	"github.com/injoyai/strategy-tail/lib/extend"
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
	rep, err := (&Runner{}).runAnalysis(cfg, make(chan struct{}))
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
	rep, err := (&Runner{}).runAnalysis(cfg, make(chan struct{}))
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

// TestSummarizeQuintiles 五组摘要纯函数合同：方向枚举、spread 与 null 语义。
func TestSummarizeQuintiles(t *testing.T) {
	// 严格递增
	s := summarizeQuintiles([]float64{-0.02, -0.01, 0, 0.01, 0.02})
	if s.Direction != "ascending" || !s.Monotonic {
		t.Fatalf("ascending: %+v", s)
	}
	if s.Spread == nil || !nearlyEq(*s.Spread, 0.04) {
		t.Fatalf("ascending spread = %+v", s.Spread)
	}
	// 严格递减
	s = summarizeQuintiles([]float64{0.02, 0.01, 0, -0.01, -0.02})
	if s.Direction != "descending" || !s.Monotonic {
		t.Fatalf("descending: %+v", s)
	}
	if s.Spread == nil || !nearlyEq(*s.Spread, -0.04) {
		t.Fatalf("descending spread = %+v", s.Spread)
	}
	// 混合
	s = summarizeQuintiles([]float64{0.01, -0.01, 0.02, 0, 0.01})
	if s.Direction != "mixed" || s.Monotonic {
		t.Fatalf("mixed: %+v", s)
	}
	// 全相等 → flat
	s = summarizeQuintiles([]float64{0.01, 0.01, 0.01, 0.01, 0.01})
	if s.Direction != "flat" || s.Monotonic {
		t.Fatalf("flat: %+v", s)
	}
	if s.Spread == nil || *s.Spread != 0 {
		t.Fatalf("flat spread = %+v", s.Spread)
	}
	// 不足五组 / NaN → insufficient，spread 为 null
	for _, q := range [][]float64{nil, {1, 2, 3}, {1, 2, 3, 4, math.NaN()}} {
		s = summarizeQuintiles(q)
		if s.Direction != "insufficient" || s.Spread != nil || s.Monotonic {
			t.Fatalf("insufficient %v: %+v", q, s)
		}
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
}
