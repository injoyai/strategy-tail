package core

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
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
	fmt.Printf("%5s \t%4s \t%6s \t%6s \t%10s \t%8s \t%8s \t%7s \t%8s \t%8s \t%8s\n",
		"年份", "交易", "胜率", "总盈亏", "平均收益", "最大收益", "最大亏损", "盈亏比", "最大回撤", "最低本金", "年化")
	for _, r := range results {
		profitFactor := fmt.Sprintf("%.2f", r.ProfitFactor)
		if math.IsInf(r.ProfitFactor, 1) {
			profitFactor = "∞"
		}
		fmt.Printf("%6d \t%10d \t%9s \t%10.2f \t%12s \t%10s \t%12s \t%10s \t%12.2f \t%12.2f \t%10s\n",
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
		fmt.Printf("%6d \t%10.2f \t%8.2f \t%8.2f \t%12s \t%8s \t%8.2f\n",
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
		fmt.Printf("%6d \t%10.2f \t%8d \t%8.2f \t%8d \t%8d \t%8.1f \t%8.2f \t%8.2f \t%8.2f\n",
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
		// 年度总收益率(%) = 总利润 / 峰值并发本金 × 100
		annualReturn = totalProfit / requiredCapital * 100
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
		TotalProfit:      totalProfit,
		AvgProfit:        stats.AvgProfit,
		MaxProfit:        stats.MaxProfit,
		MaxLoss:          stats.MaxLoss,
		ProfitFactor:     stats.ProfitFactor,
		MaxDrawdown:      maxDrawdown,
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
			profit,
			profitRate,
			v.HoldingDays(),
		})
	}

	buf, err := csv.Export(data)
	if err == nil {
		dir := filepath.Join("output", "backtest")
		os.MkdirAll(dir, 0755)
		output := filepath.Join(dir, fmt.Sprintf("%d.csv", year))
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
