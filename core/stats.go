package core

import "math"

// TradeStats 是一组交易的统计指标。
// ProfitFactor 按"收益率"口径计算（百分比盈亏比），适合等手数仓位。
// 公式：Σ盈利单收益率 / |Σ亏损单收益率|，避免高价股亏损让金额盈亏比失真。
// AvgProfit/MaxProfit/MaxLoss 均为百分比口径（%），与 WinRate/ProfitFactor 一致。
type TradeStats struct {
	Total        int     // 交易笔数
	Win          int     // 盈利笔数
	Loss         int     // 亏损笔数（不含打平）
	WinRate      float64 // 胜率（%）
	WinSum       float64 // 累计盈利收益率（%）
	LossSum      float64 // 累计亏损收益率绝对值（%）
	ProfitFactor float64 // 盈亏比；无亏损且有盈利时为 +Inf
	AvgProfit    float64 // 平均收益率（%）
	MaxProfit    float64 // 最大单笔收益率（%）
	MaxLoss      float64 // 最小单笔收益率（%，负数）
}

// Stats 按"收益率"口径汇总一组交易的胜率和盈亏比。
// 该函数是回测、选股、前端面板共用的统一统计入口。
// 单笔收益率 = (SellPrice - BuyPrice) / BuyPrice × 100。
// BuyPrice/SellPrice 为含滑点和手续费的成交价（与原版一致）。
func Stats(trades []Trade) TradeStats {
	s := TradeStats{Total: len(trades)}
	if s.Total == 0 {
		return s
	}

	var profitSum float64
	for _, t := range trades {
		rate := tradeReturnRate(t)
		profitSum += rate

		if rate > s.MaxProfit {
			s.MaxProfit = rate
		}
		if rate < s.MaxLoss {
			s.MaxLoss = rate
		}

		switch {
		case rate > 0:
			s.Win++
			s.WinSum += rate
		case rate < 0:
			s.Loss++
			s.LossSum += -rate
		}
	}

	if s.Total > 0 {
		s.WinRate = float64(s.Win) / float64(s.Total) * 100
		s.AvgProfit = profitSum / float64(s.Total)
	}
	switch {
	case s.LossSum > 0:
		s.ProfitFactor = s.WinSum / s.LossSum
	case s.WinSum > 0:
		s.ProfitFactor = math.Inf(1)
	}
	return s
}

// tradeReturnRate 计算单笔交易收益率（%）。
// 与原版一致：用 (SellPrice - BuyPrice) / BuyPrice 口径。
// BuyPrice/SellPrice 为含滑点和手续费的成交价。
func tradeReturnRate(t Trade) float64 {
	buy := t.BuyPrice.Float64()
	if buy <= 0 {
		return 0
	}
	return (t.SellPrice.Float64() - buy) / buy * 100
}
