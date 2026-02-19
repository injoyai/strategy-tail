package main

import (
	"sort"

	"github.com/injoyai/tdx/extend"
	"github.com/injoyai/tdx/protocol"
)

var _ Strategy = StrategyVolume{}

type StrategyVolume struct {
	BuyTime  string
	SellTime string
}

func (v StrategyVolume) Buy(code string, dks extend.Klines, mks protocol.Klines) *Buy {
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

	TJ1 = TJ1 && today.High == HHV(dks, 6)

	// TJ2：收盘上涨 + 6日新高 + 有上影
	TJ2 := today.Close > yesterday.Close && //收盘上涨
		today.High > today.Close && //有上影
		today.High == HHV(dks, 6) //6日新高

	// TJ3：10日区间不弱（低点 >= 高点 * 0.8）
	TJ3 := LLV(dks, 10).Float64() >= HHV(dks, 10).Float64()*0.8

	// TJ4：短期低点抬高
	TJ4 := LLV(dks, 5) > LLV(dks, 20)

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

func (v StrategyVolume) Sell(code string, dks extend.Klines, mk protocol.Klines, buy Buy) *Sell {
	dk := dks[len(dks)-1]

	t := &Sell{
		Code:  code,
		Time:  dk.Time,
		Price: dk.Close,
	}

	//for _, k := range mk {
	//	// 止损：亏损 > 10%
	//	if buy.Price > 0 && (float64(k.Close)-float64(buy.Price))/float64(buy.Price) < -0.05 {
	//		t.Time = k.Time
	//		t.Price = k.Close
	//		return t
	//	}
	//
	//	////到达卖点,按最低价-1分卖出,提升成交成功率
	//	//if k.Time.Format(time.TimeOnly) == v.SellTime {
	//	//	t.Time = k.Time
	//	//	t.Price = k.Low
	//	//	return t
	//	//}
	//
	//}
	return t
}
