package main

import (
	"fmt"
	"math"
	"sort"
)

func Analyze(results []BacktestResp) {
	// 1. 收集所有交易
	allTrades := []Trade{}
	for _, res := range results {
		allTrades = append(allTrades, res.Trades...)
	}

	// 2. 按时间排序，为了计算资金曲线和回撤
	sort.Slice(allTrades, func(i, j int) bool {
		return allTrades[i].Time.Before(allTrades[j].Time)
	})

	var totalTrades int = len(allTrades)
	var winCount int
	var totalProfit float64
	var grossProfit float64
	var grossLoss float64

	var maxProfit float64 = -math.MaxFloat64
	var maxLoss float64 = math.MaxFloat64

	// 资金曲线
	var equityCurve []float64
	currentEquity := 0.0
	equityCurve = append(equityCurve, currentEquity)

	for _, t := range allTrades {
		// Price 是 int64 类型, 单位是厘 (0.001元)
		buy := float64(t.Buy) / 1000.0
		sell := float64(t.Sell) / 1000.0
		profit := sell - buy

		totalProfit += profit
		currentEquity += profit
		equityCurve = append(equityCurve, currentEquity)

		if profit > 0 {
			winCount++
			grossProfit += profit
		} else {
			grossLoss += math.Abs(profit)
		}

		if profit > maxProfit {
			maxProfit = profit
		}
		if profit < maxLoss {
			maxLoss = profit
		}
	}

	// 计算最大回撤
	var maxDrawdown float64
	var peakEquity float64 = -math.MaxFloat64

	for _, eq := range equityCurve {
		if eq > peakEquity {
			peakEquity = eq
		}
		drawdown := peakEquity - eq
		if drawdown > maxDrawdown {
			maxDrawdown = drawdown
		}
	}

	// 输出统计结果
	fmt.Printf("\n==================== 回测统计报告 ====================\n")
	fmt.Printf("总交易次数: \t%d\n", totalTrades)

	if totalTrades > 0 {
		winRate := float64(winCount) / float64(totalTrades) * 100
		fmt.Printf("胜率: \t\t%.2f%%\n", winRate)
		fmt.Printf("总盈亏: \t\t%.2f元/手\n", totalProfit*100)
		fmt.Printf("平均每笔盈亏: \t%.2f元/手\n", totalProfit/float64(totalTrades)*100)
		fmt.Printf("最大单笔盈利: \t%.2f元/手\n", maxProfit*100)
		fmt.Printf("最大单笔亏损: \t%.2f元/手\n", maxLoss*100)

		profitFactor := 0.0
		if grossLoss != 0 {
			profitFactor = grossProfit / grossLoss
			fmt.Printf("盈亏比: \t\t%.2f\n", profitFactor)
		} else {
			fmt.Printf("盈亏比: \t\t∞ (无亏损)\n")
		}

		fmt.Printf("最大回撤: \t\t%.2f元/手\n", maxDrawdown*100)
	}
	fmt.Printf("======================================================\n")
}
