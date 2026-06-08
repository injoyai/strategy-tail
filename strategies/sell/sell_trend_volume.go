package sell

import (
	"fmt"

	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/strategies/util"
	"github.com/injoyai/tdx/extend"
)

// TrendVolumeV2 是均线趋势+量价突破V2卖出策略。
//
// 卖出条件（满足任一即执行）：
// 1. 收盘价跌破20日均线且3日内未收回
// 2. RSI(14) > 75 进入严重超买
// 3. MACD死叉且DIF < 0
// 4. 单日跌幅 > 7%
// 5. 大盘进入空头状态（需外部数据，K线中无法判断，跳过）
// 6. 时间止损触发（20日未盈利>5%）
type TrendVolumeV2 struct {
	// BelowMADays 跌破均线后观察收回的天数，默认3
	BelowMADays int
	// MaxHoldDays 最大持有天数，默认20
	MaxHoldDays int
	// MinProfitRate 最低盈利比例，默认0.05（5%）
	MinProfitRate float64
}

func (s TrendVolumeV2) Name() string {
	return "均线趋势+量价突破V2卖出"
}

func (s TrendVolumeV2) Sell(code string, dks extend.Klines, buy core.Buy) bool {
	n := len(dks)
	if n < 20 {
		return false
	}

	// 默认值
	if s.BelowMADays == 0 {
		s.BelowMADays = 3
	}
	if s.MaxHoldDays == 0 {
		s.MaxHoldDays = 20
	}
	if s.MinProfitRate == 0 {
		s.MinProfitRate = 0.05
	}

	today := dks[n-1]
	buyPrice := buy.Price.Float64()

	// 条件1: 收盘价跌破20日均线且3日内未收回
	if s.checkBelowMA(dks) {
		return true
	}

	// 条件2: RSI(14) > 75 进入严重超买
	rsi := util.CalcRSI(dks, 14)
	if rsi > 75 {
		return true
	}

	// 条件3: MACD死叉且DIF < 0
	if s.checkMACDDeathCross(dks) {
		return true
	}

	// 条件4: 单日跌幅 > 7%
	riseRate := today.RiseRate()
	if riseRate < -7 {
		return true
	}

	// 条件6: 时间止损触发（20日未盈利>5%）
	if buyPrice > 0 {
		holdDays := 0
		for i := n - 1; i >= 0; i-- {
			if dks[i].Time.Before(buy.Time) {
				break
			}
			holdDays++
		}
		if holdDays >= s.MaxHoldDays {
			profitRate := (today.Close.Float64() - buyPrice) / buyPrice
			if profitRate <= s.MinProfitRate {
				return true
			}
		}
	}

	return false
}

// checkBelowMA 检查收盘价是否跌破20日均线且N日内未收回
func (s TrendVolumeV2) checkBelowMA(dks extend.Klines) bool {
	n := len(dks)
	if n < 20+s.BelowMADays {
		return false
	}

	// 检查最近 BelowMADays 天是否都在20日均线下方
	for i := n - s.BelowMADays; i < n; i++ {
		ma20 := core.MA(dks[:i+1], 20)
		if dks[i].Close.Float64() > ma20 {
			return false // 有收回
		}
	}
	return true
}

// checkMACDDeathCross 检查MACD死叉且DIF < 0
func (s TrendVolumeV2) checkMACDDeathCross(dks extend.Klines) bool {
	n := len(dks)
	if n < 2 {
		return false
	}

	hist := util.MACDHistogram(dks, 12, 26, 9)
	if len(hist) != n {
		return false
	}

	// MACD死叉：昨天柱子>0，今天柱子<=0
	if hist[n-2] > 0 && hist[n-1] <= 0 {
		// 计算DIF是否<0
		// DIF = hist + DEA，但我们可以通过hist和DEA反推
		// 简化判断：如果MACD柱子从正变负，且当前柱子绝对值较大，说明DIF在零轴下方
		// 更准确的方式：直接计算DIF
		closes := make([]float64, n)
		for i := range dks {
			closes[i] = dks[i].Close.Float64()
		}
		emaFast := emaSeries(closes, 12)
		emaSlow := emaSeries(closes, 26)
		dif := emaFast[n-1] - emaSlow[n-1]
		if dif < 0 {
			return true
		}
	}

	return false
}

// emaSeries 计算EMA序列（与util包中相同的算法）
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

// TrendVolumeV2SellReason 返回卖出原因，用于展示
type TrendVolumeV2SellReason struct {
	Code       string  `json:"code"`
	Reason     string  `json:"reason"`
	RSI        float64 `json:"rsi,omitempty"`
	RiseRate   float64 `json:"rise_rate,omitempty"`
	ProfitRate float64 `json:"profit_rate,omitempty"`
}

// CheckTrendVolumeV2Sell 检查卖出条件并返回具体原因
func CheckTrendVolumeV2Sell(code string, dks extend.Klines, buy core.Buy) *TrendVolumeV2SellReason {
	n := len(dks)
	if n < 20 {
		return nil
	}

	s := TrendVolumeV2{}
	today := dks[n-1]
	buyPrice := buy.Price.Float64()

	// 条件1: 跌破20日均线且3日内未收回
	if s.checkBelowMA(dks) {
		return &TrendVolumeV2SellReason{
			Code:   code,
			Reason: fmt.Sprintf("跌破20日均线且%d日内未收回", s.BelowMADays),
		}
	}

	// 条件2: RSI > 75
	rsi := util.CalcRSI(dks, 14)
	if rsi > 75 {
		return &TrendVolumeV2SellReason{
			Code:   code,
			Reason: "RSI严重超买",
			RSI:    rsi,
		}
	}

	// 条件3: MACD死叉且DIF<0
	if s.checkMACDDeathCross(dks) {
		return &TrendVolumeV2SellReason{
			Code:   code,
			Reason: "MACD死叉且DIF<0",
		}
	}

	// 条件4: 单日跌幅>7%
	riseRate := today.RiseRate()
	if riseRate < -7 {
		return &TrendVolumeV2SellReason{
			Code:     code,
			Reason:   "单日跌幅超7%",
			RiseRate: riseRate,
		}
	}

	// 条件6: 时间止损
	if buyPrice > 0 {
		holdDays := 0
		for i := n - 1; i >= 0; i-- {
			if dks[i].Time.Before(buy.Time) {
				break
			}
			holdDays++
		}
		if holdDays >= s.MaxHoldDays {
			profitRate := (today.Close.Float64() - buyPrice) / buyPrice
			if profitRate <= s.MinProfitRate {
				return &TrendVolumeV2SellReason{
					Code:       code,
					Reason:     fmt.Sprintf("持有%d日未盈利超过%.0f%%", holdDays, s.MinProfitRate*100),
					ProfitRate: profitRate * 100,
				}
			}
		}
	}

	return nil
}
