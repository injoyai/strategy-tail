package extend

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/injoyai/tdx"
)

type unavailableValuationPool struct {
	tdx.IPool
	calls int
}

func TestKlineFailureStillProcessesValuation(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, DirDay), []byte("existing data"), 0644); err != nil {
		t.Fatal(err)
	}
	pull, err := NewPullKline(PullKlineConfig{Dir: dir, Codes: []string{"sh600519"}, Types: []string{Day}, ValuationDatabase: filepath.Join(t.TempDir(), "valuation.db")})
	if err != nil {
		t.Fatal(err)
	}
	pool := &unavailableValuationPool{}
	var last UpdateProgress
	err = pull.UpdateWithProgress(&tdx.Manage{IPool: pool}, true, func(p UpdateProgress) { last = p })
	if err == nil || last.Done != 2 || last.Total != 2 || last.Failed != 2 || pool.calls != 1 {
		t.Fatalf("progress=%+v calls=%d err=%v", last, pool.calls, err)
	}
	if fresh, err := pull.Updated.Updated("pull"); err != nil || fresh {
		t.Fatalf("failed batch marked complete: %v %v", fresh, err)
	}
}

func (p *unavailableValuationPool) Do(func(*tdx.Client) error) error {
	p.calls++
	return errors.New("snapshot unavailable")
}

func TestFreshKlinesDoNotSkipMissingValuationAndAllowRetry(t *testing.T) {
	pull, err := NewPullKline(PullKlineConfig{Dir: t.TempDir(), ValuationDatabase: filepath.Join(t.TempDir(), "valuation.db"), Types: []string{Day}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pull.Updated.Updated("pull"); err != nil {
		t.Fatal(err)
	}
	if err = pull.Updated.Update("pull"); err != nil {
		t.Fatal(err)
	}
	pool := &unavailableValuationPool{}
	m := &tdx.Manage{IPool: pool}
	for attempt := 1; attempt <= 2; attempt++ {
		var last UpdateProgress
		err = pull.UpdateWithProgress(m, true, func(p UpdateProgress) { last = p })
		if err == nil || last.Skipped || last.Stage != "valuation" || last.Done != 1 || last.Total != 1 || last.Failed != 1 || pool.calls != attempt {
			t.Fatalf("attempt=%d progress=%+v calls=%d err=%v", attempt, last, pool.calls, err)
		}
		if ready, err := pull.Updated.Updated("valuation"); err != nil || ready {
			t.Fatalf("failed snapshot marked complete: %v %v", ready, err)
		}
	}
	if err = pull.Updated.Update("valuation"); err != nil {
		t.Fatal(err)
	}
	var last UpdateProgress
	if err = pull.UpdateWithProgress(m, true, func(p UpdateProgress) { last = p }); err != nil || !last.Skipped || pool.calls != 2 {
		t.Fatalf("duplicate download: %+v calls=%d err=%v", last, pool.calls, err)
	}
}
