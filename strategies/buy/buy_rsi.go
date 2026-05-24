package buy

import (
	"github.com/injoyai/strategy-tail/strategies/util"
	"github.com/injoyai/tdx/extend"
)

// RSI 是 RSI 超卖买入策略。
// Period 表示 RSI 计算周期，默认 14。
// Threshold 表示买入阈值，默认 20。
// 当最新一个交易日 RSI 小于 Threshold 时返回买入信号。
// 买入价使用最新交易日收盘价。
type RSI struct {
	Period    int
	Threshold float64
}

func (s RSI) Name() string {
	return "RSI"
}

func (s RSI) Buy(code string, dks extend.Klines) bool {

	if s.Period == 0 {
		s.Period = 14
	}
	if s.Threshold == 0 {
		s.Threshold = 20
	}

	if len(dks) < s.Period+1 {
		return false
	}

	rsi := util.CalcRSI(dks, s.Period)
	if rsi >= s.Threshold {
		return false
	}

	return true
}
