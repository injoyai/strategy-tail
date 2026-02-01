package main

import (
	"fmt"
	"time"

	"github.com/injoyai/tdx/protocol"
)

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

type trade struct {
	Code  string
	Buy   bool
	Time  time.Time
	Price protocol.Price
}

type Price = protocol.Price
