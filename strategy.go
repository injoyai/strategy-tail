package main

import (
	"time"

	"github.com/injoyai/tdx/extend"
	"github.com/injoyai/tdx/protocol"
)

type Strategy interface {
	// Signal 传入 日线[上市,日期A] 分钟线[日期A]
	Signal(dks extend.Klines, mk protocol.Klines) bool
}

// 策略1
type s1 struct {
	BuyTime        string
	SellTime       string
	MinMarketValue protocol.Price
	MaxMarketValue protocol.Price
}

func (s1) Signal(dks extend.Klines, mks protocol.Klines) bool {
	if len(dks) == 0 || len(mks) == 0 {
		return false
	}

	dk := dks[len(dks)-1]

	//过滤市值过大或者过小的股票
	value := protocol.Price(dk.TotalStock) * dk.Open
	if value.Float64() > 200*1e8 || value < 50*1e8 {
		return false
	}

	//过滤换手率过大或者过小的股票
	if dk.Turnover > 5 || dk.Turnover < 2 {
		return false
	}

	//过滤收盘价和收盘价
	for _, v := range mks {
		if v.Time.Format(time.TimeOnly) >= "14:40:00" {
			if (dk.High-v.High).Float64()/dk.High.Float64() > 0.1 {
				return false
			}
			break
		}
	}

	if !priceMostlyAboveMA(mks, 20, 0.8) {
		return false
	}

	if !slowRising(mks, 20, 0.003, 0.03) {
		return false
	}

	return true
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
