package sell

import (
	"testing"

	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/lib/extend"
	"github.com/injoyai/tdx/protocol"
)

func Test止盈止损_达到止盈应卖出(t *testing.T) {
	ks := make持仓K线(10)
	// 买入价 10.2（第2天），后续价格持续上涨到 11+（>5%）
	buy := makeBuyAt(ks, 2)

	s := A止盈止损{TakeProfit: 0.05, StopLoss: 0.03}
	if !s.Sell("sh600000", ks, buy) {
		t.Fatal("达到止盈应触发卖出")
	}
}

func Test止盈止损_达到止损应卖出(t *testing.T) {
	ks := make持仓K线(10)
	// 构造一个高价买入后下跌的场景
	// make持仓K线 每天价格 10+i*0.1，第5天约10.5
	// 手动设置买入价为 11.0（高于后续所有价格），触发3%止损
	buy := core.Buy{
		Code:  "sh600000",
		Time:  ks[5].Time,
		Price: ks[5].Close, // 买入价约10.5
	}
	// 把买入后的价格调低，使跌幅超过3%
	// 10.5 * 0.97 = 10.185，需要收盘价低于此
	for i := 6; i < len(ks); i++ {
		ks[i].Close = protocol.Yuan(10.0)
		ks[i].Open = protocol.Yuan(10.0)
	}

	s := A止盈止损{TakeProfit: 0.05, StopLoss: 0.03}
	if !s.Sell("sh600000", ks, buy) {
		t.Fatal("达到止损应触发卖出")
	}
}

func Test止盈止损_买入日前历史数据不误触发(t *testing.T) {
	ks := make持仓K线(10)
	// 买入在第8天（价格约10.8），之前有更低的价格
	// 如果遍历历史数据会误触发，但正确实现不应触发
	buy := makeBuyAt(ks, 8)

	// 止盈5%：10.8 * 1.05 = 11.34，最后一天价格约10.9，未达到
	// 止损3%：10.8 * 0.97 = 10.476，最后一天价格约10.9，未达到
	s := A止盈止损{TakeProfit: 0.05, StopLoss: 0.03}
	if s.Sell("sh600000", ks, buy) {
		t.Fatal("买入日前的历史数据不应导致误触发")
	}
}

func Test止盈止损_未达到不卖出(t *testing.T) {
	ks := make持仓K线(10)
	buy := makeBuyAt(ks, 2)

	// 止盈50% / 止损30%，正常波动不会触发
	s := A止盈止损{TakeProfit: 0.50, StopLoss: 0.30}
	if s.Sell("sh600000", ks, buy) {
		t.Fatal("未达到止盈止损不应触发")
	}
}

func makeBuyAt(ks extend.Klines, idx int) core.Buy {
	return core.Buy{
		Code:  "sh600000",
		Time:  ks[idx].Time,
		Price: ks[idx].Close,
	}
}
