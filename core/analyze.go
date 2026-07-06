package core

import (
	"encoding/json"
	"fmt"
	"math"
	"path/filepath"
	"sort"
	"strconv"
	"time"

	"github.com/injoyai/goutil/g"
	"github.com/injoyai/goutil/oss"
	"github.com/injoyai/goutil/oss/csv"
	"github.com/injoyai/strategy-tail/lib/extend"
)

type AnalyzeResult struct {
	Year            int
	TotalTrades     int
	WinRate         float64 // 胜率（%）
	TotalProfit     float64 // 总盈亏（元，每手）
	AvgProfit       float64 // 平均收益率（%）
	MaxProfit       float64 // 最大单笔收益率（%）
	MaxLoss         float64 // 最小单笔收益率（%，负数）
	ProfitFactor    float64 // 盈亏比
	MaxDrawdown     float64 // 最大回撤（元，每手）
	RequiredCapital float64 // 最低本金（元）
	AnnualReturn    float64 // 年化收益率（%）
	Sharpe          float64 // 夏普比率（年化）
	Sortino         float64 // 索提诺比率（年化）
	Calmar          float64 // 卡玛比率
	BenchReturn     float64 // 基准收益率（%）
	Alpha           float64 // Jensen's Alpha（%）
	Beta            float64 // 贝塔
	// 阶段二新增指标
	MaxDrawdownPct   float64 // 最大回撤率（%）
	DrawdownDuration int     // 最大回撤持续天数（交易日）
	UnderwaterMax    float64 // 水下曲线最大深度（%）
	WinStreakMax     int     // 最大连胜笔数
	LossStreakMax    int     // 最大连亏笔数
	AvgHoldingDays   float64 // 平均持仓天数
	ProfitSkewness   float64 // 收益分布偏度
	ProfitKurtosis   float64 // 收益分布峰度
	VaR95            float64 // 95% VaR（%，单笔）
	CVaR95           float64 // 95% CVaR（%，单笔）
}

func PrintAnalyzeResults(results []AnalyzeResult) {
	if len(results) == 0 {
		return
	}
	fmt.Printf("\n年度回测结果:\n")
	// 第一行：基础指标
	fmt.Printf("%5s \t%4s \t%6s \t%6s \t%10s \t%10s \t%10s \t%7s \t%10s \t%10s \t%8s\n",
		"年份", "交易", "胜率", "总盈亏", "平均收益", "最大收益", "最大亏损", "盈亏比", "最大回撤", "最低本金", "年化")
	for _, r := range results {
		profitFactor := fmt.Sprintf("%.2f", r.ProfitFactor)
		if math.IsInf(r.ProfitFactor, 1) {
			profitFactor = "∞"
		}
		fmt.Printf("%6d \t%8d \t%8s \t%12.2f \t%10s \t%10s \t%10s \t%8s \t%12.2f \t%12.2f \t%10s\n",
			r.Year,
			r.TotalTrades,
			formatPercent(r.WinRate),
			r.TotalProfit,
			formatPercent(r.AvgProfit),
			formatPercent(r.MaxProfit),
			formatPercent(r.MaxLoss),
			profitFactor,
			r.MaxDrawdown,
			r.RequiredCapital,
			formatPercent(r.AnnualReturn),
		)
	}
	// 第二行：风险调整与基准对比指标
	fmt.Printf("\n风险调整指标:\n")
	fmt.Printf("%5s \t%8s \t%8s \t%8s \t%10s \t%8s \t%8s\n",
		"年份", "Sharpe", "Sortino", "Calmar", "基准收益", "Alpha", "Beta")
	for _, r := range results {
		fmt.Printf("%6d \t%8.2f \t%8.2f \t%8.2f \t%10s \t%8s \t%8.2f\n",
			r.Year,
			r.Sharpe,
			r.Sortino,
			r.Calmar,
			formatPercent(r.BenchReturn),
			formatPercent(r.Alpha),
			r.Beta,
		)
	}
	// 第三行：阶段二新增指标
	fmt.Printf("\n进阶分析指标:\n")
	fmt.Printf("%5s \t%8s \t%8s \t%8s \t%8s \t%8s \t%8s \t%8s \t%8s \t%8s\n",
		"年份", "回撤率%", "回撤天数", "水下深度", "最大连胜", "最大连亏", "持仓天数", "偏度", "峰度", "VaR95")
	for _, r := range results {
		fmt.Printf("%6d \t%8.2f \t%8d \t%8.2f \t%8d \t%8d \t%8.1f \t%8.2f \t%8.2f \t%8.2f\n",
			r.Year,
			r.MaxDrawdownPct,
			r.DrawdownDuration,
			r.UnderwaterMax,
			r.WinStreakMax,
			r.LossStreakMax,
			r.AvgHoldingDays,
			r.ProfitSkewness,
			r.ProfitKurtosis,
			r.VaR95,
		)
	}
}

func formatPercent(v float64) string {
	return fmt.Sprintf("%.2f%%", v)
}

// Analyze 计算单年度回测统计指标。
// getDayKlines 用于可视化；benchmarkKlines 为基准（指数/ETF）日线，可为 nil。
// cost 和 pos 用于计算本金和盈亏口径。
func Analyze(year int, allTrades []Trade, getDayKlines GetDayKlines, benchmarkKlines extend.Klines, cost Cost, pos PositionConfig) AnalyzeResult {

	// 按买入时间排序，为了计算资金曲线和回撤
	sort.Slice(allTrades, func(i, j int) bool {
		return allTrades[i].BuyTime.Before(allTrades[j].BuyTime)
	})

	stats := Stats(allTrades)
	totalTrades := stats.Total
	var totalProfit float64

	// 资金曲线（按实际成本口径）
	var equityCurve []float64
	currentEquity := 0.0
	equityCurve = append(equityCurve, currentEquity)

	for _, t := range allTrades {
		// 与原版一致：用 (SellPrice - BuyPrice) * quantity 计算盈亏
		profit := (t.SellPrice.Float64() - t.BuyPrice.Float64()) * float64(t.Quantity)
		totalProfit += profit
		currentEquity += profit
		equityCurve = append(equityCurve, currentEquity)
	}

	// 计算最大回撤
	var maxDrawdown float64
	var peakEquity float64 = -math.MaxFloat64
	var peakIdx, maxDDStartIdx, maxDDEndIdx int

	for i, eq := range equityCurve {
		if eq > peakEquity {
			peakEquity = eq
			peakIdx = i
		}
		drawdown := peakEquity - eq
		if drawdown > maxDrawdown {
			maxDrawdown = drawdown
			maxDDStartIdx = peakIdx
			maxDDEndIdx = i
		}
	}

	drawdownDuration := maxDDEndIdx - maxDDStartIdx

	requiredCapital := calculateRequiredCapital(allTrades, pos)
	annualReturn := 0.0
	if requiredCapital > 0 {
		annualReturn = totalProfit * 100 / requiredCapital * 100
	}

	// 最大回撤率
	maxDrawdownPct := 0.0
	if requiredCapital > 0 {
		maxDrawdownPct = maxDrawdown * 100 / requiredCapital
	}

	// 水下曲线最大深度
	underwaterMax := 0.0
	peak := 0.0
	for _, eq := range equityCurve {
		if eq > peak {
			peak = eq
		}
		if peak > 0 {
			depth := (peak - eq) / peak * 100
			if depth > underwaterMax {
				underwaterMax = depth
			}
		}
	}

	// 风险调整指标：用每笔交易收益率（小数）计算，年化系数取交易笔数
	tradeReturns := make([]float64, 0, len(allTrades))
	for _, t := range allTrades {
		r := tradeReturnRate(t) / 100
		tradeReturns = append(tradeReturns, r)
	}
	sharpe := SharpeRatio(tradeReturns, len(tradeReturns))
	sortino := SortinoRatio(tradeReturns, len(tradeReturns))
	maxDrawdownRatio := 0.0
	if requiredCapital > 0 {
		maxDrawdownRatio = maxDrawdown * 100 / requiredCapital
	}
	calmar := CalmarRatio(annualReturn/100, maxDrawdownRatio)

	// 基准对比
	benchReturn := BenchmarkReturn(benchmarkKlines)
	alpha, beta := computeTradeAlphaBeta(allTrades, benchmarkKlines)

	// 连胜连亏分析
	winStreak, lossStreak := calculateStreaks(allTrades)

	// 持仓天数
	var totalHoldingDays float64
	validTrades := 0
	for _, t := range allTrades {
		if !t.Virtual {
			totalHoldingDays += float64(t.HoldingDays())
			validTrades++
		}
	}
	avgHoldingDays := 0.0
	if validTrades > 0 {
		avgHoldingDays = totalHoldingDays / float64(validTrades)
	}

	// 收益分布偏度/峰度
	skewness, kurtosis := skewKurtosis(tradeReturns)

	// VaR / CVaR（95%）
	var95, cvar95 := varCVaR(tradeReturns, 0.05)

	result := AnalyzeResult{
		Year:             year,
		TotalTrades:      totalTrades,
		WinRate:          stats.WinRate,
		TotalProfit:      totalProfit * 100,
		AvgProfit:        stats.AvgProfit,
		MaxProfit:        stats.MaxProfit,
		MaxLoss:          stats.MaxLoss,
		ProfitFactor:     stats.ProfitFactor,
		MaxDrawdown:      maxDrawdown * 100,
		RequiredCapital:  requiredCapital,
		AnnualReturn:     annualReturn,
		Sharpe:           sharpe,
		Sortino:          sortino,
		Calmar:           calmar,
		BenchReturn:      benchReturn * 100,
		Alpha:            alpha * 100,
		Beta:             beta,
		MaxDrawdownPct:   maxDrawdownPct,
		DrawdownDuration: drawdownDuration,
		UnderwaterMax:    underwaterMax,
		WinStreakMax:     winStreak,
		LossStreakMax:    lossStreak,
		AvgHoldingDays:   avgHoldingDays,
		ProfitSkewness:   skewness,
		ProfitKurtosis:   kurtosis,
		VaR95:            var95 * 100,
		CVaR95:           cvar95 * 100,
	}

	data := [][]any{
		{"代码", "买入时间", "买入价格", "卖出时间", "卖出价格", "盈亏", "收益率", "持有天数"},
	}

	for _, v := range allTrades {
		profitRate := tradeReturnRate(v)
		profit := (v.SellPrice.Float64() - v.BuyPrice.Float64()) * float64(v.Quantity)
		data = append(data, []any{
			v.Code,
			v.BuyTime.Format(time.DateTime), v.BuyPrice.Float64(),
			v.SellTime.Format(time.DateTime), v.SellPrice.Float64(),
			profit * 100,
			profitRate,
			v.HoldingDays(),
		})
	}

	buf, err := csv.Export(data)
	if err == nil {
		output := filepath.Join("./output/", fmt.Sprintf("%d.csv", year))
		oss.New(output, buf)
	}

	visualizeTrades(year, allTrades, getDayKlines)
	return result
}

// calculateStreaks 计算最大连胜和最大连亏笔数。
func calculateStreaks(trades []Trade) (maxWin, maxLoss int) {
	curWin, curLoss := 0, 0
	for _, t := range trades {
		r := tradeReturnRate(t)
		if r > 0 {
			curWin++
			curLoss = 0
			if curWin > maxWin {
				maxWin = curWin
			}
		} else if r < 0 {
			curLoss++
			curWin = 0
			if curLoss > maxLoss {
				maxLoss = curLoss
			}
		} else {
			curWin = 0
			curLoss = 0
		}
	}
	return
}

// skewKurtosis 计算收益率序列的偏度和峰度。
func skewKurtosis(returns []float64) (skew, kurt float64) {
	n := len(returns)
	if n < 3 {
		return 0, 0
	}
	sum := 0.0
	for _, r := range returns {
		sum += r
	}
	mean := sum / float64(n)

	var sqSum, cuSum, foSum float64
	for _, r := range returns {
		d := r - mean
		d2 := d * d
		sqSum += d2
		cuSum += d2 * d
		foSum += d2 * d2
	}
	variance := sqSum / float64(n)
	if variance == 0 {
		return 0, 0
	}
	std := math.Sqrt(variance)
	skew = (cuSum / float64(n)) / (std * std * std)
	kurt = (foSum/float64(n))/(variance*variance) - 3 // 超额峰度
	return
}

// varCVaR 计算收益率序列的 VaR 和 CVaR。
// significance 为显著性水平（如 0.05 表示 95% VaR）。
// 返回值为负数（表示损失）。
func varCVaR(returns []float64, significance float64) (varVal, cvarVal float64) {
	n := len(returns)
	if n == 0 {
		return 0, 0
	}
	sorted := make([]float64, n)
	copy(sorted, returns)
	sort.Float64s(sorted)

	idx := int(float64(n) * significance)
	if idx < 1 {
		idx = 1
	}
	if idx > n {
		idx = n
	}
	varVal = sorted[idx-1]

	// CVaR = 排序后前 idx 个收益的均值
	sum := 0.0
	for i := 0; i < idx; i++ {
		sum += sorted[i]
	}
	cvarVal = sum / float64(idx)
	return
}

func visualizeTrades(year int, allTrades []Trade, getDayKlines GetDayKlines) {
	ExportTradeVisualHTML([]int{year}, map[int][]Trade{year: allTrades}, getDayKlines, nil)
}

func ExportTradeVisualHTML(years []int, yearlyTrades map[int][]Trade, getDayKlines GetDayKlines, results []AnalyzeResult) {
	if len(years) == 0 {
		return
	}

	codeYears := make(map[string]map[int][]Trade)
	for _, year := range years {
		for _, tr := range yearlyTrades[year] {
			if codeYears[tr.Code] == nil {
				codeYears[tr.Code] = make(map[int][]Trade)
			}
			codeYears[tr.Code][year] = append(codeYears[tr.Code][year], tr)
		}
	}

	charts := make([]map[string]any, 0, len(codeYears))
	codes := make([]string, 0, len(codeYears))
	for code := range codeYears {
		codes = append(codes, code)
	}
	sort.Strings(codes)

	for _, code := range codes {
		dks, err := getDayKlines(code, time.Time{}, time.Now())
		if err != nil || len(dks) == 0 {
			continue
		}

		kline := make([][]any, 0, len(dks))
		for _, k := range dks {
			kline = append(kline, []any{
				k.Time.Format(time.DateOnly),
				k.Open.Float64(),
				k.Close.Float64(),
				k.Low.Float64(),
				k.High.Float64(),
				k.Volume,
			})
		}

		tradeYears := codeYears[code]
		yearsForCode := make([]int, 0, len(tradeYears))
		for year := range tradeYears {
			yearsForCode = append(yearsForCode, year)
		}
		sort.Ints(yearsForCode)

		marks := make([]map[string]any, 0)
		tradeRows := make([]map[string]any, 0)
		for _, year := range yearsForCode {
			for _, t := range tradeYears[year] {
				buyRate := tradeReturnRate(t)
				profit := (t.SellPrice.Float64() - t.BuyPrice.Float64()) * float64(t.Quantity) * 100
				marks = append(marks, map[string]any{
					"date":  t.BuyTime.Format(time.DateOnly),
					"time":  t.BuyTime.Format(time.TimeOnly),
					"year":  year,
					"price": t.BuyPrice.Float64(),
					"type":  "买",
					"rate":  buyRate,
				})
				marks = append(marks, map[string]any{
					"date":  t.SellTime.Format(time.DateOnly),
					"time":  t.SellTime.Format(time.TimeOnly),
					"year":  year,
					"price": t.SellPrice.Float64(),
					"type":  "卖",
					"rate":  buyRate,
				})
				tradeRows = append(tradeRows, map[string]any{
					"year":      year,
					"buyDate":   t.BuyTime.Format(time.DateOnly),
					"buyTime":   t.BuyTime.Format(time.TimeOnly),
					"buyPrice":  t.BuyPrice.Float64(),
					"sellDate":  t.SellTime.Format(time.DateOnly),
					"sellTime":  t.SellTime.Format(time.TimeOnly),
					"sellPrice": t.SellPrice.Float64(),
					"profit":    profit,
					"rate":      buyRate,
				})
			}
		}
		sort.Slice(marks, func(i, j int) bool {
			left := fmt.Sprintf("%04v-%s %s", marks[i]["year"], marks[i]["date"], marks[i]["time"])
			right := fmt.Sprintf("%04v-%s %s", marks[j]["year"], marks[j]["date"], marks[j]["time"])
			return left < right
		})
		sort.Slice(tradeRows, func(i, j int) bool {
			left := fmt.Sprintf("%04v-%s %s", tradeRows[i]["year"], tradeRows[i]["buyDate"], tradeRows[i]["buyTime"])
			right := fmt.Sprintf("%04v-%s %s", tradeRows[j]["year"], tradeRows[j]["buyDate"], tradeRows[j]["buyTime"])
			return left < right
		})

		charts = append(charts, map[string]any{
			"code":   code,
			"years":  yearsForCode,
			"kline":  kline,
			"trades": marks,
			"rows":   tradeRows,
		})
	}

	if len(charts) == 0 {
		return
	}

	content, err := buildTradeVisualHTML(charts, results, yearlyTrades)
	if err != nil {
		return
	}
	output := filepath.Join("./output/", "trades.html")
	oss.New(output, []byte(content))
}

func buildTradeVisualHTML(charts []map[string]any, results []AnalyzeResult, yearlyTrades map[int][]Trade) (string, error) {
	chartsJSON, err := json.Marshal(charts)
	if err != nil {
		return "", err
	}

	// 构建年度统计摘要
	yearStats := make([]map[string]any, 0, len(results))
	for _, r := range results {
		profitFactor := fmt.Sprintf("%.2f", r.ProfitFactor)
		if math.IsInf(r.ProfitFactor, 1) {
			profitFactor = "∞"
		}
		yearStats = append(yearStats, map[string]any{
			"year":            r.Year,
			"trades":          r.TotalTrades,
			"winRate":         fmt.Sprintf("%.2f", r.WinRate),
			"profitFactor":    profitFactor,
			"avgProfit":       fmt.Sprintf("%.2f", r.AvgProfit),
			"maxProfit":       fmt.Sprintf("%.2f", r.MaxProfit),
			"maxLoss":         fmt.Sprintf("%.2f", r.MaxLoss),
			"totalProfit":     fmt.Sprintf("%.2f", r.TotalProfit),
			"annualReturn":    fmt.Sprintf("%.2f", r.AnnualReturn),
			"sharpe":          fmt.Sprintf("%.2f", r.Sharpe),
			"sortino":         fmt.Sprintf("%.2f", r.Sortino),
			"calmar":          fmt.Sprintf("%.2f", r.Calmar),
			"maxDrawdown":     fmt.Sprintf("%.2f", r.MaxDrawdownPct),
			"drawdownDays":    r.DrawdownDuration,
			"winStreak":       r.WinStreakMax,
			"lossStreak":      r.LossStreakMax,
			"holdingDays":     fmt.Sprintf("%.1f", r.AvgHoldingDays),
			"var95":           fmt.Sprintf("%.2f", r.VaR95),
			"requiredCapital": fmt.Sprintf("%.2f", r.RequiredCapital),
		})
	}

	// 构建资金曲线和月度收益数据
	equityData := make([]map[string]any, 0)
	monthlyData := make(map[string][]float64)
	for year := range yearlyTrades {
		trades := yearlyTrades[year]
		sort.Slice(trades, func(i, j int) bool {
			return trades[i].BuyTime.Before(trades[j].BuyTime)
		})
		cumEquity := 0.0
		for _, t := range trades {
			profit := (t.SellPrice.Float64() - t.BuyPrice.Float64()) * float64(t.Quantity)
			cumEquity += profit
			equityData = append(equityData, map[string]any{
				"date":   t.SellTime.Format("2006-01-02"),
				"equity": fmt.Sprintf("%.2f", cumEquity),
				"profit": fmt.Sprintf("%.2f", profit),
				"rate":   fmt.Sprintf("%.2f", tradeReturnRate(t)),
				"code":   t.Code,
				"year":   year,
			})
			monthKey := t.BuyTime.Format("2006-01")
			rate := tradeReturnRate(t)
			monthlyData[monthKey] = append(monthlyData[monthKey], rate)
		}
	}

	// 月度收益汇总
	monthlySummary := make([]map[string]any, 0)
	for month, rates := range monthlyData {
		sum := 0.0
		for _, r := range rates {
			sum += r
		}
		monthlySummary = append(monthlySummary, map[string]any{
			"month":  month,
			"return": fmt.Sprintf("%.2f", sum),
			"trades": len(rates),
		})
	}
	sort.Slice(monthlySummary, func(i, j int) bool {
		return monthlySummary[i]["month"].(string) < monthlySummary[j]["month"].(string)
	})

	// 收益分布
	distBuckets := []float64{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}
	for _, e := range equityData {
		rate, _ := strconv.ParseFloat(e["rate"].(string), 64)
		switch {
		case rate < -15:
			distBuckets[0]++
		case rate < -10:
			distBuckets[1]++
		case rate < -5:
			distBuckets[2]++
		case rate < 0:
			distBuckets[3]++
		case rate == 0:
			distBuckets[4]++
		case rate < 5:
			distBuckets[5]++
		case rate < 10:
			distBuckets[6]++
		case rate < 15:
			distBuckets[7]++
		case rate < 20:
			distBuckets[8]++
		case rate < 25:
			distBuckets[9]++
		default:
			distBuckets[10]++
		}
	}

	statsJSON, _ := json.Marshal(yearStats)
	equityJSON, _ := json.Marshal(equityData)
	monthlyJSON, _ := json.Marshal(monthlySummary)
	distJSON, _ := json.Marshal(distBuckets)
	chartsStr := string(chartsJSON)

	return professionalReportHTML(statsJSON, equityJSON, monthlyJSON, distJSON, chartsStr), nil
}

// professionalReportHTML 生成专业回测报告 HTML（中文指标 + ECharts 图表）。
func professionalReportHTML(statsJSON, equityJSON, monthlyJSON, distJSON []byte, chartsStr string) string {
	return `<!-- Generated by Trae Work -->
<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>策略回测报告</title>
<script src="https://cdn.jsdelivr.net/npm/echarts@5/dist/echarts.min.js"></script>
<style>
:root{--bg:#f8f9fb;--bg2:#fff;--ink:#1a1a2e;--muted:#6b7280;--rule:#e5e7eb;--accent:#3b82f6;--red:#ef4444;--green:#10b981}
*{margin:0;padding:0;box-sizing:border-box}
body{font-family:-apple-system,"Microsoft YaHei","PingFang SC",sans-serif;background:var(--bg);color:var(--ink);line-height:1.6;font-size:15px}
.container{max-width:1200px;margin:0 auto;padding:24px 20px}
.header{text-align:center;padding:40px 20px 30px;background:linear-gradient(135deg,#1e293b 0%,#334155 100%);color:#fff;border-radius:12px;margin-bottom:28px}
.header h1{font-size:28px;font-weight:700;margin-bottom:8px}
.header .subtitle{font-size:15px;opacity:.8}
.section{margin-bottom:32px}
.section-title{font-size:20px;font-weight:700;margin-bottom:16px;padding-bottom:10px;border-bottom:2px solid var(--accent);color:var(--ink)}
.metrics-grid{display:grid;grid-template-columns:repeat(auto-fill,minmax(180px,1fr));gap:14px}
.metric-card{background:var(--bg2);border-radius:10px;padding:16px 18px;box-shadow:0 1px 3px rgba(0,0,0,.06);border:1px solid var(--rule)}
.metric-card .label{font-size:13px;color:var(--muted);margin-bottom:6px}
.metric-card .value{font-size:24px;font-weight:700}
.metric-card .value.pos{color:var(--red)}
.metric-card .value.neg{color:var(--green)}
.metric-card .value.neutral{color:var(--accent)}
.metric-card .sub{font-size:12px;color:var(--muted);margin-top:4px}
.chart-box{background:var(--bg2);border-radius:10px;padding:20px;box-shadow:0 1px 3px rgba(0,0,0,.06);border:1px solid var(--rule);margin-bottom:16px}
.chart-box .chart-title{font-size:16px;font-weight:600;margin-bottom:14px}
.chart{width:100%;height:360px}
.chart.tall{height:420px}
table{width:100%;border-collapse:collapse;font-size:14px}
.table-wrap{background:var(--bg2);border-radius:10px;padding:20px;box-shadow:0 1px 3px rgba(0,0,0,.06);border:1px solid var(--rule);overflow-x:auto;max-height:400px;overflow-y:auto}
th,td{padding:9px 12px;text-align:center;border-bottom:1px solid var(--rule);white-space:nowrap}
th{background:#f9fafb;font-weight:600;color:var(--muted);position:sticky;top:0;z-index:1}
td.pos{color:var(--red);font-weight:600}
td.neg{color:var(--green);font-weight:600}
tbody tr:hover{background:#f0f4ff}
.toolbar{display:flex;gap:12px;align-items:center;flex-wrap:wrap;margin-bottom:16px}
.toolbar select,.toolbar input{height:34px;padding:0 10px;border:1px solid var(--rule);border-radius:6px;font-size:14px;background:var(--bg2)}
.kline-section{background:var(--bg2);border-radius:10px;padding:20px;box-shadow:0 1px 3px rgba(0,0,0,.06);border:1px solid var(--rule)}
.kline-chart{width:100%;height:600px}
@media(max-width:768px){.metrics-grid{grid-template-columns:repeat(2,1fr)}.container{padding:16px 12px}}
</style>
</head>
<body>
<div class="container">
<div class="header">
<h1>MACD 策略回测报告</h1>
<div class="subtitle" id="reportSubtitle"></div>
</div>

<div class="section">
<div class="section-title">年度统计概览</div>
<div id="yearTable"></div>
</div>

<div class="section">
<div class="section-title">核心绩效指标</div>
<div class="metrics-grid" id="metricsGrid"></div>
</div>

<div class="section">
<div class="section-title">资金曲线</div>
<div class="chart-box">
<div class="chart-title">累计盈亏走势</div>
<div id="equityChart" class="chart tall"></div>
</div>
</div>

<div class="section">
<div class="section-title">回撤分析</div>
<div class="chart-box">
<div class="chart-title">水下曲线（Underwater Equity）</div>
<div id="drawdownChart" class="chart"></div>
</div>
</div>

<div class="section">
<div class="section-title">收益分布</div>
<div class="chart-box">
<div class="chart-title">单笔交易收益率分布</div>
<div id="distChart" class="chart"></div>
</div>
</div>

<div class="section">
<div class="section-title">月度收益矩阵</div>
<div class="chart-box">
<div class="chart-title">月度收益率热力图</div>
<div id="monthlyChart" class="chart tall"></div>
</div>
</div>

<div class="section">
<div class="section-title">交易明细</div>
<div class="toolbar">
<label>股票代码 <select id="code"></select></label>
</div>
<div class="table-wrap">
<table>
<thead><tr><th>年份</th><th>代码</th><th>买入日期</th><th>买入价</th><th>卖出日期</th><th>卖出价</th><th>收益率</th><th>盈亏</th></tr></thead>
<tbody id="tradeRows"></tbody>
</table>
</div>
</div>

<div class="section">
<div class="section-title">K线买卖点可视化</div>
<div class="kline-section">
<div id="klineChart" class="kline-chart"></div>
</div>
</div>

</div>

<script>
const yearStats = ` + string(statsJSON) + `;
const equityData = ` + string(equityJSON) + `;
const monthlyData = ` + string(monthlyJSON) + `;
const distBuckets = ` + string(distJSON) + `;
const allCharts = ` + chartsStr + `;

document.getElementById('reportSubtitle').textContent = '回测年份：' + yearStats.map(x=>x.year).join('、') + '  |  生成日期：' + new Date().toLocaleDateString('zh-CN');

// 年度统计表格
(function(){
  let html = '<div class="table-wrap"><table><thead><tr><th>年份</th><th>交易笔数</th><th>胜率</th><th>盈亏比</th><th>平均收益</th><th>最大盈利</th><th>最大亏损</th><th>年化收益</th><th>夏普比率</th><th>最大回撤</th><th>回撤天数</th><th>最大连胜</th><th>最大连亏</th><th>持仓天数</th><th>VaR95</th></tr></thead><tbody>';
  yearStats.forEach(r=>{
    html += '<tr><td><b>'+r.year+'</b></td><td>'+r.trades+'</td><td class="'+(parseFloat(r.winRate)>=50?'pos':'neg')+'">'+r.winRate+'%</td><td>'+r.profitFactor+'</td><td class="'+(parseFloat(r.avgProfit)>=0?'pos':'neg')+'">'+r.avgProfit+'%</td><td class="pos">'+r.maxProfit+'%</td><td class="neg">'+r.maxLoss+'%</td><td class="'+(parseFloat(r.annualReturn)>=0?'pos':'neg')+'">'+r.annualReturn+'%</td><td>'+r.sharpe+'</td><td class="neg">'+r.maxDrawdown+'%</td><td>'+r.drawdownDays+'</td><td class="pos">'+r.winStreak+'</td><td class="neg">'+r.lossStreak+'</td><td>'+r.holdingDays+'</td><td class="neg">'+r.var95+'%</td></tr>';
  });
  html += '</tbody></table></div>';
  document.getElementById('yearTable').innerHTML = html;
})();

// 核心指标卡片
(function(){
  const r = yearStats[0] || {};
  const cards = [
    {label:'交易笔数',value:r.trades||'0',cls:'neutral',sub:'总交易次数'},
    {label:'胜率',value:(r.winRate||'0')+'%',cls:parseFloat(r.winRate)>=50?'pos':'neg',sub:'盈利交易占比'},
    {label:'盈亏比',value:r.profitFactor||'0',cls:'neutral',sub:'盈利总额/亏损总额'},
    {label:'平均收益率',value:(r.avgProfit||'0')+'%',cls:parseFloat(r.avgProfit)>=0?'pos':'neg',sub:'单笔平均收益'},
    {label:'年化收益率',value:(r.annualReturn||'0')+'%',cls:parseFloat(r.annualReturn)>=0?'pos':'neg',sub:'按本金计算'},
    {label:'夏普比率',value:r.sharpe||'0',cls:parseFloat(r.sharpe)>=1?'pos':'neg',sub:'风险调整收益'},
    {label:'索提诺比率',value:r.sortino||'0',cls:parseFloat(r.sortino)>=1?'pos':'neg',sub:'下行风险调整'},
    {label:'卡玛比率',value:r.calmar||'0',cls:parseFloat(r.calmar)>=1?'pos':'neg',sub:'年化/最大回撤'},
    {label:'最大回撤',value:(r.maxDrawdown||'0')+'%',cls:'neg',sub:'峰值到谷值'},
    {label:'VaR95',value:(r.var95||'0')+'%',cls:'neg',sub:'95%置信度最大损失'},
    {label:'最大连胜',value:(r.winStreak||'0')+'笔',cls:'pos',sub:'连续盈利笔数'},
    {label:'最大连亏',value:(r.lossStreak||'0')+'笔',cls:'neg',sub:'连续亏损笔数'},
    {label:'平均持仓',value:(r.holdingDays||'0')+'天',cls:'neutral',sub:'单笔持仓天数'},
    {label:'所需本金',value:r.requiredCapital||'0',cls:'neutral',sub:'最低资金需求(元)'}
  ];
  document.getElementById('metricsGrid').innerHTML = cards.map(c=>
    '<div class="metric-card"><div class="label">'+c.label+'</div><div class="value '+c.cls+'">'+c.value+'</div><div class="sub">'+c.sub+'</div></div>'
  ).join('');
})();

// 资金曲线
(function(){
  if(!equityData.length) return;
  const chart = echarts.init(document.getElementById('equityChart'));
  const dates = equityData.map(x=>x.date);
  const equity = equityData.map(x=>parseFloat(x.equity));
  chart.setOption({
    animation:false,
    tooltip:{trigger:'axis',appendToBody:true},
    grid:{left:70,right:30,top:40,bottom:60},
    xAxis:{type:'category',data:dates,axisLine:{lineStyle:{color:'#ccc'}}},
    yAxis:{type:'value',name:'盈亏(元)',axisLine:{lineStyle:{color:'#ccc'}}},
    dataZoom:[{type:'inside'},{type:'slider',bottom:10}],
    series:[{
      name:'累计盈亏',type:'line',data:equity,symbol:'none',
      areaStyle:{color:{type:'linear',x:0,y:0,x2:0,y2:1,colorStops:[{offset:0,color:'rgba(59,130,246,0.3)'},{offset:1,color:'rgba(59,130,246,0)'}]}},
      lineStyle:{width:2,color:'#3b82f6'},
      markLine:{data:[{yAxis:0,lineStyle:{color:'#999',type:'dashed'}}],symbol:'none',label:{show:false}}
    }]
  });
  window.addEventListener('resize',()=>chart.resize());
})();

// 回撤/水下曲线
(function(){
  if(!equityData.length) return;
  const chart = echarts.init(document.getElementById('drawdownChart'));
  const dates = equityData.map(x=>x.date);
  let peak = 0;
  const underwater = equityData.map(x=>{
    const eq = parseFloat(x.equity);
    if(eq > peak) peak = eq;
    return peak > 0 ? ((eq - peak) / peak * 100) : 0;
  });
  chart.setOption({
    animation:false,
    tooltip:{trigger:'axis',appendToBody:true,formatter:p=>p[0].axisValue+'<br/>水下深度: '+p[0].data.toFixed(2)+'%'},
    grid:{left:70,right:30,top:30,bottom:60},
    xAxis:{type:'category',data:dates,axisLine:{lineStyle:{color:'#ccc'}}},
    yAxis:{type:'value',name:'回撤%',max:0,axisLine:{lineStyle:{color:'#ccc'}}},
    dataZoom:[{type:'inside'},{type:'slider',bottom:10}],
    series:[{
      name:'水下曲线',type:'line',data:underwater,symbol:'none',
      areaStyle:{color:'rgba(239,68,68,0.25)'},
      lineStyle:{width:1.5,color:'#ef4444'}
    }]
  });
  window.addEventListener('resize',()=>chart.resize());
})();

// 收益分布
(function(){
  const chart = echarts.init(document.getElementById('distChart'));
  const labels = ['<-15%','-15~-10%','-10~-5%','-5~0%','0%','0~5%','5~10%','10~15%','15~20%','20~25%','>25%'];
  const colors = distBuckets.map((_,i)=> i<4 ? '#10b981' : (i===4 ? '#999' : '#ef4444'));
  chart.setOption({
    animation:false,
    tooltip:{trigger:'axis',appendToBody:true},
    grid:{left:60,right:30,top:30,bottom:40},
    xAxis:{type:'category',data:labels,axisLabel:{rotate:30}},
    yAxis:{type:'value',name:'笔数'},
    series:[{
      name:'交易笔数',type:'bar',data:distBuckets.map((v,i)=>({value:v,itemStyle:{color:colors[i]}})),
      label:{show:true,position:'top',formatter:'{c}'}
    }]
  });
  window.addEventListener('resize',()=>chart.resize());
})();

// 月度收益热力图
(function(){
  if(!monthlyData.length) return;
  const chart = echarts.init(document.getElementById('monthlyChart'));
  const yearSet = new Set();
  monthlyData.forEach(m=>yearSet.add(m.month.split('-')[0]));
  const years = Array.from(yearSet).sort();
  const months = ['1月','2月','3月','4月','5月','6月','7月','8月','9月','10月','11月','12月'];
  const heatData = [];
  let minVal = 0, maxVal = 0;
  monthlyData.forEach(m=>{
    const [y,mo] = m.month.split('-');
    const yi = years.indexOf(y);
    const mi = parseInt(mo)-1;
    const val = parseFloat(m.return);
    heatData.push([yi,mi,val]);
    if(val<minVal) minVal=val;
    if(val>maxVal) maxVal=val;
  });
  chart.setOption({
    animation:false,
    tooltip:{appendToBody:true,formatter:p=>years[p.value[0]]+'年'+months[p.value[1]]+'<br/>收益率: '+p.value[2]+'%'},
    grid:{left:60,right:30,top:30,bottom:50},
    xAxis:{type:'category',data:years},
    yAxis:{type:'category',data:months},
    visualMap:{
      min:minVal,max:maxVal,calculable:true,orient:'horizontal',left:'center',bottom:5,
      inRange:{color:['#10b981','#f0f0f0','#ef4444']}
    },
    series:[{
      name:'月度收益',type:'heatmap',data:heatData,
      label:{show:true,formatter:p=>p.value[2]+'%'},
      emphasis:{itemStyle:{shadowBlur:10}}
    }]
  });
  window.addEventListener('resize',()=>chart.resize());
})();

// 交易明细表
(function(){
  const select = document.getElementById('code');
  const allRows = [];
  allCharts.forEach(c=>{ (c.rows||[]).forEach(r=>allRows.push({...r,code:c.code})); });
  allRows.sort((a,b)=>(a.year+'-'+a.buyDate)<(b.year+'-'+b.buyDate)?-1:1);
  const allOpt = document.createElement('option');
  allOpt.value = ''; allOpt.textContent = '全部代码';
  select.appendChild(allOpt);
  allCharts.forEach(c=>{
    const opt = document.createElement('option');
    opt.value = c.code; opt.textContent = c.code+'（'+((c.rows||[]).length)+'笔）';
    select.appendChild(opt);
  });
  function render(){
    const code = select.value;
    const showRows = code ? allRows.filter(r=>r.code===code) : allRows;
    document.getElementById('tradeRows').innerHTML = showRows.map(r=>{
      const rate = parseFloat(r.rate); const profit = parseFloat(r.profit);
      return '<tr><td>'+r.year+'</td><td>'+r.code+'</td><td>'+r.buyDate+'</td><td>'+Number(r.buyPrice).toFixed(2)+'</td><td>'+r.sellDate+'</td><td>'+Number(r.sellPrice).toFixed(2)+'</td><td class="'+(rate>=0?'pos':'neg')+'">'+rate.toFixed(2)+'%</td><td class="'+(profit>=0?'pos':'neg')+'">'+profit.toFixed(2)+'</td></tr>';
    }).join('');
  }
  select.addEventListener('change',render);
  render();
})();

// K线买卖点
(function(){
  if(!allCharts.length) return;
  const select = document.getElementById('code');
  const chart = echarts.init(document.getElementById('klineChart'));
  function ema(v,p){const a=2/(p+1);const r=[v[0]];for(let i=1;i<v.length;i++)r.push(r[i-1]+a*(v[i]-r[i-1]));return r;}
  function calcMACD(c){const e12=ema(c,12);const e26=ema(c,26);const d=c.map((_,i)=>e12[i]-e26[i]);const de=ema(d,9);return{dif:d,dea:de,macd:d.map((v,i)=>(v-de[i])*2)};}
  function ma(v,p){return v.map((_,i)=>{if(i+1<p)return null;let s=0;for(let j=i-p+1;j<=i;j++)s+=Number(v[j]||0);return Number((s/p).toFixed(3));});}
  function render(){
    const code = select.value || allCharts[0].code;
    const item = allCharts.find(x=>x.code===code) || allCharts[0];
    if(!item){chart.setOption({title:{text:'无数据'}});return;}
    const dates=item.kline.map(x=>x[0]);
    const values=item.kline.map(x=>[x[1],x[2],x[3],x[4]]);
    const closes=item.kline.map(x=>x[2]);
    const vols=item.kline.map(x=>x[5]);
    const macd=calcMACD(closes);
    const dateMap=new Map(item.kline.map(x=>[x[0],x]));
    const marks=(item.trades||[]).map(x=>{
      const k=dateMap.get(x.date);
      const bp=k?(x.type==='买'?k[3]:k[4]):x.price;
      return{name:x.type,coord:[x.date,bp],value:x.type==='买'?'B':'S',symbol:'triangle',symbolRotate:x.type==='买'?0:180,symbolSize:14,symbolOffset:[0,x.type==='买'?12:-12],itemStyle:{color:x.type==='买'?'#ef4444':'#10b981'},label:{show:true,formatter:x.type==='买'?'B':'S',color:'#fff',fontWeight:'bold',fontSize:10,offset:[0,x.type==='买'?4:-4]},tooltip:{formatter:x.type+' '+x.date+' '+x.time+'<br/>价格:'+Number(x.price).toFixed(2)+'<br/>收益:'+Number(x.rate).toFixed(2)+'%'}};
    });
    chart.setOption({animation:false,
      title:{text:item.code+' K线买卖点',left:16,top:10},
      legend:{top:12,data:['日K','MA5','MA10','MA20','MA60','成交量','MACD','DIF','DEA']},
      tooltip:{trigger:'axis',axisPointer:{type:'cross'}},
      axisPointer:{link:[{xAxisIndex:'all'}]},
      dataZoom:[{type:'inside',xAxisIndex:[0,1,2]},{show:true,xAxisIndex:[0,1,2],type:'slider',bottom:8}],
      grid:[{left:60,right:30,top:60,height:'50%'},{left:60,right:30,top:'64%',height:'12%'},{left:60,right:30,top:'80%',height:'12%'}],
      xAxis:[{type:'category',data:dates,boundaryGap:false},{type:'category',gridIndex:1,data:dates,boundaryGap:false,axisLabel:{show:false}},{type:'category',gridIndex:2,data:dates,boundaryGap:false,axisLabel:{show:false}}],
      yAxis:[{scale:true,splitArea:{show:true}},{scale:true,gridIndex:1,splitNumber:2,axisLabel:{show:false},splitLine:{show:false}},{scale:true,gridIndex:2,splitNumber:3,splitLine:{show:true}}],
      series:[
        {name:'日K',type:'candlestick',data:values,itemStyle:{color:'#ef4444',color0:'#10b981',borderColor:'#ef4444',borderColor0:'#10b981'},markPoint:{data:marks}},
        {name:'MA5',type:'line',data:ma(closes,5),symbol:'none',lineStyle:{width:1,color:'#f59e0b'}},
        {name:'MA10',type:'line',data:ma(closes,10),symbol:'none',lineStyle:{width:1,color:'#8b5cf6'}},
        {name:'MA20',type:'line',data:ma(closes,20),symbol:'none',lineStyle:{width:1,color:'#3b82f6'}},
        {name:'MA60',type:'line',data:ma(closes,60),symbol:'none',lineStyle:{width:1,color:'#10b981'}},
        {name:'成交量',type:'bar',xAxisIndex:1,yAxisIndex:1,data:vols,itemStyle:{color:p=>values[p.dataIndex]&&values[p.dataIndex][1]>=values[p.dataIndex][0]?'#ef4444':'#10b981'}},
        {name:'MACD',type:'bar',xAxisIndex:2,yAxisIndex:2,data:macd.macd.map(v=>+v.toFixed(4)),itemStyle:{color:p=>p.data>=0?'#ef4444':'#10b981'}},
        {name:'DIF',type:'line',xAxisIndex:2,yAxisIndex:2,data:macd.dif.map(v=>+v.toFixed(4)),symbol:'none',lineStyle:{width:1,color:'#f59e0b'}},
        {name:'DEA',type:'line',xAxisIndex:2,yAxisIndex:2,data:macd.dea.map(v=>+v.toFixed(4)),symbol:'none',lineStyle:{width:1,color:'#3b82f6'}}
      ]
    },true);
  }
  window.addEventListener('resize',()=>chart.resize());
  select.addEventListener('change',render);
  render();
})();
</script>
</body>
</html>`
}

// calculateRequiredCapital 计算回测所需最低本金。
// 按交易时间轴逐日累计"在持仓位 × 成本"，取峰值。
// 修正：跨日持仓重叠时，本金是叠加的，而非仅取单日最大值。
func calculateRequiredCapital(allTrades []Trade, pos PositionConfig) float64 {
	if len(allTrades) == 0 {
		return 0
	}

	sharesPerLot := pos.SharesPerLot
	if sharesPerLot <= 0 {
		sharesPerLot = SharesPerLot
	}

	// 按日期记录每日的持仓投入
	// 遍历每笔交易，在买入日到卖出日之间累加该笔的买入成本
	dailyExposure := make(map[string]float64)

	for _, t := range allTrades {
		buyCost := t.BuyCost
		if buyCost <= 0 {
			// 兼容旧数据：用原始价格 × 数量估算
			buyCost = t.BuyPrice.Float64() * float64(sharesPerLot)
		}

		// 从买入日到卖出日（含），每日都在持仓
		cur := t.BuyTime
		end := t.SellTime
		for !cur.After(end) {
			key := cur.Format(time.DateOnly)
			dailyExposure[key] += buyCost
			cur = cur.AddDate(0, 0, 1)
		}
	}

	if len(dailyExposure) == 0 {
		return 0
	}

	// 取所有日期中最大的日度暴露作为所需本金
	maxExposure := 0.0
	for _, v := range dailyExposure {
		if v > maxExposure {
			maxExposure = v
		}
	}

	return g.Max(0., maxExposure)
}
