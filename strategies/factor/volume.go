package factor

import (
	"fmt"
	"math"

	"github.com/injoyai/strategy-tail/lib/extend"
)

// 量比 是今日成交量相对前 N 日均量的倍数（不含今日）。
// 数据不足（len < Days+1）或前 N 日均量为 0 返回 NaN。
type 量比 struct {
	Days int
}

func (f 量比) Name() string {
	return fmt.Sprintf("量比(%d)", daysOr(f.Days, 5))
}

func (f 量比) Value(code string, dks extend.Klines) float64 {
	n := daysOr(f.Days, 5)
	if len(dks) < n+1 {
		return math.NaN()
	}
	w := dks[len(dks)-n-1 : len(dks)-1]
	var sum int64
	for _, k := range w {
		sum += k.Volume
	}
	avg := float64(sum) / float64(n)
	if avg == 0 {
		return math.NaN()
	}
	return float64(dks[len(dks)-1].Volume) / avg
}

// 量分位 是今量在近 N 日（含今日）中的分位：vol ≤ 今量 的天数占比。
// 数据不足（len < Days）返回 NaN。
type 量分位 struct {
	Days int
}

func (f 量分位) Name() string {
	return fmt.Sprintf("量分位(%d)", daysOr(f.Days, 60))
}

func (f 量分位) Value(code string, dks extend.Klines) float64 {
	n := daysOr(f.Days, 60)
	if len(dks) < n {
		return math.NaN()
	}
	today := dks[len(dks)-1].Volume
	var cnt int
	for _, k := range dks[len(dks)-n:] {
		if k.Volume <= today {
			cnt++
		}
	}
	return float64(cnt) / float64(n)
}

// 放量占比 是近 N 日（含今日）中成交量超过 1.5 倍日均量的天数占比。
// 数据不足（len < Days）返回 NaN。
type 放量占比 struct {
	Days int
}

func (f 放量占比) Name() string {
	return fmt.Sprintf("放量占比(%d)", daysOr(f.Days, 20))
}

func (f 放量占比) Value(code string, dks extend.Klines) float64 {
	n := daysOr(f.Days, 20)
	if len(dks) < n {
		return math.NaN()
	}
	w := dks[len(dks)-n:]
	var sum int64
	for _, k := range w {
		sum += k.Volume
	}
	threshold := 1.5 * float64(sum) / float64(n)
	var cnt int
	for _, k := range w {
		if float64(k.Volume) > threshold {
			cnt++
		}
	}
	return float64(cnt) / float64(n)
}
