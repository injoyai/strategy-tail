package factor

import (
	"math"

	"github.com/injoyai/strategy-tail/lib/extend"
)

// 实体幅度 是K线实体占比：|close - open| / open。
// 空数据或 open=0 返回 NaN。
type 实体幅度 struct{}

func (f 实体幅度) Name() string { return "实体幅度" }

func (f 实体幅度) Value(code string, dks extend.Klines) float64 {
	if len(dks) == 0 {
		return math.NaN()
	}
	k := dks[len(dks)-1]
	o := k.Open.Float64()
	if o == 0 {
		return math.NaN()
	}
	return math.Abs(k.Close.Float64()-o) / o
}

// 上影占比 是上影线占全幅比例：(high - max(open, close)) / (high - low)。
// 空数据返回 NaN；一字线（high=low）返回 0。
type 上影占比 struct{}

func (f 上影占比) Name() string { return "上影占比" }

func (f 上影占比) Value(code string, dks extend.Klines) float64 {
	if len(dks) == 0 {
		return math.NaN()
	}
	k := dks[len(dks)-1]
	h, l := k.High.Float64(), k.Low.Float64()
	if h == l {
		return 0
	}
	top := k.Open.Float64()
	if c := k.Close.Float64(); c > top {
		top = c
	}
	return (h - top) / (h - l)
}

// 下影占比 是下影线占全幅比例：(min(open, close) - low) / (high - low)。
// 空数据返回 NaN；一字线（high=low）返回 0。
type 下影占比 struct{}

func (f 下影占比) Name() string { return "下影占比" }

func (f 下影占比) Value(code string, dks extend.Klines) float64 {
	if len(dks) == 0 {
		return math.NaN()
	}
	k := dks[len(dks)-1]
	h, l := k.High.Float64(), k.Low.Float64()
	if h == l {
		return 0
	}
	bot := k.Open.Float64()
	if c := k.Close.Float64(); c < bot {
		bot = c
	}
	return (bot - l) / (h - l)
}

// 全幅占比 是当日高低全幅相对昨收的比例：(High - Low) / 昨收。
// 输出为小数比例（0.05 表示当日全幅约为昨收的 5%），衡量单日波幅强度。
// 无参数；只取最近 2 根K线（今日与昨收）。
// 最少历史 len >= 2，不足或昨收为 0 返回 NaN。
// 缺失/退化处理：一字线（High=Low）返回 0（合法值）。
type 全幅占比 struct{}

func (f 全幅占比) Name() string { return "全幅占比" }

func (f 全幅占比) Value(code string, dks extend.Klines) float64 {
	if len(dks) < 2 {
		return math.NaN()
	}
	prev := dks[len(dks)-2].Close.Float64()
	if prev == 0 {
		return math.NaN()
	}
	k := dks[len(dks)-1]
	return (k.High.Float64() - k.Low.Float64()) / prev
}

// K线重心 是收盘价在当日高低区间中的相对位置：(2·Close - High - Low) / (High - Low)。
// 输出为无量纲分数，取值 [-1, 1]：1 表示收在最高价，-1 表示收在最低价，0 为区间中点。
// 无参数；只取最近 1 根K线。
// 最少历史 len >= 1，空数据返回 NaN。
// 缺失/退化处理：一字线（High=Low）返回 0（合法值）。
type K线重心 struct{}

func (f K线重心) Name() string { return "K线重心" }

func (f K线重心) Value(code string, dks extend.Klines) float64 {
	if len(dks) == 0 {
		return math.NaN()
	}
	k := dks[len(dks)-1]
	h, l := k.High.Float64(), k.Low.Float64()
	if h == l {
		return 0
	}
	return (2*k.Close.Float64() - h - l) / (h - l)
}
