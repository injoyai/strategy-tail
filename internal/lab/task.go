package lab

import (
	"errors"

	"github.com/injoyai/strategy-tail/internal/researchrun"
)

type taskStart struct {
	kind, phase, runID string
	config             *RunConfig
}

// beginTask 在持有 mu 时初始化当前任务；已完成报告由各功能独立保留。
func (r *Runner) beginTask(start taskStart) *runControl {
	ctl := &runControl{stop: make(chan struct{}), task: start.kind}
	r.control.Store(ctl)
	r.doneCodes.Store(0)
	r.totalCodes.Store(0)
	r.currentCode.Store(nil)
	r.errMsg.Store(nil)
	r.lastRun.Store(start.config)
	r.phase.Store(nil)
	r.portfolioRun.Store(nil)
	r.stageProgress.Store(nil)
	r.task.Store(&start.kind)
	if start.runID != "" {
		r.portfolioRun.Store(&start.runID)
	}
	if start.phase != "" {
		r.phase.Store(&start.phase)
		r.setTaskProgress(start.kind, "", researchrun.Progress{Phase: start.phase})
	}
	// 元数据先于 running 发布，停止请求与状态查询均使用本次任务。
	r.running.Store(true)
	state := "running"
	r.state.Store(&state)
	return ctl
}

// launchTask 接管 mu；功能回调只负责执行和发布自己的结果。
// stoppedState 保留旧契约：回测/分析停止为 idle，组合取消为 done。
func (r *Runner) launchTask(ctl *runControl, stoppedState string, run func(chan struct{}) error) {
	go func() {
		defer r.mu.Unlock()
		defer r.running.Store(false)
		defer r.recoverTaskPanic()
		state := "done"
		if err := run(ctl.stop); err != nil {
			if errors.Is(err, errStopped) {
				state = stoppedState
			} else {
				message := err.Error()
				r.errMsg.Store(&message)
				state = "error"
			}
		}
		r.state.Store(&state)
	}()
}
