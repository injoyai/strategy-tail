package sell

import (
	"testing"

	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/lib/extend"
	f "github.com/injoyai/strategy-tail/strategies/factor"
	"github.com/injoyai/tdx/protocol"
)

// T3 时序批次 C 组（卖出侧）行为快照（台账附录 B：sell_macd_reversal /
// sell_macd_dead_cross_negative / sell_break_ma / sell_below_short_ma /
// sell_ma_dead_cross / sell_long_ma_down）。
// 断言先按旧实现固定，迁移到 strategies/rule 内核后必须原样通过；
// helper 一律 t3c 前缀，避免与并行迁移组的同名符号冲突。
// MACD 族另带逐前缀对拍：以旧算法内联为 oracle，对随机与整形行情的每个
// 前缀长度断言门面与 oracle 一致，覆盖窗口左端钳 0、minDiff 派生边界、
// 交叉恰等与预热（卖侧 MACD反转 无 slow+signal 预检，与买侧不同）。

// t3cK 构造单根 K 线：last 为昨收（厘），o/h/l/c 为开高低收（厘），
// volume 为成交量（手）。
func t3cK(last, o, h, l, c protocol.Price, volume int64) *extend.Kline {
	return &extend.Kline{Kline: &protocol.Kline{
		Last:   last,
		Open:   o,
		High:   h,
		Low:    l,
		Close:  c,
		Volume: volume,
	}}
}

// t3cSeries 按收盘序列组装 K 线前缀：开高低收与收盘同值，last 自动取
// 前一根收盘（首根为 0），量恒定。
func t3cSeries(closes []protocol.Price, volume int64) extend.Klines {
	ks := make(extend.Klines, len(closes))
	for i, c := range closes {
		var last protocol.Price
		if i > 0 {
			last = closes[i-1]
		}
		ks[i] = t3cK(last, c, c, c, c, volume)
	}
	return ks
}

// t3cCloses n 根 close 厘 + 末根 closeLast 厘的收盘序列。
func t3cCloses(n int, close, closeLast protocol.Price) []protocol.Price {
	out := make([]protocol.Price, n)
	for i := range out {
		out[i] = close
	}
	out[n-1] = closeLast
	return out
}

// t3cWalk 随机游走收盘序列（厘），固定种子可复现。
func t3cWalk(seed int64, n int) []protocol.Price {
	step := make([]int, n)
	rng := int64(seed)
	for i := 1; i < n; i++ {
		rng = rng*6364136223846793005 + 1442695040888963407
		step[i] = int(uint64(rng)>>33)%201 - 100
	}
	out := make([]protocol.Price, n)
	out[0] = 10000
	for i := 1; i < n; i++ {
		next := out[i-1] + protocol.Price(step[i])
		if next <= 0 {
			next = out[i-1] + 100
		}
		out[i] = next
	}
	return out
}

// t3cWave 多段折线收盘：segs 依次为各段天数，段间涨跌方向交替，步长固定
// step 厘/天——上行后下行再反弹再回落，保证 MACD 柱出现多个局部峰谷与
// 均线死叉/金叉交替。
func t3cWave(base protocol.Price, step int, lens ...int) []protocol.Price {
	out := make([]protocol.Price, 0, len(lens))
	c := base
	sign := 1
	for _, l := range lens {
		for i := 0; i < l; i++ {
			c += protocol.Price(sign * step)
			if c < 100 {
				c = 100
			}
			out = append(out, c)
		}
		sign = -sign
	}
	return out
}

// ---- sell_macd_reversal（MACD反转）----

// t3cMACD反转Oracle 迁移前 MACD反转.Sell 内联（逐语句等价）。
func t3cMACD反转Oracle(s MACD反转, dks extend.Klines) bool {
	fast, slow, signal, lookback := s.Fast, s.Slow, s.Signal, s.Lookback
	if fast == 0 {
		fast = 12
	}
	if slow == 0 {
		slow = 26
	}
	if signal == 0 {
		signal = 9
	}
	if lookback == 0 {
		lookback = 20
	}
	hist := (f.MACD柱值{Fast: fast, Slow: slow, Signal: signal}).Series(dks)
	if len(hist) != len(dks) {
		return false
	}
	n := len(hist)
	if n < 2 {
		return false
	}
	yesterday, today := hist[n-2], hist[n-1]
	if !(today < yesterday-s.MinDiff) {
		return false
	}
	windowStart := n - 1 - lookback
	if windowStart < 0 {
		windowStart = 0
	}
	maxV := hist[windowStart]
	for j := windowStart + 1; j <= n-2; j++ {
		if hist[j] > maxV {
			maxV = hist[j]
		}
	}
	return yesterday == maxV
}

// TestT3CMACD反转快照 固定 MACD反转 旧行为。
func TestT3CMACD反转快照(t *testing.T) {
	// 零值参数（0→12/26/9/20，minDiff 0=不限，同目录默认）：平价行情柱恒 0，
	// 今柱==昨柱恰等 0 不触发；单根行情 n<2 不触发。
	if (MACD反转{}).Sell("sh600000", t3cSeries(t3cCloses(40, 10000, 10000), 100), core.Buy{}) {
		t.Fatal("平价行情 MACD 柱恰等 0 不应触发")
	}
	if (MACD反转{}).Sell("sh600000", t3cSeries(t3cCloses(1, 10000, 10000), 100), core.Buy{}) {
		t.Fatal("单根行情不应触发")
	}
	// 逐前缀对拍：覆盖窗口左端钳 0（lookback 超过前缀长度）、minDiff 派生
	// 边界（0/正/负）、以及卖侧无 slow+signal 预检（n=2..slow+signal-1 仍
	// 按比较判定，预热口径与买侧 MACD反转 不同）。
	cases := []MACD反转{
		{},
		{Fast: 3, Slow: 6, Signal: 2, Lookback: 5},
		{Fast: 1, Slow: 2, Signal: 2, Lookback: 3, MinDiff: 0.5},
		{Fast: 3, Slow: 6, Signal: 2, Lookback: 100},
		{Fast: 12, Slow: 26, Signal: 9, Lookback: 20, MinDiff: -1},
	}
	fixtures := [][]protocol.Price{
		t3cWalk(1, 60),
		t3cWalk(2, 60),
		t3cWave(10000, 150, 10, 20, 25),
		t3cCloses(40, 10000, 10000),
	}
	triggered := false
	for _, s := range cases {
		for _, closes := range fixtures {
			dks := t3cSeries(closes, 100)
			for n := 0; n <= len(dks); n++ {
				want := t3cMACD反转Oracle(s, dks[:n])
				if got := s.Sell("sh600000", dks[:n], core.Buy{}); got != want {
					t.Fatalf("MACD反转 %+v 前缀 %d 根: got %v want %v", s, n, got, want)
				}
				if want {
					triggered = true
				}
			}
		}
	}
	if !triggered {
		t.Fatal("对拍夹具未覆盖任何触发样例，oracle 可能失效")
	}
}

// ---- sell_macd_dead_cross_negative（A_MACD死叉且DIF为负）----

// t3cMACD死叉DIF为负Oracle 迁移前 A_MACD死叉且DIF为负.Sell 内联。
func t3cMACD死叉DIF为负Oracle(s A_MACD死叉且DIF为负, dks extend.Klines) bool {
	fast, slow, signal := s.Fast, s.Slow, s.Signal
	if fast == 0 {
		fast = 12
	}
	if slow == 0 {
		slow = 26
	}
	if signal == 0 {
		signal = 9
	}
	n := len(dks)
	if n < slow+signal {
		return false
	}
	hist := (f.MACD柱值{Fast: fast, Slow: slow, Signal: signal}).Series(dks)
	if len(hist) != n {
		return false
	}
	if !(hist[n-2] > 0 && hist[n-1] <= 0) {
		return false
	}
	dif := (f.MACDDIF值{Fast: fast, Slow: slow}).Value("", dks)
	return dif < 0
}

// TestT3CMACD死叉DIF为负快照 固定 A_MACD死叉且DIF为负 旧行为。
func TestT3CMACD死叉DIF为负快照(t *testing.T) {
	// 预热拦截：零值参数（12/26/9）门槛 slow+signal=35 根，34 根差一根
	// 不触发；非默认 1/2/2 门槛 4 根，3 根差一根不触发。
	if (A_MACD死叉且DIF为负{}).Sell("sh600000", t3cSeries(t3cWalk(3, 34), 100), core.Buy{}) {
		t.Fatal("预热差一根（34 根算 35）不应触发")
	}
	if (A_MACD死叉且DIF为负{Fast: 1, Slow: 2, Signal: 2}).Sell("sh600000", t3cSeries(t3cWalk(4, 3), 100), core.Buy{}) {
		t.Fatal("预热差一根（3 根算 4）不应触发")
	}
	// 平价行情：柱恒 0，昨柱恰等 0 不构成下穿；空行情不触发。
	if (A_MACD死叉且DIF为负{}).Sell("sh600000", t3cSeries(t3cCloses(40, 10000, 10000), 100), core.Buy{}) {
		t.Fatal("平价行情昨柱恰等 0 不应触发")
	}
	if (A_MACD死叉且DIF为负{}).Sell("sh600000", nil, core.Buy{}) {
		t.Fatal("空行情不应触发")
	}
	// 逐前缀对拍：上涨→深跌→反弹→再跌行情保证出现"柱由正转负且 DIF<0"
	// 的触发样例，也覆盖"柱下穿但 DIF 仍为正"的不触发侧。
	cases := []A_MACD死叉且DIF为负{
		{},
		{Fast: 1, Slow: 2, Signal: 2},
		{Fast: 3, Slow: 6, Signal: 2},
	}
	fixtures := [][]protocol.Price{
		t3cWave(10000, 200, 10, 15, 20, 8, 10),
		t3cWalk(5, 60),
		t3cWalk(6, 60),
		t3cCloses(45, 10000, 10000),
	}
	triggered := false
	for _, s := range cases {
		for _, closes := range fixtures {
			dks := t3cSeries(closes, 100)
			for n := 0; n <= len(dks); n++ {
				want := t3cMACD死叉DIF为负Oracle(s, dks[:n])
				if got := s.Sell("sh600000", dks[:n], core.Buy{}); got != want {
					t.Fatalf("MACD死叉且DIF为负 %+v 前缀 %d 根: got %v want %v", s, n, got, want)
				}
				if want {
					triggered = true
				}
			}
		}
	}
	if !triggered {
		t.Fatal("对拍夹具未覆盖任何触发样例，oracle 可能失效")
	}
}

// ---- sell_break_ma（A收盘跌破均线）----

// TestT3C收盘跌破均线快照 固定 A收盘跌破均线 旧行为。
func TestT3C收盘跌破均线快照(t *testing.T) {
	// 非默认 p=2/days=2，n=4 恰好门槛：末两日收盘均<=当日均线，其中前一日
	// 收盘恰等于均线（含相等口径），连续天数恰等触发。
	if !(A收盘跌破均线{Period: 2, Days: 2}).Sell("sh600000", t3cSeries([]protocol.Price{1000, 1000, 1000, 900}, 100), core.Buy{}) {
		t.Fatal("p=2/days=2：连续 2 日收盘<=均线（含恰等）应触发")
	}
	// 中途收回不触发：第 3 日收盘 1100 > 均线 1050 断开连续段。
	if (A收盘跌破均线{Period: 2, Days: 2}).Sell("sh600000", t3cSeries([]protocol.Price{1000, 1000, 1100, 900}, 100), core.Buy{}) {
		t.Fatal("p=2/days=2：连续段中断不应触发")
	}
	// 恰等单日不足以构成连续：末日恰等计 1 天 < 2 天。
	if (A收盘跌破均线{Period: 2, Days: 2}).Sell("sh600000", t3cSeries([]protocol.Price{1000, 1050, 1050}, 100), core.Buy{}) {
		t.Fatal("p=2/days=2：恰等仅 1 天不应触发")
	}
	// days=1 非默认：单日跌破即触发。
	if !(A收盘跌破均线{Period: 3, Days: 1}).Sell("sh600000", t3cSeries([]protocol.Price{1000, 1100, 1200, 900}, 100), core.Buy{}) {
		t.Fatal("p=3/days=1：单日跌破应触发")
	}
	// 预热差一根：n=period+days-1=3，即使连续段已够 2 天也不触发。
	if (A收盘跌破均线{Period: 2, Days: 2}).Sell("sh600000", t3cSeries([]protocol.Price{1000, 1000, 900}, 100), core.Buy{}) {
		t.Fatal("预热差一根（3 根算 4）不应触发")
	}
	// 零值参数（0→20/3，同目录默认）：23 根横盘收盘恰等于均线，含相等
	// 连续 3 天触发；22 根差一根不触发。
	if !(A收盘跌破均线{}).Sell("sh600000", t3cSeries(t3cCloses(23, 1000, 1000), 100), core.Buy{}) {
		t.Fatal("零值参数：23 根横盘收盘恰等于均线应触发（含相等）")
	}
	if (A收盘跌破均线{}).Sell("sh600000", t3cSeries(t3cCloses(22, 1000, 1000), 100), core.Buy{}) {
		t.Fatal("零值参数：预热差一根（22 根算 23）不应触发")
	}
	// 全零收盘：收盘 0 <= 均线 0 连续成立，锁定旧零值触发语义。
	if !(A收盘跌破均线{Period: 2, Days: 2}).Sell("sh600000", t3cSeries([]protocol.Price{0, 0, 0, 0}, 100), core.Buy{}) {
		t.Fatal("全零收盘收盘<=均线恒成立应触发")
	}
	// 空行情。
	if (A收盘跌破均线{}).Sell("sh600000", nil, core.Buy{}) {
		t.Fatal("空行情不应触发")
	}
}

// ---- sell_below_short_ma（A跌破短期均线）----

// TestT3C跌破短期均线快照 固定 A跌破短期均线 旧行为。
func TestT3C跌破短期均线快照(t *testing.T) {
	// 零值参数（0→10，同目录默认）：9 根 10 元 + 末根 9.9 元 → 收盘 9.9 <
	// 均线 9.99 触发；10 根横盘收盘恰等于均线（严格小于）不触发。
	if !(A跌破短期均线{}).Sell("sh600000", t3cSeries(t3cCloses(10, 1000, 990), 100), core.Buy{}) {
		t.Fatal("零值参数：收盘 9.9 元低于均线 9.99 元应触发")
	}
	if (A跌破短期均线{}).Sell("sh600000", t3cSeries(t3cCloses(10, 1000, 1000), 100), core.Buy{}) {
		t.Fatal("零值参数：收盘恰等于均线不应触发（严格小于）")
	}
	// 非默认 p=2：[10,8] 均线 9 元，收盘 8 元触发；恰等/高于均线不触发。
	if !(A跌破短期均线{Period: 2}).Sell("sh600000", t3cSeries([]protocol.Price{1000, 800}, 100), core.Buy{}) {
		t.Fatal("p=2：收盘 8 元低于均线 9 元应触发")
	}
	if (A跌破短期均线{Period: 2}).Sell("sh600000", t3cSeries([]protocol.Price{1000, 1000}, 100), core.Buy{}) {
		t.Fatal("p=2：收盘恰等于均线不应触发")
	}
	if (A跌破短期均线{Period: 2}).Sell("sh600000", t3cSeries([]protocol.Price{800, 1000}, 100), core.Buy{}) {
		t.Fatal("p=2：收盘高于均线不应触发")
	}
	// p=1 非默认恰等：1 日均线即当日收盘自身，收盘恒等于均线，任何行情
	// 都不触发（旧实现同语义）；999 与 998 均不触发。
	if (A跌破短期均线{Period: 1}).Sell("sh600000", t3cSeries([]protocol.Price{1000, 999}, 100), core.Buy{}) {
		t.Fatal("p=1：收盘恰等于自身均线不应触发")
	}
	if (A跌破短期均线{Period: 1}).Sell("sh600000", t3cSeries([]protocol.Price{1000, 998}, 100), core.Buy{}) {
		t.Fatal("p=1：1 日均线即收盘自身不应触发")
	}
	// 预热差一根与空行情、全零收盘（无 MA>0 守卫，0<0 不触发）。
	if (A跌破短期均线{Period: 2}).Sell("sh600000", t3cSeries([]protocol.Price{1000}, 100), core.Buy{}) {
		t.Fatal("预热差一根（1 根算 2 日均线）不应触发")
	}
	if (A跌破短期均线{}).Sell("sh600000", t3cSeries(t3cCloses(9, 1000, 990), 100), core.Buy{}) {
		t.Fatal("预热差一根（9 根算 10 日均线）不应触发")
	}
	if (A跌破短期均线{}).Sell("sh600000", nil, core.Buy{}) {
		t.Fatal("空行情不应触发")
	}
	if (A跌破短期均线{Period: 2}).Sell("sh600000", t3cSeries([]protocol.Price{0, 0}, 100), core.Buy{}) {
		t.Fatal("全零收盘 0<0 不应触发")
	}
}

// ---- sell_ma_dead_cross（A均线死叉）----

// TestT3C均线死叉快照 固定 A均线死叉 旧行为。
func TestT3C均线死叉快照(t *testing.T) {
	// 非默认 short=2/long=3：昨侧恰等（昨短==昨长=10 元）且今短<今长触发。
	if !(A均线死叉{Short: 2, Long: 3}).Sell("sh600000", t3cSeries([]protocol.Price{1000, 1000, 1000, 900}, 100), core.Buy{}) {
		t.Fatal("s=2/l=3：昨短恰等昨长且今短<今长应触发（昨侧恰等口径）")
	}
	// 上涨中不触发：今短 12.5 > 今长 12。
	if (A均线死叉{Short: 2, Long: 3}).Sell("sh600000", t3cSeries([]protocol.Price{1000, 1100, 1200, 1300}, 100), core.Buy{}) {
		t.Fatal("s=2/l=3：上涨中不应触发")
	}
	// 已死叉多日不触发：昨短 11.5 < 昨长 12，昨侧 Ge 不成立。
	if (A均线死叉{Short: 2, Long: 3}).Sell("sh600000", t3cSeries([]protocol.Price{1300, 1200, 1100, 1000}, 100), core.Buy{}) {
		t.Fatal("s=2/l=3：昨日已死叉不应触发")
	}
	// 今侧恰等不触发：横盘今短==今长（今侧严格小于）。
	if (A均线死叉{Short: 2, Long: 3}).Sell("sh600000", t3cSeries(t3cCloses(4, 1000, 1000), 100), core.Buy{}) {
		t.Fatal("s=2/l=3：今短恰等今长不应触发")
	}
	// 预热：n=long=3 与 n=2 均不触发；空行情不触发。
	if (A均线死叉{Short: 2, Long: 3}).Sell("sh600000", t3cSeries([]protocol.Price{1000, 1000, 900}, 100), core.Buy{}) {
		t.Fatal("s=2/l=3：预热差一根（3 根算 long+1=4）不应触发")
	}
	if (A均线死叉{Short: 2, Long: 3}).Sell("sh600000", t3cSeries([]protocol.Price{1000, 900}, 100), core.Buy{}) {
		t.Fatal("s=2/l=3：不足 long 根不应触发")
	}
	if (A均线死叉{Short: 2, Long: 3}).Sell("sh600000", nil, core.Buy{}) {
		t.Fatal("空行情不应触发")
	}
	// 零值参数（0→5/20，同目录默认）：n=21 恰好门槛，昨日横盘短长均线
	// 恰等（10 元），末根跌至 8 元使今短 9.6 < 今长 9.9 触发；20 根差一根
	// 不触发；21 根横盘今侧恰等不触发。
	if !(A均线死叉{}).Sell("sh600000", t3cSeries(t3cCloses(21, 1000, 800), 100), core.Buy{}) {
		t.Fatal("零值参数：n=21 恰好且昨短恰等昨长应触发")
	}
	if (A均线死叉{}).Sell("sh600000", t3cSeries(t3cCloses(20, 1000, 900), 100), core.Buy{}) {
		t.Fatal("零值参数：预热差一根（20 根算 long+1=21）不应触发")
	}
	if (A均线死叉{}).Sell("sh600000", t3cSeries(t3cCloses(21, 1000, 1000), 100), core.Buy{}) {
		t.Fatal("零值参数：横盘今短恰等今长不应触发")
	}
}

// ---- sell_long_ma_down（LongMADown）----

// TestT3C长期均线转弱快照 固定 LongMADown 旧行为。
func TestT3C长期均线转弱快照(t *testing.T) {
	// 零值 lookback（0→5，同目录默认）预热：255 根恰好门槛；254 根差一根
	// 不触发。
	flat255 := t3cSeries(t3cCloses(255, 1000, 1000), 100)
	if (LongMADown{}).Sell("sh600000", flat255[:254], core.Buy{}) {
		t.Fatal("预热差一根（254 根算 250+5）不应触发")
	}
	// 恰好门槛：全走平 255 根，最弱日增量为 0（不>0），两路取反恒真触发
	//（"走平即转弱"）。
	if !(LongMADown{}).Sell("sh600000", flat255, core.Buy{}) {
		t.Fatal("零值参数：255 根走平增量 0 应触发")
	}
	// 单边上行：250/60 日最弱日增量均为正，两路均线均向上不触发。
	up := make([]protocol.Price, 255)
	for i := range up {
		up[i] = protocol.Price(1000 + i)
	}
	if (LongMADown{}).Sell("sh600000", t3cSeries(up, 100), core.Buy{}) {
		t.Fatal("单边上行 250/60 日均线均向上不应触发")
	}
	// 60 日侧转弱即触发：前 190 日上行后 65 日走平——250 日增量仍为正，
	// 60 日增量恰为 0，Or  single侧成立触发。
	mix := make([]protocol.Price, 255)
	for i := 0; i < 190; i++ {
		mix[i] = protocol.Price(1000 + 100*i)
	}
	for i := 190; i < 255; i++ {
		mix[i] = 19900
	}
	if !(LongMADown{}).Sell("sh600000", t3cSeries(mix, 100), core.Buy{}) {
		t.Fatal("60 日侧走平（250 日仍向上）应触发")
	}
	// 非默认 lookback=2：门槛 252 根，251 根差一根不触发、252 根走平触发。
	flat252 := t3cSeries(t3cCloses(252, 1000, 1000), 100)
	if (LongMADown{Lookback: 2}).Sell("sh600000", flat252[:251], core.Buy{}) {
		t.Fatal("预热差一根（251 根算 250+2）不应触发")
	}
	if !(LongMADown{Lookback: 2}).Sell("sh600000", flat252, core.Buy{}) {
		t.Fatal("非默认 lookback=2：252 根走平应触发")
	}
	// 空行情。
	if (LongMADown{}).Sell("sh600000", nil, core.Buy{}) {
		t.Fatal("空行情不应触发")
	}
	// 目录外退化分支：负 lookback 时旧 maUp 恒 false，取反两路恒真，
	// 仅余预热拦截（250+lookback），迁移按门面分支保留。
	if !(LongMADown{Lookback: -3}).Sell("sh600000", t3cSeries(t3cCloses(250, 1000, 1000), 100), core.Buy{}) {
		t.Fatal("负 lookback：250 根满足 250-3 门槛应触发（旧行为）")
	}
	if (LongMADown{Lookback: -3}).Sell("sh600000", t3cSeries(t3cCloses(246, 1000, 1000), 100), core.Buy{}) {
		t.Fatal("负 lookback：246 根不足 247 门槛不应触发")
	}
}
