package sell

import (
	"fmt"

	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/lib/extend"
)

// A持仓N天 买入后满 N 个交易日强制卖出。
// Days 表示持仓天数上限（不含买入日），默认 5。
// 触发条件：当前交易日（dks 最后一天）距买入日达到 Days 个交易日。
//
// 按交易日索引计算，不按日历日，避免周末/节假日错位。
// 用于确保短线策略的持仓周期可控，资金不沉淀在横盘股上。
type A持仓N天 struct {
	Days int
}

func (s A持仓N天) Name() string {
	days := s.Days
	if days == 0 {
		days = 5
	}
	return fmt.Sprintf("%d天强制平仓", days)
}

func (s A持仓N天) Sell(code string, dks extend.Klines, buy core.Buy) bool {
	days := s.Days
	if days == 0 {
		days = 5
	}
	if len(dks) == 0 {
		return false
	}

	// 找到买入日在 dks 中的位置，计算已持仓的交易日数
	buyIdx := -1
	for i, k := range dks {
		if !k.Time.Before(buy.Time) {
			buyIdx = i
			break
		}
	}
	if buyIdx < 0 {
		return false
	}

	// 持仓天数 = 当前K线总数 - 买入日索引 - 1（不含买入日当天）
	holdingDays := len(dks) - buyIdx - 1
	return holdingDays >= days
}
