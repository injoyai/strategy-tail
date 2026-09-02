package main

import (
	"fmt"
	"path/filepath"

	"github.com/injoyai/strategy-tail/lib/report"
)

// ============================================================================
// PDF 报告策略专属配置（共享布局与绘制逻辑见 lib/report）
// ============================================================================

// exportReport 生成下跌企稳·MACD绿柱缩短转红策略 PDF 报告。
func exportReport(r *ReportData) error {
	return report.ExportPDF(r, report.Options{
		OutputDir: filepath.Join("output", "backtest-macd-green"),
		Filename:  "report.pdf",
		StrategyDesc: []string{
			"策略逻辑: 下跌趋势(收盘<60日均线)中 MACD 出现大量绿柱(负柱)且逐日收窄(空头动能衰竭),",
			"股价企稳(阳线+收涨+不创新低), 量柱由负转红当日买入, 并叠加放量确认(>5日均量1.5倍)。",
			"卖出: 止盈15%/止损10% 让利润奔跑 + 盈利后追踪止损8% + MACD 红柱拐头离场。",
			"参考: 社区经典「绿柱缩短抄底 + 零轴下金叉需企稳确认」(柱线变化领先金叉 3-5 根 K 线)。",
		},
		Advice: buildAdvice,
	})
}

// buildAdvice 结论与建议文本。
func buildAdvice(r *report.ReportData) []string {
	lines := []string{
		"1. 以 60 日均线作为「下跌趋势」判定，收盘价低于 60 日线才允许买入，避免追高。",
		"2. 绿柱连续收窄(空头衰竭) + 企稳确认 + 量柱转红 + 放量确认四重共振作为买点, 有效过滤假信号。",
	}
	if len(r.Results) > 0 {
		total := 0
		for _, res := range r.Results {
			if res.Year == 0 {
				continue // 跳过全周期合计行
			}
			total += res.TotalTrades
		}
		lines = append(lines, fmt.Sprintf("3. 全样本 %d 笔交易, 建议结合盈亏比与胜率评估期望收益, 单笔盈亏比>2 时系统为正期望。", total))
	}
	lines = append(lines,
		"4. 若回测表现出色(年化>15%且回撤<20%), 可将该组合固化为常用策略; 若表现平淡, 可放宽止盈或缩短绿柱收窄天数。",
		"5. 免责声明: 本报告为历史数据回测, 不代表未来收益, 实盘需结合仓位管理与市场环境。",
	)
	return lines
}
