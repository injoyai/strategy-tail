package extend

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/injoyai/strategy-tail/internal/update"
	"github.com/injoyai/strategy-tail/internal/xorms"
	"github.com/injoyai/tdx/v2"
)

func TestUpdateLockExcludesOtherProcesses(t *testing.T) {
	if dir := os.Getenv("STRATEGY_UPDATE_LOCK_TEST_DIR"); dir != "" {
		_, err := lockUpdates(dir)
		if !errors.Is(err, ErrUpdateLocked) {
			t.Fatalf("child lock error: %v", err)
		}
		return
	}
	dir := t.TempDir()
	release, err := lockUpdates(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { release() })
	if _, err := lockUpdates(dir); !errors.Is(err, ErrUpdateLocked) {
		t.Fatalf("duplicate lock: %v", err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestUpdateLockExcludesOtherProcesses$")
	cmd.Env = append(os.Environ(), "STRATEGY_UPDATE_LOCK_TEST_DIR="+dir)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("other process entered: %v\n%s", err, b)
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
	release2, err := lockUpdates(dir)
	if err != nil {
		t.Fatalf("lock not released: %v", err)
	}
	release2()
}

func TestUpdateJobsReportOnceAfterRetriesAndReturnFailures(t *testing.T) {
	var attempts atomic.Int32
	reports, failures := 0, 0
	err := runUpdateCodes([]string{"retry", "failed"}, 2, func(code string) error {
		if code == "retry" && attempts.Add(1) >= 3 {
			return nil
		}
		return fmt.Errorf("network failure")
	}, func(code string, err error) {
		reports++
		if err != nil {
			failures++
		}
	})
	if err == nil || reports != 2 || failures != 1 || attempts.Load() != 3 {
		t.Fatalf("err=%v reports=%d failures=%d retryAttempts=%d", err, reports, failures, attempts.Load())
	}
}

func TestUpdateFreshnessSkipsRepeatedDownloads(t *testing.T) {
	pull := fixturePull(t, PullKlineConfig{Types: []string{Day}})
	updated := pull.Updated
	if err := updated.Mark(pull.sourceUpdateKey()); err != nil {
		t.Fatal(err)
	}
	var last UpdateProgress
	// An empty manager cannot download. A fresh batch must return before network work.
	if err := pull.UpdateWithProgress(&MarketData{}, true, func(p UpdateProgress) { last = p }); err != nil {
		t.Fatal(err)
	}
	if !last.Skipped || last.Total != 0 {
		t.Fatalf("unexpected work: %+v", last)
	}
}

func TestUpdateFreshnessUsesCompleteBatchAfterClose(t *testing.T) {
	dir := t.TempDir()
	db, err := xorms.NewSQLite(filepath.Join(dir, "update.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	updated, err := update.New(db, updateCloseHour, updateCloseMinute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := updated.IsUpdated("pull"); err != nil {
		t.Fatal(err)
	}
	if err := updated.Mark("pull"); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	close := time.Date(now.Year(), now.Month(), now.Day(), 15, 30, 0, 0, time.Local)
	if !now.After(close) {
		close = close.AddDate(0, 0, -1)
	}
	for _, tc := range []struct {
		name  string
		at    time.Time
		fresh bool
	}{
		{"before close", close.Add(-time.Minute), false},
		{"at cutoff", close, true},
		{"after cutoff", close.Add(time.Second), true},
		{"prior batch", close.AddDate(0, 0, -1).Add(time.Hour), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := db.Where("`Key`=?", "pull").Cols("Time").Update(&update.Record{Time: tc.at.Unix()}); err != nil {
				t.Fatal(err)
			}
			fresh, err := updated.IsUpdated("pull")
			if err != nil || fresh != tc.fresh {
				t.Fatalf("fresh=%v err=%v want=%v", fresh, err, tc.fresh)
			}
		})
	}
}

func TestUpdateFailureStillProcessesRemainingCodesAndTypes(t *testing.T) {
	pull := fixturePull(t, PullKlineConfig{Codes: []string{"sh600000", "sz000001"}, Goroutines: 1, Types: []string{Day, Minute}})
	updated := pull.Updated
	jobs := map[string]int{}
	var last UpdateProgress
	var phases []UpdateProgress
	err := pull.UpdateWithProgress(&MarketData{}, true, func(p UpdateProgress) {
		phases = append(phases, p)
		if p.Done > last.Done {
			jobs[p.Stage+":"+p.Code]++
		}
		last = p
	})
	if err == nil || last.Done != 4 || last.Total != 4 || last.Failed != 4 || last.Stage != Minute || len(jobs) != 4 {
		t.Fatalf("batch stopped early: progress=%+v jobs=%v err=%v", last, jobs, err)
	}
	if len(phases) != 8 {
		t.Fatalf("unexpected stage events: %+v", phases)
	}
	for i, p := range phases[1 : len(phases)-1] {
		stage := Day
		if i >= 3 {
			stage = Minute
		}
		if p.Stage != stage || p.StageDone != i%3 || p.StageTotal != 2 {
			t.Fatalf("phase did not reset: %+v", phases)
		}
	}
	for job, count := range jobs {
		if count != 1 {
			t.Fatalf("%s reported %d times", job, count)
		}
	}
	if fresh, err := updated.IsUpdated("pull"); err != nil || fresh {
		t.Fatalf("partial batch marked fresh: %v %v", fresh, err)
	}
}

func TestUpdateFailedCodeDoesNotPreventLaterSuccess(t *testing.T) {
	calls := map[string]int{}
	completed := []string{}
	err := runUpdateCodes([]string{"failed", "remaining"}, 1, func(code string) error {
		calls[code]++
		if code == "failed" {
			return errors.New("download failed")
		}
		return nil
	}, func(code string, err error) {
		if err == nil {
			completed = append(completed, code)
		}
	})
	if err == nil || calls["failed"] != tdx.DefaultRetry || calls["remaining"] != 1 || len(completed) != 1 || completed[0] != "remaining" {
		t.Fatalf("remaining update lost: calls=%v completed=%v err=%v", calls, completed, err)
	}
}

func TestFailedCodeDoesNotMarkWholeBatchUpdated(t *testing.T) {
	pull := fixturePull(t, PullKlineConfig{Codes: []string{"sh600000"}, Goroutines: 1, Types: []string{Day}})
	updated := pull.Updated
	var progress UpdateProgress
	if err := pull.UpdateWithProgress(&MarketData{}, true, func(p UpdateProgress) { progress = p }); err == nil {
		t.Fatal("failed stock was silently accepted")
	}
	if progress.Done != 1 || progress.Failed != 1 {
		t.Fatalf("failed progress: %+v", progress)
	}
	if fresh, err := updated.IsUpdated("pull"); err != nil || fresh {
		t.Fatalf("failed batch marked fresh: %v %v", fresh, err)
	}
}
