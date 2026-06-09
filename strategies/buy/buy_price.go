package buy

import (
	"fmt"

	"github.com/injoyai/tdx/extend"
	"github.com/injoyai/tdx/protocol"
)

// A现价 是价格区间过滤条件。
// Min 表示允许买入的最低价格，单位为元；Min 为 0 时不限制最低价格。
// Max 表示允许买入的最高价格，单位为元；Max 为 0 时不限制最高价格。
// 判断对象是最新交易日的收盘价，满足价格区间后返回买入信号。
// 适合作为 BuyAll 中的价格过滤条件，与其它形态、均线、成交量条件组合使用。
type A现价 struct {
	Min float64
	Max float64
}

func (b A现价) Name() string {
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

func (b A现价) Buy(code string, dks extend.Klines) bool {
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
