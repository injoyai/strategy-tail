package lab

import (
	"math"

	"github.com/injoyai/strategy-tail/lib/extend"
	"github.com/injoyai/tdx/protocol"
)

// LabelSkipReason 单次标签未能生成时的归因；LabelOK 表示成功。
type LabelSkipReason string

const (
	LabelOK                      LabelSkipReason = ""
	LabelSkipMissingEntryPrice   LabelSkipReason = "missing_entry_price"
	LabelSkipMissingExitPrice    LabelSkipReason = "missing_exit_price"
	LabelSkipUntradableEntry     LabelSkipReason = "untradable_entry"
	LabelSkipInsufficientHorizon LabelSkipReason = "insufficient_horizon"
	LabelSkipNonFiniteReturn     LabelSkipReason = "non_finite_return"
)

// LabelCoverage 单 Horizon 的标签覆盖统计。
// 不变式：LabeledSignals 与各跳过桶之和等于 EligibleSignals。
type LabelCoverage struct {
	EligibleSignals     int
	LabeledSignals      int
	MissingEntryPrice   int
	MissingExitPrice    int
	UntradableEntry     int
	InsufficientHorizon int
	NonFiniteReturn     int
}

// Record 记一次标签尝试；reason==LabelOK 计入成功，其余计入对应跳过桶。
func (c *LabelCoverage) Record(reason LabelSkipReason) {
	c.EligibleSignals++
	switch reason {
	case LabelOK:
		c.LabeledSignals++
	case LabelSkipMissingEntryPrice:
		c.MissingEntryPrice++
	case LabelSkipMissingExitPrice:
		c.MissingExitPrice++
	case LabelSkipUntradableEntry:
		c.UntradableEntry++
	case LabelSkipInsufficientHorizon:
		c.InsufficientHorizon++
	case LabelSkipNonFiniteReturn:
		c.NonFiniteReturn++
	}
}

// labelReturn 对信号日 signalIdx 计算单一 Horizon 收益标签。
// entry/exit 越出 Dks 时按序从 Future 缓冲区取价（combined 索引 = Dks 长度偏移）。
// ok=false 且 reason==LabelOK 表示调用方契约违反（signalIdx 越界、horizon<1、
// 未知 kind），调用方不得将其计入任何跳过桶。
func labelReturn(dks, future extend.Klines, signalIdx, horizon int, kind string) (float64, LabelSkipReason, bool) {
	if signalIdx < 0 || signalIdx >= len(dks) || horizon < 1 {
		return 0, LabelOK, false
	}
	switch kind {
	case labelKindNextOpenToClose, labelKindSameCloseToCloseLegacy:
	default:
		return 0, LabelOK, false
	}

	total := len(dks) + len(future)
	at := func(idx int) (*extend.Kline, bool) {
		if idx < 0 || idx >= total {
			return nil, false
		}
		if idx < len(dks) {
			return dks[idx], true
		}
		return future[idx-len(dks)], true
	}

	exitK, ok := at(signalIdx + horizon)
	if !ok {
		return 0, LabelSkipInsufficientHorizon, false
	}
	var entry protocol.Price
	switch kind {
	case labelKindNextOpenToClose:
		entryK, ok := at(signalIdx + 1)
		if !ok {
			return 0, LabelSkipInsufficientHorizon, false
		}
		entry = entryK.Open
	case labelKindSameCloseToCloseLegacy:
		entry = dks[signalIdx].Close
	}
	if entry <= 0 {
		return 0, LabelSkipMissingEntryPrice, false
	}
	exit := exitK.Close
	if exit <= 0 {
		return 0, LabelSkipMissingExitPrice, false
	}

	e, x := entry.Float64(), exit.Float64()
	if math.IsNaN(e) || math.IsInf(e, 0) || math.IsNaN(x) || math.IsInf(x, 0) {
		return 0, LabelSkipNonFiniteReturn, false
	}
	ret := x/e - 1
	if math.IsNaN(ret) || math.IsInf(ret, 0) {
		return 0, LabelSkipNonFiniteReturn, false
	}
	return ret, LabelOK, true
}
