package script

import (
	"context"
	_ "embed"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/lib/extend"
	sb "github.com/injoyai/strategy-tail/strategies/buy"
	"github.com/injoyai/tdx/protocol"
)

// interp_test.go 验证正式脚本（strategies/script/matrix.go）经 Yaegi 实例化后
// 与原生编译组件对拍一致（设计文档 §8 测试策略）。

//go:embed testdata/strategy.go
var sampleScript string

// TestLoadScript 示例脚本经 Yaegi 加载返回正确数量的变体。
func TestLoadScript(t *testing.T) {
	variants, err := LoadScript(sampleScript)
	if err != nil {
		t.Fatalf("加载失败: %v", err)
	}
	if len(variants) != 2 {
		t.Fatalf("变体数量: %d", len(variants))
	}
	if variants[0].Name != "解释·收回MA5" || variants[1].Name != "解释·收回MA10" {
		t.Fatalf("变体名: %q, %q", variants[0].Name, variants[1].Name)
	}
	for i, v := range variants {
		if v.Buyer == nil {
			t.Fatalf("变体 %d Buyer 为空", i)
		}
		if v.Buyer.Name() == "" {
			t.Fatalf("变体 %d Name 为空", i)
		}
	}
}

// TestLoadScript_BuyerParity 解释器 Buyer 与原生编译 Buyer 对拍
// （makeProbeKlines 数据与 probe_yaegi_binary_test.go 一致，另加一组不触发买入的数据）。
func TestLoadScript_BuyerParity(t *testing.T) {
	variants, err := LoadScript(sampleScript)
	if err != nil {
		t.Fatalf("加载失败: %v", err)
	}

	// 原生编译组件对拍（与脚本等价的 And 组合）
	natives := []core.Buyer{
		sb.And{sb.A阴线收回{SupportPeriod: 5, MinBodyRatio: 0.3, MaxRise: 1.0, Trend: sb.MAUp{Period: 5}}},
		sb.And{sb.A阴线收回{SupportPeriod: 10, MinBodyRatio: 0.3, MaxRise: 1.0, Trend: sb.MAUp{Period: 5}}},
	}
	ks := makeProbeKlines()
	for i, v := range variants {
		want := natives[i].Buy("sh600000", ks)
		got := v.Buyer.Buy("sh600000", ks)
		if got != want {
			t.Fatalf("变体 %q: 解释器=%v 原生=%v", v.Name, got, want)
		}
	}

	// 不触发买入的下降趋势数据
	down := makeDownKlines()
	for i, v := range variants {
		want := natives[i].Buy("sh600000", down)
		got := v.Buyer.Buy("sh600000", down)
		if got != want {
			t.Fatalf("下降趋势 变体 %q: 解释器=%v 原生=%v", v.Name, got, want)
		}
	}
}

// TestCheckScript 干跑校验：合法脚本通过、语法错误报出。
func TestCheckScript(t *testing.T) {
	if err := CheckScript(sampleScript); err != nil {
		t.Fatalf("合法脚本不应报错: %v", err)
	}
	if err := CheckScript("package main\nfunc broken( {}"); err == nil {
		t.Fatal("语法错误未报出")
	}
	// Strategy 返回类型错误也应在校验阶段发现（调用时）
	if err := CheckScript("package main\nfunc Strategy() int { return 1 }"); err != nil {
		// CheckScript 不调用 Strategy()，类型错误在 LoadScript 阶段暴露
		t.Logf("CheckScript 未执行 Strategy()，返回类型错误延后暴露: %v", err)
	}
}

// TestLoadScript_WrongReturnType Strategy() 返回非 []core.Variant 时报错。
func TestLoadScript_WrongReturnType(t *testing.T) {
	_, err := LoadScript("package main\nfunc Strategy() int { return 1 }")
	if err == nil {
		t.Fatal("返回类型错误未报出")
	}
}

// TestLoadScriptSerializesConcurrentLoads 并发加载经信号量串行排队，全部
// 正常完成（验证并发 1 限制不破坏正确性）。
func TestLoadScriptSerializesConcurrentLoads(t *testing.T) {
	src := `package main

import "github.com/injoyai/strategy-tail/core"

func Strategy() []core.Variant {
	return []core.Variant{{Name: "demo"}}
}
`
	var wg sync.WaitGroup
	errs := make(chan error, 4)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := LoadScript(src)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("并发加载失败: %v", err)
		}
	}
}

func TestLoadScriptTimeoutKeepsGate(t *testing.T) {
	for _, body := range []string{"for {}", "time.Sleep(300 * time.Millisecond)"} {
		t.Run(body, func(t *testing.T) {
			gate := make(chan struct{}, 1)
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			src := `package main
import "time"
import "github.com/injoyai/strategy-tail/core"
func Strategy() []core.Variant { _ = time.Second; ` + body + `; return nil }`
			if _, err := loadScript(ctx, src, gate); err == nil || !strings.Contains(err.Error(), "超时") {
				t.Fatalf("expected timeout, got %v", err)
			}
			// 即使宿主调用随后返回，也不能把未提供完成通知的取消当作安全退出。
			time.Sleep(350 * time.Millisecond)
			if len(gate) != 1 {
				t.Fatal("timeout must retain the execution token")
			}
			next, stop := context.WithTimeout(context.Background(), 20*time.Millisecond)
			defer stop()
			if _, err := loadScript(next, sampleScript, gate); err == nil {
				t.Fatal("another script ran after an unconfirmed timeout")
			}
		})
	}
}

// makeDownKlines 构造持续下降趋势数据（不应触发买入，对拍负样本）。
func makeDownKlines() extend.Klines {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.Local)
	n := 21
	ks := make(extend.Klines, 0, n)
	for i := 0; i < n; i++ {
		c := 20 - float64(i)*0.2
		ks = append(ks, &extend.Kline{
			Unix: base.AddDate(0, 0, i).Unix(),
			Kline: &protocol.Kline{
				Time:  base.AddDate(0, 0, i),
				Open:  protocol.Yuan(c + 0.1),
				Close: protocol.Yuan(c),
				High:  protocol.Yuan(c + 0.15),
				Low:   protocol.Yuan(c - 0.05),
			},
		})
	}
	return ks
}
