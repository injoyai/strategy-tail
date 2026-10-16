package factor

import (
	"fmt"
	"math"

	"github.com/injoyai/strategy-tail/lib/extend"
	"github.com/injoyai/tdx/protocol"
)

// A最高价 是近 N 日（含今日）最高价，0 默认 20 日，数值单位为元。
type A最高价 int

func (f A最高价) Name() string { return fmt.Sprintf("最高价(%d)", daysOr(int(f), 20)) }
func (f A最高价) Value(code string, dks extend.Klines) float64 {
	if len(dks) < daysOr(int(f), 20) {
		return math.NaN()
	}
	return f.Price(dks).Float64()
}

// Price 供条件保留整数价格比较；缺数据返回 0，调用方须先校验窗口。
func (f A最高价) Price(dks extend.Klines) protocol.Price {
	n := daysOr(int(f), 20)
	if len(dks) < n {
		return 0
	}
	return dks.HHV(n)
}

// A最低价 是近 N 日（含今日）最低价，沿用行情 LLV 的零值语义；0 默认 20 日。
type A最低价 int

func (f A最低价) Name() string { return fmt.Sprintf("最低价(%d)", daysOr(int(f), 20)) }
func (f A最低价) Value(code string, dks extend.Klines) float64 {
	if len(dks) < daysOr(int(f), 20) {
		return math.NaN()
	}
	return f.Price(dks).Float64()
}

// Price 供条件保留整数价格比较；缺数据返回 0，调用方须先校验窗口。
func (f A最低价) Price(dks extend.Klines) protocol.Price {
	n := daysOr(int(f), 20)
	if len(dks) < n {
		return 0
	}
	return dks.LLV(n)
}

// A均量 是近 N 日（含今日）的平均成交量，0 默认 5 日。
type A均量 int

func (f A均量) Name() string { return fmt.Sprintf("均量(%d)", daysOr(int(f), 5)) }
func (f A均量) Value(code string, dks extend.Klines) float64 {
	n := daysOr(int(f), 5)
	if len(dks) < n {
		return math.NaN()
	}
	var sum float64
	for _, k := range dks[len(dks)-n:] {
		sum += float64(k.Volume)
	}
	return sum / float64(n)
}
