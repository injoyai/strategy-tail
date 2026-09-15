package sell

import (
	"fmt"
	"time"

	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/lib/extend"
)

// A持仓N天尾盘 买入后满 N 个交易日，在当日尾盘时段卖出。
// Days 表示持仓天数（不含买入日），默认 1（即次日尾盘卖出）。
// Time 表示尾盘起始时间（含），默认 "14:55:00"（本地 5 分钟线最后一根，
// 其收盘价 ≈ 当日收盘价，按该根快照成交即接近收盘卖出）。
//
// 引擎在分钟级循环中逐根覆写 dks 最后一根的时间戳后调用本组件：
// 当日时间未到 Time 时不触发，到 Time 后触发，成交价为该分钟快照收盘。
// 当日无分钟数据时（引擎退化为单根日 K，快照时间 00:00:00），
// 成交价即当日收盘，视为尾盘卖出，同样触发。
type A持仓N天尾盘 struct {
	Days int
	Time string
}

func (s A持仓N天尾盘) Name() string {
	days := s.Days
	if days == 0 {
		days = 1
	}
	t := s.Time
	if t == "" {
		t = "14:55:00"
	}
	return fmt.Sprintf("%d天尾盘(%s)卖出", days, t)
}

func (s A持仓N天尾盘) Sell(code string, dks extend.Klines, buy core.Buy) bool {
	days := s.Days
	if days == 0 {
		days = 1
	}
	sellTime := s.Time
	if sellTime == "" {
		sellTime = "14:55:00"
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
	if holdingDays < days {
		return false
	}

	// 当前快照时间：引擎分钟级覆写 dks 最后一根的时间戳
	tod := dks[len(dks)-1].Time.Format(time.TimeOnly)
	// 无分钟数据退化：快照时间 00:00:00，成交价即当日收盘，视为尾盘卖出
	if tod == "00:00:00" {
		return true
	}
	return tod >= sellTime
}
