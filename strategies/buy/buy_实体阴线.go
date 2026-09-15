package buy

import (
	"fmt"

	"github.com/injoyai/strategy-tail/lib/extend"
)

// A实体阴线 是"有实体的阴线"K 线形态买入条件。
//
// 形态：当天收阴（收盘价 < 开盘价），且阴线实体占当天振幅的比例达到
// MinBodyRatio，用于过滤上下影线过长的十字星式阴线。
// 常与 MACD连涨 组合，表达"量柱趋势向上时出现实体阴线洗盘"的博弈买点：
//
//	buy.And{ buy.MACD连涨{MinDays: 2}, buy.A实体阴线{MinBodyRatio: 0.5} }
//
// 触发条件：
//  1. 当天收阴：收盘价 < 开盘价；
//  2. 实体比例：(开盘价 - 收盘价) / (最高价 - 最低价) ≥ MinBodyRatio。
//     MinBodyRatio <= 0 表示只要收阴即可（不校验实体比例）。
type A实体阴线 struct {
	MinBodyRatio float64 // 阴线实体占振幅最小比例，<=0 表示只要收阴即可
}

func (s A实体阴线) Name() string {
	if s.MinBodyRatio > 0 {
		return fmt.Sprintf("实体阴线(实体≥%.0f%%)", s.MinBodyRatio*100)
	}
	return "实体阴线"
}

func (s A实体阴线) Buy(code string, dks extend.Klines) bool {
	if len(dks) == 0 {
		return false
	}

	today := dks[len(dks)-1]
	open := today.Open.Float64()
	close := today.Close.Float64()

	// 条件1：当天收阴
	if close >= open {
		return false
	}

	// 条件2：实体比例过滤十字星（脏数据下 high==low 时除零保护）
	if s.MinBodyRatio > 0 {
		rangeVal := today.High.Float64() - today.Low.Float64()
		if rangeVal <= 0 || (open-close)/rangeVal < s.MinBodyRatio {
			return false
		}
	}

	return true
}
