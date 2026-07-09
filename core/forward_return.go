package core

import (
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/injoyai/bar"
	"github.com/injoyai/logs"
	"github.com/injoyai/strategy-tail/lib/extend"
	"github.com/injoyai/tdx/protocol"
)

// ============================================================================
// 买入信号未来N天收益分析(独立于 Backtest 引擎)
// ============================================================================

// DefaultForwardDays 返回默认的未来收益统计天数。
func DefaultForwardDays() []int {
	return []int{1, 3, 5, 10, 15, 20, 30}
}

// ForwardReturn 单次买入信号的未来收益记录。
type ForwardReturn struct {
	Code     string
	BuyTime  time.Time
	BuyPrice protocol.Price
	// Returns: N天 -> 收益率% = (dks[i+N].Close - BuyPrice) / BuyPrice * 100
	Returns map[int]float64
}

// ForwardReturnSummary 按N天汇总的统计指标。
type ForwardReturnSummary struct {
	Days         int
	Count        int     // 有效信号数(有足够未来数据的)
	AvgReturn    float64 // 平均收益率%
	MedianReturn float64 // 中位数收益率%
	WinRate      float64 // 正收益占比%
	MaxReturn    float64 // 最大收益率%
	MinReturn    float64 // 最小收益率%(负数)
}

// ForwardReturnAnalysis 独立分析器,与 Backtest 无关。
// 只需提供买入策略和日线数据源,统计信号触发后未来N天的收益分布。
type ForwardReturnAnalysis struct {
	Buyer
	Codes        []string
	Years        []int
	GetDayKlines GetDayKlines
	ForwardDays  []int // 为空时使用 DefaultForwardDays()
	Goroutines   int
}

// Scan 对单只股票执行信号扫描,返回每次买入信号的未来收益记录。
// his 为历史K线(当年之前),dks 为当年日线。
// ls 构建方式与 Backtest.Do() 一致,确保信号检测结果相同。
func (this ForwardReturnAnalysis) Scan(code string, his, dks extend.Klines) []ForwardReturn {
	if len(dks) == 0 {
		return nil
	}

	days := this.ForwardDays
	if len(days) == 0 {
		days = DefaultForwardDays()
	}

	joinKlines := func(base extend.Klines, extra ...*extend.Kline) extend.Klines {
		ls := make(extend.Klines, 0, len(base)+len(extra))
		ls = append(ls, base...)
		ls = append(ls, extra...)
		return ls
	}

	result := []ForwardReturn(nil)
	for i := 0; i < len(dks); i++ {
		today := dks[i]
		_his := joinKlines(his, dks[:i]...)
		ls := joinKlines(_his, today)

		if !this.Buy(code, ls) {
			continue
		}

		buyPrice := today.Close
		returns := make(map[int]float64, len(days))
		for _, n := range days {
			idx := i + n
			if idx < len(dks) {
				futureClose := dks[idx].Close
				bp := buyPrice.Float64()
				if bp > 0 {
					returns[n] = (futureClose.Float64() - bp) / bp * 100
				}
			}
		}

		result = append(result, ForwardReturn{
			Code:     code,
			BuyTime:  today.Time,
			BuyPrice: buyPrice,
			Returns:  returns,
		})
	}

	return result
}

// SummarizeForwardReturns 按N天汇总所有信号的未来收益统计。
func SummarizeForwardReturns(returns []ForwardReturn, days []int) []ForwardReturnSummary {
	summaries := make([]ForwardReturnSummary, 0, len(days))
	for _, n := range days {
		values := []float64(nil)
		for _, fr := range returns {
			if r, ok := fr.Returns[n]; ok {
				values = append(values, r)
			}
		}
		summaries = append(summaries, summarizeOne(n, values))
	}
	return summaries
}

// summarizeOne 计算单个N天的收益统计。
func summarizeOne(days int, values []float64) ForwardReturnSummary {
	if len(values) == 0 {
		return ForwardReturnSummary{Days: days, Count: 0}
	}

	sum := 0.0
	win := 0
	maxVal := values[0]
	minVal := values[0]
	for _, v := range values {
		sum += v
		if v > 0 {
			win++
		}
		if v > maxVal {
			maxVal = v
		}
		if v < minVal {
			minVal = v
		}
	}

	// 中位数
	sorted := make([]float64, len(values))
	copy(sorted, values)
	sort.Float64s(sorted)
	median := sorted[len(sorted)/2]
	if len(sorted)%2 == 0 {
		median = (sorted[len(sorted)/2-1] + sorted[len(sorted)/2]) / 2
	}

	return ForwardReturnSummary{
		Days:         days,
		Count:        len(values),
		AvgReturn:    sum / float64(len(values)),
		MedianReturn: median,
		WinRate:      float64(win) / float64(len(values)) * 100,
		MaxReturn:    maxVal,
		MinReturn:    minVal,
	}
}

// Run 执行全部分析:并发扫描买入信号 -> 汇总统计 -> 控制台输出 -> HTML报告。
func (this ForwardReturnAnalysis) Run() {
	days := this.ForwardDays
	if len(days) == 0 {
		days = DefaultForwardDays()
	}

	buyerName := "未知策略"
	if this.Buyer != nil {
		buyerName = this.Buyer.Name()
	}
	logs.Info("买入信号未来N天收益分析: " + buyerName)

	allReturns := []ForwardReturn(nil)
	for _, year := range this.Years {
		yearReturns := this.scanYear(year)
		allReturns = append(allReturns, yearReturns...)
	}

	if len(allReturns) == 0 {
		logs.Warn("未检测到任何买入信号")
		return
	}

	logs.Infof("共检测到 %d 个买入信号", len(allReturns))

	summaries := SummarizeForwardReturns(allReturns, days)
	PrintForwardReturnSummary(buyerName, summaries)
	exportForwardReturnHTML(buyerName, summaries, allReturns, days)
}

// scanYear 扫描单个年份的所有股票买入信号。
func (this ForwardReturnAnalysis) scanYear(year int) []ForwardReturn {
	hisStart := time.Date(year-2, 6, 1, 0, 0, 0, 0, time.Local)
	start := time.Date(year, 1, 1, 0, 0, 0, 0, time.Local)
	end := time.Date(year, 12, 31, 23, 0, 0, 0, time.Local)

	goroutines := this.Goroutines
	if goroutines <= 0 {
		goroutines = 10
	}

	result := []ForwardReturn(nil)
	mu := sync.Mutex{}
	b := bar.NewCoroutine(
		len(this.Codes),
		goroutines,
		bar.WithPrefix(fmt.Sprintf("[%d][%s]", year, "xx000000")),
	)
	defer b.Close()

	for _, code := range this.Codes {
		b.Go(func() {
			b.SetPrefix(fmt.Sprintf("[%d][%s]", year, code))

			dks, err := this.GetDayKlines(code, hisStart, end)
			if err != nil {
				b.Logf("[错误] %s", err)
				b.Flush()
				return
			}

			his := []*extend.Kline(nil)
			for i, v := range dks {
				if v.Time.Before(start) {
					his = append(his, v)
				} else {
					dks = dks[i:]
					break
				}
			}

			frs := this.Scan(code, his, dks)
			mu.Lock()
			defer mu.Unlock()
			result = append(result, frs...)
		})
	}
	b.Wait()
	return result
}

// PrintForwardReturnSummary 打印未来N天收益汇总到控制台。
func PrintForwardReturnSummary(buyerName string, summaries []ForwardReturnSummary) {
	fmt.Printf("\n买入信号未来N天收益分析: %s\n\n", buyerName)
	fmt.Printf("%5s \t%8s \t%10s \t%10s \t%8s \t%10s \t%10s\n",
		"N天", "信号数", "平均收益", "中位数", "胜率", "最大收益", "最大亏损")
	for _, s := range summaries {
		fmt.Printf("%6d \t%10d \t%12.2f%% \t%12.2f%% \t%9.1f%% \t%12.2f%% \t%12.2f%%\n",
			s.Days, s.Count, s.AvgReturn, s.MedianReturn, s.WinRate, s.MaxReturn, s.MinReturn)
	}
}

// exportForwardReturnHTML 生成HTML报告(Task 5实现)。
func exportForwardReturnHTML(buyerName string, summaries []ForwardReturnSummary, allReturns []ForwardReturn, days []int) {
	// Task 5 将实现HTML报告生成
}
