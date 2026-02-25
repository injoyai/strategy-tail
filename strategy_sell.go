package main

import (
	"fmt"
	"time"

	"github.com/injoyai/conv"
	"github.com/injoyai/tdx/extend"
	"github.com/injoyai/tdx/protocol"
)

type Seller interface {
	Name() string
	Sell(code string, dks extend.Klines, mk protocol.Klines, buy Buy) *Sell
}

type Sell struct {
	Code  string
	Time  time.Time
	Price protocol.Price
}

func (this *Sell) String() string {
	return fmt.Sprintf("代码: %s  卖出价: %.2f", this.Code, this.Price.Float64())
}

/*

 */

type SellTomorrow string

func (this SellTomorrow) Name() string {
	return "次日"
}

func (this SellTomorrow) Sell(code string, dks extend.Klines, mk protocol.Klines, buy Buy) *Sell {
	if len(dks) == 0 {
		return nil
	}
	sellTime := string(this)
	if len(sellTime) == 0 {
		sellTime = "10:00:00"
	}
	for _, v := range mk {
		if v.Time.Format(time.TimeOnly) >= sellTime {
			return &Sell{
				Code:  code,
				Time:  v.Time,
				Price: v.Close,
			}
		}
	}
	dk := dks[len(dks)-1]
	return &Sell{
		Code:  code,
		Time:  dk.Time,
		Price: dk.Open,
	}
}

type SellFall float64

func (this SellFall) Sell(code string, dks extend.Klines, mk protocol.Klines, buy Buy) *Sell {
	if len(dks) == 0 {
		return nil
	}
	for _, v := range mk {
		if (v.Close.Float64()-buy.Price.Float64())/buy.Price.Float64() < -float64(this) {
			return &Sell{
				Code:  code,
				Time:  v.Time,
				Price: v.Close,
			}
		}
	}
	return nil
}

type SellDay int

func (this SellDay) Name() string {
	return conv.String(this) + "天后"
}

func (this SellDay) Sell(code string, dks extend.Klines, mk protocol.Klines, buy Buy) *Sell {
	if len(dks) < 1 {
		return nil
	}
	dk := dks[len(dks)-1]
	if dk.Time.Sub(buy.Time).Hours()/24 > float64(this) {
		return &Sell{
			Code:  code,
			Time:  dk.Time,
			Price: dk.Open,
		}
	}
	return nil
}
