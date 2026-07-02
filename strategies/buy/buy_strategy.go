package buy

import (
	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/lib/extend"
)

// Strategy 是带名称的组合策略包装器。
// 用于给组合策略（如 And/Or）或单个策略定义一个有意义的名称，
// 便于在回测输出、诊断器中识别策略变体。
func Strategy(name string, buy core.Buyer) core.Buyer {
	return _strategy{
		name:  name,
		Buyer: buy,
	}
}

type _strategy struct {
	name  string
	Buyer core.Buyer
}

func (s _strategy) Name() string {
	return s.name
}

func (s _strategy) Buy(code string, dks extend.Klines) bool {
	if s.Buyer == nil {
		return false
	}
	return s.Buyer.Buy(code, dks)
}

func (s _strategy) Children() []core.Buyer {
	if s.Buyer == nil {
		return nil
	}
	if cb, ok := s.Buyer.(core.CompositeBuyer); ok {
		return cb.Children()
	}
	return nil
}
