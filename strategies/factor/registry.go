package factor

import (
	"github.com/injoyai/strategy-tail/core"
)

// CatalogEntry 因子目录项，json 字段供 GET /api/factors 直出。
type CatalogEntry struct {
	Kind        string `json:"kind"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

type entry struct {
	Kind        string
	Default     int
	New         func(days int) core.Factor
	Description string
}

// registry 全部因子目录（显式列表，不用反射：Yaegi 与 IDE 跳转友好）。
var registry = []entry{
	{Kind: "momentum", Default: 20, New: func(d int) core.Factor { return &N日动量{Days: d} }, Description: "近N日涨跌幅"},
	{Kind: "ma_bias", Default: 20, New: func(d int) core.Factor { return &均线偏离{Days: d} }, Description: "收盘价相对N日均线的偏离率"},
	{Kind: "slope", Default: 20, New: func(d int) core.Factor { return &N日斜率{Days: d} }, Description: "近N日收盘线性回归斜率的相对值"},
	{Kind: "volatility", Default: 20, New: func(d int) core.Factor { return &N日波动{Days: d} }, Description: "近N日收益率总体标准差"},
	{Kind: "amplitude", Default: 20, New: func(d int) core.Factor { return &N日振幅{Days: d} }, Description: "近N日最高最低区间占比"},
	{Kind: "volume_ratio", Default: 5, New: func(d int) core.Factor { return &量比{Days: d} }, Description: "今日成交量相对前N日均量的倍数"},
	{Kind: "volume_pct", Default: 60, New: func(d int) core.Factor { return &量分位{Days: d} }, Description: "今量在近N日中的分位"},
	{Kind: "volume_surge", Default: 20, New: func(d int) core.Factor { return &放量占比{Days: d} }, Description: "近N日放量天数占比"},
	{Kind: "body", Default: 1, New: func(int) core.Factor { return &实体幅度{} }, Description: "K线实体占比"},
	{Kind: "upper_shadow", Default: 1, New: func(int) core.Factor { return &上影占比{} }, Description: "上影线占全幅比例"},
	{Kind: "lower_shadow", Default: 1, New: func(int) core.Factor { return &下影占比{} }, Description: "下影线占全幅比例"},
	{Kind: "position", Default: 60, New: func(d int) core.Factor { return &N日高低位{Days: d} }, Description: "收盘价在N日区间中的位置"},
	{Kind: "kvalue", Default: 9, New: func(d int) core.Factor { return &K值{Days: d} }, Description: "KDJ K线"},
	{Kind: "vp_corr", Default: 20, New: func(d int) core.Factor { return &量价相关{Days: d} }, Description: "近N日量价Pearson相关系数"},
}

// All 返回全部因子目录。
func All() []CatalogEntry {
	out := make([]CatalogEntry, 0, len(registry))
	for _, e := range registry {
		out = append(out, CatalogEntry{
			Kind:        e.Kind,
			Name:        e.New(e.Default).Name(),
			Description: e.Description,
		})
	}
	return out
}

// Build 按 kind 构造因子：kind 未知返回 nil；days<=0 使用该因子默认参数。
func Build(kind string, days int) core.Factor {
	for _, e := range registry {
		if e.Kind != kind {
			continue
		}
		if days <= 0 {
			days = e.Default
		}
		return e.New(days)
	}
	return nil
}
