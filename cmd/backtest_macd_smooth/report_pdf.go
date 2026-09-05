package main

import (
	"fmt"
	"path/filepath"

	"github.com/injoyai/strategy-tail/lib/report"
)

// ============================================================================
// PDF 报告策略专属配置（共享布局与绘制逻辑见 lib/report）
// ============================================================================

// exportReport 生成 MACD量柱流畅策略 PDF 报告。
func exportReport(r *ReportData) error {
	// 带年份与参数标识的文件名，避免不同配置互相覆盖
	yearsLabel := ""
	for i, y := range r.Years {
		if i > 0 {
			yearsLabel += "-"
		}
		yearsLabel += fmt.Sprintf("%d", y)
	}
	paramLabel := fmt.Sprintf("S%dD%dR%d", *flagSmooth, *flagDays, *flagRev)

	return report.ExportPDF(r, report.Options{
		OutputDir: filepath.Join("output", "backtest-macd-smooth"),
		Filename:  "MACD量柱流畅_" + yearsLabel + "_" + paramLabel + "回测报告.pdf",
		StrategyDesc: []string{
			"策略逻辑: MACD 量柱经 EMA(5) 平滑后最近 10 天方向反转<=1次(走势流畅),",
			"量柱低位拐头(今天变大且昨天为近4日最低点), 同时 30 日均线向上(中期趋势确认),",
			"三重共振作为买点。卖出使用默认 MACD 反转卖出策略(无盈利等二浪 / 有盈利即反转卖出)。",
			"参考: 量柱流畅度过滤可排除锯齿形伪信号, 叠加均线趋势与 MACD 拐头共振提高胜率。",
		},
		Advice: buildAdvice,
	})
}

// buildAdvice 结论与建议文本。
func buildAdvice(r *report.ReportData) []string {
	lines := []string{
		"1. 量柱流畅度(MACD顺滑)过滤锯齿形伪信号, 叠加 MACD 低位反转拐头与 30 日均线上行, 三重共振提高买点质量。",
		"2. 卖出使用默认 MACD 反转策略, 无盈利等二次上升浪, 有盈利(>0.5%)则在反转时卖出。",
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
		"4. 若回测表现出色(年化>15%且回撤<20%), 可将该组合固化为常用策略; 若表现平淡, 可调整平滑周期或反转回看窗口。",
		"5. 免责声明: 本报告为历史数据回测, 不代表未来收益, 实盘需结合仓位管理与市场环境。",
	)
	return lines
}
