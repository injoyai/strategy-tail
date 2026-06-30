package sell

import (
	"time"

	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/lib/extend"
)

// A买入次日未高开 是买入次日未高开的卖出条件。
// 触发条件：今日是买入日的下一个交易日，且今日开盘价 <= 昨日收盘价（即没有高开）。
// 注：dks 只包含交易日 K 线，按 dks[n-2] 定位"昨天"，避免周末/节假日错位。
type A买入次日未高开 struct{}

func (s A买入次日未高开) Name() string {
	return "买入次日未高开"
}

func (s A买入次日未高开) Sell(code string, dks extend.Klines, buy core.Buy) bool {
	n := len(dks)
	if n < 2 {
		return false
	}
	today := dks[n-1]
	yesterday := dks[n-2]

	// 昨天必须是买入日
	if yesterday.Time.Format(time.DateOnly) == buy.Time.Format(time.DateOnly) {
		return today.Open <= yesterday.Close
	}

	return false
}
