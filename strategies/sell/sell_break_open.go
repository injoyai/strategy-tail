package sell

import (
	"time"

	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/lib/extend"
)

// A跌破买入日开盘 是基于买入日开盘价的卖出条件。
// 触发条件：当日收盘价跌破买入当天的开盘价。
// 注：dks 只包含交易日 K 线，按日期匹配定位买入日。
type A跌破买入日开盘 struct{}

func (s A跌破买入日开盘) Name() string {
	return "跌破买入日开盘"
}

func (s A跌破买入日开盘) Sell(code string, dks extend.Klines, buy core.Buy) bool {
	n := len(dks)
	if n == 0 {
		return false
	}
	today := dks[n-1]

	// 当日不卖（买入日不触发）
	buyDate := buy.Time.Format(time.DateOnly)
	if today.Time.Format(time.DateOnly) == buyDate {
		return false
	}

	// 定位买入日 K 线，取其开盘价
	for i := n - 1; i >= 0; i-- {
		if dks[i].Time.Format(time.DateOnly) == buyDate {
			return today.Close < dks[i].Open
		}
	}
	return false
}
