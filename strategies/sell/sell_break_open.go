package sell

import (
	"time"

	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/lib/extend"
	"github.com/injoyai/strategy-tail/strategies/rule"
)

// A跌破买入日开盘 是基于买入日开盘价的卖出条件。
// 触发条件：当日收盘价跌破买入当天的开盘价。
// 注：dks 只包含交易日 K 线，按日期匹配定位买入日。
type A跌破买入日开盘 struct{}

func (s A跌破买入日开盘) Name() string {
	return "跌破买入日开盘"
}

func (s A跌破买入日开盘) Sell(code string, dks extend.Klines, buy core.Buy) bool {
	// 判断委托共用内核，与旧实现同序：先判"今日非买入日"（旧实现买入日
	// 不触发的预检查，And 左侧短路保持次序），再比较末根收盘价(厘) <
	// 买入日开盘价(厘)，Lt 厘级 int64 精确等旧 protocol.Price 直接比较。
	// 买入日开盘价沿用旧逆序日期串匹配定位，找不到（或持仓不可用）返回
	// 无效值，比较不触发等旧 return false。
	notBuyDay, err := rule.NewRuleFunc("今日非买入日", func(in rule.Input) bool {
		n := len(in.Klines)
		if n == 0 {
			return false
		}
		pos, ok := in.Position()
		if !ok {
			return false
		}
		return in.Klines[n-1].Time.Format(time.DateOnly) != pos.Time.Format(time.DateOnly)
	})
	if err != nil {
		return false
	}
	buyOpen := rule.OperandFunc("买入日开盘(厘)", func(in rule.Input) rule.Value {
		pos, ok := in.Position()
		if !ok {
			return rule.InvalidValue()
		}
		buyDate := pos.Time.Format(time.DateOnly)
		for i := len(in.Klines) - 1; i >= 0; i-- {
			if in.Klines[i].Time.Format(time.DateOnly) == buyDate {
				return rule.PriceValue(in.Klines[i].Open)
			}
		}
		return rule.InvalidValue()
	})
	c, err := rule.NewCompare(rule.ClosePrice(), rule.Lt, buyOpen)
	if err != nil {
		return false
	}
	and, err := rule.NewAnd(notBuyDay, c)
	if err != nil {
		return false
	}
	return and.Eval(rule.NewInput(code, dks, nil, &buy))
}
