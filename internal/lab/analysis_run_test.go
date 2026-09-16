package lab

import (
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
	if _, err := os.Stat(filepath.Join("output", "factor", "momentum", "report.json")); err != nil {
		t.Fatalf("report.json 不存在: %v", err)
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
