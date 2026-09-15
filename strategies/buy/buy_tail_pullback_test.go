package buy

import (
	"testing"
	"time"

	"github.com/injoyai/strategy-tail/lib/extend"
	"github.com/injoyai/tdx/protocol"
)

// make阴线收回K线 构造 K 线，每日可指定 open/close/high/low。
func make阴线收回K线(opens, closes, highs, lows []float64) extend.Klines {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.Local)
	n := len(closes)
	ks := make(extend.Klines, n)
	for i := 0; i < n; i++ {
		ks[i] = &extend.Kline{
			Unix: base.AddDate(0, 0, i).Unix(),
			Kline: &protocol.Kline{
				Time:   base.AddDate(0, 0, i),
				Open:   protocol.Yuan(opens[i]),
				Close:  protocol.Yuan(closes[i]),
				High:   protocol.Yuan(highs[i]),
				Low:    protocol.Yuan(lows[i]),
				Volume: 100,
			},
		}
	}
	return ks
}

// make上升趋势 构造 n 天平稳上升趋势的基础 K 线（每天涨 d），返回 opens/closes/highs/lows。
func make上升趋势(n int, d float64) (opens, closes, highs, lows []float64) {
	opens = make([]float64, n)
	closes = make([]float64, n)
	highs = make([]float64, n)
	lows = make([]float64, n)
	for i := 0; i < n; i++ {
		c := 10 + float64(i)*d
		opens[i] = c - d/2
		closes[i] = c
		highs[i] = c + 0.05
		lows[i] = opens[i] - 0.05
	}
	return
}

// Test阴线收回_标准形态应触发 上升趋势中收阴、盘中破MA5、尾盘收回。
func Test阴线收回_标准形态应触发(t *testing.T) {
	opens, closes, highs, lows := make上升趋势(20, 0.2)
	// 第20天（索引19）：高开低走收阴，盘中跌破MA5，尾盘收回
	opens = append(opens, 14.4)
	closes = append(closes, 13.9)
	highs = append(highs, 14.5)
	lows = append(lows, 13.3) // MA5约13.6，盘中跌破

	ks := make阴线收回K线(opens, closes, highs, lows)

	s := A阴线收回{SupportPeriod: 5}
	if !s.Buy("sh600000", ks) {
		t.Fatal("标准阴线收回形态应触发买入")
	}
}

// Test阴线收回_未跌破均线不触发 盘中最低价未触及MA5，不算洗盘。
func Test阴线收回_未跌破均线不触发(t *testing.T) {
	opens, closes, highs, lows := make上升趋势(20, 0.2)
	opens = append(opens, 14.4)
	closes = append(closes, 13.9)
	highs = append(highs, 14.5)
	lows = append(lows, 13.8) // 未跌破MA5

	ks := make阴线收回K线(opens, closes, highs, lows)

	s := A阴线收回{SupportPeriod: 5}
	if s.Buy("sh600000", ks) {
		t.Fatal("盘中未跌破均线不应触发")
	}
}

// Test阴线收回_收盘未收回不触发 尾盘没站回均线。
func Test阴线收回_收盘未收回不触发(t *testing.T) {
	opens, closes, highs, lows := make上升趋势(20, 0.2)
	opens = append(opens, 14.4)
	closes = append(closes, 13.4) // 收在MA5下方
	highs = append(highs, 14.5)
	lows = append(lows, 13.3)

	ks := make阴线收回K线(opens, closes, highs, lows)

	s := A阴线收回{SupportPeriod: 5}
	if s.Buy("sh600000", ks) {
		t.Fatal("收盘未收回均线不应触发")
	}
}

// Test阴线收回_实体不足不触发 十字星式阴线被 MinBodyRatio 过滤。
func Test阴线收回_实体不足不触发(t *testing.T) {
	opens, closes, highs, lows := make上升趋势(20, 0.2)
	opens = append(opens, 14.2)
	closes = append(closes, 13.95) // 实体0.25，振幅约1.3，比例约0.19 < 0.3
	highs = append(highs, 14.5)
	lows = append(lows, 13.3)

	ks := make阴线收回K线(opens, closes, highs, lows)

	s := A阴线收回{SupportPeriod: 5, MinBodyRatio: 0.3}
	if s.Buy("sh600000", ks) {
		t.Fatal("阴线实体不足不应触发")
	}
}

// Test阴线收回_收阳不触发 形态要求收阴。
func Test阴线收回_收阳不触发(t *testing.T) {
	opens, closes, highs, lows := make上升趋势(20, 0.2)
	opens = append(opens, 13.6)
	closes = append(closes, 14.2)
	highs = append(highs, 14.5)
	lows = append(lows, 13.3)

	ks := make阴线收回K线(opens, closes, highs, lows)

	s := A阴线收回{SupportPeriod: 5}
	if s.Buy("sh600000", ks) {
		t.Fatal("收阳不应触发")
	}
}

// Test阴线收回_趋势过滤生效 趋势向下时带趋势过滤不应触发。
func Test阴线收回_趋势过滤生效(t *testing.T) {
	// 构造下降趋势：每天跌0.2
	n := 20
	opens := make([]float64, n)
	closes := make([]float64, n)
	highs := make([]float64, n)
	lows := make([]float64, n)
	for i := 0; i < n; i++ {
		c := 14 - float64(i)*0.2
		opens[i] = c + 0.1
		closes[i] = c
		highs[i] = opens[i] + 0.05
		lows[i] = c - 0.05
	}
	// 最后一天阴线盘中破MA5尾盘收回（前5天收盘：11.0,10.8,10.6,10.4,10.2 → MA5=10.6）
	opens = append(opens, 10.8)
	closes = append(closes, 10.7)
	highs = append(highs, 10.9)
	lows = append(lows, 10.3)

	ks := make阴线收回K线(opens, closes, highs, lows)

	// 无趋势过滤：触发
	s := A阴线收回{SupportPeriod: 5}
	if !s.Buy("sh600000", ks) {
		t.Fatal("无趋势过滤时应触发")
	}

	// MA5向上过滤：下降趋势不满足
	s2 := A阴线收回{SupportPeriod: 5, Trend: MAUp{Period: 5}}
	if s2.Buy("sh600000", ks) {
		t.Fatal("MA5向下时带趋势过滤不应触发")
	}
}

// Test阴线收回_数据不足不触发 K线数量不够计算均线。
func Test阴线收回_数据不足不触发(t *testing.T) {
	opens, closes, highs, lows := make上升趋势(3, 0.2)
	opens = append(opens, 3.4)
	closes = append(closes, 3.2)
	highs = append(highs, 3.5)
	lows = append(lows, 3.0)

	ks := make阴线收回K线(opens, closes, highs, lows)

	s := A阴线收回{SupportPeriod: 5}
	if s.Buy("sh600000", ks) {
		t.Fatal("数据不足不应触发")
	}
}
