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
	Unit           string `json:"unit"` // ratio | multiple | score | correlation
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

// registry 全部因子目录（显式列表，不用反射：Yaegi 与 IDE 跳转友好）。
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
