package sell

import (
	"time"

	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/lib/extend"
)

// A买入次日跌破买入价 是买入次日跌破买入价的卖出条件。
// 触发条件：当前交易日是买入日的下一个交易日，且今日收盘价 < 买入价。
// 注：dks 只包含交易日 K 线，按日期比较判定"次日"。
type A买入次日跌破买入价 struct{}

func (s A买入次日跌破买入价) Name() string {
	return "买入次日跌破买入价"
}

func (s A买入次日跌破买入价) Sell(code string, dks extend.Klines, buy core.Buy) bool {
	n := len(dks)
	if n < 2 {
		return false
	}
	today := dks[n-1]
	yesterday := dks[n-2]

	// 昨天必须是买入日
	if yesterday.Time.Format(time.DateOnly) != buy.Time.Format(time.DateOnly) {
		return false
	}

	return today.Close < buy.Price
}
