package factor

import (
	"fmt"
	"math"

	"github.com/injoyai/strategy-tail/lib/extend"
)

// N日高低位 是收盘价在近 N 日区间中的位置：(close - LLV) / (HHV - LLV)，值域 [0,1]。
// 数据不足（len < Days）或 HHV=LLV 返回 NaN。
type N日高低位 struct {
	Days int
}

func (f N日高低位) Name() string {
	return fmt.Sprintf("N日高低位(%d)", daysOr(f.Days, 60))
}

func (f N日高低位) Value(code string, dks extend.Klines) float64 {
	n := daysOr(f.Days, 60)
	if len(dks) < n {
		return math.NaN()
	}
	hhv := dks.HHV(n).Float64()
	llv := dks.LLV(n).Float64()
	if hhv == llv {
		return math.NaN()
	}
	return (dks[len(dks)-1].Close.Float64() - llv) / (hhv - llv)
}

// 收盘分位 是今收盘在近 N 日（含今日）收盘价中的分位：收盘 ≤ 今收盘 的天数占比，
// 与 量分位 同口径；值域 [1/N, 1]，数据不足（len < Days）返回 NaN。
// 与 N日高低位 的差异：高低位是区间内线性位置，本因子是排名分位（全平价窗口 → 1）。
type 收盘分位 struct {
	Days int
}

func (f 收盘分位) Name() string {
	return fmt.Sprintf("收盘分位(%d)", daysOr(f.Days, 120))
}

func (f 收盘分位) Value(code string, dks extend.Klines) float64 {
	n := daysOr(f.Days, 120)
	if len(dks) < n {
		return math.NaN()
	}
	today := dks[len(dks)-1].Close.Float64()
	cnt := 0
	for _, k := range dks[len(dks)-n:] {
		if k.Close.Float64() <= today {
			cnt++
		}
	}
	return float64(cnt) / float64(n)
}

// K值 是 KDJ 指标中的 K 线（0..100）：K = 2/3·K前 + 1/3·RSV，首日 K前=50。
// RSV = (close - LLV) / (HHV - LLV) * 100，窗口为截至当日最多 n 根；HHV=LLV 时 RSV=50。
// 数据不足（len < Days）返回 NaN。
// 性能注记：逐日重算窗口极值为 O(len*n)，全市场约 1.6e10 次比较/250日——
// IC 分析（internal/lab）必须用随机样本，勿对全市场逐票全历史调用。
type K值 struct {
	Days int
}

func (f K值) Name() string {
	return fmt.Sprintf("K值(%d)", daysOr(f.Days, 9))
}

func (f K值) Value(code string, dks extend.Klines) float64 {
	n := daysOr(f.Days, 9)
	if len(dks) < n {
		return math.NaN()
	}
	k := 50.0
	for i := 0; i < len(dks); i++ {
		lo := i - n + 1
		if lo < 0 {
			lo = 0
		}
		w := dks[lo : i+1]
		hhv, llv := w[0].High.Float64(), w[0].Low.Float64()
		for _, k2 := range w[1:] {
			if v := k2.High.Float64(); v > hhv {
				hhv = v
			}
			if v := k2.Low.Float64(); v < llv {
				llv = v
			}
		}
		rsv := 50.0
		if hhv != llv {
			rsv = (dks[i].Close.Float64() - llv) / (hhv - llv) * 100
		}
		k = 2.0/3.0*k + 1.0/3.0*rsv
	}
	return k
}
