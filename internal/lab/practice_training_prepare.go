package lab

import (
	"errors"
	"fmt"
	"log"
	"runtime"
	"time"

	"github.com/injoyai/strategy-tail/internal/researchrun"
	"github.com/injoyai/strategy-tail/lib/extend"
)

func (r *Runner) StartPracticeBank(req practiceStartRequest) (*practiceBank, error) {
	if req.RequestID == "" {
		return nil, errPracticeRequestIDRequired
	}
	if err := practiceValidateYears(req.FromYear, req.ToYear); err != nil {
		return nil, err
	}
	if r.trainingStore == nil || r.builderStore == nil {
		return nil, errPracticeInternal
	}
	r.trainingStore.mu.Lock()
	existing, err := r.trainingStore.findBank(req)
	r.trainingStore.mu.Unlock()
	if err != nil || existing != nil {
		return existing, err
	}
	st, err := r.builderStore.Get(req.StrategyID)
	if errors.Is(err, errBuilderNotFound) {
		return nil, errPracticeStrategyNotFound
	}
	if err != nil {
		return nil, errPracticeStrategyRead
	}
	if st.Version != req.StrategyVersion {
		return nil, errPracticeStrategyVersion
	}
	proj, err := practiceValidateBuySide(st.Spec)
	if err != nil {
		return nil, err
	}
	if !r.mu.TryLock() {
		return nil, errPracticeBusy
	}
	b, err := r.trainingStore.createBank(req, proj, practiceStartCost())
	if err != nil {
		r.mu.Unlock()
		return nil, err
	}
	if b.Phase != practicePhasePreparing {
		r.mu.Unlock()
		return b, nil
	}
	ctl := r.beginTask(taskStart{kind: practiceTaskPrepare, phase: practicePhaseKeyPrepare, runID: b.ID})
	accepted := *b
	r.launchTask(ctl, "idle", func(stop chan struct{}) error { return r.preparePracticeBank(b, stop) })
	return &accepted, nil
}
func (r *Runner) preparePracticeBank(b *practiceBank, stop chan struct{}) (err error) {
	// Only a ready manifest publishes the completed stocks. Failed/interrupted
	// banks stay unavailable, even if their immutable stock files were saved.
	defer func() {
		if p := recover(); p != nil {
			log.Printf("[lab] 题库准备异常: %v", p)
			err = errPracticeInternal
		}
		if err != nil {
			b.Phase = practicePhaseFailed
			b.FailReason = practiceFailData
			if errors.Is(err, errStopped) {
				b.Phase = practicePhaseCancelled
				b.FailReason = ""
			}
			if errors.Is(err, errPracticeInternal) {
				b.FailReason = practiceFailInternal
			}
			if b.Total == 0 && err.Error() == "指定年份范围内没有命中机会" {
				b.FailReason = practiceFailNoMatch
			}
		}
		if writeErr := r.trainingStore.writeBank(b); writeErr != nil {
			log.Printf("[lab] 题库保存失败: %v", writeErr)
			err = errPracticeInternal
		}
		if err != nil && !errors.Is(err, errStopped) {
			log.Printf("[lab] 题库建立失败: %v", err)
			err = fmt.Errorf("题库建立失败，请查看题库状态后重试")
		}
	}()
	src := r.practiceSourceOf()
	codes, e := src.Codes()
	if e != nil {
		return e
	}
	seen := map[string]bool{}
	unique := make([]string, 0, len(codes))
	for _, c := range codes {
		if !seen[c] {
			seen[c] = true
			unique = append(unique, c)
		}
	}
	r.setTaskProgress(practiceTaskPrepare, "stocks", researchrun.Progress{Phase: practicePhaseKeyScan, Total: len(unique)})
	_, cov, e := practiceScan(practiceScanParams{Workers: runtime.GOMAXPROCS(0), Codes: unique, Source: src, FromYear: b.Request.FromYear, ToYear: b.Request.ToYear, Now: time.Now(), Buyer: b.Spec.Buyer, Stop: stop,
		OnStock: func(code string, ks extend.Klines, hits []int) error {
			m, e := buildPracticeMarket(code, ks, practiceMarketSource, practiceNowUTC())
			if e != nil {
				return e
			}
			return r.trainingStore.appendStock(b, m, hits)
		},
		OnProgress: func(done, total int) {
			r.setTaskProgress(practiceTaskPrepare, "stocks", researchrun.Progress{Phase: practicePhaseKeyScan, Done: done, Total: total})
		},
	})
	b.Coverage = cov
	if e != nil {
		return e
	}
	if b.Total == 0 {
		return fmt.Errorf("指定年份范围内没有命中机会")
	}
	r.setTaskProgress(practiceTaskPrepare, "stocks", researchrun.Progress{Phase: practicePhaseKeySave})
	select {
	case <-stop:
		return errStopped
	default:
	}
	b.Phase = "ready"
	b.ReadyAt = practiceNowUTC()
	return nil
}
