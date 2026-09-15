package buy

import (
	"testing"
)

// Test实体阴线_标准形态应触发 收盘低于开盘，实体占比高。
func Test实体阴线_标准形态应触发(t *testing.T) {
	opens, closes, highs, lows := make上升趋势(20, 0.2)
	// 最后一天高开低走收实体阴线：开14.4 收13.9，实体0.5，振幅0.7，比例约0.714
	opens = append(opens, 14.4)
	closes = append(closes, 13.9)
	highs = append(highs, 14.5)
	lows = append(lows, 13.8)

	ks := make阴线收回K线(opens, closes, highs, lows)

	for _, ratio := range []float64{0, 0.3, 0.5, 0.7} {
		s := A实体阴线{MinBodyRatio: ratio}
		if !s.Buy("sh600000", ks) {
			t.Fatalf("标准实体阴线在 MinBodyRatio=%.1f 下应触发", ratio)
		}
	}
}

// Test实体阴线_十字星不触发 实体占振幅比例不足被过滤。
func Test实体阴线_十字星不触发(t *testing.T) {
	opens, closes, highs, lows := make上升趋势(20, 0.2)
	// 开14.2 收13.95：实体0.25，振幅1.2，比例约0.208 < 0.3
	opens = append(opens, 14.2)
	closes = append(closes, 13.95)
	highs = append(highs, 14.5)
	lows = append(lows, 13.3)

	ks := make阴线收回K线(opens, closes, highs, lows)

	s := A实体阴线{MinBodyRatio: 0.3}
	if s.Buy("sh600000", ks) {
		t.Fatal("十字星式阴线实体不足不应触发")
	}

	// MinBodyRatio=0 时只要收阴即可，应触发
	s2 := A实体阴线{MinBodyRatio: 0}
	if !s2.Buy("sh600000", ks) {
		t.Fatal("MinBodyRatio=0 时收阴即应触发")
	}
}

// Test实体阴线_收阳不触发 形态要求收阴。
func Test实体阴线_收阳不触发(t *testing.T) {
	opens, closes, highs, lows := make上升趋势(20, 0.2)
	opens = append(opens, 13.6)
	closes = append(closes, 14.2)
	highs = append(highs, 14.5)
	lows = append(lows, 13.3)

	ks := make阴线收回K线(opens, closes, highs, lows)

	s := A实体阴线{MinBodyRatio: 0.3}
	if s.Buy("sh600000", ks) {
		t.Fatal("收阳不应触发")
	}
}

// Test实体阴线_收平不触发 开盘价等于收盘价不算阴线。
func Test实体阴线_收平不触发(t *testing.T) {
	opens, closes, highs, lows := make上升趋势(20, 0.2)
	opens = append(opens, 14.0)
	closes = append(closes, 14.0)
	highs = append(highs, 14.5)
	lows = append(lows, 13.5)

	ks := make阴线收回K线(opens, closes, highs, lows)

	s := A实体阴线{MinBodyRatio: 0}
	if s.Buy("sh600000", ks) {
		t.Fatal("收平不应触发")
	}
}

// Test实体阴线_空数据不触发 无 K 线时应安全返回 false。
func Test实体阴线_空数据不触发(t *testing.T) {
	s := A实体阴线{MinBodyRatio: 0.3}
	if s.Buy("sh600000", nil) {
		t.Fatal("空数据不应触发")
	}
}
