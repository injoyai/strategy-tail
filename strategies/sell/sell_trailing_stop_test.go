package sell

import (
	"testing"

	"github.com/injoyai/tdx/protocol"
)

func Test追踪止损_从峰值回撤达阈值应卖出(t *testing.T) {
	ks := make持仓K线(10) // 价格 10, 10.1, ..., 10.9
	buy := makeBuyAt(ks, 2)
	// 把最后一天调低，制造回撤：peak=10.8(idx8)，current=10.0
	ks[9].Close = protocol.Yuan(10.0)
	ks[9].Open = protocol.Yuan(10.0)
	// 回撤 = (10.8-10.0)/10.8 ≈ 7.4% >= 5%
	s := A追踪止损{Drawdown: 0.05}
	if !s.Sell("sh600000", ks, buy) {
		t.Fatal("从峰值回撤超5%应触发卖出")
	}
}

func Test追踪止损_无回撤不卖出(t *testing.T) {
	ks := make持仓K线(10) // 持续上涨，最后一天即峰值
	buy := makeBuyAt(ks, 2)
	s := A追踪止损{Drawdown: 0.05}
	if s.Sell("sh600000", ks, buy) {
		t.Fatal("当前即峰值、无回撤不应触发")
	}
}

func Test追踪止损_回撤不足不卖出(t *testing.T) {
	ks := make持仓K线(10)
	buy := makeBuyAt(ks, 2)
	ks[9].Close = protocol.Yuan(10.7) // peak=10.8, current=10.7, 回撤≈0.93%
	s := A追踪止损{Drawdown: 0.05}
	if s.Sell("sh600000", ks, buy) {
		t.Fatal("回撤不足5%不应触发")
	}
}

func Test追踪止损_买入日前不误触发(t *testing.T) {
	ks := make持仓K线(10)
	buy := makeBuyAt(ks, 8) // 买入靠后，买入后无回撤
	s := A追踪止损{Drawdown: 0.05}
	if s.Sell("sh600000", ks, buy) {
		t.Fatal("买入后无回撤不应触发")
	}
}

func Test追踪止损_关闭时不卖出(t *testing.T) {
	ks := make持仓K线(10)
	buy := makeBuyAt(ks, 2)
	ks[9].Close = protocol.Yuan(10.0)
	s := A追踪止损{Drawdown: 0} // 关闭
	if s.Sell("sh600000", ks, buy) {
		t.Fatal("Drawdown=0 应关闭，不触发")
	}
}
