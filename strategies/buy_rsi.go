package strategies

import (
	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/tdx/extend"
	"github.com/injoyai/tdx/protocol"
)

// BuyRSI 是 RSI 超卖买入策略。
// Period 表示 RSI 计算周期，默认 14。
// Threshold 表示买入阈值，默认 20。
// 当最新一个交易日 RSI 小于 Threshold 时返回买入信号。
// 买入价使用最新交易日收盘价。
type BuyRSI struct {
	Period    int
	Threshold float64
}

func (s BuyRSI) Name() string {
	return "RSI买入"
}

func (s BuyRSI) Buy(code string, dks extend.Klines, mks protocol.Klines) *core.Buy {

	if s.Period == 0 {
		s.Period = 14
	}
	if s.Threshold == 0 {
		s.Threshold = 20
	}

	if len(dks) < s.Period+1 {
		return nil
	}

	rsi := calcRSI(dks, s.Period)
	if rsi >= s.Threshold {
		return nil
	}

	today := dks[len(dks)-1]
	return &core.Buy{
		Code:  code,
		Time:  today.Time,
		Price: today.Close,
	}
}

// calcRSI 根据日线收盘价计算最近 period 个交易日的 RSI。
// 这里使用简单平均涨跌幅计算，返回值范围通常为 0 到 100。
// 返回 50 表示最近周期内没有明显涨跌变化。
func calcRSI(dks extend.Klines, period int) float64 {
	if period <= 0 || len(dks) < period+1 {
		return 0
	}

	start := len(dks) - period
	if start < 1 {
		start = 1
	}

	gainSum := 0.0
	lossSum := 0.0
	for i := start; i < len(dks); i++ {
		change := dks[i].Close.Float64() - dks[i-1].Close.Float64()
		if change > 0 {
			gainSum += change
		} else if change < 0 {
			lossSum += -change
		}
	}

	avgGain := gainSum / float64(period)
	avgLoss := lossSum / float64(period)

	if avgLoss == 0 {
		if avgGain == 0 {
			return 50
		}
		return 100
	}
	rs := avgGain / avgLoss
	return 100 - (100 / (1 + rs))
}
