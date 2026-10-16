package extend

import (
	"fmt"

	"github.com/injoyai/strategy-tail/researchdata"
	tdxdata "github.com/injoyai/strategy-tail/researchdata/tdx"
	"github.com/injoyai/tdx"
)

func (this *PullKline) updateValuation(m *tdx.Manage, progress UpdateProgress, report func(UpdateProgress)) (UpdateProgress, error) {
	if this.Config.ValuationDatabase == "" {
		return progress, nil
	}
	fresh, err := this.Updated.Updated("valuation")
	if err != nil {
		return progress, err
	}
	if fresh {
		if !progress.Skipped {
			progress.Done++
			progress.Stage, progress.Code = "valuation", ""
			progress.StageDone, progress.StageTotal = 1, 1
			if report != nil {
				report(progress)
			}
		}
		return progress, nil
	}
	if progress.Total == 0 {
		progress.Total = 1
	}
	progress.Stage, progress.Code, progress.Skipped = "valuation", "", false
	progress.StageDone, progress.StageTotal = 0, 0
	progress.Message = "正在采集沪深主板PE、静态PE和股息率"
	if report != nil {
		report(progress)
	}
	written, skipped := 0, 0
	store, err := researchdata.OpenSQLiteStore(this.Config.ValuationDatabase)
	if err == nil {
		written, skipped, err = tdxdata.Sync(m.IPool, store)
		closeErr := store.Close()
		if err == nil {
			err = closeErr
		}
	}
	if err == nil {
		err = this.Updated.Update("valuation")
	}
	progress.Done++
	progress.StageDone, progress.StageTotal = 1, 1
	if err != nil {
		progress.Failed++
		err = fmt.Errorf("估值快照更新失败: %w", err)
		progress.Message = err.Error()
	} else {
		progress.Message = fmt.Sprintf("估值快照已写入%d只，跳过%d只无效记录", written, skipped)
	}
	if report != nil {
		report(progress)
	}
	return progress, err
}
