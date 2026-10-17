package builder

import (
	"fmt"

	"github.com/injoyai/strategy-tail/core"
	sb "github.com/injoyai/strategy-tail/strategies/buy"
	ss "github.com/injoyai/strategy-tail/strategies/sell"
)

// builder_catalog.go 搭建器组件目录与工厂映射（设计文档 §3）。
//
// 目录是前端条件编辑器的唯一事实来源：稳定组件 ID、中文名称、角色分类、
// 参数定义（类型/单位/默认值/范围）。工厂把 JSON 参数映射为现有 Go 组件，
// 不引入新公式语言，不反射猜测默认值。目录与工厂的一致性由测试保证。

// 条件角色：买入 / 策略卖出信号 / 独立风控（PROJECT_RULES.md §6.3 风控即 Seller）。
const (
	roleBuy     = "buy"
	roleSell    = "sell_signal"
	roleRisk    = "risk"
	SpecVersion = 1
)

// 参数类型（ParamDef.Type）：
//   int      整数（周期、天数）
//   float    浮点原值（倍数、阈值、金额）
//   price    价格（元）
//   ratio    界面按百分数输入/显示，执行时除以 100 变成比例（如 5 → 0.05）
//   percent  百分数原值存储（如 A单日跌幅大于 7 表示 7%），不做换算
//   text     文本（如尾盘时间 "14:55:00"）
//   intList  整数列表（如均线多头排列 [5,10,20]）

// ParamDef 组件参数定义。
// Min/Max 为界面输入单位（ratio 参数按百分数），nil 表示不限制。
type ParamDef struct {
	Key     string   `json:"key"`
	Label   string   `json:"label"`
	Type    string   `json:"type"`
	Unit    string   `json:"unit,omitempty"`
	Default any      `json:"default,omitempty"`
	Min     *float64 `json:"min,omitempty"`
	Max     *float64 `json:"max,omitempty"`
	Step    float64  `json:"step,omitempty"`
	Pattern string   `json:"pattern,omitempty"` // text 参数格式约束（正则）
	Desc    string   `json:"desc,omitempty"`
}

// ComponentEntry 可运行组件条目（目录 → 工厂一一对应）。
type ComponentEntry struct {
	ID       string     `json:"id"`
	Name     string     `json:"name"`
	Role     string     `json:"role"`
	Category string     `json:"category"`
	Desc     string     `json:"desc,omitempty"`
	Params   []ParamDef `json:"params,omitempty"`
}

// UnavailableComponent 暂未接入的组件及原因（设计文档 §3：不能静默忽略）。
type UnavailableComponent struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Role   string `json:"role"`
	Reason string `json:"reason"`
}

// pf 返回 float64 指针（目录范围定义用）。
func pf(v float64) *float64 { return &v }

// intParam 整数参数（单位默认"日"）。
func intParam(key, label string, def int, min, max float64, desc string) ParamDef {
	p := ParamDef{Key: key, Label: label, Type: "int", Default: def, Step: 1, Desc: desc}
	if min != 0 || max != 0 {
		p.Min, p.Max = pf(min), pf(max)
	}
	return p
}

// floatParam 浮点参数。
func floatParam(key, label, unit string, def, min, max, step float64, desc string) ParamDef {
	p := ParamDef{Key: key, Label: label, Type: "float", Unit: unit, Default: def, Step: step, Desc: desc}
	if min != 0 || max != 0 {
		p.Min, p.Max = pf(min), pf(max)
	}
	return p
}

// ratioParam 比例参数（界面百分数，执行时 /100）。min/max 按百分数。
func ratioParam(key, label string, def, min, max, step float64, desc string) ParamDef {
	p := ParamDef{Key: key, Label: label, Type: "ratio", Unit: "%", Default: def, Step: step, Desc: desc}
	if min != 0 || max != 0 {
		p.Min, p.Max = pf(min), pf(max)
	}
	return p
}

// percentParam 百分数原值参数（如乖离率、单日跌幅阈值）。
func percentParam(key, label string, def, min, max, step float64, desc string) ParamDef {
	p := ParamDef{Key: key, Label: label, Type: "percent", Unit: "%", Default: def, Step: step, Desc: desc}
	if min != 0 || max != 0 {
		p.Min, p.Max = pf(min), pf(max)
	}
	return p
}

// macdParams MACD 三参数（Fast/Slow/Signal，0=组件默认 12/26/9）。
func macdParams() []ParamDef {
	return []ParamDef{
		intParam("fast", "快线周期", 12, 2, 100, ""),
		intParam("slow", "慢线周期", 26, 2, 200, ""),
		intParam("signal", "信号周期", 9, 2, 100, ""),
	}
}

// builderCatalog 返回可运行组件目录（前端按 role+category 渲染）。
func builderCatalog() []ComponentEntry {
	return []ComponentEntry{
		// ---- 买入：基准与板块 ----
		{ID: "buy_all", Name: "全部样本买入", Role: roleBuy, Category: "基准",
			Desc: "显式基准：不做任何过滤，所有样本都买入"},
		{ID: "buy_board_cyb", Name: "创业板股票", Role: roleBuy, Category: "板块", Desc: "仅保留创业板股票（代码前缀 sz30）"},
		{ID: "buy_board_kcb", Name: "科创板股票", Role: roleBuy, Category: "板块", Desc: "仅保留科创板股票（代码前缀 sh68）"},
		{ID: "buy_board_bj", Name: "北证股票", Role: roleBuy, Category: "板块", Desc: "仅保留北交所股票（代码前缀 bj）"},
		{ID: "buy_etf", Name: "ETF基金", Role: roleBuy, Category: "板块", Desc: "仅保留被行情代码规则识别为 ETF 的标的"},

		// ---- 买入：价格与市值 ----
		{ID: "buy_price", Name: "价格范围", Role: roleBuy, Category: "价格市值",
			Desc: "最新收盘价处于 [Min,Max] 区间，0 表示不限",
			Params: []ParamDef{
				floatParam("min", "最低价", "元", 2, 0, 10000, 0.1, "0=不限"),
				floatParam("max", "最高价", "元", 120, 0, 10000, 0.1, "0=不限"),
			}},
		{ID: "buy_market_cap", Name: "流通市值范围", Role: roleBuy, Category: "价格市值",
			Desc: "流通市值处于 [Min,Max] 区间，0 表示不限",
			Params: []ParamDef{
				floatParam("min", "最小市值", "亿元", 0, 0, 100000, 1, "0=不限"),
				floatParam("max", "最大市值", "亿元", 0, 0, 100000, 1, "0=不限"),
			}},

		// ---- 买入：均线趋势 ----
		{ID: "buy_price_above_ma", Name: "现价高于N日均线", Role: roleBuy, Category: "均线趋势",
			Desc:   "最新收盘价高于指定周期均线",
			Params: []ParamDef{intParam("period", "均线周期", 20, 1, 1000, "")}},
		{ID: "buy_stand_above_ma", Name: "连续站上N日均线", Role: roleBuy, Category: "均线趋势",
			Desc: "收盘价连续 Days 天高于 Period 日均线",
			Params: []ParamDef{
				intParam("period", "均线周期", 20, 1, 1000, ""),
				intParam("days", "连续天数", 5, 1, 100, ""),
			}},
		{ID: "buy_close_above_ma", Name: "收盘高于N日均线", Role: roleBuy, Category: "均线趋势",
			Desc:   "最新收盘价高于指定周期均线",
			Params: []ParamDef{intParam("period", "均线周期", 20, 1, 1000, "")}},
		{ID: "buy_ma_up", Name: "均线向上", Role: roleBuy, Category: "均线趋势",
			Desc: "均线在回看窗口内逐日上行且斜率达标；回看天数 0 使用组件默认 5",
			Params: []ParamDef{
				intParam("period", "均线周期", 20, 1, 1000, ""),
				intParam("lookback", "回看天数", 5, 0, 100, "0=组件默认5"),
				ratioParam("minSlope", "最小斜率", 0, 0, 10, 0.01, "日均涨幅下限，0=不校验"),
			}},
		{ID: "buy_ma_bull", Name: "均线多头排列", Role: roleBuy, Category: "均线趋势",
			Desc: "给定周期均线按从小到大依次向上排列",
			Params: []ParamDef{{
				Key: "periods", Label: "均线周期", Type: "intList",
				Default: []int{5, 10, 20}, Min: pf(1), Max: pf(1000), Desc: "逗号分隔，如 5,10,20；留空使用组件默认",
			}}},
		{ID: "buy_pullback_ma", Name: "回踩N日均线", Role: roleBuy, Category: "均线趋势",
			Desc: "价格回踩短期均线获得支撑；支撑周期 0=不启用",
			Params: []ParamDef{
				intParam("period", "回踩均线周期", 5, 1, 1000, ""),
				intParam("supportPeriod", "支撑均线周期", 0, 0, 1000, "0=不启用"),
				intParam("minTouchDays", "最少触碰天数", 3, 1, 100, ""),
			}},

		// ---- 买入：MACD ----
		{ID: "buy_macd_negative", Name: "MACD连续负数", Role: roleBuy, Category: "MACD",
			Desc: "MACD 柱连续 MinDays 天为负；MaxDays 0=不限",
			Params: append(macdParams(),
				intParam("minDays", "最少天数", 1, 1, 1000, ""),
				intParam("maxDays", "最多天数", 0, 0, 1000, "0=不限"),
			)},
		{ID: "buy_macd_rising", Name: "MACD柱连涨", Role: roleBuy, Category: "MACD",
			Desc: "MACD 柱连续上升；MaxDays 0=不限，Lookback 0=不校验起点",
			Params: append(macdParams(),
				intParam("minDays", "最少天数", 1, 1, 1000, ""),
				intParam("maxDays", "最多天数", 0, 0, 1000, "0=不限"),
				intParam("lookback", "起点回看", 0, 0, 1000, "0=不校验"),
			)},
		{ID: "buy_macd_positive", Name: "MACD连续为正", Role: roleBuy, Category: "MACD",
			Desc:   "MACD 柱连续指定天数大于 0，包含当日",
			Params: append(macdParams(), intParam("days", "最少天数", 1, 1, 1000, ""))},
		{ID: "buy_macd_reversal", Name: "MACD反转", Role: roleBuy, Category: "MACD",
			Desc: "MACD 柱在低位拐头；MaxLookback 0=不限",
			Params: append(macdParams(),
				intParam("minLookback", "最短回看", 20, 1, 1000, ""),
				intParam("maxLookback", "最长回看", 0, 0, 1000, "0=不限"),
				floatParam("minDiff", "最小差值", "", 0, 0, 10, 0.001, "柱值最小变化量，0=不限"),
			)},
		{ID: "buy_macd_golden_cross", Name: "MACD金叉", Role: roleBuy, Category: "MACD",
			Desc: "MACD 柱由负转正", Params: macdParams()},
		{ID: "buy_macd_above_zero", Name: "MACD零轴上方", Role: roleBuy, Category: "MACD",
			Desc: "MACD 柱当前为正", Params: macdParams()},

		// ---- 买入：RSI ----
		{ID: "buy_rsi_low", Name: "RSI低于阈值", Role: roleBuy, Category: "RSI",
			Desc: "指定周期的 RSI 低于阈值",
			Params: []ParamDef{
				intParam("period", "RSI周期", 14, 2, 100, ""),
				floatParam("threshold", "阈值", "", 20, 1, 99, 1, ""),
			}},
		{ID: "buy_rsi_turn_up", Name: "RSI拐头向上", Role: roleBuy, Category: "RSI",
			Desc:   "今日 RSI 高于昨日 RSI",
			Params: []ParamDef{intParam("period", "RSI周期", 14, 2, 100, "")}},
		{ID: "buy_rsi_range", Name: "RSI处于区间", Role: roleBuy, Category: "RSI",
			Desc: "RSI 处于上下限区间（含边界）；下限为 0 时使用 30，上限为 0 时使用 50",
			Params: []ParamDef{
				intParam("period", "RSI周期", 14, 2, 100, ""),
				floatParam("min", "下限", "", 30, 0, 100, 1, "0=组件默认30"),
				floatParam("max", "上限", "", 50, 0, 100, 1, "0=组件默认50"),
			}},
		{ID: "buy_rsi_oversold_rebound", Name: "RSI超卖回升", Role: roleBuy, Category: "RSI",
			Desc: "昨日 RSI 低于阈值且今日回升到阈值之上",
			Params: []ParamDef{
				intParam("period", "RSI周期", 14, 2, 100, ""),
				floatParam("threshold", "超卖阈值", "", 30, 1, 99, 1, ""),
			}},

		// ---- 买入：成交量 ----
		{ID: "buy_volume_surge", Name: "成交量放大", Role: roleBuy, Category: "成交量",
			Desc: "今日成交量大于近 Period 日均量的 Ratio 倍",
			Params: []ParamDef{
				intParam("period", "均量周期", 5, 1, 500, ""),
				floatParam("ratio", "放大倍数", "倍", 1.5, 0.1, 100, 0.1, ""),
			}},
		{ID: "buy_volume_double", Name: "倍量", Role: roleBuy, Category: "成交量",
			Desc: "今日量处于 [MinRatio,MaxRatio] 倍基准量区间；MaxRatio 0=不限",
			Params: []ParamDef{
				floatParam("minRatio", "最小倍数", "倍", 2, 0.1, 100, 0.1, ""),
				floatParam("maxRatio", "最大倍数", "倍", 0, 0, 100, 0.1, "0=不限"),
				intParam("baseDays", "基准天数", 1, 1, 100, ""),
			}},
		{ID: "buy_volume_shrink", Name: "缩量", Role: roleBuy, Category: "成交量",
			Desc: "今日成交量小于近 Days 日均量的 Ratio 倍（<1 为缩量）",
			Params: []ParamDef{
				intParam("days", "均量周期", 5, 1, 500, ""),
				floatParam("ratio", "量比上限", "倍", 0.8, 0.01, 10, 0.01, ""),
			}},
		{ID: "buy_turnover_min", Name: "成交额大于", Role: roleBuy, Category: "成交量",
			Desc: "当日成交额下限，用于过滤流动性差的标的",
			Params: []ParamDef{
				floatParam("min", "最小成交额", "元", 50000000, 0, 1e12, 1000000, "0=组件默认5000万"),
			}},

		// ---- 买入：K线形态 ----
		{ID: "buy_limit_up_hit", Name: "当日涨停", Role: roleBuy, Category: "K线形态",
			Desc: "当日涨幅达到板块涨停阈值（主板约10%，创业/科创约20%）"},
		{ID: "buy_filter_limit_up", Name: "过滤当日涨停", Role: roleBuy, Category: "K线形态",
			Desc: "排除当日已涨停的标的（无法买入）"},
		{ID: "buy_break_high", Name: "突破N日新高", Role: roleBuy, Category: "K线形态",
			Desc:   "最新收盘价突破前 N 个交易日的最高价，不含当日",
			Params: []ParamDef{intParam("period", "周期", 20, 1, 1000, "")}},
		{ID: "buy_solid_bull", Name: "实体阳线", Role: roleBuy, Category: "K线形态",
			Desc:   "阳线实体占振幅比例达标；0=只要收阳",
			Params: []ParamDef{ratioParam("minBodyRatio", "最小实体比例", 0, 0, 100, 1, "0=只要收阳")}},
		{ID: "buy_solid_bear", Name: "实体阴线", Role: roleBuy, Category: "K线形态",
			Desc:   "阴线实体占振幅比例达标；0=只要收阴（常用于低吸）",
			Params: []ParamDef{ratioParam("minBodyRatio", "最小实体比例", 0, 0, 100, 1, "0=只要收阴")}},
		{ID: "buy_close_gt_prev_close", Name: "收盘大于昨收", Role: roleBuy, Category: "K线形态", Desc: "最新收盘价高于前一交易日收盘价"},
		{ID: "buy_close_gt_prev_open", Name: "收盘大于昨开", Role: roleBuy, Category: "K线形态", Desc: "最新收盘价高于前一交易日开盘价"},
		{ID: "buy_recent_gain_below", Name: "近N日涨幅小于", Role: roleBuy, Category: "K线形态",
			Desc: "近 N 个交易日累计涨幅低于涨幅上限；上限为 0 时使用 15%",
			Params: []ParamDef{
				intParam("days", "区间天数", 5, 1, 500, ""),
				percentParam("max", "涨幅上限", 15, -100, 1000, 0.5, ""),
			}},
		{ID: "buy_day_gain_range", Name: "单日涨幅范围", Role: roleBuy, Category: "K线形态",
			Desc: "当日涨幅处于上下限区间（含边界），0 表示对应方向不限",
			Params: []ParamDef{
				percentParam("min", "涨幅下限", 0, -100, 100, 0.5, "0=不限"),
				percentParam("max", "涨幅上限", 0, -100, 100, 0.5, "0=不限"),
			}},
		{ID: "buy_bottom_up", Name: "底部抬升", Role: roleBuy, Category: "K线形态",
			Desc:   "窗口内低点逐级抬高",
			Params: []ParamDef{intParam("window", "窗口天数", 8, 2, 500, "")}},

		// ---- 买入：波动率 ----
		{ID: "buy_atr_range", Name: "ATR波动率范围", Role: roleBuy, Category: "波动率",
			Desc: "ATR/收盘价处于区间内，过滤死水与妖股",
			Params: []ParamDef{
				intParam("period", "ATR周期", 14, 2, 500, ""),
				percentParam("minPct", "最小波动", 0.5, 0, 100, 0.1, "0=不限"),
				percentParam("maxPct", "最大波动", 5, 0, 100, 0.1, "0=不限"),
			}},

		// ---- 买入：均值回归 ----
		{ID: "buy_boll_lower", Name: "布林下轨", Role: roleBuy, Category: "均值回归",
			Desc: "收盘价跌破布林下轨（均值 - StdTimes×标准差）",
			Params: []ParamDef{
				intParam("period", "布林周期", 20, 2, 500, ""),
				floatParam("stdTimes", "标准差倍数", "σ", 2, 0.1, 10, 0.1, ""),
			}},
		{ID: "buy_boll_lower_volume", Name: "布林下轨+地量", Role: roleBuy, Category: "均值回归",
			Desc: "布林下轨叠加地量确认，降低假信号",
			Params: []ParamDef{
				intParam("period", "布林周期", 20, 2, 500, ""),
				floatParam("stdTimes", "标准差倍数", "σ", 2, 0.1, 10, 0.1, ""),
				intParam("volDays", "均量周期", 5, 1, 500, ""),
				floatParam("volRatio", "量比上限", "倍", 0.6, 0.01, 10, 0.01, ""),
			}},
		{ID: "buy_boll_lower_rsi", Name: "布林下轨+RSI超卖", Role: roleBuy, Category: "均值回归",
			Desc: "布林下轨叠加 RSI 超卖双重确认",
			Params: []ParamDef{
				intParam("period", "布林周期", 20, 2, 500, ""),
				floatParam("stdTimes", "标准差倍数", "σ", 2, 0.1, 10, 0.1, ""),
				intParam("rsiPeriod", "RSI周期", 14, 2, 100, ""),
				floatParam("rsiThreshold", "RSI阈值", "", 30, 1, 99, 1, ""),
			}},
		{ID: "buy_bias_oversold", Name: "乖离超卖", Role: roleBuy, Category: "均值回归",
			Desc: "乖离率 (Close-MA)/MA×100 低于阈值",
			Params: []ParamDef{
				intParam("period", "均线周期", 20, 2, 500, ""),
				percentParam("minBias", "乖离阈值", -7, -100, -0.1, 0.5, ""),
			}},
		{ID: "buy_zscore_oversold", Name: "Z-Score超卖", Role: roleBuy, Category: "均值回归",
			Desc: "价格偏离均值超过 MinZ 个标准差",
			Params: []ParamDef{
				intParam("period", "统计窗口", 20, 2, 500, ""),
				floatParam("minZ", "最小Z值", "", -2, -10, -0.1, 0.1, ""),
			}},
		{ID: "buy_donchian_lower", Name: "唐奇安下沿", Role: roleBuy, Category: "均值回归",
			Desc:   "收盘价触及近 Period 日最低价（不含当日）",
			Params: []ParamDef{intParam("period", "通道周期", 20, 2, 500, "")}},

		// ---- 卖出信号：MACD ----
		{ID: "sell_macd_reversal", Name: "MACD反转", Role: roleSell, Category: "MACD",
			Desc: "MACD 柱在近 Lookback 日最高点后拐头",
			Params: append(macdParams(),
				intParam("lookback", "回看天数", 20, 1, 1000, ""),
				floatParam("minDiff", "最小差值", "", 0, 0, 10, 0.001, "柱值最小变化量，0=不限"),
			)},
		{ID: "sell_macd_down_after_buy", Name: "MACD买入后连跌", Role: roleSell, Category: "MACD",
			Desc: "买入 AfterDays 天后 MACD 柱连续 Days 天下降",
			Params: append(macdParams(),
				intParam("afterDays", "起始天数", 1, 1, 1000, ""),
				intParam("days", "连跌天数", 1, 1, 1000, ""),
			)},
		{ID: "sell_macd_dead_cross_negative", Name: "MACD死叉且DIF<0", Role: roleSell, Category: "MACD",
			Desc: "MACD 柱由正转负且 DIF 在零轴下方", Params: macdParams()},

		// ---- 卖出信号：均线趋势 ----
		{ID: "sell_break_ma", Name: "收盘跌破均线", Role: roleSell, Category: "均线趋势",
			Desc: "收盘价连续 Days 天低于 Period 日均线",
			Params: []ParamDef{
				intParam("period", "均线周期", 20, 1, 1000, ""),
				intParam("days", "连续天数", 3, 1, 100, ""),
			}},
		{ID: "sell_below_short_ma", Name: "跌破短期均线", Role: roleSell, Category: "均线趋势",
			Desc:   "最新收盘价低于指定周期的短期均线即卖出",
			Params: []ParamDef{intParam("period", "均线周期", 10, 1, 1000, "")}},
		{ID: "sell_ma_dead_cross", Name: "均线死叉", Role: roleSell, Category: "均线趋势",
			Desc: "短期均线下穿长期均线",
			Params: []ParamDef{
				intParam("short", "短期均线", 5, 1, 1000, ""),
				intParam("long", "长期均线", 20, 1, 1000, ""),
			}},
		{ID: "sell_long_ma_down", Name: "250/60日均线转弱", Role: roleSell, Category: "均线趋势",
			Desc:   "长期均线趋势不再向上",
			Params: []ParamDef{intParam("lookback", "回看天数", 5, 1, 100, "")}},
		{ID: "sell_back_to_ma", Name: "回归均线", Role: roleSell, Category: "均线趋势",
			Desc:   "收盘价回到 Period 日均线上方",
			Params: []ParamDef{intParam("period", "均线周期", 20, 1, 1000, "")}},

		// ---- 卖出信号：RSI ----
		{ID: "sell_rsi_overbought", Name: "RSI超买", Role: roleSell, Category: "RSI",
			Desc: "指定周期的 RSI 高于超买阈值即卖出",
			Params: []ParamDef{
				intParam("period", "RSI周期", 14, 2, 100, ""),
				floatParam("threshold", "超买阈值", "", 75, 50, 100, 1, ""),
			}},
		{ID: "sell_rsi_recover", Name: "RSI恢复", Role: roleSell, Category: "RSI",
			Desc: "RSI 回升至中性区阈值之上",
			Params: []ParamDef{
				intParam("period", "RSI周期", 14, 2, 100, ""),
				floatParam("threshold", "阈值", "", 50, 1, 99, 1, ""),
			}},

		// ---- 卖出信号：均值回归 ----
		{ID: "sell_back_to_boll_mid", Name: "回归布林中轨", Role: roleSell, Category: "均值回归",
			Desc:   "最新收盘价达到或高于布林中轨（指定周期均线）即卖出",
			Params: []ParamDef{intParam("period", "布林周期", 20, 2, 500, "")}},
		{ID: "sell_bias_zero", Name: "乖离归零", Role: roleSell, Category: "均值回归",
			Desc:   "负乖离修复至 0",
			Params: []ParamDef{intParam("period", "均线周期", 20, 2, 500, "")}},
		{ID: "sell_zscore_zero", Name: "Z-Score归零", Role: roleSell, Category: "均值回归",
			Desc:   "指定统计窗口的 Z-Score 达到或高于 0 即卖出",
			Params: []ParamDef{intParam("period", "统计窗口", 20, 2, 500, "")}},
		{ID: "sell_back_to_donchian_mid", Name: "回归唐奇安中轨", Role: roleSell, Category: "均值回归",
			Desc:   "最新收盘价达到或高于通道中轨（窗口内最高价与最低价的均值）即卖出",
			Params: []ParamDef{intParam("period", "通道周期", 20, 2, 500, "")}},

		// ---- 卖出信号：K线形态 ----
		{ID: "sell_big_fall", Name: "单日跌幅大于", Role: roleSell, Category: "K线形态",
			Desc:   "当日跌幅超过指定百分比即卖出",
			Params: []ParamDef{percentParam("max", "跌幅阈值", 7, 0.5, 50, 0.5, "")}},
		{ID: "sell_volume_big_bear", Name: "放量大阴线", Role: roleSell, Category: "K线形态",
			Desc: "放量下跌出货信号",
			Params: []ParamDef{
				floatParam("volumeRatio", "量比", "倍", 1.5, 0.1, 100, 0.1, ""),
				percentParam("fallPct", "最低跌幅", 2, 0.5, 50, 0.5, ""),
			}},

		// ---- 独立风控：止损止盈 ----
		{ID: "risk_takeprofit_stoploss", Name: "止盈止损", Role: roleRisk, Category: "止损止盈",
			Desc: "基于买入价的固定比例止盈/止损，0=不启用",
			Params: []ParamDef{
				ratioParam("takeProfit", "止盈比例", 10, 0, 1000, 0.5, "0=不启用"),
				ratioParam("stopLoss", "止损比例", 5, 0, 1000, 0.5, "0=不启用"),
			}},
		{ID: "risk_fixed_stop", Name: "固定止损", Role: roleRisk, Category: "止损止盈",
			Desc:   "最新收盘价相对买入价的跌幅达到止损比例即卖出；比例为 0 时使用 5%",
			Params: []ParamDef{ratioParam("pct", "止损比例", 5, 0, 1000, 0.5, "0=组件默认5%")}},
		{ID: "risk_trailing_stop", Name: "追踪止损", Role: roleRisk, Category: "止损止盈",
			Desc:   "从买入后最高点回撤超过阈值卖出",
			Params: []ParamDef{ratioParam("drawdown", "回撤阈值", 5, 0, 1000, 0.5, "0=组件默认5%")}},
		{ID: "risk_atr_stop", Name: "ATR止损", Role: roleRisk, Category: "止损止盈",
			Desc: "收盘价低于买入价减 Multiple×ATR 时止损",
			Params: []ParamDef{
				intParam("period", "ATR周期", 14, 2, 500, ""),
				floatParam("multiple", "ATR倍数", "倍", 2.5, 0.1, 100, 0.1, "0=组件默认2.5"),
			}},
		{ID: "risk_profit", Name: "止盈", Role: roleRisk, Category: "止损止盈",
			Desc:   "收盘盈利达到比例即卖出",
			Params: []ParamDef{ratioParam("pct", "止盈比例", 5, 0, 1000, 0.5, "0=组件默认5%")}},
		{ID: "risk_fixed_tp", Name: "固定止盈", Role: roleRisk, Category: "止损止盈",
			Desc:   "最新收盘价相对买入价的涨幅达到止盈比例即卖出；比例为 0 时使用 5%",
			Params: []ParamDef{ratioParam("pct", "止盈比例", 10, 0, 1000, 0.5, "0=组件默认值")}},
		{ID: "risk_trailing_takeprofit", Name: "移动止盈", Role: roleRisk, Category: "止损止盈",
			Desc: "盈利激活后回吐 RetreatPct 比例利润卖出",
			Params: []ParamDef{
				ratioParam("activateProfit", "激活盈利", 5, 0, 1000, 0.5, "0=组件默认5%"),
				ratioParam("retreat", "回吐比例", 50, 0, 100, 1, "0=组件默认50%"),
			}},
		{ID: "risk_atr_takeprofit", Name: "ATR止盈", Role: roleRisk, Category: "止损止盈",
			Desc: "收盘价高于买入价加 Multiple×ATR 时止盈",
			Params: []ParamDef{
				intParam("period", "ATR周期", 14, 2, 500, ""),
				floatParam("multiple", "ATR倍数", "倍", 2.0, 0.1, 100, 0.1, "0=组件默认2.0"),
			}},

		// ---- 独立风控：持仓期限 ----
		{ID: "risk_holding_days", Name: "持仓N天强平", Role: roleRisk, Category: "持仓期限",
			Desc:   "买入后满指定交易日数强制卖出，不含买入当天",
			Params: []ParamDef{intParam("days", "持仓天数", 5, 1, 1000, "")}},
		{ID: "risk_holding_days_tail", Name: "持仓N天尾盘卖", Role: roleRisk, Category: "持仓期限",
			Desc: "持仓第 N 天在指定尾盘时间卖出（需分钟线）",
			Params: []ParamDef{
				intParam("days", "持仓天数", 1, 1, 1000, ""),
				{Key: "time", Label: "卖出时间", Type: "text", Default: "14:55:00",
					Pattern: `^([01]\d|2[0-3]):[0-5]\d:[0-5]\d$`, Desc: "HH:MM:SS"},
			}},

		// ---- 独立风控：买入价防线 ----
		{ID: "risk_break_buy_price", Name: "次日跌破买入价", Role: roleRisk, Category: "买入价防线",
			Desc: "买入次日收盘低于买入价即卖出"},
		{ID: "risk_no_gap_up", Name: "次日未高开", Role: roleRisk, Category: "买入价防线",
			Desc: "买入次日开盘不高于买入价即卖出"},
		{ID: "risk_break_buy_open", Name: "跌破买入日开盘", Role: roleRisk, Category: "买入价防线",
			Desc: "收盘低于买入日开盘价即卖出"},
	}
}

// builderUnavailable 列出暂未接入的公开组件及原因（§3：不静默忽略）。
func builderUnavailable() []UnavailableComponent {
	return []UnavailableComponent{
		{ID: "buy.A均线向上", Name: "均线向上(旧)", Role: roleBuy,
			Reason: "与「均线向上」(buy_ma_up) 语义重复，仅回看缺省不同；请使用 buy_ma_up 并显式设置回看天数"},
		{ID: "buy.A阳线", Name: "阳线", Role: roleBuy, Reason: "与「实体阳线」实体比例 0 等价"},
		{ID: "buy.A单日涨幅小于", Name: "单日涨幅小于", Role: roleBuy, Reason: "本次未接入；可用「单日涨幅范围」上限表达"},
		{ID: "buy.A突破N天新高", Name: "突破N天新高", Role: roleBuy, Reason: "与「突破N日新高」重复"},
		{ID: "buy.A近N天最高价", Name: "近N天最高价", Role: roleBuy, Reason: "本次未接入"},
		{ID: "buy.A通达信倍量", Name: "通达信倍量", Role: roleBuy, Reason: "本次未接入"},
		{ID: "buy.BuyCloseBelowMA", Name: "BuyCloseBelowMA", Role: roleBuy, Reason: "旧英文命名组件，待接入"},
		{ID: "buy.BreakMA", Name: "BreakMA", Role: roleBuy, Reason: "旧英文命名组件，待接入"},
		{ID: "buy.A阴线收回", Name: "阴线收回", Role: roleBuy, Reason: "本次未接入"},
		{ID: "buy.A下跌企稳倍量", Name: "下跌企稳倍量", Role: roleBuy, Reason: "本次未接入"},
		{ID: "buy.A底顶部抬升", Name: "底顶部抬升", Role: roleBuy, Reason: "与「底部抬升」接近，语义待确认"},
		{ID: "buy.A长期均线多头", Name: "长期均线多头", Role: roleBuy, Reason: "蓝筹回调族组件，本次未接入"},
		{ID: "buy.A收盘价站上长期均线", Name: "收盘价站上长期均线", Role: roleBuy, Reason: "蓝筹回调族组件，本次未接入"},
		{ID: "buy.A浅回调至均线", Name: "浅回调至均线", Role: roleBuy, Reason: "蓝筹回调族组件，本次未接入"},
		{ID: "buy.A企稳信号", Name: "企稳信号", Role: roleBuy, Reason: "蓝筹回调族组件，本次未接入"},
		{ID: "buy.A缩量企稳", Name: "缩量企稳", Role: roleBuy, Reason: "蓝筹回调族组件，本次未接入"},
		{ID: "buy.A年线向上", Name: "年线向上", Role: roleBuy, Reason: "蓝筹回调族组件，本次未接入"},
		{ID: "buy.A非连续下跌", Name: "非连续下跌", Role: roleBuy, Reason: "蓝筹回调族组件，本次未接入"},
		{ID: "buy.MACD正数缓降最低点", Name: "MACD正数缓降最低点", Role: roleBuy, Reason: "本次未接入"},
		{ID: "buy.MACD平滑", Name: "MACD平滑", Role: roleBuy, Reason: "本次未接入"},
		{ID: "buy.MACD顺滑", Name: "MACD顺滑", Role: roleBuy, Reason: "本次未接入"},
		{ID: "buy.MACD转红", Name: "MACD转红", Role: roleBuy, Reason: "本次未接入；可用「MACD金叉」表达类似形态"},
		{ID: "buy.MACD负柱缩短", Name: "MACD负柱缩短", Role: roleBuy, Reason: "本次未接入"},
		{ID: "buy.A上证N日均线向上", Name: "上证N日均线向上", Role: roleBuy, Reason: "需要指数行情数据，Lab 样本回测暂不提供"},
		{ID: "buy.A指数多头排列", Name: "指数多头排列", Role: roleBuy, Reason: "需要指数行情数据，Lab 样本回测暂不提供"},
		{ID: "buy.A指数站上MA", Name: "指数站上MA", Role: roleBuy, Reason: "需要指数行情数据，Lab 样本回测暂不提供"},
		{ID: "buy.N天前符合", Name: "N天前符合", Role: roleBuy, Reason: "嵌套 Buyer 包装组件；条件树已原生支持分组，无需该包装"},
		{ID: "buy.A因子过滤", Name: "因子过滤（旧组件）", Role: roleBuy, Reason: "请使用「因子条件」节点，买入与卖出共用同一份因子定义"},
		{ID: "buy.A因子TopN", Name: "因子TopN", Role: roleBuy, Reason: "属于「组合研究」模式，不在条件树接入"},
		{ID: "sell.SellRSI", Name: "SellRSI", Role: roleSell, Reason: "扩展 Seller 接口（含分钟线回调），不兼容 sell.Or 组合；请用「RSI恢复」"},
		{ID: "sell.SellAt", Name: "定时卖出", Role: roleRisk, Reason: "扩展 Seller 接口，不兼容组合；请用「持仓N天尾盘卖」"},
		{ID: "sell.SellTPSL", Name: "止盈止损(旧)", Role: roleRisk, Reason: "旧版会回看买入日之前历史数据，请用「止盈止损」"},
		{ID: "sell.A缩量滞涨", Name: "缩量滞涨", Role: roleSell, Reason: "本次未接入"},
		{ID: "sell.A时间止损", Name: "时间止损", Role: roleRisk, Reason: "与「持仓N天强平」重复，语义待确认"},
	}
}

// CatalogResponse 汇总目录响应。
type CatalogResponse struct {
	Components  []ComponentEntry       `json:"components"`
	Unavailable []UnavailableComponent `json:"unavailable"`
}

func Catalog() CatalogResponse {
	return CatalogResponse{Components: builderCatalog(), Unavailable: builderUnavailable()}
}

// builderComponent 按角色与 ID 查找目录条目。
func builderComponent(id string) (ComponentEntry, bool) {
	for _, c := range builderCatalog() {
		if c.ID == id {
			return c, true
		}
	}
	return ComponentEntry{}, false
}

// buyFactories 组件 ID → 买入组件工厂（参数已在 spec 校验层做过类型检查）。
var buyFactories = map[string]func(map[string]any) (core.Buyer, error){
	"buy_all":       func(map[string]any) (core.Buyer, error) { return sb.A全部{}, nil },
	"buy_board_cyb": func(map[string]any) (core.Buyer, error) { return sb.A创业板{}, nil },
	"buy_board_kcb": func(map[string]any) (core.Buyer, error) { return sb.A科创板{}, nil },
	"buy_board_bj":  func(map[string]any) (core.Buyer, error) { return sb.A北证板{}, nil },
	"buy_etf":       func(map[string]any) (core.Buyer, error) { return sb.ETF{}, nil },
	"buy_price": func(p map[string]any) (core.Buyer, error) {
		min, err := floatArg(p, "min")
		if err != nil {
			return nil, err
		}
		max, err := floatArg(p, "max")
		return sb.A价格{Min: min, Max: max}, err
	},
	"buy_market_cap": func(p map[string]any) (core.Buyer, error) {
		min, err := floatArg(p, "min")
		if err != nil {
			return nil, err
		}
		max, err := floatArg(p, "max")
		return sb.A流通市值{Min: min, Max: max}, err
	},
	"buy_price_above_ma": func(p map[string]any) (core.Buyer, error) {
		v, err := intArg(p, "period")
		return sb.A现价大于N日均线(v), err
	},
	"buy_stand_above_ma": func(p map[string]any) (core.Buyer, error) {
		period, err := intArg(p, "period")
		if err != nil {
			return nil, err
		}
		days, err := intArg(p, "days")
		if err != nil {
			return nil, err
		}
		return sb.A站上N日均线{Period: period, Days: days}, nil
	},
	"buy_close_above_ma": func(p map[string]any) (core.Buyer, error) {
		v, err := intArg(p, "period")
		return sb.A收盘高于均线{Period: v}, err
	},
	"buy_ma_up": func(p map[string]any) (core.Buyer, error) {
		period, err := intArg(p, "period")
		if err != nil {
			return nil, err
		}
		lookback, err := intArg(p, "lookback")
		if err != nil {
			return nil, err
		}
		slope, err := ratioArg(p, "minSlope")
		if err != nil {
			return nil, err
		}
		// Lookback 缺省必须保持 0：组件内部 0→5，MACD 模板等价依赖此行为
		return sb.MAUp{Period: period, Lookback: lookback, MinSlope: slope}, nil
	},
	"buy_ma_bull": func(p map[string]any) (core.Buyer, error) {
		v, err := intListArg(p, "periods")
		return sb.A均线多头排列(v), err
	},
	"buy_pullback_ma": func(p map[string]any) (core.Buyer, error) {
		period, err := intArg(p, "period")
		if err != nil {
			return nil, err
		}
		support, err := intArg(p, "supportPeriod")
		if err != nil {
			return nil, err
		}
		touch, err := intArg(p, "minTouchDays")
		if err != nil {
			return nil, err
		}
		return sb.A回踩N日均线{Period: period, SupportPeriod: support, MinTouchDays: touch}, nil
	},
	"buy_macd_negative": func(p map[string]any) (core.Buyer, error) {
		f, s, g, err := macdArgs(p)
		if err != nil {
			return nil, err
		}
		minDays, err := intArg(p, "minDays")
		if err != nil {
			return nil, err
		}
		maxDays, err := intArg(p, "maxDays")
		return sb.MACD负数{Fast: f, Slow: s, Signal: g, MinDays: minDays, MaxDays: maxDays}, err
	},
	"buy_macd_rising": func(p map[string]any) (core.Buyer, error) {
		f, s, g, err := macdArgs(p)
		if err != nil {
			return nil, err
		}
		minDays, err := intArg(p, "minDays")
		if err != nil {
			return nil, err
		}
		maxDays, err := intArg(p, "maxDays")
		if err != nil {
			return nil, err
		}
		lookback, err := intArg(p, "lookback")
		if err != nil {
			return nil, err
		}
		return sb.MACD连涨{Fast: f, Slow: s, Signal: g, MinDays: minDays, MaxDays: maxDays, Lookback: lookback}, nil
	},
	"buy_macd_positive": func(p map[string]any) (core.Buyer, error) {
		f, s, g, err := macdArgs(p)
		if err != nil {
			return nil, err
		}
		days, err := intArg(p, "days")
		return sb.MACD正数{Fast: f, Slow: s, Signal: g, Days: days}, err
	},
	"buy_macd_reversal": func(p map[string]any) (core.Buyer, error) {
		f, s, g, err := macdArgs(p)
		if err != nil {
			return nil, err
		}
		minLb, err := intArg(p, "minLookback")
		if err != nil {
			return nil, err
		}
		maxLb, err := intArg(p, "maxLookback")
		if err != nil {
			return nil, err
		}
		diff, err := floatArg(p, "minDiff")
		return sb.MACD反转{Fast: f, Slow: s, Signal: g, MinLookback: minLb, MaxLookback: maxLb, MinDiff: diff}, err
	},
	"buy_macd_golden_cross": func(p map[string]any) (core.Buyer, error) {
		f, s, g, err := macdArgs(p)
		return sb.MACD金叉{Fast: f, Slow: s, Signal: g}, err
	},
	"buy_macd_above_zero": func(p map[string]any) (core.Buyer, error) {
		f, s, g, err := macdArgs(p)
		return sb.MACD零轴上方{Fast: f, Slow: s, Signal: g}, err
	},
	"buy_rsi_low": func(p map[string]any) (core.Buyer, error) {
		period, err := intArg(p, "period")
		if err != nil {
			return nil, err
		}
		threshold, err := floatArg(p, "threshold")
		return sb.RSI{Period: period, Threshold: threshold}, err
	},
	"buy_rsi_turn_up": func(p map[string]any) (core.Buyer, error) {
		v, err := intArg(p, "period")
		return sb.RSI拐头{Period: v}, err
	},
	"buy_rsi_range": func(p map[string]any) (core.Buyer, error) {
		period, err := intArg(p, "period")
		if err != nil {
			return nil, err
		}
		min, err := floatArg(p, "min")
		if err != nil {
			return nil, err
		}
		max, err := floatArg(p, "max")
		return sb.RSI区间{Period: period, Min: min, Max: max}, err
	},
	"buy_rsi_oversold_rebound": func(p map[string]any) (core.Buyer, error) {
		period, err := intArg(p, "period")
		if err != nil {
			return nil, err
		}
		threshold, err := floatArg(p, "threshold")
		return sb.RSI超卖回升{Period: period, Threshold: threshold}, err
	},
	"buy_volume_surge": func(p map[string]any) (core.Buyer, error) {
		period, err := intArg(p, "period")
		if err != nil {
			return nil, err
		}
		ratio, err := floatArg(p, "ratio")
		return sb.A成交量放大{Period: period, Ratio: ratio}, err
	},
	"buy_volume_double": func(p map[string]any) (core.Buyer, error) {
		min, err := floatArg(p, "minRatio")
		if err != nil {
			return nil, err
		}
		max, err := floatArg(p, "maxRatio")
		if err != nil {
			return nil, err
		}
		base, err := intArg(p, "baseDays")
		return sb.A倍量{MinRatio: min, MaxRatio: max, BaseDays: base}, err
	},
	"buy_volume_shrink": func(p map[string]any) (core.Buyer, error) {
		days, err := intArg(p, "days")
		if err != nil {
			return nil, err
		}
		ratio, err := floatArg(p, "ratio")
		return sb.A缩量{Days: days, Ratio: ratio}, err
	},
	"buy_turnover_min": func(p map[string]any) (core.Buyer, error) {
		v, err := floatArg(p, "min")
		return sb.A成交额大于(v), err
	},
	"buy_limit_up_hit":        func(map[string]any) (core.Buyer, error) { return sb.A涨停{}, nil },
	"buy_filter_limit_up":     func(map[string]any) (core.Buyer, error) { return sb.A过滤涨停{}, nil },
	"buy_close_gt_prev_close": func(map[string]any) (core.Buyer, error) { return sb.A收盘大于昨收{}, nil },
	"buy_close_gt_prev_open":  func(map[string]any) (core.Buyer, error) { return sb.A收盘大于昨开{}, nil },
	"buy_break_high": func(p map[string]any) (core.Buyer, error) {
		v, err := intArg(p, "period")
		return sb.A突破N日高点{Period: v}, err
	},
	"buy_solid_bull": func(p map[string]any) (core.Buyer, error) {
		v, err := ratioArg(p, "minBodyRatio")
		return sb.A实体阳线{MinBodyRatio: v}, err
	},
	"buy_solid_bear": func(p map[string]any) (core.Buyer, error) {
		v, err := ratioArg(p, "minBodyRatio")
		return sb.A实体阴线{MinBodyRatio: v}, err
	},
	"buy_recent_gain_below": func(p map[string]any) (core.Buyer, error) {
		days, err := intArg(p, "days")
		if err != nil {
			return nil, err
		}
		max, err := floatArg(p, "max")
		return sb.A近N日涨幅小于{Days: days, Max: max}, err
	},
	"buy_day_gain_range": func(p map[string]any) (core.Buyer, error) {
		min, err := floatArg(p, "min")
		if err != nil {
			return nil, err
		}
		max, err := floatArg(p, "max")
		return sb.A单日涨幅范围{Min: min, Max: max}, err
	},
	"buy_bottom_up": func(p map[string]any) (core.Buyer, error) {
		v, err := intArg(p, "window")
		return sb.A底部抬升{Window: v}, err
	},
	"buy_atr_range": func(p map[string]any) (core.Buyer, error) {
		period, err := intArg(p, "period")
		if err != nil {
			return nil, err
		}
		min, err := floatArg(p, "minPct")
		if err != nil {
			return nil, err
		}
		max, err := floatArg(p, "maxPct")
		return sb.ATR波动率范围{Period: period, MinPct: min, MaxPct: max}, err
	},
	"buy_boll_lower": func(p map[string]any) (core.Buyer, error) {
		period, err := intArg(p, "period")
		if err != nil {
			return nil, err
		}
		std, err := floatArg(p, "stdTimes")
		return sb.A布林下轨{Period: period, StdTimes: std}, err
	},
	"buy_boll_lower_volume": func(p map[string]any) (core.Buyer, error) {
		period, err := intArg(p, "period")
		if err != nil {
			return nil, err
		}
		std, err := floatArg(p, "stdTimes")
		if err != nil {
			return nil, err
		}
		days, err := intArg(p, "volDays")
		if err != nil {
			return nil, err
		}
		ratio, err := floatArg(p, "volRatio")
		return sb.A布林下轨缩量{Period: period, StdTimes: std, VolDays: days, VolRatio: ratio}, err
	},
	"buy_boll_lower_rsi": func(p map[string]any) (core.Buyer, error) {
		period, err := intArg(p, "period")
		if err != nil {
			return nil, err
		}
		std, err := floatArg(p, "stdTimes")
		if err != nil {
			return nil, err
		}
		rp, err := intArg(p, "rsiPeriod")
		if err != nil {
			return nil, err
		}
		rt, err := floatArg(p, "rsiThreshold")
		return sb.A布林下轨RSI超卖{Period: period, StdTimes: std, RSIPeriod: rp, RSIThreshold: rt}, err
	},
	"buy_bias_oversold": func(p map[string]any) (core.Buyer, error) {
		period, err := intArg(p, "period")
		if err != nil {
			return nil, err
		}
		bias, err := floatArg(p, "minBias")
		return sb.A乖离超卖{Period: period, MinBias: bias}, err
	},
	"buy_zscore_oversold": func(p map[string]any) (core.Buyer, error) {
		period, err := intArg(p, "period")
		if err != nil {
			return nil, err
		}
		minZ, err := floatArg(p, "minZ")
		return sb.AZScore超卖{Period: period, MinZ: minZ}, err
	},
	"buy_donchian_lower": func(p map[string]any) (core.Buyer, error) {
		v, err := intArg(p, "period")
		return sb.A唐奇安下沿{Period: v}, err
	},
}

// sellFactories 组件 ID → 卖出组件工厂（策略卖出信号与独立风控共用）。
var sellFactories = map[string]func(map[string]any) (core.Seller, error){
	"sell_macd_reversal": func(p map[string]any) (core.Seller, error) {
		f, s, g, err := macdArgs(p)
		if err != nil {
			return nil, err
		}
		lookback, err := intArg(p, "lookback")
		if err != nil {
			return nil, err
		}
		diff, err := floatArg(p, "minDiff")
		return ss.MACD反转{Fast: f, Slow: s, Signal: g, Lookback: lookback, MinDiff: diff}, err
	},
	"sell_macd_down_after_buy": func(p map[string]any) (core.Seller, error) {
		f, s, g, err := macdArgs(p)
		if err != nil {
			return nil, err
		}
		after, err := intArg(p, "afterDays")
		if err != nil {
			return nil, err
		}
		days, err := intArg(p, "days")
		return ss.MACD买入后连跌{Fast: f, Slow: s, Signal: g, AfterDays: after, Days: days}, err
	},
	"sell_macd_dead_cross_negative": func(p map[string]any) (core.Seller, error) {
		f, s, g, err := macdArgs(p)
		return ss.A_MACD死叉且DIF为负{Fast: f, Slow: s, Signal: g}, err
	},
	"sell_break_ma": func(p map[string]any) (core.Seller, error) {
		period, err := intArg(p, "period")
		if err != nil {
			return nil, err
		}
		days, err := intArg(p, "days")
		return ss.A收盘跌破均线{Period: period, Days: days}, err
	},
	"sell_below_short_ma": func(p map[string]any) (core.Seller, error) {
		v, err := intArg(p, "period")
		return ss.A跌破短期均线{Period: v}, err
	},
	"sell_ma_dead_cross": func(p map[string]any) (core.Seller, error) {
		short, err := intArg(p, "short")
		if err != nil {
			return nil, err
		}
		long, err := intArg(p, "long")
		return ss.A均线死叉{Short: short, Long: long}, err
	},
	"sell_long_ma_down": func(p map[string]any) (core.Seller, error) {
		v, err := intArg(p, "lookback")
		return ss.LongMADown{Lookback: v}, err
	},
	"sell_back_to_ma": func(p map[string]any) (core.Seller, error) {
		v, err := intArg(p, "period")
		return ss.A回到均线{Period: v}, err
	},
	"sell_rsi_overbought": func(p map[string]any) (core.Seller, error) {
		period, err := intArg(p, "period")
		if err != nil {
			return nil, err
		}
		threshold, err := floatArg(p, "threshold")
		return ss.A_RSI超买{Period: period, Threshold: threshold}, err
	},
	"sell_rsi_recover": func(p map[string]any) (core.Seller, error) {
		period, err := intArg(p, "period")
		if err != nil {
			return nil, err
		}
		threshold, err := floatArg(p, "threshold")
		return ss.ARSI恢复{Period: period, Threshold: threshold}, err
	},
	"sell_back_to_boll_mid": func(p map[string]any) (core.Seller, error) {
		v, err := intArg(p, "period")
		return ss.A回到布林中轨{Period: v}, err
	},
	"sell_bias_zero": func(p map[string]any) (core.Seller, error) {
		v, err := intArg(p, "period")
		return ss.A乖离归零{Period: v}, err
	},
	"sell_zscore_zero": func(p map[string]any) (core.Seller, error) {
		v, err := intArg(p, "period")
		return ss.AZScore归零{Period: v}, err
	},
	"sell_back_to_donchian_mid": func(p map[string]any) (core.Seller, error) {
		v, err := intArg(p, "period")
		return ss.A回到唐奇安中轨{Period: v}, err
	},
	"sell_big_fall": func(p map[string]any) (core.Seller, error) {
		v, err := floatArg(p, "max")
		return ss.A单日跌幅大于{Max: v}, err
	},
	"sell_volume_big_bear": func(p map[string]any) (core.Seller, error) {
		ratio, err := floatArg(p, "volumeRatio")
		if err != nil {
			return nil, err
		}
		fall, err := floatArg(p, "fallPct")
		return ss.A放量大阴线{VolumeRatio: ratio, FallPct: fall}, err
	},
	"risk_takeprofit_stoploss": func(p map[string]any) (core.Seller, error) {
		tp, err := ratioArg(p, "takeProfit")
		if err != nil {
			return nil, err
		}
		sl, err := ratioArg(p, "stopLoss")
		return ss.A止盈止损{TakeProfit: tp, StopLoss: sl}, err
	},
	"risk_fixed_stop": func(p map[string]any) (core.Seller, error) {
		v, err := ratioArg(p, "pct")
		return ss.A固定止损{Pct: v}, err
	},
	"risk_trailing_stop": func(p map[string]any) (core.Seller, error) {
		v, err := ratioArg(p, "drawdown")
		return ss.A追踪止损{Drawdown: v}, err
	},
	"risk_atr_stop": func(p map[string]any) (core.Seller, error) {
		period, err := intArg(p, "period")
		if err != nil {
			return nil, err
		}
		mul, err := floatArg(p, "multiple")
		return ss.A_ATR止损{Period: period, Multiple: mul}, err
	},
	"risk_profit": func(p map[string]any) (core.Seller, error) {
		v, err := ratioArg(p, "pct")
		return ss.A盈利(v), err
	},
	"risk_fixed_tp": func(p map[string]any) (core.Seller, error) {
		v, err := ratioArg(p, "pct")
		return ss.A固定止盈(v), err
	},
	"risk_trailing_takeprofit": func(p map[string]any) (core.Seller, error) {
		activate, err := ratioArg(p, "activateProfit")
		if err != nil {
			return nil, err
		}
		retreat, err := ratioArg(p, "retreat")
		return ss.A移动止盈{ActivateProfitPct: activate, RetreatPct: retreat}, err
	},
	"risk_atr_takeprofit": func(p map[string]any) (core.Seller, error) {
		period, err := intArg(p, "period")
		if err != nil {
			return nil, err
		}
		mul, err := floatArg(p, "multiple")
		return ss.A_ATR止盈{Period: period, Multiple: mul}, err
	},
	"risk_holding_days": func(p map[string]any) (core.Seller, error) {
		v, err := intArg(p, "days")
		return ss.A持仓N天{Days: v}, err
	},
	"risk_holding_days_tail": func(p map[string]any) (core.Seller, error) {
		days, err := intArg(p, "days")
		if err != nil {
			return nil, err
		}
		ts, err := strArg(p, "time")
		return ss.A持仓N天尾盘{Days: days, Time: ts}, err
	},
	"risk_break_buy_price": func(map[string]any) (core.Seller, error) { return ss.A买入次日跌破买入价{}, nil },
	"risk_no_gap_up":       func(map[string]any) (core.Seller, error) { return ss.A买入次日未高开{}, nil },
	"risk_break_buy_open":  func(map[string]any) (core.Seller, error) { return ss.A跌破买入日开盘{}, nil },
}

// ---------------------------------------------------------------------------
// 参数提取助手：JSON 解码后数字均为 float64，这里统一做类型收敛。
// 范围校验在 spec 校验层按 ParamDef 执行；工厂只做类型收敛与缺省。
// ---------------------------------------------------------------------------

// numArg 取数值参数（key 缺省返回 0）。
func numArg(p map[string]any, key string) (float64, bool, error) {
	v, ok := p[key]
	if !ok || v == nil {
		return 0, false, nil
	}
	f, ok := v.(float64)
	if !ok {
		return 0, false, fmt.Errorf("参数 %s 须为数字", key)
	}
	return f, true, nil
}

// floatArg 浮点参数。
func floatArg(p map[string]any, key string) (float64, error) {
	v, _, err := numArg(p, key)
	return v, err
}

// ratioArg 比例参数：界面百分数 → 执行比例（5 → 0.05）。
func ratioArg(p map[string]any, key string) (float64, error) {
	v, _, err := numArg(p, key)
	return v / 100, err
}

// intArg 整数参数（非整数值报错）。
func intArg(p map[string]any, key string) (int, error) {
	v, ok, err := numArg(p, key)
	if err != nil || !ok {
		return 0, err
	}
	if v != float64(int(v)) {
		return 0, fmt.Errorf("参数 %s 须为整数", key)
	}
	return int(v), nil
}

// macdArgs MACD 三参数。
func macdArgs(p map[string]any) (fast, slow, signal int, err error) {
	if fast, err = intArg(p, "fast"); err != nil {
		return 0, 0, 0, err
	}
	if slow, err = intArg(p, "slow"); err != nil {
		return 0, 0, 0, err
	}
	if signal, err = intArg(p, "signal"); err != nil {
		return 0, 0, 0, err
	}
	return fast, slow, signal, nil
}

// strArg 文本参数。
func strArg(p map[string]any, key string) (string, error) {
	v, ok := p[key]
	if !ok || v == nil {
		return "", nil
	}
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("参数 %s 须为文本", key)
	}
	return s, nil
}

// intListArg 整数列表参数。
func intListArg(p map[string]any, key string) ([]int, error) {
	v, ok := p[key]
	if !ok || v == nil {
		return nil, nil
	}
	arr, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("参数 %s 须为数组", key)
	}
	out := make([]int, 0, len(arr))
	for i, item := range arr {
		f, ok := item.(float64)
		if !ok || f != float64(int(f)) {
			return nil, fmt.Errorf("参数 %s 第 %d 项须为整数", key, i+1)
		}
		out = append(out, int(f))
	}
	return out, nil
}
