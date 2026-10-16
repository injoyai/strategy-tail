package sell

import (
	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/lib/extend"
	"github.com/injoyai/strategy-tail/strategies/buy"
)

// A因子过滤 复用买入因子条件作为策略卖出信号；持仓风控仍由独立 Seller 负责。
type A因子过滤 struct {
	buy.A因子过滤
}

func (s A因子过滤) Sell(code string, dks extend.Klines, entry core.Buy) bool {
	return s.Buy(code, dks)
}
