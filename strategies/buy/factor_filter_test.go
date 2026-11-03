package buy

import (
	"math"
	"testing"
	"time"

	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/lib/extend"
	f "github.com/injoyai/strategy-tail/strategies/factor"
	"github.com/injoyai/strategy-tail/strategies/rule"
	"github.com/injoyai/tdx/v2/protocol"
)

// stubFactor 恒定返回预设值的因子桩。
type stubFactor struct {
	name string
	val  float64
}

func (f *stubFactor) Name() string                                 { return f.name }
func (f *stubFactor) Value(code string, dks extend.Klines) float64 { return f.val }

func mkKline(base time.Time, i int, c float64) *extend.Kline {
	tm := base.AddDate(0, 0, i)
	return &extend.Kline{
		Unix: tm.Unix(),
		Kline: &protocol.Kline{Time: tm, Open: c, Close: c,
			High: c, Low: c, Volume: 10000},
	}
}

func TestA因子过滤(t *testing.T) {
	base := time.Date(2025, 1, 1, 0, 0, 0, 0, time.Local)
	ks := extend.Klines{mkKline(base, 0, 10)}

	// 值 0.5 落在 [0, 1] → true
	b := A因子过滤{Factor: &stubFactor{name: "f", val: 0.5}, Min: 0, Max: 1}
	if !b.Buy("sh600000", ks) {
		t.Fatal("0.5∈[0,1] 应买入")
	}

	// 边界含端点：值 0 与 1 均命中
	if !(A因子过滤{Factor: &stubFactor{name: "f"}, Min: 0, Max: 1}).Buy("sh600000", ks) {
		t.Fatal("stubFactor 默认 val=0 应命中下界")
	}
	if !(A因子过滤{Factor: &stubFactor{name: "f", val: 1}, Min: 0, Max: 1}).Buy("sh600000", ks) {
		t.Fatal("1 应命中上界")
	}

	// 区间外 → false
	if (A因子过滤{Factor: &stubFactor{name: "f", val: 1.5}, Min: 0, Max: 1}).Buy("sh600000", ks) {
		t.Fatal("1.5∉[0,1] 不应买入")
	}

	// NaN → false
	nan := A因子过滤{Factor: &stubFactor{name: "f", val: math.NaN()}, Min: -1, Max: 1}
	if nan.Buy("sh600000", ks) {
		t.Fatal("NaN 不应买入")
	}

	// Factor=nil → false
	if (A因子过滤{Min: -1e9, Max: 1e9}).Buy("sh600000", ks) {
		t.Fatal("Factor 为 nil 不应买入")
	}

	// 空数据 → false
	if (A因子过滤{Factor: &stubFactor{name: "f", val: 0.5}, Min: 0, Max: 1}).Buy("sh600000", nil) {
		t.Fatal("空数据不应买入")
	}

	// Min > Max（配反区间）→ 恒 false
	if (A因子过滤{Factor: &stubFactor{name: "f", val: 0.5}, Min: 1, Max: 0}).Buy("sh600000", ks) {
		t.Fatal("配反区间不应买入")
	}

	// Name
	if g := b.Name(); g != "f∈[0,1]" {
		t.Fatalf("Name = %s", g)
	}
	if g := (A因子过滤{Factor: &stubFactor{name: "N日动量(5)", val: 0}, Min: -1e9, Max: 0.05}).Name(); g != "N日动量(5)∈[-1e+09,0.05]" {
		t.Fatalf("单边 Name = %s", g)
	}
	if g := (A因子过滤{}).Name(); g != "因子过滤" {
		t.Fatalf("nil Name = %s", g)
	}
}

// 编译期确认实现 Buyer。
var _ core.Buyer = A因子过滤{}

func TestFactorFilterNoAllocations(t *testing.T) {
	ks := extend.Klines{mkKline(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), 0, 10)}
	for _, bounds := range [][2]float64{{0, 1}, {math.Inf(-1), math.Inf(1)}} {
		b := A因子过滤{Factor: &stubFactor{val: 0.5}, Min: bounds[0], Max: bounds[1]}
		if !b.Buy("sh600000", ks) {
			t.Fatalf("夹具应触发: %v", bounds)
		}
		if allocs := testing.AllocsPerRun(1000, func() { b.Buy("sh600000", ks) }); allocs != 0 {
			t.Fatalf("纯因子区间判断不应分配规则对象或 MACD 缓存，range=%v allocs=%g", bounds, allocs)
		}
	}
}

var factorFilterBenchmarkResult bool

func BenchmarkFactorFilter(b *testing.B) {
	ks := make(extend.Klines, 50)
	for i := range ks {
		ks[i] = mkKline(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), i, 10+float64(i)/10)
	}
	filter := A因子过滤{Factor: f.N日动量{Days: 5}, Min: 0, Max: 1e9}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		factorFilterBenchmarkResult = filter.Buy("sh600000", ks)
	}
}

func TestFactorRangeLegacyBoundaries(t *testing.T) {
	ks := extend.Klines{mkKline(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), 0, 10)}
	for _, bounds := range [][2]float64{
		{0, 0}, {-1e9, 1e9}, {1, 0}, {math.NaN(), 1}, {0, math.NaN()},
		{math.Inf(-1), 0}, {0, math.Inf(1)}, {math.Inf(-1), math.Inf(1)},
		{math.Inf(1), math.Inf(1)}, {math.Inf(-1), math.Inf(-1)},
	} {
		for _, value := range []float64{-1e9 - 1, -1e9, -1, 0, 1, 1e9, 1e9 + 1, math.NaN(), math.Inf(-1), math.Inf(1)} {
			b := A因子过滤{Factor: &stubFactor{val: value}, Min: bounds[0], Max: bounds[1]}
			want := bounds[0] <= value && value <= bounds[1]
			if got := b.Buy("sh600000", ks); got != want {
				t.Fatalf("value=%g range=%v: got %v want %v", value, bounds, got, want)
			}
			compiled, err := rule.NewFactorRange(b.Factor, b.Min, b.Max)
			if err != nil {
				if want {
					t.Fatalf("合法区间构造失败: value=%g range=%v: %v", value, bounds, err)
				}
			} else if compiled.Eval(rule.Input{Code: "sh600000", Klines: ks}) != want {
				t.Fatalf("编译规则与直接求值不一致: value=%g range=%v", value, bounds)
			}
			if b.Buy("sh600000", nil) {
				t.Fatalf("空行情不可触发: value=%g range=%v", value, bounds)
			}
		}
	}
}
