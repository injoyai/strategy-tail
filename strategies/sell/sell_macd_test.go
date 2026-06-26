package sell

import (
	"testing"
	"time"

	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/lib/extend"
	"github.com/injoyai/tdx/protocol"
)

func TestMACD买入后连跌默认买入后一天跌则卖出(t *testing.T) {
	ks := makeMACD连跌K线([]float64{10, 11, 12, 13, 14, 12})
	buy := core.Buy{Code: "sh600000", Time: ks[4].Time, Price: ks[4].Close}

	if !(MACD买入后连跌{Fast: 1, Slow: 2, Signal: 2}).Sell("sh600000", ks, buy) {
		t.Fatal("expected sell when MACD histogram falls after one trading day")
	}
}

func TestMACD买入后连跌未到买入后N天不卖出(t *testing.T) {
	ks := makeMACD连跌K线([]float64{10, 11, 12, 13, 14, 12})
	buy := core.Buy{Code: "sh600000", Time: ks[4].Time, Price: ks[4].Close}

	if (MACD买入后连跌{Fast: 1, Slow: 2, Signal: 2, AfterDays: 2}).Sell("sh600000", ks, buy) {
		t.Fatal("expected no sell before AfterDays is reached")
	}
}

func TestMACD买入后连跌连续两天跌则卖出(t *testing.T) {
	ks := makeMACD连跌K线([]float64{10, 11, 12, 13, 14, 12, 8})
	buy := core.Buy{Code: "sh600000", Time: ks[4].Time, Price: ks[4].Close}

	if !(MACD买入后连跌{Fast: 1, Slow: 2, Signal: 2, AfterDays: 1, Days: 2}).Sell("sh600000", ks, buy) {
		t.Fatal("expected sell when MACD histogram falls for two consecutive days")
	}
}

func TestMACD买入后连跌连续天数不足不卖出(t *testing.T) {
	ks := makeMACD连跌K线([]float64{10, 11, 12, 13, 14, 12, 13})
	buy := core.Buy{Code: "sh600000", Time: ks[4].Time, Price: ks[4].Close}

	if (MACD买入后连跌{Fast: 1, Slow: 2, Signal: 2, AfterDays: 1, Days: 2}).Sell("sh600000", ks, buy) {
		t.Fatal("expected no sell when MACD histogram does not fall for required consecutive days")
	}
}

func makeMACD连跌K线(closes []float64) extend.Klines {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.Local)
	ks := make(extend.Klines, len(closes))
	for i, close := range closes {
		ks[i] = &extend.Kline{
			Unix: base.AddDate(0, 0, i).Unix(),
			Kline: &protocol.Kline{
				Time:   base.AddDate(0, 0, i),
				Open:   protocol.Yuan(close),
				Close:  protocol.Yuan(close),
				High:   protocol.Yuan(close + 0.5),
				Low:    protocol.Yuan(close - 0.5),
				Volume: 100,
			},
		}
	}
	return ks
}
