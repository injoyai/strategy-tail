package buy

import (
	"testing"

	"github.com/injoyai/strategy-tail/lib/extend"
	"github.com/injoyai/tdx/v2/protocol"
)

// P2 组行为快照（buy_price / buy_market_cap / buy_limit_up_hit / buy_filter_limit_up）：
// 先针对旧实现固定期望值，迁移到 strategies/rule 共用内核后本文件必须原样通过。
// helper 以 p2 前缀命名，避免与其他并行迁移组冲突。

// p2K 构造单根 K 线。价格一律直接给厘（protocol.Price），避免 Yuan 的浮点截断；
// last 为昨收（厘），决定 RiseRate 的昨收口径（last==0 时退化为对开盘价）；
// floatStock 为流通股本（股），决定 A流通市值 的取值。
func p2K(last, open, high, low, close float64, floatStock int64) *extend.Kline {
	return &extend.Kline{
		Kline: &protocol.Kline{
			Last:   last,
			Open:   open,
			High:   high,
			Low:    low,
			Close:  close,
			Volume: 1000,
		},
		FloatStock: floatStock,
	}
}

// p2Ks 组装 K 线前缀。
func p2Ks(ks ...*extend.Kline) extend.Klines { return ks }

// ---- A价格（buy_price）----

// TestP2A价格_零值参数不限两侧 Min/Max 均为 0 时，有行情即通过；空前缀不通过。
func TestP2A价格_零值参数不限两侧(t *testing.T) {
	ks := p2Ks(p2K(9.9, 10.0, 10.1, 9.8, 10.0, 1e8))
	if !(A价格{}).Buy("sh600000", ks) {
		t.Fatal("零值参数且行情非空应触发")
	}
	if (A价格{}).Buy("sh600000", nil) {
		t.Fatal("零值参数但空前缀不应触发")
	}
}

// TestP2A价格_目录默认2到120 目录默认 Min=2、Max=120。
func TestP2A价格_目录默认2到120(t *testing.T) {
	b := A价格{Min: 2, Max: 120}
	if !b.Buy("sh600000", p2Ks(p2K(9.9, 10.0, 10.1, 9.8, 10.0, 1e8))) {
		t.Fatal("收盘 10 元在 [2,120] 内应触发")
	}
	if b.Buy("sh600000", p2Ks(p2K(1.4, 1.5, 1.6, 1.4, 1.5, 1e8))) {
		t.Fatal("收盘 1.5 元低于下限 2 元不应触发")
	}
	if b.Buy("sh600000", p2Ks(p2K(149.0, 150.0, 151.0, 149.0, 150.0, 1e8))) {
		t.Fatal("收盘 150 元高于上限 120 元不应触发")
	}
}

// TestP2A价格_非默认区间边界含端点 区间判定含下限与上限端点。
func TestP2A价格_非默认区间边界含端点(t *testing.T) {
	b := A价格{Min: 5, Max: 12.5}
	if !b.Buy("sh600000", p2Ks(p2K(4.9, 5.0, 5.1, 4.9, 5.0, 1e8))) {
		t.Fatal("收盘 5 元等于下限应触发")
	}
	if !b.Buy("sh600000", p2Ks(p2K(12.4, 12.5, 12.6, 12.4, 12.5, 1e8))) {
		t.Fatal("收盘 12.5 元等于上限应触发")
	}
	if b.Buy("sh600000", p2Ks(p2K(4.899, 4.999, 5.099, 4.899, 4.999, 1e8))) {
		t.Fatal("收盘 4.999 元低于下限不应触发")
	}
	if b.Buy("sh600000", p2Ks(p2K(12.5, 12.501, 12.601, 12.5, 12.501, 1e8))) {
		t.Fatal("收盘 12.501 元高于上限不应触发")
	}
}

// TestP2A价格_上限小于下限恒不触发 历史退化语义：区间不可能满足时恒 false。
func TestP2A价格_上限小于下限恒不触发(t *testing.T) {
	b := A价格{Min: 10, Max: 5}
	if b.Buy("sh600000", p2Ks(p2K(6.9, 7.0, 7.1, 6.9, 7.0, 1e8))) {
		t.Fatal("Max<Min 时任何价格不应触发")
	}
}

// TestP2A价格_空K线不触发 带区间时空前缀安全返回 false。
func TestP2A价格_空K线不触发(t *testing.T) {
	if (A价格{Min: 2, Max: 120}).Buy("sh600000", nil) {
		t.Fatal("空前缀不应触发")
	}
}

// TestP2A价格_平价行情按收盘判断 high==low、量 0 的平价 K 线只看收盘价。
func TestP2A价格_平价行情按收盘判断(t *testing.T) {
	ks := p2Ks(p2K(10.0, 10.0, 10.0, 10.0, 10.0, 1e8))
	if !(A价格{Min: 5, Max: 12.5}).Buy("sh600000", ks) {
		t.Fatal("平价 K 线收盘 10 元在区间内应触发")
	}
	if (A价格{Min: 10.5, Max: 12.5}).Buy("sh600000", ks) {
		t.Fatal("平价 K 线收盘 10 元低于下限不应触发")
	}
}

// ---- A流通市值（buy_market_cap）----
// 因子口径：value = Close(元) × FloatStock / 1e8（亿元）。
// close=10000 厘（10 元）、股本 1e8 股时 value=10。

// TestP2A流通市值_零值参数不限两侧 Min/Max 均为 0 时，有行情即通过（含缺股本 value=0）。
func TestP2A流通市值_零值参数不限两侧(t *testing.T) {
	if !(A流通市值{}).Buy("sh600000", p2Ks(p2K(9.9, 10.0, 10.1, 9.8, 10.0, 1e8))) {
		t.Fatal("零值参数且行情非空应触发")
	}
	if !(A流通市值{}).Buy("sh600000", p2Ks(p2K(9.9, 10.0, 10.1, 9.8, 10.0, 0))) {
		t.Fatal("零值参数且缺股本（value=0）应触发")
	}
	if (A流通市值{}).Buy("sh600000", nil) {
		t.Fatal("零值参数但空前缀不应触发")
	}
}

// TestP2A流通市值_区间边界含端点 下限与上限端点均含。
func TestP2A流通市值_区间边界含端点(t *testing.T) {
	b := A流通市值{Min: 10, Max: 20}
	if !b.Buy("sh600000", p2Ks(p2K(9.9, 10.0, 10.1, 9.8, 10.0, 1e8))) {
		t.Fatal("value=10 等于下限应触发")
	}
	if !b.Buy("sh600000", p2Ks(p2K(19.9, 20.0, 20.1, 19.9, 20.0, 1e8))) {
		t.Fatal("value=20 等于上限应触发")
	}
	if b.Buy("sh600000", p2Ks(p2K(9.898, 9.999, 10.0, 9.898, 9.999, 1e8))) {
		t.Fatal("value=9.999 低于下限不应触发")
	}
	if b.Buy("sh600000", p2Ks(p2K(20.0, 20.001, 20.101, 20.0, 20.001, 1e8))) {
		t.Fatal("value=20.001 高于上限不应触发")
	}
}

// TestP2A流通市值_仅下限 仅 Min>0 时按 >= 判定。
func TestP2A流通市值_仅下限(t *testing.T) {
	b := A流通市值{Min: 50}
	if b.Buy("sh600000", p2Ks(p2K(9.9, 10.0, 10.1, 9.8, 10.0, 1e8))) {
		t.Fatal("value=10 低于下限 50 不应触发")
	}
	if !b.Buy("sh600000", p2Ks(p2K(49.9, 50.0, 50.1, 49.9, 50.0, 1e8))) {
		t.Fatal("value=50 等于下限应触发")
	}
}

// TestP2A流通市值_仅上限 仅 Max>0 时按 <= 判定。
func TestP2A流通市值_仅上限(t *testing.T) {
	b := A流通市值{Max: 5}
	if b.Buy("sh600000", p2Ks(p2K(9.9, 10.0, 10.1, 9.8, 10.0, 1e8))) {
		t.Fatal("value=10 高于上限 5 不应触发")
	}
	if !b.Buy("sh600000", p2Ks(p2K(4.9, 5.0, 5.1, 4.9, 5.0, 1e8))) {
		t.Fatal("value=5 等于上限应触发")
	}
}

// TestP2A流通市值_缺股本按零值参与比较 缺股本 value=0 是有效值：过得了上限、过不了下限。
func TestP2A流通市值_缺股本按零值参与比较(t *testing.T) {
	ks := p2Ks(p2K(9.9, 10.0, 10.1, 9.8, 10.0, 0))
	if !(A流通市值{Max: 30}).Buy("sh600000", ks) {
		t.Fatal("value=0 <= 上限 30 应触发")
	}
	if (A流通市值{Min: 1}).Buy("sh600000", ks) {
		t.Fatal("value=0 < 下限 1 不应触发")
	}
}

// TestP2A流通市值_上限小于下限恒不触发 历史退化语义。
func TestP2A流通市值_上限小于下限恒不触发(t *testing.T) {
	b := A流通市值{Min: 50, Max: 20}
	if b.Buy("sh600000", p2Ks(p2K(9.9, 10.0, 10.1, 9.8, 10.0, 1e8))) {
		t.Fatal("Max<Min 时不应触发")
	}
}

// TestP2A流通市值_空K线不触发。
func TestP2A流通市值_空K线不触发(t *testing.T) {
	if (A流通市值{Min: 1}).Buy("sh600000", nil) {
		t.Fatal("空前缀不应触发")
	}
}

// ---- A涨停（buy_limit_up_hit）----
// 阈值：sh60/sz00 涨幅>=9.8%；sh68/sz30/bj92 涨幅>=19.8%；其余 false。
// 昨收口径：last=10000 厘时 close=10980 厘涨幅恰为 9.8（实测无 ulp 偏差）。

// TestP2A涨停_主板阈值相等触发 sh60 收盘恰为 9.8% 时应触发（含边界）。
func TestP2A涨停_主板阈值相等触发(t *testing.T) {
	if !(A涨停{}).Buy("sh600000", p2Ks(p2K(10.0, 10.9, 11.0, 10.8, 10.98, 1e8))) {
		t.Fatal("涨幅恰为 9.8% 应触发")
	}
	if (A涨停{}).Buy("sh600000", p2Ks(p2K(10.0, 10.9, 11.0, 10.8, 10.979, 1e8))) {
		t.Fatal("涨幅 9.79% 未达阈值不应触发")
	}
	if !(A涨停{}).Buy("sh600000", p2Ks(p2K(10.0, 10.9, 11.0, 10.8, 10.981, 1e8))) {
		t.Fatal("涨幅 9.81% 应触发")
	}
}

// TestP2A涨停_创业板科创板阈值 20cm 板按 19.8% 判定。
func TestP2A涨停_创业板科创板阈值(t *testing.T) {
	for _, code := range []string{"sh688001", "sz300750", "bj920001"} {
		if !(A涨停{}).Buy(code, p2Ks(p2K(100.0, 119.0, 120.0, 118.0, 119.8, 1e8))) {
			t.Fatalf("%s 涨幅恰为 19.8%% 应触发", code)
		}
		if (A涨停{}).Buy(code, p2Ks(p2K(100.0, 119.0, 120.0, 118.0, 119.799, 1e8))) {
			t.Fatalf("%s 涨幅 19.799%% 未达 19.8%% 阈值不应触发", code)
		}
	}
}

// TestP2A涨停_六位代码自动补前缀 6 位代码 AddPrefix 后按板块判定。
func TestP2A涨停_六位代码自动补前缀(t *testing.T) {
	if !(A涨停{}).Buy("600000", p2Ks(p2K(10.0, 10.9, 11.0, 10.8, 10.98, 1e8))) {
		t.Fatal("600000→sh600000 涨幅 9.8%% 应触发")
	}
	if !(A涨停{}).Buy("000001", p2Ks(p2K(10.0, 10.9, 11.0, 10.8, 10.98, 1e8))) {
		t.Fatal("000001→sz000001 涨幅 9.8%% 应触发")
	}
	if !(A涨停{}).Buy("300750", p2Ks(p2K(100.0, 119.0, 120.0, 118.0, 119.8, 1e8))) {
		t.Fatal("300750→sz300750 涨幅 19.8%% 应触发")
	}
}

// TestP2A涨停_非八位代码不触发 未识别代码与长度非 8 时一律 false。
func TestP2A涨停_非八位代码不触发(t *testing.T) {
	ks := p2Ks(p2K(10.0, 10.9, 11.0, 10.8, 10.98, 1e8))
	if (A涨停{}).Buy("6000000", ks) {
		t.Fatal("7 位代码不应触发")
	}
	if (A涨停{}).Buy("123456", ks) {
		t.Fatal("未识别的 6 位代码不应触发")
	}
	if (A涨停{}).Buy("", ks) {
		t.Fatal("空代码不应触发")
	}
}

// TestP2A涨停_昨收缺失退化为对开盘价 Last==0 时涨幅按 (Close-Open)/Open 计算。
func TestP2A涨停_昨收缺失退化为对开盘价(t *testing.T) {
	if !(A涨停{}).Buy("sh600000", p2Ks(p2K(0.0, 1.0, 1.1, 0.99, 1.098, 1e8))) {
		t.Fatal("昨收缺失时涨幅 9.8%% 应触发")
	}
	if (A涨停{}).Buy("sh600000", p2Ks(p2K(0.0, 1.0, 1.1, 0.99, 1.097, 1e8))) {
		t.Fatal("昨收缺失时涨幅 9.7%% 不应触发")
	}
}

// TestP2A涨停_空K线与平价行情 空前缀 false；平价行情涨幅 0 不触发。
func TestP2A涨停_空K线与平价行情(t *testing.T) {
	if (A涨停{}).Buy("sh600000", nil) {
		t.Fatal("空前缀不应触发")
	}
	if (A涨停{}).Buy("sh600000", p2Ks(p2K(1.0, 1.0, 1.0, 1.0, 1.0, 0))) {
		t.Fatal("平价行情（high==low、量 0）涨幅 0 不应触发")
	}
}

// ---- A过滤涨停（buy_filter_limit_up）----
// 与 A涨停 反向：sh60/sz00 涨幅<9.8% 通过；sh68/sz30/bj92 涨幅<19.8% 通过。

// TestP2A过滤涨停_主板阈值相等不通过 涨幅恰为 9.8% 时已涨停，过滤不通过。
func TestP2A过滤涨停_主板阈值相等不通过(t *testing.T) {
	if (A过滤涨停{}).Buy("sh600000", p2Ks(p2K(10.0, 10.9, 11.0, 10.8, 10.98, 1e8))) {
		t.Fatal("涨幅恰为 9.8%% 不应通过（已涨停）")
	}
	if !(A过滤涨停{}).Buy("sh600000", p2Ks(p2K(10.0, 10.9, 11.0, 10.8, 10.979, 1e8))) {
		t.Fatal("涨幅 9.79%% 未涨停应通过")
	}
	if (A过滤涨停{}).Buy("sh600000", p2Ks(p2K(10.0, 10.9, 11.0, 10.8, 10.981, 1e8))) {
		t.Fatal("涨幅 9.81%% 已达涨停阈值不应通过")
	}
}

// TestP2A过滤涨停_创业板科创板阈值 20cm 板按 19.8% 判定。
func TestP2A过滤涨停_创业板科创板阈值(t *testing.T) {
	for _, code := range []string{"sh688001", "sz300750", "bj920001"} {
		if (A过滤涨停{}).Buy(code, p2Ks(p2K(100.0, 119.0, 120.0, 118.0, 119.8, 1e8))) {
			t.Fatalf("%s 涨幅恰为 19.8%% 不应通过", code)
		}
		if !(A过滤涨停{}).Buy(code, p2Ks(p2K(100.0, 119.0, 120.0, 118.0, 119.799, 1e8))) {
			t.Fatalf("%s 涨幅 19.799%% 未达 19.8%% 阈值应通过", code)
		}
	}
}

// TestP2A过滤涨停_六位代码自动补前缀。
func TestP2A过滤涨停_六位代码自动补前缀(t *testing.T) {
	if !(A过滤涨停{}).Buy("600000", p2Ks(p2K(10.0, 10.9, 11.0, 10.8, 10.979, 1e8))) {
		t.Fatal("600000→sh600000 涨幅 9.79%% 应通过")
	}
	if !(A过滤涨停{}).Buy("300750", p2Ks(p2K(100.0, 119.0, 120.0, 118.0, 119.799, 1e8))) {
		t.Fatal("300750→sz300750 涨幅 19.799%% 应通过")
	}
}

// TestP2A过滤涨停_非八位代码不通过 未识别代码与长度非 8 时一律 false。
func TestP2A过滤涨停_非八位代码不通过(t *testing.T) {
	ks := p2Ks(p2K(10.0, 10.9, 11.0, 10.8, 10.979, 1e8))
	if (A过滤涨停{}).Buy("6000000", ks) {
		t.Fatal("7 位代码不应通过")
	}
	if (A过滤涨停{}).Buy("123456", ks) {
		t.Fatal("未识别的 6 位代码不应通过")
	}
	if (A过滤涨停{}).Buy("", ks) {
		t.Fatal("空代码不应通过")
	}
}

// TestP2A过滤涨停_空K线与平价行情 空前缀 false；平价行情涨幅 0 应通过。
func TestP2A过滤涨停_空K线与平价行情(t *testing.T) {
	if (A过滤涨停{}).Buy("sh600000", nil) {
		t.Fatal("空前缀不应通过")
	}
	if !(A过滤涨停{}).Buy("sh600000", p2Ks(p2K(1.0, 1.0, 1.0, 1.0, 1.0, 0))) {
		t.Fatal("平价行情涨幅 0 应通过")
	}
}
