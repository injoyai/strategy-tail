package sell

import (
	"testing"
	"time"

	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/lib/extend"
	"github.com/injoyai/tdx/protocol"
)

func TestA买入次日跌破买入价触发卖出(t *testing.T) {
	ks := makeBreakBuyPrice连续K线(3)
	// 买入价 10，次日收盘 9
	ks[1].Open = protocol.Yuan(9.5)
	ks[1].Close = protocol.Yuan(9)
	buy := core.Buy{Code: "sh600000", Time: ks[0].Time, Price: protocol.Yuan(10)}

	if !(A买入次日跌破买入价{}).Sell("sh600000", ks[:2], buy) {
		t.Fatal("expected sell when next day close < buy price")
	}
}

func TestA买入次日跌破买入价当日不卖(t *testing.T) {
	ks := makeBreakBuyPrice连续K线(2)
	ks[0].Close = protocol.Yuan(9)
	buy := core.Buy{Code: "sh600000", Time: ks[0].Time, Price: protocol.Yuan(10)}

	if (A买入次日跌破买入价{}).Sell("sh600000", ks[:1], buy) {
		t.Fatal("expected no sell on the buy day")
	}
}

func TestA买入次日跌破买入价仅次日生效(t *testing.T) {
	ks := makeBreakBuyPrice连续K线(3)
	// 次日不跌破，第三日跌破，按"仅次日"语义不应触发
	ks[1].Close = protocol.Yuan(11)
	ks[2].Close = protocol.Yuan(9)
	buy := core.Buy{Code: "sh600000", Time: ks[0].Time, Price: protocol.Yuan(10)}

	if (A买入次日跌破买入价{}).Sell("sh600000", ks, buy) {
		t.Fatal("expected no sell beyond the next trading day")
	}
}

func TestA买入次日跌破买入价次日不跌破不卖(t *testing.T) {
	ks := makeBreakBuyPrice连续K线(2)
	ks[1].Close = protocol.Yuan(10)
	buy := core.Buy{Code: "sh600000", Time: ks[0].Time, Price: protocol.Yuan(10)}

	if (A买入次日跌破买入价{}).Sell("sh600000", ks, buy) {
		t.Fatal("expected no sell when next day close is not below buy price")
	}
}

func makeBreakBuyPrice连续K线(n int) extend.Klines {
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
