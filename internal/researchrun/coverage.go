package researchrun

import "github.com/injoyai/logs"

// LogCoverage writes the same bounded coverage summary for every experiment
// command. Full failure details remain available in Coverage for JSON reports.
func LogCoverage(coverage Coverage) {
	logs.Infof("数据覆盖: 完成 %d/%d，跳过 %d", coverage.Completed, coverage.Requested, coverage.Skipped)
	for i, failure := range coverage.Failures {
		if i == 5 {
			logs.Warnf("另有 %d 个数据失败未展开", len(coverage.Failures)-i)
			break
		}
		logs.Warnf("跳过 %s(%d) [%s]: %s", failure.Code, failure.Year, failure.Stage, failure.Message)
	}
}
