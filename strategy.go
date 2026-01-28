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
type s1 struct{}

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

	return true
}
