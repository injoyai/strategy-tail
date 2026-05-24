package buy

import (
	"fmt"

	"github.com/injoyai/tdx/extend"
)

// Price 是价格区间过滤条件。
// Min 表示允许买入的最低价格，单位为元；Min 为 0 时不限制最低价格。
// Max 表示允许买入的最高价格，单位为元；Max 为 0 时不限制最高价格。
// 判断对象是最新交易日的收盘价，满足价格区间后返回买入信号。
// 适合作为 BuyAll 中的价格过滤条件，与其它形态、均线、成交量条件组合使用。
type Price struct {
	Min float64
	Max float64
}

func (b Price) Name() string {
	switch {
	case b.Min > 0 && b.Max > 0:
		return fmt.Sprintf("价格[%.1f-%.1f]", b.Min, b.Max)
	case b.Min > 0:
		return fmt.Sprintf("价格[%.1f-]", b.Min)
	case b.Max > 0:
		return fmt.Sprintf("价格[-%.1f]", b.Max)
	default:
		return "价格范围买入"
	}
}

func (b Price) Buy(code string, dks extend.Klines) bool {
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
