package factor

import (
	"fmt"
	"math"

	"github.com/injoyai/strategy-tail/lib/extend"
)

// Pearson 计算两个等长序列的皮尔逊相关系数。
// 长度为 0、长度不等或任一侧零方差返回 NaN。
// 导出供 internal/lab 的 IC 分析复用（spearman = pearson(ranks(x), ranks(y))）。
func Pearson(xs, ys []float64) float64 {
	if len(xs) == 0 || len(xs) != len(ys) {
		return math.NaN()
	}
	var sx, sy float64
	for i := range xs {
		sx += xs[i]
		sy += ys[i]
	}
	n := float64(len(xs))
	mx, my := sx/n, sy/n
	var sxx, syy, sxy float64
	for i := range xs {
		dx, dy := xs[i]-mx, ys[i]-my
		sxx += dx * dx
		syy += dy * dy
		sxy += dx * dy
	}
	if sxx == 0 || syy == 0 {
		return math.NaN()
	}
	return sxy / math.Sqrt(sxx*syy)
}

// 量价相关 是近 N 日（含今日）收盘价与成交量的 Pearson 相关系数。
// 数据不足（len < Days）或零方差返回 NaN。
type 量价相关 struct {
	Days int
}

func (f 量价相关) Name() string {
	return fmt.Sprintf("量价相关(%d)", daysOr(f.Days, 20))
}

func (f 量价相关) Value(code string, dks extend.Klines) float64 {
	n := daysOr(f.Days, 20)
	if len(dks) < n {
		return math.NaN()
	}
	w := dks[len(dks)-n:]
	xs := make([]float64, n)
	ys := make([]float64, n)
	for i, k := range w {
		xs[i] = k.Close.Float64()
		ys[i] = float64(k.Volume)
	}
	return Pearson(xs, ys)
}
