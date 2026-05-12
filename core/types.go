package core

import (
	"fmt"
	"time"

	"github.com/injoyai/tdx/extend"
	"github.com/injoyai/tdx/protocol"
)

type Strategy struct {
	Buyer
	Seller
}

func (s Strategy) String() string {
	return s.Buyer.Name() + "," + s.Seller.Name()
}

type Trade struct {
	Code      string
	BuyTime   time.Time
	SellTime  time.Time
	BuyPrice  protocol.Price
	SellPrice protocol.Price
}

type (
	Price  = protocol.Price
	Klines = protocol.Klines
)

type Buyer interface {
	Name() string
	Buy(code string, historyDayklines extend.Klines, todayMinklines protocol.Klines) *Buy
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
	Sell(code string, history, future extend.Klines, getMinklines func(after int) Klines, buy Buy) *Sell
}

type Sell struct {
	Code  string
	Time  time.Time
	Price protocol.Price
}

func (s *Sell) String() string {
	return fmt.Sprintf("代码: %s  卖出价: %.2f", s.Code, s.Price.Float64())
}

/*



 */

type GetMinKlines struct {
	today time.Time
	m     map[string]protocol.Klines
}

func (this GetMinKlines) Get(after int) protocol.Klines {
	t := this.today.AddDate(0, 0, after)
	if v, ok := this.m[t.Format(time.DateOnly)]; ok {
		return v
	}
	return nil
}
