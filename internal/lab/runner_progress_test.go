package lab

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
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

// TestRunnerStopIdempotent 并发重复 Stop 不 panic（runControl.once 幂等
// close），且能正确关闭当前任务的 stop 通道。
func TestRunnerStopIdempotent(t *testing.T) {
	r := NewRunner()
	r.running.Store(true)
	ctl := &runControl{stop: make(chan struct{})}
	r.control.Store(ctl)
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r.Stop()
		}()
	}
	wg.Wait()
	select {
	case <-ctl.stop:
	default:
		t.Fatal("stop 通道应已关闭")
	}
}

// TestRecoverTaskPanic 任务 goroutine 的 panic 被 recover 后置 error 态并
// 记录错误消息，进程不被击穿。
func TestRecoverTaskPanic(t *testing.T) {
	r := NewRunner()
	func() {
		defer r.recoverTaskPanic()
		panic("boom")
	}()
	if st := *r.state.Load(); st != "error" {
		t.Fatalf("state = %s, want error", st)
	}
	if msg := r.errMsg.Load(); msg == nil || !strings.Contains(*msg, "boom") {
		t.Fatalf("errMsg = %v, 应包含 panic 值", msg)
	}
}
