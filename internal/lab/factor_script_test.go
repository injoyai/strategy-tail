package lab

import (
	"testing"
	"time"

	"github.com/injoyai/strategy-tail/lib/extend"
	"github.com/injoyai/tdx/protocol"
)

// factorScript 探针脚本：值字面量因子 + A因子过滤 行为断言；
// Build 工厂 + A因子TopN 构造（覆盖另两处注册）。
const factorScript = `package main

import (
	"github.com/injoyai/strategy-tail/core"
	f "github.com/injoyai/strategy-tail/strategies/factor"
	sb "github.com/injoyai/strategy-tail/strategies/buy"
)

func Strategy() []core.Variant {
	return []core.Variant{
		{Name: "动量过滤", Buyer: sb.A因子过滤{Factor: f.N日动量{Days: 5}, Min: 0, Max: 1}},
		{Name: "动量TopN", Buyer: sb.A因子TopN{Factor: f.Build("momentum", 5), N: 3, Asc: false}},
	}
}
`

// closeKs 构造 o=h=l=c 的内存日K序列（量 10000）。
func closeKs(base time.Time, closes ...float64) extend.Klines {
	ks := make(extend.Klines, 0, len(closes))
	for i, c := range closes {
		tm := base.AddDate(0, 0, i)
		ks = append(ks, &extend.Kline{
			Unix: tm.Unix(),
			Kline: &protocol.Kline{
				Time: tm, Open: protocol.Yuan(c), Close: protocol.Yuan(c),
				High: protocol.Yuan(c), Low: protocol.Yuan(c), Volume: 10000,
			},
		})
	}
	return ks
}

// TestFactorScript Yaegi 因子探针：注册缺失时 LoadScript 报 undefined。
func TestFactorScript(t *testing.T) {
	variants, err := LoadScript(factorScript)
	if err != nil {
		t.Fatalf("脚本加载失败（符号注册缺失或类型不可用）: %v", err)
	}
	if len(variants) != 2 {
		t.Fatalf("变体数 = %d (期望 2)", len(variants))
	}

	base := time.Date(2025, 1, 1, 0, 0, 0, 0, time.Local)

	// 上行 6 根：动量(5) = 12.5/10 − 1 = 0.25 ∈ [0,1] → 买入
	if !variants[0].Buyer.Buy("sh600001", closeKs(base, 10, 10.5, 11, 11.5, 12, 12.5)) {
		t.Fatal("动量 0.25 应在 [0,1] 区间买入")
	}

	// 3 根：动量(5) 数据不足 NaN → 不买
	if variants[0].Buyer.Buy("sh600001", closeKs(base, 10, 10.5, 11)) {
		t.Fatal("数据不足（NaN）不应买入")
	}

	// Name() 断言：Build 返回 nil 时为 "因子TopN(降序)"，可探测 registry kind 漂移
	if got := variants[1].Buyer.Name(); got != "N日动量(5) TopN(3,降序)" {
		t.Fatalf("TopN Name = %q (期望 %q，若为 %q 则 Build 返回 nil)", got, "N日动量(5) TopN(3,降序)", "因子TopN(降序)")
	}

	// TopN 无快照恒 false（只验证脚本构造的 TopN 可调用、不崩）
	if variants[1].Buyer.Buy("sh600001", closeKs(base, 10, 10.5, 11, 11.5, 12, 12.5)) {
		t.Fatal("无快照 TopN 不应买入")
	}
}
