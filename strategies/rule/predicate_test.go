package rule

import (
	"math"
	"testing"
)

func TestCompareThresholdBoundaries(t *testing.T) {
	// 阈值相等边界：每个方向对 value==threshold 的判定必须严格符合操作符语义。
	// 同时覆盖浮点与整数厘两个值域。
	cases := []struct {
		name  string
		left  Value
		op    Op
		right Value
		want  bool
	}{
		// 浮点域，阈值 10
		{"浮点 相等 Lt", FloatValue(10), Lt, FloatValue(10), false},
		{"浮点 相等 Le", FloatValue(10), Le, FloatValue(10), true},
		{"浮点 相等 Gt", FloatValue(10), Gt, FloatValue(10), false},
		{"浮点 相等 Ge", FloatValue(10), Ge, FloatValue(10), true},
		{"浮点 相等 Eq", FloatValue(10), Eq, FloatValue(10), true},
		{"浮点 大于 Lt", FloatValue(10.001), Lt, FloatValue(10), false},
		{"浮点 大于 Le", FloatValue(10.001), Le, FloatValue(10), false},
		{"浮点 大于 Gt", FloatValue(10.001), Gt, FloatValue(10), true},
		{"浮点 大于 Ge", FloatValue(10.001), Ge, FloatValue(10), true},
		{"浮点 大于 Eq", FloatValue(10.001), Eq, FloatValue(10), false},
		{"浮点 小于 Lt", FloatValue(9.999), Lt, FloatValue(10), true},
		{"浮点 小于 Le", FloatValue(9.999), Le, FloatValue(10), true},
		{"浮点 小于 Gt", FloatValue(9.999), Gt, FloatValue(10), false},
		{"浮点 小于 Ge", FloatValue(9.999), Ge, FloatValue(10), false},
		{"浮点 小于 Eq", FloatValue(9.999), Eq, FloatValue(10), false},
		// 整数厘域，阈值 10000 厘（10.000 元）
		{"厘价 相等 Lt", PriceValue(10.0), Lt, PriceValue(10.0), false},
		{"厘价 相等 Le", PriceValue(10.0), Le, PriceValue(10.0), true},
		{"厘价 相等 Gt", PriceValue(10.0), Gt, PriceValue(10.0), false},
		{"厘价 相等 Ge", PriceValue(10.0), Ge, PriceValue(10.0), true},
		{"厘价 相等 Eq", PriceValue(10.0), Eq, PriceValue(10.0), true},
		{"厘价 高 1 厘 Gt", PriceValue(10.001), Gt, PriceValue(10.0), true},
		{"厘价 高 1 厘 Gt 反向", PriceValue(10.0), Gt, PriceValue(10.001), false},
		{"厘价 低 1 厘 Lt", PriceValue(9.999), Lt, PriceValue(10.0), true},
		// 0/缺省参数语义：0 是真实阈值，不是无界
		{"零阈值 Eq 命中", FloatValue(0), Eq, FloatValue(0), true},
		{"负值偏离 Gt", FloatValue(-0.0001), Gt, FloatValue(0), false},
		// 负值阈值
		{"负阈值 Lt", FloatValue(-7.5), Lt, FloatValue(-2), true},
		{"负阈值 Ge", FloatValue(-2), Ge, FloatValue(-2), true},
		{"负阈值 Ge 未达", FloatValue(-2.5), Ge, FloatValue(-2), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rule, err := NewCompare(Literal(c.left), c.op, Literal(c.right))
			if err != nil {
				t.Fatalf("构造失败: %v", err)
			}
			if got := rule.Eval(NewInput("sh600000", makeKlines(10, 11), nil, nil)); got != c.want {
				t.Fatalf("%v %v %v = %v, 期望 %v", c.left, c.op, c.right, got, c.want)
			}
			if got := EvalCompare(c.left, c.op, c.right); got != c.want {
				t.Fatalf("直接比较 %v %v %v = %v, 期望 %v", c.left, c.op, c.right, got, c.want)
			}
		})
	}
}

func TestCompareCentBoundaryNotLost(t *testing.T) {
	// 厘级边界不能因统一转 float64 丢失：相邻厘价的比较走 int64 精确语义。
	// 12.345 与 12.346 元相差 1 厘，浮点化再比较可能吞掉该差异。
	rule, err := NewCompare(closeOperand{}, Gt, Literal(PriceValue(12.345-0.001)))
	if err != nil {
		t.Fatalf("构造失败: %v", err)
	}
	ks := makeKlines(12.344, 12.345) // 末根收盘 12.345 元 = 12345 厘
	if !rule.Eval(NewInput("sh600000", ks, nil, nil)) {
		t.Fatal("12345 厘 > 12344 厘 应触发（厘级边界必须精确保留）")
	}
}

func TestCompareInvalidAndCrossKindNotTrigger(t *testing.T) {
	ops := []Op{Lt, Le, Gt, Ge, Eq}
	cases := []struct {
		name  string
		left  Value
		right Value
	}{
		{"左侧 NaN", FloatValue(math.NaN()), FloatValue(1)},
		{"右侧 NaN", FloatValue(1), FloatValue(math.NaN())},
		{"左侧正无穷", FloatValue(math.Inf(1)), FloatValue(1)},
		{"右侧负无穷", FloatValue(1), FloatValue(math.Inf(-1))},
		{"左侧显式无效", InvalidValue(), FloatValue(1)},
		{"右侧显式无效", FloatValue(1), InvalidValue()},
		{"跨域 价对浮点", PriceValue(12.345), FloatValue(12.345)},
		{"跨域 浮点对价", FloatValue(12.345), PriceValue(12.345)},
	}
	for _, c := range cases {
		for _, op := range ops {
			t.Run(c.name+" "+op.String(), func(t *testing.T) {
				rule, err := NewCompare(Literal(c.left), op, Literal(c.right))
				if err != nil {
					t.Fatalf("构造失败: %v", err)
				}
				if rule.Eval(Input{Code: "sh600000"}) {
					t.Fatalf("无效值/跨域不得触发: %v %v %v", c.left, op, c.right)
				}
				if EvalCompare(c.left, op, c.right) {
					t.Fatalf("直接比较无效值/跨域不得触发: %v %v %v", c.left, op, c.right)
				}
			})
		}
	}
}

func TestCompareFloatEqIsExact(t *testing.T) {
	// 浮点 Eq 是精确相等语义：运行期浮点累加 0.1+0.2 与 0.3 不相等
	// （文档化行为，区间/阈值边界请用 Ge/Le）。
	a, b := 0.1, 0.2
	rule, err := NewCompare(Literal(FloatValue(a+b)), Eq, Literal(FloatValue(0.3)))
	if err != nil {
		t.Fatalf("构造失败: %v", err)
	}
	if rule.Eval(Input{}) {
		t.Fatal("运行期 0.1+0.2 == 0.3 在精确相等语义下应为 false")
	}
	same, err := NewCompare(Literal(FloatValue(0.5)), Eq, Literal(FloatValue(0.5)))
	if err != nil {
		t.Fatalf("构造失败: %v", err)
	}
	if !same.Eval(Input{}) {
		t.Fatal("0.5 == 0.5 应为 true")
	}
}

func TestCompareConstructorRejects(t *testing.T) {
	if _, err := NewCompare(nil, Gt, Literal(FloatValue(1))); err == nil {
		t.Fatal("左操作数为空应拒绝构造")
	}
	if _, err := NewCompare(Literal(FloatValue(1)), Gt, nil); err == nil {
		t.Fatal("右操作数为空应拒绝构造")
	}
	if _, err := NewCompare(Literal(FloatValue(1)), Op(99), Literal(FloatValue(1))); err == nil {
		t.Fatal("非法操作符应拒绝构造")
	}
	if EvalCompare(FloatValue(1), Op(99), FloatValue(1)) {
		t.Fatal("直接比较非法操作符不得触发")
	}
}

func TestCompareName(t *testing.T) {
	rule, err := NewCompare(closeOperand{}, Ge, Literal(PriceValue(20.0)))
	if err != nil {
		t.Fatalf("构造失败: %v", err)
	}
	if got, want := rule.Name(), "close >= 20"; got != want {
		t.Fatalf("Name = %q, 期望 %q", got, want)
	}
}

func TestRangeBoundaries(t *testing.T) {
	cases := []struct {
		name         string
		value        Value
		min, max     *Value
		minIn, maxIn bool
		want         bool
	}{
		// [10,20] 含两端
		{"含两端 下边界", FloatValue(10), mv(FloatValue(10)), mv(FloatValue(20)), true, true, true},
		{"含两端 上边界", FloatValue(20), mv(FloatValue(10)), mv(FloatValue(20)), true, true, true},
		{"含两端 中间", FloatValue(15), mv(FloatValue(10)), mv(FloatValue(20)), true, true, true},
		{"含两端 低于下界", FloatValue(9.999), mv(FloatValue(10)), mv(FloatValue(20)), true, true, false},
		{"含两端 高于上界", FloatValue(20.001), mv(FloatValue(10)), mv(FloatValue(20)), true, true, false},
		// (10,20) 不含两端
		{"不含两端 下边界", FloatValue(10), mv(FloatValue(10)), mv(FloatValue(20)), false, false, false},
		{"不含两端 上边界", FloatValue(20), mv(FloatValue(10)), mv(FloatValue(20)), false, false, false},
		{"不含两端 中间", FloatValue(15), mv(FloatValue(10)), mv(FloatValue(20)), false, false, true},
		{"不含两端 贴近下界", FloatValue(10.001), mv(FloatValue(10)), mv(FloatValue(20)), false, false, true},
		// 混合 [10,20)
		{"左含右不含 下边界", FloatValue(10), mv(FloatValue(10)), mv(FloatValue(20)), true, false, true},
		{"左含右不含 上边界", FloatValue(20), mv(FloatValue(10)), mv(FloatValue(20)), true, false, false},
		// 单边界限（结构性无界，替代旧 ±1e9）：只看下限
		{"仅下限 含边界 命中", FloatValue(10), mv(FloatValue(10)), nil, true, false, true},
		{"仅下限 含边界 低于", FloatValue(9.9), mv(FloatValue(10)), nil, true, false, false},
		{"仅下限 不含边界 相等", FloatValue(10), mv(FloatValue(10)), nil, false, false, false},
		{"仅下限 远高于", FloatValue(1e12), mv(FloatValue(10)), nil, true, false, true},
		// 单边界限：只看上限
		{"仅上限 含边界 命中", FloatValue(20), nil, mv(FloatValue(20)), false, true, true},
		{"仅上限 含边界 高于", FloatValue(20.1), nil, mv(FloatValue(20)), false, true, false},
		{"仅上限 极低值命中", FloatValue(-1e12), nil, mv(FloatValue(20)), false, true, true},
		// 负值区间（如乖离 [-7,-2]）
		{"负区间 内", FloatValue(-5), mv(FloatValue(-7)), mv(FloatValue(-2)), true, true, true},
		{"负区间 低于", FloatValue(-8), mv(FloatValue(-7)), mv(FloatValue(-2)), true, true, false},
		{"负区间 高于", FloatValue(-1), mv(FloatValue(-7)), mv(FloatValue(-2)), true, true, false},
		{"负区间 下边界", FloatValue(-7), mv(FloatValue(-7)), mv(FloatValue(-2)), true, true, true},
		// 0 阈值是真实值：0 不等于无界/缺省
		{"零下限 含边界 命中", FloatValue(0), mv(FloatValue(0)), nil, true, false, true},
		{"零下限 含边界 低于", FloatValue(-1e-9), mv(FloatValue(0)), nil, true, false, false},
		{"零上限 含边界 命中", FloatValue(0), nil, mv(FloatValue(0)), false, true, true},
		// 点区间（min==max）
		{"点区间 含命中", FloatValue(7), mv(FloatValue(7)), mv(FloatValue(7)), true, true, true},
		{"点区间 含未命中", FloatValue(8), mv(FloatValue(7)), mv(FloatValue(7)), true, true, false},
		{"点区间 不含恒假", FloatValue(7), mv(FloatValue(7)), mv(FloatValue(7)), false, false, false},
		// 整数厘域区间
		{"厘价区间 内", PriceValue(12.345), mv(PriceValue(12.34)), mv(PriceValue(12.35)), true, true, true},
		{"厘价区间 上边界", PriceValue(12.35), mv(PriceValue(12.34)), mv(PriceValue(12.35)), true, true, true},
		{"厘价区间 超出", PriceValue(12.351), mv(PriceValue(12.34)), mv(PriceValue(12.35)), true, true, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rule, err := NewRange(Literal(c.value), c.min, c.max, c.minIn, c.maxIn)
			if err != nil {
				t.Fatalf("构造失败: %v", err)
			}
			if got := rule.Eval(NewInput("sh600000", makeKlines(10), nil, nil)); got != c.want {
				t.Fatalf("Eval = %v, 期望 %v", got, c.want)
			}
		})
	}
}

func mv(v Value) *Value { return &v }

func TestRangeInvalidOperandNotTrigger(t *testing.T) {
	cases := []struct {
		name     string
		operand  Operand
		min, max *Value
	}{
		{"NaN 不触发", Literal(InvalidValue()), mv(FloatValue(0)), nil},
		{"操作数 NaN 字面量不触发", Literal(FloatValue(math.NaN())), mv(FloatValue(-1)), mv(FloatValue(1))},
		{"跨域 操作数厘对浮点界", Literal(PriceValue(12.345)), mv(FloatValue(0)), nil},
		{"跨域 操作数浮点对厘界", Literal(FloatValue(1)), mv(PriceValue(0.0)), nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rule, err := NewRange(c.operand, c.min, c.max, true, true)
			if err != nil {
				t.Fatalf("构造失败: %v", err)
			}
			if rule.Eval(NewInput("sh600000", makeKlines(10), nil, nil)) {
				t.Fatal("无效值/跨域不得触发")
			}
		})
	}
}

func TestRangeConstructorRejects(t *testing.T) {
	if _, err := NewRange(nil, mv(FloatValue(0)), nil, true, true); err == nil {
		t.Fatal("操作数为空应拒绝构造")
	}
	if _, err := NewRange(Literal(FloatValue(1)), nil, nil, true, true); err == nil {
		t.Fatal("两侧均无界应拒绝构造")
	}
	if _, err := NewRange(Literal(FloatValue(1)), mv(FloatValue(math.NaN())), nil, true, true); err == nil {
		t.Fatal("NaN 下限应拒绝构造")
	}
	if _, err := NewRange(Literal(FloatValue(1)), nil, mv(FloatValue(math.Inf(1))), true, true); err == nil {
		t.Fatal("Inf 上限应拒绝构造")
	}
	if _, err := NewRange(Literal(FloatValue(1)), mv(InvalidValue()), nil, true, true); err == nil {
		t.Fatal("无效值下限应拒绝构造")
	}
	// min > max 且未声明退化语义：构造期直接拒绝（计划 3.4）
	if _, err := NewRange(Literal(FloatValue(1)), mv(FloatValue(5)), mv(FloatValue(2)), true, true); err == nil {
		t.Fatal("下限大于上限应拒绝构造")
	}
	// 上下限值域不一致
	if _, err := NewRange(Literal(FloatValue(1)), mv(PriceValue(0.001)), mv(FloatValue(2)), true, true); err == nil {
		t.Fatal("上下限跨值域应拒绝构造")
	}
	// Max=0 退化为无上限是门面的映射职责：内核把 0 当真实边界
	rule, err := NewRange(Literal(FloatValue(-1)), nil, mv(FloatValue(0)), false, true)
	if err != nil {
		t.Fatalf("零上限是合法边界: %v", err)
	}
	if !rule.Eval(Input{}) {
		t.Fatal("-1 <= 0 应命中（0 是真实上限而非无界）")
	}
}

func TestRangeName(t *testing.T) {
	rule, err := NewRange(closeOperand{}, mv(PriceValue(0.2)), mv(PriceValue(12.0)), true, true)
	if err != nil {
		t.Fatalf("构造失败: %v", err)
	}
	if got, want := rule.Name(), "close∈[0.2,12]"; got != want {
		t.Fatalf("Name = %q, 期望 %q", got, want)
	}
	openRule, err := NewRange(closeOperand{}, nil, mv(PriceValue(12.0)), false, false)
	if err != nil {
		t.Fatalf("构造失败: %v", err)
	}
	if got, want := openRule.Name(), "close∈(-∞,12)"; got != want {
		t.Fatalf("Name = %q, 期望 %q", got, want)
	}
}

func TestAndShortCircuitAndOrder(t *testing.T) {
	cases := []struct {
		name      string
		results   []bool
		want      bool
		wantOrder []string
	}{
		{"首个即假 后续不求值", []bool{false, true, true}, false, []string{"a"}},
		{"中位假 停在中位", []bool{true, false, true}, false, []string{"a", "b"}},
		{"全真 求值全部", []bool{true, true, true}, true, []string{"a", "b", "c"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			order := []string{}
			rules := make([]Rule, 0, len(c.results))
			for i, r := range c.results {
				rules = append(rules, recordingRuleOf(string(rune('a'+i)), r, &order))
			}
			and, err := NewAnd(rules...)
			if err != nil {
				t.Fatalf("构造失败: %v", err)
			}
			if got := and.Eval(NewInput("sh600000", makeKlines(10), nil, nil)); got != c.want {
				t.Fatalf("And = %v, 期望 %v", got, c.want)
			}
			if len(order) != len(c.wantOrder) {
				t.Fatalf("求值顺序 %v, 期望 %v", order, c.wantOrder)
			}
			for i := range order {
				if order[i] != c.wantOrder[i] {
					t.Fatalf("求值顺序 %v, 期望 %v（必须左到右短路）", order, c.wantOrder)
				}
			}
		})
	}
}

func TestOrShortCircuitAndOrder(t *testing.T) {
	cases := []struct {
		name      string
		results   []bool
		want      bool
		wantOrder []string
	}{
		{"首个即真 后续不求值", []bool{true, false, false}, true, []string{"a"}},
		{"中位真 停在中位", []bool{false, true, false}, true, []string{"a", "b"}},
		{"全假 求值全部", []bool{false, false, false}, false, []string{"a", "b", "c"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			order := []string{}
			rules := make([]Rule, 0, len(c.results))
			for i, r := range c.results {
				rules = append(rules, recordingRuleOf(string(rune('a'+i)), r, &order))
			}
			or, err := NewOr(rules...)
			if err != nil {
				t.Fatalf("构造失败: %v", err)
			}
			if got := or.Eval(NewInput("sh600000", makeKlines(10), nil, nil)); got != c.want {
				t.Fatalf("Or = %v, 期望 %v", got, c.want)
			}
			for i := range order {
				if i >= len(c.wantOrder) || order[i] != c.wantOrder[i] {
					t.Fatalf("求值顺序 %v, 期望 %v（必须左到右短路）", order, c.wantOrder)
				}
			}
		})
	}
}

func TestOrInvalidLeafDoesNotSinkTree(t *testing.T) {
	// 计划 3.4：单个叶子缺数据（无效值不触发）不影响其它叶子成立，
	// 例如"因子缺数据 OR 显式全部买入"。
	invalidLeaf, err := NewCompare(Literal(InvalidValue()), Gt, Literal(FloatValue(0)))
	if err != nil {
		t.Fatalf("构造失败: %v", err)
	}
	or, err := NewOr(invalidLeaf, Const(true))
	if err != nil {
		t.Fatalf("构造失败: %v", err)
	}
	if !or.Eval(Input{}) {
		t.Fatal("无效叶子不触发时另一条成立仍应成立")
	}
}

func TestAndOrConstructorRejects(t *testing.T) {
	if _, err := NewAnd(); err == nil {
		t.Fatal("空 And 应拒绝构造")
	}
	if _, err := NewOr(); err == nil {
		t.Fatal("空 Or 应拒绝构造")
	}
	if _, err := NewAnd(nil); err == nil {
		t.Fatal("nil 子项应拒绝构造")
	}
	if _, err := NewOr(Const(true), nil); err == nil {
		t.Fatal("混合 nil 子项应拒绝构造")
	}
	// 零值直构作为防御：空组求值恒 false
	if (And{}).Eval(Input{}) {
		t.Fatal("空 And 求值应为 false")
	}
	if (Or{}).Eval(Input{}) {
		t.Fatal("空 Or 求值应为 false")
	}
}

func TestConstPredicate(t *testing.T) {
	in := NewInput("sh600000", makeKlines(10), nil, nil)
	if !Const(true).Eval(in) {
		t.Fatal("Const(true) 应恒真")
	}
	if Const(false).Eval(in) {
		t.Fatal("Const(false) 应恒假")
	}
	if got := Const(true).Name(); got != "true" {
		t.Fatalf("Name = %q, 期望 true", got)
	}
}

func TestLogicNames(t *testing.T) {
	a, err := NewAnd(Const(true), Const(false))
	if err != nil {
		t.Fatalf("构造失败: %v", err)
	}
	if got := a.Name(); got != "[true & false]" {
		t.Fatalf("And Name = %q", got)
	}
	o, err := NewOr(Const(true), Const(false))
	if err != nil {
		t.Fatalf("构造失败: %v", err)
	}
	if got := o.Name(); got != "[true | false]" {
		t.Fatalf("Or Name = %q", got)
	}
}

func TestRulesWithEmptyPrefixDoNotPanic(t *testing.T) {
	// 空前缀：操作数取不到值返回无效，所有规则不触发、不 panic。
	in := NewInput("sh600000", nil, nil, nil)
	if in.AsOf().IsZero() != true {
		t.Fatal("空前缀 AsOf 应为零值")
	}
	cmp, err := NewCompare(closeOperand{}, Gt, Literal(PriceValue(0.1)))
	if err != nil {
		t.Fatalf("构造失败: %v", err)
	}
	if cmp.Eval(in) {
		t.Fatal("空前缀比较不得触发")
	}
	rng, err := NewRange(closeOperand{}, mv(PriceValue(0.001)), nil, true, false)
	if err != nil {
		t.Fatalf("构造失败: %v", err)
	}
	if rng.Eval(in) {
		t.Fatal("空前缀区间不得触发")
	}
	and, err := NewAnd(cmp, Const(true))
	if err != nil {
		t.Fatalf("构造失败: %v", err)
	}
	if and.Eval(in) {
		t.Fatal("空前缀组合不得触发")
	}
}
