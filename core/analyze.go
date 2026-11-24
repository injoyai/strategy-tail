package core

import (
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/injoyai/strategy-tail/lib/extend"
)

type AnalyzeResult struct {
	Year            int
	TotalTrades     int
	WinRate         float64 // 净收益胜率（%）
	TotalProfit     float64 // 总净盈亏（元，每手）
	AvgProfit       float64 // 平均净收益率（%）
	MaxProfit       float64 // 最大单笔净收益率（%）
	MaxLoss         float64 // 最小单笔净收益率（%，负数）
	ProfitFactor    float64 // 净收益率盈亏比
	MaxDrawdown     float64 // 最大净值回撤（元，每手）
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
	DrawdownDuration int     // 最大回撤持续天数（按卖出日之间的日历天数计）
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
		"年份", "交易", "净胜率", "净盈亏", "平均净收益", "最大净收益", "最大净亏损", "净盈亏比", "最大回撤", "最低本金", "年化")
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
		fmt.Printf("%6d \t%10.2f \t%8.2f \t%8s \t%12s \t%8s \t%8.2f\n",
			r.Year,
			r.Sharpe,
			r.Sortino,
			formatRatio(r.Calmar),
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

// formatRatio 将比率格式化为两位小数；无回撤的 Calmar 为 +Inf，统一显示为 ∞。
func formatRatio(v float64) string {
	if math.IsInf(v, 1) {
		return "∞"
	}
	if math.IsInf(v, -1) {
		return "-∞"
	}
	return fmt.Sprintf("%.2f", v)
}

// settledTrades 为同一结算时刻补充确定性顺序，避免并发收集顺序改变回撤与连胜连亏。
func settledTrades(all []Trade) []Trade {
	trades := append([]Trade(nil), all...)
	sort.Slice(trades, func(i, j int) bool {
		a, b := trades[i], trades[j]
		if !a.SellTime.Equal(b.SellTime) {
			return a.SellTime.Before(b.SellTime)
		}
		if !a.BuyTime.Equal(b.BuyTime) {
			return a.BuyTime.Before(b.BuyTime)
		}
		if a.Code != b.Code {
			return a.Code < b.Code
		}
		if x, y := tradeProfitAmount(a), tradeProfitAmount(b); x != y {
			return x < y
		}
		return tradeReturnRate(a) < tradeReturnRate(b)
	})
	return trades
}

// Analyze 计算单年度回测统计指标，不执行文件或报告写入。
// getDayKlines 与 cost 为兼容既有调用保留；成交成本已固化在 Trade 中，不再
// 由分析阶段重新计算。benchmarkKlines 为基准（指数/ETF）日线，可为 nil，
// pos 用于兼容旧记录的所需本金估算。
func Analyze(year int, allTrades []Trade, _ GetDayKlines, benchmarkKlines extend.Klines, _ Cost, pos PositionConfig) AnalyzeResult {

	trades := settledTrades(allTrades)
	stats := Stats(trades)

	equityCurve, sellDates, totalProfit := buildEquityCurve(trades)
	requiredCapital := calculateRequiredCapital(trades, pos)

	annualReturn := 0.0
	if requiredCapital > 0 {
		// 年度总收益率(%) = 总利润 / 峰值并发本金 × 100
		annualReturn = totalProfit / requiredCapital * 100
	}

	// 回撤口径统一为小数比例：百分比展示由 ratio*100 派生，Calmar 直接使用小数
	maxDrawdown, maxDrawdownRatio, drawdownDuration, underwaterMax := drawdownMetrics(equityCurve, sellDates, requiredCapital)

	tradeReturns := make([]float64, 0, len(trades))
	for _, t := range trades {
		tradeReturns = append(tradeReturns, tradeReturnRate(t)/100)
	}
	sharpe, sortino, calmar := riskAdjustedMetrics(trades, tradeReturns, annualReturn, maxDrawdownRatio)

	// 基准对比
	benchReturn := BenchmarkReturn(benchmarkKlines)
	alpha, beta := computeTradeAlphaBeta(trades, benchmarkKlines)

	// 连胜连亏分析
	winStreak, lossStreak := calculateStreaks(trades)
	avgHoldingDays := averageHoldingDays(trades)

	// 收益分布偏度/峰度
	skewness, kurtosis := skewKurtosis(tradeReturns)

	// VaR / CVaR（95%）
	var95, cvar95 := varCVaR(tradeReturns, 0.05)

	return AnalyzeResult{
		Year:             year,
		TotalTrades:      stats.Total,
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
		MaxDrawdownPct:   maxDrawdownRatio * 100,
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
}

// buildEquityCurve 构造按卖出结算的资金曲线。
// 返回曲线（首点为 0 初始权益）、逐点卖出日期（首点为零值）和累计净盈亏。
func buildEquityCurve(trades []Trade) (equityCurve []float64, sellDates []time.Time, totalProfit float64) {
	currentEquity := 0.0
	equityCurve = append(equityCurve, currentEquity)
	sellDates = append(sellDates, time.Time{})

	for _, t := range trades {
		profit := tradeProfitAmount(t)
		totalProfit += profit
		currentEquity += profit
		equityCurve = append(equityCurve, currentEquity)
		sellDates = append(sellDates, t.SellTime)
	}
	return
}

// drawdownMetrics 计算最大回撤（元/小数比例）、回撤持续天数和水下最大深度。
// requiredCapital <= 0 时回撤比例按 0 处理。
func drawdownMetrics(equityCurve []float64, sellDates []time.Time, requiredCapital float64) (maxDrawdown, maxDrawdownRatio float64, duration int, underwaterMax float64) {
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

	if requiredCapital > 0 {
		maxDrawdownRatio = maxDrawdown / requiredCapital
	}

	// 回撤时长按卖出日之间的日历天数计，而非回撤覆盖的笔数
	if maxDDEndIdx > 0 && maxDDStartIdx < len(sellDates) {
		startDate := sellDates[maxDDStartIdx]
		if startDate.IsZero() {
			// 回撤起点落在初始零值点时，以首笔卖出日为起算日期
			startDate = sellDates[1]
		}
		endDate := sellDates[maxDDEndIdx]
		if !startDate.IsZero() && !endDate.IsZero() && endDate.After(startDate) {
			duration = int(math.Round(endDate.Sub(startDate).Hours() / 24))
		}
	}

	// 水下深度以含本金的权益峰值为分母。equityCurve 仅包含累计净利润。
	peak := requiredCapital
	for _, profit := range equityCurve {
		eq := requiredCapital + profit
		if eq > peak {
			peak = eq
		}
		if requiredCapital > 0 && peak > 0 {
			depth := (peak - eq) / peak * 100
			if depth > underwaterMax {
				underwaterMax = depth
			}
		}
	}
	return
}

// riskAdjustedMetrics 计算年化 Sharpe/Sortino 与 Calmar。
// 每笔交易视为一个收益期，年化期数按实际回测跨度折算（见 annualizationPeriods）。
func riskAdjustedMetrics(trades []Trade, tradeReturns []float64, annualReturn, maxDrawdownRatio float64) (sharpe, sortino, calmar float64) {
	periodsPerYear := annualizationPeriods(trades)
	sharpe = SharpeRatio(tradeReturns, periodsPerYear)
	sortino = SortinoRatio(tradeReturns, periodsPerYear)
	calmar = CalmarRatio(annualReturn/100, maxDrawdownRatio)
	return
}

// annualizationPeriods 按实际时间跨度折算年化期数：交易笔数 / 跨度年数。
// 直接用交易笔数作年化系数会默认全部交易恰好落在一年内，跨年度或多频交易会失真。
// 跨度不足 1 天按 1 天计，最少 1 期。
func annualizationPeriods(trades []Trade) int {
	n := len(trades)
	if n == 0 {
		return 0
	}
	start, end := trades[0].BuyTime, trades[0].SellTime
	for _, t := range trades {
		if t.BuyTime.Before(start) {
			start = t.BuyTime
		}
		if t.SellTime.After(end) {
			end = t.SellTime
		}
	}
	days := end.Sub(start).Hours() / 24
	if days < 1 {
		days = 1
	}
	periods := int(math.Round(float64(n) * 365.25 / days))
	if periods < 1 {
		periods = 1
	}
	return periods
}

// averageHoldingDays 计算非虚拟成交的平均持仓天数。
func averageHoldingDays(trades []Trade) float64 {
	var total float64
	valid := 0
	for _, t := range trades {
		if !t.Virtual {
			total += float64(t.HoldingDays())
			valid++
		}
	}
	if valid == 0 {
		return 0
	}
	return total / float64(valid)
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

	// 事件扫描：买入日加入暴露，卖出日收盘后才释放（卖出当日仍持仓）。
	// 与逐日累加等价，但复杂度从 O(交易×持仓天数) 降为 O(交易 log 交易)。
	type capitalDay struct{ buys, sells float64 }
	byDay := make(map[time.Time]*capitalDay, len(allTrades)*2)
	dayOf := func(t time.Time) time.Time {
		return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
	}

	for _, t := range allTrades {
		buyCost := t.BuyCost
		if buyCost <= 0 {
			// 兼容旧数据：用原始价格 × 数量估算
			buyCost = t.BuyPrice * float64(sharesPerLot)
		}

		buyDay, sellDay := dayOf(t.BuyTime), dayOf(t.SellTime)
		if sellDay.Before(buyDay) {
			// 卖出早于买入的异常记录不产生持仓，与逐日循环口径一致
			continue
		}
		if d, ok := byDay[buyDay]; ok {
			d.buys += buyCost
		} else {
			byDay[buyDay] = &capitalDay{buys: buyCost}
		}
		if d, ok := byDay[sellDay]; ok {
			d.sells += buyCost
		} else {
			byDay[sellDay] = &capitalDay{sells: buyCost}
		}
	}

	if len(byDay) == 0 {
		return 0
	}

	days := make([]time.Time, 0, len(byDay))
	for day := range byDay {
		days = append(days, day)
	}
	sort.Slice(days, func(i, j int) bool { return days[i].Before(days[j]) })

	exposure, maxExposure := 0.0, 0.0
	for _, day := range days {
		exposure += byDay[day].buys
		if exposure > maxExposure {
			maxExposure = exposure
		}
		exposure -= byDay[day].sells
	}
	return maxExposure
}
