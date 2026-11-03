package buy

import (
	"testing"
	"time"

	"github.com/injoyai/strategy-tail/lib/extend"
	"github.com/injoyai/tdx/v2/protocol"
)

// p6Klines 构造测试用 K 线序列，每根按 [4]float64{开, 收, 高, 低}（元）给定，
// 日期自 2026-01-01 起逐日递增。仅本文件使用（p6 = 迁移批次 P6）。
func p6Klines(bars ...[4]float64) extend.Klines {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.Local)
	ks := make(extend.Klines, len(bars))
	for i, b := range bars {
		tm := base.AddDate(0, 0, i)
		ks[i] = &extend.Kline{
			Unix: tm.Unix(),
			Kline: &protocol.Kline{
				Time:  tm,
				Open:  b[0],
				Close: b[1],
				High:  b[2],
				Low:   b[3],
			},
		}
	}
	return ks
}

// TestP6原生谓词_代码前缀类别 5 个原生谓词在各代码类别下的行为快照。
// 期望值按当前实现逐项核对后固定：A全部 恒真；板块谓词要求 AddPrefix 后
// len==8 且前缀匹配；ETF 谓词直接 protocol.IsETF，不接受未加前缀的 6 位码。
func TestP6原生谓词_代码前缀类别(t *testing.T) {
	cases := []struct {
		name                 string
		code                 string
		all, cyb, kcb, bjbnd bool
		etf                  bool
	}{
		{"创业板裸码", "300001", true, true, false, false, false},
		{"创业板已加前缀", "sz300001", true, true, false, false, false},
		{"科创板裸码", "688001", true, false, true, false, false},
		{"北证裸码", "920001", true, false, false, true, false},
		{"沪主板", "600000", true, false, false, false, false},
		{"深主板", "000001", true, false, false, false, false},
		{"非8位短码", "60000", true, false, false, false, false},
		{"空前缀", "", true, false, false, false, false},
		{"沪ETF已加前缀", "sh510300", true, false, false, false, true},
		{"深ETF已加前缀", "sz159915", true, false, false, false, true},
		{"深ETF未加前缀", "159915", true, false, false, false, false},
	}
	for _, tc := range cases {
		if got := (A全部{}).Buy(tc.code, nil); got != tc.all {
			t.Errorf("%s: A全部 = %v, 期望 %v", tc.name, got, tc.all)
		}
		if got := (A创业板{}).Buy(tc.code, nil); got != tc.cyb {
			t.Errorf("%s: A创业板 = %v, 期望 %v", tc.name, got, tc.cyb)
		}
		if got := (A科创板{}).Buy(tc.code, nil); got != tc.kcb {
			t.Errorf("%s: A科创板 = %v, 期望 %v", tc.name, got, tc.kcb)
		}
		if got := (A北证板{}).Buy(tc.code, nil); got != tc.bjbnd {
			t.Errorf("%s: A北证板 = %v, 期望 %v", tc.name, got, tc.bjbnd)
		}
		if got := (ETF{}).Buy(tc.code, nil); got != tc.etf {
			t.Errorf("%s: ETF = %v, 期望 %v", tc.name, got, tc.etf)
		}
	}
}

// TestP6收盘大于昨收_边界与退化 行为快照：严格大于、预热差一根、平价行情。
func TestP6收盘大于昨收_边界与退化(t *testing.T) {
	s := A收盘大于昨收{}
	cases := []struct {
		name string
		ks   extend.Klines
		want bool
	}{
		{"今收高于昨收", p6Klines([4]float64{13.7, 13.8, 13.9, 13.6}, [4]float64{14.0, 14.1, 14.2, 13.9}), true},
		{"今收等于昨收不触发", p6Klines([4]float64{13.7, 13.8, 13.9, 13.6}, [4]float64{14.0, 13.8, 14.1, 13.7}), false},
		{"今收低于昨收", p6Klines([4]float64{13.7, 13.8, 13.9, 13.6}, [4]float64{14.0, 13.5, 14.1, 13.4}), false},
		{"预热差一根", p6Klines([4]float64{13.7, 13.8, 13.9, 13.6}), false},
		{"空K线", nil, false},
		{"平价行情", p6Klines([4]float64{10, 10, 10, 10}, [4]float64{10, 10, 10, 10}), false},
	}
	for _, tc := range cases {
		if got := s.Buy("sh600000", tc.ks); got != tc.want {
			t.Errorf("%s: A收盘大于昨收 = %v, 期望 %v", tc.name, got, tc.want)
		}
	}
}

// TestP6收盘大于昨开_边界与退化 行为快照：严格大于、预热差一根、平价行情。
func TestP6收盘大于昨开_边界与退化(t *testing.T) {
	s := A收盘大于昨开{}
	cases := []struct {
		name string
		ks   extend.Klines
		want bool
	}{
		{"今收高于昨开", p6Klines([4]float64{13.7, 13.8, 13.9, 13.6}, [4]float64{13.9, 14.0, 14.1, 13.8}), true},
		{"今收等于昨开不触发", p6Klines([4]float64{13.7, 13.8, 13.9, 13.6}, [4]float64{13.8, 13.7, 13.9, 13.6}), false},
		{"今收低于昨开", p6Klines([4]float64{13.7, 13.8, 13.9, 13.6}, [4]float64{13.9, 13.5, 14.0, 13.4}), false},
		{"预热差一根", p6Klines([4]float64{13.7, 13.8, 13.9, 13.6}), false},
		{"空K线", nil, false},
		{"平价行情", p6Klines([4]float64{10, 10, 10, 10}, [4]float64{10, 10, 10, 10}), false},
	}
	for _, tc := range cases {
		if got := s.Buy("sh600000", tc.ks); got != tc.want {
			t.Errorf("%s: A收盘大于昨开 = %v, 期望 %v", tc.name, got, tc.want)
		}
	}
}

// TestP6实体阴线_参数与形态边界 行为快照：收阴判定、目录默认 0、非默认比例、
// 占比边界相等、负值退化、单根 K 线（预热 1 根）、平价行情。
func TestP6实体阴线_参数与形态边界(t *testing.T) {
	// 实体 0.5 / 振幅 0.7 ≈ 0.714
	阴线 := [4]float64{14.4, 13.9, 14.5, 13.8}
	// 实体 0.25 / 振幅 1.2 ≈ 0.208（十字星式）
	十字星 := [4]float64{14.2, 13.95, 14.5, 13.3}
	// 实体 0.5 / 振幅 1.0 = 精确 0.5（占比边界相等）
	精确半 := [4]float64{10.0, 9.5, 10.0, 9.0}

	cases := []struct {
		name  string
		ratio float64
		ks    extend.Klines
		want  bool
	}{
		{"标准阴线目录默认0", 0, p6Klines(阴线), true},
		{"标准阴线非默认0.7", 0.7, p6Klines(阴线), true},
		{"标准阴线非默认0.8不足", 0.8, p6Klines(阴线), false},
		{"十字星默认0收阴即触发", 0, p6Klines(十字星), true},
		{"十字星0.3过滤", 0.3, p6Klines(十字星), false},
		{"占比边界相等0.5含边界", 0.5, p6Klines(精确半), true},
		{"占比略高于实体0.51", 0.51, p6Klines(精确半), false},
		{"负阈值退化为只要收阴", -0.5, p6Klines(十字星), true},
		{"收阳不触发", 0, p6Klines([4]float64{13.6, 14.2, 14.5, 13.3}), false},
		{"收平不触发", 0, p6Klines([4]float64{14.0, 14.0, 14.5, 13.5}), false},
		{"平价行情", 0, p6Klines([4]float64{10, 10, 10, 10}), false},
		{"单根阴线达标", 0.7, p6Klines([4]float64{10.0, 9.5, 10.0, 9.5}), true},
		{"空K线", 0.3, nil, false},
	}
	for _, tc := range cases {
		s := A实体阴线{MinBodyRatio: tc.ratio}
		if got := s.Buy("sh600000", tc.ks); got != tc.want {
			t.Errorf("%s: A实体阴线(MinBodyRatio=%v) = %v, 期望 %v", tc.name, tc.ratio, got, tc.want)
		}
	}
}
