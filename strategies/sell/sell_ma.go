package sell

import (
	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/lib/extend"
	"github.com/injoyai/strategy-tail/strategies/rule"
)

// LongMADown 是 250 日或 60 日均线不再同时向上时卖出的策略。
// Lookback 表示连续向上比较的交易日数量，默认 5。
// 当 250 日均线或 60 日均线在最近 Lookback 个交易日中出现走平或走弱时返回卖出信号。
// K 线数量不足（少于 250+Lookback 根）时无法判断均线趋势，不触发卖出。
// 该策略与 buy.LongMAUp 对应，用于在中长期趋势过滤条件失效时退出持仓。
type LongMADown struct {
	Lookback int
}

func (s LongMADown) Name() string {
	return "250/60日均线转弱"
}

func (s LongMADown) Sell(code string, dks extend.Klines, buy core.Buy) bool {
	lb := s.Lookback
	if lb == 0 {
		lb = 5
	}
	// 旧 maUp 对 lookback<=0 直接返回 false，取反后两路恒真（预热拦截仍
	// 生效）；内核 MAIncre 对负 lookback 返回无效值不触发，为保持零行为
	// 差异，该目录外退化分支留在门面。
	if lb < 0 {
		return len(dks) >= 250+lb
	}
	// 判断委托共用内核，与旧实现同序：先预热拦截（len<250+lookback，避免
	// 250 日侧"无法判断"被取反误判为转弱导致恒卖），再"250 日或 60 日最弱
	// 日增量 <=0"任一成立（旧 maUp 为增量>0，取反等价 <=0；预热拦截后两个
	// 增量均有有限值，无效值分支不可达）。求值顺序保持旧 250 侧在先。
	gate, err := rule.NewMinKlines(250 + lb)
	if err != nil {
		return false
	}
	weak250, err := rule.NewCompare(rule.MAIncre(250, lb), rule.Le, rule.Literal(rule.FloatValue(0)))
	if err != nil {
		return false
	}
	weak60, err := rule.NewCompare(rule.MAIncre(60, lb), rule.Le, rule.Literal(rule.FloatValue(0)))
	if err != nil {
		return false
	}
	any, err := rule.NewOr(weak250, weak60)
	if err != nil {
		return false
	}
	and, err := rule.NewAnd(gate, any)
	if err != nil {
		return false
	}
	return and.Eval(rule.NewInput(code, dks, nil, &buy))
}
