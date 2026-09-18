package lab

import (
	"math"
	"testing"
	"time"

	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/internal/researchrun"
	"github.com/injoyai/strategy-tail/lib/extend"
	"github.com/injoyai/strategy-tail/researchdata"
	"github.com/injoyai/tdx/protocol"
)

// --- Task 2 Step 2/5：观察层构建与无前视合同 ---

// stubFactor 返回固定值的因子桩。
type stubFactor struct{ value float64 }

func (f stubFactor) Name() string                       { return "stub" }
func (f stubFactor) ValueAt(core.FactorContext) float64 { return f.value }

// countingFactor 计数桩：记录每次 ValueAt 收到的 Klines 长度与末根时间。
type countingFactor struct {
	value    float64
	calls    int
	lengths  []int
	lastTmes []time.Time
}

func (f *countingFactor) Name() string { return "counting" }
func (f *countingFactor) ValueAt(ctx core.FactorContext) float64 {
	f.calls++
	f.lengths = append(f.lengths, len(ctx.Klines))
	if n := len(ctx.Klines); n > 0 {
		f.lastTmes = append(f.lastTmes, ctx.Klines[n-1].Time)
	}
	return f.value
}

// flakeyFactor 第 skip+1 次调用返回 NaN。
type flakeyFactor struct {
	skip int
	call int
}

func (f *flakeyFactor) Name() string { return "flakey" }
func (f *flakeyFactor) ValueAt(core.FactorContext) float64 {
	f.call++
	if f.call == f.skip+1 {
		return math.NaN()
	}
	return 1
}

func obsKline(at time.Time, price float64) *extend.Kline {
	return &extend.Kline{Kline: &protocol.Kline{
		Time: at, Open: protocol.Yuan(price), High: protocol.Yuan(price),
		Low: protocol.Yuan(price), Close: protocol.Yuan(price), Volume: 100,
	}}
}

func obsYearData() researchrun.YearData {
	return researchrun.YearData{
		His: extend.Klines{
			obsKline(time.Date(2024, 12, 30, 0, 0, 0, 0, time.Local), 9),
			obsKline(time.Date(2024, 12, 31, 0, 0, 0, 0, time.Local), 9.5),
		},
		Dks: extend.Klines{
			obsKline(time.Date(2025, 1, 1, 0, 0, 0, 0, time.Local), 10),
			obsKline(time.Date(2025, 1, 2, 0, 0, 0, 0, time.Local), 10.5),
			obsKline(time.Date(2025, 1, 3, 0, 0, 0, 0, time.Local), 11),
		},
		Future: extend.Klines{
			obsKline(time.Date(2025, 1, 6, 0, 0, 0, 0, time.Local), 11.5),
			obsKline(time.Date(2025, 1, 7, 0, 0, 0, 0, time.Local), 12),
			obsKline(time.Date(2025, 1, 8, 0, 0, 0, 0, time.Local), 12.5),
		},
	}
}

// 因子每天只调用一次，且只看到信号日以前（含当日）的数据。
func TestBuildObservationsFactorSeesOnlyPast(t *testing.T) {
	data := obsYearData()
	fct := &countingFactor{value: 1}
	res := buildObservations("sh600000", data, fct, []int{1}, labelKindNextOpenToClose, nil)
	if fct.calls != len(data.Dks) {
		t.Fatalf("factor calls = %d, want %d", fct.calls, len(data.Dks))
	}
	base := len(data.His)
	for i, want := range []time.Time{
		time.Date(2025, 1, 1, 0, 0, 0, 0, time.Local),
		time.Date(2025, 1, 2, 0, 0, 0, 0, time.Local),
		time.Date(2025, 1, 3, 0, 0, 0, 0, time.Local),
	} {
		if fct.lengths[i] != base+i+1 {
			t.Fatalf("call %d saw %d klines, want %d", i, fct.lengths[i], base+i+1)
		}
		if !fct.lastTmes[i].Equal(want) {
			t.Fatalf("call %d last kline = %v, want %v", i, fct.lastTmes[i], want)
		}
	}
	if len(res.Observations) != len(data.Dks) {
		t.Fatalf("observations = %d, want %d", len(res.Observations), len(data.Dks))
	}
	for i, obs := range res.Observations {
		if obs.KlineIndex != i || obs.Code != "sh600000" || !obs.Date.Equal(data.Dks[i].Time) {
			t.Fatalf("observation %d = %+v", i, obs)
		}
	}
}

// 修改 Future 缓冲区不得影响任何 factor(t)。
func TestBuildObservationsIgnoresFuture(t *testing.T) {
	data := obsYearData()
	a := buildObservations("sh600000", data, stubFactor{value: 7}, []int{1}, labelKindNextOpenToClose, nil)
	data.Future = extend.Klines{
		obsKline(time.Date(2025, 1, 6, 0, 0, 0, 0, time.Local), 88),
		obsKline(time.Date(2025, 1, 7, 0, 0, 0, 0, time.Local), 99),
	}
	b := buildObservations("sh600000", data, stubFactor{value: 7}, []int{1}, labelKindNextOpenToClose, nil)
	if len(a.Observations) != len(b.Observations) {
		t.Fatalf("observation count changed: %d vs %d", len(a.Observations), len(b.Observations))
	}
	for i := range a.Observations {
		if a.Observations[i].Value != b.Observations[i].Value || !a.Observations[i].Date.Equal(b.Observations[i].Date) {
			t.Fatalf("observation %d changed with Future: %+v vs %+v", i, a.Observations[i], b.Observations[i])
		}
	}
}

// 多 Horizon 共用同一次因子调用；各 Horizon 独立投影标签。
func TestBuildObservationsSingleFactorCallAcrossHorizons(t *testing.T) {
	data := obsYearData()
	fct := &countingFactor{value: 1}
	res := buildObservations("sh600000", data, fct, []int{1, 2, 5}, labelKindNextOpenToClose, nil)
	if fct.calls != len(data.Dks) {
		t.Fatalf("factor calls = %d, want one per day regardless of horizons", fct.calls)
	}
	for _, h := range []int{1, 2} {
		if len(res.Labeled[h]) != len(data.Dks) {
			t.Fatalf("h=%d labeled = %d, want %d", h, len(res.Labeled[h]), len(data.Dks))
		}
	}
	// h=5：只有第 1 天信号可完成标签（exit 需要 index 5 = Future[2]，i>=1 越界）
	if len(res.Labeled[5]) != 1 {
		t.Fatalf("h=5 labeled = %d, want 1", len(res.Labeled[5]))
	}
	if res.Coverage[5].InsufficientHorizon != 2 {
		t.Fatalf("h=5 coverage = %+v, want 2 insufficientHorizon", res.Coverage[5])
	}
}

// 年末跨年：12 月信号通过 Future 缓冲区完成标签。
func TestBuildObservationsDecemberCrossYearLabels(t *testing.T) {
	data := researchrun.YearData{
		Dks: extend.Klines{
			obsKline(time.Date(2024, 12, 30, 0, 0, 0, 0, time.Local), 10),
			obsKline(time.Date(2024, 12, 31, 0, 0, 0, 0, time.Local), 10.2),
		},
		Future: extend.Klines{
			obsKline(time.Date(2025, 1, 2, 0, 0, 0, 0, time.Local), 11),   // entry
			obsKline(time.Date(2025, 1, 3, 0, 0, 0, 0, time.Local), 12.1), // h=2 exit
		},
	}
	res := buildObservations("sh600000", data, stubFactor{value: 1}, []int{2, 5}, labelKindNextOpenToClose, nil)
	if len(res.Labeled[2]) != 2 {
		t.Fatalf("h=2 labeled = %d, want 2 (both December signals)", len(res.Labeled[2]))
	}
	last := res.Labeled[2][1]
	if want := 12.1/11 - 1; math.Abs(last.Return-want) > 1e-12 {
		t.Fatalf("cross-year return = %v, want %v", last.Return, want)
	}
	if last.Horizon != 2 || !last.Date.Equal(time.Date(2024, 12, 31, 0, 0, 0, 0, time.Local)) {
		t.Fatalf("unexpected labeled observation: %+v", last)
	}
	if res.Coverage[2].LabeledSignals != 2 || res.Coverage[2].EligibleSignals != 2 {
		t.Fatalf("h=2 coverage = %+v", res.Coverage[2])
	}
	if res.Coverage[5].InsufficientHorizon != 2 || res.Coverage[5].LabeledSignals != 0 {
		t.Fatalf("h=5 coverage = %+v", res.Coverage[5])
	}
}

// 停牌（开盘价缺失）计入 missingEntryPrice，且 sum(labeled+skips)==eligible。
func TestBuildObservationsCoverageAlignmentWithHalt(t *testing.T) {
	data := obsYearData()
	data.Dks[1] = obsKline(time.Date(2025, 1, 2, 0, 0, 0, 0, time.Local), 0)
	res := buildObservations("sh600000", data, stubFactor{value: 1}, []int{1}, labelKindNextOpenToClose, nil)
	cov := res.Coverage[1]
	if cov.EligibleSignals != 3 {
		t.Fatalf("EligibleSignals = %d, want 3", cov.EligibleSignals)
	}
	if cov.MissingEntryPrice != 1 || cov.LabeledSignals != 2 {
		t.Fatalf("coverage = %+v", cov)
	}
	if cov.LabeledSignals+cov.MissingEntryPrice != cov.EligibleSignals {
		t.Fatalf("alignment broken: %+v", cov)
	}
	if len(res.Labeled[1]) != 2 {
		t.Fatalf("labeled = %d, want 2", len(res.Labeled[1]))
	}
}

// 因子值非有限（NaN/Inf）的交易日不产生观察，也不计入标签覆盖。
func TestBuildObservationsSkipsNonFiniteFactorValue(t *testing.T) {
	data := obsYearData()
	res := buildObservations("sh600000", data, &flakeyFactor{skip: 1}, []int{1}, labelKindNextOpenToClose, nil)
	if len(res.Observations) != len(data.Dks)-1 {
		t.Fatalf("observations = %d, want %d (NaN day skipped)", len(res.Observations), len(data.Dks)-1)
	}
	if res.Coverage[1].EligibleSignals != len(data.Dks)-1 {
		t.Fatalf("EligibleSignals = %d, want %d", res.Coverage[1].EligibleSignals, len(data.Dks)-1)
	}
}

var _ researchdata.View = nil // 保证 import 与接口一致（View 为接口）
