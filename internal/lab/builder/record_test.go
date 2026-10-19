package builder

import (
	"reflect"
	"testing"

	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/lib/extend"
	sb "github.com/injoyai/strategy-tail/strategies/buy"
	ss "github.com/injoyai/strategy-tail/strategies/sell"
)

// record_test.go 原因记录包装器的合同测试（R07/R15）：
// 包装器只转发一次求值并旁路登记帧，不改变短路顺序、不重复求值。

var stubKS = extend.Klines{}

type stubBuyer struct {
	name   string
	result bool
	calls  int
}

func (b *stubBuyer) Name() string                   { return b.name }
func (b *stubBuyer) Buy(string, extend.Klines) bool { b.calls++; return b.result }

type stubSeller struct {
	name   string
	result bool
	calls  int
}

func (s *stubSeller) Name() string                              { return s.name }
func (s *stubSeller) Sell(string, extend.Klines, core.Buy) bool { s.calls++; return s.result }

// TestRecordWrapForwardsExactlyOnce 包装器逐次转发求值并委托 Name；nil 原样返回。
func TestRecordWrapForwardsExactlyOnce(t *testing.T) {
	b := &stubBuyer{name: "测试买入", result: true}
	rc := NewRecordContext()
	w := rc.WrapBuyerRule(b, RuleDesc{Path: "buy", Role: RoleBuy, Name: b.name}, false, true)
	if w.Name() != b.name {
		t.Fatalf("Name 应委托 inner: %q", w.Name())
	}
	for i := 0; i < 3; i++ {
		if !w.Buy("sh600001", stubKS) {
			t.Fatal("Buy 结果应原样转发")
		}
	}
	if b.calls != 3 {
		t.Fatalf("inner 每次调用应恰好求值一次, got %d", b.calls)
	}

	s := &stubSeller{name: "测试卖出", result: true}
	ws := rc.WrapSellerRule(s, RuleDesc{Path: "sellSignal", Role: RoleSellSignal, Name: s.name}, false, true)
	if ws.Name() != s.name {
		t.Fatalf("Seller Name 应委托 inner: %q", ws.Name())
	}
	if !ws.Sell("sh600001", stubKS, core.Buy{}) {
		t.Fatal("Sell 结果应原样转发")
	}
	if s.calls != 1 {
		t.Fatalf("Seller inner 应恰好求值一次, got %d", s.calls)
	}

	if rc.WrapBuyerRule(nil, RuleDesc{}, false, false) != nil {
		t.Fatal("nil Buyer 应原样返回")
	}
	if rc.WrapBuyerTree(nil, "buy", RoleBuy, true) != nil {
		t.Fatal("nil Buyer 树应原样返回")
	}
	if rc.WrapSellerTree(nil, "sell", RoleSell, true) != nil {
		t.Fatal("nil Seller 树应原样返回")
	}
}

// TestRecordBuyerAndShortCircuit And 首假短路：后续分支不求值、无帧。
func TestRecordBuyerAndShortCircuit(t *testing.T) {
	a := &stubBuyer{name: "a", result: false}
	b := &stubBuyer{name: "b", result: true}
	c := &stubBuyer{name: "c", result: true}
	rc := NewRecordContext()
	w := rc.WrapBuyerTree(sb.And{a, b, c}, "buy", RoleBuy, true)
	if w.Buy("sh600001", stubKS) {
		t.Fatal("And 首假应为假")
	}
	if a.calls != 1 || b.calls != 0 || c.calls != 0 {
		t.Fatalf("短路顺序被改变: a=%d b=%d c=%d", a.calls, b.calls, c.calls)
	}
	root := rc.frames[len(rc.frames)-1]
	if root.result || root.desc.Path != "buy" || root.desc.Op != "and" {
		t.Fatalf("根帧不符: %+v", root.desc)
	}
	if len(root.children) != 1 || root.children[0].desc.Path != "buy.children[0]" || root.children[0].result {
		t.Fatalf("只有首个子规则应有失败帧: %+v", root.children)
	}
	if rc.ExtractSuccessPath() != nil {
		t.Fatal("根失败不应有成功路径")
	}
}

// TestRecordBuyerOrShortCircuit Or 首真即返：后续分支不求值；成功路径只取首个真分支。
func TestRecordBuyerOrShortCircuit(t *testing.T) {
	a := &stubBuyer{name: "a", result: true}
	b := &stubBuyer{name: "b", result: true}
	rc := NewRecordContext()
	w := rc.WrapBuyerTree(sb.Or{a, b}, "buy", RoleBuy, true)
	if !w.Buy("sh600001", stubKS) {
		t.Fatal("Or 首真应为真")
	}
	if a.calls != 1 || b.calls != 0 {
		t.Fatalf("短路顺序被改变: a=%d b=%d", a.calls, b.calls)
	}
	if got := rc.ExtractSuccessPath(); !reflect.DeepEqual(got, []string{"buy", "buy.children[0]"}) {
		t.Fatalf("成功路径 = %v", got)
	}
}

// TestRecordNestedFailurePruned 嵌套失败分支：内层 And 短路后其未执行分支无帧，
// Or 回落到首个为真的后续分支（未执行分支天然剪枝）。
func TestRecordNestedFailurePruned(t *testing.T) {
	a := &stubBuyer{name: "a", result: false}
	b := &stubBuyer{name: "b", result: true}
	c := &stubBuyer{name: "c", result: true}
	rc := NewRecordContext()
	w := rc.WrapBuyerTree(sb.Or{sb.And{a, b}, c}, "buy", RoleBuy, true)
	if !w.Buy("sh600001", stubKS) {
		t.Fatal("Or 第二分支为真应为真")
	}
	if a.calls != 1 || b.calls != 0 || c.calls != 1 {
		t.Fatalf("求值次数被改变: a=%d b=%d c=%d", a.calls, b.calls, c.calls)
	}
	if got := rc.ExtractSuccessPath(); !reflect.DeepEqual(got, []string{"buy", "buy.children[1]"}) {
		t.Fatalf("成功路径 = %v", got)
	}
	root := rc.frames[len(rc.frames)-1]
	if len(root.children) != 2 {
		t.Fatalf("应只有两个实际执行分支的帧: %d", len(root.children))
	}
	if root.children[0].result {
		t.Fatal("内层 And 帧应为假")
	}
	if len(root.children[0].children) != 1 {
		t.Fatalf("内层 And 短路后只应有首个子帧: %+v", root.children[0].children)
	}
}

// TestWrapBuyerTreeRegistersChildrenBeforeGroup 递归包装的注册顺序：子规则先于组节点。
func TestWrapBuyerTreeRegistersChildrenBeforeGroup(t *testing.T) {
	a := &stubBuyer{result: true}
	b := &stubBuyer{result: true}
	c := &stubBuyer{result: true}
	d := &stubBuyer{result: true}
	rc := NewRecordContext()
	rc.WrapBuyerTree(sb.And{sb.And{a, b}, sb.Or{c, d}}, "buy", RoleBuy, true)
	paths := make([]string, 0, len(rc.rules))
	for _, r := range rc.rules {
		paths = append(paths, r.Path)
	}
	want := []string{
		"buy.children[0].children[0]",
		"buy.children[0].children[1]",
		"buy.children[0]",
		"buy.children[1].children[0]",
		"buy.children[1].children[1]",
		"buy.children[1]",
		"buy",
	}
	if !reflect.DeepEqual(paths, want) {
		t.Fatalf("注册顺序 = %v", paths)
	}
}

// TestRecordSellerTreeAndShortCircuit 卖出侧 And 短路同样只登记实际执行分支。
func TestRecordSellerTreeAndShortCircuit(t *testing.T) {
	s1 := &stubSeller{name: "s1", result: false}
	s2 := &stubSeller{name: "s2", result: true}
	rc := NewRecordContext()
	w := rc.WrapSellerTree(ss.And{s1, s2}, "sell", RoleSell, true)
	if w.Sell("sh600001", stubKS, core.Buy{}) {
		t.Fatal("And 首假应为假")
	}
	if s1.calls != 1 || s2.calls != 0 {
		t.Fatalf("短路顺序被改变: s1=%d s2=%d", s1.calls, s2.calls)
	}
	root := rc.frames[len(rc.frames)-1]
	if len(root.children) != 1 || root.children[0].desc.Path != "sell.children[0]" || root.children[0].result {
		t.Fatalf("只有首个子规则应有失败帧: %+v", root.children)
	}
}

// TestRecordSellerMatchesSellerShapes RecordSeller 分组形态与 Seller() 一致：
// 0→恒假 Or；1→单规则提升为根（不双重包装）；N→Or 根且风控先求值。
// 空前缀下 A持仓N天 恒不触发，故只断言帧结构与包装形态。
func TestRecordSellerMatchesSellerShapes(t *testing.T) {
	risk := func(days float64) *BuilderNode {
		return &BuilderNode{Type: "condition", ComponentID: "risk_holding_days", Params: map[string]any{"days": days}}
	}

	empty := &BuilderSpec{}
	seller, err := empty.RecordSeller(NewRecordContext())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := seller.(ss.Or); !ok || seller.Sell("sh600001", stubKS, core.Buy{}) {
		t.Fatalf("空卖出组合应为恒假 ss.Or, got %T", seller)
	}

	one := &BuilderSpec{Risks: []*BuilderNode{risk(1)}}
	plain, err := one.Seller()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := plain.(ss.A持仓N天); !ok {
		t.Fatalf("单规则 Seller() 不应包 Or, got %T", plain)
	}
	rc := NewRecordContext()
	seller, err = one.RecordSeller(rc)
	if err != nil {
		t.Fatal(err)
	}
	if seller.Sell("sh600001", stubKS, core.Buy{}) {
		t.Fatal("空前缀下持仓天数规则不应触发")
	}
	if len(rc.frames) != 1 || rc.frames[0].desc.Path != "risks[0]" || rc.frames[0].desc.Role != roleRisk {
		t.Fatalf("单规则应提升为根且路径 risks[0]: %+v", rc.frames)
	}

	two := &BuilderSpec{Risks: []*BuilderNode{risk(1), risk(2)}}
	rc2 := NewRecordContext()
	seller, err = two.RecordSeller(rc2)
	if err != nil {
		t.Fatal(err)
	}
	if seller.Sell("sh600001", stubKS, core.Buy{}) {
		t.Fatal("空前缀下不应触发")
	}
	root := rc2.frames[len(rc2.frames)-1]
	if root.desc.Path != "sell" || root.desc.Op != "or" || !root.orGroup || root.result {
		t.Fatalf("多规则应为 Or 根: %+v", root.desc)
	}
	if len(root.children) != 2 || root.children[0].desc.Path != "risks[0]" || root.children[1].desc.Path != "risks[1]" {
		t.Fatalf("根应有两个风控子帧: %+v", root.children)
	}
	if rc2.ExtractSuccessPath() != nil {
		t.Fatal("根失败不应有成功路径")
	}
}

// TestRecordConditionKeepsDeclaredParams 条件节点参数按声明原样记录，
// 不补目录默认值；Name 取构建出的组件实例名。
func TestRecordConditionKeepsDeclaredParams(t *testing.T) {
	spec := &BuilderSpec{Buy: &BuilderNode{
		Type:        "condition",
		ComponentID: "buy_price_above_ma",
		Params:      map[string]any{"period": 20.0},
	}}
	rc := NewRecordContext()
	buyer, err := spec.RecordBuyer(rc)
	if err != nil {
		t.Fatal(err)
	}
	rules := rc.Rules()
	if len(rules) != 1 {
		t.Fatalf("应只登记根规则: %+v", rules)
	}
	r := rules[0]
	if r.Path != "buy" || r.Role != RoleBuy || r.Op != "" {
		t.Fatalf("根叶子规则描述不符: %+v", r)
	}
	if !reflect.DeepEqual(r.Params, map[string]any{"period": 20.0}) {
		t.Fatalf("声明参数应原样记录: %+v", r.Params)
	}
	if r.Name == "" || r.Name != buyer.Name() {
		t.Fatalf("Name 应取组件实例名: %q != %q", r.Name, buyer.Name())
	}
}

// TestRecordFramesResetPerRootEval 根入口每次求值重建帧缓冲，不残留上次结果。
func TestRecordFramesResetPerRootEval(t *testing.T) {
	flippy := &stubBuyer{result: true}
	rc := NewRecordContext()
	w := rc.WrapBuyerTree(sb.Or{flippy}, "buy", RoleBuy, true)
	if !w.Buy("sh600001", stubKS) {
		t.Fatal("首真应为真")
	}
	if got := rc.ExtractSuccessPath(); !reflect.DeepEqual(got, []string{"buy", "buy.children[0]"}) {
		t.Fatalf("首次成功路径 = %v", got)
	}
	flippy.result = false
	if w.Buy("sh600001", stubKS) {
		t.Fatal("次假应为假")
	}
	if rc.ExtractSuccessPath() != nil {
		t.Fatal("第二次求值应重置帧缓冲")
	}
	if len(rc.frames) != 1 || rc.frames[0].result {
		t.Fatalf("帧应重建而非追加: %+v", rc.frames)
	}
}

// TestNewFactorRuleParams 因子规则参数直接取声明：必填三项恒在，
// factorVersion/min/max 仅在声明时出现。
func TestNewFactorRuleParams(t *testing.T) {
	min, max := 0.05, 0.9
	ft := &FactorFilterSpec{Kind: "kvalue", Days: 9, FactorVersion: 3, Operator: "between", Min: &min, Max: &max}
	desc := NewFactorRule(ft, "buy.children[1]", RoleBuy)
	want := map[string]any{"kind": "kvalue", "days": 9, "operator": "between", "factorVersion": 3, "min": 0.05, "max": 0.9}
	if !reflect.DeepEqual(desc.Params, want) {
		t.Fatalf("参数 = %+v, want %+v", desc.Params, want)
	}
	if desc.Path != "buy.children[1]" || desc.Role != RoleBuy || desc.Name != ft.Describe() {
		t.Fatalf("描述不符: %+v", desc)
	}

	bare := &FactorFilterSpec{Kind: "momentum", Days: 20, Operator: "gte", Min: &min}
	params := NewFactorRule(bare, "buy", RoleBuy).Params
	if _, ok := params["factorVersion"]; ok {
		t.Fatal("缺省版本不应出现")
	}
	if _, ok := params["max"]; ok {
		t.Fatal("缺省上限不应出现")
	}
}
