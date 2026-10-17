package lab

import (
	"errors"
	"os"
	"os/exec"
	"testing"
)

func TestPortfolioRunLockAcrossProcesses(t *testing.T) {
	if root := os.Getenv("STRATEGY_PORTFOLIO_LOCK_TEST_ROOT"); root != "" {
		store := NewPortfolioExperimentStore(root, root)
		unlock, err := store.lockRun(os.Getenv("STRATEGY_PORTFOLIO_LOCK_TEST_ID"))
		if err == nil {
			unlock()
		}
		if !errors.Is(err, errPortfolioRunLocked) {
			t.Fatalf("child entered live experiment: %v", err)
		}
		return
	}
	f := newExperimentFixture(t)
	exp := f.createOne(t)
	unlock, err := f.store.lockRun(exp.ExperimentID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { unlock() })
	cmd := exec.Command(os.Args[0], "-test.run=^TestPortfolioRunLockAcrossProcesses$")
	cmd.Env = append(os.Environ(), "STRATEGY_PORTFOLIO_LOCK_TEST_ROOT="+f.recordsRoot,
		"STRATEGY_PORTFOLIO_LOCK_TEST_ID="+exp.ExperimentID)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("other process entered: %v\n%s", err, output)
	}
	if err := unlock(); err != nil {
		t.Fatal(err)
	}
	unlocked, err := f.store.lockRun(exp.ExperimentID)
	if err != nil {
		t.Fatalf("lock was not released: %v", err)
	}
	unlocked()
}
