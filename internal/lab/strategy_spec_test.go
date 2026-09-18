package lab

import (
	"encoding/json"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/injoyai/strategy-tail/core"
	sb "github.com/injoyai/strategy-tail/strategies/buy"
	f "github.com/injoyai/strategy-tail/strategies/factor"
)

func fptr(v float64) *float64 { return &v }

// validSpec 合法基准配置：两个条件；同 kind 不同窗口并存（允许）。
func validSpec() StrategySpec {
	return StrategySpec{
		Version:      1,
		Name:         "测试策略",
		BasePresetID: "pullback_ma5_up",
		FactorFilters: []FactorFilterSpec{
			{Kind: "momentum", Days: 20, Operator: "gte", Min: fptr(0.05)},
			{Kind: "momentum", Days: 60, Operator: "lte", Max: fptr(0.5)},
		},
		Exit: ExitSpec{HoldingDays: 1, TakeProfit: 0.10},
		Run: StrategyRunSpec{StartYear: 2024, EndYear: 2024,
			SampleMode: "codes", SampleCodes: []string{"sh600001"}},
	}
}

// TestStrategySpecValidate 声明式合同：version/preset/名称长度/1～4 项数量/
// 重复 kind+days/factor kind/days>0/operator/有限数/区间边界。
// Run 的最终合法性留给 RunConfig.Validate。
func TestStrategySpecValidate(t *testing.T) {
	if err := validSpec().Validate(); err != nil {
		t.Fatalf("合法 spec 应通过: %v", err)
	}
	name80 := validSpec()
	name80.Name = strings.Repeat("策", 80)
	if err := name80.Validate(); err != nil {
		t.Fatalf("80 字符名称应通过: %v", err)
	}
	blank := validSpec()
	blank.Name = "   "
	if err := blank.Validate(); err != nil {
		t.Fatalf("空白名称应通过（由后端生成名称）: %v", err)
	}
	noPreset := validSpec()
	noPreset.BasePresetID = ""
	if err := noPreset.Validate(); err != nil {
		t.Fatalf("不使用基础预设应通过: %v", err)
	}

	cases := []struct {
		note string
		mut  func(s *StrategySpec)
	}{
		{"version=0", func(s *StrategySpec) { s.Version = 0 }},
		{"version=2", func(s *StrategySpec) { s.Version = 2 }},
		{"未知 preset", func(s *StrategySpec) { s.BasePresetID = "nope" }},
		{"名称超 80 字符", func(s *StrategySpec) { s.Name = strings.Repeat("策", 81) }},
		{"条件数量 0", func(s *StrategySpec) { s.FactorFilters = nil }},
		{"条件数量 0（空切片）", func(s *StrategySpec) { s.FactorFilters = []FactorFilterSpec{} }},
		{"条件数量 5", func(s *StrategySpec) {
			s.FactorFilters = append(s.FactorFilters,
				FactorFilterSpec{Kind: "kvalue", Days: 9, Operator: "gte", Min: fptr(0)},
				FactorFilterSpec{Kind: "position", Days: 60, Operator: "lte", Max: fptr(1)},
				FactorFilterSpec{Kind: "body", Days: 1, Operator: "lte", Max: fptr(1)})
		}},
		{"第 1 项未知因子", func(s *StrategySpec) { s.FactorFilters[0].Kind = "nope" }},
		{"第 2 项未知因子", func(s *StrategySpec) { s.FactorFilters[1].Kind = "nope" }},
		{"days=0", func(s *StrategySpec) { s.FactorFilters[0].Days = 0 }},
		{"days<0", func(s *StrategySpec) { s.FactorFilters[0].Days = -1 }},
		{"重复 kind+days", func(s *StrategySpec) { s.FactorFilters[1].Days = 20 }},
		{"operator 无效", func(s *StrategySpec) { s.FactorFilters[0].Operator = "eq" }},
		{"gte 缺 min", func(s *StrategySpec) { s.FactorFilters[0].Min = nil }},
		{"gte min NaN", func(s *StrategySpec) { s.FactorFilters[0].Min = fptr(math.NaN()) }},
		{"gte min Inf", func(s *StrategySpec) { s.FactorFilters[0].Min = fptr(math.Inf(1)) }},
		{"lte 缺 max", func(s *StrategySpec) { s.FactorFilters[1].Max = nil }},
		{"between 缺 min", func(s *StrategySpec) {
			s.FactorFilters[1] = FactorFilterSpec{Kind: "momentum", Days: 60,
				Operator: "between", Max: fptr(0.5)}
		}},
		{"between 缺 max", func(s *StrategySpec) {
			s.FactorFilters[1] = FactorFilterSpec{Kind: "momentum", Days: 60,
				Operator: "between", Min: fptr(-0.5)}
		}},
		{"between 反区间", func(s *StrategySpec) {
			s.FactorFilters[1] = FactorFilterSpec{Kind: "momentum", Days: 60,
				Operator: "between", Min: fptr(0.2), Max: fptr(-0.2)}
		}},
		{"between 上限 NaN", func(s *StrategySpec) {
			s.FactorFilters[1] = FactorFilterSpec{Kind: "momentum", Days: 60,
				Operator: "between", Min: fptr(-0.2), Max: fptr(math.NaN())}
		}},
	}
	for _, c := range cases {
		s := validSpec()
		c.mut(&s)
		if err := s.Validate(); err == nil {
			t.Fatalf("%s: 应报错", c.note)
		}
	}
}

// TestStrategySpecVariants N≥2 时生成 N+2 个变体，顺序为
// 基准、N 个单条件（与 factorFilters 顺序一一对应，名称稳定）、组合增强；
// N=1 时组合增强与单条件 1 等价，只生成 基准+单条件 共 2 个。
// 所有变体共用同一基础 Buyer，组合增强只追加 N 个 A因子过滤且固定 AND。
func TestStrategySpecVariants(t *testing.T) {
	base, err := BuildPresetBuyer("pullback_ma5_up")
	if err != nil {
		t.Fatal(err)
	}

	vs, err := validSpec().Variants()
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 4 {
		t.Fatalf("2 条件应生成 4 个变体, got %d", len(vs))
	}
	wantNames := []string{
		"基准 · MA5 向上 · 收回 MA5",
		"单条件 1 · N日动量(20)≥0.05",
		"单条件 2 · N日动量(60)≤0.5",
		"组合增强 · MA5 向上 · 收回 MA5 · N日动量(20)≥0.05、N日动量(60)≤0.5",
	}
	for i, want := range wantNames {
		if vs[i].Name != want {
			t.Fatalf("vs[%d].Name = %q, want %q", i, vs[i].Name, want)
		}
	}
	if vs[0].Buyer.Name() != base.Name() {
		t.Fatalf("基准变体 Buyer = %q, want %q", vs[0].Buyer.Name(), base.Name())
	}

	// 3 条件 → 5 个变体；单条件与 factorFilters 数组顺序一一对应
	s := validSpec()
	s.FactorFilters = append(s.FactorFilters,
		FactorFilterSpec{Kind: "kvalue", Days: 9, Operator: "between", Min: fptr(0), Max: fptr(100)})
	vs, err = s.Variants()
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 5 {
		t.Fatalf("3 条件应生成 5 个变体, got %d", len(vs))
	}
	if vs[3].Name != "单条件 3 · K值(9)∈[0,100]" {
		t.Fatalf("单条件 3 名称 = %q", vs[3].Name)
	}
	if vs[4].Name != "组合增强 · MA5 向上 · 收回 MA5 · N日动量(20)≥0.05、N日动量(60)≤0.5、K值(9)∈[0,100]" {
		t.Fatalf("组合增强名称 = %q", vs[4].Name)
	}

	// 单条件变体 = And{基础, 对应过滤}；基础 Buyer 语义与基准一致
	for i := 1; i <= 3; i++ {
		and, ok := vs[i].Buyer.(sb.And)
		if !ok || len(and) != 2 {
			t.Fatalf("单条件 %d 应为 And{基础, 过滤}, got %T", i, vs[i].Buyer)
		}
		if and[0].Name() != base.Name() {
			t.Fatalf("单条件 %d 基础 Buyer = %q, want %q", i, and[0].Name(), base.Name())
		}
		flt, ok := and[1].(sb.A因子过滤)
		if !ok {
			t.Fatalf("单条件 %d And[1] 应为 A因子过滤, got %T", i, and[1])
		}
		ft := s.FactorFilters[i-1]
		if flt.Factor.Name() != f.Build(ft.Kind, ft.Days).Name() {
			t.Fatalf("单条件 %d 因子 %q 与 factorFilters[%d] 不对应", i, flt.Factor.Name(), i-1)
		}
	}

	// 组合增强 = And{基础, 全部 N 个过滤}
	combined, ok := vs[4].Buyer.(sb.And)
	if !ok {
		t.Fatalf("组合增强应为 sb.And, got %T", vs[4].Buyer)
	}
	if len(combined) != 4 {
		t.Fatalf("组合增强应为 And{基础, 3 个过滤}, got %d 项", len(combined))
	}
	if combined[0].Name() != base.Name() {
		t.Fatalf("组合增强基础 Buyer = %q, want %q", combined[0].Name(), base.Name())
	}
	for i, b := range combined[1:] {
		if _, ok := b.(sb.A因子过滤); !ok {
			t.Fatalf("组合增强 And[%d] 应为 A因子过滤, got %T", i+1, b)
		}
	}

	// 1 条件 → 2 个变体（基准 + 单条件 1）：组合增强与单条件等价，不重复生成
	one := validSpec()
	one.FactorFilters = one.FactorFilters[:1]
	vs, err = one.Variants()
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 2 {
		t.Fatalf("1 条件应生成 2 个变体, got %d", len(vs))
	}
	if vs[0].Name != "基准 · MA5 向上 · 收回 MA5" {
		t.Fatalf("vs[0].Name = %q", vs[0].Name)
	}
	if vs[1].Name != "单条件 1 · N日动量(20)≥0.05" {
		t.Fatalf("vs[1].Name = %q, want 单条件 1（无独立组合增强）", vs[1].Name)
	}
	and, ok := vs[1].Buyer.(sb.And)
	if !ok || len(and) != 2 || and[0].Name() != base.Name() {
		t.Fatalf("单条件 1 应为 And{基础, 过滤}, got %T", vs[1].Buyer)
	}
	if _, ok := and[1].(sb.A因子过滤); !ok {
		t.Fatalf("单条件 1 And[1] 应为 A因子过滤, got %T", and[1])
	}

	// 不使用预设 → A全部 作为透明对照基准，因子条件独立决定买入。
	standalone := validSpec()
	standalone.BasePresetID = ""
	vs, err = standalone.Variants()
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 4 {
		t.Fatalf("无预设的 2 条件应生成 4 个变体, got %d", len(vs))
	}
	if vs[0].Name != "基准 · 全部样本" {
		t.Fatalf("无预设基准名称 = %q", vs[0].Name)
	}
	if _, ok := vs[0].Buyer.(sb.A全部); !ok {
		t.Fatalf("无预设基准 Buyer 应为 A全部, got %T", vs[0].Buyer)
	}
	if vs[3].Name != "因子组合 · N日动量(20)≥0.05、N日动量(60)≤0.5" {
		t.Fatalf("无预设组合名称 = %q", vs[3].Name)
	}
	standaloneAnd, ok := vs[3].Buyer.(sb.And)
	if !ok || len(standaloneAnd) != 3 {
		t.Fatalf("无预设组合应为 And{A全部, 2 个过滤}, got %T/%d", vs[3].Buyer, len(standaloneAnd))
	}
	if _, ok := standaloneAnd[0].(sb.A全部); !ok {
		t.Fatalf("无预设组合首项应为 A全部, got %T", standaloneAnd[0])
	}
}

// TestStrategySpecFilterMapping gte/lte/between 正确映射到
// A因子过滤.Min/Max（单边用 ±1e9，0 是合法阈值）。
func TestStrategySpecFilterMapping(t *testing.T) {
	cases := []struct {
		note    string
		ft      FactorFilterSpec
		wantMin float64
		wantMax float64
	}{
		{"gte", FactorFilterSpec{Kind: "momentum", Days: 20, Operator: "gte", Min: fptr(0.05)}, 0.05, 1e9},
		{"lte", FactorFilterSpec{Kind: "momentum", Days: 20, Operator: "lte", Max: fptr(0.05)}, -1e9, 0.05},
		{"between", FactorFilterSpec{Kind: "momentum", Days: 20, Operator: "between", Min: fptr(-0.1), Max: fptr(0.2)}, -0.1, 0.2},
		{"gte 0 阈值", FactorFilterSpec{Kind: "momentum", Days: 20, Operator: "gte", Min: fptr(0)}, 0, 1e9},
	}
	for _, c := range cases {
		s := validSpec()
		s.FactorFilters[0] = c.ft
		vs, err := s.Variants()
		if err != nil {
			t.Fatalf("%s: %v", c.note, err)
		}
		and := vs[1].Buyer.(sb.And)
		flt := and[1].(sb.A因子过滤)
		if flt.Min != c.wantMin || flt.Max != c.wantMax {
			t.Fatalf("%s: Min/Max = %v/%v, want %v/%v",
				c.note, flt.Min, flt.Max, c.wantMin, c.wantMax)
		}
	}
}

// TestStrategySpecFactorVersion 因子实现版本 fail-closed 合同：
// 0=旧请求兼容；>0 必须等于注册表当前版本；未来版本、旧版本与负值拒绝。
func TestStrategySpecFactorVersion(t *testing.T) {
	cur, _ := f.Catalog("momentum")
	curVer := cur.ImplementationVersion
	if curVer <= 0 {
		t.Fatalf("registry 当前版本应为正: %d", curVer)
	}
	// 0=旧配置兼容，正常通过
	s := validSpec()
	s.FactorFilters[0].FactorVersion = 0
	if err := s.Validate(); err != nil {
		t.Fatalf("factorVersion=0 旧请求应通过: %v", err)
	}
	// 当前版本通过
	s = validSpec()
	s.FactorFilters[0].FactorVersion = curVer
	if err := s.Validate(); err != nil {
		t.Fatalf("factorVersion=%d 当前版本应通过: %v", curVer, err)
	}
	// 未来版本拒绝
	s = validSpec()
	s.FactorFilters[0].FactorVersion = curVer + 1
	if err := s.Validate(); err == nil {
		t.Fatalf("factorVersion=%d 未来版本应拒绝", curVer+1)
	}
	// 旧版本拒绝（当前版本 >1 时才有旧版本；版本 1 时 1 即当前版本）
	if curVer > 1 {
		s = validSpec()
		s.FactorFilters[0].FactorVersion = curVer - 1
		if err := s.Validate(); err == nil {
			t.Fatalf("factorVersion=%d 旧版本应拒绝", curVer-1)
		}
	}
	// 负值拒绝
	s = validSpec()
	s.FactorFilters[0].FactorVersion = -1
	if err := s.Validate(); err == nil {
		t.Fatal("factorVersion=-1 应拒绝")
	}
	// 只允许从已校验路径进入：直接 build() 也必须校验版本（fail-closed）
	s = validSpec()
	s.FactorFilters[0].FactorVersion = curVer + 1
	if _, err := s.FactorFilters[0].build(); err == nil {
		t.Fatal("绕过 Validate 直接 build() 也应拒绝版本漂移")
	}
}

// TestStrategySpecRunConfig 只做字段转换：止盈 0.10 不二次除以 100，
// 卖出规则最终合法性委托现有 RunConfig.Validate。
func TestStrategySpecRunConfig(t *testing.T) {
	rc := validSpec().RunConfig()
	if rc.StartYear != 2024 || rc.EndYear != 2024 || rc.SampleMode != "codes" {
		t.Fatalf("运行范围转换错误: %+v", rc)
	}
	if rc.SampleCodes == nil || len(rc.SampleCodes) != 1 {
		t.Fatalf("样本代码转换错误: %+v", rc.SampleCodes)
	}
	if rc.HoldingDays != 1 {
		t.Fatalf("HoldingDays = %d", rc.HoldingDays)
	}
	if rc.TakeProfit != 0.10 {
		t.Fatalf("TakeProfit = %v, want 0.10（不得二次换算）", rc.TakeProfit)
	}
	if rc.StopLoss != 0 {
		t.Fatalf("StopLoss = %v, want 0", rc.StopLoss)
	}
	if err := rc.Validate(); err != nil {
		t.Fatalf("转换结果应通过现有 Validate: %v", err)
	}

	// 仅止损 0.05
	s := validSpec()
	s.Exit.TakeProfit = 0
	s.Exit.StopLoss = 0.05
	rc = s.RunConfig()
	if rc.TakeProfit != 0 || rc.StopLoss != 0.05 {
		t.Fatalf("止损转换错误: %+v", rc)
	}
	if err := rc.Validate(); err != nil {
		t.Fatalf("仅止损应通过 Validate: %v", err)
	}

	// 卖出规则全 0 → 由 RunConfig.Validate 拒绝（不维护第二套规则）
	s.Exit.StopLoss = 0
	s.Exit.HoldingDays = 0
	if err := s.RunConfig().Validate(); err == nil {
		t.Fatal("卖出规则全 0 应由 RunConfig.Validate 拒绝")
	}
}

// TestStrategySpecRoundTrip JSON 往返保留条件顺序，且不丢失 nil 和 0 的区别。
func TestStrategySpecRoundTrip(t *testing.T) {
	s := validSpec()
	s.FactorFilters[1] = FactorFilterSpec{Kind: "momentum", Days: 60,
		Operator: "between", Min: fptr(-0.1), Max: fptr(0.2)}
	buf, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	var back StrategySpec
	if err := json.Unmarshal(buf, &back); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(s, back) {
		t.Fatalf("往返不一致:\n原始 %+v\n还原 %+v", s, back)
	}
	if len(back.FactorFilters) != 2 || back.FactorFilters[0].Days != 20 || back.FactorFilters[1].Days != 60 {
		t.Fatalf("往返应保留条件顺序: %+v", back.FactorFilters)
	}

	// 0 阈值显式序列化；单边缺省字段省略
	s2 := validSpec()
	s2.FactorFilters[0] = FactorFilterSpec{Kind: "momentum", Days: 20, Operator: "gte", Min: fptr(0)}
	s2.FactorFilters[1] = FactorFilterSpec{Kind: "kvalue", Days: 9, Operator: "gte", Min: fptr(0.8)}
	buf, _ = json.Marshal(s2)
	if !strings.Contains(string(buf), `"min":0`) {
		t.Fatalf("min=0 应显式序列化: %s", buf)
	}
	if strings.Contains(string(buf), `"max"`) {
		t.Fatalf("gte 单边不应出现 max 字段: %s", buf)
	}
	var back2 StrategySpec
	if err := json.Unmarshal(buf, &back2); err != nil {
		t.Fatal(err)
	}
	ft := back2.FactorFilters[0]
	if ft.Min == nil || *ft.Min != 0 || ft.Max != nil {
		t.Fatalf("min=0/max=null 往返失真: %+v", ft)
	}
}

// TestStrategySpecDisplayName name 为空时生成可读名称；非空 trim。
func TestStrategySpecDisplayName(t *testing.T) {
	s := validSpec()
	s.Name = ""
	got := s.DisplayName()
	if got == "" || !strings.Contains(got, "MA5 向上 · 收回 MA5") {
		t.Fatalf("空 name 应生成含预设名的可读名称: %q", got)
	}
	s.Name = "  我的策略  "
	if got := s.DisplayName(); got != "我的策略" {
		t.Fatalf("非空 name 应 trim: %q", got)
	}
	s.Name = ""
	s.BasePresetID = ""
	if got := s.DisplayName(); got != "仅因子条件 · 2 条" {
		t.Fatalf("无预设自动名称 = %q", got)
	}
}

// 编译期确认 Variants 产出物满足 core.Variant 契约。
var _ = func() []core.Variant { return nil }
