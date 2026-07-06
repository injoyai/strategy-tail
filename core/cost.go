package core

import (
	"github.com/injoyai/tdx/protocol"
)

// ============================================================================
// 成本计算模型
// ============================================================================

// BuyCost 计算买入总支出（元）。
// 买入支出 = 成交价 × 数量 + 佣金 + 过户费
// 成交价 = 原始价 + 滑点（买入向上滑点）
func (c Cost) BuyCost(rawPrice protocol.Price, quantity int) (execPrice protocol.Price, totalCost float64) {
	execPrice = rawPrice + c.Slippage
	turnover := execPrice.Float64() * float64(quantity)

	commission := turnover * c.CommissionRate
	if c.MinCommission > 0 && commission < c.MinCommission {
		commission = c.MinCommission
	}

	transferFee := turnover * c.TransferFeeRate

	totalCost = turnover + commission + transferFee
	return
}

// SellIncome 计算卖出总收入（元）。
// 卖出收入 = 成交价 × 数量 - 佣金 - 印花税 - 过户费
// 成交价 = 原始价 - 滑点（卖出向下滑点）
func (c Cost) SellIncome(rawPrice protocol.Price, quantity int) (execPrice protocol.Price, netIncome float64) {
	execPrice = rawPrice - c.Slippage
	turnover := execPrice.Float64() * float64(quantity)

	commission := turnover * c.CommissionRate
	if c.MinCommission > 0 && commission < c.MinCommission {
		commission = c.MinCommission
	}

	stampDuty := turnover * c.StampDutyRate
	transferFee := turnover * c.TransferFeeRate

	netIncome = turnover - commission - stampDuty - transferFee
	return
}

// TransferFeeForCode 根据代码判断是否收取过户费。
// 沪市（sh688/sh60）收取过户费，深市不收。
func TransferFeeForCode(code string) bool {
	pref := protocol.AddPrefix(code)
	if len(pref) < 4 {
		return false
	}
	// 沪市股票：sh60 开头（主板）、sh688 开头（科创板）
	return pref[:2] == "sh" && (pref[2:4] == "60" || pref[2:4] == "68")
}
