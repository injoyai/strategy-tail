package buy

import (
	"fmt"

	"github.com/injoyai/strategy-tail/lib/extend"
	"github.com/injoyai/strategy-tail/strategies/util"
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

// RSI拐头 是 RSI 动量反转确认策略。
// Period 表示 RSI 计算周期，默认 14。
// 触发条件：今日 RSI > 昨日 RSI，即 RSI 从下行转为上行，确认超卖反转启动。
// 通常与 RSI 超卖策略组合使用（And{RSI{}, RSI拐头{}}），
// 先确认 RSI 处于超卖区，再确认拐头向上，避免在持续下跌中接飞刀。
type RSI拐头 struct {
	Period int
}

func (s RSI拐头) Name() string {
	p := s.Period
	if p == 0 {
		p = 14
	}
	return fmt.Sprintf("RSI%d拐头", p)
}

func (s RSI拐头) Buy(code string, dks extend.Klines) bool {
	p := s.Period
	if p == 0 {
		p = 14
	}
	// 拐头需计算今日与昨日 RSI，各需 period+1 根 K 线
	if len(dks) < p+2 {
		return false
	}
	todayRSI := util.CalcRSI(dks, p)
	prevRSI := util.CalcRSI(dks[:len(dks)-1], p)
	return todayRSI > prevRSI
}
