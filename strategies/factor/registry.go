package factor

import (
	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/researchdata"
)

// CatalogEntry 因子目录项，json 字段供 GET /api/factors 直出。
type CatalogEntry struct {
	Kind           string `json:"kind"`
	Name           string `json:"name"`
	Description    string `json:"description"`
	Category       string `json:"category"`
	ParameterLabel string `json:"parameterLabel"`
	DefaultDays    int    `json:"defaultDays"`
	Unit           string `json:"unit"` // ratio | multiple | score | correlation | percent
	Example        string `json:"example"`
	// ImplementationVersion 因子实现版本：任何改变相同输入下数值输出的
	// 算法/缺失值/窗口语义变更都必须递增（见设计文档 §4.1）。候选保存与
	// 简单策略过滤的 fail-closed 校验以此为准，禁止依赖零值。
	ImplementationVersion int `json:"implementationVersion"`
}

type entry struct {
	Kind                  string
	Default               int
	New                   func(days int) core.Factor
	NewContext            func(days int) core.ContextFactor
	Description           string
	Category              string
	ParameterLabel        string
	Unit                  string
	Example               string
	ImplementationVersion int
}

// registry 显式登记所有因子，便于查找实现。
// Default 是默认窗口的唯一来源：Build(kind,0) 与目录 DefaultDays 均取自该字段。
// Example 只解释原始值含义，不提供阈值推荐、评级或经验收益。
var registry = []entry{
	{
		Kind: "momentum", Default: 20,
		New:                   func(d int) core.Factor { return &N日动量{Days: d} },
		Description:           "近N日涨跌幅",
		Category:              "趋势与动量",
		ParameterLabel:        "回看天数",
		Unit:                  "ratio",
		Example:               "原始值 0.05 表示近 N 日上涨 5%，-0.05 表示下跌 5%。",
		ImplementationVersion: 1,
	},
	{
		Kind: "skip_month_momentum", Default: 252,
		New:                   func(d int) core.Factor { return &跳过近月动量{Days: d} },
		Description:           "近N日累计收益率，固定跳过最近21个交易日",
		Category:              "趋势与动量",
		ParameterLabel:        "总回看天数（跳过近21日）",
		Unit:                  "ratio",
		Example:               "原始值 0.30 表示从 N 个交易日前到 21 个交易日前累计上涨约 30%。",
		ImplementationVersion: 1,
	},
	{
		Kind: "short_reversal", Default: 20,
		New:                   func(d int) core.Factor { return &N日短期反转{Days: d} },
		Description:           "近N日涨跌幅的相反数",
		Category:              "趋势与动量",
		ParameterLabel:        "回看天数",
		Unit:                  "ratio",
		Example:               "原始值 0.05 表示近 N 日下跌约 5%，-0.05 表示上涨约 5%。",
		ImplementationVersion: 1,
	},
	{
		Kind: "ma_bias", Default: 20,
		New:                   func(d int) core.Factor { return &均线偏离{Days: d} },
		Description:           "收盘价相对N日均线的偏离率",
		Category:              "趋势与动量",
		ParameterLabel:        "均线天数",
		Unit:                  "ratio",
		Example:               "原始值 0.03 表示收盘价高于 N 日均线约 3%，-0.03 表示低于约 3%。",
		ImplementationVersion: 1,
	},
	{
		Kind: "slope", Default: 20,
		New:                   func(d int) core.Factor { return &N日斜率{Days: d} },
		Description:           "近N日收盘线性回归斜率的相对值",
		Category:              "趋势与动量",
		ParameterLabel:        "回看天数",
		Unit:                  "ratio",
		Example:               "原始值 0.01 表示近 N 日收盘价线性上行斜率约为窗口均价的 1%/日。",
		ImplementationVersion: 1,
	},
	{
		Kind: "volatility", Default: 20,
		New:                   func(d int) core.Factor { return &N日波动{Days: d} },
		Description:           "近N日收益率总体标准差",
		Category:              "波动",
		ParameterLabel:        "统计天数",
		Unit:                  "ratio",
		Example:               "原始值 0.02 表示近 N 日日收益率标准差约 2%。",
		ImplementationVersion: 1,
	},
	{
		Kind: "amplitude", Default: 20,
		New:                   func(d int) core.Factor { return &N日振幅{Days: d} },
		Description:           "近N日最高最低区间占比",
		Category:              "波动",
		ParameterLabel:        "统计天数",
		Unit:                  "ratio",
		Example:               "原始值 0.15 表示近 N 日最高价较期间最低价高约 15%。",
		ImplementationVersion: 1,
	},
	{
		Kind: "macd_hist", Default: 1,
		New:                   func(int) core.Factor { return &MACD柱强度{} },
		Description:           "12/26/9 MACD柱相对收盘价的强度",
		Category:              "趋势与动量",
		ParameterLabel:        "无参数（固定12/26/9）",
		Unit:                  "ratio",
		Example:               "原始值 -0.01 表示 MACD 柱约为收盘价的 -1%。",
		ImplementationVersion: 1,
	},
	{
		Kind: "macd_delta", Default: 1,
		New:                   func(int) core.Factor { return &MACD柱增量{} },
		Description:           "12/26/9 MACD柱较昨日的相对增量",
		Category:              "趋势与动量",
		ParameterLabel:        "无参数（固定12/26/9）",
		Unit:                  "ratio",
		Example:               "原始值 0.002 表示 MACD 柱较昨日增加量约为收盘价的 0.2%。",
		ImplementationVersion: 1,
	},
	{
		Kind: "macd_trough_position", Default: 4,
		New:                   func(d int) core.Factor { return &MACD低位位置{Days: d} },
		Description:           "昨日MACD柱在近N日区间中的位置",
		Category:              "趋势与动量",
		ParameterLabel:        "回看天数",
		Unit:                  "ratio",
		Example:               "原始值 0 表示昨日 MACD 柱为近 N 日最低，1 表示最高。",
		ImplementationVersion: 1,
	},
	{
		Kind: "macd_negative_streak", Default: 1,
		New:                   func(int) core.Factor { return &MACD负柱连续天数{} },
		Description:           "截至当日MACD柱连续为负的天数",
		Category:              "趋势与动量",
		ParameterLabel:        "无参数（连续计数）",
		Unit:                  "score",
		Example:               "原始值 5 表示截至当日 MACD 柱已连续 5 天为负。",
		ImplementationVersion: 1,
	},
	{
		Kind: "macd_rising_streak", Default: 1,
		New:                   func(int) core.Factor { return &MACD柱连续增长天数{} },
		Description:           "截至当日MACD柱连续增长的步数",
		Category:              "趋势与动量",
		ParameterLabel:        "无参数（连续计数）",
		Unit:                  "score",
		Example:               "原始值 3 表示 MACD 柱已连续 3 个交易步增长。",
		ImplementationVersion: 1,
	},
	{
		Kind: "ma_min_slope", Default: 20,
		New:                   func(d int) core.Factor { return &均线最弱日斜率{Days: d} },
		Description:           "最近5步N日均线相对涨速的最小值",
		Category:              "趋势与动量",
		ParameterLabel:        "均线天数",
		Unit:                  "ratio",
		Example:               "原始值 0.0002 表示最近 5 步中最弱的一步仍上涨约 0.02%。",
		ImplementationVersion: 1,
	},
	{
		Kind: "volume_ratio", Default: 5,
		New:                   func(d int) core.Factor { return &量比{Days: d} },
		Description:           "今日成交量相对前N日均量的倍数",
		Category:              "量能",
		ParameterLabel:        "均量天数",
		Unit:                  "multiple",
		Example:               "原始值 2.5 表示今日成交量约为前 N 日均量的 2.5 倍。",
		ImplementationVersion: 1,
	},
	{
		Kind: "volume_pct", Default: 60,
		New:                   func(d int) core.Factor { return &量分位{Days: d} },
		Description:           "今量在近N日中的分位",
		Category:              "量能",
		ParameterLabel:        "统计窗口",
		Unit:                  "ratio",
		Example:               "原始值 0.8 表示今量不低于近 N 日（含今日）中 80% 交易日的成交量。",
		ImplementationVersion: 1,
	},
	{
		Kind: "volume_surge", Default: 20,
		New:                   func(d int) core.Factor { return &放量占比{Days: d} },
		Description:           "近N日放量天数占比",
		Category:              "量能",
		ParameterLabel:        "统计窗口",
		Unit:                  "ratio",
		Example:               "原始值 0.3 表示近 N 日（含今日）中约 30% 的交易日成交量超过窗口均量的 1.5 倍。",
		ImplementationVersion: 1,
	},
	{
		Kind: "amihud_illiquidity", Default: 20,
		New:                   func(d int) core.Factor { return &Amihud非流动性{Days: d} },
		Description:           "近N日单位成交额对应的绝对收益率均值",
		Category:              "规模与流动性",
		ParameterLabel:        "统计天数",
		Unit:                  "score",
		Example:               "原始值 0.02 表示平均每 1 亿元成交额对应约 2% 的绝对收益率，数值越大流动性越弱。",
		ImplementationVersion: 1,
	},
	{
		Kind: "log_float_cap", Default: 1,
		New:                   func(int) core.Factor { return &对数流通市值{} },
		Description:           "流通市值（亿元）的自然对数",
		Category:              "规模与流动性",
		ParameterLabel:        "无参数（当日流通股本）",
		Unit:                  "score",
		Example:               "原始值 5.99 表示流通市值约为 exp(5.99)=400 亿元。",
		ImplementationVersion: 1,
	},
	{
		Kind: "body", Default: 1,
		New:                   func(int) core.Factor { return &实体幅度{} },
		Description:           "K线实体占比",
		Category:              "K线形态",
		ParameterLabel:        "无参数（单根K线）",
		Unit:                  "ratio",
		Example:               "原始值 0.06 表示当日实体幅度约为开盘价的 6%。",
		ImplementationVersion: 1,
	},
	{
		Kind: "upper_shadow", Default: 1,
		New:                   func(int) core.Factor { return &上影占比{} },
		Description:           "上影线占全幅比例",
		Category:              "K线形态",
		ParameterLabel:        "无参数（单根K线）",
		Unit:                  "ratio",
		Example:               "原始值 0.3 表示上影线约占当日高低全幅的 30%。",
		ImplementationVersion: 1,
	},
	{
		Kind: "lower_shadow", Default: 1,
		New:                   func(int) core.Factor { return &下影占比{} },
		Description:           "下影线占全幅比例",
		Category:              "K线形态",
		ParameterLabel:        "无参数（单根K线）",
		Unit:                  "ratio",
		Example:               "原始值 0.3 表示下影线约占当日高低全幅的 30%。",
		ImplementationVersion: 1,
	},
	{
		Kind: "position", Default: 60,
		New:                   func(d int) core.Factor { return &N日高低位{Days: d} },
		Description:           "收盘价在N日区间中的位置",
		Category:              "位置",
		ParameterLabel:        "回看天数",
		Unit:                  "ratio",
		Example:               "原始值 0.9 表示收盘价处于近 N 日高低区间的 90% 位置（0 为最低，1 为最高）。",
		ImplementationVersion: 1,
	},
	{
		Kind: "high_distance", Default: 252,
		New:                   func(d int) core.Factor { return &N日收盘高点距离{Days: d} },
		Description:           "收盘价相对近N日最高收盘价的距离",
		Category:              "位置",
		ParameterLabel:        "回看天数",
		Unit:                  "ratio",
		Example:               "原始值 -0.10 表示当前收盘价低于近 N 日最高收盘价约 10%，0 表示处于最高点。",
		ImplementationVersion: 1,
	},
	{
		Kind: "close_pct", Default: 120,
		New:                   func(d int) core.Factor { return &收盘分位{Days: d} },
		Description:           "今收盘在近N日收盘价中的分位",
		Category:              "位置",
		ParameterLabel:        "统计窗口",
		Unit:                  "ratio",
		Example:               "原始值 0.85 表示今收盘不低于近 N 日（含今日）中 85% 交易日的收盘价。",
		ImplementationVersion: 1,
	},
	{
		Kind: "kvalue", Default: 9,
		New:                   func(d int) core.Factor { return &K值{Days: d} },
		Description:           "KDJ K线",
		Category:              "位置",
		ParameterLabel:        "计算周期",
		Unit:                  "score",
		Example:               "原始值即 KDJ 的 K 值，取值 0～100，如 80 表示 K 值为 80。",
		ImplementationVersion: 1,
	},
	{
		Kind: "vp_corr", Default: 20,
		New:                   func(d int) core.Factor { return &量价相关{Days: d} },
		Description:           "近N日量价Pearson相关系数",
		Category:              "相关性",
		ParameterLabel:        "统计天数",
		Unit:                  "correlation",
		Example:               "原始值 0.6 表示近 N 日量价正相关，0 为不相关，-0.6 为负相关。",
		ImplementationVersion: 1,
	},
	{
		Kind: "pe_ttm", Default: 1,
		NewContext: func(int) core.ContextFactor {
			return 最新字段{Dataset: "valuation.daily", Field: "pe_ttm", Label: "市盈率TTM"}
		},
		Description:           "最近可见的滚动市盈率",
		Category:              "估值",
		ParameterLabel:        "无参数（日频历史值）",
		Unit:                  "multiple",
		Example:               "原始值 15 表示总市值约为过去 12 个月归母利润的 15 倍；亏损公司可能为负值。",
		ImplementationVersion: 1,
	},
	{
		Kind: "pe_static", Default: 1,
		NewContext: func(int) core.ContextFactor {
			return 最新字段{Dataset: "valuation.daily", Field: "pe_static", Label: "静态市盈率"}
		},
		Description:           "最近可见的静态市盈率",
		Category:              "估值",
		ParameterLabel:        "无参数（日频历史值）",
		Unit:                  "multiple",
		Example:               "原始值 20 表示总市值约为最近完整年度归母利润的 20 倍。",
		ImplementationVersion: 1,
	},
	{
		Kind: "pb_mrq", Default: 1,
		NewContext: func(int) core.ContextFactor {
			return 最新字段{Dataset: "valuation.daily", Field: "pb_mrq", Label: "市净率MRQ"}
		},
		Description:           "最近可见的市净率（最近报告期）",
		Category:              "估值",
		ParameterLabel:        "无参数（日频历史值）",
		Unit:                  "multiple",
		Example:               "原始值 2.5 表示总市值约为最近报告期净资产的 2.5 倍。",
		ImplementationVersion: 1,
	},
	{
		Kind: "ps_ttm", Default: 1,
		NewContext: func(int) core.ContextFactor {
			return 最新字段{Dataset: "valuation.daily", Field: "ps_ttm", Label: "市销率TTM"}
		},
		Description:           "最近可见的滚动市销率",
		Category:              "估值",
		ParameterLabel:        "无参数（日频历史值）",
		Unit:                  "multiple",
		Example:               "原始值 3 表示总市值约为过去 12 个月营业收入的 3 倍。",
		ImplementationVersion: 1,
	},
	{
		Kind: "pcf_ocf_ttm", Default: 1,
		NewContext: func(int) core.ContextFactor {
			return 最新字段{Dataset: "valuation.daily", Field: "pcf_ocf_ttm", Label: "市现率TTM"}
		},
		Description:           "最近可见的经营现金流口径滚动市现率",
		Category:              "估值",
		ParameterLabel:        "无参数（日频历史值）",
		Unit:                  "multiple",
		Example:               "原始值 12 表示总市值约为过去 12 个月经营现金流的 12 倍。",
		ImplementationVersion: 1,
	},
	{
		Kind: "peg", Default: 1,
		NewContext: func(int) core.ContextFactor {
			return 最新字段{Dataset: "valuation.daily", Field: "peg", Label: "PEG"}
		},
		Description:           "最近可见的供应商口径 PEG",
		Category:              "估值",
		ParameterLabel:        "无参数（日频历史值）",
		Unit:                  "multiple",
		Example:               "原始值 1.2 表示市盈率约为供应商采用的盈利增长率的 1.2 倍。",
		ImplementationVersion: 1,
	},
	{
		Kind: "macd_negative_shrinking_streak", Default: 1,
		New:                   func(int) core.Factor { return &MACD负柱缩短天数{} },
		Description:           "12/26/9 MACD柱连续上涨且前一柱为负的步数，含首次穿越零轴",
		Category:              "趋势与动量",
		ParameterLabel:        "无参数（连续计数，固定12/26/9）",
		Unit:                  "score",
		Example:               "原始值 3 表示连续 3 步上涨且每步前一柱为负；今天可以首次为零或转正。",
		ImplementationVersion: 1,
	},
	{
		Kind: "macd_same_sign_reversals", Default: 10,
		New:                   func(d int) core.Factor { return &MACD同号段最大反转次数{Days: d} },
		Description:           "12/26/9 MACD经EMA5平滑后，近N步各同号段反转次数的最大值",
		Category:              "趋势与动量",
		ParameterLabel:        "观察步数（固定12/26/9、EMA5）",
		Unit:                  "score",
		Example:               "原始值 2 表示窗口内反转最多的同号段发生 2 次方向变化；异号段分别计数，平段不改变方向。",
		ImplementationVersion: 1,
	},
	{
		Kind: "macd_same_sign_max_change", Default: 5,
		New:                   func(d int) core.Factor { return &MACD同号最大变化比{Days: d} },
		Description:           "12/26/9 MACD近N步同号非零柱的最大变化比，跳过零轴穿越",
		Category:              "趋势与动量",
		ParameterLabel:        "观察步数（固定12/26/9）",
		Unit:                  "ratio",
		Example:               "原始值 0.5 表示同号非零柱的最大变化比为 50%；没有可比较步骤时为 0。",
		ImplementationVersion: 1,
	},
	{
		Kind: "downside_vol", Default: 20,
		New:                   func(d int) core.Factor { return &N日下行波动{Days: d} },
		Description:           "近N日负收益的下行波动（只累计亏损侧）",
		Category:              "波动",
		ParameterLabel:        "统计天数",
		Unit:                  "ratio",
		Example:               "原始值 0.015 表示近 N 日亏损侧日收益的均方根约 1.5%，全正收益时为 0。",
		ImplementationVersion: 1,
	},
	{
		Kind: "return_skew", Default: 20,
		New:                   func(d int) core.Factor { return &N日收益偏度{Days: d} },
		Description:           "近N日收益率分布的三阶标准化偏度",
		Category:              "波动",
		ParameterLabel:        "统计天数",
		Unit:                  "score",
		Example:               "原始值 -1 表示近 N 日收益分布左偏（长左尾），0 为对称，1 为右偏。",
		ImplementationVersion: 2,
	},
	{
		Kind: "parkinson_vol", Default: 20,
		New:                   func(d int) core.Factor { return &N日Parkinson波动{Days: d} },
		Description:           "近N日基于高低价的Parkinson波动率",
		Category:              "波动",
		ParameterLabel:        "统计天数",
		Unit:                  "ratio",
		Example:               "原始值 0.03 表示由日内高低区间估计的日波动约 3%。",
		ImplementationVersion: 1,
	},
	{
		Kind: "turnover_mean", Default: 20,
		New:                   func(d int) core.Factor { return &平均换手率{Days: d} },
		Description:           "近N日日均换手率（百分数）",
		Category:              "量能",
		ParameterLabel:        "统计天数",
		Unit:                  "percent",
		Example:               "原始值 2.5 表示近 N 日日均换手率约 2.5%（数值本身即百分数）。",
		ImplementationVersion: 1,
	},
	{
		Kind: "turnover_pct", Default: 60,
		New:                   func(d int) core.Factor { return &换手率分位{Days: d} },
		Description:           "今换手率在近N日中的分位",
		Category:              "量能",
		ParameterLabel:        "统计窗口",
		Unit:                  "ratio",
		Example:               "原始值 0.9 表示今换手率不低于近 N 日（含今日）中 90% 交易日的换手率。",
		ImplementationVersion: 1,
	},
	{
		Kind: "volume_cv", Default: 20,
		New:                   func(d int) core.Factor { return &量波动{Days: d} },
		Description:           "近N日成交量的变异系数（标准差/均量）",
		Category:              "量能",
		ParameterLabel:        "统计天数",
		Unit:                  "ratio",
		Example:               "原始值 0.5 表示近 N 日成交量标准差约为均量的一半，量能越不稳定值越大。",
		ImplementationVersion: 1,
	},
	{
		Kind: "volume_up_ratio", Default: 20,
		New:                   func(d int) core.Factor { return &阳线量比{Days: d} },
		Description:           "近N日阳线日均量相对窗口均量的倍数",
		Category:              "量能",
		ParameterLabel:        "统计天数",
		Unit:                  "multiple",
		Example:               "原始值 1.2 表示近 N 日阳线（收盘>开盘）日均成交量约为窗口均量的 1.2 倍。",
		ImplementationVersion: 1,
	},
	{
		Kind: "volume_down_ratio", Default: 20,
		New:                   func(d int) core.Factor { return &阴线量比{Days: d} },
		Description:           "近N日阴线日均量相对窗口均量的倍数",
		Category:              "量能",
		ParameterLabel:        "统计天数",
		Unit:                  "multiple",
		Example:               "原始值 0.8 表示近 N 日阴线（收盘<开盘）日均成交量约为窗口均量的 0.8 倍。",
		ImplementationVersion: 1,
	},
	{
		Kind: "range_ratio", Default: 1,
		New:                   func(int) core.Factor { return &全幅占比{} },
		Description:           "当日高低全幅相对昨收的比例",
		Category:              "K线形态",
		ParameterLabel:        "无参数（单根K线）",
		Unit:                  "ratio",
		Example:               "原始值 0.05 表示当日最高价较最低价高出的幅度约为昨收的 5%。",
		ImplementationVersion: 1,
	},
	{
		Kind: "bar_center", Default: 1,
		New:                   func(int) core.Factor { return &K线重心{} },
		Description:           "收盘价在当日高低区间中的相对位置",
		Category:              "K线形态",
		ParameterLabel:        "无参数（单根K线）",
		Unit:                  "score",
		Example:               "原始值 0.5 表示收盘价位于当日高低区间的中点与最高点之间（-1 为最低，1 为最高）。",
		ImplementationVersion: 1,
	},
	{
		Kind: "dividend_yield", Default: 1,
		NewContext: func(int) core.ContextFactor {
			return 最新字段{Dataset: "tdx.valuation.daily", Field: "dividend_yield", Label: "股息率"}
		},
		Description:           "最近可见的通达信口径股息率（百分数）",
		Category:              "估值",
		ParameterLabel:        "无参数（日频历史值）",
		Unit:                  "percent",
		Example:               "原始值 3 表示股息率约 3%（数值本身即百分数）。",
		ImplementationVersion: 1,
	},
}

// All 返回全部因子目录。
func All() []CatalogEntry {
	out := make([]CatalogEntry, 0, len(registry))
	for _, e := range registry {
		out = append(out, CatalogEntry{
			Kind:                  e.Kind,
			Name:                  e.buildContext(e.Default).Name(),
			Description:           e.Description,
			Category:              e.Category,
			ParameterLabel:        e.ParameterLabel,
			DefaultDays:           e.Default,
			Unit:                  e.Unit,
			Example:               e.Example,
			ImplementationVersion: e.ImplementationVersion,
		})
	}
	return out
}

// Build 按 kind 构造因子：kind 未知返回 nil；days<=0 使用该因子默认参数。
func Build(kind string, days int) core.Factor {
	for _, e := range registry {
		if e.Kind != kind || e.New == nil {
			continue
		}
		if days <= 0 {
			days = e.Default
		}
		return e.New(days)
	}
	return nil
}

// BuildContext constructs the point-in-time factor form used by research
// pipelines. Existing price factors are adapted without changing their output;
// future data-backed catalog entries can implement core.ContextFactor directly.
func BuildContext(kind string, days int) core.ContextFactor {
	for _, e := range registry {
		if e.Kind != kind {
			continue
		}
		if days <= 0 {
			days = e.Default
		}
		return e.buildContext(days)
	}
	return nil
}

func (e entry) buildContext(days int) core.ContextFactor {
	if e.NewContext != nil {
		return e.NewContext(days)
	}
	if e.New != nil {
		return core.Contextual(e.New(days))
	}
	return nil
}

// BuildWithData binds a catalog factor to a data view and exposes the original
// core.Factor contract required by Buyer/TopN/backtest paths.
func BuildWithData(kind string, days int, data researchdata.View) core.Factor {
	return core.BindContextFactor(BuildContext(kind, days), data)
}

// Catalog 返回单个因子目录元数据：kind 未知返回 false
// （分析主流程先经 Validate 拒绝未知 kind，此分支不应触发）。
func Catalog(kind string) (CatalogEntry, bool) {
	for _, e := range registry {
		if e.Kind == kind {
			return CatalogEntry{
				Kind:                  e.Kind,
				Name:                  e.buildContext(e.Default).Name(),
				Description:           e.Description,
				Category:              e.Category,
				ParameterLabel:        e.ParameterLabel,
				DefaultDays:           e.Default,
				Unit:                  e.Unit,
				Example:               e.Example,
				ImplementationVersion: e.ImplementationVersion,
			}, true
		}
	}
	return CatalogEntry{}, false
}
