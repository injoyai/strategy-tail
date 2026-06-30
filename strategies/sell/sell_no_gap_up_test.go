package sell

import (
	"testing"
	"time"

	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/lib/extend"
	"github.com/injoyai/tdx/protocol"
)

func TestA买入次日未高开开盘等于昨日收盘卖出(t *testing.T) {
	ks := makeNoGapUp连续K线(2)
	// 昨日收盘 10.5，今日开盘 10.5（未高开）
	ks[0].Close = protocol.Yuan(10.5)
	ks[1].Open = protocol.Yuan(10.5)
	buy := core.Buy{Code: "sh600000", Time: ks[0].Time, Price: ks[0].Close}

	if !(A买入次日未高开{}).Sell("sh600000", ks, buy) {
		t.Fatal("expected sell when next day open is equal to previous close (no gap up)")
	}
}

func TestA买入次日未高开开盘低于昨日收盘卖出(t *testing.T) {
	ks := makeNoGapUp连续K线(2)
	ks[0].Close = protocol.Yuan(10.5)
	ks[1].Open = protocol.Yuan(10.0)
	buy := core.Buy{Code: "sh600000", Time: ks[0].Time, Price: ks[0].Close}

	if !(A买入次日未高开{}).Sell("sh600000", ks, buy) {
		t.Fatal("expected sell when next day opens below previous close")
	}
}

func TestA买入次日未高开高开则不卖(t *testing.T) {
	ks := makeNoGapUp连续K线(2)
	ks[0].Close = protocol.Yuan(10.5)
	ks[1].Open = protocol.Yuan(10.6)
	buy := core.Buy{Code: "sh600000", Time: ks[0].Time, Price: ks[0].Close}

	if (A买入次日未高开{}).Sell("sh600000", ks, buy) {
		t.Fatal("expected no sell when next day gaps up")
	}
}

func TestA买入次日未高开仅次日生效(t *testing.T) {
	ks := makeNoGapUp连续K线(3)
	// 次日高开，第三日不高开，按"仅次日"语义不触发
	ks[0].Close = protocol.Yuan(10.5)
	ks[1].Open = protocol.Yuan(10.6)
	ks[1].Close = protocol.Yuan(10.6)
	ks[2].Open = protocol.Yuan(10.5)
	buy := core.Buy{Code: "sh600000", Time: ks[0].Time, Price: ks[0].Close}

	if (A买入次日未高开{}).Sell("sh600000", ks, buy) {
		t.Fatal("expected no sell beyond the next trading day")
	}
}

func makeNoGapUp连续K线(n int) extend.Klines {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.Local)
	ks := make(extend.Klines, n)
	for i := 0; i < n; i++ {
		ks[i] = &extend.Kline{
			Unix: base.AddDate(0, 0, i).Unix(),
			Kline: &protocol.Kline{
				Time:   base.AddDate(0, 0, i),
				Open:   protocol.Yuan(10),
				Close:  protocol.Yuan(10),
				High:   protocol.Yuan(10.5),
				Low:    protocol.Yuan(9.5),
				Volume: 100,
			},
		}
	}
	return ks
}
