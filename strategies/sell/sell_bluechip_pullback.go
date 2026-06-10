package sell

import (
	"fmt"

	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/tdx/extend"
)

// A跌破短期均线 是收盘价跌破短期均线即触发的卖出条件。
// Period 表示均线周期，默认 10。
// 高胜率策略的核心卖出条件：回调买入后，如果跌破短期均线说明趋势可能反转。
// 比"跌破MA20"更敏感，能更快止损。
type A跌破短期均线 struct {
	Period int
}

func (s A跌破短期均线) Name() string {
	period := s.Period
	if period == 0 {
		period = 10
	}
	return fmt.Sprintf("跌破MA%d", period)
}

func (s A跌破短期均线) Sell(code string, dks extend.Klines, b core.Buy) bool {
	period := s.Period
	if period == 0 {
		period = 10
	}
	if len(dks) < period {
		return false
	}
	closePrice := dks[len(dks)-1].Close.Float64()
	ma := core.MA(dks, period)
	return closePrice < ma
}

// A固定止盈 是盈利达到目标即卖出的保守止盈条件。
// Pct 表示止盈百分比（小数），默认 0.05（5%）。
// 高胜率策略的核心：不贪婪，达到目标就锁定利润。
type A固定止盈 struct {
	Pct float64
}

func (s A固定止盈) Name() string {
	pct := s.Pct
	if pct == 0 {
		pct = 0.05
	}
	return fmt.Sprintf("止盈%.1f%%", pct*100)
}

func (s A固定止盈) Sell(code string, dks extend.Klines, b core.Buy) bool {
	pct := s.Pct
	if pct == 0 {
		pct = 0.05
	}
	if len(dks) == 0 || b.Price.Float64() <= 0 {
		return false
	}
	profit := (dks[len(dks)-1].Close.Float64() - b.Price.Float64()) / b.Price.Float64()
	return profit >= pct
}

// A_ATR止盈 是基于ATR的保守止盈条件。
// Period 表示ATR周期，默认 14。
// Multiple 表示止盈ATR倍数，默认 2.0。
// 到达目标后立即卖出，不等待更多利润。
type A_ATR止盈 struct {
	Period   int
	Multiple float64
}

func (s A_ATR止盈) Name() string {
	mul := s.Multiple
	if mul == 0 {
		mul = 2.0
	}
	return fmt.Sprintf("ATR止盈x%.1f", mul)
}

func (s A_ATR止盈) Sell(code string, dks extend.Klines, b core.Buy) bool {
	period := s.Period
	if period == 0 {
		period = 14
	}
	mul := s.Multiple
	if mul == 0 {
		mul = 2.0
	}
	n := len(dks)
	if n < period+1 || b.Price.Float64() <= 0 {
		return false
	}
	trSum := 0.0
	for i := n - period; i < n; i++ {
		high := dks[i].High.Float64()
		low := dks[i].Low.Float64()
		prevClose := dks[i-1].Close.Float64()
		tr := high - low
		if d := high - prevClose; d > tr {
			tr = d
		}
		if d := prevClose - low; d > tr {
			tr = d
		}
		trSum += tr
	}
	atr := trSum / float64(period)
	closePrice := dks[n-1].Close.Float64()
	return closePrice >= b.Price.Float64()+mul*atr
}

// A均线死叉 是短期均线下穿长期均线的卖出条件。
// Short/Long 表示均线周期，默认 5/20。
// 比"跌破均线"更可靠的趋势反转信号。
type A均线死叉 struct {
	Short int
	Long  int
}

func (s A均线死叉) Name() string {
	short, long := s.params()
	return fmt.Sprintf("MA%d下穿MA%d", short, long)
}

func (s A均线死叉) params() (int, int) {
	short, long := s.Short, s.Long
	if short == 0 {
		short = 5
	}
	if long == 0 {
		long = 20
	}
	return short, long
}

func (s A均线死叉) Sell(code string, dks extend.Klines, b core.Buy) bool {
	short, long := s.params()
	n := len(dks)
	if n < long+1 {
		return false
	}
	// 今日：短期均线 < 长期均线
	maShortNow := core.MA(dks, short)
	maLongNow := core.MA(dks, long)
	if maShortNow >= maLongNow {
		return false
	}
	// 昨日：短期均线 >= 长期均线（刚刚下穿）
	maShortPrev := core.MA(dks[:n-1], short)
	maLongPrev := core.MA(dks[:n-1], long)
	return maShortPrev >= maLongPrev
}

// A缩量滞涨 是成交量萎缩且涨幅极小的卖出条件（动能衰竭信号）。
// VolumePeriod 表示均量周期，默认 5。
// VolumeRatio 表示今日量/均量的阈值，默认 0.6（缩量）。
// MaxRise 表示允许的最大涨幅，默认 0.5（%）。
// Lookback 表示连续滞涨天数，默认 3。
// 说明上涨动能衰竭，趋势可能即将反转。
type A缩量滞涨 struct {
	VolumePeriod int
	VolumeRatio  float64
	MaxRise      float64
	Lookback     int
}

func (s A缩量滞涨) Name() string {
	return "缩量滞涨"
}

func (s A缩量滞涨) Sell(code string, dks extend.Klines, b core.Buy) bool {
	volPeriod := s.VolumePeriod
	if volPeriod == 0 {
		volPeriod = 5
	}
	volRatio := s.VolumeRatio
	if volRatio == 0 {
		volRatio = 0.6
	}
	maxRise := s.MaxRise
	if maxRise == 0 {
		maxRise = 0.5
	}
	lookback := s.Lookback
	if lookback == 0 {
		lookback = 3
	}
	n := len(dks)
	if n < volPeriod+lookback {
		return false
	}
	// 连续N天缩量滞涨
	count := 0
	for i := n - lookback; i < n; i++ {
		// 缩量
		avg := core.AverageVolume(dks[i-volPeriod : i])
		if avg <= 0 || float64(dks[i].Volume) > avg*volRatio {
			continue
		}
		// 滞涨（涨幅极小）
		if dks[i].RiseRate() <= maxRise {
			count++
		}
	}
	return count >= lookback
}
