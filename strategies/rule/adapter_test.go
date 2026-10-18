package rule

import (
	"testing"
	"time"

	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/lib/extend"
	"github.com/injoyai/tdx/protocol"
)

func TestInputAsOfDerivedFromSnapshot(t *testing.T) {
	// AsOf 从可见快照派生（末根 Time），不使用系统时间；
	// 截断前缀即回退 AsOf，为 T3 历史引用预留同一语义。
	ks := makeKlines(10, 11, 12)
	in := NewInput("sh600000", ks, nil, nil)
	if !in.AsOf().Equal(ks[len(ks)-1].Time) {
		t.Fatalf("AsOf = %v, 期望末根时间 %v", in.AsOf(), ks[len(ks)-1].Time)
	}
	past := NewInput("sh600000", ks[:2], nil, nil)
	if !past.AsOf().Equal(ks[1].Time) {
		t.Fatalf("截断前缀 AsOf = %v, 期望 %v", past.AsOf(), ks[1].Time)
	}
	if !NewInput("sh600000", nil, nil, nil).AsOf().IsZero() {
		t.Fatal("空前缀 AsOf 应为零值时间")
	}
}

func TestInputFactorContextPassthrough(t *testing.T) {
	ks := makeKlines(10, 11)
	view := stubView{}
	in := NewInput("sz000001", ks, view, nil)
	fc := in.FactorContext()
	if fc.Code != "sz000001" {
		t.Fatalf("FactorContext.Code = %q", fc.Code)
	}
	if !fc.AsOf.Equal(ks[len(ks)-1].Time) {
		t.Fatalf("FactorContext.AsOf = %v, 期望末根时间", fc.AsOf)
	}
	if len(fc.Klines) != len(ks) {
		t.Fatalf("FactorContext.Klines 长度 = %d, 期望 %d", len(fc.Klines), len(ks))
	}
	if fc.Data != view {
		t.Fatal("FactorContext.Data 应原样传入 PIT 视图")
	}
}

func TestBuyerAdapterPositionUnavailable(t *testing.T) {
	// 买入侧求值遇到需要持仓的原语必须返回"不可用"，不得伪造买入价/时间。
	probe := &positionProbe{}
	adapter, err := NewBuyerAdapter("测试买入", probe, nil)
	if err != nil {
		t.Fatalf("构造失败: %v", err)
	}
	adapter.Buy("sh600000", makeKlines(10, 11))
	if probe.available {
		t.Fatal("买入侧持仓应不可用")
	}
	if probe.buy != nil {
		t.Fatalf("买入侧不得出现伪造持仓: %+v", probe.buy)
	}
	// 需要持仓的规则在买入侧一律不触发。
	buyer, err := NewBuyerAdapter("需持仓买入", requirePositionRule{}, nil)
	if err != nil {
		t.Fatalf("构造失败: %v", err)
	}
	if buyer.Buy("sh600000", makeKlines(10)) {
		t.Fatal("买入侧无持仓，需持仓规则不得触发")
	}
}

// requirePositionRule 需要持仓才成立的伪规则：持仓不可用即不触发。
type requirePositionRule struct{}

func (requirePositionRule) Name() string { return "需持仓" }

func (requirePositionRule) Eval(in Input) bool {
	_, ok := in.Position()
	return ok
}

func TestSellerAdapterPassesBuyToRule(t *testing.T) {
	probe := &positionProbe{}
	adapter, err := NewSellerAdapter("测试卖出", probe, nil)
	if err != nil {
		t.Fatalf("构造失败: %v", err)
	}
	buyTime := time.Date(2026, 10, 10, 9, 31, 0, 0, time.UTC)
	buy := core.Buy{
		Code:  "sh600000",
		Time:  buyTime,
		Price: protocol.Yuan(12.34),
		Rise:  0.5,
	}
	if !adapter.Sell("sh600000", makeKlines(10, 11, 12), buy) {
		t.Fatal("探针规则应恒真")
	}
	if !probe.available {
		t.Fatal("卖出侧持仓应可用")
	}
	if probe.buy == nil {
		t.Fatal("卖出侧应传入持仓")
	}
	if probe.buy.Code != buy.Code || probe.buy.Price != buy.Price || probe.buy.Rise != buy.Rise {
		t.Fatalf("持仓字段不一致: got %+v want %+v", probe.buy, &buy)
	}
	if !probe.buy.Time.Equal(buy.Time) {
		t.Fatalf("持仓时间不一致: got %v want %v", probe.buy.Time, buy.Time)
	}
	// 需要持仓的规则在卖出侧应触发。
	seller, err := NewSellerAdapter("需持仓卖出", requirePositionRule{}, nil)
	if err != nil {
		t.Fatalf("构造失败: %v", err)
	}
	if !seller.Sell("sh600000", makeKlines(10), buy) {
		t.Fatal("卖出侧有持仓，需持仓规则应触发")
	}
}

func TestAdapterInjectsDataView(t *testing.T) {
	view := stubView{}
	probe := &positionProbe{}
	buyer, err := NewBuyerAdapter("带视图买入", probe, view)
	if err != nil {
		t.Fatalf("构造失败: %v", err)
	}
	buyer.Buy("sh600000", makeKlines(10))
	if probe.data != view {
		t.Fatal("买入求值输入应携带注入的 PIT 视图")
	}
	seller, err := NewSellerAdapter("带视图卖出", probe, view)
	if err != nil {
		t.Fatalf("构造失败: %v", err)
	}
	seller.Sell("sh600000", makeKlines(10), core.Buy{Code: "sh600000"})
	if probe.data != view {
		t.Fatal("卖出求值输入应携带注入的 PIT 视图")
	}
}

func TestAdapterNameContract(t *testing.T) {
	rule, err := NewCompare(closeOperand{}, Gt, Literal(PriceValue(100)))
	if err != nil {
		t.Fatalf("构造失败: %v", err)
	}
	buyer, err := NewBuyerAdapter("A价格2-120元", rule, nil)
	if err != nil {
		t.Fatalf("构造失败: %v", err)
	}
	if buyer.Name() != "A价格2-120元" {
		t.Fatalf("Buyer Name = %q, 应为构造参数合同名", buyer.Name())
	}
	seller, err := NewSellerAdapter("A止盈止损0.1/0.05", rule, nil)
	if err != nil {
		t.Fatalf("构造失败: %v", err)
	}
	if seller.Name() != "A止盈止损0.1/0.05" {
		t.Fatalf("Seller Name = %q, 应为构造参数合同名", seller.Name())
	}
	if _, err := NewBuyerAdapter("", rule, nil); err == nil {
		t.Fatal("空名称应拒绝构造")
	}
	if _, err := NewBuyerAdapter("x", nil, nil); err == nil {
		t.Fatal("nil 规则应拒绝构造")
	}
	if _, err := NewSellerAdapter("", rule, nil); err == nil {
		t.Fatal("空名称应拒绝构造")
	}
	if _, err := NewSellerAdapter("x", nil, nil); err == nil {
		t.Fatal("nil 规则应拒绝构造")
	}
}

func TestSameRuleInstanceIsolatedAcrossCodesAndPositions(t *testing.T) {
	// 同一规则实例跨不同代码、持仓与输入求值相互隔离：无跨调用状态。
	rule, err := NewCompare(closeOperand{}, Gt, Literal(PriceValue(protocol.Yuan(11))))
	if err != nil {
		t.Fatalf("构造失败: %v", err)
	}
	buyer, err := NewBuyerAdapter("隔离测试", rule, nil)
	if err != nil {
		t.Fatalf("构造失败: %v", err)
	}
	seller, err := NewSellerAdapter("隔离测试", rule, nil)
	if err != nil {
		t.Fatalf("构造失败: %v", err)
	}
	buy := core.Buy{Code: "sh600000", Time: time.Unix(0, 0).UTC(), Price: protocol.Yuan(9)}
	ksHigh := makeKlines(10, 11, 12)  // 末收 12 元 > 11 触发
	ksLow := makeKlines(10, 11, 10.5) // 末收 10.5 元 不触发
	type step struct {
		call func() bool
		want bool
	}
	steps := []step{
		{func() bool { return buyer.Buy("sh600000", ksHigh) }, true},
		{func() bool { return buyer.Buy("sz000001", ksLow) }, false},
		{func() bool { return seller.Sell("sh600000", ksHigh, buy) }, true},
		{func() bool { return seller.Sell("sz000001", ksLow, buy) }, false},
		{func() bool { return buyer.Buy("bj430000", ksHigh) }, true},
		{func() bool { return buyer.Buy("sh600000", ksLow) }, false},
	}
	for i, s := range steps {
		if got := s.call(); got != s.want {
			t.Fatalf("第 %d 步 = %v, 期望 %v（跨调用不得互相污染）", i+1, got, s.want)
		}
	}
	// 输入 K 线不得被求值修改：长度与末根内容在多次求值后保持原样。
	checkImmutable := func(ks extend.Klines) {
		t.Helper()
		beforeLen := len(ks)
		beforeClose := ks[len(ks)-1].Close
		beforeTime := ks[len(ks)-1].Time
		_ = buyer.Buy("sh600000", ks)
		_ = seller.Sell("sh600000", ks, buy)
		if len(ks) != beforeLen || ks[len(ks)-1].Close != beforeClose ||
			!ks[len(ks)-1].Time.Equal(beforeTime) {
			t.Fatal("求值不得修改输入 K 线")
		}
	}
	checkImmutable(ksHigh)
	checkImmutable(ksLow)
}

func TestAdaptersSatisfyCoreInterfaces(t *testing.T) {
	// 编译期断言已在 adapter.go 声明，这里补充运行时确认。
	var buyer core.Buyer = &BuyerAdapter{name: "b", rule: Const(true)}
	var seller core.Seller = &SellerAdapter{name: "s", rule: Const(true)}
	if buyer.Name() != "b" || seller.Name() != "s" {
		t.Fatal("接口断言失败")
	}
	if !buyer.Buy("sh600000", makeKlines(10)) || !seller.Sell("sh600000", makeKlines(10), core.Buy{}) {
		t.Fatal("适配器应转发规则求值")
	}
}
