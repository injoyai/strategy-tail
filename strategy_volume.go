package main

import (
	"time"

	"github.com/injoyai/tdx/extend"
	"github.com/injoyai/tdx/protocol"
)

var _ Strategy = volume{}

type volume struct {
	BuyTime  string
	SellTime string
}

func (s volume) Buy(code string, dks extend.Klines, mks protocol.Klines) *trade {
	if len(dks) < 20 || len(mks) == 0 {
		return nil
	}

	// === 倍量选股核心 ===
	if !matchMultipleVolumeSPK(dks) {
		return nil
	}

	dk := dks[len(dks)-1]

	t := &trade{
		Code: code,
		Buy:  true,
	}

	// 分钟线买点
	for _, v := range mks {
		if v.Time.Format(time.TimeOnly) >= s.BuyTime {
			if (dk.High-v.High).Float64()/dk.High.Float64() > 0.1 {
				return nil
			}
			t.Time = v.Time
			t.Price = v.High
			break
		}
	}

	if t.Price == 0 {
		return nil
	}

	if !priceMostlyAboveMA(mks, 20, 0.8) {
		return nil
	}

	if !slowRising(mks, 20, 0.003, 0.03) {
		return nil
	}

	return t
}

func matchMultipleVolumeSPK(dks extend.Klines) bool {
	n := len(dks)
	if n < 20 {
		return false
	}

	i := n - 1
	dk := dks[i]

	// TJ1：倍量
	TJ1 := isMultipleVolume(dks, i)

	// TJ2：收盘上涨 + 6日新高 + 有上影
	TJ2 := false
	if i >= 6 {
		TJ2 =
			dk.Close > dks[i-1].Close &&
				dk.High > dk.Close &&
				dk.High.Float64() == hhvHigh(dks, i, 6)
	}

	// TJ3：10日区间不弱（低点 >= 高点 * 0.8）
	TJ3 := false
	if i >= 10 {
		llv := llvLow(dks, i-1, 10)
		hhv := hhvHigh(dks, i-1, 10)
		if hhv > 0 {
			TJ3 = llv >= hhv*0.8
		}
	}

	// TJ4：短期低点抬高
	TJ4 := llvLow(dks, i, 5) > llvLow(dks, i, 20)

	return TJ1 && TJ2 && TJ3 && TJ4
}

func isMultipleVolume(dks extend.Klines, i int) bool {
	if i <= 0 {
		return false
	}
	v := dks[i].Volume
	prev := dks[i-1].Volume
	if prev <= 0 {
		return false
	}
	return float64(v)/float64(prev) >= 2.9
}

func hhvHigh(dks extend.Klines, end, n int) float64 {
	start := end - n + 1
	if start < 0 {
		start = 0
	}
	max := dks[start].High.Float64()
	for i := start + 1; i <= end; i++ {
		if dks[i].High.Float64() > max {
			max = dks[i].High.Float64()
		}
	}
	return max
}

func llvLow(dks extend.Klines, end, n int) float64 {
	start := end - n + 1
	if start < 0 {
		start = 0
	}
	min := dks[start].Low.Float64()
	for i := start + 1; i <= end; i++ {
		if dks[i].Low.Float64() < min {
			min = dks[i].Low.Float64()
		}
	}
	return min
}

func barsLastMultiple(dks extend.Klines, end int) int {
	for i := end - 1; i >= 0; i-- {
		if isMultipleVolume(dks, i) {
			return end - i
		}
	}
	return end + 1
}

func (v volume) Sell(code string, dks extend.Klines, mk protocol.Klines) *trade {
	t := &trade{Code: code, Buy: false}
	for _, k := range mk {
		//到达卖点,按最低价-1分卖出,提升成交成功率
		if k.Time.Format(time.TimeOnly) == v.SellTime {
			t.Time = k.Time
			t.Price = k.Low
			return t
		}
		if t.Price == 0 || t.Price > k.Low {
			t.Price = k.Low
		}
	}
	return t
}

// 辅助函数
func RefFloat(arr []Price, i, n int) Price {
	if i-n < 0 {
		return arr[0]
	}
	return arr[i-n]
}

func HHV(arr []Price, i, n int) Price {
	start := i - n + 1
	if start < 0 {
		start = 0
	}
	max := arr[start]
	for j := start + 1; j <= i; j++ {
		if arr[j] > max {
			max = arr[j]
		}
	}
	return max
}

func LLV(arr []Price, i, n int) Price {
	start := i - n + 1
	if start < 0 {
		start = 0
	}
	min := arr[start]
	for j := start + 1; j <= i; j++ {
		if arr[j] < min {
			min = arr[j]
		}
	}
	return min
}

// 统计过去n天布尔值为true的次数
func CountTrue(arr []bool, i, n int) int {
	start := i - n + 1
	if start < 0 {
		start = 0
	}
	count := 0
	for j := start; j <= i; j++ {
		if arr[j] {
			count++
		}
	}
	return count
}

// 找到上一次true出现的位置距离当前的天数
func BarsLast(arr []bool, i int) int {
	for j := i - 1; j >= 0; j-- {
		if arr[j] {
			return i - j
		}
	}
	return i + 1
}

/*
func SelectStocks(klines extend.Klines) []bool {
	n := len(klines)
	V := make([]float64, n)
	C := make([]Price, n)
	H := make([]Price, n)
	L := make([]Price, n)
	O := make([]Price, n)
	for i, k := range klines {
		V[i] = float64(k.Volume)
		C[i] = k.Close
		H[i] = k.High
		L[i] = k.Low
		O[i] = k.Open
	}

	// 计算倍量
	multiple := make([]bool, n)
	for i := 1; i < n; i++ {
		multiple[i] = V[i]/V[i-1] >= 2.9
	}

	xg := make([]bool, n)
	for i := 0; i < n; i++ {
		// N = 上一次倍量出现距离
		N := 1
		if i > 0 {
			N = BarsLast(multiple, i) // 等价于 REF(BARSLAST(倍量),1)
		}

		// TJ1~TJ4
		TJ1 := multiple[i]
		TJ2 := false
		if i >= 6 {
			TJ2 = C[i] > RefFloat(C, i, 1) && H[i] > C[i] && H[i] == HHV(H, i, 6)
		}
		TJ3 := false
		if i >= 10 {
			TJ3 = RefFloat(LLV(L, i, 10), i, 1) >= RefFloat(HHV(H, i, 10), i, 1)*0.8
		}
		TJ4 := false
		if i >= 20 {
			TJ4 = LLV(L, i, 5) > LLV(L, i, 20)
		}

		SPK := TJ1 && TJ2 && TJ3 && TJ4
		xg[i] = SPK
	}

	return xg
}
*/
