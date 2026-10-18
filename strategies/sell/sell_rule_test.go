package sell

import (
	"testing"

	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/lib/extend"
	"github.com/injoyai/tdx/protocol"
)

// P7 组迁移行为快照（台账附录 A：sell_back_to_ma / sell_rsi_overbought /
// sell_rsi_recover / sell_back_to_boll_mid / sell_bias_zero / sell_zscore_zero /
// sell_back_to_donchian_mid / sell_big_fall / sell_volume_big_bear）。
// 断言先按旧实现固定，迁移到 strategies/rule 内核后必须原样通过；
// helper 一律 p7 前缀，避免与并行迁移组的同名符号冲突。
//
// 本批 9 个组件的目录默认参数与组件内零值归一结果相同（均线/布林/唐奇安
// 20、RSI 14、超买 75、恢复 50、跌幅 7、放量大阴线 1.5/2.0），零值参数
// 用例同时覆盖目录默认。价格一律直接给厘（protocol.Price），避免 Yuan 的
// 浮点截断；恰等边界夹具均取旧实现运算下可精确表示的值（均线为整数厘
// 整除、跌幅取 2 的幂次分母、量阈取整数），避免浮点末位翻转。

// p7K 构造单根 K 线：last 为昨收（厘），o/h/l/c 为开高低收（厘），
// volume 为成交量（手）。
func p7K(last, o, h, l, c protocol.Price, volume int64) *extend.Kline {
	return &extend.Kline{Kline: &protocol.Kline{
		Last:   last,
		Open:   o,
		High:   h,
		Low:    l,
		Close:  c,
		Volume: volume,
	}}
}

// p7Series 按收盘序列组装 K 线前缀：开高低收与收盘同值，last 自动取
// 前一根收盘（首根为 0），量恒定。
func p7Series(closes []protocol.Price, volume int64) extend.Klines {
	ks := make(extend.Klines, len(closes))
	for i, c := range closes {
		var last protocol.Price
		if i > 0 {
			last = closes[i-1]
		}
		ks[i] = p7K(last, c, c, c, c, volume)
	}
	return ks
}

// p7Closes n 根 close 厘 + 末根 closeLast 厘的收盘序列。
func p7Closes(n int, close, closeLast protocol.Price) []protocol.Price {
	out := make([]protocol.Price, n)
	for i := range out {
		out[i] = close
	}
	out[n-1] = closeLast
	return out
}

// ---- A回到均线（sell_back_to_ma）----
// 语义：MA>0 且 今收 >= MA(p)；len<p → false；全零收盘均线为 0 → false。

// TestP7回归均线快照 固定 A回到均线 旧行为。
func TestP7回归均线快照(t *testing.T) {
	// 零值参数（0→20，同目录默认）：20 根全 10 元，收盘恰等于均线触发
	//（含边界）；9.9 元低于均线不触发。
	if !(A回到均线{}).Sell("sh600000", p7Series(p7Closes(20, 10000, 10000), 100), core.Buy{}) {
		t.Fatal("零值参数：收盘恰等于 20 日均线应触发")
	}
	if (A回到均线{}).Sell("sh600000", p7Series(p7Closes(20, 10000, 9900), 100), core.Buy{}) {
		t.Fatal("零值参数：收盘 9.9 元低于均线 9.995 元不应触发")
	}
	// 非默认值 p=2：[8,10] 元均线 9 元，收盘 10 元触发；[10,8.001] 元
	// 均线 9 元（18001/2 整除），收盘 8.001 元不触发。
	if !(A回到均线{Period: 2}).Sell("sh600000", p7Series([]protocol.Price{8000, 10000}, 100), core.Buy{}) {
		t.Fatal("p=2：收盘 10 元高于均线 9 元应触发")
	}
	if (A回到均线{Period: 2}).Sell("sh600000", p7Series([]protocol.Price{10000, 8001}, 100), core.Buy{}) {
		t.Fatal("p=2：收盘 8.001 元低于均线 9 元不应触发")
	}
	// 全零收盘：均线为 0，旧实现 ma<=0 提前返回 false。
	if (A回到均线{Period: 2}).Sell("sh600000", p7Series([]protocol.Price{0, 0}, 100), core.Buy{}) {
		t.Fatal("全零收盘均线为 0 不应触发")
	}
	// 预热差一根与空行情。
	if (A回到均线{Period: 2}).Sell("sh600000", p7Series([]protocol.Price{8000}, 100), core.Buy{}) {
		t.Fatal("预热差一根（1 根算 2 日均线）不应触发")
	}
	if (A回到均线{}).Sell("sh600000", p7Series(p7Closes(19, 10000, 10100), 100), core.Buy{}) {
		t.Fatal("预热差一根（19 根算 20 日均线）不应触发")
	}
	if (A回到均线{}).Sell("sh600000", nil, core.Buy{}) {
		t.Fatal("空行情不应触发")
	}
}

// ---- A_RSI超买（sell_rsi_overbought）----
// 语义：浮点 RSI（util.CalcRSI 口径）> 阈值（严格，恰等不触发）；
// len<period+1 → false；横盘后单边行情 RSI=100/0。

// TestP7RSI超买快照 固定 A_RSI超买 旧行为。
func TestP7RSI超买快照(t *testing.T) {
	// 零值参数（14/75，同目录默认）：19 根横盘 + 末根上涨，窗口内仅
	// 上涨 → RSI=100 > 75 触发；单边下跌 RSI=0 不触发。
	if !(A_RSI超买{}).Sell("sh600000", p7Series(p7Closes(20, 10000, 10100), 100), core.Buy{}) {
		t.Fatal("零值参数：单边上涨 RSI=100 应触发")
	}
	if (A_RSI超买{}).Sell("sh600000", p7Series(p7Closes(20, 10000, 9900), 100), core.Buy{}) {
		t.Fatal("零值参数：单边下跌 RSI=0 不应触发")
	}
	// 非默认值恰等：p=2、[10,11,9] 元 → avgGain=500、avgLoss=1000，
	// RSI 恰为 33.33333333333333，严格大于恰等不触发；阈值低于/高于
	// 该值时方向正确。
	eq := []protocol.Price{10000, 11000, 9000}
	if (A_RSI超买{Period: 2, Threshold: 33.33333333333333}).Sell("sh600000", p7Series(eq, 100), core.Buy{}) {
		t.Fatal("RSI 恰为 33.33333333333333 等于阈值不应触发（严格大于）")
	}
	if !(A_RSI超买{Period: 2, Threshold: 33.32}).Sell("sh600000", p7Series(eq, 100), core.Buy{}) {
		t.Fatal("RSI 33.333 高于阈值 33.32 应触发")
	}
	if (A_RSI超买{Period: 2, Threshold: 33.34}).Sell("sh600000", p7Series(eq, 100), core.Buy{}) {
		t.Fatal("RSI 33.333 低于阈值 33.34 不应触发")
	}
	// 预热差一根：p=2 需 3 根，2 根不触发；零值参数需 15 根，14 根不触发。
	if (A_RSI超买{Period: 2, Threshold: 10}).Sell("sh600000", p7Series([]protocol.Price{10000, 11000}, 100), core.Buy{}) {
		t.Fatal("预热差一根（2 根算 p=2+1）不应触发")
	}
	if (A_RSI超买{}).Sell("sh600000", p7Series(p7Closes(14, 10000, 10100), 100), core.Buy{}) {
		t.Fatal("预热差一根（14 根算 14+1）不应触发")
	}
	// 空行情。
	if (A_RSI超买{}).Sell("sh600000", nil, core.Buy{}) {
		t.Fatal("空行情不应触发")
	}
}

// ---- ARSI恢复（sell_rsi_recover）----
// 语义：整数 RSI（extend 0-100 截断口径）>= 阈值（含恰等边界）；
// len<p+1 → false。与浮点 RSI 不同源（台账风险 §5.4）。

// TestP7RSI恢复快照 固定 ARSI恢复 旧行为。
func TestP7RSI恢复快照(t *testing.T) {
	// 非默认值恰等：p=2、[10,11,9] 元 → 100×1000/3000 整数截断 = 33，
	// 阈值 33 恰等触发（>= 含边界）；33.5 不触发；32 触发。同夹具浮点
	// RSI 为 33.333…，整数口径截断为 33（两口径不同源，不可混用）。
	eq := []protocol.Price{10000, 11000, 9000}
	if !(ARSI恢复{Period: 2, Threshold: 33}).Sell("sh600000", p7Series(eq, 100), core.Buy{}) {
		t.Fatal("整数 RSI=33 恰等于阈值 33 应触发（含边界）")
	}
	if (ARSI恢复{Period: 2, Threshold: 33.5}).Sell("sh600000", p7Series(eq, 100), core.Buy{}) {
		t.Fatal("整数 RSI 33 低于阈值 33.5 不应触发")
	}
	if !(ARSI恢复{Period: 2, Threshold: 32}).Sell("sh600000", p7Series(eq, 100), core.Buy{}) {
		t.Fatal("整数 RSI 33 高于阈值 32 应触发")
	}
	// 零值参数（14/50，同目录默认）：17 根横盘、第 18 根跌 1 元、末根
	// 反弹 2 元 → 窗口内 gain=2000、loss=1000 → 整数 RSI=66 >= 50 触发。
	recoverFix := append(p7Closes(18, 10000, 9000), 11000)
	if !(ARSI恢复{}).Sell("sh600000", p7Series(recoverFix, 100), core.Buy{}) {
		t.Fatal("零值参数：整数 RSI=66 回升至 50 之上应触发")
	}
	if (ARSI恢复{Threshold: 67}).Sell("sh600000", p7Series(recoverFix, 100), core.Buy{}) {
		t.Fatal("整数 RSI 66 低于阈值 67 不应触发")
	}
	// 预热差一根：p=2 需 3 根，2 根不触发；零值参数需 15 根，14 根不触发。
	if (ARSI恢复{Period: 2, Threshold: 1}).Sell("sh600000", p7Series([]protocol.Price{10000, 11000}, 100), core.Buy{}) {
		t.Fatal("预热差一根（2 根算 p=2+1）不应触发")
	}
	if (ARSI恢复{}).Sell("sh600000", p7Series(p7Closes(14, 10000, 11000), 100), core.Buy{}) {
		t.Fatal("预热差一根（14 根算 14+1）不应触发")
	}
	// 空行情。
	if (ARSI恢复{}).Sell("sh600000", nil, core.Buy{}) {
		t.Fatal("空行情不应触发")
	}
}

// ---- A回到布林中轨（sell_back_to_boll_mid）----
// 语义：MA>0 且 今收 >= 中轨（该周期均线）；len<p → false。

// TestP7回归布林中轨快照 固定 A回到布林中轨 旧行为。
func TestP7回归布林中轨快照(t *testing.T) {
	// 零值参数（0→20，同目录默认）：20 根全 10 元，收盘恰等于中轨触发；
	// 9.9 元低于中轨不触发。
	if !(A回到布林中轨{}).Sell("sh600000", p7Series(p7Closes(20, 10000, 10000), 100), core.Buy{}) {
		t.Fatal("零值参数：收盘恰等于布林中轨应触发")
	}
	if (A回到布林中轨{}).Sell("sh600000", p7Series(p7Closes(20, 10000, 9900), 100), core.Buy{}) {
		t.Fatal("零值参数：收盘低于中轨不应触发")
	}
	// 非默认值 p=3：[10,8,10] 元中轨 9333（28000/3 整除），收盘 10 元
	// 触发；[10,8,6] 元中轨 8 元，收盘 6 元不触发。
	if !(A回到布林中轨{Period: 3}).Sell("sh600000", p7Series([]protocol.Price{10000, 8000, 10000}, 100), core.Buy{}) {
		t.Fatal("p=3：收盘 10 元高于中轨 9.333 元应触发")
	}
	if (A回到布林中轨{Period: 3}).Sell("sh600000", p7Series([]protocol.Price{10000, 8000, 6000}, 100), core.Buy{}) {
		t.Fatal("p=3：收盘 6 元低于中轨 8 元不应触发")
	}
	// 全零收盘：中轨为 0，旧实现 ma<=0 提前返回 false。
	if (A回到布林中轨{Period: 3}).Sell("sh600000", p7Series([]protocol.Price{0, 0, 0}, 100), core.Buy{}) {
		t.Fatal("全零收盘中轨为 0 不应触发")
	}
	// 预热差一根与空行情。
	if (A回到布林中轨{Period: 3}).Sell("sh600000", p7Series([]protocol.Price{10000, 8000}, 100), core.Buy{}) {
		t.Fatal("预热差一根（2 根算 3 日中轨）不应触发")
	}
	if (A回到布林中轨{}).Sell("sh600000", p7Series(p7Closes(19, 10000, 10100), 100), core.Buy{}) {
		t.Fatal("预热差一根（19 根算 20 日中轨）不应触发")
	}
	if (A回到布林中轨{}).Sell("sh600000", nil, core.Buy{}) {
		t.Fatal("空行情不应触发")
	}
}

// ---- A乖离归零（sell_bias_zero）----
// 语义：MA>0 且 (今收-MA)/MA×100 >= 0；len<p → false；乖离恰等 0 触发。

// TestP7乖离归零快照 固定 A乖离归零 旧行为。
func TestP7乖离归零快照(t *testing.T) {
	// 零值参数（0→20，同目录默认）：19 根 10 元 + 末根 10 元 → 乖离恰为
	// 0 触发；末根 10.1 元正乖离触发；末根 9.9 元负乖离不触发。
	if !(A乖离归零{}).Sell("sh600000", p7Series(p7Closes(20, 10000, 10000), 100), core.Buy{}) {
		t.Fatal("零值参数：乖离恰为 0 应触发（含边界）")
	}
	if !(A乖离归零{}).Sell("sh600000", p7Series(p7Closes(20, 10000, 10100), 100), core.Buy{}) {
		t.Fatal("零值参数：正乖离应触发")
	}
	if (A乖离归零{}).Sell("sh600000", p7Series(p7Closes(20, 10000, 9900), 100), core.Buy{}) {
		t.Fatal("零值参数：负乖离不应触发")
	}
	// 非默认值 p=2：[6,6] 元乖离恰为 0 触发；[6,3.4] 元乖离约 -27.7% 不触发。
	if !(A乖离归零{Period: 2}).Sell("sh600000", p7Series([]protocol.Price{6000, 6000}, 100), core.Buy{}) {
		t.Fatal("p=2：乖离恰为 0 应触发")
	}
	if (A乖离归零{Period: 2}).Sell("sh600000", p7Series([]protocol.Price{6000, 3400}, 100), core.Buy{}) {
		t.Fatal("p=2：乖离约 -27.7% 不应触发")
	}
	// 全零收盘：均线为 0，旧实现 ma<=0 提前返回 false。
	if (A乖离归零{Period: 2}).Sell("sh600000", p7Series([]protocol.Price{0, 0}, 100), core.Buy{}) {
		t.Fatal("全零收盘均线为 0 不应触发")
	}
	// 预热差一根与空行情。
	if (A乖离归零{Period: 2}).Sell("sh600000", p7Series([]protocol.Price{6000}, 100), core.Buy{}) {
		t.Fatal("预热差一根（1 根算 2 日均线）不应触发")
	}
	if (A乖离归零{}).Sell("sh600000", p7Series(p7Closes(19, 10000, 10100), 100), core.Buy{}) {
		t.Fatal("预热差一根（19 根算 20 日均线）不应触发")
	}
	if (A乖离归零{}).Sell("sh600000", nil, core.Buy{}) {
		t.Fatal("空行情不应触发")
	}
}

// ---- AZScore归零（sell_zscore_zero）----
// 语义：Z=(今收-均值)/σ >= 0；无均线守卫；σ=0 → NaN → false；len<p → false。

// TestP7ZScore归零快照 固定 AZScore归零 旧行为。
func TestP7ZScore归零快照(t *testing.T) {
	// 零值参数（0→20，同目录默认）：19 根 10 元 + 末根 10.1 元 → Z>0
	// 触发；末根 9.9 元 → Z<0 不触发。
	if !(AZScore归零{}).Sell("sh600000", p7Series(p7Closes(20, 10000, 10100), 100), core.Buy{}) {
		t.Fatal("零值参数：Z 为正应触发")
	}
	if (AZScore归零{}).Sell("sh600000", p7Series(p7Closes(20, 10000, 9900), 100), core.Buy{}) {
		t.Fatal("零值参数：Z 为负不应触发")
	}
	// 平价行情：σ=0 → Z NaN → 不触发。
	if (AZScore归零{}).Sell("sh600000", p7Series(p7Closes(20, 10000, 10000), 100), core.Buy{}) {
		t.Fatal("平价行情 σ=0 不应触发")
	}
	// 非默认值恰等：p=3、[8,10,9] 元均值恰为 9 元且 σ>0，Z 恰为 0 触发；
	// 末根 8.999 元低于均值 → Z<0 不触发。
	if !(AZScore归零{Period: 3}).Sell("sh600000", p7Series([]protocol.Price{8000, 10000, 9000}, 100), core.Buy{}) {
		t.Fatal("p=3：Z 恰为 0 应触发（含边界）")
	}
	if (AZScore归零{Period: 3}).Sell("sh600000", p7Series([]protocol.Price{8000, 10000, 8999}, 100), core.Buy{}) {
		t.Fatal("p=3：Z 为负不应触发")
	}
	// 预热差一根与空行情。
	if (AZScore归零{Period: 3}).Sell("sh600000", p7Series([]protocol.Price{8000, 10000}, 100), core.Buy{}) {
		t.Fatal("预热差一根（2 根算 3 日窗口）不应触发")
	}
	if (AZScore归零{}).Sell("sh600000", p7Series(p7Closes(19, 10000, 10100), 100), core.Buy{}) {
		t.Fatal("预热差一根（19 根算 20 日窗口）不应触发")
	}
	if (AZScore归零{}).Sell("sh600000", nil, core.Buy{}) {
		t.Fatal("空行情不应触发")
	}
}

// ---- A回到唐奇安中轨（sell_back_to_donchian_mid）----
// 语义：mid=(HHV(p)+LLV(p))/2，窗口含今日（High/Low 口径，台账风险 §5.5
// 卖出口径）；mid<=0 → false；今收 >= mid（含恰等边界）。

// TestP7回归唐奇安中轨快照 固定 A回到唐奇安中轨 旧行为。
func TestP7回归唐奇安中轨快照(t *testing.T) {
	// 零值参数（0→20，同目录默认）：前 19 根 OHLC 10 元 + 末根开 10 元、
	// 高 10 元、低 9.9 元、收 9.95 元 → HHV=10、LLV=9.9、mid=9.95，
	// 收盘恰等于中轨触发（含边界）。
	base19 := p7Series(p7Closes(19, 10000, 10000), 100)
	if !(A回到唐奇安中轨{}).Sell("sh600000", append(base19, p7K(10000, 10000, 10000, 9900, 9950, 100)), core.Buy{}) {
		t.Fatal("零值参数：收盘恰等于唐奇安中轨应触发")
	}
	// 末根整根 9.8 元 → LLV=9.8、mid=9.9，收盘 9.8 元低于中轨不触发。
	base19b := p7Series(p7Closes(19, 10000, 10000), 100)
	if (A回到唐奇安中轨{}).Sell("sh600000", append(base19b, p7K(10000, 9800, 9800, 9800, 9800, 100)), core.Buy{}) {
		t.Fatal("零值参数：收盘 9.8 元低于中轨 9.9 元不应触发")
	}
	// 窗口含今日：末根最高 12 元计入 HHV → mid=(12+10)/2=11 元，收盘
	// 10.5 元不触发；若不含今日 mid=10 元反而会触发，该断言锁定含今日口径。
	base19c := p7Series(p7Closes(19, 10000, 10000), 100)
	if (A回到唐奇安中轨{}).Sell("sh600000", append(base19c, p7K(10000, 10000, 12000, 10000, 10500, 100)), core.Buy{}) {
		t.Fatal("末根最高价应计入窗口（含今日），mid=11 元不应触发")
	}
	// 非默认值 p=3：[10,8,9] 元 OHLC 同收 → HHV=10、LLV=8、mid=9，
	// 收盘恰等触发；8.999 元不触发；9.001 元触发。
	if !(A回到唐奇安中轨{Period: 3}).Sell("sh600000", p7Series([]protocol.Price{10000, 8000, 9000}, 100), core.Buy{}) {
		t.Fatal("p=3：收盘恰等于中轨 9 元应触发")
	}
	if (A回到唐奇安中轨{Period: 3}).Sell("sh600000", p7Series([]protocol.Price{10000, 8000, 8999}, 100), core.Buy{}) {
		t.Fatal("p=3：收盘 8.999 元低于中轨不应触发")
	}
	if !(A回到唐奇安中轨{Period: 3}).Sell("sh600000", p7Series([]protocol.Price{10000, 8000, 9001}, 100), core.Buy{}) {
		t.Fatal("p=3：收盘 9.001 元高于中轨应触发")
	}
	// 全零行情：HHV=LLV=0 → mid=0，旧实现 mid<=0 提前返回 false。
	if (A回到唐奇安中轨{Period: 3}).Sell("sh600000", p7Series([]protocol.Price{0, 0, 0}, 100), core.Buy{}) {
		t.Fatal("全零行情中轨为 0 不应触发")
	}
	// 预热差一根与空行情。
	if (A回到唐奇安中轨{Period: 3}).Sell("sh600000", p7Series([]protocol.Price{10000, 8000}, 100), core.Buy{}) {
		t.Fatal("预热差一根（2 根算 3 日窗口）不应触发")
	}
	if (A回到唐奇安中轨{}).Sell("sh600000", p7Series(p7Closes(19, 10000, 10100), 100), core.Buy{}) {
		t.Fatal("预热差一根（19 根算 20 日窗口）不应触发")
	}
	if (A回到唐奇安中轨{}).Sell("sh600000", nil, core.Buy{}) {
		t.Fatal("空行情不应触发")
	}
}

// ---- A单日跌幅大于（sell_big_fall）----
// 语义：当日涨幅（RiseRate 口径）< -max（严格，恰等不触发）；len==0 →
// false；Last=0 退化对开盘价；NaN（Last=0 且 Open=0）→ false。

// TestP7单日跌幅快照 固定 A单日跌幅大于 旧行为。
func TestP7单日跌幅快照(t *testing.T) {
	// 零值参数（0→7，同目录默认）：10→8 元跌幅 -12.5% 触发；10→9.6 元
	// 跌幅 -5% 不触发。
	if !(A单日跌幅大于{}).Sell("sh600000", p7Series([]protocol.Price{8000, 7000}, 100), core.Buy{}) {
		t.Fatal("零值参数：跌幅 -12.5% 超过 7% 应触发")
	}
	if (A单日跌幅大于{}).Sell("sh600000", p7Series([]protocol.Price{8000, 7600}, 100), core.Buy{}) {
		t.Fatal("零值参数：跌幅 -5% 未达 7% 不应触发")
	}
	// 非默认值恰等：max=12.5，跌幅恰为 -12.5%（-1000/8000 二进制精确）
	// 严格小于不触发；-12.5125% 触发；-12.4875% 不触发。
	if (A单日跌幅大于{Max: 12.5}).Sell("sh600000", p7Series([]protocol.Price{8000, 7000}, 100), core.Buy{}) {
		t.Fatal("跌幅恰为 -12.5% 等于阈值不应触发（严格小于）")
	}
	if !(A单日跌幅大于{Max: 12.5}).Sell("sh600000", p7Series([]protocol.Price{8000, 6999}, 100), core.Buy{}) {
		t.Fatal("跌幅 -12.5125% 低于阈值 -12.5 应触发")
	}
	if (A单日跌幅大于{Max: 12.5}).Sell("sh600000", p7Series([]protocol.Price{8000, 7001}, 100), core.Buy{}) {
		t.Fatal("跌幅 -12.4875% 高于阈值不应触发")
	}
	// 上涨日不触发。
	if (A单日跌幅大于{}).Sell("sh600000", p7Series([]protocol.Price{8000, 8500}, 100), core.Buy{}) {
		t.Fatal("上涨日不应触发")
	}
	// 单根行情 Last=0 退化对开盘价：开 10 元收 9 元 → 跌幅 -10% 触发。
	if !(A单日跌幅大于{}).Sell("sh600000", extend.Klines{p7K(0, 10000, 10000, 9000, 9000, 100)}, core.Buy{}) {
		t.Fatal("单根行情 Last=0 退化按开盘价计跌幅 -10% 应触发")
	}
	// 畸形数据：Last=0 且 Open=0 → 涨幅 NaN → 不触发。
	if (A单日跌幅大于{}).Sell("sh600000", extend.Klines{p7K(0, 0, 0, 0, 0, 100)}, core.Buy{}) {
		t.Fatal("畸形数据涨幅 NaN 不应触发")
	}
	// 空行情。
	if (A单日跌幅大于{}).Sell("sh600000", nil, core.Buy{}) {
		t.Fatal("空行情不应触发")
	}
}

// ---- A放量大阴线（sell_volume_big_bear）----
// 语义：与旧实现同序短路：今收<今开（厘整数）→ 涨幅 <= -fallPct（含恰等）
// → 今日量 > 前5日均量(不含今日)×倍数（严格）；基准均量 <=0 → false；
// n<6 → false。

// TestP7放量大阴线快照 固定 A放量大阴线 旧行为。
func TestP7放量大阴线快照(t *testing.T) {
	// 5 根横盘 8 元（量 100 手）+ 今日阴线：开 8 元收 7 元（跌幅 -12.5%）。
	// 零值参数（1.5/2.0，同目录默认）：均量 100×1.5=150，今日 151 手
	// 严格大于触发；恰等 150 手不触发；149 手不触发。
	bearBar := func(close protocol.Price, volume int64) *extend.Kline {
		return p7K(8000, 8000, 8000, close, close, volume)
	}
	flat5 := p7Series([]protocol.Price{8000, 8000, 8000, 8000, 8000}, 100)
	if !(A放量大阴线{}).Sell("sh600000", append(flat5, bearBar(7000, 151)), core.Buy{}) {
		t.Fatal("零值参数：阴线跌幅 -12.5% 且量 151>150 应触发")
	}
	flat5b := p7Series([]protocol.Price{8000, 8000, 8000, 8000, 8000}, 100)
	if (A放量大阴线{}).Sell("sh600000", append(flat5b, bearBar(7000, 150)), core.Buy{}) {
		t.Fatal("今日量恰等于均量 1.5 倍（150 手）不应触发（严格大于）")
	}
	flat5c := p7Series([]protocol.Price{8000, 8000, 8000, 8000, 8000}, 100)
	if (A放量大阴线{}).Sell("sh600000", append(flat5c, bearBar(7000, 149)), core.Buy{}) {
		t.Fatal("今日量 149 手低于量阈不应触发")
	}
	// 跌幅恰等：非默认 fallPct=12.5，跌幅恰为 -12.5% 含边界继续量比判定
	// 触发；跌幅 -12.4875%（收 7.001 元）高于阈值不触发。
	flat5d := p7Series([]protocol.Price{8000, 8000, 8000, 8000, 8000}, 100)
	if !(A放量大阴线{FallPct: 12.5}).Sell("sh600000", append(flat5d, bearBar(7000, 151)), core.Buy{}) {
		t.Fatal("跌幅恰为 -12.5% 等于阈值应继续触发（含边界）")
	}
	flat5e := p7Series([]protocol.Price{8000, 8000, 8000, 8000, 8000}, 100)
	if (A放量大阴线{FallPct: 12.5}).Sell("sh600000", append(flat5e, bearBar(7001, 151)), core.Buy{}) {
		t.Fatal("跌幅 -12.4875% 未达 -12.5% 不应触发")
	}
	// 阴线判定边界：open==close 平盘不触发（即便跌幅与量比均满足）。
	flat5f := p7Series([]protocol.Price{8000, 8000, 8000, 8000, 8000}, 100)
	if (A放量大阴线{}).Sell("sh600000", append(flat5f, p7K(8000, 7000, 8000, 7000, 7000, 151)), core.Buy{}) {
		t.Fatal("open==close 平盘不是阴线不应触发")
	}
	// 基准均量为 0（前 5 根全零量）→ false。
	zeroVol5 := p7Series([]protocol.Price{8000, 8000, 8000, 8000, 8000}, 0)
	if (A放量大阴线{}).Sell("sh600000", append(zeroVol5, bearBar(7000, 151)), core.Buy{}) {
		t.Fatal("前日均量为 0 无可比基准不应触发")
	}
	// 预热差一根：共 5 根（量窗口需 6 根）→ false。
	flat4 := p7Series([]protocol.Price{8000, 8000, 8000, 8000}, 100)
	if (A放量大阴线{}).Sell("sh600000", append(flat4, bearBar(7000, 151)), core.Buy{}) {
		t.Fatal("预热差一根（5 根算 5+1）不应触发")
	}
	// 空行情。
	if (A放量大阴线{}).Sell("sh600000", nil, core.Buy{}) {
		t.Fatal("空行情不应触发")
	}
}
