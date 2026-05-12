package strategies

import (
	"sort"

	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/tdx/extend"
	"github.com/injoyai/tdx/protocol"
)

type BuyVolume struct{}

func (v BuyVolume) Name() string {
	return "倍量买入"
}

func (v BuyVolume) Buy(code string, dks extend.Klines, mks protocol.Klines) *core.Buy {
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

	TJ1 := float64(today.Volume)/float64(yesterday.Volume) >= muli
	TJ1 = TJ1 && today.Close > today.Open

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

	TJ2 := today.Close > yesterday.Close &&
		today.High > today.Close &&
		today.High == dks.HHV(6)

	TJ3 := dks.LLV(10).Float64() >= dks.HHV(10).Float64()*0.8
	TJ4 := dks.LLV(5) > dks.LLV(20)

	if !TJ1 || !TJ2 || !TJ3 || !TJ4 {
		return nil
	}

	return &core.Buy{
		Code:  code,
		Time:  today.Time,
		Price: today.Close,
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
