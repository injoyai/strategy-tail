package sell

import (
	"testing"

	"github.com/injoyai/strategy-tail/core"
)

func TestSellRSIFactorKeepsSignalTime(t *testing.T) {
	ks := makeMACD连跌K线([]float64{10, 8, 9, 10, 11})
	strategy := SellRSI{Period: 2, Threshold: 50}
	// 第一天 RSI 为 1/3×100；第二天恢复为 100，按原规则使用该日开盘价。
	got := strategy.Sell("sh600000", ks[:2], ks[2:], nil, core.Buy{})
	if got == nil || !got.Time.Equal(ks[3].Time) || got.Price != ks[3].Open || got.Code != "sh600000" {
		t.Fatalf("卖出信号时点或价格改变: %+v", got)
	}
	if got := strategy.Sell("sh600000", ks[:2], ks[2:3], nil, core.Buy{}); got != nil {
		t.Fatal("截断未来数据后不能提前卖出")
	}
	strategy.Threshold = 100
	if got := strategy.Sell("sh600000", ks[:2], ks[2:], nil, core.Buy{}); got != nil {
		t.Fatal("原卖出条件必须严格大于阈值")
	}
	if got := (SellRSI{}).Sell("sh600000", ks[:2], ks[2:], nil, core.Buy{}); got != nil {
		t.Fatal("默认14日RSI的数据不足时不能卖出")
	}
}
