package report

import (
	"fmt"
	"math"
	"os"
	"path/filepath"

	"github.com/injoyai/logs"
	"github.com/injoyai/strategy-tail/core"
	"github.com/signintech/gopdf"
)

// ============================================================================
// 回测 PDF 报告渲染（手机查看专用，A4 竖版）
// ============================================================================
// 抽取自 cmd/backtest_macd_smooth 与 cmd/backtest_macd_green 的重复实现。
// 布局、配色、表格/卡片/审计/蒙特卡洛绘制为共享逻辑；
// 策略说明文案与结论建议由各 cmd 通过 Options 注入。

// ReportData 汇总回测结果，供 PDF 报告渲染。
type ReportData struct {
	StrategyName string
	BuyerName    string
	SellerName   string
	Benchmark    string
	Years        []int
	Results      []core.AnalyzeResult
	AllTrades    []core.Trade
	MC           core.MonteCarloResult
	Audit        core.AuditResult
	Cost         core.Cost
	Position     core.PositionConfig
	GeneratedAt  string
}

// Options 策略专属的报告配置，由各 cmd 提供。
type Options struct {
	// OutputDir 报告输出目录，例 output/backtest-macd-smooth
	OutputDir string
	// Filename PDF 文件名（可含参数标识以避免不同配置互相覆盖）
	Filename string
	// StrategyDesc 策略说明卡片的文本行
	StrategyDesc []string
	// Advice 返回"结论与建议"文本行（可按 r 动态生成，如总交易笔数）
	Advice func(r *ReportData) []string
}

// PDF 布局常量（单位 mm）
const (
	pdfMarginX   = 12.0
	pdfMarginTop = 14.0
	pdfPageW     = 210.0
	pdfPageH     = 297.0
	pdfContentW  = pdfPageW - pdfMarginX*2 // 186mm
	pdfFont      = "simhei"
	pdfFontPath  = `C:\Windows\Fonts\simhei.ttf`
)

// 颜色（A 股惯例：红涨绿跌）
var (
	colRed    = [3]uint8{220, 38, 38}
	colGreen  = [3]uint8{22, 163, 74}
	colInk    = [3]uint8{26, 26, 46}
	colMuted  = [3]uint8{107, 114, 128}
	colAccent = [3]uint8{59, 130, 246}
	colHead   = [3]uint8{249, 250, 251}
	colLine   = [3]uint8{229, 231, 235}
	colCard   = [3]uint8{248, 249, 251}
	colAmber  = [3]uint8{245, 158, 11}
	colWhite  = [3]uint8{255, 255, 255}
)

// ExportPDF 导出 PDF 报告到 opt.OutputDir/opt.Filename。
func ExportPDF(r *ReportData, opt Options) error {
	pdf := &gopdf.GoPdf{}
	pdf.Start(gopdf.Config{
		PageSize: gopdf.Rect{W: pdfPageW, H: pdfPageH},
		Unit:     gopdf.UnitMM,
	})
	if err := pdf.AddTTFFontWithOption(pdfFont, pdfFontPath, gopdf.TtfOption{
		UseKerning: true,
	}); err != nil {
		return fmt.Errorf("加载字体失败: %w", err)
	}

	pdf.AddPage()
	y := pdfMarginTop

	// ===== 标题区 =====
	y = drawHeader(pdf, r, y)
	y += 4

	// ===== 策略说明 =====
	y = drawStrategyInfo(pdf, opt.StrategyDesc, y)
	y += 5

	// ===== 概要卡片 =====
	y = drawSummaryCards(pdf, r, y)
	y += 5

	// ===== 年度指标表 =====
	y = ensureSpace(pdf, y, 50)
	y = drawSectionTitle(pdf, y, "一、年度回测指标", "")
	y += 2
	y = drawYearlyTable(pdf, y, r)
	y += 5

	// ===== 风险调整指标表 =====
	y = ensureSpace(pdf, y, 40)
	y = drawSectionTitle(pdf, y, "二、风险调整与基准对比", "")
	y += 2
	y = drawRiskTable(pdf, y, r)
	y += 5

	// ===== 蒙特卡洛模拟 =====
	y = ensureSpace(pdf, y, 40)
	y = drawSectionTitle(pdf, y, "三、蒙特卡洛稳健性模拟", "")
	y += 2
	y = drawMonteCarlo(pdf, y, r.MC)
	y += 5

	// ===== 前视偏差审计 =====
	y = ensureSpace(pdf, y, 36)
	y = drawSectionTitle(pdf, y, "四、前视偏差审计", "")
	y += 2
	y = drawAudit(pdf, y, r.Audit)
	y += 5

	// ===== 结论与建议 =====
	y = ensureSpace(pdf, y, 40)
	y = drawSectionTitle(pdf, y, "五、结论与建议", "")
	y += 2
	y = drawAdvice(pdf, y, opt, r)

	// ===== 写文件 =====
	os.MkdirAll(opt.OutputDir, 0755)
	output := filepath.Join(opt.OutputDir, opt.Filename)
	if err := pdf.WritePdf(output); err != nil {
		return fmt.Errorf("写入PDF失败: %w", err)
	}
	logs.Info("PDF报告已生成: " + output)
	return nil
}

// ---------- 绘制辅助 ----------

func setFont(pdf *gopdf.GoPdf, size float64) {
	pdf.SetFont(pdfFont, "", size)
}

func setTextColor(pdf *gopdf.GoPdf, c [3]uint8) {
	pdf.SetTextColor(c[0], c[1], c[2])
}

func setFillColor(pdf *gopdf.GoPdf, c [3]uint8) {
	pdf.SetFillColor(c[0], c[1], c[2])
}

// ensureSpace 检查剩余空间，不足则换页
func ensureSpace(pdf *gopdf.GoPdf, y, needed float64) float64 {
	if y+needed > pdfPageH-pdfMarginTop-10 {
		pdf.AddPage()
		return pdfMarginTop
	}
	return y
}

// formatYearsLabel 年份列表格式化为 "2022-2023-2024" 形式。
func formatYearsLabel(years []int) string {
	s := ""
	for i, y := range years {
		if i > 0 {
			s += "-"
		}
		s += fmt.Sprintf("%d", y)
	}
	return s
}

// drawHeader 标题区
func drawHeader(pdf *gopdf.GoPdf, r *ReportData, y float64) float64 {
	// 深色背景
	setFillColor(pdf, [3]uint8{30, 41, 59})
	pdf.Rectangle(pdfMarginX, y, pdfMarginX+pdfContentW, y+32, "F", 0, 0)
	// 标题
	setFont(pdf, 16)
	setTextColor(pdf, colWhite)
	pdf.SetX(pdfMarginX + 8)
	pdf.SetY(y + 6)
	pdf.Cell(nil, r.StrategyName+" 回测报告")
	// 副标题：买入逻辑摘要
	setFont(pdf, 9)
	pdf.SetX(pdfMarginX + 8)
	pdf.SetY(y + 16)
	pdf.Cell(nil, "买入: "+r.BuyerName)
	// 元信息
	setFont(pdf, 8)
	pdf.SetX(pdfMarginX + 8)
	pdf.SetY(y + 23)
	pdf.Cell(nil, fmt.Sprintf("基准: %s ｜ 年份: %s ｜ 生成日期: %s",
		r.Benchmark, formatYearsLabel(r.Years), r.GeneratedAt))
	return y + 32
}

// drawStrategyInfo 策略说明卡片
func drawStrategyInfo(pdf *gopdf.GoPdf, lines []string, y float64) float64 {
	y = ensureSpace(pdf, y, 30)
	setFillColor(pdf, colCard)
	h := 26.0
	pdf.Rectangle(pdfMarginX, y, pdfMarginX+pdfContentW, y+h, "F", 0, 0)
	setFont(pdf, 8)
	yy := y + 3.5
	for _, line := range lines {
		setTextColor(pdf, colInk)
		pdf.SetX(pdfMarginX + 4)
		pdf.SetY(yy)
		pdf.Cell(nil, line)
		yy += 5
	}
	return y + h
}

// drawSummaryCards 概要卡片
func drawSummaryCards(pdf *gopdf.GoPdf, r *ReportData, y float64) float64 {
	totalTrades := 0
	winRate := 0.0
	avgProfit := 0.0
	profitFactor := 0.0
	yearCount := 0
	if len(r.Results) > 0 {
		for _, res := range r.Results {
			if res.Year == 0 {
				continue // 跳过全周期合计行
			}
			totalTrades += res.TotalTrades
			winRate += res.WinRate
			avgProfit += res.AvgProfit
			profitFactor += res.ProfitFactor
			yearCount++
		}
		if yearCount > 0 {
			n := float64(yearCount)
			winRate /= n
			avgProfit /= n
			profitFactor /= n
		}
	}

	cards := []struct {
		label string
		value string
	}{
		{"总交易笔数", fmt.Sprintf("%d", totalTrades)},
		{"平均胜率", fmt.Sprintf("%.1f%%", winRate)},
		{"平均单笔收益", fmt.Sprintf("%.2f%%", avgProfit)},
		{"平均盈亏比", fmt.Sprintf("%.2f", profitFactor)},
	}
	cardW := pdfContentW / float64(len(cards))
	cardH := 16.0
	for i, c := range cards {
		x := pdfMarginX + float64(i)*cardW
		setFillColor(pdf, colCard)
		pdf.Rectangle(x, y, x+cardW-2, y+cardH, "F", 0, 0)
		setFont(pdf, 7)
		setTextColor(pdf, colMuted)
		pdf.SetX(x + 3)
		pdf.SetY(y + 2.5)
		pdf.Cell(nil, c.label)
		setFont(pdf, 12)
		setTextColor(pdf, colInk)
		pdf.SetX(x + 3)
		pdf.SetY(y + 7)
		pdf.Cell(nil, c.value)
	}
	return y + cardH
}

// drawSectionTitle 章节标题
func drawSectionTitle(pdf *gopdf.GoPdf, y float64, title, badge string) float64 {
	setFont(pdf, 11)
	setTextColor(pdf, colInk)
	pdf.SetX(pdfMarginX)
	pdf.SetY(y)
	pdf.Cell(nil, title)
	if badge != "" {
		tw, _ := pdf.MeasureTextWidth(title)
		bx := pdfMarginX + tw + 3
		setFillColor(pdf, colAccent)
		pdf.Rectangle(bx, y+0.5, bx+12, y+5.5, "F", 0, 0)
		setFont(pdf, 7)
		setTextColor(pdf, colWhite)
		pdf.SetX(bx + 1)
		pdf.SetY(y + 1.5)
		pdf.Cell(nil, badge)
	}
	// 下划线
	pdf.SetStrokeColor(colLine[0], colLine[1], colLine[2])
	pdf.SetLineWidth(0.3)
	pdf.Line(pdfMarginX, y+6.5, pdfMarginX+pdfContentW, y+6.5)
	return y + 7
}

// drawTableRow 画一行表格
func drawTableRow(pdf *gopdf.GoPdf, y float64, cells []string, colW []float64, bg [3]uint8, textColor [3]uint8, size float64) {
	x := pdfMarginX
	setFillColor(pdf, bg)
	pdf.Rectangle(x, y, x+pdfContentW, y+6, "F", 0, 0)
	setFont(pdf, size)
	setTextColor(pdf, textColor)
	for i, c := range cells {
		pdf.SetStrokeColor(colLine[0], colLine[1], colLine[2])
		pdf.SetLineWidth(0.2)
		pdf.Line(x, y, x, y+6)
		pdf.SetX(x + 1)
		pdf.SetY(y + 1.5)
		tw, _ := pdf.MeasureTextWidth(c)
		offset := (colW[i] - tw) / 2
		if offset < 0.5 {
			offset = 0.5
		}
		pdf.SetX(x + offset)
		pdf.SetY(y + 1.5)
		pdf.Cell(nil, c)
		x += colW[i]
	}
	pdf.Line(x, y, x, y+6)
	pdf.Line(pdfMarginX, y, pdfMarginX+pdfContentW, y)
	pdf.Line(pdfMarginX, y+6, pdfMarginX+pdfContentW, y+6)
}

// fmtPF 盈亏比格式化（∞ 处理）
func fmtPF(v float64) string {
	if math.IsInf(v, 1) {
		return "∞"
	}
	return fmt.Sprintf("%.2f", v)
}

// yearLabel 年份显示（Year==0 为全周期合计行，与 smooth 原版文案一致）
func yearLabel(year int) string {
	if year == 0 {
		return "五年合计"
	}
	return fmt.Sprintf("%d", year)
}

// drawYearlyTable 年度基础指标表
func drawYearlyTable(pdf *gopdf.GoPdf, y float64, r *ReportData) float64 {
	headers := []string{"年份", "交易", "胜率", "总盈亏", "平均收益", "最大收益", "最大亏损", "盈亏比", "最大回撤"}
	colW := []float64{18, 18, 20, 24, 24, 22, 22, 20, 18}
	rowH := 6.5
	needed := 10 + float64(len(r.Results))*rowH
	y = ensureSpace(pdf, y, needed)

	drawTableRow(pdf, y, headers, colW, colHead, colMuted, 8.5)
	y += 6

	for i := range r.Results {
		res := r.Results[i]
		y = ensureSpace(pdf, y, 8)
		row := []string{
			yearLabel(res.Year),
			fmt.Sprintf("%d", res.TotalTrades),
			fmt.Sprintf("%.1f%%", res.WinRate),
			fmt.Sprintf("%.0f", res.TotalProfit),
			fmt.Sprintf("%.2f%%", res.AvgProfit),
			fmt.Sprintf("%.2f%%", res.MaxProfit),
			fmt.Sprintf("%.2f%%", res.MaxLoss),
			fmtPF(res.ProfitFactor),
			fmt.Sprintf("%.1f%%", res.MaxDrawdownPct),
		}
		rowColor := colInk
		if res.AvgProfit > 0 {
			rowColor = colRed
		} else if res.AvgProfit < 0 {
			rowColor = colGreen
		}
		bgColor := colWhite
		if res.Year == 0 {
			bgColor = colHead
		}
		drawTableRow(pdf, y, row, colW, bgColor, rowColor, 8.5)
		y += rowH
	}
	return y
}

// drawRiskTable 风险调整指标表
func drawRiskTable(pdf *gopdf.GoPdf, y float64, r *ReportData) float64 {
	headers := []string{"年份", "年化收益", "Sharpe", "Sortino", "Calmar", "基准收益", "Alpha", "Beta", "最大回撤"}
	colW := []float64{18, 22, 20, 20, 20, 22, 20, 18, 24}
	rowH := 6.5
	needed := 10 + float64(len(r.Results))*rowH
	y = ensureSpace(pdf, y, needed)

	drawTableRow(pdf, y, headers, colW, colHead, colMuted, 8.5)
	y += 6

	for i := range r.Results {
		res := r.Results[i]
		y = ensureSpace(pdf, y, 8)
		row := []string{
			yearLabel(res.Year),
			fmt.Sprintf("%.1f%%", res.AnnualReturn),
			fmt.Sprintf("%.2f", res.Sharpe),
			fmt.Sprintf("%.2f", res.Sortino),
			fmt.Sprintf("%.2f", res.Calmar),
			fmt.Sprintf("%.1f%%", res.BenchReturn),
			fmt.Sprintf("%.2f%%", res.Alpha),
			fmt.Sprintf("%.2f", res.Beta),
			fmt.Sprintf("%.1f%%", res.MaxDrawdownPct),
		}
		rowColor := colInk
		if res.AnnualReturn > 0 {
			rowColor = colRed
		} else if res.AnnualReturn < 0 {
			rowColor = colGreen
		}
		bgColor := colWhite
		if res.Year == 0 {
			bgColor = colHead
		}
		drawTableRow(pdf, y, row, colW, bgColor, rowColor, 8)
		y += rowH
	}
	return y
}

// drawMonteCarlo 蒙特卡洛模拟结果
func drawMonteCarlo(pdf *gopdf.GoPdf, y float64, mc core.MonteCarloResult) float64 {
	if mc.ProbProfit == 0 && mc.ReturnP50 == 0 {
		y = ensureSpace(pdf, y, 10)
		setFont(pdf, 9)
		setTextColor(pdf, colMuted)
		pdf.SetX(pdfMarginX + 2)
		pdf.SetY(y)
		pdf.Cell(nil, "交易笔数不足，未进行蒙特卡洛模拟（需 >10 笔）。")
		return y + 8
	}

	// 表格：百分位收益 + 回撤
	headers := []string{"指标", "P5", "P25", "P50", "P75", "P95"}
	colW := []float64{40, 29.2, 29.2, 29.2, 29.2, 29.2}
	rows := [][]string{
		{"最终收益率", fmt.Sprintf("%.1f%%", mc.ReturnP5), fmt.Sprintf("%.1f%%", mc.ReturnP25),
			fmt.Sprintf("%.1f%%", mc.ReturnP50), fmt.Sprintf("%.1f%%", mc.ReturnP75), fmt.Sprintf("%.1f%%", mc.ReturnP95)},
		{"最大回撤", "—", "—", fmt.Sprintf("%.1f%%", mc.MaxDrawdownP50), "—", fmt.Sprintf("%.1f%%", mc.MaxDrawdownP95)},
	}
	needed := 12 + float64(len(rows))*7
	y = ensureSpace(pdf, y, needed+8)

	drawTableRow(pdf, y, headers, colW, colHead, colMuted, 8.5)
	y += 6
	rowColor := colRed
	if mc.ReturnP50 < 0 {
		rowColor = colGreen
	}
	drawTableRow(pdf, y, rows[0], colW, colWhite, rowColor, 8.5)
	y += 7
	drawTableRow(pdf, y, rows[1], colW, colWhite, colInk, 8.5)
	y += 9

	// 概率卡片
	cards := []struct {
		label string
		value string
	}{
		{"盈利概率", fmt.Sprintf("%.1f%%", mc.ProbProfit*100)},
		{"破产概率(亏>20%)", fmt.Sprintf("%.2f%%", mc.ProbRuin*100)},
	}
	cardW := pdfContentW/2 - 2
	cardH := 14.0
	for i, c := range cards {
		x := pdfMarginX + float64(i)*(cardW+4)
		setFillColor(pdf, colCard)
		pdf.Rectangle(x, y, x+cardW, y+cardH, "F", 0, 0)
		setFont(pdf, 7.5)
		setTextColor(pdf, colMuted)
		pdf.SetX(x + 4)
		pdf.SetY(y + 2)
		pdf.Cell(nil, c.label)
		setFont(pdf, 13)
		if c.label == "盈利概率" {
			setTextColor(pdf, colRed)
		} else {
			setTextColor(pdf, colGreen)
		}
		pdf.SetX(x + 4)
		pdf.SetY(y + 6)
		pdf.Cell(nil, c.value)
	}
	// 方法说明
	y += cardH + 1
	setFont(pdf, 7)
	setTextColor(pdf, colMuted)
	pdf.SetX(pdfMarginX + 2)
	pdf.SetY(y)
	pdf.Cell(nil, "方法: 对历史逐笔收益率做 1000 次有放回重采样(bootstrap)后复利累计, 得到最终收益与回撤的经验分布。")
	return y + 7
}

// drawAudit 前视偏差审计
func drawAudit(pdf *gopdf.GoPdf, y float64, audit core.AuditResult) float64 {
	y = ensureSpace(pdf, y, 12)
	// 状态行
	status := "通过 ✓"
	statusColor := colGreen
	if !audit.Passed {
		status = "发现问题"
		statusColor = colAmber
	}
	setFont(pdf, 10)
	setTextColor(pdf, statusColor)
	pdf.SetX(pdfMarginX + 2)
	pdf.SetY(y)
	pdf.Cell(nil, "审计状态: "+status)
	y += 6

	setFont(pdf, 8.5)
	setTextColor(pdf, colInk)
	info := fmt.Sprintf("策略: %s ｜ 交易笔数: %d ｜ 检查数据点: %d ｜ 问题数: %d",
		audit.StrategyName, audit.TradeCount, audit.DataPointsChecked, len(audit.Issues))
	pdf.SetX(pdfMarginX + 2)
	pdf.SetY(y)
	pdf.Cell(nil, info)
	y += 6

	if len(audit.Issues) > 0 {
		y = ensureSpace(pdf, y, 6*float64(len(audit.Issues)))
		setFont(pdf, 8)
		setTextColor(pdf, colAmber)
		for _, issue := range audit.Issues {
			pdf.SetX(pdfMarginX + 2)
			pdf.SetY(y)
			pdf.Cell(nil, "• "+issue)
			y += 5
		}
	}
	return y
}

// drawAdvice 结论与建议
func drawAdvice(pdf *gopdf.GoPdf, y float64, opt Options, r *ReportData) float64 {
	var lines []string
	if opt.Advice != nil {
		lines = opt.Advice(r)
	}
	for _, a := range lines {
		y = ensureSpace(pdf, y, 7)
		setFont(pdf, 8.5)
		setTextColor(pdf, colInk)
		pdf.SetX(pdfMarginX + 2)
		pdf.SetY(y)
		pdf.Cell(nil, a)
		y += 5.5
	}
	return y
}
