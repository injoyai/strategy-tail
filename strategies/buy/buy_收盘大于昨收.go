package buy

import (
	"github.com/injoyai/strategy-tail/lib/extend"
)

// A收盘大于昨收 是"当日收盘价大于昨日收盘价"买入条件。
//
// 用途：作为附加过滤条件，要求今天收盘仍高于昨天收盘，
// 表达"阴线回调没有吞掉昨日阳线成果"的强势整理确认。常与其他条件组合：
//
//	buy.And{ buy.MACD连涨{MinDays: 2}, buy.A收盘大于昨收{} }
//
// 触发条件：当日收盘价严格大于昨日收盘价（相等不触发）。
type A收盘大于昨收 struct{}

func (s A收盘大于昨收) Name() string { return "收盘>昨收" }

func (s A收盘大于昨收) Buy(code string, dks extend.Klines) bool {
	if len(dks) < 2 {
		return false
	}
	today := dks[len(dks)-1]
	yesterday := dks[len(dks)-2]
	return today.Close.Float64() > yesterday.Close.Float64()
}
