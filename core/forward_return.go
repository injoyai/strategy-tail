package core

import (
	"sort"
	"time"

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
