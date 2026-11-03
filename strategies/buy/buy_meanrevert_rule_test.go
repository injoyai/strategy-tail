package buy

import (
	"testing"

	"github.com/injoyai/strategy-tail/lib/extend"
	"github.com/injoyai/tdx/v2/protocol"
)

// P5 组迁移行为快照（台账附录 A：buy_boll_lower / buy_bias_oversold /
// buy_zscore_oversold / buy_donchian_lower / buy_boll_lower_volume /
// buy_boll_lower_rsi / buy_day_gain_range）。断言先按旧实现固定，迁移到
// strategies/rule 内核后必须原样通过；helper 一律 p5 前缀，避免与并行
// 迁移组的同名符号冲突。
//
// 本批 7 个组件的目录默认参数与组件内零值归一结果相同（如布林 20/2、
// 单日涨幅范围 0/0=不限），零值参数用例同时覆盖目录默认。价格一律直接
// 给厘（protocol.Price），避免 Yuan 的浮点截断；恰等边界夹具的浮点值
// 已离线按旧实现的运算顺序逐一核算。

// p5K 构造单根 K 线：close 为收盘价（厘），last 为昨收（厘），volume 为
// 成交量（手）；open/high/low 与收盘同值（布林/唐奇安/量比/涨幅各组件
// 所需字段均由此确定，LLV 取 Low 即收盘序列）。
func p5K(last, close float64, volume int64) *extend.Kline {
	return &extend.Kline{Kline: &protocol.Kline{
		Last:   last,
		Open:   close,
		High:   close,
		Low:    close,
		Close:  close,
		Volume: volume,
	}}
}

// p5SeriesV 按收盘序列与逐日量组装 K 线前缀，last 自动取前一根收盘
// （首根为 0，单日涨幅组件的用例均至少 2 根，不触及 last=0 退化）。
func p5SeriesV(closes []float64, volumes []int64) extend.Klines {
	ks := make(extend.Klines, len(closes))
	for i, c := range closes {
		var last float64
		if i > 0 {
			last = closes[i-1]
		}
		ks[i] = p5K(last, c, volumes[i])
	}
	return ks
}

// p5Series 收盘序列 + 统一成交量。
func p5Series(closes []float64, volume int64) extend.Klines {
	volumes := make([]int64, len(closes))
	for i := range volumes {
		volumes[i] = volume
	}
	return p5SeriesV(closes, volumes)
}

// p5Closes 便捷构造 n 根 close 厘 + 末根 closeLast 厘的收盘序列。
func p5Closes(n int, close, closeLast float64) []float64 {
	out := make([]float64, n)
	for i := range out {
		out[i] = close
	}
	out[n-1] = closeLast
	return out
}

// ---- A布林下轨（buy_boll_lower）----
// 语义：今收(浮点元) <= 布林下轨 MA-std×σ；σ=0 时下轨 NaN → false；
// len<period → false。p=2 时 st<=1 才可能触发（st=1 时下轨恒等于末根前
// 一收盘，为精确恰等点）。

// TestP5布林下轨快照 固定 A布林下轨 旧行为。
func TestP5布林下轨快照(t *testing.T) {
	// 零值参数（0→20 / 0→2，同目录默认）：19 根 10 元 + 末根 2 元，
	// 下轨 ≈6.113，触发；20 根全 10 元 σ=0 → 下轨 NaN → 不触发。
	if !(A布林下轨{}).Buy("sh600000", p5Series(p5Closes(20, 10.0, 2.0), 100)) {
		t.Fatal("零值参数：末根 2 元低于下轨 6.113 应触发")
	}
	if (A布林下轨{}).Buy("sh600000", p5Series(p5Closes(20, 10.0, 10.0), 100)) {
		t.Fatal("零值参数：平价行情 σ=0 下轨 NaN 不应触发")
	}
	// 非默认值：p=2、st=1 时下轨恰等于末根收盘（恰等边界，含 <=）。
	if !(A布林下轨{Period: 2, StdTimes: 1}).Buy("sh600000", p5Series([]float64{10.0, 8.0}, 100)) {
		t.Fatal("p=2 st=1：收盘 8 元恰等于下轨 8 元应触发")
	}
	if (A布林下轨{Period: 2, StdTimes: 1.0000001}).Buy("sh600000", p5Series([]float64{10.0, 8.0}, 100)) {
		t.Fatal("p=2 st=1.0000001：收盘 8 元高于下轨 7.9999999 不应触发")
	}
	if !(A布林下轨{Period: 2, StdTimes: 0.9999}).Buy("sh600000", p5Series([]float64{10.0, 8.0}, 100)) {
		t.Fatal("p=2 st=0.9999：收盘 8 元低于下轨 8.0001 应触发")
	}
	// 预热差一根：p=20 需 20 根，19 根不触发；p=2 需 2 根，1 根不触发。
	if (A布林下轨{}).Buy("sh600000", p5Series(p5Closes(19, 10.0, 2.0), 100)) {
		t.Fatal("预热差一根（19 根算 20 日布林）不应触发")
	}
	if (A布林下轨{Period: 2, StdTimes: 1}).Buy("sh600000", p5Series([]float64{8.0}, 100)) {
		t.Fatal("预热差一根（1 根算 2 日布林）不应触发")
	}
	// 空行情。
	if (A布林下轨{}).Buy("sh600000", nil) {
		t.Fatal("空行情不应触发")
	}
}

// ---- A乖离超卖（buy_bias_oversold）----
// 语义：MA>0 且 乖离率 <= MinBias；0 会被覆盖为 -7（无法表达 0 阈值）；
// len<period → false；ma==0（含全零收盘）→ false。

// TestP5乖离超卖快照 固定 A乖离超卖 旧行为。
func TestP5乖离超卖快照(t *testing.T) {
	// 零值参数（0→20 / 0→-7）：末根 2 元对 MA20=9.6 乖离约 -79.2%，触发。
	if !(A乖离超卖{}).Buy("sh600000", p5Series(p5Closes(20, 10.0, 2.0), 100)) {
		t.Fatal("零值参数：乖离约 -79.2% 低于 -7% 应触发")
	}
	// 非默认值恰等：p=2、收盘 [6,2] 元时 MA=4、乖离恰为 -50%（新旧公式
	// 在该夹具均为精确 -50.0），阈值 -50 含边界触发；±0.1 张开方向。
	if !(A乖离超卖{Period: 2, MinBias: -50}).Buy("sh600000", p5Series([]float64{6.0, 2.0}, 100)) {
		t.Fatal("p=2：乖离恰为 -50% 等于阈值应触发")
	}
	if !(A乖离超卖{Period: 2, MinBias: -49.9}).Buy("sh600000", p5Series([]float64{6.0, 2.0}, 100)) {
		t.Fatal("p=2：乖离 -50% 低于阈值 -49.9% 应触发")
	}
	if (A乖离超卖{Period: 2, MinBias: -50.1}).Buy("sh600000", p5Series([]float64{6.0, 2.0}, 100)) {
		t.Fatal("p=2：乖离 -50% 高于阈值 -50.1% 不应触发")
	}
	if (A乖离超卖{Period: 2, MinBias: -50}).Buy("sh600000", p5Series([]float64{6.0, 3.4}, 100)) {
		t.Fatal("p=2：乖离约 -27.7% 未达 -50% 不应触发")
	}
	// ma==0：全零收盘时均线为 0，旧实现 ma<=0 提前返回 false。
	if (A乖离超卖{Period: 2, MinBias: -50}).Buy("sh600000", p5Series([]float64{0.0, 0.0}, 100)) {
		t.Fatal("全零收盘均线为 0 不应触发")
	}
	// 预热差一根与空行情。
	if (A乖离超卖{}).Buy("sh600000", p5Series(p5Closes(19, 10.0, 2.0), 100)) {
		t.Fatal("预热差一根（19 根算 20 日均线）不应触发")
	}
	if (A乖离超卖{}).Buy("sh600000", nil) {
		t.Fatal("空行情不应触发")
	}
}

// ---- AZScore超卖（buy_zscore_oversold）----
// 语义：Z = (今收-MA)/σ <= MinZ；σ=0 → NaN → false；0 会被覆盖为 -2。

// TestP5ZScore超卖快照 固定 AZScore超卖 旧行为。
func TestP5ZScore超卖快照(t *testing.T) {
	// 零值参数（0→20 / 0→-2）：末根 2 元的 Z 约 -4.359，触发；阈值 -5 时不触发。
	if !(AZScore超卖{}).Buy("sh600000", p5Series(p5Closes(20, 10.0, 2.0), 100)) {
		t.Fatal("零值参数：Z 约 -4.36 低于 -2 应触发")
	}
	if (AZScore超卖{MinZ: -5}).Buy("sh600000", p5Series(p5Closes(20, 10.0, 2.0), 100)) {
		t.Fatal("Z 约 -4.36 高于阈值 -5 不应触发")
	}
	// 恰等：p=2、收盘 [10,8] 时 Z 恰为 -1（精确），阈值 -1 含边界触发。
	if !(AZScore超卖{Period: 2, MinZ: -1}).Buy("sh600000", p5Series([]float64{10.0, 8.0}, 100)) {
		t.Fatal("p=2：Z 恰为 -1 等于阈值应触发")
	}
	if !(AZScore超卖{Period: 2, MinZ: -0.9999}).Buy("sh600000", p5Series([]float64{10.0, 8.0}, 100)) {
		t.Fatal("p=2：Z -1 低于阈值 -0.9999 应触发")
	}
	if (AZScore超卖{Period: 2, MinZ: -1.0001}).Buy("sh600000", p5Series([]float64{10.0, 8.0}, 100)) {
		t.Fatal("p=2：Z -1 高于阈值 -1.0001 不应触发")
	}
	// 平价行情 σ=0 → Z NaN → 不触发；预热差一根与空行情。
	if (AZScore超卖{}).Buy("sh600000", p5Series(p5Closes(20, 10.0, 10.0), 100)) {
		t.Fatal("平价行情 σ=0 不应触发")
	}
	if (AZScore超卖{}).Buy("sh600000", p5Series(p5Closes(19, 10.0, 2.0), 100)) {
		t.Fatal("预热差一根（19 根算 20 日窗口）不应触发")
	}
	if (AZScore超卖{}).Buy("sh600000", nil) {
		t.Fatal("空行情不应触发")
	}
}

// ---- A唐奇安下沿（buy_donchian_lower）----
// 语义：今收(整数厘) <= 前 period 日最低价（dks[:n-1]，不含今日，台账
// 风险 §5.5 买入侧口径）；len<period+1 → false。

// TestP5唐奇安下沿快照 固定 A唐奇安下沿 旧行为。
func TestP5唐奇安下沿快照(t *testing.T) {
	// 零值参数（0→20）：前 20 根均 10 元，末根 9.9 元触及下沿触发；
	// 10.1 元未触及不触发；恰为 10 元（整数厘恰等）触发。
	if !(A唐奇安下沿{}).Buy("sh600000", p5Series(p5Closes(21, 10.0, 9.9), 100)) {
		t.Fatal("零值参数：收盘 9.9 元低于前 20 日最低 10 元应触发")
	}
	if (A唐奇安下沿{}).Buy("sh600000", p5Series(p5Closes(21, 10.0, 10.1), 100)) {
		t.Fatal("零值参数：收盘 10.1 元未触及下沿不应触发")
	}
	if !(A唐奇安下沿{}).Buy("sh600000", p5Series(p5Closes(21, 10.0, 10.0), 100)) {
		t.Fatal("零值参数：收盘恰等于前 20 日最低应触发（含边界）")
	}
	// 非默认值 p=2：前 2 根 [10,8] 元最低 8 元，末根恰等/低于/高于。
	if !(A唐奇安下沿{Period: 2}).Buy("sh600000", p5Series([]float64{10.0, 8.0, 8.0}, 100)) {
		t.Fatal("p=2：收盘恰等于前 2 日最低 8 元应触发")
	}
	if !(A唐奇安下沿{Period: 2}).Buy("sh600000", p5Series([]float64{10.0, 8.0, 7.999}, 100)) {
		t.Fatal("p=2：收盘 7.999 元低于下沿应触发")
	}
	if (A唐奇安下沿{Period: 2}).Buy("sh600000", p5Series([]float64{10.0, 8.0, 8.001}, 100)) {
		t.Fatal("p=2：收盘 8.001 元高于下沿不应触发")
	}
	// 预热差一根：p=2 需 3 根，2 根不触发；空行情与单根不触发。
	if (A唐奇安下沿{Period: 2}).Buy("sh600000", p5Series([]float64{10.0, 8.0}, 100)) {
		t.Fatal("预热差一根（2 根算 p=2+1）不应触发")
	}
	if (A唐奇安下沿{Period: 2}).Buy("sh600000", p5Series([]float64{8.0}, 100)) {
		t.Fatal("单根行情不应触发")
	}
	if (A唐奇安下沿{}).Buy("sh600000", nil) {
		t.Fatal("空行情不应触发")
	}
}

// ---- A布林下轨缩量（buy_boll_lower_volume）----
// 语义：先下轨（今收 <= 下轨，失败即短路返回 false），再地量
// （今日量 <= 前VolDays日均量(不含今日)×VolRatio，含边界）；
// 基准均量 <=0（含全零量）→ false；len<p 或 len<VolDays+1 → false。

// TestP5布林下轨缩量快照 固定 A布林下轨缩量 旧行为。
func TestP5布林下轨缩量快照(t *testing.T) {
	// 零值参数（20/2/5/0.6）：下轨触发 + 前五日均量 100 手、今日 60 手
	// 恰为 0.6 倍（100×0.6 精确等于 60），含边界触发。
	if !(A布林下轨缩量{}).Buy("sh600000", p5SeriesV(p5Closes(20, 10.0, 2.0), append(makeVols(19, 100), 60))) {
		t.Fatal("零值参数：下轨触发且今日量恰为均量 0.6 倍应触发")
	}
	if (A布林下轨缩量{}).Buy("sh600000", p5SeriesV(p5Closes(20, 10.0, 2.0), append(makeVols(19, 100), 61))) {
		t.Fatal("今日量 61 手超过均量 0.6 倍（60 手）不应触发")
	}
	if !(A布林下轨缩量{}).Buy("sh600000", p5SeriesV(p5Closes(20, 10.0, 2.0), append(makeVols(19, 100), 59))) {
		t.Fatal("今日量 59 手低于地量上限应触发")
	}
	// 主条件短路：平价行情 σ=0 下轨 NaN，即便量再小也不触发。
	if (A布林下轨缩量{}).Buy("sh600000", p5SeriesV(p5Closes(20, 10.0, 10.0), append(makeVols(19, 100), 1))) {
		t.Fatal("下轨不成立（σ=0）时不应触发")
	}
	// 基准均量为 0（全零量）→ false。
	if (A布林下轨缩量{}).Buy("sh600000", p5SeriesV(p5Closes(20, 10.0, 2.0), makeVols(20, 0))) {
		t.Fatal("前日均量为 0 无可比基准不应触发")
	}
	// 第二条件预热差一根：len=25 满足 p=20 但不满足 vd=25 的 26 根。
	if (A布林下轨缩量{VolDays: 25}).Buy("sh600000", p5SeriesV(p5Closes(25, 10.0, 2.0), makeVols(25, 100))) {
		t.Fatal("量窗口差一根（25 根算 25+1）不应触发")
	}
	// 空行情。
	if (A布林下轨缩量{}).Buy("sh600000", nil) {
		t.Fatal("空行情不应触发")
	}
}

// makeVols 构造 n 根统一量的切片（p5 专用）。
func makeVols(n int, v int64) []int64 {
	out := make([]int64, n)
	for i := range out {
		out[i] = v
	}
	return out
}

// ---- A布林下轨RSI超卖（buy_boll_lower_rsi）----
// 语义：先下轨（失败短路），再浮点 RSI(RSIPeriod) <= RSIThreshold；
// len<p 或 len<rp+1 → false；RSI 缺数据 NaN → false。

// TestP5布林下轨RSI超卖快照 固定 A布林下轨RSI超卖 旧行为。
func TestP5布林下轨RSI超卖快照(t *testing.T) {
	// 零值参数（20/2/14/30）：下轨触发且 19 根横盘 + 末根暴跌的
	// RSI(14) 恰为 0（无上涨），触发。
	if !(A布林下轨RSI超卖{}).Buy("sh600000", p5Series(p5Closes(20, 10.0, 2.0), 100)) {
		t.Fatal("零值参数：下轨触发且 RSI=0 低于 30 应触发")
	}
	// RSI 恰等：[10,11,9] 元 p=3 st=1 下轨成立（9 <= 9.1835），rp=2 时
	// RSI 恰为 33.33333333333333，阈值取该精确值含边界触发。
	eq := []float64{10.0, 11.0, 9.0}
	if !(A布林下轨RSI超卖{Period: 3, StdTimes: 1, RSIPeriod: 2, RSIThreshold: 33.33333333333333}).Buy("sh600000", p5Series(eq, 100)) {
		t.Fatal("RSI 恰为 33.33333333333333 等于阈值应触发")
	}
	if (A布林下轨RSI超卖{Period: 3, StdTimes: 1, RSIPeriod: 2, RSIThreshold: 33.32}).Buy("sh600000", p5Series(eq, 100)) {
		t.Fatal("RSI 33.333 高于阈值 33.32 不应触发")
	}
	if !(A布林下轨RSI超卖{Period: 3, StdTimes: 1, RSIPeriod: 2, RSIThreshold: 33.34}).Buy("sh600000", p5Series(eq, 100)) {
		t.Fatal("RSI 33.333 低于阈值 33.34 应触发")
	}
	// 主条件短路：[10,11,10.5] 元 st=1 时收盘 10.5 高于下轨 10.09，
	// 即便 RSI 66.7 <= 70 也不触发。
	if (A布林下轨RSI超卖{Period: 3, StdTimes: 1, RSIPeriod: 2, RSIThreshold: 70}).Buy("sh600000", p5Series([]float64{10.0, 11.0, 10.5}, 100)) {
		t.Fatal("下轨不成立时不应触发")
	}
	// 平价行情 σ=0 下轨 NaN → 不触发；预热差一根（2 根）与空行情。
	if (A布林下轨RSI超卖{}).Buy("sh600000", p5Series(p5Closes(20, 10.0, 10.0), 100)) {
		t.Fatal("平价行情 σ=0 不应触发")
	}
	if (A布林下轨RSI超卖{}).Buy("sh600000", p5Series(p5Closes(19, 10.0, 2.0), 100)) {
		t.Fatal("预热差一根（19 根算 20 日布林）不应触发")
	}
	if (A布林下轨RSI超卖{}).Buy("sh600000", nil) {
		t.Fatal("空行情不应触发")
	}
}

// ---- A单日涨幅范围（buy_day_gain_range）----
// 语义：当日涨幅（(收-昨收)/昨收×100）∈ [Min,Max]（含边界）；
// Min/Max 为 0 表示该侧不限（负下限生效）；两侧均不限时有行情即通过；
// Max<Min 恒 false。涨幅恰等边界夹具均取精确值（5.0/-5.0）。

// TestP5单日涨幅范围快照 固定 A单日涨幅范围 旧行为。
func TestP5单日涨幅范围快照(t *testing.T) {
	// 零值参数（0/0=不限，同目录默认）：有行情即通过，空行情不通过。
	if !(A单日涨幅范围{}).Buy("sh600000", p5Series([]float64{10.0, 10.5}, 100)) {
		t.Fatal("零值参数：有行情应触发")
	}
	if (A单日涨幅范围{}).Buy("sh600000", nil) {
		t.Fatal("零值参数：空行情不应触发")
	}
	// 涨幅恰为 +5%（10000→10500 厘，精确 5.0）：上下界恰等均含边界。
	if !(A单日涨幅范围{Min: 5}).Buy("sh600000", p5Series([]float64{10.0, 10.5}, 100)) {
		t.Fatal("涨幅 5% 恰等于下限 5 应触发")
	}
	if (A单日涨幅范围{Min: 5.000001}).Buy("sh600000", p5Series([]float64{10.0, 10.5}, 100)) {
		t.Fatal("涨幅 5% 低于下限 5.000001 不应触发")
	}
	if !(A单日涨幅范围{Max: 5}).Buy("sh600000", p5Series([]float64{10.0, 10.5}, 100)) {
		t.Fatal("涨幅 5% 恰等于上限 5 应触发")
	}
	if (A单日涨幅范围{Max: 4.999999}).Buy("sh600000", p5Series([]float64{10.0, 10.5}, 100)) {
		t.Fatal("涨幅 5% 高于上限 4.999999 不应触发")
	}
	if !(A单日涨幅范围{Min: 5, Max: 5}).Buy("sh600000", p5Series([]float64{10.0, 10.5}, 100)) {
		t.Fatal("涨幅 5% 落在 [5,5] 内应触发")
	}
	if (A单日涨幅范围{Min: 6, Max: 7}).Buy("sh600000", p5Series([]float64{10.0, 10.5}, 100)) {
		t.Fatal("涨幅 5% 不在 [6,7] 内不应触发")
	}
	// 负下限生效：涨幅恰为 -5%（10000→9500 厘，精确 -5.0）。
	if !(A单日涨幅范围{Min: -5}).Buy("sh600000", p5Series([]float64{10.0, 9.5}, 100)) {
		t.Fatal("涨幅 -5% 恰等于负下限 -5 应触发")
	}
	if (A单日涨幅范围{Min: -4.9}).Buy("sh600000", p5Series([]float64{10.0, 9.5}, 100)) {
		t.Fatal("涨幅 -5% 低于负下限 -4.9 不应触发")
	}
	if !(A单日涨幅范围{Max: 0}).Buy("sh600000", p5Series([]float64{10.0, 9.5}, 100)) {
		t.Fatal("涨幅 -5% 低于上限 0 应触发")
	}
	// Max<Min 历史退化：区间不可能满足，恒不触发。
	if (A单日涨幅范围{Min: 5, Max: -5}).Buy("sh600000", p5Series([]float64{10.0, 10.5}, 100)) {
		t.Fatal("Max<Min 时不应触发")
	}
	// 单边哨兵与结构性无界等价：±1e9 的有限涨幅判定与 0=不限一致。
	sentinel := p5Series([]float64{10.0, 10.5}, 100)
	if !(A单日涨幅范围{Min: -1e9}).Buy("sh600000", sentinel) {
		t.Fatal("Min=-1e9 等价不限下限应触发")
	}
	if !(A单日涨幅范围{Max: 1e9}).Buy("sh600000", sentinel) {
		t.Fatal("Max=1e9 等价不限上限应触发")
	}
	if !(A单日涨幅范围{Min: -1e9, Max: 1e9}).Buy("sh600000", sentinel) {
		t.Fatal("±1e9 双哨兵与双侧不限等价应触发")
	}
	// 带界限时空行情不触发。
	if (A单日涨幅范围{Min: -5, Max: 7}).Buy("sh600000", nil) {
		t.Fatal("带界限空行情不应触发")
	}
}
