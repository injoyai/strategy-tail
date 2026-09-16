package lab

import (
	"strings"
	"testing"

	"github.com/injoyai/strategy-tail/core"
	sb "github.com/injoyai/strategy-tail/strategies/buy"
)

// TestStrategyPresetCatalog 目录冻结为四个 ID，唯一且顺序稳定。
func TestStrategyPresetCatalog(t *testing.T) {
	want := []string{"pullback_ma5_up", "pullback_ma10_up",
		"pullback_ma5_bull", "pullback_ma5_plain"}
	got := StrategyPresetIDs()
	if len(got) != len(want) {
		t.Fatalf("目录数量 = %d, want %d", len(got), len(want))
	}
	for i, id := range want {
		if got[i] != id {
			t.Fatalf("目录[%d] = %q, want %q", i, got[i], id)
		}
	}
}

// TestBuildPresetBuyer 未知 ID 返回明确错误；已知 ID 每次返回
// 非 nil、名称稳定的新 Buyer。
func TestBuildPresetBuyer(t *testing.T) {
	if _, err := BuildPresetBuyer("nope"); err == nil || !strings.Contains(err.Error(), "nope") {
		t.Fatalf("未知 ID 应报错且含 ID: %v", err)
	}
	for _, id := range StrategyPresetIDs() {
		b1, err := BuildPresetBuyer(id)
		if err != nil || b1 == nil {
			t.Fatalf("%s: Build = (%v, %v)", id, b1, err)
		}
		b2, err := BuildPresetBuyer(id)
		if err != nil {
			t.Fatal(err)
		}
		if b1.Name() == "" || b1.Name() != b2.Name() {
			t.Fatalf("%s: 名称不稳定 %q vs %q", id, b1.Name(), b2.Name())
		}
	}
}

// TestPresetBuildersMatchMatrix 四个 Builder 与 strategies/script/matrix.go
// 的 Buyer 参数逐项一致。matrix.go 带 //go:build ignore 不能 import，
// 按其参数复刻期望 Buyer，通过组件 Name()（参数的函数）比较锁定。
func TestPresetBuildersMatchMatrix(t *testing.T) {
	want := []struct {
		id     string
		expect core.Buyer
	}{
		{"pullback_ma5_up", sb.And{
			sb.A流通市值{Min: 20},
			sb.A价格{Min: 2, Max: 120},
			sb.A过滤涨停{},
			sb.A阴线收回{SupportPeriod: 5, MinBodyRatio: 0.3, MaxRise: 1.0,
				Trend: sb.MAUp{Period: 5}},
		}},
		{"pullback_ma10_up", sb.And{
			sb.A流通市值{Min: 20},
			sb.A价格{Min: 2, Max: 120},
			sb.A过滤涨停{},
			sb.A阴线收回{SupportPeriod: 10, MinBodyRatio: 0.3, MaxRise: 1.0,
				Trend: sb.MAUp{Period: 5}},
		}},
		{"pullback_ma5_bull", sb.And{
			sb.A流通市值{Min: 20},
			sb.A价格{Min: 2, Max: 120},
			sb.A过滤涨停{},
			sb.A阴线收回{SupportPeriod: 5, MinBodyRatio: 0.3, MaxRise: 1.0,
				Trend: sb.A均线多头排列{5, 10, 20}},
		}},
		{"pullback_ma5_plain", sb.And{
			sb.A流通市值{Min: 20},
			sb.A价格{Min: 2, Max: 120},
			sb.A过滤涨停{},
			sb.A阴线收回{SupportPeriod: 5, MinBodyRatio: 0.3, MaxRise: 1.0},
		}},
	}
	for _, w := range want {
		b, err := BuildPresetBuyer(w.id)
		if err != nil {
			t.Fatalf("%s: %v", w.id, err)
		}
		if b.Name() != w.expect.Name() {
			t.Fatalf("%s: Name = %q, want %q（参数与 matrix.go 不一致）", w.id, b.Name(), w.expect.Name())
		}
	}
}

// TestPresetInfosDTO API 展示 DTO 只含人类可读字段，不泄露
// Go 类型名、函数名或源码路径。
func TestPresetInfosDTO(t *testing.T) {
	infos := PresetInfos()
	if len(infos) != 4 {
		t.Fatalf("数量 = %d, want 4", len(infos))
	}
	for _, p := range infos {
		if p.ID == "" || p.Name == "" || p.Description == "" || len(p.Rules) == 0 {
			t.Fatalf("%s: 展示字段不完整 %+v", p.ID, p)
		}
		lower := strings.ToLower(p.Name + p.Description + strings.Join(p.Rules, " "))
		for _, leak := range []string{
			"core.buyer", "sb.and", "a流通市值", "a价格", "a过滤涨停",
			"a阴线收回", "maup", "a均线多头排列", "supportperiod", "minbodyratio",
			"func", "struct", ".go", "package",
		} {
			if strings.Contains(lower, leak) {
				t.Fatalf("%s: DTO 泄露 %q", p.ID, leak)
			}
		}
	}
}
