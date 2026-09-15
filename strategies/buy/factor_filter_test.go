package buy

import (
	"math"
	"testing"
	"time"

	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/lib/extend"
	"github.com/injoyai/tdx/protocol"
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
		Kline: &protocol.Kline{Time: tm, Open: protocol.Yuan(c), Close: protocol.Yuan(c),
			High: protocol.Yuan(c), Low: protocol.Yuan(c), Volume: 10000},
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
