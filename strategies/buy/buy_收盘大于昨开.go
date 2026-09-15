package buy

import (
	"github.com/injoyai/strategy-tail/lib/extend"
)

// A收盘大于昨开 是"当日收盘价大于昨日开盘价"买入条件。
//
// 用途：作为附加过滤条件，要求今天收盘站上昨日开盘价之上，
// 表达"多头回补了昨日跳空/实体区间"的偏多确认。常与其他条件组合：
//
//	buy.And{ buy.MACD连涨{MinDays: 2}, buy.A收盘大于昨开{} }
//
// 触发条件：当日收盘价严格大于昨日开盘价（相等不触发）。
type A收盘大于昨开 struct{}

func (s A收盘大于昨开) Name() string { return "收盘>昨开" }

func (s A收盘大于昨开) Buy(code string, dks extend.Klines) bool {
	if len(dks) < 2 {
		return false
	}
	today := dks[len(dks)-1]
	yesterday := dks[len(dks)-2]
	return today.Close.Float64() > yesterday.Open.Float64()
}
