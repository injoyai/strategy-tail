package factor

import (
	"strconv"
	"strings"
	"testing"
)

// wantKinds 目录的固定顺序（顺序是 API 合同，不得漂移）。
var wantKinds = [...]string{
	"momentum", "skip_month_momentum", "short_reversal", "ma_bias", "slope", "volatility", "amplitude",
	"macd_hist", "macd_delta", "macd_trough_position", "macd_negative_streak", "macd_rising_streak", "ma_min_slope",
	"volume_ratio", "volume_pct", "volume_surge", "amihud_illiquidity", "log_float_cap",
	"body", "upper_shadow", "lower_shadow",
	"position", "high_distance", "close_pct", "kvalue", "vp_corr",
	"pe_ttm", "pe_static", "pb_mrq", "ps_ttm", "pcf_ocf_ttm", "peg",
}

func TestAll(t *testing.T) {
	all := All()
	if len(all) != len(wantKinds) {
		t.Fatalf("目录应有 %d 项, got %d", len(wantKinds), len(all))
	}
	if all[0].Kind != "momentum" || all[0].Name != "N日动量(20)" {
		t.Fatalf("首项异常: %+v", all[0])
	}
	// JSON 字段非空（供 /api/factors 直出）
	for _, e := range all {
		if e.Kind == "" || e.Name == "" || e.Description == "" {
			t.Fatalf("目录项字段缺失: %+v", e)
		}
	}
	// kind 唯一，且上下文构造的名字与目录名一致。
	seen := map[string]bool{}
	for _, e := range all {
		if seen[e.Kind] {
			t.Fatalf("kind 重复: %s", e.Kind)
		}
		seen[e.Kind] = true
		if f := BuildContext(e.Kind, 0); f == nil || f.Name() != e.Name {
			t.Fatalf("%s: BuildContext 默认名 %v 与目录名 %q 不一致", e.Kind, f, e.Name)
		}
	}
	// 顺序稳定
	for i, e := range all {
		if e.Kind != wantKinds[i] {
			t.Fatalf("第 %d 项 kind = %s, want %s", i, e.Kind, wantKinds[i])
		}
	}
}

// TestRegistryMetadata 人类可读元数据合同：
// category/parameterLabel/defaultDays/unit/example 全部就位，
// 且 defaultDays 与 Build(kind, 0) 实际使用的默认窗口一致。
func TestRegistryMetadata(t *testing.T) {
	wantCategory := map[string]bool{
		"趋势与动量": true, "波动": true, "量能": true,
		"规模与流动性": true, "K线形态": true, "位置": true, "相关性": true, "估值": true,
	}
	wantUnit := map[string]bool{
		"ratio": true, "multiple": true, "score": true, "correlation": true,
	}
	for _, e := range All() {
		if !wantCategory[e.Category] {
			t.Fatalf("%s: category %q 不在约定分组内", e.Kind, e.Category)
		}
		if e.ParameterLabel == "" {
			t.Fatalf("%s: parameterLabel 为空", e.Kind)
		}
		if e.DefaultDays <= 0 {
			t.Fatalf("%s: defaultDays = %d, 应为正", e.Kind, e.DefaultDays)
		}
		if !wantUnit[e.Unit] {
			t.Fatalf("%s: unit %q 非法（ratio|multiple|score|correlation）", e.Kind, e.Unit)
		}
		if e.Example == "" || !strings.ContainsAny(e.Example, "0123456789") {
			t.Fatalf("%s: example 未解释原始值: %q", e.Kind, e.Example)
		}
		// 目录不提供阈值推荐、评级或经验收益
		for _, s := range []string{e.Example, e.Description, e.ParameterLabel} {
			if strings.Contains(s, "推荐") || strings.Contains(s, "建议") || strings.Contains(s, "评级") {
				t.Fatalf("%s: 目录出现推荐性措辞: %q", e.Kind, s)
			}
		}
		// defaultDays 与 Build(kind, 0) 一致：窗口因子名以 (N) 结尾，N 应等于 defaultDays
		f := BuildContext(e.Kind, 0)
		if f == nil {
			t.Fatalf("%s: BuildContext 失败", e.Kind)
		}
		if name := f.Name(); strings.HasSuffix(name, ")") {
			if i := strings.LastIndex(name, "("); i >= 0 {
				n, err := strconv.Atoi(name[i+1 : len(name)-1])
				if err != nil || n != e.DefaultDays {
					t.Fatalf("%s: 默认名 %q 与 defaultDays %d 不一致", e.Kind, name, e.DefaultDays)
				}
			}
		}
		// 显式传入 defaultDays 应与默认构造同名（单根K线因子 days 被忽略，亦成立）
		if g := BuildContext(e.Kind, e.DefaultDays); g == nil || g.Name() != f.Name() {
			t.Fatalf("%s: BuildContext(kind, defaultDays) 与默认构造名称不一致", e.Kind)
		}
	}
}

// TestRegistryImplementationVersions 实现版本合同：所有版本必须 > 0（禁止
// 依赖零值）、All() 与 Catalog() 返回同一版本、kind 唯一、顺序合同不变。
func TestRegistryImplementationVersions(t *testing.T) {
	all := All()
	seen := map[string]bool{}
	for _, e := range all {
		if e.ImplementationVersion <= 0 {
			t.Fatalf("%s: ImplementationVersion = %d, want > 0", e.Kind, e.ImplementationVersion)
		}
		if seen[e.Kind] {
			t.Fatalf("kind 重复: %s", e.Kind)
		}
		seen[e.Kind] = true
		c, ok := Catalog(e.Kind)
		if !ok {
			t.Fatalf("Catalog(%s) 不存在", e.Kind)
		}
		if c.ImplementationVersion != e.ImplementationVersion {
			t.Fatalf("%s: All() 版本 %d ≠ Catalog() 版本 %d",
				e.Kind, e.ImplementationVersion, c.ImplementationVersion)
		}
	}
	// 顺序合同与 TestAll 保持一致，不得漂移
	if len(all) != len(wantKinds) {
		t.Fatalf("目录项数 = %d, want %d", len(all), len(wantKinds))
	}
	for i, e := range all {
		if e.Kind != wantKinds[i] {
			t.Fatalf("第 %d 项 kind = %s, want %s", i, e.Kind, wantKinds[i])
		}
	}
}

func TestBuild(t *testing.T) {
	// days<=0 → 默认参数
	if f := Build("momentum", 0); f == nil || f.Name() != "N日动量(20)" {
		t.Fatalf("默认参数异常: %v", f)
	}
	// 显式参数
	if f := Build("momentum", 5); f == nil || f.Name() != "N日动量(5)" {
		t.Fatalf("参数传递异常: %v", f)
	}
	// 具体类型断言
	if _, ok := Build("kvalue", 0).(*K值); !ok {
		t.Fatalf("kvalue 应构造 *K值")
	}
	if _, ok := Build("body", 0).(*实体幅度); !ok {
		t.Fatalf("body 应构造 *实体幅度")
	}
	// 单根K线因子显式传正数 days 也被忽略
	if _, ok := Build("body", 5).(*实体幅度); !ok {
		t.Fatalf("body 应忽略 days")
	}
	// 未知 kind → nil
	if f := Build("no_such", 5); f != nil {
		t.Fatalf("未知 kind 应返回 nil, got %v", f)
	}
}
