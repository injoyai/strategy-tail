package lab

import (
	"reflect"
	"testing"
	"time"

	"github.com/injoyai/tdx/protocol"
	"github.com/traefik/yaegi/interp"
	"github.com/traefik/yaegi/stdlib"

	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/lib/extend"
	sb "github.com/injoyai/strategy-tail/strategies/buy"
)

// 核心探针：解释器构造的 Buyer 能否被编译侧代码调用（决定 Yaegi 方案是否成立）。
func TestYaegiBinaryBinding(t *testing.T) {
	// 符号表：binary 包包装编译侧类型（Yaegi 官方机制：(*T)(nil) 表示类型符号）
	syms := interp.Exports{
		"github.com/injoyai/strategy-tail/core/core": {
			"Variant": reflect.ValueOf((*core.Variant)(nil)),
		},
		"github.com/injoyai/strategy-tail/strategies/buy/buy": {
			"A阴线收回": reflect.ValueOf((*sb.A阴线收回)(nil)),
			"And":   reflect.ValueOf((*sb.And)(nil)),
			"MAUp":  reflect.ValueOf((*sb.MAUp)(nil)),
		},
	}

	src := `package main

import (
	sb "github.com/injoyai/strategy-tail/strategies/buy"
	"github.com/injoyai/strategy-tail/core"
)

func Strategy() []core.Variant {
	return []core.Variant{
		{Name: "解释器构造·收回MA5", Buyer: sb.And{sb.A阴线收回{SupportPeriod: 5, MinBodyRatio: 0.3, MaxRise: 1.0}}},
	}
}
`
	i := interp.New(interp.Options{GoPath: "D:\\GOPATH"})
	if err := i.Use(stdlib.Symbols); err != nil {
		t.Fatal(err)
	}
	if err := i.Use(syms); err != nil {
		t.Fatal(err)
	}
	if _, err := i.Eval(src); err != nil {
		t.Fatalf("eval 失败: %v", err)
	}
	rv, err := i.Eval("Strategy()")
	if err != nil {
		t.Fatalf("调用失败: %v", err)
	}

	var variants []core.Variant
	switch p := rv.Interface().(type) {
	case []core.Variant:
		variants = p
	case *interface{}:
		switch q := (*p).(type) {
		case []core.Variant:
			variants = q
		default:
			t.Fatalf("嵌套类型: %T", *p)
		}
	default:
		t.Fatalf("返回类型: %T", rv.Interface())
	}
	if len(variants) != 1 || variants[0].Name != "解释器构造·收回MA5" {
		t.Fatalf("variants 异常: %+v", variants)
	}

	// 编译侧调用解释器构造的 Buyer：用与单测一致的标准形态数据对拍
	buyer := variants[0].Buyer
	if buyer.Name() == "" {
		t.Fatal("name 为空")
	}

	// 与原生编译组件对拍（数据构造复用 strategies/buy 测试同款逻辑）
	native := sb.And{sb.A阴线收回{SupportPeriod: 5, MinBodyRatio: 0.3, MaxRise: 1.0}}
	dks := makeProbeKlines()
	want := native.Buy("sh600000", dks)
	got := buyer.Buy("sh600000", dks)
	if got != want {
		t.Fatalf("解释器 Buyer=%v 与原生=%v 不一致", got, want)
	}
	if !got {
		t.Fatal("期望该数据触发买入")
	}
}

// makeProbeKlines 构造上升趋势 + 最后一天阴线收回 MA5 的数据（对拍用）。
func makeProbeKlines() extend.Klines {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.Local)
	n := 20
	ks := make(extend.Klines, 0, n+1)
	for i := 0; i < n; i++ {
		c := 10 + float64(i)*0.2
		ks = append(ks, &extend.Kline{
			Unix: base.AddDate(0, 0, i).Unix(),
			Kline: &protocol.Kline{
				Time:  base.AddDate(0, 0, i),
				Open:  protocol.Yuan(c - 0.1),
				Close: protocol.Yuan(c),
				High:  protocol.Yuan(c + 0.05),
				Low:   protocol.Yuan(c - 0.15),
			},
		})
	}
	// 最后一天：高开低走收阴，盘中跌破 MA5，尾盘收回
	ks = append(ks, &extend.Kline{
		Unix: base.AddDate(0, 0, n).Unix(),
		Kline: &protocol.Kline{
			Time:  base.AddDate(0, 0, n),
			Open:  protocol.Yuan(14.4),
			Close: protocol.Yuan(13.9),
			High:  protocol.Yuan(14.5),
			Low:   protocol.Yuan(13.3),
		},
	})
	return ks
}
