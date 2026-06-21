package core

import (
	"fmt"
	"time"

	"github.com/injoyai/tdx/extend"
	"github.com/injoyai/tdx/protocol"
)

const (
	TypeBuy  = "buy"
	TypeSell = "sell"
)

type Trade struct {
	Code      string
	BuyTime   time.Time
	SellTime  time.Time
	BuyPrice  protocol.Price
	SellPrice protocol.Price
	Virtual   bool //是否为虚拟成交(尚未卖出,按最新价估算)
}

type (
	Price  = protocol.Price
	Klines = protocol.Klines
)

type Buyer interface {
	Name() string
	Buy(code string, dks extend.Klines) bool
}

type Buy struct {
	Code  string
	Time  time.Time
	Price protocol.Price
}

func (b *Buy) String() string {
	return fmt.Sprintf("代码: %s  买入价: %.2f", b.Code, b.Price.Float64())
}

type Seller interface {
	Name() string
	Sell(code string, dks extend.Klines, buy Buy) bool
}

type Sell struct {
	Code  string         //代码
	Time  time.Time      //卖出时间
	Price protocol.Price //卖出价格
}

func (s *Sell) String() string {
	return fmt.Sprintf("代码: %s  卖出价: %.2f", s.Code, s.Price.Float64())
}
