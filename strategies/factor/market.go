package factor

import (
	"math"

	"github.com/injoyai/strategy-tail/lib/extend"
)

// 价格 返回行情原始价格（元），支持 open/close/high/low，空值默认 close。
type A价格 string

func (f A价格) Name() string { return "价格(" + string(f) + ")" }
func (f A价格) Value(code string, dks extend.Klines) float64 {
	if len(dks) == 0 {
		return math.NaN()
	}
	k := dks[len(dks)-1]
	switch f {
	case "", "close":
		return k.Close
	case "open":
		return k.Open
	case "high":
		return k.High
	case "low":
		return k.Low
	}
	return math.NaN()
}

type A成交量 struct{}

func (A成交量) Name() string { return "成交量" }
func (A成交量) Value(code string, dks extend.Klines) float64 {
	if len(dks) == 0 {
		return math.NaN()
	}
	return float64(dks[len(dks)-1].Volume)
}

type A成交额 struct{}

func (A成交额) Name() string { return "成交额(元)" }
func (A成交额) Value(code string, dks extend.Klines) float64 {
	if len(dks) == 0 {
		return math.NaN()
	}
	return dks[len(dks)-1].Amount
}

// 流通市值 保留原条件的行情市值口径（亿元），缺失股本产生的 0 不改写为估算值。
type A流通市值 struct{}

func (A流通市值) Name() string { return "流通市值(亿元)" }
func (A流通市值) Value(code string, dks extend.Klines) float64 {
	if len(dks) == 0 {
		return math.NaN()
	}
	return dks[len(dks)-1].FloatValue() / 1e8
}

// 单日涨幅 使用策略层的百分数口径，与 N日动量的原始比例不同。
type A单日涨幅 struct{}

func (A单日涨幅) Name() string { return "单日涨幅(%)" }
func (A单日涨幅) Value(code string, dks extend.Klines) float64 {
	if len(dks) == 0 {
		return math.NaN()
	}
	rate, ok := dks[len(dks)-1].RisePercent()
	if !ok {
		return math.NaN()
	}
	return rate
}

// 实体振幅占比 是 |Close-Open|/(High-Low)，与以开盘价为分母的“实体幅度”不同。
type A实体振幅占比 struct{}

func (A实体振幅占比) Name() string { return "实体振幅占比" }
func (A实体振幅占比) Value(code string, dks extend.Klines) float64 {
	if len(dks) == 0 {
		return math.NaN()
	}
	k := dks[len(dks)-1]
	rangeValue := k.High - k.Low
	if rangeValue <= 0 {
		return math.NaN()
	}
	return math.Abs(k.Close-k.Open) / rangeValue
}
