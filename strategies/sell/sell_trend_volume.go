package sell

import (
	"fmt"

	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/lib/extend"
	"github.com/injoyai/strategy-tail/strategies/util"
)

// A收盘跌破均线 是收盘价连续N日跌破指定均线的卖出条件。
// Period 表示均线周期，默认 20。
// Days 表示连续跌破的天数，默认 3。
// 当最近 Days 天收盘价都低于均线时返回卖出信号。
type A收盘跌破均线 struct {
	Period int
	Days   int
}

func (s A收盘跌破均线) Name() string {
	period := s.Period
	if period == 0 {
		period = 20
	}
	days := s.Days
	if days == 0 {
		days = 3
	}
	return fmt.Sprintf("跌破%d日均线%d日未收回", period, days)
}

func (s A收盘跌破均线) Sell(code string, dks extend.Klines, buy core.Buy) bool {
	period := s.Period
	if period == 0 {
		period = 20
	}
	days := s.Days
	if days == 0 {
		days = 3
	}
	n := len(dks)
	if n < period+days {
		return false
	}
	for i := n - days; i < n; i++ {
		ma := core.MA(dks[:i+1], period)
		if dks[i].Close.Float64() > ma {
			return false
		}
	}
	return true
}

// A_RSI超买 是 RSI 大于指定阈值的卖出条件。
// Period 表示 RSI 计算周期，默认 14。
// Threshold 表示超买阈值，默认 75。
type A_RSI超买 struct {
	Period    int
	Threshold float64
}

func (s A_RSI超买) Name() string {
	threshold := s.Threshold
	if threshold == 0 {
		threshold = 75
	}
	return fmt.Sprintf("RSI>%.0f", threshold)
}

func (s A_RSI超买) Sell(code string, dks extend.Klines, buy core.Buy) bool {
	period := s.Period
	if period == 0 {
		period = 14
	}
	threshold := s.Threshold
	if threshold == 0 {
		threshold = 75
	}
	if len(dks) < period+1 {
		return false
	}
	return util.CalcRSI(dks, period) > threshold
}

// A_MACD死叉且DIF为负 是 MACD 死叉同时 DIF 小于 0 的卖出条件。
// Fast/Slow/Signal 为 MACD 参数，默认 12/26/9。
type A_MACD死叉且DIF为负 struct {
	Fast   int
	Slow   int
	Signal int
}

func (s A_MACD死叉且DIF为负) Name() string {
	return "MACD死叉且DIF<0"
}

func (s A_MACD死叉且DIF为负) Sell(code string, dks extend.Klines, buy core.Buy) bool {
	fast, slow, signal := defaultMACDParams(s.Fast, s.Slow, s.Signal)
	n := len(dks)
	if n < slow+signal {
		return false
	}
	hist := util.MACDHistogram(dks, fast, slow, signal)
	if len(hist) != n {
		return false
	}
	// 死叉：昨日柱>0，今日柱<=0
	if !(hist[n-2] > 0 && hist[n-1] <= 0) {
		return false
	}
	// 计算 DIF
	closes := make([]float64, n)
	for i := range dks {
		closes[i] = dks[i].Close.Float64()
	}
	emaFast := emaSeries(closes, fast)
	emaSlow := emaSeries(closes, slow)
	dif := emaFast[n-1] - emaSlow[n-1]
	return dif < 0
}

// A单日跌幅大于 是单日跌幅超过指定值的卖出条件。
// Max 表示触发卖出的跌幅阈值（%），默认 7。
// 例如 Max=7 表示当日跌幅超过 7% 时返回卖出信号。
type A单日跌幅大于 struct {
	Max float64
}

func (s A单日跌幅大于) Name() string {
	max := s.Max
	if max == 0 {
		max = 7
	}
	return fmt.Sprintf("单日跌幅>%.0f%%", max)
}

func (s A单日跌幅大于) Sell(code string, dks extend.Klines, buy core.Buy) bool {
	max := s.Max
	if max == 0 {
		max = 7
	}
	if len(dks) == 0 {
		return false
	}
	return dks[len(dks)-1].RiseRate() < -max
}

// A时间止损 是持有时间到期且未达到最低盈利的卖出条件。
// MaxHoldDays 表示最大持有天数，默认 20。
// MinProfitRate 表示最低盈利比例（小数），默认 0.05（5%）。
// 当持有天数 >= MaxHoldDays 且收益率 <= MinProfitRate 时返回卖出信号。
type A时间止损 struct {
	MaxHoldDays int
}

func (s A时间止损) Name() string {
	days := s.MaxHoldDays
	if days == 0 {
		days = 20
	}
	return fmt.Sprintf("持有%d日", days)
}

func (s A时间止损) Sell(code string, dks extend.Klines, buy core.Buy) bool {
	maxDays := s.MaxHoldDays
	if maxDays == 0 {
		maxDays = 20
	}
	n := len(dks)
	if n == 0 {
		return false
	}
	buyPrice := buy.Price.Float64()
	if buyPrice <= 0 {
		return false
	}
	// 计算持有天数（按K线数）
	holdDays := 0
	for i := n - 1; i >= 0; i-- {
		if !dks[i].Time.After(buy.Time) {
			break
		}
		holdDays++
	}
	return holdDays >= maxDays
}

// defaultMACDParams 返回默认的 MACD 参数（12, 26, 9）
func defaultMACDParams(fast, slow, signal int) (int, int, int) {
	if fast == 0 {
		fast = 12
	}
	if slow == 0 {
		slow = 26
	}
	if signal == 0 {
		signal = 9
	}
	return fast, slow, signal
}

// emaSeries 计算 EMA 序列（与util包中相同算法）
func emaSeries(values []float64, period int) []float64 {
	n := len(values)
	out := make([]float64, n)
	if n == 0 {
		return out
	}
	if period <= 1 {
		copy(out, values)
		return out
	}
	alpha := 2.0 / (float64(period) + 1.0)
	out[0] = values[0]
	for i := 1; i < n; i++ {
		out[i] = out[i-1] + alpha*(values[i]-out[i-1])
	}
	return out
}

// A固定止损 是相对买入价跌幅超过阈值的卖出条件。
// Pct 表示止损百分比，默认 0.05（即-5%）。
// 立即触发，无延迟。建议作为第一道防线。
type A固定止损 struct {
	Pct float64
}

func (s A固定止损) Name() string {
	pct := s.Pct
	if pct == 0 {
		pct = 0.05
	}
	return fmt.Sprintf("固定止损%.1f%%", pct*100)
}

func (s A固定止损) Sell(code string, dks extend.Klines, b core.Buy) bool {
	pct := s.Pct
	if pct == 0 {
		pct = 0.05
	}
	if len(dks) == 0 || b.Price.Float64() <= 0 {
		return false
	}
	loss := (dks[len(dks)-1].Close.Float64() - b.Price.Float64()) / b.Price.Float64()
	return loss <= -pct
}

// A_ATR止损 是基于ATR动态止损的卖出条件。
// Period 表示ATR周期，默认14。
// Multiple 表示止损倍数，默认2.5（最大回撤=2.5*ATR）。
// 适应不同波动性的股票，比固定百分比止损更科学。
type A_ATR止损 struct {
	Period   int
	Multiple float64
}

func (s A_ATR止损) Name() string {
	mul := s.Multiple
	if mul == 0 {
		mul = 2.5
	}
	return fmt.Sprintf("ATR止损x%.1f", mul)
}

func (s A_ATR止损) Sell(code string, dks extend.Klines, b core.Buy) bool {
	period := s.Period
	if period == 0 {
		period = 14
	}
	mul := s.Multiple
	if mul == 0 {
		mul = 2.5
	}
	n := len(dks)
	if n < period+1 || b.Price.Float64() <= 0 {
		return false
	}
	// 计算ATR
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
	return closePrice < b.Price.Float64()-mul*atr
}

// A移动止盈 是盈利达到一定水平后回吐部分利润触发的卖出条件。
// ActivateProfitPct 表示激活移动止盈的最低盈利（小数），默认0.05（5%）。
// RetreatPct 表示从最高点回撤的比例，默认0.5（即回吐50%利润）。
// 例如：盈利10%后回撤至5%（10%×0.5）即卖出。
type A移动止盈 struct {
	ActivateProfitPct float64
	RetreatPct        float64
}

func (s A移动止盈) Name() string {
	return "移动止盈"
}

func (s A移动止盈) Sell(code string, dks extend.Klines, b core.Buy) bool {
	activate := s.ActivateProfitPct
	if activate == 0 {
		activate = 0.05
	}
	retreat := s.RetreatPct
	if retreat == 0 {
		retreat = 0.5
	}
	buyPrice := b.Price.Float64()
	if buyPrice <= 0 || len(dks) == 0 {
		return false
	}
	// 从买入日之后计算最高收益
	maxProfit := 0.0
	for i := len(dks) - 1; i >= 0; i-- {
		if dks[i].Time.Before(b.Time) {
			break
		}
		profit := (dks[i].High.Float64() - buyPrice) / buyPrice
		if profit > maxProfit {
			maxProfit = profit
		}
	}
	// 还没达到激活线
	if maxProfit < activate {
		return false
	}
	// 当前收益
	curProfit := (dks[len(dks)-1].Close.Float64() - buyPrice) / buyPrice
	// 回吐超过 retreat 比例则卖出
	return curProfit <= maxProfit*(1-retreat)
}

// A放量大阴线 是当日放量下跌的卖出条件（"出货线"）。
// VolumeRatio 表示成交量放大倍数（相对前5日均量），默认1.5。
// FallPct 表示最低跌幅，默认2.0（%）。
// 主力出货信号，应立即离场。
type A放量大阴线 struct {
	VolumeRatio float64
	FallPct     float64
}

func (s A放量大阴线) Name() string {
	return "放量大阴线"
}

func (s A放量大阴线) Sell(code string, dks extend.Klines, b core.Buy) bool {
	volRatio := s.VolumeRatio
	if volRatio == 0 {
		volRatio = 1.5
	}
	fallPct := s.FallPct
	if fallPct == 0 {
		fallPct = 2.0
	}
	n := len(dks)
	if n < 6 {
		return false
	}
	today := dks[n-1]
	if today.Close >= today.Open {
		return false
	}
	if today.RiseRate() > -fallPct {
		return false
	}
	avgVol := core.AverageVolume(dks[n-6 : n-1])
	if avgVol <= 0 {
		return false
	}
	return float64(today.Volume) > avgVol*volRatio
}
