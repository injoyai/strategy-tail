package rule

import (
	"fmt"

	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/lib/extend"
	"github.com/injoyai/strategy-tail/researchdata"
)

// BuyerAdapter 把 Rule 适配为 core.Buyer。名称由构造参数提供
// （旧门面传入自己的合同名，内核不生成泛化表达式字符串）。
// 每次 Buy 构造一次求值输入：买入侧不提供持仓信息（Buy 恒 nil），
// 需要持仓的原语按不可用处理；PIT 数据视图在构造时注入，可空。
// 适配器与规则均无状态，同一实例可跨代码、跨持仓复用。
type BuyerAdapter struct {
	name string
	rule Rule
	data researchdata.View
}

// NewBuyerAdapter 构造 BuyerAdapter；name 不能为空，rule 不能为 nil。
func NewBuyerAdapter(name string, rule Rule, data researchdata.View) (*BuyerAdapter, error) {
	if name == "" {
		return nil, fmt.Errorf("Buyer 适配器名称不能为空")
	}
	if rule == nil {
		return nil, fmt.Errorf("Buyer 适配器的规则不能为 nil")
	}
	return &BuyerAdapter{name: name, rule: rule, data: data}, nil
}

// Name 返回构造时提供的合同名称。
func (a *BuyerAdapter) Name() string { return a.name }

// Rule 返回内部规则（诊断/展开用），不得修改其求值行为。
func (a *BuyerAdapter) Rule() Rule { return a.rule }

// Buy 实现 core.Buyer：按本次可见 K 线前缀构造输入并求值。
func (a *BuyerAdapter) Buy(code string, dks extend.Klines) bool {
	return a.rule.Eval(NewInput(code, dks, a.data, nil))
}

// SellerAdapter 把 Rule 适配为 core.Seller。名称由构造参数提供；
// 每次 Sell 把 core.Buy 以只读指针传入求值输入，规则侧需要持仓的
// 原语经 Input.Position 获取；PIT 数据视图在构造时注入，可空。
type SellerAdapter struct {
	name string
	rule Rule
	data researchdata.View
}

// NewSellerAdapter 构造 SellerAdapter；name 不能为空，rule 不能为 nil。
func NewSellerAdapter(name string, rule Rule, data researchdata.View) (*SellerAdapter, error) {
	if name == "" {
		return nil, fmt.Errorf("Seller 适配器名称不能为空")
	}
	if rule == nil {
		return nil, fmt.Errorf("Seller 适配器的规则不能为 nil")
	}
	return &SellerAdapter{name: name, rule: rule, data: data}, nil
}

// Name 返回构造时提供的合同名称。
func (a *SellerAdapter) Name() string { return a.name }

// Rule 返回内部规则（诊断/展开用），不得修改其求值行为。
func (a *SellerAdapter) Rule() Rule { return a.rule }

// Sell 实现 core.Seller：按本次可见 K 线前缀与本次买入信息构造输入并求值。
// buy 是按值传入的当次副本，取地址仅用于只读传递，不产生跨调用状态。
func (a *SellerAdapter) Sell(code string, dks extend.Klines, buy core.Buy) bool {
	return a.rule.Eval(NewInput(code, dks, a.data, &buy))
}

// 编译期断言：两个适配器满足原 core 接口（计划 T1 验收）。
var (
	_ core.Buyer  = (*BuyerAdapter)(nil)
	_ core.Seller = (*SellerAdapter)(nil)
)
