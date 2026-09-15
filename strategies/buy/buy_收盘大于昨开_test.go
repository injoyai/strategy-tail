package buy

import (
	"testing"
)

// Test收盘大于昨开_满足条件触发 当日收盘价高于昨日开盘价时应触发。
func Test收盘大于昨开_满足条件触发(t *testing.T) {
	opens, closes, highs, lows := make上升趋势(20, 0.2)
	// 基础序列末天（索引19）：开13.7 收13.8
	// 追加一天：开13.9 收14.0，14.0 > 13.7 应触发
	opens = append(opens, 13.9)
	closes = append(closes, 14.0)
	highs = append(highs, 14.1)
	lows = append(lows, 13.8)

	ks := make阴线收回K线(opens, closes, highs, lows)

	s := A收盘大于昨开{}
	if !s.Buy("sh600000", ks) {
		t.Fatalf("收盘%.2f > 昨开%.2f 应触发", closes[len(closes)-1], opens[len(opens)-2])
	}
}

// Test收盘大于昨开_低于昨开不触发 当日收盘价低于昨日开盘价不应触发。
func Test收盘大于昨开_低于昨开不触发(t *testing.T) {
	opens, closes, highs, lows := make上升趋势(20, 0.2)
	// 追加一天：开13.9 收13.5，13.5 < 13.7 不应触发
	opens = append(opens, 13.9)
	closes = append(closes, 13.5)
	highs = append(highs, 14.0)
	lows = append(lows, 13.4)

	ks := make阴线收回K线(opens, closes, highs, lows)

	s := A收盘大于昨开{}
	if s.Buy("sh600000", ks) {
		t.Fatal("收盘低于昨开不应触发")
	}
}

// Test收盘大于昨开_等于昨开不触发 严格大于，相等不触发。
func Test收盘大于昨开_等于昨开不触发(t *testing.T) {
	opens, closes, highs, lows := make上升趋势(20, 0.2)
	// 追加一天：收13.7 == 昨开13.7，不应触发
	opens = append(opens, 13.8)
	closes = append(closes, 13.7)
	highs = append(highs, 13.9)
	lows = append(lows, 13.6)

	ks := make阴线收回K线(opens, closes, highs, lows)

	s := A收盘大于昨开{}
	if s.Buy("sh600000", ks) {
		t.Fatal("收盘等于昨开（非严格大于）不应触发")
	}
}

// Test收盘大于昨开_仅一根K线不触发 无昨日数据应安全返回 false。
func Test收盘大于昨开_仅一根K线不触发(t *testing.T) {
	opens, closes, highs, lows := make上升趋势(1, 0.2)
	ks := make阴线收回K线(opens, closes, highs, lows)

	s := A收盘大于昨开{}
	if s.Buy("sh600000", ks) {
		t.Fatal("仅一根K线（无昨日）不应触发")
	}
}

// Test收盘大于昨开_空数据不触发 空数据应安全返回 false。
func Test收盘大于昨开_空数据不触发(t *testing.T) {
	s := A收盘大于昨开{}
	if s.Buy("sh600000", nil) {
		t.Fatal("空数据不应触发")
	}
}
