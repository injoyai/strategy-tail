package core

import (
	"fmt"
	"strings"
	"time"

	"github.com/injoyai/tdx/extend"
	"github.com/injoyai/tdx/protocol"
)

type Strategy struct {
	BuyAll
	SellAny
}

func (s Strategy) String() string {
	return s.BuyAll.Name() + "," + s.SellAny.Name()
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

type BuyAll struct {
	Buyers []Buyer
}

func (b BuyAll) Name() string {
	names := make([]string, 0, len(b.Buyers))
	for _, v := range b.Buyers {
		if v == nil {
			continue
		}
		names = append(names, v.Name())
	}
	if len(names) == 0 {
		return "BuyAll"
	}
	return strings.Join(names, "&")
}

func (b BuyAll) Buy(code string, dks extend.Klines) bool {
	buy := false
	for _, v := range b.Buyers {
		if v == nil {
			continue
		}
		buy = true
		if !v.Buy(code, dks) {
			return false
		}
	}
	return buy
}

type SellAny struct {
	Sellers []Seller
}

func (s SellAny) Name() string {
	names := make([]string, 0, len(s.Sellers))
	for _, v := range s.Sellers {
		if v == nil {
			continue
		}
		names = append(names, v.Name())
	}
	if len(names) == 0 {
		return "SellAny"
	}
	return strings.Join(names, "|")
}

func (s SellAny) Sell(code string, dks extend.Klines, buy Buy) bool {
	for _, v := range s.Sellers {
		if v == nil {
			continue
		}
		if v.Sell(code, dks, buy) {
			return true
		}
	}
	return false
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
