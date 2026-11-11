package lab

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/injoyai/strategy-tail/internal/researchrun"
)

func TestTaskSwitchDoesNotLeakPreviousRun(t *testing.T) {
	for _, task := range []string{taskPortfolioExperiment, taskPortfolioValidation} {
		t.Run(task, func(t *testing.T) {
			r := NewRunner()
			cfg := RunConfig{StartYear: 2025, EndYear: 2025, SampleMode: "codes", SampleCodes: []string{"sh600000"}}
			r.lastRun.Store(&cfg)
			oldError, oldCode := "previous failure", "sh600000"
			r.errMsg.Store(&oldError)
			r.currentCode.Store(&oldCode)
			r.doneCodes.Store(10)
			r.totalCodes.Store(10)
			r.setTaskProgress("backtest", "stocks", researchrun.Progress{Phase: "reporting", Done: 10, Total: 10})
			report, analysis := &Report{}, &AnalysisReport{}
			r.lastReport.Store(report)
			r.lastAnalysis.Store(analysis)
			gate := make(chan struct{})
			r.mu.Lock()
			r.startPortfolioGoroutine("current-run", task, func(chan struct{}) error {
				<-gate
				return nil
			})
			t.Cleanup(func() { close(gate); waitPortfolioDone(t, r) })
			st := r.Status()
			if st["task"] != task || st["state"] != "running" || st["config"] != nil || st["doneCodes"] != int64(0) || st["totalCodes"] != int64(0) {
				t.Fatalf("previous task leaked into %s: %+v", task, st)
			}
			if r.errMsg.Load() != nil || st["currentCode"] == oldCode || st["phase"] == "reporting" {
				t.Fatalf("previous diagnostic state leaked into %s: %+v", task, st)
			}
			if r.LatestReport() != report || r.LatestAnalysis() != analysis {
				t.Fatal("starting another task discarded completed results")
			}
			r.Stop()
			select {
			case <-r.control.Load().stop:
			default:
				t.Fatal("Stop did not reach the current portfolio task")
			}
		})
	}
}

func TestTaskLifecycleReleasesLockAndKeepsStopLocal(t *testing.T) {
	for _, tc := range []struct {
		name, stoppedState, wantState string
		err                           error
		panicValue                    string
	}{
		{name: "success", wantState: "done"},
		{name: "failure", err: errors.New("save failed"), wantState: "error"},
		{name: "backtest-stop", stoppedState: "idle", err: errStopped, wantState: "idle"},
		{name: "analysis-stop", stoppedState: "idle", err: fmt.Errorf("cancel: %w", errStopped), wantState: "idle"},
		{name: "portfolio-stop", stoppedState: "done", err: errStopped, wantState: "done"},
		{name: "panic", panicValue: "test panic", wantState: "error"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := NewRunner()
			r.mu.Lock()
			old := r.beginTask(taskStart{kind: "backtest", phase: "preparing"})
			gate := make(chan struct{})
			r.launchTask(old, tc.stoppedState, func(stop chan struct{}) error {
				<-gate
				if tc.panicValue != "" {
					panic(tc.panicValue)
				}
				return tc.err
			})
			if r.mu.TryLock() {
				r.mu.Unlock()
				close(gate)
				t.Fatal("another task can start before the callback finishes")
			}
			var wg sync.WaitGroup
			for i := 0; i < 10; i++ {
				wg.Add(1)
				go func() { defer wg.Done(); r.Stop() }()
			}
			wg.Wait()
			close(gate)
			deadline := time.Now().Add(3 * time.Second)
			for !r.mu.TryLock() {
				if time.Now().After(deadline) {
					t.Fatal("finished task leaked the lock")
				}
				time.Sleep(time.Millisecond)
			}
			defer r.mu.Unlock()
			if st := r.Status(); st["state"] != tc.wantState || r.running.Load() {
				t.Fatalf("incorrect terminal state: %+v", st)
			}
			if tc.wantState == "error" {
				want := tc.panicValue
				if tc.err != nil {
					want = tc.err.Error()
				}
				if msg, _ := r.Status()["error"].(string); !strings.Contains(msg, want) {
					t.Fatalf("task failure lost: %q", msg)
				}
			}
			next := r.beginTask(taskStart{kind: "analysis", phase: "preparing", config: &RunConfig{StartYear: 2026}})
			defer r.running.Store(false)
			old.requestStop()
			select {
			case <-next.stop:
				t.Fatal("old task cancellation reached the next task")
			default:
			}
			if st := r.Status(); st["state"] != "running" || st["error"] != nil || st["config"].(RunConfig).StartYear != 2026 {
				t.Fatalf("next task inherited a terminal state: %+v", st)
			}
		})
	}
}
