package lab

import (
	"encoding/json"
	"math"
	"net/http"
	"reflect"
	"testing"

	"github.com/injoyai/strategy-tail/core"
	f "github.com/injoyai/strategy-tail/strategies/factor"
)

// 新增目录因子的最小接入测试（规范 §6.4）：目录 API 可见且版本正确、
// 研究配置校验通过、搭建器保存后恢复参数与版本、Buyer/Seller/组合变体
// 与 Build 直连求值在逐日前缀上信号一致。
// 数值正确性由 strategies/factor 包的可手算单测覆盖，此处只验证链路。

// TestNewFactorsInResearchAndBuilder 价量因子接入链路验证。
func TestNewFactorsInResearchAndBuilder(t *testing.T) {
	t.Chdir(t.TempDir())
	h := NewServer().Handler()
	var catalog []f.CatalogEntry
	if err := json.Unmarshal(doReq(t, h, http.MethodGet, "/api/factors", nil, http.StatusOK), &catalog); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		kind string
		days int
		max  float64
	}{
		{"downside_vol", 20, 1},
		{"return_skew", 20, 1},
		{"parkinson_vol", 20, 1},
		{"turnover_mean", 20, 3},
		{"turnover_pct", 60, 0.9},
		{"volume_cv", 20, 0.5},
		{"volume_up_ratio", 20, 1.2},
		{"volume_down_ratio", 20, 1.2},
		{"range_ratio", 1, 0.05},
		{"bar_center", 1, 0.5},
	} {
		t.Run(tt.kind, func(t *testing.T) {
			version := 1
			if tt.kind == "return_skew" {
				version = 2
			}
			found := false
			for _, entry := range catalog {
				if entry.Kind == tt.kind {
					found = true
					if entry.ImplementationVersion != version {
						t.Fatalf("factor version = %d, want %d", entry.ImplementationVersion, version)
					}
				}
			}
			if !found {
				t.Fatal("missing catalog factor")
			}
			cfg := AnalyzeConfig{RunConfig: RunConfig{StartYear: 2025, EndYear: 2025, SampleMode: "all"}, Kind: tt.kind, Days: tt.days, Window: 5}
			if err := cfg.Validate(); err != nil {
				t.Fatal(err)
			}
			ft := FactorFilterSpec{Kind: tt.kind, Days: tt.days, FactorVersion: version, Operator: "lte", Max: fptr(tt.max)}
			if tt.kind == "return_skew" {
				old := ft
				old.FactorVersion = 1
				if err := old.Validate(); err == nil {
					t.Fatal("旧版偏度阈值不能静默复用新版实现")
				}
			}
			spec := BuilderSpec{Version: 1, Name: "新增因子接入", Buy: factorNode(ft), SellSignal: factorNode(ft)}
			if err := spec.Validate("run"); err != nil {
				t.Fatal(err)
			}
			store := NewBuilderStore(t.TempDir())
			saved, err := store.Create(spec)
			if err != nil {
				t.Fatal(err)
			}
			loaded, err := store.Get(saved.ID)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(loaded.Spec, spec) {
				t.Fatal("factor spec changed on reload")
			}
			buyer, err := loaded.Spec.Buyer()
			if err != nil {
				t.Fatal(err)
			}
			seller, err := loaded.Spec.Seller()
			if err != nil {
				t.Fatal(err)
			}
			variants, err := (StrategySpec{Version: 1, FactorFilters: []FactorFilterSpec{ft}}).Variants()
			if err != nil {
				t.Fatal(err)
			}
			fct := f.Build(tt.kind, tt.days)
			if fct == nil {
				t.Fatal("Build failed")
			}
			for _, flat := range []bool{false, true} {
				bars := conditionBaselineBars(flat)
				// 基准K线不填换手率字段，为换手率/量能类因子补齐（百分数刻度）
				for i, k := range bars {
					k.Turnover = 2 + 0.1*float64(i%7)
				}
				for end := 0; end <= len(bars); end++ {
					ks := bars[:end]
					v := fct.Value("sh600000", ks)
					want := !math.IsNaN(v) && v <= tt.max
					if buyer.Buy("sh600000", ks) != want {
						t.Fatalf("prefix %d: buyer 与直连求值不一致 (factor %v)", end, v)
					}
					if seller.Sell("sh600000", ks, core.Buy{}) != want {
						t.Fatalf("prefix %d: seller 与直连求值不一致 (factor %v)", end, v)
					}
					if variants[1].Buyer.Buy("sh600000", ks) != want {
						t.Fatalf("prefix %d: 组合变体与直连求值不一致 (factor %v)", end, v)
					}
				}
			}
		})
	}
}

// TestDividendYieldContextFactorIntegration 上下文因子（股息率）接入验证：
// 经 BuildWithData 绑定默认空数据视图，缺数据 fail-closed，不产生信号。
func TestDividendYieldContextFactorIntegration(t *testing.T) {
	t.Chdir(t.TempDir())
	h := NewServer().Handler()
	var catalog []f.CatalogEntry
	if err := json.Unmarshal(doReq(t, h, http.MethodGet, "/api/factors", nil, http.StatusOK), &catalog); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, entry := range catalog {
		if entry.Kind == "dividend_yield" {
			found = true
			if entry.ImplementationVersion != 1 || entry.Unit != "percent" {
				t.Fatalf("dividend_yield 目录元数据异常: %+v", entry)
			}
		}
	}
	if !found {
		t.Fatal("missing catalog factor")
	}
	cfg := AnalyzeConfig{RunConfig: RunConfig{StartYear: 2025, EndYear: 2025, SampleMode: "all"}, Kind: "dividend_yield", Days: 1, Window: 5}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	ft := FactorFilterSpec{Kind: "dividend_yield", Days: 1, FactorVersion: 1, Operator: "lte", Max: fptr(5)}
	spec := BuilderSpec{Version: 1, Name: "股息率接入", Buy: factorNode(ft), SellSignal: factorNode(ft)}
	if err := spec.Validate("run"); err != nil {
		t.Fatal(err)
	}
	buyer, err := spec.Buyer()
	if err != nil {
		t.Fatal(err)
	}
	seller, err := spec.Seller()
	if err != nil {
		t.Fatal(err)
	}
	bars := conditionBaselineBars(false)
	for end := 0; end <= len(bars); end++ {
		ks := bars[:end]
		if buyer.Buy("sh600000", ks) {
			t.Fatalf("prefix %d: 空数据视图下不应产生买入信号", end)
		}
		if seller.Sell("sh600000", ks, core.Buy{}) {
			t.Fatalf("prefix %d: 空数据视图下不应产生卖出信号", end)
		}
	}
}
