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
