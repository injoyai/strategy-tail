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
