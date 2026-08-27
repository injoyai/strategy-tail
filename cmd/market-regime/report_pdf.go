package main

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"time"

	"github.com/injoyai/logs"
	"github.com/signintech/gopdf"
)

// ============================================================================
// PDF 报告生成（手机查看专用，A4 竖版）
// ============================================================================

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
	colRed   = [3]uint8{220, 38, 38}
	colGreen = [3]uint8{22, 163, 74}
	colInk   = [3]uint8{26, 26, 46}
	colMuted = [3]uint8{107, 114, 128}
	colAccent= [3]uint8{59, 130, 246}
	colHead  = [3]uint8{249, 250, 251}
	colLine  = [3]uint8{229, 231, 235}
	colCard  = [3]uint8{248, 249, 251}
	colAmber = [3]uint8{245, 158, 11}
	colWhite = [3]uint8{255, 255, 255}
)

// ExportPDF 导出 PDF 报告到 output/market-regime/report.pdf
func ExportPDF(r *AnalysisResult) {
	pdf := &gopdf.GoPdf{}
	pdf.Start(gopdf.Config{
		PageSize: gopdf.Rect{W: pdfPageW, H: pdfPageH},
		Unit:     gopdf.UnitMM,
	})
	if err := pdf.AddTTFFontWithOption(pdfFont, pdfFontPath, gopdf.TtfOption{
		UseKerning: true,
	}); err != nil {
		logs.Errorf("加载字体失败: %v", err)
		return
	}

	pdf.AddPage()
	y := pdfMarginTop

	// ===== 标题区 =====
	y = drawHeader(pdf, r, y)
	y += 4

	// ===== 概要卡片 =====
	y = drawSummaryCards(pdf, r, y)
	y += 5

	// ===== 关键发现 =====
	best, worst := FindBestWorst(r)
	y = drawFindings(pdf, r, best, worst, y)
	y += 5

	// ===== 各维度分组统计 =====
	y = drawSectionTitle(pdf, y, "一、各维度分组统计", "11 维度")
	y += 2
	for _, dr := range r.DimensionResults {
		y = drawDimensionTable(pdf, y, dr)
		y += 4
	}

	// ===== 年度×综合状态 交叉表 =====
	y = ensureSpace(pdf, y, 30)
	y = drawSectionTitle(pdf, y, "二、年度×综合状态 交叉统计", "")
	y += 2
	y = drawYearlyTable(pdf, y, r)
	y += 5

	// ===== 综合状态×月份 平均收益 =====
	y = ensureSpace(pdf, y, 30)
	y = drawSectionTitle(pdf, y, "三、综合状态×月份 平均收益(%)", "")
	y += 2
	y = drawMonthlyTable(pdf, y, r)
	y += 5

	// ===== 操作建议 =====
	y = ensureSpace(pdf, y, 40)
	y = drawSectionTitle(pdf, y, "四、操作建议", "")
	y += 2
	y = drawAdvice(pdf, y, best, worst)

	// ===== 写文件 =====
	dir := filepath.Join("output", "market-regime")
	os.MkdirAll(dir, 0755)
	output := filepath.Join(dir, "report.pdf")
	if err := pdf.WritePdf(output); err != nil {
		logs.Errorf("写入PDF失败: %v", err)
		return
	}
	logs.Info("PDF报告已生成: " + output)
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

// drawHeader 标题区
func drawHeader(pdf *gopdf.GoPdf, r *AnalysisResult, y float64) float64 {
	// 深色背景
	setFillColor(pdf, [3]uint8{30, 41, 59})
	pdf.Rectangle(pdfMarginX, y, pdfMarginX+pdfContentW, y+32, "F", 0, 0)
	// 标题
	setFont(pdf, 16)
	setTextColor(pdf, colWhite)
	pdf.SetX(pdfMarginX + 8)
	pdf.SetY(y + 6)
	pdf.Cell(nil, "策略 × 大盘状态 分析报告")
	// 副标题
	setFont(pdf, 10)
	pdf.SetX(pdfMarginX + 8)
	pdf.SetY(y + 16)
	pdf.Cell(nil, "策略："+r.StrategyName)
	// 元信息
	pdf.SetX(pdfMarginX + 8)
	pdf.SetY(y + 23)
	pdf.Cell(nil, fmt.Sprintf("基准：%s ｜ 年份：%d-%d ｜ 生成日期：%s",
		r.Benchmark, r.Years[0], r.Years[len(r.Years)-1], time.Now().Format("2006-01-02")))
	return y + 32
}

// drawSummaryCards 概要卡片
func drawSummaryCards(pdf *gopdf.GoPdf, r *AnalysisResult, y float64) float64 {
	cards := []struct {
		label string
		value string
	}{
		{"总交易笔数", fmt.Sprintf("%d", r.TotalTrades)},
		{"匹配大盘数据", fmt.Sprintf("%d", r.MatchedTrades)},
		{"匹配率", fmt.Sprintf("%.1f%%", safeDiv(r.MatchedTrades*100, r.TotalTrades))},
		{"分析年份", fmt.Sprintf("%d-%d", r.Years[0], r.Years[len(r.Years)-1])},
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

// drawFindings 关键发现
func drawFindings(pdf *gopdf.GoPdf, r *AnalysisResult, best, worst GroupStat, y float64) float64 {
	y = ensureSpace(pdf, y, 28)
	// 标题
	setFont(pdf, 11)
	setTextColor(pdf, colInk)
	pdf.SetX(pdfMarginX)
	pdf.SetY(y)
	pdf.Cell(nil, "★ 关键发现")
	y += 6

	// 最佳环境卡片
	drawFindingCard(pdf, pdfMarginX, y, "最佳市场环境", best, colRed)
	// 最差环境卡片
	drawFindingCard(pdf, pdfMarginX+pdfContentW/2, y, "最差市场环境", worst, colGreen)
	return y + 20
}

func drawFindingCard(pdf *gopdf.GoPdf, x, y float64, title string, g GroupStat, color [3]uint8) {
	w := pdfContentW/2 - 2
	h := 20.0
	setFillColor(pdf, colWhite)
	pdf.Rectangle(x, y, x+w, y+h, "F", 0, 0)
	// 左侧色条
	setFillColor(pdf, color)
	pdf.Rectangle(x, y, x+1.2, y+h, "F", 0, 0)

	setFont(pdf, 7)
	setTextColor(pdf, colMuted)
	pdf.SetX(x + 3)
	pdf.SetY(y + 2)
	pdf.Cell(nil, title)

	setFont(pdf, 9)
	setTextColor(pdf, colInk)
	pdf.SetX(x + 3)
	pdf.SetY(y + 6.5)
	label := g.Label
	if label == "" {
		label = "—"
	}
	pdf.Cell(nil, fmt.Sprintf("[%s] %s", g.Dimension, label))

	setFont(pdf, 8)
	pdf.SetX(x + 3)
	pdf.SetY(y + 12)
	pf := fmt.Sprintf("%.2f", g.ProfitFactor)
	if math.IsInf(g.ProfitFactor, 1) {
		pf = "∞"
	}
	pdf.Cell(nil, fmt.Sprintf("笔数=%d  胜率=%.1f%%  均收=%.2f%%  盈亏比=%s",
		g.Count, g.WinRate, g.AvgProfit, pf))

	setFont(pdf, 7)
	setTextColor(pdf, colMuted)
	pdf.SetX(x + 3)
	pdf.SetY(y + 16.5)
	pdf.Cell(nil, fmt.Sprintf("最大收益=%.2f%%  最大亏损=%.2f%%", g.MaxProfit, g.MaxLoss))
}

// drawSectionTitle 章节标题
func drawSectionTitle(pdf *gopdf.GoPdf, y float64, title, badge string) float64 {
	setFont(pdf, 11)
	setTextColor(pdf, colInk)
	pdf.SetX(pdfMarginX)
	pdf.SetY(y)
	pdf.Cell(nil, title)
	if badge != "" {
		// badge 背景
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

// drawDimensionTable 单个维度表格
func drawDimensionTable(pdf *gopdf.GoPdf, y float64, dr DimensionResult) float64 {
	// 表头 + 每行 ~6mm，预估高度
	rows := 0
	for _, g := range dr.Groups {
		if g.Label == "(无数据)" {
			continue
		}
		rows++
	}
	needed := 14 + float64(rows)*6.5
	y = ensureSpace(pdf, y, needed)

	// 维度名
	setFont(pdf, 9)
	setTextColor(pdf, colAccent)
	pdf.SetX(pdfMarginX)
	pdf.SetY(y)
	pdf.Cell(nil, "▸ "+dr.Dimension)
	y += 5

	// 表头
	headers := []string{"标签", "笔数", "胜率%", "平均收益", "盈亏比", "最大收益", "最大亏损"}
	colW := []float64{40, 18, 22, 28, 22, 28, 28}
	drawTableRow(pdf, y, headers, colW, colHead, colMuted, 8, true)
	y += 6
	// 数据行
	for _, g := range dr.Groups {
		if g.Label == "(无数据)" {
			continue
		}
		pf := fmt.Sprintf("%.2f", g.ProfitFactor)
		if math.IsInf(g.ProfitFactor, 1) {
			pf = "∞"
		}
		row := []string{
			g.Label,
			fmt.Sprintf("%d", g.Count),
			fmt.Sprintf("%.1f", g.WinRate),
			fmt.Sprintf("%.2f%%", g.AvgProfit),
			pf,
			fmt.Sprintf("%.2f%%", g.MaxProfit),
			fmt.Sprintf("%.2f%%", g.MaxLoss),
		}
		// 颜色：正收益红、负收益绿
		rowColor := colInk
		if g.AvgProfit > 0 {
			rowColor = colRed
		} else if g.AvgProfit < 0 {
			rowColor = colGreen
		}
		y = ensureSpace(pdf, y, 8)
		drawTableRow(pdf, y, row, colW, colWhite, rowColor, 8, false)
		y += 6
	}
	return y
}

// drawTableRow 画一行表格
func drawTableRow(pdf *gopdf.GoPdf, y float64, cells []string, colW []float64, bg [3]uint8, textColor [3]uint8, size float64, bold bool) {
	x := pdfMarginX
	setFillColor(pdf, bg)
	pdf.Rectangle(x, y, x+pdfContentW, y+6, "F", 0, 0)
	setFont(pdf, size)
	setTextColor(pdf, textColor)
	for i, c := range cells {
		// 画竖线
		pdf.SetStrokeColor(colLine[0], colLine[1], colLine[2])
		pdf.SetLineWidth(0.2)
		pdf.Line(x, y, x, y+6)
		// 文字（居中）
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
	// 最后竖线
	pdf.Line(x, y, x, y+6)
	// 上下横线
	pdf.Line(pdfMarginX, y, pdfMarginX+pdfContentW, y)
	pdf.Line(pdfMarginX, y+6, pdfMarginX+pdfContentW, y+6)
}

// drawYearlyTable 年度×综合状态 交叉表
func drawYearlyTable(pdf *gopdf.GoPdf, y float64, r *AnalysisResult) float64 {
	composites := []string{"强势", "弱势", "震荡"}
	needed := 12 + float64(len(r.Years))*7
	y = ensureSpace(pdf, y, needed)

	// 表头
	headers := []string{"年份"}
	colW := []float64{30}
	for range composites {
		headers = append(headers, "笔数 胜率 均收")
		colW = append(colW, 52)
	}
	drawTableRow(pdf, y, headers, colW, colHead, colMuted, 8, true)
	y += 6

	for _, yr := range r.Years {
		y = ensureSpace(pdf, y, 8)
		cells := r.YearlyComposite[yr]
		row := []string{fmt.Sprintf("%d", yr)}
		for _, c := range composites {
			g, ok := cells[c]
			if !ok || g.Count == 0 {
				row = append(row, "— — —")
			} else {
				row = append(row, fmt.Sprintf("%d  %.0f%%  %.2f%%", g.Count, g.WinRate, g.AvgProfit))
			}
		}
		// 整行用默认色，但根据强势/弱势着色文字
		// 简化：用 ink 色
		drawTableRow(pdf, y, row, colW, colWhite, colInk, 7.5, false)
		y += 6
	}
	return y
}

// drawMonthlyTable 综合状态×月份 平均收益
func drawMonthlyTable(pdf *gopdf.GoPdf, y float64, r *AnalysisResult) float64 {
	composites := []string{"强势", "弱势", "震荡"}
	needed := 12 + float64(len(composites)+1)*7
	y = ensureSpace(pdf, y, needed)

	// 表头：月份 1-12
	headers := []string{"状态"}
	colW := []float64{22}
	for m := 1; m <= 12; m++ {
		headers = append(headers, fmt.Sprintf("%d月", m))
		colW = append(colW, 13.7)
	}
	drawTableRow(pdf, y, headers, colW, colHead, colMuted, 7, true)
	y += 6

	for _, c := range composites {
		y = ensureSpace(pdf, y, 8)
		months := r.MonthlyComposite[c]
		row := []string{c}
		for m := 1; m <= 12; m++ {
			v, ok := months[m]
			if !ok {
				row = append(row, "—")
			} else {
				row = append(row, fmt.Sprintf("%.1f", v))
			}
		}
		// 根据状态选色
		color := colInk
		switch c {
		case "强势":
			color = colRed
		case "弱势":
			color = colGreen
		}
		drawTableRow(pdf, y, row, colW, colWhite, color, 7, false)
		y += 6
	}
	return y
}

// drawAdvice 操作建议
func drawAdvice(pdf *gopdf.GoPdf, y float64, best, worst GroupStat) float64 {
	advices := []string{
		"1. 在「综合=弱势」或「均线排列=空头排列」时空仓或减仓，避免逆势操作。",
		"2. 规避「MA20走平」和「近5日持平」的无方向震荡市场。",
		"3. 不要在「突破20日新低」时抄底，该策略本质是趋势跟随，非抄底。",
		"4. 最优入场窗口：多头排列 + 站上MA60 + 60日高位。",
		"5. 关注年度×综合状态交叉表，避开历史上该策略持续亏损的年度×状态组合。",
	}
	for _, a := range advices {
		y = ensureSpace(pdf, y, 8)
		setFont(pdf, 9)
		setTextColor(pdf, colInk)
		pdf.SetX(pdfMarginX + 2)
		pdf.SetY(y)
		pdf.Cell(nil, a)
		y += 6
	}
	return y
}
