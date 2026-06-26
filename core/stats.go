package core

import "math"

// TradeStats 是一组交易的统计指标。
// ProfitFactor 按"收益率"口径计算（百分比盈亏比），适合等手数仓位。
// 公式：Σ盈利单收益率 / |Σ亏损单收益率|，避免高价股亏损让金额盈亏比失真。
type TradeStats struct {
	Total        int     // 交易笔数
	Win          int     // 盈利笔数
	Loss         int     // 亏损笔数（不含打平）
	WinRate      float64 // 胜率（%）
	WinSum       float64 // 累计盈利收益率（%）
	LossSum      float64 // 累计亏损收益率绝对值（%）
	ProfitFactor float64 // 盈亏比；无亏损且有盈利时为 +Inf
}

// Stats 按"收益率"口径汇总一组交易的胜率和盈亏比。
// 该函数是回测、选股、前端面板共用的统一统计入口。
// 单笔收益率 = (Sell - Buy) / Buy。
func Stats(trades []Trade) TradeStats {
	s := TradeStats{Total: len(trades)}
	if s.Total == 0 {
		return s
	}

	for _, t := range trades {
		buy := t.BuyPrice.Float64()
		if buy <= 0 {
			continue
		}
		rate := (t.SellPrice.Float64() - buy) / buy * 100
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
	}
	switch {
	case s.LossSum > 0:
		s.ProfitFactor = s.WinSum / s.LossSum
	case s.WinSum > 0:
		s.ProfitFactor = math.Inf(1)
	}
	return s
}
