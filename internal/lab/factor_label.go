package lab

import (
	"math"
	"slices"
	"time"

	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/lib/extend"
)

// LabelSkipReason 单次标签未能生成时的归因；LabelOK 表示成功。
type LabelSkipReason string

const (
	LabelOK                       LabelSkipReason = ""
	LabelSkipMissingEntryPrice    LabelSkipReason = "missing_entry_price"
	LabelSkipMissingExitPrice     LabelSkipReason = "missing_exit_price"
	LabelSkipUntradableEntry      LabelSkipReason = "untradable_entry"
	LabelSkipInsufficientHorizon  LabelSkipReason = "insufficient_horizon"
	LabelSkipNonFiniteReturn      LabelSkipReason = "non_finite_return"
	LabelSkipMissingSignalSession LabelSkipReason = "missing_signal_session"
	LabelSkipMissingEntryBar      LabelSkipReason = "missing_entry_bar"
	LabelSkipMissingExitBar       LabelSkipReason = "missing_exit_bar"
	LabelSkipUntradableExit       LabelSkipReason = "untradable_exit"
	LabelSkipCorporateEvent       LabelSkipReason = "corporate_event"
)

// LabelCoverage 单 Horizon 的标签覆盖统计。
// 不变式：LabeledSignals 与各跳过桶之和等于 EligibleSignals。
type LabelCoverage struct {
	EligibleSignals      int `json:"eligibleSignals"`
	LabeledSignals       int `json:"labeledSignals"`
	MissingEntryPrice    int `json:"missingEntryPrice"`
	MissingExitPrice     int `json:"missingExitPrice"`
	UntradableEntry      int `json:"untradableEntry"`
	InsufficientHorizon  int `json:"insufficientHorizon"`
	NonFiniteReturn      int `json:"nonFiniteReturn"`
	MissingSignalSession int `json:"missingSignalSession,omitempty"`
	MissingEntryBar      int `json:"missingEntryBar,omitempty"`
	MissingExitBar       int `json:"missingExitBar,omitempty"`
	UntradableExit       int `json:"untradableExit,omitempty"`
	CorporateEvent       int `json:"corporateEvent,omitempty"`
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
	case LabelSkipMissingSignalSession:
		c.MissingSignalSession++
	case LabelSkipMissingEntryBar:
		c.MissingEntryBar++
	case LabelSkipMissingExitBar:
		c.MissingExitBar++
	case LabelSkipUntradableExit:
		c.UntradableExit++
	case LabelSkipCorporateEvent:
		c.CorporateEvent++
	}
}

// merge 累加另一份覆盖统计（多票多年并行加载后合并），保持桶和不变式。
func (c *LabelCoverage) merge(o LabelCoverage) {
	c.EligibleSignals += o.EligibleSignals
	c.LabeledSignals += o.LabeledSignals
	c.MissingEntryPrice += o.MissingEntryPrice
	c.MissingExitPrice += o.MissingExitPrice
	c.UntradableEntry += o.UntradableEntry
	c.InsufficientHorizon += o.InsufficientHorizon
	c.NonFiniteReturn += o.NonFiniteReturn
	c.MissingSignalSession += o.MissingSignalSession
	c.MissingEntryBar += o.MissingEntryBar
	c.MissingExitBar += o.MissingExitBar
	c.UntradableExit += o.UntradableExit
	c.CorporateEvent += o.CorporateEvent
}

// factorSessionCalendar 对齐所有股票的标签日期。用上证指数日线提供共同的沪深市场日序。
type factorSessionCalendar struct {
	days  []time.Time
	index map[time.Time]int
}

const factorCalendarCode = "sh000001"

func newFactorSessionCalendar(bars extend.Klines) factorSessionCalendar {
	c := factorSessionCalendar{
		days:  make([]time.Time, 0, len(bars)),
		index: make(map[time.Time]int, len(bars)),
	}
	for _, bar := range bars {
		day := core.DayOf(bar.Time)
		if _, exists := c.index[day]; !exists {
			c.index[day] = len(c.days)
			c.days = append(c.days, day)
		}
	}
	return c
}

func (c factorSessionCalendar) labelWithEvents(day time.Time, bars map[time.Time]*extend.Kline, held int, events []string) (float64, LabelSkipReason, bool) {
	idx, found := c.index[day]
	if !found {
		return 0, LabelSkipMissingSignalSession, false
	}
	if idx+1+held >= len(c.days) {
		return 0, LabelSkipInsufficientHorizon, false
	}
	entry := bars[c.days[idx+1]]
	if entry == nil {
		return 0, LabelSkipMissingEntryBar, false
	}
	if entry.Volume <= 0 {
		return 0, LabelSkipUntradableEntry, false
	}
	exit := bars[c.days[idx+1+held]]
	if exit == nil {
		return 0, LabelSkipMissingExitBar, false
	}
	if exit.Volume <= 0 {
		return 0, LabelSkipUntradableExit, false
	}
	if len(events) > 0 {
		// Entry at the ex-date open uses the post-event price. Later ex-dates
		// would change the economics of a holding described by prices alone.
		i, _ := slices.BinarySearch(events, c.days[idx+1].AddDate(0, 0, 1).Format(time.DateOnly))
		if i < len(events) && events[i] <= c.days[idx+1+held].Format(time.DateOnly) {
			return 0, LabelSkipCorporateEvent, false
		}
	}
	return returnFromPrices(entry.Open, exit.Close)
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
	var entry float64
	entryVolume := int64(0)
	switch kind {
	case labelKindNextOpenToClose:
		entryK, ok := at(signalIdx + 1)
		if !ok {
			return 0, LabelSkipInsufficientHorizon, false
		}
		entry = entryK.Open
		entryVolume = entryK.Volume
	case labelKindSameCloseToCloseLegacy:
		entry = dks[signalIdx].Close
		entryVolume = dks[signalIdx].Volume
	}
	// 停牌日（无成交）不可成交；停牌日 OHLC 常全为 0，须先于价格缺失归因，
	// 否则该桶永远为空，LabelCoverage 无法披露 7.3 节要求的不可交易日。
	if entryVolume <= 0 {
		return 0, LabelSkipUntradableEntry, false
	}
	return returnFromPrices(entry, exitK.Close)
}

func returnFromPrices(entry, exit float64) (float64, LabelSkipReason, bool) {
	if entry <= 0.0 {
		return 0, LabelSkipMissingEntryPrice, false
	}
	if exit <= 0.0 {
		return 0, LabelSkipMissingExitPrice, false
	}

	e, x := entry, exit
	if math.IsNaN(e) || math.IsInf(e, 0) || math.IsNaN(x) || math.IsInf(x, 0) {
		return 0, LabelSkipNonFiniteReturn, false
	}
	ret := x/e - 1
	if math.IsNaN(ret) || math.IsInf(ret, 0) {
		return 0, LabelSkipNonFiniteReturn, false
	}
	return ret, LabelOK, true
}
