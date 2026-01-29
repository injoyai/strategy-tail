package main

import (
	"fmt"
	"time"

	"github.com/injoyai/tdx/extend"
	"github.com/injoyai/tdx/protocol"
)

var _ Strategy = volume{}

type volume struct {
	SellTime string
}

func (v volume) Buy(code string, dks extend.Klines, mk protocol.Klines) *trade {
	if len(dks) < 20 { // TJ4需要20天
		return nil
	}

	i := len(dks) - 1
	dk := dks[i]

	// 准备数据
	// TJ1: 倍量:=V/REF(V,1)>=2.9;
	vol := float64(dk.Volume)
	refVol1 := float64(dks[i-1].Volume)
	if refVol1 == 0 {
		return nil
	}
	beiLiang := vol/refVol1 >= 2.9
	if !beiLiang {
		return nil
	}
	//fmt.Printf("[%s] TJ1 met: Vol/RefVol=%.2f\n", code, vol/refVol1)

	// TJ2: C>REF(C,1)&&H>C&&H=HHV(H,6);
	close := dk.Close
	refClose1 := dks[i-1].Close
	high := dk.High

	isHighestHigh6 := true
	for j := 0; j < 6; j++ {
		if dks[i-j].High > high {
			isHighestHigh6 = false
			break
		}
	}

	tj2 := close > refClose1 && high > close && isHighestHigh6
	if !tj2 {
		return nil
	}
	//fmt.Printf("[%s] TJ2 met\n", code)

	// TJ3: REF(LLV(L,10),1)>=REF(HHV(H,10),1)*0.8;
	llv10 := dks[i-1].Low
	hhv10 := dks[i-1].High
	for j := 1; j < 10; j++ {
		idx := i - 1 - j
		if idx < 0 {
			break
		}
		if dks[idx].Low < llv10 {
			llv10 = dks[idx].Low
		}
		if dks[idx].High > hhv10 {
			hhv10 = dks[idx].High
		}
	}

	tj3 := float64(llv10) >= float64(hhv10)*0.8
	if !tj3 {
		return nil
	}
	//fmt.Printf("[%s] TJ3 met\n", code)

	// TJ4: LLV(L,5)>LLV(L,20);
	llv5 := dk.Low
	for j := 0; j < 5; j++ { // LLV(L,5) includes today? Yes usually.
		if dks[i-j].Low < llv5 {
			llv5 = dks[i-j].Low
		}
	}

	llv20 := dk.Low
	for j := 0; j < 20; j++ {
		if dks[i-j].Low < llv20 {
			llv20 = dks[i-j].Low
		}
	}

	tj4 := llv5 > llv20
	if !tj4 {
		return nil
	}
	fmt.Printf("[%s] ALL Conditions met on %s\n", code, dk.Time.Format("2006-01-02"))

	t := &trade{
		Code:  code,
		Buy:   true,
		Time:  time.Time{},
		Price: 0,
	}

	found := false
	for _, k := range mk {
		if k.Time.Format(time.TimeOnly) >= "14:50:00" {
			t.Time = k.Time
			t.Price = k.High + protocol.Yuan(0.01)
			found = true
			break
		}
	}

	if !found {
		t.Time = dk.Time
		t.Price = dk.Close
	}

	return t
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
