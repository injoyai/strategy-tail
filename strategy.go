package main

import (
	"fmt"
	"time"

	"github.com/injoyai/tdx/extend"
	"github.com/injoyai/tdx/protocol"
)

type IStrategy interface {
	Buy(code string, dks extend.Klines, mk protocol.Klines) *Buy
	Sell(code string, dks extend.Klines, mk protocol.Klines, buy Buy) *Sell
}

type Strategy struct {
	Buyer
	Seller
}

func (this Strategy) String() string {
	return this.Buyer.Name() + "买入," + this.Seller.Name() + "卖出"
}

type Trade struct {
	Code      string
	BuyTime   time.Time
	SellTime  time.Time
	BuyPrice  protocol.Price
	SellPrice protocol.Price
}

type Price = protocol.Price

type Buyer interface {
	Name() string
	Buy(code string, dks extend.Klines, mk protocol.Klines) *Buy
}

type Buy struct {
	Code  string
	Time  time.Time
	Price protocol.Price
}

func (this *Buy) String() string {
	return fmt.Sprintf("代码: %s  买入价: %.2f", this.Code, this.Price.Float64())
}
