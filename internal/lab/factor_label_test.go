package lab

import (
	"math"
	"testing"
	"time"

	"github.com/injoyai/strategy-tail/lib/extend"
	"github.com/injoyai/tdx/protocol"
)

// --- Task 2 Step 3：labelReturn 纯函数 ---

// labelKline 构造 Open/Close 各自可控的日 K。
func labelKline(at time.Time, open, close float64) *extend.Kline {
	return &extend.Kline{Kline: &protocol.Kline{
		Time: at, Open: protocol.Yuan(open), High: protocol.Yuan(math.Max(open, close)),
		Low: protocol.Yuan(math.Min(open, close)), Close: protocol.Yuan(close), Volume: 100,
	}}
}

// labelDay 生成测试用连续交易日。
func labelDay(n int) time.Time {
	return time.Date(2024, 12, 2+n, 0, 0, 0, 0, time.Local)
}

// h=1 精确使用次日 Open → 次日 Close。
func TestLabelReturnNextOpenToCloseH1UsesNextDayOpenClose(t *testing.T) {
	dks := extend.Klines{
		labelKline(labelDay(0), 10, 10.5),
		labelKline(labelDay(1), 11, 11.55), // entry=11, exit=11.55
		labelKline(labelDay(2), 12, 12.5),
	}
	ret, reason, ok := labelReturn(dks, nil, 0, 1, labelKindNextOpenToClose)
	if !ok || reason != LabelOK {
		t.Fatalf("expected ok, got ok=%v reason=%q", ok, reason)
	}
	if want := 11.55/11 - 1; math.Abs(ret-want) > 1e-12 {
		t.Fatalf("ret = %v, want %v", ret, want)
	}
}

// 修改 t+2 以后的价格不得影响 h=1 标签。
func TestLabelReturnNextOpenToCloseH1IgnoresLaterBars(t *testing.T) {
	base := extend.Klines{
		labelKline(labelDay(0), 10, 10.5),
		labelKline(labelDay(1), 11, 11.55),
		labelKline(labelDay(2), 12, 12.5),
	}
	want, _, _ := labelReturn(base, nil, 0, 1, labelKindNextOpenToClose)
	base[2] = labelKline(labelDay(2), 99, 99)
	ret, _, ok := labelReturn(base, nil, 0, 1, labelKindNextOpenToClose)
	if !ok || ret != want {
		t.Fatalf("h=1 must ignore later bars: ret=%v want=%v ok=%v", ret, want, ok)
	}
}

// h=5 使用 signalIdx+5 的收盘退出；中间 K 线不参与。
func TestLabelReturnNextOpenToCloseH5UsesHorizonExit(t *testing.T) {
	dks := extend.Klines{
		labelKline(labelDay(0), 10, 10.1),
		labelKline(labelDay(1), 11, 11.1), // entry=11
		labelKline(labelDay(2), 12, 12.1),
		labelKline(labelDay(3), 13, 13.1),
		labelKline(labelDay(4), 14, 14.1),
		labelKline(labelDay(5), 15, 16.5), // exit=16.5
	}
	ret, reason, ok := labelReturn(dks, nil, 0, 5, labelKindNextOpenToClose)
	if !ok || reason != LabelOK {
		t.Fatalf("expected ok, got ok=%v reason=%q", ok, reason)
	}
	if want := 16.5/11 - 1; math.Abs(ret-want) > 1e-12 {
		t.Fatalf("ret = %v, want %v", ret, want)
	}
}

// 年末跨年：entry/exit 越出 Dks 时从 Future 缓冲区取价。
func TestLabelReturnCrossesYearViaFutureBuffer(t *testing.T) {
	dks := extend.Klines{
		labelKline(labelDay(0), 10, 10.1),
		labelKline(labelDay(1), 10.2, 10.3), // 最后一个信号日
	}
	future := extend.Klines{
		labelKline(labelDay(2), 11, 11.1), // entry
		labelKline(labelDay(3), 11.2, 12), // exit
	}
	ret, reason, ok := labelReturn(dks, future, 1, 2, labelKindNextOpenToClose)
	if !ok || reason != LabelOK {
		t.Fatalf("expected cross-year label ok, got ok=%v reason=%q", ok, reason)
	}
	if want := 12.0/11.0 - 1; math.Abs(ret-want) > 1e-12 {
		t.Fatalf("ret = %v, want %v", ret, want)
	}
}

// 整个数据集尾部（Dks+Future）不足 horizon 时记 insufficientHorizon。
func TestLabelReturnInsufficientHorizonAtDatasetTail(t *testing.T) {
	dks := extend.Klines{
		labelKline(labelDay(0), 10, 10.1),
		labelKline(labelDay(1), 10.2, 10.3),
	}
	withTail := extend.Klines{labelKline(labelDay(2), 11, 11.1)}
	cases := []struct {
		name      string
		signalIdx int
		horizon   int
		future    extend.Klines
	}{
		{"exit needs future[1]", 1, 2, withTail},
		{"h=3 beyond tail", 1, 3, withTail},
		{"no next-day entry at all", 1, 1, nil},
	}
	for _, tc := range cases {
		_, reason, ok := labelReturn(dks, tc.future, tc.signalIdx, tc.horizon, labelKindNextOpenToClose)
		if ok || reason != LabelSkipInsufficientHorizon {
			t.Fatalf("%s: got ok=%v reason=%q, want insufficientHorizon", tc.name, ok, reason)
		}
	}
}

// 次日开盘价为 0（停牌/缺数据）记 missingEntryPrice。
func TestLabelReturnMissingEntryPriceOnZeroOpen(t *testing.T) {
	dks := extend.Klines{
		labelKline(labelDay(0), 10, 10.1),
		labelKline(labelDay(1), 0, 10.2),
		labelKline(labelDay(2), 11, 11.1),
	}
	_, reason, ok := labelReturn(dks, nil, 0, 1, labelKindNextOpenToClose)
	if ok || reason != LabelSkipMissingEntryPrice {
		t.Fatalf("got ok=%v reason=%q, want missingEntryPrice", ok, reason)
	}
}

// 退出日收盘价为 0 记 missingExitPrice。
func TestLabelReturnMissingExitPriceOnZeroClose(t *testing.T) {
	dks := extend.Klines{
		labelKline(labelDay(0), 10, 10.1),
		labelKline(labelDay(1), 11, 11.1),
		labelKline(labelDay(2), 12, 0),
	}
	_, reason, ok := labelReturn(dks, nil, 0, 2, labelKindNextOpenToClose)
	if ok || reason != LabelSkipMissingExitPrice {
		t.Fatalf("got ok=%v reason=%q, want missingExitPrice", ok, reason)
	}
}

// legacy 同收盘标签：entry 为信号日 Close，不使用次日 Open。
func TestLabelReturnLegacyCloseToClose(t *testing.T) {
	dks := extend.Klines{
		labelKline(labelDay(0), 10, 10.5), // entry=10.5
		labelKline(labelDay(1), 0, 11),    // Open=0 不得影响 legacy
		labelKline(labelDay(2), 12, 12.6), // exit=12.6
	}
	ret, reason, ok := labelReturn(dks, nil, 0, 2, labelKindSameCloseToCloseLegacy)
	if !ok || reason != LabelOK {
		t.Fatalf("expected ok, got ok=%v reason=%q", ok, reason)
	}
	if want := 12.6/10.5 - 1; math.Abs(ret-want) > 1e-12 {
		t.Fatalf("ret = %v, want %v", ret, want)
	}
	// legacy 信号日 Close 缺失同样记 missingEntryPrice
	dks[0] = labelKline(labelDay(0), 10, 0)
	_, reason, ok = labelReturn(dks, nil, 0, 2, labelKindSameCloseToCloseLegacy)
	if ok || reason != LabelSkipMissingEntryPrice {
		t.Fatalf("legacy zero entry close: got ok=%v reason=%q", ok, reason)
	}
}

// 调用方契约违反（越界 signalIdx、horizon<1、未知 kind）一律拒绝。
func TestLabelReturnRejectsInvalidInputs(t *testing.T) {
	dks := extend.Klines{
		labelKline(labelDay(0), 10, 10.1),
		labelKline(labelDay(1), 11, 11.1),
	}
	cases := []struct {
		name      string
		signalIdx int
		horizon   int
		kind      string
	}{
		{"negative signalIdx", -1, 1, labelKindNextOpenToClose},
		{"signalIdx beyond dks", 2, 1, labelKindNextOpenToClose},
		{"zero horizon", 0, 0, labelKindNextOpenToClose},
		{"negative horizon", 0, -1, labelKindNextOpenToClose},
		{"unknown kind", 0, 1, "something_else"},
	}
	for _, tc := range cases {
		_, reason, ok := labelReturn(dks, nil, tc.signalIdx, tc.horizon, tc.kind)
		if ok {
			t.Fatalf("%s: expected rejection", tc.name)
		}
		if reason != LabelOK {
			t.Fatalf("%s: contract violation must not invent a skip reason, got %q", tc.name, reason)
		}
	}
}

// 覆盖统计对齐：sum(labeled + 各跳过原因) == eligibleSignals。
func TestLabelCoverageRecordAlignsSum(t *testing.T) {
	var cov LabelCoverage
	reasons := []LabelSkipReason{
		LabelOK, LabelOK,
		LabelSkipMissingEntryPrice,
		LabelSkipMissingExitPrice,
		LabelSkipUntradableEntry,
		LabelSkipInsufficientHorizon,
		LabelSkipNonFiniteReturn,
	}
	for _, r := range reasons {
		cov.Record(r)
	}
	if cov.EligibleSignals != len(reasons) {
		t.Fatalf("EligibleSignals = %d, want %d", cov.EligibleSignals, len(reasons))
	}
	skips := cov.MissingEntryPrice + cov.MissingExitPrice + cov.UntradableEntry +
		cov.InsufficientHorizon + cov.NonFiniteReturn
	if cov.LabeledSignals+skips != cov.EligibleSignals {
		t.Fatalf("alignment broken: %+v (skips=%d)", cov, skips)
	}
	if cov.LabeledSignals != 2 || cov.MissingEntryPrice != 1 || cov.MissingExitPrice != 1 ||
		cov.UntradableEntry != 1 || cov.InsufficientHorizon != 1 || cov.NonFiniteReturn != 1 {
		t.Fatalf("unexpected buckets: %+v", cov)
	}
}
