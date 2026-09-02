package buy

import (
	"testing"
	"time"

	"github.com/injoyai/strategy-tail/lib/extend"
	"github.com/injoyai/strategy-tail/strategies/util"
	"github.com/injoyai/tdx/protocol"
)

// makeKlinesFromCloses 用收盘价序列构造 Klines。
func makeKlinesFromCloses(closes []float64) extend.Klines {
	n := len(closes)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.Local)
	ks := make(extend.Klines, n)
	for i := 0; i < n; i++ {
		p := closes[i]
		ks[i] = &extend.Kline{
			Unix: base.AddDate(0, 0, i).Unix(),
			Kline: &protocol.Kline{
				Time:   base.AddDate(0, 0, i),
				Open:   protocol.Yuan(p),
				Close:  protocol.Yuan(p),
				High:   protocol.Yuan(p),
				Low:    protocol.Yuan(p),
				Volume: 100,
			},
		}
	}
	return ks
}

// linearDeclineThenUp 构造"长期线性下跌 + 末段回升"序列。
// 线性下跌段 MACD 量柱会持续回升（DIF 趋稳、DEA 追赶），
// 因此末段会形成一段较长的连涨。
func linearDeclineThenUp(declineDays, riseDays int) extend.Klines {
	n := declineDays + riseDays
	closes := make([]float64, n)
	for i := 0; i < n; i++ {
		if i < declineDays {
			closes[i] = 100 - float64(i)*0.5
		} else {
			base := 100 - float64(declineDays-1)*0.5
			closes[i] = base + float64(i-declineDays+1)*1.0
		}
	}
	return makeKlinesFromCloses(closes)
}

// accelDeclineThenUp 构造"加速下跌 + 末段回升1天"序列。
// 加速下跌段 MACD 量柱持续走低，末段回升使量柱拐头向上，
// 因此末段连涨步数较短（预期为 1）。
func accelDeclineThenUp(declineDays int) extend.Klines {
	n := declineDays + 1
	closes := make([]float64, n)
	for i := 0; i < declineDays; i++ {
		closes[i] = 200 - 0.05*float64(i)*float64(i)
	}
	closes[declineDays] = closes[declineDays-1] + 5
	return makeKlinesFromCloses(closes)
}

// countRisingSteps 按用户语义统计末段连涨步数：
// 从今天往前，每遇到"当天量柱 > 前一天"算 1 步，直到断开。
func countRisingSteps(hist []float64) int {
	n := len(hist)
	if n < 2 {
		return 0
	}
	end := n - 1
	if hist[end] <= hist[end-1] {
		return 0
	}
	steps := 0
	for i := end; i > 0 && hist[i] > hist[i-1]; i-- {
		steps++
	}
	return steps
}

// smoothDeclineThenUp 构造"长期线性下跌 + 末段线性回升"序列。
// 线性下跌和线性回升段产生的 MACD 量柱变化平滑，方向反转少。
func smoothDeclineThenUp(declineDays, riseDays int) extend.Klines {
	n := declineDays + riseDays
	closes := make([]float64, n)
	for i := 0; i < n; i++ {
		if i < declineDays {
			closes[i] = 100 - float64(i)*0.5
		} else {
			base := 100 - float64(declineDays-1)*0.5
			closes[i] = base + float64(i-declineDays+1)*1.0
		}
	}
	return makeKlinesFromCloses(closes)
}

// zigzagCloses 构造"锯齿形"收盘价序列，使 MACD 量柱忽上忽下。
// 交替出现涨-跌-涨-跌，产生大量方向反转。
func zigzagCloses(days int) extend.Klines {
	closes := make([]float64, days)
	closes[0] = 100
	for i := 1; i < days; i++ {
		if i%2 == 1 {
			closes[i] = closes[i-1] + 3 // 涨
		} else {
			closes[i] = closes[i-1] - 2.5 // 跌
		}
	}
	return makeKlinesFromCloses(closes)
}

// sameSideZigzag 构造"同侧锯齿"序列：价格小幅震荡上行，
// 使平滑后 MACD 量柱停留在正数侧并反复上下波动，
// 从而在同一个正数段内产生多次方向反转（拐头）。
func sameSideZigzag(days int) extend.Klines {
	closes := make([]float64, days)
	closes[0] = 100
	for i := 1; i < days; i++ {
		if i%4 == 1 {
			closes[i] = closes[i-1] + 1.5
		} else if i%4 == 2 {
			closes[i] = closes[i-1] - 1.0
		} else if i%4 == 3 {
			closes[i] = closes[i-1] + 1.5
		} else {
			closes[i] = closes[i-1] - 1.0
		}
	}
	return makeKlinesFromCloses(closes)
}

// TestMACD顺滑 验证 MACD 量柱曲线光滑度判断（按同号段统计拐头）：
// 1) 线性下跌+回升序列，量柱方向一致，应通过；
// 2) 同侧锯齿形序列，同一个正数段内多次拐头，应被拒绝；
// 3) 交替穿越零轴的锯齿，因零轴穿越不计拐头，应通过；
// 4) MaxReversals 参数控制段内拐头容忍度。
func TestMACD顺滑(t *testing.T) {
	// 1) 线性下跌后回升：量柱走势平滑，方向反转少
	ks := smoothDeclineThenUp(50, 15)
	s := MACD顺滑{Smooth: 5, Days: 10, MaxReversals: 1}
	if !s.Buy("sz000001", ks) {
		t.Fatal("线性下跌+回升序列，量柱曲线应足够光滑（段内反转≤1），却未通过")
	}

	// 严格单调（MaxReversals=0）也应通过，因为 EMA 平滑后方向一致
	s0 := MACD顺滑{Smooth: 5, Days: 10, MaxReversals: 0}
	if !s0.Buy("sz000001", ks) {
		t.Fatal("线性序列 EMA 平滑后应严格单调（段内反转=0），却未通过")
	}

	// 2) 同侧锯齿形序列：量柱停留在正数侧反复上下波动，
	//    同一个正数段内拐头多次（>1），应被拒绝
	ks2 := sameSideZigzag(80)
	s1 := MACD顺滑{Smooth: 5, Days: 10, MaxReversals: 1}
	if s1.Buy("sz000001", ks2) {
		t.Fatal("同侧锯齿形序列，正数段内多次拐头，反转应>1，却通过了")
	}

	// 3) 交替穿越零轴的锯齿：每个同号段只含 1 天，段内无拐头，
	//    零轴穿越不计入拐头，应通过（MaxReversals=1 甚至 0 均通过）
	ks3 := zigzagCloses(80)
	sZ1 := MACD顺滑{Smooth: 5, Days: 10, MaxReversals: 1}
	if !sZ1.Buy("sz000001", ks3) {
		t.Fatal("交替穿越零轴的锯齿，每个同号段内无拐头，应通过，却未通过")
	}
	sZ0 := MACD顺滑{Smooth: 5, Days: 10, MaxReversals: 0}
	if !sZ0.Buy("sz000001", ks3) {
		t.Fatal("交替穿越零轴的锯齿，零轴穿越不计拐头，MaxReversals=0 也应通过，却未通过")
	}

	// 4) 验证 SmoothedMACDHistogram 与原始 MACDHistogram 的关系
	smoothed := util.SmoothedMACDHistogram(ks, 12, 26, 9, 5)
	raw := util.MACDHistogram(ks, 12, 26, 9)
	if len(smoothed) != len(raw) {
		t.Fatalf("平滑序列长度 %d != 原始序列长度 %d", len(smoothed), len(raw))
	}
	// 平滑后的末尾值应与原始值不同（EMA 平滑改变了值）
	if smoothed[len(smoothed)-1] == raw[len(raw)-1] {
		t.Fatal("EMA 平滑后末尾值应与原始值不同")
	}
}

// TestMACD连涨StreakIsRisingSteps 验证 MinDays/MaxDays 表示"上涨步数"
// （今天量柱 > 昨天量柱 为 1 步），而非柱子根数。
//
// 当前实现用 streakEnd - streakStart + 1 计数，多算了 1 根基础柱，
// 导致 {MinDays:N, MaxDays:N} 在恰好连涨 N 步时被错误拒绝。
func TestMACD连涨StreakIsRisingSteps(t *testing.T) {
	cases := []struct {
		name string
		ks   extend.Klines
	}{
		{"线性下跌后回升", linearDeclineThenUp(50, 1)},
		{"加速下跌后回升", accelDeclineThenUp(58)},
	}
	for _, c := range cases {
		hist := util.MACDHistogram(c.ks, 12, 26, 9)
		steps := countRisingSteps(hist)
		if steps < 1 {
			t.Fatalf("%s: 末段连涨步数应 >= 1, 实得 %d", c.name, steps)
		}

		// 恰好连涨 steps 步：{MinDays:steps, MaxDays:steps} 应触发买入
		s := MACD连涨{MinDays: steps, MaxDays: steps}
		if !s.Buy("sz000001", c.ks) {
			t.Fatalf("%s: steps=%d, 期望 {MinDays:%d,MaxDays:%d} 触发买入 (当前实现 streakDays=%d, 多算了1)",
				c.name, steps, steps, steps, steps+1)
		}

		// 连涨步数不足：{MinDays:steps+1, MaxDays:steps+1} 不应触发
		s2 := MACD连涨{MinDays: steps + 1, MaxDays: steps + 1}
		if s2.Buy("sz000001", c.ks) {
			t.Fatalf("%s: steps=%d, 期望 {MinDays:%d,MaxDays:%d} 不触发 (当前实现错误触发)",
				c.name, steps, steps+1, steps+1)
		}
	}
}

// TestMACD负柱缩短 验证绿柱缩短（负柱收窄）判断：
// 1) 下跌后回升的转折日（量柱转红）应触发负柱缩短；
// 2) 负柱仍在放大的加速下跌段不应触发；
// 3) MaxDays 上限生效时，过长的缩短连续段应被拒绝。
func TestMACD负柱缩短(t *testing.T) {
	// 1) 线性下跌后回升：负柱持续收窄，转折日（转红）应触发 MinDays=2
	ks := linearDeclineThenUp(50, 15)
	turnIdx := -1
	for i := 2; i <= len(ks); i++ {
		h := util.MACDHistogram(ks[:i], 12, 26, 9)
		if h[len(h)-1] > 0 && h[len(h)-2] <= 0 {
			turnIdx = i
			break
		}
	}
	if turnIdx < 0 {
		t.Fatal("测试序列未出现量柱转红，测试无效")
	}
	s := MACD负柱缩短{MinDays: 2}
	if !s.Buy("sz000001", ks[:turnIdx]) {
		t.Fatalf("转折日(第%d天)应触发负柱缩短", turnIdx)
	}
	// 转折日当天组合 MACD转红 也应通过
	turn := MACD转红{}
	if !turn.Buy("sz000001", ks[:turnIdx]) {
		t.Fatalf("转折日(第%d天)应触发MACD转红", turnIdx)
	}

	// 2) 加速下跌段：负柱持续放大，不应触发
	ks2 := accelDeclineThenUp(58)
	found := false
	for i := 2; i <= len(ks2); i++ {
		h := util.MACDHistogram(ks2[:i], 12, 26, 9)
		if h[len(h)-1] < h[len(h)-2] {
			found = true
			if s.Buy("sz000001", ks2[:i]) {
				t.Fatalf("第%d天负柱在放大却触发了负柱缩短 (%v -> %v)", i, h[len(h)-2], h[len(h)-1])
			}
		}
	}
	if !found {
		t.Fatal("加速下跌序列未出现负柱放大段，测试无效")
	}

	// 3) MaxDays 上限：转折日前的连续缩短段很长，应被 MaxDays=5 拒绝
	sMax := MACD负柱缩短{MinDays: 2, MaxDays: 5}
	if sMax.Buy("sz000001", ks[:turnIdx]) {
		t.Fatal("连续缩短段超过5天，MaxDays=5 应拒绝")
	}
}

// TestMACD转红 验证量柱由负转正（零轴金叉）的判断。
// 用“长期下跌 + 末段回升”序列，使 MACD 量柱在回升段穿越零轴，
// 逐个截断点校验买家触发条件与 hist[n-1]>0 && hist[n-2]<=0 完全一致。
func TestMACD转红(t *testing.T) {
	ks := linearDeclineThenUp(50, 15)
	s := MACD转红{}

	triggered := false
	for i := 2; i <= len(ks); i++ {
		sub := ks[:i]
		h := util.MACDHistogram(sub, 12, 26, 9)
		last := len(h) - 1
		expectCross := h[last] > 0 && h[last-1] <= 0
		got := s.Buy("sz000001", sub)
		if expectCross && !got {
			t.Fatalf("第 %d 天应为穿越日但未触发 (hist[last]=%v, hist[last-1]=%v)",
				i, h[last], h[last-1])
		}
		if !expectCross && got {
			t.Fatalf("第 %d 天不应触发但触发了 (hist[last]=%v, hist[last-1]=%v)",
				i, h[last], h[last-1])
		}
		if expectCross {
			triggered = true
		}
	}
	if !triggered {
		t.Fatalf("测试序列中未出现任何穿越日，测试无效")
	}
}
