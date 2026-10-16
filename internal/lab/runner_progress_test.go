package lab

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/injoyai/strategy-tail/internal/researchrun"
)

func TestBacktestStatusProgressByPhase(t *testing.T) {
	r := NewRunner()
	task, running := "backtest", "running"
	r.task.Store(&task)
	r.state.Store(&running)
	for _, p := range []researchrun.Progress{
		{Phase: "preparing"},
		{Phase: "loading", Done: 1, Total: 2, Code: "sh600001"},
		{Phase: "simulating", Done: 0, Total: 3},
		{Phase: "simulating", Done: 1, Total: 3, Code: "2025-01-02"},
		{Phase: "reporting"},
	} {
		r.setTaskProgress("backtest", "", p)
		st := r.Status()
		if st["phase"] != p.Phase || st["currentCode"] != p.Code {
			t.Fatalf("phase snapshot: %+v", st)
		}
		if p.Total == 0 {
			if st["done"] != -1 || st["total"] != -1 || st["progress"] != -1 {
				t.Fatalf("unknown progress must be indeterminate: %+v", st)
			}
		} else if st["done"] != p.Done || st["total"] != p.Total || st["progress"] != float64(p.Done)/float64(p.Total)*100 {
			t.Fatalf("phase counts: %+v", st)
		}
	}
	failed := "error"
	r.state.Store(&failed)
	if st := r.Status(); st["progress"] != -1 || st["phase"] != "reporting" {
		t.Fatalf("report failure must not claim completion: %+v", st)
	}
	done := "done"
	r.state.Store(&done)
	if st := r.Status(); st["progress"] != 100.0 {
		t.Fatalf("saved report must be complete: %+v", st)
	}
	// 下一项任务不得继承回测阶段。
	task = "analysis"
	r.task.Store(&task)
	if st := r.Status(); st["phase"] != nil {
		t.Fatalf("backtest phase leaked into analysis: %+v", st)
	}
}

func TestAnalysisProgressDoesNotFinishBeforeReportSave(t *testing.T) {
	for _, protocol := range []bool{false, true} {
		name := "v3"
		if protocol {
			name = "v4"
		}
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			setupAnalysisData(t, dir)
			writeV4Fixture(t, dir)
			cfg := AnalyzeConfig{RunConfig: RunConfig{StartYear: 2025, EndYear: 2025, SampleMode: "codes", SampleCodes: []string{"sh600001", "sh600002"}}, Kind: "momentum", Days: 2, Window: 1}
			if protocol {
				p := v4TestProtocol(1, 5)
				cfg.Protocol, cfg.Window = &p, 0
			}
			blocker := filepath.Join(dir, "blocked")
			if err := os.WriteFile(blocker, []byte("file"), 0600); err != nil {
				t.Fatal(err)
			}
			r := NewRunner()
			r.store = NewAnalysisStore(filepath.Join(blocker, "reports"))
			if err := r.StartAnalysis(cfg); err != nil {
				t.Fatal(err)
			}
			st := waitPortfolioDone(t, r)
			if st["state"] != "error" || st["phase"] != "reporting" || st["total"] != -1 || st["progress"] != -1 {
				t.Fatalf("save failure must not look like finished stock processing: %+v", st)
			}
			if st["doneCodes"] != int64(2) {
				t.Fatalf("fixture did not finish loading: %+v", st)
			}
		})
	}
}

func TestTaskProgressClearsCountsBetweenStages(t *testing.T) {
	for _, task := range []string{"analysis", taskPortfolioExperiment, taskPortfolioValidation} {
		r := NewRunner()
		r.task.Store(&task)
		running := "running"
		r.state.Store(&running)
		r.setTaskProgress(task, "stocks", researchrun.Progress{Phase: "observations", Done: 2, Total: 2, Code: "sh600001"})
		for _, phase := range []string{"ic", "grouping", "metrics", "attribution", "reporting", "finalize"} {
			r.setTaskProgress(task, "", researchrun.Progress{Phase: phase})
			if st := r.Status(); st["phase"] != phase || st["progress"] != -1 || st["total"] != -1 || st["currentCode"] != "" {
				t.Fatalf("%s/%s inherited completed counts: %+v", task, phase, st)
			}
		}
	}
}
