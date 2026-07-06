package core_test

import (
	"math"
	"testing"
	"time"

	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/lib/extend"
	"github.com/injoyai/strategy-tail/strategies/sell"
	"github.com/injoyai/tdx/protocol"
)

// ============================================================================
// 成本模型测试
// ============================================================================

func TestBuyCost含滑点和佣金(t *testing.T) {
	c := core.Cost{
		CommissionRate: 0.0003,
		Slippage:       protocol.Yuan(0.01),
		MinCommission:  5.0,
	}
	execPrice, totalCost := c.BuyCost(protocol.Yuan(10.00), 100)
	if math.Abs(execPrice.Float64()-10.01) > 1e-6 {
		t.Fatalf("execPrice expected 10.01, got %v", execPrice)
	}
	if math.Abs(totalCost-1006) > 0.01 {
		t.Fatalf("totalCost expected ~1006, got %v", totalCost)
	}
}

func TestBuyCost大额佣金超过最低(t *testing.T) {
	c := core.Cost{
		CommissionRate: 0.0003,
		Slippage:       protocol.Yuan(0.01),
		MinCommission:  5.0,
	}
	execPrice, totalCost := c.BuyCost(protocol.Yuan(100), 1000)
	if math.Abs(execPrice.Float64()-100.01) > 1e-6 {
		t.Fatalf("execPrice expected 100.01, got %v", execPrice)
	}
	if math.Abs(totalCost-100040.003) > 0.1 {
		t.Fatalf("totalCost expected ~100040, got %v", totalCost)
	}
}

func TestSellIncome含印花税和佣金(t *testing.T) {
	c := core.Cost{
		CommissionRate: 0.0003,
		StampDutyRate:  0.001,
		Slippage:       protocol.Yuan(0.01),
		MinCommission:  5.0,
	}
	execPrice, netIncome := c.SellIncome(protocol.Yuan(11.00), 100)
	if math.Abs(execPrice.Float64()-10.99) > 1e-6 {
		t.Fatalf("execPrice expected 10.99, got %v", execPrice)
	}
	if math.Abs(netIncome-1092.901) > 0.01 {
		t.Fatalf("netIncome expected ~1092.901, got %v", netIncome)
	}
}

func TestTradeProfit含成本口径(t *testing.T) {
	c := core.DefaultCost()
	_, buyCost := c.BuyCost(protocol.Yuan(10.00), 100)
	_, sellIncome := c.SellIncome(protocol.Yuan(11.00), 100)

	tr := core.Trade{
		BuyPrice:   protocol.Yuan(10.00),
		SellPrice:  protocol.Yuan(11.00),
		BuyCost:    buyCost,
		SellIncome: sellIncome,
		Quantity:   100,
	}

	profit := tr.Profit()
	if profit >= 10.0 {
		t.Fatalf("含成本收益率应低于10%%, got %.4f%%", profit)
	}
	if profit <= 0 {
		t.Fatalf("盈利交易收益率应为正, got %.4f%%", profit)
	}
}

// ============================================================================
// 回测引擎测试（卖出统一走 Seller 组合）
// ============================================================================

func TestDo止损触发(t *testing.T) {
	day0 := testKline(time.Date(2024, 1, 2, 15, 0, 0, 0, time.Local), 10, 10, 10, 10)
	day1 := testKline(time.Date(2024, 1, 3, 15, 0, 0, 0, time.Local), 9, 9, 9, 9)

	dks := extend.Klines{day0, day1}

	bt := core.Backtest{
		Buyer:    alwaysBuyer{},
		Seller:   sell.Or{sell.A止盈止损{StopLoss: 0.08}},
		Cost:     core.Cost{Slippage: protocol.Yuan(0)},
		Position: core.PositionConfig{SharesPerLot: 100},
	}

	ts := bt.Do("test", nil, dks, nil)
	if len(ts) < 1 {
		t.Fatalf("expected at least 1 trade (stop loss), got %d", len(ts))
	}
	if !ts[0].SellTime.Equal(day1.Time) {
		t.Fatalf("expected sell on day1 (stop loss), got %v", ts[0].SellTime)
	}
	if ts[0].Virtual {
		t.Fatal("first trade should not be virtual (stop loss)")
	}
}

func TestDo止盈触发(t *testing.T) {
	day0 := testKline(time.Date(2024, 1, 2, 15, 0, 0, 0, time.Local), 10, 10, 10, 10)
	day1 := testKline(time.Date(2024, 1, 3, 15, 0, 0, 0, time.Local), 12, 12, 12, 12)

	dks := extend.Klines{day0, day1}

	bt := core.Backtest{
		Buyer:    alwaysBuyer{},
		Seller:   sell.Or{sell.A止盈止损{TakeProfit: 0.15}},
		Cost:     core.Cost{Slippage: protocol.Yuan(0)},
		Position: core.PositionConfig{SharesPerLot: 100},
	}

	ts := bt.Do("test", nil, dks, nil)
	if len(ts) < 1 {
		t.Fatalf("expected at least 1 trade (take profit), got %d", len(ts))
	}
	if !ts[0].SellTime.Equal(day1.Time) {
		t.Fatalf("expected sell on day1 (take profit), got %v", ts[0].SellTime)
	}
	if ts[0].Virtual {
		t.Fatal("first trade should not be virtual (take profit)")
	}
}

func TestDo持仓天数上限(t *testing.T) {
	base := time.Date(2024, 1, 2, 15, 0, 0, 0, time.Local)
	dks := make(extend.Klines, 25)
	for i := 0; i < 25; i++ {
		dks[i] = testKline(base.AddDate(0, 0, i), 10, 10, 10, 10)
	}

	bt := core.Backtest{
		Buyer:    alwaysBuyer{},
		Seller:   sell.Or{sell.A持仓N天{Days: 5}},
		Cost:     core.Cost{Slippage: protocol.Yuan(0)},
		Position: core.PositionConfig{SharesPerLot: 100},
	}

	ts := bt.Do("test", nil, dks, nil)
	if len(ts) == 0 {
		t.Fatalf("expected trades, got 0")
	}
	expectedSell := dks[5].Time
	if !ts[0].SellTime.Equal(expectedSell) {
		t.Fatalf("expected first sell on day5 (max holding), got %v", ts[0].SellTime)
	}
}

func TestDoT加1规则(t *testing.T) {
	day0 := testKline(time.Date(2024, 1, 2, 15, 0, 0, 0, time.Local), 10, 10, 10, 10)
	day1 := testKline(time.Date(2024, 1, 3, 15, 0, 0, 0, time.Local), 11, 11, 11, 11)
	day2 := testKline(time.Date(2024, 1, 4, 15, 0, 0, 0, time.Local), 12, 12, 12, 12)

	dks := extend.Klines{day0, day1, day2}

	bt := core.Backtest{
		Buyer:    alwaysBuyer{},
		Seller:   alwaysSeller{},
		Cost:     core.Cost{Slippage: protocol.Yuan(0)},
		Position: core.PositionConfig{SharesPerLot: 100},
	}

	ts := bt.Do("test", nil, dks, nil)
	if len(ts) == 0 {
		t.Fatalf("expected trades, got 0")
	}
	for _, tr := range ts {
		if tr.Virtual {
			continue
		}
		if tr.BuyTime.Equal(tr.SellTime) {
			t.Fatalf("T+1 violated: trade bought and sold on %v", tr.BuyTime)
		}
	}
}

func TestDo纯策略卖出(t *testing.T) {
	day0 := testKline(time.Date(2024, 1, 2, 15, 0, 0, 0, time.Local), 10, 10, 10, 10)
	day1 := testKline(time.Date(2024, 1, 3, 15, 0, 0, 0, time.Local), 11, 11, 11, 11)
	day2 := testKline(time.Date(2024, 1, 4, 15, 0, 0, 0, time.Local), 12, 12, 12, 12)

	dks := extend.Klines{day0, day1, day2}

	bt := core.Backtest{
		Buyer:    alwaysBuyer{},
		Seller:   alwaysSeller{},
		Cost:     core.Cost{Slippage: protocol.Yuan(0)},
		Position: core.PositionConfig{SharesPerLot: 100},
	}

	ts := bt.Do("test", nil, dks, nil)
	if len(ts) == 0 {
		t.Fatalf("expected trades, got 0")
	}
	if !ts[len(ts)-1].Virtual {
		t.Fatal("last trade should be virtual (end of period)")
	}
}

// ============================================================================
// 辅助类型
// ============================================================================

type alwaysBuyer struct{}

func (alwaysBuyer) Name() string                            { return "always" }
func (alwaysBuyer) Buy(code string, dks extend.Klines) bool { return true }

type alwaysSeller struct{}

func (alwaysSeller) Name() string                                           { return "always" }
func (alwaysSeller) Sell(code string, dks extend.Klines, buy core.Buy) bool { return true }

type neverSeller struct{}

func (neverSeller) Name() string                                           { return "never" }
func (neverSeller) Sell(code string, dks extend.Klines, buy core.Buy) bool { return false }

func testKline(t time.Time, open, high, low, close float64) *extend.Kline {
	return &extend.Kline{
		Unix: t.Unix(),
		Kline: &protocol.Kline{
			Time:  t,
			Open:  protocol.Yuan(open),
			High:  protocol.Yuan(high),
			Low:   protocol.Yuan(low),
			Close: protocol.Yuan(close),
		},
	}
}
