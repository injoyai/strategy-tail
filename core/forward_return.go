package core

import (
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
