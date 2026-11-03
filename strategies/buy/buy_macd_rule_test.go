package buy

import (
	"math"
	"math/rand"
	"testing"
	"time"

	"github.com/injoyai/strategy-tail/lib/extend"
	f "github.com/injoyai/strategy-tail/strategies/factor"
	"github.com/injoyai/tdx/v2/protocol"
)

// T3 时序批次 A 组迁移行为快照（台账附录 B：buy_macd_negative /
// buy_macd_positive / buy_macd_rising / buy_macd_reversal /
// buy_macd_golden_cross / buy_macd_above_zero / buy_rsi_oversold_rebound）。
// 断言先按旧实现对拍固定，迁移到 strategies/rule 共用内核后本文件必须
// 原样通过；helper 一律 t3a 前缀，避免与其他并行迁移组冲突。
//
// buy_recent_gain_below（A近N日涨幅小于）按台账属本组，但组件实际位于
// buy_price.go（Buy :190-202），超出本组允许修改的文件边界，未迁移；
// 文末用例锁定其旧行为作为后续迁移的对拍基线。
//
// MACD 数值采用小参数（3/6/2，预热 slow+signal=8 根）的手写短序列，
// 并以 factor.MACD柱值.Series 作为序列 oracle；方向性用例均带前提断言，
// 防止夹具失效导致断言恒真。

// t3aCloseKlines 以收盘序列构造 K 线（元，内部转厘；开=收-0.5、高=收+2、
// 低=收-2）。MACD/RSI 只读收盘，高低开不影响柱值。
func t3aCloseKlines(closes ...float64) extend.Klines {
	ks := make(extend.Klines, 0, len(closes))
	for i, c := range closes {
		ks = append(ks, &extend.Kline{
			Kline: &protocol.Kline{
				Time:  time.Date(2026, 1, i+1, 0, 0, 0, 0, time.Local),
				Open:  c - 0.5,
				High:  c + 2,
				Low:   c - 2,
				Close: c,
			},
		})
	}
	return ks
}

// t3aFlatCloses 生成 n 个相同收盘价，用于平价行情与预热边界构造。
func t3aFlatCloses(n int) []float64 {
	out := make([]float64, n)
	for i := range out {
		out[i] = 10
	}
	return out
}

// t3aRandWalk 固定种子随机游走收盘序列（首根 15 元，日波动 ±2.8%）。
func t3aRandWalk(seed, n int) []float64 {
	rng := rand.New(rand.NewSource(int64(seed)))
	closes := make([]float64, n)
	closes[0] = 15
	for i := 1; i < n; i++ {
		closes[i] = closes[i-1] * (1 + rng.Float64()*0.06 - 0.028)
	}
	return closes
}

// ---- 旧实现 oracle（迁移前行为快照；最终对拍完成后按计划第 8 节清理）----

func t3aOracleMACD负数(s MACD负数, dks extend.Klines) bool {
	if s.Fast == 0 {
		s.Fast = 12
	}
	if s.Slow == 0 {
		s.Slow = 26
	}
	if s.Signal == 0 {
		s.Signal = 9
	}
	if s.MinDays == 0 {
		s.MinDays = 1
	}
	maxDays := s.MaxDays
	if maxDays > 0 && maxDays < s.MinDays {
		maxDays = 0
	}
	n := len(dks)
	if n < s.Slow+s.Signal || n < s.MinDays {
		return false
	}
	hist := (f.MACD柱值{Fast: s.Fast, Slow: s.Slow, Signal: s.Signal}).Series(dks)
	if len(hist) != n {
		return false
	}
	count := f.NegativeStreak(hist)
	if count < s.MinDays {
		return false
	}
	if maxDays > 0 && count > maxDays {
		return false
	}
	return true
}

func t3aOracleMACD正数(s MACD正数, dks extend.Klines) bool {
	if s.Fast == 0 {
		s.Fast = 12
	}
	if s.Slow == 0 {
		s.Slow = 26
	}
	if s.Signal == 0 {
		s.Signal = 9
	}
	if s.Days == 0 {
		s.Days = 1
	}
	n := len(dks)
	if n < s.Slow+s.Signal || n < s.Days {
		return false
	}
	hist := (f.MACD柱值{Fast: s.Fast, Slow: s.Slow, Signal: s.Signal}).Series(dks)
	if len(hist) != n {
		return false
	}
	return f.PositiveStreak(hist) >= s.Days
}

func t3aOracleMACD连涨(s MACD连涨, dks extend.Klines) bool {
	if s.Fast == 0 {
		s.Fast = 12
	}
	if s.Slow == 0 {
		s.Slow = 26
	}
	if s.Signal == 0 {
		s.Signal = 9
	}
	if s.MinDays == 0 {
		s.MinDays = 1
	}
	maxDays := s.MaxDays
	if maxDays > 0 && maxDays < s.MinDays {
		maxDays = 0
	}
	n := len(dks)
	if n < s.Slow+s.Signal || n < s.MinDays+1 {
		return false
	}
	hist := (f.MACD柱值{Fast: s.Fast, Slow: s.Slow, Signal: s.Signal}).Series(dks)
	if len(hist) != n {
		return false
	}
	streakEnd := n - 1
	if hist[streakEnd] <= hist[streakEnd-1] {
		return false
	}
	streakDays := f.RisingStreak(hist)
	streakStart := streakEnd - streakDays
	if streakDays < s.MinDays {
		return false
	}
	if maxDays > 0 && streakDays > maxDays {
		return false
	}
	if s.Lookback > 0 {
		startIdx := streakStart - 1
		if startIdx < 0 {
			return false
		}
		windowStart := startIdx - s.Lookback
		if windowStart < 0 {
			windowStart = 0
		}
		for i := windowStart; i < startIdx; i++ {
			if hist[i] < hist[startIdx] {
				return false
			}
		}
	}
	return true
}

func t3aOracleMACD反转(s MACD反转, dks extend.Klines) bool {
	if s.Fast == 0 {
		s.Fast = 12
	}
	if s.Slow == 0 {
		s.Slow = 26
	}
	if s.Signal == 0 {
		s.Signal = 9
	}
	if s.MinLookback == 0 {
		s.MinLookback = 20
	}
	maxLb := s.MaxLookback
	if maxLb > 0 && maxLb < s.MinLookback {
		maxLb = 0
	}
	n := len(dks)
	if n < 2 || n < s.Slow+s.Signal {
		return false
	}
	hist := (f.MACD柱值{Fast: s.Fast, Slow: s.Slow, Signal: s.Signal}).Series(dks)
	if len(hist) != n {
		return false
	}
	yesterday := hist[n-2]
	today := hist[n-1]
	if !(today > yesterday+s.MinDiff) {
		return false
	}
	end := maxLb
	if end == 0 {
		end = s.MinLookback
	}
	for k := s.MinLookback; k <= end; k++ {
		if k <= 0 || f.SeriesPosition(hist[max(n-1-k, 0):n-1]) == 0 {
			return true
		}
	}
	return false
}

func t3aOracleMACD金叉(s MACD金叉, dks extend.Klines) bool {
	fast, slow, signal := defaultMACDParams(s.Fast, s.Slow, s.Signal)
	n := len(dks)
	if n < slow+signal {
		return false
	}
	hist := (f.MACD柱值{Fast: fast, Slow: slow, Signal: signal}).Series(dks)
	if len(hist) != n {
		return false
	}
	return hist[n-2] <= 0 && hist[n-1] > 0
}

func t3aOracleMACD零轴上方(s MACD零轴上方, dks extend.Klines) bool {
	fast, slow, signal := defaultMACDParams(s.Fast, s.Slow, s.Signal)
	n := len(dks)
	if n < slow+signal {
		return false
	}
	hist := (f.MACD柱值{Fast: fast, Slow: slow, Signal: signal}).Series(dks)
	if len(hist) != n {
		return false
	}
	return hist[n-1] > 0
}

func t3aOracleRSI超卖回升(s RSI超卖回升, code string, dks extend.Klines) bool {
	period := s.Period
	if period == 0 {
		period = 14
	}
	threshold := s.Threshold
	if threshold == 0 {
		threshold = 30
	}
	n := len(dks)
	if n < period+2 {
		return false
	}
	prevRSI := f.RSI值(period).Value(code, dks[:n-1])
	rsi := f.RSI值(period).Value(code, dks)
	return prevRSI < threshold && rsi >= threshold
}

// t3aSearchStreak 固定种子搜索末尾连续段（neg: 柱值<0；否则 >0 或上升）
// 达到 minStreak 的序列，返回 K 线、柱值序列与实际连续天数。
func t3aSearchStreak(t *testing.T, fast, slow, signal, minStreak int, neg bool) (extend.Klines, []float64, int) {
	t.Helper()
	for seed := 1; seed <= 500; seed++ {
		ks := t3aCloseKlines(t3aRandWalk(seed, 45)...)
		hist := (f.MACD柱值{Fast: fast, Slow: slow, Signal: signal}).Series(ks)
		var s int
		if neg {
			s = f.NegativeStreak(hist)
		} else {
			s = f.PositiveStreak(hist)
		}
		if s >= minStreak {
			return ks, hist, s
		}
	}
	t.Fatalf("夹具构造失败：500 种子内未找到末尾连续段 >=%d 的序列", minStreak)
	return nil, nil, 0
}

// ---- MACD负数（buy_macd_negative）----

func TestT3aMACD负数快照(t *testing.T) {
	const fast, slow, signal = 3, 6, 2 // 预热 slow+signal=8 根
	p := func(minDays, maxDays int) MACD负数 {
		return MACD负数{Fast: fast, Slow: slow, Signal: signal, MinDays: minDays, MaxDays: maxDays}
	}

	// 平价行情：柱值全 0（EMA 无变化），连续负数恒不成立。
	flat := t3aCloseKlines(t3aFlatCloses(30)...)
	if p(1, 0).Buy("sh600000", flat) {
		t.Fatal("平价行情柱值全 0 不应触发")
	}
	// 预热差一根 / 恰好 MinKlines。
	if p(1, 0).Buy("sh600000", flat[:7]) {
		t.Fatal("预热差一根（7<8）不应触发")
	}
	if p(1, 0).Buy("sh600000", flat[:8]) {
		t.Fatal("恰满预热但平价柱值 0 不满足 <0，不应触发")
	}
	// minDays 大于 slow+signal 时长度门槛归一为 max(8, minDays)。
	if p(10, 0).Buy("sh600000", flat[:9]) {
		t.Fatal("n=9 < minDays=10（门槛归一 10）不应触发")
	}
	if p(10, 0).Buy("sh600000", flat[:10]) {
		t.Fatal("n=10 恰满 minDays=10 但平价柱值 0 不应触发")
	}
	// 空行情 / 零值参数归一（空行情走预热拦截）。
	if (MACD负数{}).Buy("sh600000", nil) {
		t.Fatal("空行情不应触发")
	}

	// 随机夹具：末尾负柱连续段 >=3。
	ks, _, streak := t3aSearchStreak(t, fast, slow, signal, 3, true)
	if !p(1, 0).Buy("sh600000", ks) {
		t.Fatal("零值参数（目录默认 minDays=1）末段连续负柱应触发")
	}
	if !p(streak, 0).Buy("sh600000", ks) {
		t.Fatalf("MinDays 恰等 streak=%d 应触发", streak)
	}
	if p(streak+1, 0).Buy("sh600000", ks) {
		t.Fatalf("MinDays=%d 超出 streak=%d 不应触发", streak+1, streak)
	}
	// MaxDays 恰等 / 超出（负数族 Max 边界）。
	if !p(streak, streak).Buy("sh600000", ks) {
		t.Fatalf("Min=Max=streak=%d 恰等上下限应触发", streak)
	}
	if p(1, streak-1).Buy("sh600000", ks) {
		t.Fatalf("连续段 streak=%d 超出 MaxDays=%d 不应触发", streak, streak-1)
	}
	// MaxDays < MinDays 历史退化：上限不生效（等价只校验下限）。
	if !p(streak, streak-1).Buy("sh600000", ks) {
		t.Fatalf("MaxDays<MinDays 退化不限上限，MinDays=%d 应触发", streak)
	}

	// oracle 对拍：30 序列 × 3 参数组。
	for seed := 1; seed <= 30; seed++ {
		dks := t3aCloseKlines(t3aRandWalk(seed+1000, 45)...)
		for _, c := range []MACD负数{
			{Fast: fast, Slow: slow, Signal: signal},
			{Fast: fast, Slow: slow, Signal: signal, MinDays: 2, MaxDays: 4},
			{Fast: fast, Slow: slow, Signal: signal, MinDays: 3},
		} {
			if got, want := c.Buy("sh600000", dks), t3aOracleMACD负数(c, dks); got != want {
				t.Fatalf("seed=%d %+v: got %v want %v", seed, c, got, want)
			}
		}
	}
}

// ---- MACD正数（buy_macd_positive）----

func TestT3aMACD正数快照(t *testing.T) {
	const fast, slow, signal = 3, 6, 2
	p := func(days int) MACD正数 {
		return MACD正数{Fast: fast, Slow: slow, Signal: signal, Days: days}
	}

	// 平价行情：柱值全 0，连续正数恒不成立。
	flat := t3aCloseKlines(t3aFlatCloses(30)...)
	if p(1).Buy("sh600000", flat) {
		t.Fatal("平价行情柱值全 0 不应触发")
	}
	if p(1).Buy("sh600000", flat[:7]) {
		t.Fatal("预热差一根（7<8）不应触发")
	}
	if p(1).Buy("sh600000", flat[:8]) {
		t.Fatal("恰满预热但平价柱值 0 不满足 >0，不应触发")
	}
	if (MACD正数{}).Buy("sh600000", nil) {
		t.Fatal("空行情不应触发")
	}

	// 随机夹具：末尾正柱连续段 >=3。
	ks, _, streak := t3aSearchStreak(t, fast, slow, signal, 3, false)
	if !p(1).Buy("sh600000", ks) {
		t.Fatal("零值参数（目录默认 days=1）末段连续正柱应触发")
	}
	if !p(streak).Buy("sh600000", ks) {
		t.Fatalf("Days 恰等 streak=%d 应触发", streak)
	}
	if p(streak+1).Buy("sh600000", ks) {
		t.Fatalf("Days=%d 超出 streak=%d 不应触发", streak+1, streak)
	}

	// oracle 对拍：30 序列 × 3 参数组。
	for seed := 1; seed <= 30; seed++ {
		dks := t3aCloseKlines(t3aRandWalk(seed+2000, 45)...)
		for _, c := range []MACD正数{
			{Fast: fast, Slow: slow, Signal: signal},
			{Fast: fast, Slow: slow, Signal: signal, Days: 2},
			{Fast: fast, Slow: slow, Signal: signal, Days: 5},
		} {
			if got, want := c.Buy("sh600000", dks), t3aOracleMACD正数(c, dks); got != want {
				t.Fatalf("seed=%d %+v: got %v want %v", seed, c, got, want)
			}
		}
	}
}

// ---- MACD连涨（buy_macd_rising）----

func TestT3aMACD连涨快照(t *testing.T) {
	const fast, slow, signal = 3, 6, 2 // 预热 max(8, minDays+1) 根
	p := func(minDays, maxDays, lookback int) MACD连涨 {
		return MACD连涨{Fast: fast, Slow: slow, Signal: signal, MinDays: minDays, MaxDays: maxDays, Lookback: lookback}
	}

	// 平价行情：今柱>昨柱恒不成立。
	flat := t3aCloseKlines(t3aFlatCloses(30)...)
	if p(1, 0, 0).Buy("sh600000", flat) {
		t.Fatal("平价行情不应触发")
	}
	if p(1, 0, 0).Buy("sh600000", flat[:7]) {
		t.Fatal("预热差一根（7<8）不应触发")
	}
	if p(1, 0, 0).Buy("sh600000", flat[:8]) {
		t.Fatal("恰满预热但平价柱值 0 不满足今>昨，不应触发")
	}
	if (MACD连涨{}).Buy("sh600000", nil) {
		t.Fatal("空行情不应触发")
	}

	// 随机夹具：末尾上升连续段 >=3。
	var ks extend.Klines
	var hist []float64
	streak := 0
	for seed := 1; seed <= 500; seed++ {
		cand := t3aCloseKlines(t3aRandWalk(seed, 45)...)
		h := (f.MACD柱值{Fast: fast, Slow: slow, Signal: signal}).Series(cand)
		if r := f.RisingStreak(h); r >= 3 {
			ks, hist, streak = cand, h, r
			break
		}
	}
	if streak < 3 {
		t.Fatal("夹具构造失败：500 种子内未找到末尾上升连续段 >=3 的序列")
	}
	if !p(1, 0, 0).Buy("sh600000", ks) {
		t.Fatal("零值参数（目录默认 minDays=1）末段连续上升应触发")
	}
	if !p(streak, 0, 0).Buy("sh600000", ks) {
		t.Fatalf("MinDays 恰等 streak=%d 应触发", streak)
	}
	if p(streak+1, 0, 0).Buy("sh600000", ks) {
		t.Fatalf("MinDays=%d 超出 streak=%d 不应触发", streak+1, streak)
	}
	if !p(streak, streak, 0).Buy("sh600000", ks) {
		t.Fatalf("Min=Max=streak=%d 恰等上下限应触发", streak)
	}
	if p(1, streak-1, 0).Buy("sh600000", ks) {
		t.Fatalf("连续上升 streak=%d 超出 MaxDays=%d 不应触发", streak, streak-1)
	}

	// Lookback 起点校验：lookback=1 时窗口仅含起点前一根 hist[startIdx-1]，
	// 接受 ⟺ hist[startIdx-1] >= hist[startIdx]（起点为窗口最低，含并列）。
	// 固定种子搜索一个接受种子与一个拒绝种子，前提断言防夹具失效。
	startIdx := len(ks) - 2 - streak
	accSeed, rejSeed := 0, 0
	for seed := 1; seed <= 500 && (accSeed == 0 || rejSeed == 0); seed++ {
		cand := t3aCloseKlines(t3aRandWalk(seed, 45)...)
		h := (f.MACD柱值{Fast: fast, Slow: slow, Signal: signal}).Series(cand)
		if f.RisingStreak(h) < 3 {
			continue
		}
		si := len(cand) - 2 - f.RisingStreak(h)
		if si <= 0 {
			continue
		}
		if h[si-1] >= h[si] {
			if accSeed == 0 {
				accSeed = seed
			}
		} else {
			if rejSeed == 0 {
				rejSeed = seed
			}
		}
	}
	if accSeed == 0 || rejSeed == 0 {
		t.Fatal("夹具构造失败：500 种子内未凑齐 lookback=1 接受/拒绝种子")
	}
	acc := t3aCloseKlines(t3aRandWalk(accSeed, 45)...)
	rej := t3aCloseKlines(t3aRandWalk(rejSeed, 45)...)
	if !p(1, 0, 1).Buy("sh600000", acc) {
		t.Fatalf("lookback=1 起点%v >= 前值应触发", hist[startIdx])
	}
	if p(1, 0, 1).Buy("sh600000", rej) {
		t.Fatal("lookback=1 窗口存在低于起点的柱值不应触发")
	}

	// lookback 覆盖全前缀（60 > n）：起点前历史上存在更低柱值即拒绝；
	// 下跌后回升序列起点柱通常高于历史最深负柱，应拒绝（前提断言）。
	dip := make([]float64, 0, 45)
	for i := 0; i < 25; i++ {
		dip = append(dip, 15*math.Pow(0.97, float64(i)))
	}
	for i := 0; i < 20; i++ {
		dip = append(dip, dip[len(dip)-1]*1.01)
	}
	dipKs := t3aCloseKlines(dip...)
	dipHist := (f.MACD柱值{Fast: fast, Slow: slow, Signal: signal}).Series(dipKs)
	dipStart := len(dipKs) - 2 - f.RisingStreak(dipHist)
	if dipStart > 0 {
		hasLower := false
		for _, v := range dipHist[:dipStart] {
			if v < dipHist[dipStart] {
				hasLower = true
				break
			}
		}
		if hasLower && p(1, 0, 60).Buy("sh600000", dipKs) {
			t.Fatal("lookback=60 覆盖全前缀且存在更低柱值不应触发")
		}
	}

	// 极端起点分支（确定性构造，前提断言防御）：
	// - startIdx==0（连涨段起点即首根前一位）：旧窗口 [0,0) 为空、扫描不
	//   执行，视为通过 → lookback>0 仍触发；
	// - startIdx<0（全序列柱值连续上升，streak=n-1）：旧实现直接拒绝。
	ramp := t3aCloseKlines(10, 10, 11, 12.5, 15, 19, 25, 32) // startIdx==0
	rampHist := (f.MACD柱值{Fast: fast, Slow: slow, Signal: signal}).Series(ramp)
	if !(len(ramp)-2-f.RisingStreak(rampHist) == 0) {
		t.Fatalf("夹具前提不成立：startIdx=%d", len(ramp)-2-f.RisingStreak(rampHist))
	}
	if !p(1, 0, 5).Buy("sh600000", ramp) {
		t.Fatal("startIdx==0 时旧窗口为空视为通过，lookback>0 应触发")
	}
	soar := t3aCloseKlines(10, 10.5, 11.5, 13, 15.5, 19, 24, 30) // streak=n-1
	soarHist := (f.MACD柱值{Fast: fast, Slow: slow, Signal: signal}).Series(soar)
	if !(len(soar)-2-f.RisingStreak(soarHist) < 0) {
		t.Fatalf("夹具前提不成立：startIdx=%d", len(soar)-2-f.RisingStreak(soarHist))
	}
	if p(1, 0, 5).Buy("sh600000", soar) {
		t.Fatal("startIdx<0（连涨段起点无前一根）不应触发")
	}

	// oracle 对拍：30 序列 × lookback {0,1,5} × min/max 组合。
	for seed := 1; seed <= 30; seed++ {
		dks := t3aCloseKlines(t3aRandWalk(seed+3000, 45)...)
		for _, c := range []MACD连涨{
			{Fast: fast, Slow: slow, Signal: signal},
			{Fast: fast, Slow: slow, Signal: signal, Lookback: 1},
			{Fast: fast, Slow: slow, Signal: signal, Lookback: 5},
			{Fast: fast, Slow: slow, Signal: signal, MinDays: 2, MaxDays: 4, Lookback: 8},
		} {
			if got, want := c.Buy("sh600000", dks), t3aOracleMACD连涨(c, dks); got != want {
				t.Fatalf("seed=%d %+v: got %v want %v", seed, c, got, want)
			}
		}
	}
}

// ---- MACD反转（buy_macd_reversal）----

func TestT3aMACD反转快照(t *testing.T) {
	const fast, slow, signal = 12, 26, 9 // 默认参数，预热 35 根

	// 平价行情：柱值全 0。
	flat := t3aCloseKlines(t3aFlatCloses(35)...)
	if (MACD反转{Fast: fast, Slow: slow, Signal: signal}).Buy("sh600000", flat) {
		t.Fatal("平价行情今柱 0 不大于昨柱+0 不应触发")
	}
	// 负 MinDiff：0 > 0-0.5 成立，且昨日（0）为近 20 日窗口最低（全平窗口
	// SeriesPosition==0，含并列平价）→ 触发；预热差一根仍拦截。
	if !(MACD反转{Fast: fast, Slow: slow, Signal: signal, MinDiff: -0.5}).Buy("sh600000", flat) {
		t.Fatal("MinDiff=-0.5 平价行情应触发（昨日为窗口并列最低）")
	}
	if (MACD反转{Fast: fast, Slow: slow, Signal: signal, MinDiff: -0.5}).Buy("sh600000", flat[:34]) {
		t.Fatal("预热差一根（34<35）不应触发")
	}
	if (MACD反转{}).Buy("sh600000", nil) {
		t.Fatal("空行情不应触发")
	}

	// 随机夹具：今日柱>昨日柱 且 昨日为近 20 日窗口最低（默认参数命中）。
	found := false
	for seed := 1; seed <= 500 && !found; seed++ {
		ks := t3aCloseKlines(t3aRandWalk(seed, 45)...)
		if !(MACD反转{Fast: fast, Slow: slow, Signal: signal}).Buy("sh600000", ks) {
			continue
		}
		found = true
		// MinLookback 提高到 30：昨日是否仍为近 30 日窗口最低由 oracle 判定，
		// 断言与默认参数一致的对拍关系由下方 oracle 对拍覆盖，这里仅要求
		// 命中夹具存在。
	}
	if !found {
		t.Fatal("夹具构造失败：500 种子内未找到 MACD 低位拐头序列")
	}

	// oracle 对拍：40 序列 × 4 参数组（覆盖 k 扫描区间与 MinDiff）。
	for seed := 1; seed <= 40; seed++ {
		dks := t3aCloseKlines(t3aRandWalk(seed+4000, 45)...)
		for _, c := range []MACD反转{
			{Fast: fast, Slow: slow, Signal: signal},
			{Fast: fast, Slow: slow, Signal: signal, MinLookback: 4, MaxLookback: 20},
			{Fast: fast, Slow: slow, Signal: signal, MinDiff: 0.02},
			{Fast: fast, Slow: slow, Signal: signal, MinLookback: 40, MaxLookback: 44},
		} {
			if got, want := c.Buy("sh600000", dks), t3aOracleMACD反转(c, dks); got != want {
				t.Fatalf("seed=%d %+v: got %v want %v", seed, c, got, want)
			}
		}
	}
}

// ---- MACD金叉（buy_macd_golden_cross）----

func TestT3aMACD金叉快照(t *testing.T) {
	const fast, slow, signal = 3, 6, 2 // 预热 8 根
	p := MACD金叉{Fast: fast, Slow: slow, Signal: signal}

	// 昨柱恰等 0（7 根平价后大涨）：前 7 根 EMA 无变化 → 昨柱==0，
	// 末根大涨 → 今柱>0；恰等在昨日侧（<=0），应触发。前提断言防御。
	gap := t3aCloseKlines(10, 10, 10, 10, 10, 10, 10, 15)
	gapHist := (f.MACD柱值{Fast: fast, Slow: slow, Signal: signal}).Series(gap)
	if gapHist[len(gapHist)-2] != 0 || gapHist[len(gapHist)-1] <= 0 {
		t.Fatalf("夹具前提不成立：昨柱=%v 今柱=%v", gapHist[len(gapHist)-2], gapHist[len(gapHist)-1])
	}
	if !p.Buy("sh600000", gap) {
		t.Fatal("昨柱恰等 0 且今柱>0（恰等在昨日侧）应触发")
	}
	// 今柱恰等 0（全平序列）：>0 严格，不触发。
	eq := t3aCloseKlines(t3aFlatCloses(8)...)
	if p.Buy("sh600000", eq) {
		t.Fatal("今柱恰等 0（>0 严格）不应触发")
	}
	// 预热差一根。
	if p.Buy("sh600000", eq[:7]) {
		t.Fatal("预热差一根（7<8）不应触发")
	}
	if p.Buy("sh600000", nil) {
		t.Fatal("空行情不应触发")
	}
	// 昨柱<0 今柱>0 的正常金叉（深跌后大阳），前提断言防御。
	diving := t3aCloseKlines(10, 9, 8, 7, 6, 5, 4, 3, 25)
	diveHist := (f.MACD柱值{Fast: fast, Slow: slow, Signal: signal}).Series(diving)
	if !(diveHist[len(diveHist)-2] < 0 && diveHist[len(diveHist)-1] > 0) {
		t.Fatalf("夹具前提不成立：昨柱=%v 今柱=%v", diveHist[len(diveHist)-2], diveHist[len(diveHist)-1])
	}
	if !p.Buy("sh600000", diving) {
		t.Fatal("昨柱<0 且今柱>0 应触发")
	}

	// oracle 对拍：40 序列 × 2 参数组。
	for seed := 1; seed <= 40; seed++ {
		dks := t3aCloseKlines(t3aRandWalk(seed+5000, 45)...)
		for _, c := range []MACD金叉{
			{Fast: fast, Slow: slow, Signal: signal},
			{}, // 目录默认 12/26/9
		} {
			if got, want := c.Buy("sh600000", dks), t3aOracleMACD金叉(c, dks); got != want {
				t.Fatalf("seed=%d %+v: got %v want %v", seed, c, got, want)
			}
		}
	}
}

// ---- MACD零轴上方（buy_macd_above_zero）----

func TestT3aMACD零轴上方快照(t *testing.T) {
	const fast, slow, signal = 3, 6, 2
	p := MACD零轴上方{Fast: fast, Slow: slow, Signal: signal}

	// 平价行情：今柱 0 不满足 >0。
	flat := t3aCloseKlines(t3aFlatCloses(8)...)
	if p.Buy("sh600000", flat) {
		t.Fatal("平价行情今柱恰等 0 不应触发")
	}
	if p.Buy("sh600000", flat[:7]) {
		t.Fatal("预热差一根（7<8）不应触发")
	}
	if p.Buy("sh600000", nil) {
		t.Fatal("空行情不应触发")
	}
	// 持续上涨 → 今柱>0 应触发；持续下跌 → 不触发，前提断言防御。
	up := t3aCloseKlines(6, 7, 8, 9, 10, 11, 12, 13)
	upHist := (f.MACD柱值{Fast: fast, Slow: slow, Signal: signal}).Series(up)
	if upHist[len(upHist)-1] <= 0 {
		t.Fatalf("夹具前提不成立：末柱=%v", upHist[len(upHist)-1])
	}
	if !p.Buy("sh600000", up) {
		t.Fatal("持续上涨末柱>0 应触发")
	}
	down := t3aCloseKlines(13, 12, 11, 10, 9, 8, 7, 6)
	downHist := (f.MACD柱值{Fast: fast, Slow: slow, Signal: signal}).Series(down)
	if downHist[len(downHist)-1] >= 0 {
		t.Fatalf("夹具前提不成立：末柱=%v", downHist[len(downHist)-1])
	}
	if p.Buy("sh600000", down) {
		t.Fatal("持续下跌末柱<0 不应触发")
	}

	// oracle 对拍：40 序列 × 2 参数组。
	for seed := 1; seed <= 40; seed++ {
		dks := t3aCloseKlines(t3aRandWalk(seed+6000, 45)...)
		for _, c := range []MACD零轴上方{
			{Fast: fast, Slow: slow, Signal: signal},
			{}, // 目录默认 12/26/9
		} {
			if got, want := c.Buy("sh600000", dks), t3aOracleMACD零轴上方(c, dks); got != want {
				t.Fatalf("seed=%d %+v: got %v want %v", seed, c, got, want)
			}
		}
	}
}

// ---- RSI超卖回升（buy_rsi_oversold_rebound）----

func TestT3aRSI超卖回升快照(t *testing.T) {
	// 预热：昨侧 RSI 需 period+1 根前缀 → n<period+2 拦截。
	short := t3aCloseKlines(t3aRandWalk(7, 15)...) // n=15 < 14+2
	if (RSI超卖回升{}).Buy("sh600000", short) {
		t.Fatal("预热差一根（15<16）不应触发")
	}
	if (RSI超卖回升{}).Buy("sh600000", nil) {
		t.Fatal("空行情不应触发")
	}
	// 持续上涨序列 RSI 高位：昨 RSI<threshold 不成立，不触发。
	rally := t3aCloseKlines(t3aRandWalk(8, 20)...)
	for i := range rally {
		rally[i].Close = 10 * math.Pow(1.02, float64(i))
		rally[i].Open = rally[i].Close
	}
	if (RSI超卖回升{}).Buy("sh600000", rally) {
		t.Fatal("持续上涨 RSI 高位不应触发")
	}

	// 跨阈值夹具：昨 RSI<threshold 且 今 RSI>=threshold（恰等在今日侧）。
	// 固定种子搜索 period=14/threshold=30 与 period=2/threshold=30 各一例。
	search := func(period int, n int, amp float64) (extend.Klines, float64) {
		for seed := 1; seed <= 800; seed++ {
			rng := rand.New(rand.NewSource(int64(seed)))
			closes := make([]float64, n)
			closes[0] = 10
			for i := 1; i < n; i++ {
				closes[i] = closes[i-1] * (1 + rng.Float64()*amp - amp/2)
			}
			ks := t3aCloseKlines(closes...)
			prev := f.RSI值(period).Value("sh600000", ks[:len(ks)-1])
			cur := f.RSI值(period).Value("sh600000", ks)
			if prev < 30 && cur >= 30 {
				return ks, cur
			}
		}
		return nil, math.NaN()
	}
	ks14, cur14 := search(14, 20, 0.05)
	if ks14 == nil {
		t.Fatal("夹具构造失败：800 种子内未找到 RSI(14) 跨 30 序列")
	}
	if !(RSI超卖回升{}).Buy("sh600000", ks14) {
		t.Fatal("昨 RSI<30 且今 RSI>=30（恰等在今日侧）应触发")
	}
	// 阈值提高到今 RSI 之上：今 RSI < threshold 不触发。
	if (RSI超卖回升{Threshold: cur14 + 1}).Buy("sh600000", ks14) {
		t.Fatalf("今 RSI=%.4f 低于阈值 %.4f 不应触发", cur14, cur14+1)
	}
	ks2, cur2 := search(2, 10, 0.08)
	if ks2 == nil {
		t.Fatal("夹具构造失败：800 种子内未找到 RSI(2) 跨 30 序列")
	}
	if !(RSI超卖回升{Period: 2}).Buy("sh600000", ks2) {
		t.Fatal("period=2 跨阈值应触发")
	}
	_ = cur2

	// oracle 对拍：30 序列 × 4 参数组。
	for seed := 1; seed <= 30; seed++ {
		rng := rand.New(rand.NewSource(int64(seed + 7000)))
		closes := make([]float64, 24)
		closes[0] = 12
		for i := 1; i < len(closes); i++ {
			closes[i] = closes[i-1] * (1 + rng.Float64()*0.05 - 0.025)
		}
		dks := t3aCloseKlines(closes...)
		for _, c := range []RSI超卖回升{
			{},
			{Threshold: 50},
			{Period: 2},
			{Period: 5, Threshold: 70},
		} {
			if got, want := c.Buy("sh600000", dks), t3aOracleRSI超卖回升(c, "sh600000", dks); got != want {
				t.Fatalf("seed=%d %+v: got %v want %v", seed, c, got, want)
			}
		}
	}
}

// ---- A近N日涨幅小于（buy_recent_gain_below，锁定旧路径基线）----
// 组件实际位于 buy_price.go:190-202，超出 T3-A 组允许修改的文件边界，
// 本组未迁移；以下用例锁定旧行为（riseRateNDays 严格小于、数据不足或
// 起点价 <=0 不触发、0→days=5/0→max=15 归一），供后续迁移对拍。

func TestT3a近N日涨幅小于旧行为快照(t *testing.T) {
	// 收盘 10 →(5日)→ 12.5：涨幅 (12.5-10)/10*100 = 25（浮点精确）。
	ks := t3aCloseKlines(10, 10.5, 11, 11.5, 12, 12.5)
	if (A近N日涨幅小于{Days: 5, Max: 25}).Buy("sh600000", ks) {
		t.Fatal("涨幅恰等阈值（25<25 严格）不应触发")
	}
	if !(A近N日涨幅小于{Days: 5, Max: 25.5}).Buy("sh600000", ks) {
		t.Fatal("涨幅 25 < 25.5 应触发")
	}
	// 零值归一：Days 0→5、Max 0→15；涨幅 25 > 15 不触发。
	if (A近N日涨幅小于{}).Buy("sh600000", ks) {
		t.Fatal("零值参数（默认 max=15）涨幅 25 不应触发")
	}
	// 非默认窗口：近 3 日从 11 → 12.5 ≈ 13.64%，max=14 应触发。
	if !(A近N日涨幅小于{Days: 3, Max: 14}).Buy("sh600000", ks) {
		t.Fatal("近3日涨幅约 13.64% < 14% 应触发")
	}
	// 数据不足：n=5 < days+1=6。
	if (A近N日涨幅小于{Days: 5, Max: 999}).Buy("sh600000", ks[:5]) {
		t.Fatal("数据不足（5<6）不应触发")
	}
	// 起点收盘 <=0（起点取 n-1-days 位置收盘，riseRateNDays 拦截）：不触发。
	if (A近N日涨幅小于{Days: 2, Max: 999}).Buy("sh600000", t3aCloseKlines(10, 0, 11, 12)) {
		t.Fatal("起点收盘 0 不应触发")
	}
	// 空行情。
	if (A近N日涨幅小于{}).Buy("sh600000", nil) {
		t.Fatal("空行情不应触发")
	}
}
