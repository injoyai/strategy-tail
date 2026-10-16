package lab

import "github.com/injoyai/strategy-tail/internal/researchrun"

type taskProgress struct {
	Task string
	Unit string
	researchrun.Progress
}

// 任务类型也随快照发布，切换任务时不能沿用上一项任务的阶段。
func (r *Runner) setTaskProgress(task, unit string, p researchrun.Progress) {
	r.stageProgress.Store(&taskProgress{Task: task, Unit: unit, Progress: p})
}
