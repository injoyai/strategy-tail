package core

import (
	"fmt"
	"math"
	"path/filepath"
	"sort"
	"time"

	"github.com/injoyai/goutil/g"
	"github.com/injoyai/goutil/oss"
	"github.com/injoyai/goutil/other/csv"
	"github.com/injoyai/tdx/extend"
)

func Analyze(allTrades []Trade, getDayKlines func(code string) (extend.Klines, error)) {

	// 2. 按时间排序，为了计算资金曲线和回撤
	sort.Slice(allTrades, func(i, j int) bool {
		return allTrades[i].BuyTime.Before(allTrades[j].BuyTime)
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
		buy := float64(t.BuyPrice) / 1000.0
		sell := float64(t.SellPrice) / 1000.0
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
	fmt.Printf("总交易次数: \t%d*2\n", totalTrades)

	if totalTrades > 0 {
		winRate := float64(winCount) / float64(totalTrades) * 100
		fmt.Printf("胜率: \t\t%.2f%%\n", winRate)
		fmt.Printf("总盈亏: \t\t%.2f元\n", totalProfit*100)
		fmt.Printf("平均每笔盈亏: \t%.2f元\n", totalProfit/float64(totalTrades)*100)
		fmt.Printf("最大单笔盈利: \t%.2f元\n", maxProfit*100)
		fmt.Printf("最大单笔亏损: \t%.2f元\n", maxLoss*100)

		profitFactor := 0.0
		if grossLoss != 0 {
			profitFactor = grossProfit / grossLoss
			fmt.Printf("盈亏比: \t\t%.2f\n", profitFactor)
		} else {
			fmt.Printf("盈亏比: \t\t∞ (无亏损)\n")
		}

		fmt.Printf("最大回撤: \t\t%.2f元\n", maxDrawdown*100)
	}
	requiredCapital := calculateRequiredCapital(allTrades)
	fmt.Printf("所需最低本金: \t\t%.2f元\n", requiredCapital)
	if requiredCapital > 0 {
		fmt.Printf("年化收益率: \t\t%.2f%%\n", totalProfit*100/requiredCapital*100)
	}

	// 统计买入后多个持有周期的平均收益情况（按日线收盘价计算）
	if totalTrades > 0 {
		horizons := []int{1, 2, 3, 5, 10, 15, 20, 30, 45, 60}
		sums := make([]float64, len(horizons))
		counts := make([]int, len(horizons))

		// 按代码分组，避免重复拉取K线
		codeTrades := map[string][]Trade{}
		for _, t := range allTrades {
			codeTrades[t.Code] = append(codeTrades[t.Code], t)
		}

		for code, trades := range codeTrades {
			dks, err := getDayKlines(code)
			if err != nil || len(dks) == 0 {
				continue
			}

			// 建立日期到索引的映射
			dateIndex := make(map[string]int, len(dks))
			for i, k := range dks {
				dateIndex[k.Time.Format(time.DateOnly)] = i
			}

			for _, t := range trades {
				buyDate := t.BuyTime.Format(time.DateOnly)
				idx, ok := dateIndex[buyDate]
				if !ok || idx >= len(dks) {
					continue
				}
				buyClose := dks[idx].Close.Float64()
				if buyClose <= 0 {
					continue
				}

				for hi, h := range horizons {
					targetIdx := idx + h
					if targetIdx >= len(dks) {
						continue
					}
					sellClose := dks[targetIdx].Close.Float64()
					r := (sellClose - buyClose) / buyClose
					sums[hi] += r
					counts[hi]++
				}
			}
		}

		fmt.Printf("------------------------------------------------------\n")
		fmt.Printf("买入后持有1/2/3/5/10/15/20/30/45/60个交易日平均收益:\n")
		for i, h := range horizons {
			if counts[i] == 0 {
				fmt.Printf("  持有%2d日: 数据不足\n", h)
				continue
			}
			avg := sums[i] / float64(counts[i]) * 100
			fmt.Printf("  持有%2d日: 平均收益 %.2f%% (样本数 %d)\n", h, avg, counts[i])
		}
	}

	fmt.Printf("======================================================\n")

	data := [][]any{
		{"代码", "买入时间", "买入价格", "卖出时间", "卖出价格", "盈亏", "持有天数"},
	}

	for _, v := range allTrades {
		data = append(data, []any{
			v.Code,
			v.BuyTime.Format(time.DateTime), v.BuyPrice.Float64(),
			v.SellTime.Format(time.DateTime), v.SellPrice.Float64(),
			(v.SellPrice - v.BuyPrice).Float64() * 100,
			v.SellTime.Sub(v.BuyTime).String(),
		})
	}

	buf, err := csv.Export(data)
	if err == nil {
		output := filepath.Join("./output/", time.Now().Format("20060102150415")+".csv")
		oss.New(output, buf)
	}
}

func calculateRequiredCapital(allTrades []Trade) float64 {

	m := map[string][]Trade{}

	for _, v := range allTrades {
		m[v.BuyTime.Format(time.DateOnly)] = append(m[v.BuyTime.Format(time.DateOnly)], v)
	}

	if len(m) == 0 {
		return 0
	}

	xx := make([]float64, 0, len(m))
	for _, ls := range m {
		xx = append(xx, func() float64 {
			x := float64(0)
			for _, v := range ls {
				x += v.BuyPrice.Float64() * 100
			}
			return x
		}())
	}

	return g.Max(0., xx...)

}
