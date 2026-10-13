package lab

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/lib/extend"
	sb "github.com/injoyai/strategy-tail/strategies/buy"
)

func waitDataUpdate(t *testing.T, u *DataUpdater) DataUpdateStatus {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		s := u.Status()
		if s.State != "running" && !u.runner.running.Load() && u.runner.mu.TryLock() {
			u.runner.mu.Unlock()
			return s
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("data update did not finish")
	return DataUpdateStatus{}
}

func TestDataUpdateLocksOutDuplicateUpdatesAndBacktests(t *testing.T) {
	r := NewRunner()
	gate := make(chan struct{})
	u := newDataUpdater(r, filepath.Join(t.TempDir(), "settings.json"), func(manual bool, progress func(extend.UpdateProgress)) error {
		if !manual {
			t.Error("expected manual update")
		}
		progress(extend.UpdateProgress{Stage: extend.Day, Done: 1, Total: 2, Code: "sh600000"})
		<-gate
		return nil
	})
	if err := u.Start(true); err != nil {
		t.Fatal(err)
	}
	if err := u.Start(true); !errors.Is(err, errDataTaskBusy) {
		t.Fatalf("duplicate update: %v", err)
	}
	cfg := RunConfig{StartYear: 2025, EndYear: 2025, SampleMode: "codes", SampleCodes: []string{"sh600000"}, HoldingDays: 1}
	if err := r.Start(cfg, []core.Variant{{Name: "test", Buyer: sb.A全部{}}}); err == nil {
		t.Fatal("backtest entered update lock")
	}
	analysis := AnalyzeConfig{RunConfig: cfg, Kind: "momentum", Days: 2, Window: 1}
	if err := analysis.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := r.StartAnalysis(analysis); err == nil {
		t.Fatal("analysis entered update lock")
	}
	r.Stop() // Update writes are allowed to finish instead of closing a nil stop channel.
	close(gate)
	s := waitDataUpdate(t, u)
	if s.State != "done" || s.LastSuccess == "" {
		t.Fatalf("success not recorded: %+v", s)
	}
	if !r.mu.TryLock() {
		t.Fatal("task lock leaked")
	}
	r.mu.Unlock()
}

func TestDataUpdateSharedTaskStatus(t *testing.T) {
	s := NewServer()
	gate, ready := make(chan struct{}), make(chan struct{})
	s.dataUpdater = newDataUpdater(s.runner, filepath.Join(t.TempDir(), "settings.json"), func(_ bool, progress func(extend.UpdateProgress)) error {
		progress(extend.UpdateProgress{Stage: extend.Minute, Done: 2, Total: 4, Code: "sh600000", Failed: 1, Message: "更新分钟线"})
		close(ready)
		<-gate
		return errors.New("one code failed")
	})
	if err := s.dataUpdater.Start(true); err != nil {
		t.Fatal(err)
	}
	defer func() { close(gate); waitDataUpdate(t, s.dataUpdater) }()
	<-ready
	res := httptest.NewRecorder()
	s.Handler().ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/api/status", nil))
	var status map[string]any
	if err := json.Unmarshal(res.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if status["task"] != "data_update" || status["state"] != "running" || status["phase"] != extend.Minute || status["progress"] != float64(50) || status["failed"] != float64(1) || status["currentCode"] != "sh600000" {
		t.Fatalf("shared task status omitted update progress: %+v", status)
	}
}

func TestDataUpdateLocksOutPortfolioTasks(t *testing.T) {
	f := newPortfolioRunFixture(t)
	exp, _, err := f.experiments.Create(portfolioExperimentRequest(f.model, testUUID1))
	if err != nil {
		t.Fatal(err)
	}
	validation, _, err := f.validations.Create(CreatePortfolioValidationRequest{RequestID: testUUID2, Spec: portfolioValidationSpecFor(f.model)}, f.models)
	if err != nil {
		t.Fatal(err)
	}
	gate := make(chan struct{})
	u := newDataUpdater(f.runner, filepath.Join(t.TempDir(), "settings.json"), func(bool, func(extend.UpdateProgress)) error {
		<-gate
		return nil
	})
	if err := u.Start(true); err != nil {
		t.Fatal(err)
	}
	defer func() { close(gate); waitDataUpdate(t, u) }()
	if err := f.runner.StartPortfolioExperiment(exp.ExperimentID); err == nil {
		t.Fatal("portfolio experiment entered update lock")
	}
	if err := f.runner.StartPortfolioValidation(validation.ID); err == nil {
		t.Fatal("portfolio validation entered update lock")
	}
}

func TestDataUpdateFailurePreservesLastSuccessAndReleasesLock(t *testing.T) {
	r := NewRunner()
	u := newDataUpdater(r, filepath.Join(t.TempDir(), "settings.json"), func(_ bool, progress func(extend.UpdateProgress)) error {
		progress(extend.UpdateProgress{Stage: extend.Minute, Done: 2, Total: 3, Failed: 1})
		return errors.New("download failed")
	})
	u.status.LastSuccess = "2026-10-12T16:00:00+08:00"
	if err := u.Start(true); err != nil {
		t.Fatal(err)
	}
	s := waitDataUpdate(t, u)
	if s.State != "error" || s.Failed != 1 || s.LastSuccess != "2026-10-12T16:00:00+08:00" {
		t.Fatalf("failure hidden: %+v", s)
	}
	if err := u.Start(true); err != nil {
		t.Fatalf("could not retry: %v", err)
	}
	waitDataUpdate(t, u)
}

func TestAutomaticUpdateWaitsForTasksAndRunsOncePerDay(t *testing.T) {
	r := NewRunner()
	var calls atomic.Int32
	u := newDataUpdater(r, filepath.Join(t.TempDir(), "settings.json"), func(manual bool, progress func(extend.UpdateProgress)) error {
		if manual {
			t.Error("expected automatic update")
		}
		calls.Add(1)
		return nil
	})
	if err := u.Configure(DataUpdateConfig{Enabled: true, Time: "15:45"}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().In(u.location)
	before := time.Date(now.Year(), now.Month(), now.Day(), 15, 44, 0, 0, u.location)
	u.tick(before)
	if calls.Load() != 0 {
		t.Fatal("ran before scheduled time")
	}
	r.mu.Lock()
	u.tick(before.Add(2 * time.Minute))
	if calls.Load() != 0 {
		t.Fatal("ran during another task")
	}
	r.mu.Unlock()
	u.tick(before.Add(2 * time.Minute))
	waitDataUpdate(t, u)
	u.tick(before.Add(3 * time.Minute))
	if calls.Load() != 1 {
		t.Fatalf("automatic repeats: %d", calls.Load())
	}
	reloaded := newDataUpdater(NewRunner(), u.filename, u.run)
	reloaded.tick(before.Add(4 * time.Minute))
	if calls.Load() != 1 || !reloaded.Status().Config.Enabled {
		t.Fatal("schedule did not survive restart")
	}
}

func TestDataUpdateAPIRejectsCrossOriginAndInvalidSchedule(t *testing.T) {
	s := NewServer()
	s.dataUpdater = newDataUpdater(s.runner, filepath.Join(t.TempDir(), "settings.json"), func(bool, func(extend.UpdateProgress)) error { return nil })
	h := s.Handler()
	doReq(t, h, http.MethodPut, "/api/data-update/config", DataUpdateConfig{Enabled: true, Time: "25:90"}, http.StatusBadRequest)
	if s.dataUpdater.Status().Config.Enabled {
		t.Fatal("invalid schedule enabled")
	}
	for _, path := range []string{"/api/data-update/run", "/api/data-update/config"} {
		req := httptest.NewRequest(http.MethodPost, path, nil)
		if path == "/api/data-update/config" {
			req.Method = http.MethodPut
		}
		req.Header.Set("Origin", "https://other.example")
		res := httptest.NewRecorder()
		h.ServeHTTP(res, req)
		if res.Code != http.StatusForbidden {
			t.Fatalf("%s cross-origin status %d", path, res.Code)
		}
	}
	req := httptest.NewRequest(http.MethodPost, "/api/data-update/run", nil)
	req.Header.Set("Origin", "https://"+req.Host)
	res := httptest.NewRecorder()
	h.ServeHTTP(res, req)
	if res.Code != http.StatusForbidden {
		t.Fatal("cross-scheme origin accepted")
	}
	doReq(t, h, http.MethodPut, "/api/data-update/config", DataUpdateConfig{Enabled: true, Time: "16:15"}, http.StatusOK)
	if s.dataUpdater.Status().Config.Time != "16:15" {
		t.Fatal("schedule not saved")
	}
}
