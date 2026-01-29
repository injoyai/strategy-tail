package main

import (
	"time"

	"github.com/injoyai/tdx/extend"
	"github.com/injoyai/tdx/protocol"
)

type trade struct {
	Code  string
	Buy   bool
	Time  time.Time
	Price protocol.Price
}

type Strategy interface {
	// Buy 传入 日线[上市,日期A] 分钟线[日期A]
	Buy(code string, dks extend.Klines, mk protocol.Klines) *trade
	Sell(code string, dks extend.Klines, mk protocol.Klines) *trade
}

// 策略1
type s1 struct {
	BuyTime        string         //"14:40:00"
	SellTime       string         //"10:00:00"
	MinMarketValue protocol.Price //最小市值
	MaxMarketValue protocol.Price //最大市值
}

func (s s1) Buy(code string, dks extend.Klines, mks protocol.Klines) *trade {
	if len(dks) == 0 || len(mks) == 0 {
		return nil
	}

	dk := dks[len(dks)-1]

	//过滤市值过大或者过小的股票
	value := protocol.Price(dk.TotalStock) * dk.Open
	if value > s.MaxMarketValue || value < s.MinMarketValue {
		return nil
	}

	//过滤换手率过大或者过小的股票
	if dk.Turnover > 5 || dk.Turnover < 2 {
		return nil
	}

	t := &trade{
		Code:  code,
		Buy:   true,
		Time:  time.Time{},
		Price: 0,
	}

	//过滤收盘价和收盘价
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

	if !priceMostlyAboveMA(mks, 20, 0.8) {
		return nil
	}

	if !slowRising(mks, 20, 0.003, 0.03) {
		return nil
	}

	return t
}

func (s s1) Sell(code string, dks extend.Klines, mk protocol.Klines) *trade {
	t := &trade{Code: code, Buy: false}
	for _, v := range mk {
		//到达卖点,按最低价-1分卖出,提升成交成功率
		if v.Time.Format(time.TimeOnly) == s.SellTime {
			t.Time = v.Time
			t.Price = v.Low
			return t
		}
		if t.Price == 0 || t.Price > v.Low {
			t.Price = v.Low
		}
	}
	return t
}

func priceMostlyAboveMA(mks protocol.Klines, window int, ratio float64) bool {
	if window <= 1 || len(mks) < window {
		return false
	}
	sum := 0.0
	for i := 0; i < window; i++ {
		sum += mks[i].Close.Float64()
	}
	aboveCount := 0
	total := 0
	for i := window - 1; i < len(mks); i++ {
		ma := sum / float64(window)
		if mks[i].Close.Float64() >= ma {
			aboveCount++
		}
		total++
		if i+1 < len(mks) {
			sum += mks[i+1].Close.Float64()
			sum -= mks[i-window+1].Close.Float64()
		}
	}
	return total > 0 && float64(aboveCount)/float64(total) >= ratio
}

func slowRising(mks protocol.Klines, window int, minRise, maxRise float64) bool {
	if len(mks) < window {
		return false
	}
	first := mks[0].Close.Float64()
	last := mks[len(mks)-1].Close.Float64()
	if first <= 0 {
		return false
	}
	rise := (last - first) / first
	if rise < minRise || rise > maxRise {
		return false
	}
	firstMA := averageClose(mks[:window])
	lastMA := averageClose(mks[len(mks)-window:])
	return lastMA > firstMA
}

func averageClose(mks protocol.Klines) float64 {
	if len(mks) == 0 {
		return 0
	}
	sum := 0.0
	for _, v := range mks {
		sum += v.Close.Float64()
	}
	return sum / float64(len(mks))
}
