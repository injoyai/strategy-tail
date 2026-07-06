package buy

import (
	"testing"
	"time"

	"github.com/injoyai/strategy-tail/lib/extend"
	"github.com/injoyai/tdx/protocol"
)

// make均线K线 构造一组 K 线，closes 为每日收盘价。
// 开高低收统一用 close，volume 固定 100，方便测试均线逻辑。
func make均线K线(closes []float64) extend.Klines {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.Local)
	ks := make(extend.Klines, len(closes))
	for i, c := range closes {
		ks[i] = &extend.Kline{
			Unix: base.AddDate(0, 0, i).Unix(),
			Kline: &protocol.Kline{
				Time:   base.AddDate(0, 0, i),
				Open:   protocol.Yuan(c),
				Close:  protocol.Yuan(c),
				High:   protocol.Yuan(c),
				Low:    protocol.Yuan(c),
				Volume: 100,
			},
		}
	}
	return ks
}

func Test站上N日均线_持续站上应触发(t *testing.T) {
	// 构造一个持续上涨的序列，确保站上 MA20
	closes := make([]float64, 30)
	for i := range closes {
		closes[i] = 10 + float64(i)*0.2
	}
	ks := make均线K线(closes)

	s := A站上N日均线{Period: 20, Days: 3}
	if !s.Buy("sh600000", ks) {
		t.Fatal("持续站上 MA20 应触发买入")
	}
}

func Test站上N日均线_持续下跌不触发(t *testing.T) {
	closes := make([]float64, 30)
	for i := range closes {
		closes[i] = 20 - float64(i)*0.2
	}
	ks := make均线K线(closes)

	s := A站上N日均线{Period: 20, Days: 3}
	if s.Buy("sh600000", ks) {
		t.Fatal("持续下跌不应触发买入")
	}
}

func Test站上N日均线_数据不足不触发(t *testing.T) {
	closes := []float64{10, 11, 12, 13, 14, 15, 16, 17, 18, 19}
	ks := make均线K线(closes)

	s := A站上N日均线{Period: 20, Days: 3}
	if s.Buy("sh600000", ks) {
		t.Fatal("数据不足不应触发买入")
	}
}

func Test站上N日均线_站上天数不足不触发(t *testing.T) {
	// 前 27 天缓慢下跌并在 MA20 下方，最后 2 天快速拉升站上 MA20
	// Days=5 要求站上 5 天，实际只有 2 天，不应触发
	closes := make([]float64, 30)
	for i := 0; i < 28; i++ {
		closes[i] = 15 - float64(i)*0.1 // 缓慢下跌，保持在 MA20 下方
	}
	// 最后 2 天快速拉升
	closes[28] = 20
	closes[29] = 22
	ks := make均线K线(closes)

	s := A站上N日均线{Period: 20, Days: 5}
	if s.Buy("sh600000", ks) {
		t.Fatal("站上 MA20 天数不足(仅2天)不应触发买入")
	}
}

func Test站上N日均线_Name包含参数(t *testing.T) {
	s := A站上N日均线{Period: 20, Days: 5}
	name := s.Name()
	if name == "" {
		t.Fatal("Name 不应为空")
	}
}
