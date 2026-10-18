package lab

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	common "github.com/injoyai/strategy-tail"
	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/lib/extend"
	sb "github.com/injoyai/strategy-tail/strategies/buy"
	"github.com/injoyai/tdx"
	"github.com/injoyai/tdx/lib/xorms"
)

func TestDataUpdateTradingDay(t *testing.T) {
	for _, tc := range []struct {
		name, at, date string
		calendar       func(time.Time) bool
		want           string
	}{
		{"Saturday", "2026-10-17T08:00:00Z", "2026-10-17", nil, "false"},
		{"Shanghai Sunday", "2026-10-17T16:00:00Z", "2026-10-18", func(time.Time) bool { t.Fatal("weekend queried calendar"); return true }, "false"},
		{"holiday", "2026-10-01T08:00:00Z", "2026-10-01", func(time.Time) bool { return false }, "false"},
		{"trading day", "2026-10-14T08:00:00Z", "2026-10-14", func(at time.Time) bool { return at.Hour() == 16 }, "true"},
		{"calendar unavailable", "2026-10-14T08:00:00Z", "2026-10-14", nil, "null"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			at, err := time.Parse(time.RFC3339, tc.at)
			if err != nil {
				t.Fatal(err)
			}
			date, trading := dataUpdateTradingDay(at, tc.calendar)
			value, err := json.Marshal(trading)
			if err != nil || date != tc.date || string(value) != tc.want {
				t.Fatalf("date=%s trading=%s err=%v", date, value, err)
			}
		})
	}
}

func TestDataUpdateStatusReadsSharedSuccessMarker(t *testing.T) {
	dir := t.TempDir()
	filename := filepath.Join(dir, "update.db")
	db, err := xorms.NewSqlite(filename)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Sync2(new(tdx.UpdateModel)); err != nil {
		t.Fatal(err)
	}
	last := time.Date(2026, 10, 12, 16, 0, 0, 0, time.Local)
	if _, err := db.Insert(&tdx.UpdateModel{Key: "pull", Time: last.Unix()}); err != nil {
		t.Fatal(err)
	}
	db.Close()
	oldPull := common.Pull
	common.Pull = &extend.PullKline{Config: extend.PullKlineConfig{Dir: dir}}
	t.Cleanup(func() { common.Pull = oldPull })
	s := NewServer()
	s.dataUpdater = newDataUpdater(s.runner, filepath.Join(dir, "settings.json"), updateData)
	s.dataUpdater.status.LastSuccess = "2026-10-10T16:00:00+08:00"
	s.dataUpdater.status.State = "error"
	s.dataUpdater.status.FinishedAt = last.AddDate(0, 0, 2).Format(time.RFC3339)
	s.dataUpdater.status.Failed = 2
	read := func() DataUpdateStatus {
		res := httptest.NewRecorder()
		s.handleDataUpdateStatus(res, httptest.NewRequest(http.MethodGet, "/api/data-update/status", nil))
		var status DataUpdateStatus
		if err := json.Unmarshal(res.Body.Bytes(), &status); err != nil {
			t.Fatal(err)
		}
		return status
	}
	status := read()
	if status.TradingDate == "" {
		t.Fatal("status omitted market calendar date")
	}
	if status.LastSuccess != last.Format(time.RFC3339) || status.State != "error" || status.Failed != 2 || status.FinishedAt == "" {
		t.Fatalf("shared success or failed attempt missing: %+v", status)
	}
	if s.dataUpdater.Status().LastSuccess != "2026-10-10T16:00:00+08:00" {
		t.Fatal("status read rewrote saved Web state")
	}
	if err := os.Remove(filename); err != nil {
		t.Fatal(err)
	}
	if status := read(); status.LastSuccess != "" || status.LastSuccessError != "" {
		t.Fatalf("missing database reused stale Web timestamp: %+v", status)
	}
	if err := os.WriteFile(filename, []byte("invalid database"), 0600); err != nil {
		t.Fatal(err)
	}
	if status := read(); status.LastSuccessError == "" || status.State != "error" {
		t.Fatalf("database read failure hidden: %+v", status)
	}
}

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
		progress(extend.UpdateProgress{Stage: extend.Minute, Done: 2, Total: 4, StageDone: 1, StageTotal: 2, Code: "sh600000", Failed: 1, Message: "更新分钟线"})
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

func TestDataUpdatePartialSuccessSurvivesRetryAndRestart(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "settings.json")
	u := newDataUpdater(NewRunner(), filename, func(_ bool, progress func(extend.UpdateProgress)) error {
		progress(extend.UpdateProgress{Done: 3, Total: 3, Failed: 1})
		return errors.New("one download failed")
	})
	u.status.LastSuccess = "2026-10-12T16:00:00+08:00"
	if err := u.Start(true); err != nil {
		t.Fatal(err)
	}
	s := waitDataUpdate(t, u)
	if s.LastPartialSuccess != s.FinishedAt || s.LastPartialSuccess == "" || s.State != "error" || s.LastSuccess != "2026-10-12T16:00:00+08:00" {
		t.Fatalf("partial update lost or full success advanced: %+v", s)
	}
	reloaded := newDataUpdater(NewRunner(), filename, func(bool, func(extend.UpdateProgress)) error {
		return errors.New("retry could not start downloads")
	})
	if reloaded.Status().LastPartialSuccess != s.LastPartialSuccess {
		t.Fatal("partial success did not survive restart")
	}
	if err := reloaded.Start(true); err != nil {
		t.Fatal(err)
	}
	retried := waitDataUpdate(t, reloaded)
	if retried.LastPartialSuccess != s.LastPartialSuccess || retried.Failed != 0 {
		t.Fatalf("failed retry erased prior update: %+v", retried)
	}
}

func TestDataUpdateLegacyPartialSuccessAndUnfinishedFailures(t *testing.T) {
	for _, tc := range []struct {
		name                string
		done, total, failed int
		partial             bool
	}{
		{"partial", 6400, 6400, 2, true},
		{"all failed", 3, 3, 3, false},
		{"unfinished", 2, 3, 1, false},
		{"empty", 0, 0, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			filename := filepath.Join(t.TempDir(), "settings.json")
			saved := DataUpdateStatus{Config: DataUpdateConfig{Time: "15:45"}, State: "error", FinishedAt: "2026-10-14T17:32:21+08:00",
				UpdateProgress: extend.UpdateProgress{Done: tc.done, Total: tc.total, Failed: tc.failed}}
			body, err := json.Marshal(saved)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filename, body, 0600); err != nil {
				t.Fatal(err)
			}
			u := newDataUpdater(NewRunner(), filename, func(_ bool, progress func(extend.UpdateProgress)) error {
				progress(saved.UpdateProgress)
				return errors.New("downloads failed")
			})
			if got := u.Status().LastPartialSuccess; (got == saved.FinishedAt) != tc.partial || (!tc.partial && got != "") {
				t.Fatalf("legacy classification: %q", got)
			}
			if err := u.Start(true); err != nil {
				t.Fatal(err)
			}
			status := waitDataUpdate(t, u)
			if (status.LastPartialSuccess != "") != tc.partial {
				t.Fatalf("run classification: %+v", status)
			}
		})
	}
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
