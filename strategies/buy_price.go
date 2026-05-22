package strategies

import (
	"fmt"

	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/tdx/extend"
	"github.com/injoyai/tdx/protocol"
)

// BuyPrice 是价格区间过滤条件。
// Min 表示允许买入的最低价格，单位为元；Min 为 0 时不限制最低价格。
// Max 表示允许买入的最高价格，单位为元；Max 为 0 时不限制最高价格。
// 判断对象是最新交易日的收盘价，满足价格区间后返回买入信号。
// 适合作为 BuyAll 中的价格过滤条件，与其它形态、均线、成交量条件组合使用。
type BuyPrice struct {
	Min float64
	Max float64
}

func (b BuyPrice) Name() string {
	switch {
	case b.Min > 0 && b.Max > 0:
		return fmt.Sprintf("价格%.2f-%.2f买入", b.Min, b.Max)
	case b.Min > 0:
		return fmt.Sprintf("价格大于%.2f买入", b.Min)
	case b.Max > 0:
		return fmt.Sprintf("价格小于%.2f买入", b.Max)
	default:
		return "价格范围买入"
	}
}

func (b BuyPrice) Buy(code string, dks extend.Klines, mks protocol.Klines) *core.Buy {
	if len(dks) == 0 {
		return nil
	}

	today := dks[len(dks)-1]
	price := today.Close.Float64()
	if b.Min > 0 && price < b.Min {
		return nil
	}
	if b.Max > 0 && price > b.Max {
		return nil
	}

	return &core.Buy{
		Code:  code,
		Time:  today.Time,
		Price: today.Close,
	}
}
