package factor

import (
	"fmt"
	"math"

	"github.com/injoyai/strategy-tail/lib/extend"
)

// N日波动 是近 N 日日收益率的总体标准差（衡量波动强度，值越大越剧烈）。
// 数据不足（len < Days+1）或窗口内出现零收盘价返回 NaN。
type N日波动 struct {
	Days int
}

func (f N日波动) Name() string {
	return fmt.Sprintf("N日波动(%d)", daysOr(f.Days, 20))
}

func (f N日波动) Value(code string, dks extend.Klines) float64 {
	n := daysOr(f.Days, 20)
	if len(dks) < n+1 {
		return math.NaN()
	}
	w := dks[len(dks)-n-1:]
	var sum float64
	rets := make([]float64, 0, n)
	for i := 1; i < len(w); i++ {
		prev := w[i-1].Close.Float64()
		if prev == 0 {
			return math.NaN()
		}
		r := w[i].Close.Float64()/prev - 1
		rets = append(rets, r)
		sum += r
	}
	mean := sum / float64(n)
	var ss float64
	for _, r := range rets {
		ss += (r - mean) * (r - mean)
	}
	return math.Sqrt(ss / float64(n))
}

// N日振幅 是近 N 日价格区间占比：(HHV(High,N) - LLV(Low,N)) / LLV(Low,N)。
// 数据不足（len < Days）或 LLV=0 返回 NaN。
type N日振幅 struct {
	Days int
}

func (f N日振幅) Name() string {
	return fmt.Sprintf("N日振幅(%d)", daysOr(f.Days, 20))
}

func (f N日振幅) Value(code string, dks extend.Klines) float64 {
	n := daysOr(f.Days, 20)
	if len(dks) < n {
		return math.NaN()
	}
	llv := dks.LLV(n).Float64()
	if llv == 0 {
		return math.NaN()
	}
	return (dks.HHV(n).Float64() - llv) / llv
}
