package main

import (
	"sort"
	"time"

	"github.com/injoyai/tdx/extend"
	"github.com/injoyai/tdx/protocol"
)

var _ Strategy = volume{}

type volume struct {
	BuyTime  string
	SellTime string
}

func (v volume) Buy(code string, dks extend.Klines, mks protocol.Klines) *trade {
	if len(dks) < 20 {
		return nil
	}

	n := len(dks)
	today := dks[n-1]
	yesterday := dks[n-2]

	// TJ1：倍量
	TJ1 := float64(today.Volume)/float64(yesterday.Volume) >= 2.9

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

	dk := dks[len(dks)-1]

	return &trade{
		Code:  code,
		Buy:   true,
		Time:  dk.Time,
		Price: dk.Close,
	}

}

func HHV(dks extend.Klines, i int) protocol.Price {
	ls := dks[len(dks)-i:]
	sort.Slice(ls, func(i, j int) bool { return ls[i].High > ls[j].High })
	return ls[0].High
}

func LLV(dks extend.Klines, i int) protocol.Price {
	ls := dks[len(dks)-i:]
	sort.Slice(ls, func(i, j int) bool { return ls[i].Low < ls[j].Low })
	return ls[0].Low
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
