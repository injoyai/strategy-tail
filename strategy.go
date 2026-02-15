package main

import (
	"fmt"
	"time"

	"github.com/injoyai/tdx/extend"
	"github.com/injoyai/tdx/protocol"
)

type Strategy interface {
	// Buy 传入 日线[上市,日期A] 分钟线[日期A]
	Buy(code string, dks extend.Klines, mk protocol.Klines) *Buy
	Sell(code string, dks extend.Klines, mk protocol.Klines, buy Buy) *Sell
}

type Price = protocol.Price

type Trade struct {
	Code      string
	BuyTime   time.Time
	SellTime  time.Time
	BuyPrice  protocol.Price
	SellPrice protocol.Price
}

type Buy struct {
	Code  string
	Time  time.Time
	Price protocol.Price
}

func (this *Buy) String() string {
	return fmt.Sprintf("代码: %s  买入价: %.2f", this.Code, this.Price.Float64())
}

type Sell struct {
	Code  string
	Time  time.Time
	Price protocol.Price
}

func (this *Sell) String() string {
	return fmt.Sprintf("代码: %s  卖出价: %.2f", this.Code, this.Price.Float64())
}
