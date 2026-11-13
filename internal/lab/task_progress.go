package lab

import (
	"context"

	"github.com/injoyai/strategy-tail/internal/researchrun"
	"github.com/injoyai/strategy-tail/internal/screener"
)

type taskProgress struct {
	Task string
	Unit string
	researchrun.Progress
}

// 任务类型也随快照发布，切换任务时不能沿用上一项任务的阶段。
func (r *Runner) setTaskProgress(task, unit string, p researchrun.Progress) {
	r.stageProgress.Store(&taskProgress{Task: task, Unit: unit, Progress: p})
}

func (r *Runner) screenProgress(task string) screener.ScanProgress {
	return func(done, total int, code string) {
		r.setTaskProgress(task, "stocks", researchrun.Progress{Phase: "scan", Done: done, Total: total, Code: code})
	}
}

func screenTaskContext(stop <-chan struct{}) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		select {
		case <-stop:
			cancel()
		case <-ctx.Done():
		}
	}()
	return ctx, cancel
}
