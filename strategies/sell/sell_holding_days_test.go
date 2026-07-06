package sell

import (
	"testing"
	"time"

	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/lib/extend"
	"github.com/injoyai/tdx/protocol"
)

func make持仓K线(n int) extend.Klines {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.Local)
	ks := make(extend.Klines, n)
	for i := 0; i < n; i++ {
		price := 10 + float64(i)*0.1
		ks[i] = &extend.Kline{
			Unix: base.AddDate(0, 0, i).Unix(),
			Kline: &protocol.Kline{
				Time:   base.AddDate(0, 0, i),
				Open:   protocol.Yuan(price),
				Close:  protocol.Yuan(price),
				High:   protocol.Yuan(price + 0.5),
				Low:    protocol.Yuan(price - 0.5),
				Volume: 100,
			},
		}
	}
	return ks
}

func Test持仓N天_达到天数应卖出(t *testing.T) {
	ks := make持仓K线(10)
	buy := core.Buy{
		Code:  "sh600000",
		Time:  ks[2].Time, // 第3天买入
		Price: ks[2].Close,
	}

	// 持仓5天（不含买入日），ks 有 10 天，从第2天开始有 7 天持仓，>=5 应触发
	s := A持仓N天{Days: 5}
	if !s.Sell("sh600000", ks, buy) {
		t.Fatal("持仓达到5天应触发卖出")
	}
}

func Test持仓N天_未达到天数不卖出(t *testing.T) {
	ks := make持仓K线(6)
	buy := core.Buy{
		Code:  "sh600000",
		Time:  ks[2].Time, // 第3天买入
		Price: ks[2].Close,
	}

	// 从第2天开始有 3 天持仓，<5 不应触发
	s := A持仓N天{Days: 5}
	if s.Sell("sh600000", ks, buy) {
		t.Fatal("持仓不足5天不应触发卖出")
	}
}

func Test持仓N天_刚好达到天数应卖出(t *testing.T) {
	ks := make持仓K线(8)
	buy := core.Buy{
		Code:  "sh600000",
		Time:  ks[2].Time, // 第3天买入
		Price: ks[2].Close,
	}

	// 从第2天开始有 5 天持仓（不含买入日），>=5 应触发
	s := A持仓N天{Days: 5}
	if !s.Sell("sh600000", ks, buy) {
		t.Fatal("持仓刚好5天应触发卖出")
	}
}

func Test持仓N天_默认天数5(t *testing.T) {
	s := A持仓N天{}
	name := s.Name()
	if name == "" {
		t.Fatal("Name 不应为空")
	}

	ks := make持仓K线(10)
	buy := core.Buy{
		Code:  "sh600000",
		Time:  ks[3].Time,
		Price: ks[3].Close,
	}

	// 默认5天，从第3天开始有6天持仓 >= 5 应触发
	if !s.Sell("sh600000", ks, buy) {
		t.Fatal("默认5天，持仓6天应触发")
	}
}

func Test持仓N天_买入日在最后一天(t *testing.T) {
	ks := make持仓K线(5)
	buy := core.Buy{
		Code:  "sh600000",
		Time:  ks[4].Time, // 最后一天买入
		Price: ks[4].Close,
	}

	// 持仓0天，<5 不应触发
	s := A持仓N天{Days: 5}
	if s.Sell("sh600000", ks, buy) {
		t.Fatal("只持仓0天不应触发")
	}
}
