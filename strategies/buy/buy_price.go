package buy

import (
	"fmt"

	"github.com/injoyai/strategy-tail/lib/extend"
	"github.com/injoyai/tdx/protocol"
)

func A价格小于(f float64) A价格 {
	return A价格{Max: f}
}

func A价格大于(f float64) A价格 {
	return A价格{Min: f}
}

type A现价 = A价格

// A价格 是价格区间过滤条件。
// Min 表示允许买入的最低价格，单位为元；Min 为 0 时不限制最低价格。
// Max 表示允许买入的最高价格，单位为元；Max 为 0 时不限制最高价格。
// 判断对象是最新交易日的收盘价，满足价格区间后返回买入信号。
// 适合作为 BuyAll 中的价格过滤条件，与其它形态、均线、成交量条件组合使用。
type A价格 struct {
	Min float64
	Max float64
}

func (b A价格) Name() string {
	switch {
	case b.Min > 0 && b.Max > 0:
		return fmt.Sprintf("价格%.1f-%.1f元", b.Min, b.Max)
	case b.Min > 0:
		return fmt.Sprintf("价格大于%.1f元", b.Min)
	case b.Max > 0:
		return fmt.Sprintf("价格小于%.1f元", b.Max)
	default:
		return "价格范围买入"
	}
}

func (b A价格) Buy(code string, dks extend.Klines) bool {
	if len(dks) == 0 {
		return false
	}

	today := dks[len(dks)-1]
	price := today.Close.Float64()
	if b.Min > 0 && price < b.Min {
		return false
	}
	if b.Max > 0 && price > b.Max {
		return false
	}

	return true
}

// A过滤涨停 是过滤涨停买入条件。
// 当最新交易日涨幅达到或超过 MaxRiseRate 时返回 false，避免回测买入实际无法成交的涨停股票。
// 当前主程序只筛选 sh60 和 sz00，默认值按常见 10% 涨停制度设置；如需适配 ST 或 20cm 股票，可按需调整 MaxRiseRate。
type A过滤涨停 struct{}

func (b A过滤涨停) Name() string {
	return "过滤涨停"
}

func (b A过滤涨停) Buy(code string, dks extend.Klines) bool {
	if len(dks) == 0 {
		return false
	}

	code = protocol.AddPrefix(code)

	if len(code) != 8 {
		return false
	}

	switch code[:4] {
	case "sh60", "sz00":
		return dks[len(dks)-1].RiseRate() < 9.8
	case "sh68", "sz30", "bj92":
		return dks[len(dks)-1].RiseRate() < 19.8
	default:
		return false
	}
}

// A涨停 是过滤涨停买入条件。
// 当最新交易日涨幅达到或超过 MaxRiseRate 时返回 false，避免回测买入实际无法成交的涨停股票。
// 当前主程序只筛选 sh60 和 sz00，默认值按常见 10% 涨停制度设置；如需适配 ST 或 20cm 股票，可按需调整 MaxRiseRate。
type A涨停 struct{}

func (b A涨停) Name() string {
	return "过滤涨停"
}

func (b A涨停) Buy(code string, dks extend.Klines) bool {
	if len(dks) == 0 {
		return false
	}

	code = protocol.AddPrefix(code)

	if len(code) != 8 {
		return false
	}

	switch code[:4] {
	case "sh60", "sz00":
		return dks[len(dks)-1].RiseRate() >= 9.8
	case "sh68", "sz30", "bj92":
		return dks[len(dks)-1].RiseRate() >= 19.8
	default:
		return false
	}
}

// A单日涨幅小于 是当日涨幅小于指定值的买入条件。
// Max 表示最大允许涨幅（%），默认 9.5。
// 用于过滤接近涨停的股票。
type A单日涨幅小于 float64

func (b A单日涨幅小于) Name() string {
	max := b
	if max == 0 {
		max = 9.5
	}
	return fmt.Sprintf("涨幅小于%.1f%%", max)
}

func (b A单日涨幅小于) Buy(code string, dks extend.Klines) bool {
	max := float64(b)
	if max == 0 {
		max = 9.5
	}
	if len(dks) == 0 {
		return false
	}
	return dks[len(dks)-1].RiseRate() < max
}

// A近N日涨幅小于 是近N日累计涨幅小于指定值的买入条件。
// Days 表示统计天数，默认 5。
// Max 表示最大允许累计涨幅（%），默认 15。
// 用于过滤短期暴涨追高风险。
type A近N日涨幅小于 struct {
	Days int
	Max  float64
}

func (b A近N日涨幅小于) Name() string {
	days := b.Days
	if days == 0 {
		days = 5
	}
	max := b.Max
	if max == 0 {
		max = 15
	}
	return fmt.Sprintf("近%d日涨幅<%.0f%%", days, max)
}

func (b A近N日涨幅小于) Buy(code string, dks extend.Klines) bool {
	days := b.Days
	if days == 0 {
		days = 5
	}
	max := b.Max
	if max == 0 {
		max = 15
	}
	rise := riseRateNDays(dks, days)
	return rise < max
}

type A单日涨幅范围 struct {
	Min float64
	Max float64
}

func (b A单日涨幅范围) Name() string {
	switch {
	case b.Min != 0 && b.Max != 0:
		return fmt.Sprintf("涨幅%.1f%%-%.1f%%", b.Min, b.Max)
	case b.Min != 0:
		return fmt.Sprintf("涨幅大于%.1f%%", b.Min)
	case b.Max != 0:
		return fmt.Sprintf("涨幅小于%.1f%%", b.Max)
	default:
		return "涨幅范围买入"
	}
}

func (b A单日涨幅范围) Buy(code string, dks extend.Klines) bool {
	if len(dks) == 0 {
		return false
	}

	rise := dks[len(dks)-1].RiseRate()
	if b.Min != 0 && rise < b.Min {
		return false
	}
	if b.Max != 0 && rise > b.Max {
		return false
	}

	return true
}

// A突破N天新高 是当日最高价创近 N 个交易日新高的买入条件。
// N 表示回看窗口长度（含今天），默认 20。
// 触发条件：dks[n-1].High == HHV(High, N)，即今日最高价等于近 N 天最高价。
// 适合捕捉突破形态，作为趋势确认/突破入场过滤条件使用。
type A突破N天新高 int

func (b A突破N天新高) Name() string {
	days := int(b)
	if days == 0 {
		days = 20
	}
	return fmt.Sprintf("创近%d天新高", days)
}

func (b A突破N天新高) Buy(code string, dks extend.Klines) bool {
	days := int(b)
	if days == 0 {
		days = 20
	}
	if len(dks) < days {
		return false
	}
	today := dks[len(dks)-1]
	return today.High >= dks.HHV(days)
}
