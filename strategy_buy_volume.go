package main

import (
	"sort"

	"github.com/injoyai/tdx/extend"
	"github.com/injoyai/tdx/protocol"
)

var _ Buyer = BuyVolume{}

type BuyVolume struct{}

func (v BuyVolume) Name() string {
	return "倍量"
}

func (v BuyVolume) Buy(code string, dks extend.Klines, mks protocol.Klines) *Buy {
	if len(dks) < 20 {
		return nil
	}

	n := len(dks)
	today := dks[n-1]
	yesterday := dks[n-2]

	if today.Close.Float64() >= 100 {
		return nil
	}

	if today.RiseRate() < 0 {
		return nil
	}

	if today.RiseRate() > 7 {
		return nil
	}

	muli := 2.9

	// TJ1：倍量
	TJ1 := float64(today.Volume)/float64(yesterday.Volume) >= muli //倍量
	TJ1 = TJ1 && today.Close > today.Open                          //阳线

	// 近10天内首次倍量：检查今天之前最多9天
	start := n - 2
	end := n - 10
	if end < 1 {
		end = 1
	}
	for i := start; i >= end; i-- {
		cur := dks[i]
		prev := dks[i-1]
		if float64(cur.Volume)/float64(prev.Volume) >= muli {
			TJ1 = false
			break
		}
	}

	TJ1 = TJ1 && today.High == dks.HHV(6)

	// TJ2：收盘上涨 + 6日新高 + 有上影
	TJ2 := today.Close > yesterday.Close && //收盘上涨
		today.High > today.Close && //有上影
		today.High == dks.HHV(6) //6日新高

	// TJ3：10日区间不弱（低点 >= 高点 * 0.8）
	TJ3 := dks.LLV(10).Float64() >= dks.HHV(10).Float64()*0.8

	// TJ4：短期低点抬高
	TJ4 := dks.LLV(5) > dks.LLV(20)

	if !TJ1 || !TJ2 || !TJ3 || !TJ4 {
		return nil
	}

	return &Buy{
		Code:  code,
		Time:  today.Time,
		Price: today.Close,
	}

}

// HHV 最近N天的最高值
func HHV(dks extend.Klines, i int) protocol.Price {
	ls := dks[len(dks)-i:]
	sort.Slice(ls, func(i, j int) bool { return ls[i].High > ls[j].High })
	return ls[0].High
}

// LLV 最近N天的最低值
func LLV(dks extend.Klines, i int) protocol.Price {
	ls := dks[len(dks)-i:]
	sort.Slice(ls, func(i, j int) bool { return ls[i].Low < ls[j].Low })
	return ls[0].Low
}
