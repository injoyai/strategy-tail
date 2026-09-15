package buy

import (
	"fmt"

	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/lib/extend"
)

// A阴线收回 是上升趋势中的"阴线洗盘收回"买入条件。
//
// 形态：股价处于上升趋势，当天收出有实体的阴线（盘中跌破趋势），但尾盘又站回关键均线。
// 博弈点：上升趋势中的单日阴线多为洗盘，尾盘收回说明支撑有效，博取次日反弹。
//
// 触发条件（全部基于当天日线，可由分钟线实时判定）：
//  1. 当天收阴：收盘价 < 开盘价，且实体达到 MinBodyRatio（占振幅比例，过滤十字星）
//  2. 当天盘中曾跌破 MA(SupportPeriod)：最低价 < 当天 MA(SupportPeriod)（确实发生了回踩）
//  3. 尾盘收回：收盘价 ≥ 当天 MA(SupportPeriod)（收回关键均线）
//  4. 当天涨幅不超过 MaxRise（阴线默认涨幅为负，此项兼容收平/微涨的假阴线形态）
//
// 趋势条件（可选，由 Trend 字段控制，nil 表示不校验）：
//   - 常用 buy.MAUp{Period: 5}（5日均线向上）
//   - 常用 buy.A均线多头排列{5, 10, 20}
type A阴线收回 struct {
	SupportPeriod int        // 支撑均线周期，默认 5（MA5）
	MinBodyRatio  float64    // 阴线实体占振幅最小比例，默认 0.3，<=0 表示只要收阴即可
	MaxRise       float64    // 当天最大涨幅%（小数），默认 1.0，<0 表示不限制
	Trend         core.Buyer // 趋势过滤（可选），nil 表示不校验
}

func (s A阴线收回) Name() string {
	period := s.supportPeriod()
	trend := "无趋势过滤"
	if s.Trend != nil {
		trend = s.Trend.Name()
	}
	return fmt.Sprintf("阴线收回MA%d(%s)", period, trend)
}

func (s A阴线收回) supportPeriod() int {
	if s.SupportPeriod <= 0 {
		return 5
	}
	return s.SupportPeriod
}

func (s A阴线收回) Buy(code string, dks extend.Klines) bool {
	period := s.supportPeriod()
	if len(dks) < period+1 {
		return false
	}

	today := dks[len(dks)-1]
	open := today.Open.Float64()
	close := today.Close.Float64()
	low := today.Low.Float64()

	// 条件1：当天收阴且有实体
	if close >= open {
		return false
	}
	if s.MinBodyRatio > 0 {
		rangeVal := today.High.Float64() - low
		if rangeVal <= 0 || (open-close)/rangeVal < s.MinBodyRatio {
			return false
		}
	}

	// 条件2/3：盘中跌破支撑均线，收盘收回
	ma := core.MA(dks, period)
	if low >= ma || close < ma {
		return false
	}

	// 条件4：当天涨幅不超过上限（默认 1%，兼容假阴线，排除大阳线误判）
	if s.MaxRise >= 0 {
		maxRise := s.MaxRise
		if maxRise == 0 {
			maxRise = 1.0
		}
		if today.RiseRate() > maxRise {
			return false
		}
	}

	// 趋势过滤（可选）
	if s.Trend != nil && !s.Trend.Buy(code, dks) {
		return false
	}

	return true
}
