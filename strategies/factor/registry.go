package factor

import (
	"github.com/injoyai/strategy-tail/core"
)

// CatalogEntry 因子目录项，json 字段供 GET /api/factors 直出。
type CatalogEntry struct {
	Kind           string `json:"kind"`
	Name           string `json:"name"`
	Description    string `json:"description"`
	Category       string `json:"category"`
	ParameterLabel string `json:"parameterLabel"`
	DefaultDays    int    `json:"defaultDays"`
	Unit           string `json:"unit"` // ratio | multiple | score | correlation
	Example        string `json:"example"`
}

type entry struct {
	Kind           string
	Default        int
	New            func(days int) core.Factor
	Description    string
	Category       string
	ParameterLabel string
	Unit           string
	Example        string
}

// registry 全部因子目录（显式列表，不用反射：Yaegi 与 IDE 跳转友好）。
// Default 是默认窗口的唯一来源：Build(kind,0) 与目录 DefaultDays 均取自该字段。
// Example 只解释原始值含义，不提供阈值推荐、评级或经验收益。
var registry = []entry{
	{
		Kind: "momentum", Default: 20,
		New:            func(d int) core.Factor { return &N日动量{Days: d} },
		Description:    "近N日涨跌幅",
		Category:       "趋势与动量",
		ParameterLabel: "回看天数",
		Unit:           "ratio",
		Example:        "原始值 0.05 表示近 N 日上涨 5%，-0.05 表示下跌 5%。",
	},
	{
		Kind: "ma_bias", Default: 20,
		New:            func(d int) core.Factor { return &均线偏离{Days: d} },
		Description:    "收盘价相对N日均线的偏离率",
		Category:       "趋势与动量",
		ParameterLabel: "均线天数",
		Unit:           "ratio",
		Example:        "原始值 0.03 表示收盘价高于 N 日均线约 3%，-0.03 表示低于约 3%。",
	},
	{
		Kind: "slope", Default: 20,
		New:            func(d int) core.Factor { return &N日斜率{Days: d} },
		Description:    "近N日收盘线性回归斜率的相对值",
		Category:       "趋势与动量",
		ParameterLabel: "回看天数",
		Unit:           "ratio",
		Example:        "原始值 0.01 表示近 N 日收盘价线性上行斜率约为窗口均价的 1%/日。",
	},
	{
		Kind: "volatility", Default: 20,
		New:            func(d int) core.Factor { return &N日波动{Days: d} },
		Description:    "近N日收益率总体标准差",
		Category:       "波动",
		ParameterLabel: "统计天数",
		Unit:           "ratio",
		Example:        "原始值 0.02 表示近 N 日日收益率标准差约 2%。",
	},
	{
		Kind: "amplitude", Default: 20,
		New:            func(d int) core.Factor { return &N日振幅{Days: d} },
		Description:    "近N日最高最低区间占比",
		Category:       "波动",
		ParameterLabel: "统计天数",
		Unit:           "ratio",
		Example:        "原始值 0.15 表示近 N 日最高价较期间最低价高约 15%。",
	},
	{
		Kind: "volume_ratio", Default: 5,
		New:            func(d int) core.Factor { return &量比{Days: d} },
		Description:    "今日成交量相对前N日均量的倍数",
		Category:       "量能",
		ParameterLabel: "均量天数",
		Unit:           "multiple",
		Example:        "原始值 2.5 表示今日成交量约为前 N 日均量的 2.5 倍。",
	},
	{
		Kind: "volume_pct", Default: 60,
		New:            func(d int) core.Factor { return &量分位{Days: d} },
		Description:    "今量在近N日中的分位",
		Category:       "量能",
		ParameterLabel: "统计窗口",
		Unit:           "ratio",
		Example:        "原始值 0.8 表示今量不低于近 N 日（含今日）中 80% 交易日的成交量。",
	},
	{
		Kind: "volume_surge", Default: 20,
		New:            func(d int) core.Factor { return &放量占比{Days: d} },
		Description:    "近N日放量天数占比",
		Category:       "量能",
		ParameterLabel: "统计窗口",
		Unit:           "ratio",
		Example:        "原始值 0.3 表示近 N 日（含今日）中约 30% 的交易日成交量超过窗口均量的 1.5 倍。",
	},
	{
		Kind: "body", Default: 1,
		New:            func(int) core.Factor { return &实体幅度{} },
		Description:    "K线实体占比",
		Category:       "K线形态",
		ParameterLabel: "无参数（单根K线）",
		Unit:           "ratio",
		Example:        "原始值 0.06 表示当日实体幅度约为开盘价的 6%。",
	},
	{
		Kind: "upper_shadow", Default: 1,
		New:            func(int) core.Factor { return &上影占比{} },
		Description:    "上影线占全幅比例",
		Category:       "K线形态",
		ParameterLabel: "无参数（单根K线）",
		Unit:           "ratio",
		Example:        "原始值 0.3 表示上影线约占当日高低全幅的 30%。",
	},
	{
		Kind: "lower_shadow", Default: 1,
		New:            func(int) core.Factor { return &下影占比{} },
		Description:    "下影线占全幅比例",
		Category:       "K线形态",
		ParameterLabel: "无参数（单根K线）",
		Unit:           "ratio",
		Example:        "原始值 0.3 表示下影线约占当日高低全幅的 30%。",
	},
	{
		Kind: "position", Default: 60,
		New:            func(d int) core.Factor { return &N日高低位{Days: d} },
		Description:    "收盘价在N日区间中的位置",
		Category:       "位置",
		ParameterLabel: "回看天数",
		Unit:           "ratio",
		Example:        "原始值 0.9 表示收盘价处于近 N 日高低区间的 90% 位置（0 为最低，1 为最高）。",
	},
	{
		Kind: "kvalue", Default: 9,
		New:            func(d int) core.Factor { return &K值{Days: d} },
		Description:    "KDJ K线",
		Category:       "位置",
		ParameterLabel: "计算周期",
		Unit:           "score",
		Example:        "原始值即 KDJ 的 K 值，取值 0～100，如 80 表示 K 值为 80。",
	},
	{
		Kind: "vp_corr", Default: 20,
		New:            func(d int) core.Factor { return &量价相关{Days: d} },
		Description:    "近N日量价Pearson相关系数",
		Category:       "相关性",
		ParameterLabel: "统计天数",
		Unit:           "correlation",
		Example:        "原始值 0.6 表示近 N 日量价正相关，0 为不相关，-0.6 为负相关。",
	},
}

// All 返回全部因子目录。
func All() []CatalogEntry {
	out := make([]CatalogEntry, 0, len(registry))
	for _, e := range registry {
		out = append(out, CatalogEntry{
			Kind:           e.Kind,
			Name:           e.New(e.Default).Name(),
			Description:    e.Description,
			Category:       e.Category,
			ParameterLabel: e.ParameterLabel,
			DefaultDays:    e.Default,
			Unit:           e.Unit,
			Example:        e.Example,
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
