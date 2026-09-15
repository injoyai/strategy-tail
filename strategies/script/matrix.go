//go:build ignore

// 策略实验室脚本（页面编辑器直接读写本文件，服务端 Yaegi 解释执行）。
//
// 契约：package main + func Strategy() []core.Variant
// 可 import：core、strategies/buy、strategies/sell（见 internal/lab/symbols.go 注册的组件）。
// 重逻辑请组合现有组件，内联循环会被解释执行变慢。
package main

import (
	"github.com/injoyai/strategy-tail/core"
	sb "github.com/injoyai/strategy-tail/strategies/buy"
)

// Strategy 返回待对比的策略变体列表（名称不可重复）。
func Strategy() []core.Variant {
	return []core.Variant{
		{Name: "MA5向上·收回MA5", Buyer: sb.And{
			sb.A流通市值{Min: 20},
			sb.A价格{Min: 2, Max: 120},
			sb.A过滤涨停{},
			sb.A阴线收回{SupportPeriod: 5, MinBodyRatio: 0.3, MaxRise: 1.0,
				Trend: sb.MAUp{Period: 5}},
		}},
		{Name: "MA5向上·收回MA10", Buyer: sb.And{
			sb.A流通市值{Min: 20},
			sb.A价格{Min: 2, Max: 120},
			sb.A过滤涨停{},
			sb.A阴线收回{SupportPeriod: 10, MinBodyRatio: 0.3, MaxRise: 1.0,
				Trend: sb.MAUp{Period: 5}},
		}},
		{Name: "多头排列·收回MA5", Buyer: sb.And{
			sb.A流通市值{Min: 20},
			sb.A价格{Min: 2, Max: 120},
			sb.A过滤涨停{},
			sb.A阴线收回{SupportPeriod: 5, MinBodyRatio: 0.3, MaxRise: 1.0,
				Trend: sb.A均线多头排列{5, 10, 20}},
		}},
		{Name: "无趋势·收回MA5", Buyer: sb.And{
			sb.A流通市值{Min: 20},
			sb.A价格{Min: 2, Max: 120},
			sb.A过滤涨停{},
			sb.A阴线收回{SupportPeriod: 5, MinBodyRatio: 0.3, MaxRise: 1.0},
		}},
	}
}
