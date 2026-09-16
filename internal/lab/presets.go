package lab

import (
	"fmt"

	"github.com/injoyai/strategy-tail/core"
	sb "github.com/injoyai/strategy-tail/strategies/buy"
)

// presets.go 简单模式的预设 Buyer 目录。
//
// 显式 slice + 工厂函数，不用反射或字符串动态装配；Buyer 仍由
// buy.And 与现有原子 Buyer 构成，参数与 strategies/script/matrix.go
// 的四个脚本 Buyer 逐项一致（TestPresetBuildersMatchMatrix 锁定）。
// matrix.go 仍是高级模式脚本的单一事实源，两边改动须同步。

// StrategyPreset 显式注册的基础策略预设。
type StrategyPreset struct {
	ID          string            // 稳定 ID（API 与 StrategySpec 引用）
	Name        string            // 与 matrix.go 中变体同名
	Description string            // 一句话概念说明
	Rules       []string          // 逐条描述真实参数，不虚构业务规则
	Build       func() core.Buyer // 每次调用返回新 Buyer
}

// strategyPresets 首版冻结目录；slice 顺序即 API 返回顺序。
var strategyPresets = []StrategyPreset{
	{
		ID:          "pullback_ma5_up",
		Name:        "MA5 向上 · 收回 MA5",
		Description: "回调到 5 日均线附近且短期趋势向上",
		Rules: []string{
			"流通市值 ≥ 20",
			"价格 2～120",
			"过滤涨停",
			"MA5 向上",
			"阴线收回 MA5",
		},
		Build: func() core.Buyer {
			return sb.And{
				sb.A流通市值{Min: 20},
				sb.A价格{Min: 2, Max: 120},
				sb.A过滤涨停{},
				sb.A阴线收回{SupportPeriod: 5, MinBodyRatio: 0.3, MaxRise: 1.0,
					Trend: sb.MAUp{Period: 5}},
			}
		},
	},
	{
		ID:          "pullback_ma10_up",
		Name:        "MA5 向上 · 收回 MA10",
		Description: "回调到 10 日均线附近且短期趋势向上",
		Rules: []string{
			"流通市值 ≥ 20",
			"价格 2～120",
			"过滤涨停",
			"MA5 向上",
			"阴线收回 MA10",
		},
		Build: func() core.Buyer {
			return sb.And{
				sb.A流通市值{Min: 20},
				sb.A价格{Min: 2, Max: 120},
				sb.A过滤涨停{},
				sb.A阴线收回{SupportPeriod: 10, MinBodyRatio: 0.3, MaxRise: 1.0,
					Trend: sb.MAUp{Period: 5}},
			}
		},
	},
	{
		ID:          "pullback_ma5_bull",
		Name:        "多头排列 · 收回 MA5",
		Description: "短中长均线多头排列下回调到 5 日均线附近",
		Rules: []string{
			"流通市值 ≥ 20",
			"价格 2～120",
			"过滤涨停",
			"MA5 > MA10 > MA20",
			"阴线收回 MA5",
		},
		Build: func() core.Buyer {
			return sb.And{
				sb.A流通市值{Min: 20},
				sb.A价格{Min: 2, Max: 120},
				sb.A过滤涨停{},
				sb.A阴线收回{SupportPeriod: 5, MinBodyRatio: 0.3, MaxRise: 1.0,
					Trend: sb.A均线多头排列{5, 10, 20}},
			}
		},
	},
	{
		ID:          "pullback_ma5_plain",
		Name:        "无趋势 · 收回 MA5",
		Description: "阴线回踩 5 日均线后尾盘收回，不校验趋势方向",
		Rules: []string{
			"流通市值 ≥ 20",
			"价格 2～120",
			"过滤涨停",
			"无趋势约束",
			"阴线收回 MA5",
		},
		Build: func() core.Buyer {
			return sb.And{
				sb.A流通市值{Min: 20},
				sb.A价格{Min: 2, Max: 120},
				sb.A过滤涨停{},
				sb.A阴线收回{SupportPeriod: 5, MinBodyRatio: 0.3, MaxRise: 1.0},
			}
		},
	},
}

// StrategyPresetIDs 返回目录 ID（顺序稳定）。
func StrategyPresetIDs() []string {
	ids := make([]string, 0, len(strategyPresets))
	for _, p := range strategyPresets {
		ids = append(ids, p.ID)
	}
	return ids
}

// BuildPresetBuyer 按 ID 构建 Buyer；未知 ID 返回错误。
func BuildPresetBuyer(id string) (core.Buyer, error) {
	for _, p := range strategyPresets {
		if p.ID == id {
			return p.Build(), nil
		}
	}
	return nil, fmt.Errorf("未知预设策略: %s", id)
}

// PresetInfo 预设策略 API 展示 DTO（不含 Go 类型名、函数或源码）。
type PresetInfo struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Rules       []string `json:"rules"`
}

// PresetInfos 目录的 API 展示形式。
func PresetInfos() []PresetInfo {
	out := make([]PresetInfo, 0, len(strategyPresets))
	for _, p := range strategyPresets {
		out = append(out, PresetInfo{
			ID:          p.ID,
			Name:        p.Name,
			Description: p.Description,
			Rules:       append([]string(nil), p.Rules...),
		})
	}
	return out
}
