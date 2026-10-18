package buy

// T3-B 六组件迁移对拍（永久回归）：A站上N日均线 / MAUp / RSI拐头 /
// A回踩N日均线 / A底部抬升 / A近N日涨幅小于。以旧实现逐语句内联为 oracle，
// 固定种子随机游走对拍 + 确定性边界用例，任一不一致即 Fail（断言先按旧实现
// 固定，迁移到 strategies/rule 内核后必须原样通过）。helper 统一 t3b 前缀。

import (
	"crypto/sha256"
	"encoding/hex"
	"math"
	"math/rand"
	"strings"
	"testing"
	"time"

	"github.com/injoyai/strategy-tail/lib/extend"
	f "github.com/injoyai/strategy-tail/strategies/factor"
	"github.com/injoyai/tdx/protocol"
)

// ---- 数据构造（确定性）----

func t3bCloseKlines(closes []float64) extend.Klines {
	ks := make(extend.Klines, 0, len(closes))
	for i, c := range closes {
		ks = append(ks, &extend.Kline{
			Kline: &protocol.Kline{
				Time:  time.Date(2026, 1, i+1, 0, 0, 0, 0, time.Local),
				Open:  protocol.Yuan(c - 0.5),
				High:  protocol.Yuan(c + 2),
				Low:   protocol.Yuan(c - 2),
				Close: protocol.Yuan(c),
			},
		})
	}
	return ks
}

// t3bOHLC 显式收盘与最低价序列（开=收，高=max(收,低)+1），用于精确控制触碰。
func t3bOHLC(closes, lows []float64) extend.Klines {
	ks := make(extend.Klines, 0, len(closes))
	for i, c := range closes {
		ks = append(ks, &extend.Kline{
			Kline: &protocol.Kline{
				Time:  time.Date(2026, 1, i+1, 0, 0, 0, 0, time.Local),
				Open:  protocol.Yuan(c),
				High:  protocol.Yuan(math.Max(c, lows[i]) + 1),
				Low:   protocol.Yuan(lows[i]),
				Close: protocol.Yuan(c),
			},
		})
	}
	return ks
}

func t3bRandWalk(seed, n int) []float64 {
	rng := rand.New(rand.NewSource(int64(seed)))
	closes := make([]float64, n)
	closes[0] = 15
	for i := 1; i < n; i++ {
		closes[i] = closes[i-1] * (1 + rng.Float64()*0.06 - 0.028)
	}
	return closes
}

// t3bWave 正弦波加正向漂移（谷峰形态、谷值抬升），用于底部抬升对拍。
func t3bWave(seed, n int) []float64 {
	rng := rand.New(rand.NewSource(int64(seed)))
	half := 8 + rng.Intn(6)
	amp := 1.5 + rng.Float64()
	drift := 0.03 + rng.Float64()*0.04
	closes := make([]float64, n)
	for i := range closes {
		closes[i] = 8 + drift*float64(i) + amp*math.Sin(2*math.Pi*float64(i)/(2*float64(half)))
	}
	return closes
}

// t3bWaveFlat 无漂移正弦波（谷值持平）。
func t3bWaveFlat(n int) []float64 {
	closes := make([]float64, n)
	for i := range closes {
		closes[i] = 8 + 1.5*math.Sin(2*math.Pi*float64(i)/32)
	}
	return closes
}

// ---- 旧实现 oracle 复刻（迁移前行为快照）----

func t3bOracle站上均线(s A站上N日均线, code string, dks extend.Klines) bool {
	period := s.Period
	if period == 0 {
		period = 20
	}
	days := s.Days
	if days == 0 {
		days = 5
	}
	n := len(dks)
	minLen := period + days
	if n < minLen {
		return false
	}
	return (f.MA上方连续天数{Period: period, Days: days}).Value(code, dks) >= float64(days)
}

func t3bOracleMAUp(b MAUp, code string, dks extend.Klines) bool {
	if b.Period == 0 {
		b.Period = 20
	}
	if b.Lookback == 0 {
		b.Lookback = 5
	}
	return t3bMaUp(dks, b.Period, b.Lookback, b.MinSlope)
}

func t3bMaUp(dks extend.Klines, period, lookback int, minSlope float64) bool {
	if period <= 0 || lookback <= 0 || len(dks) < period+lookback {
		return false
	}
	slope := (f.MA最弱日斜率{Period: period, Lookback: lookback}).Value(code0, dks)
	return slope > 0 && !(slope < minSlope)
}

const code0 = ""

func t3bOracleRSI拐头(s RSI拐头, code string, dks extend.Klines) bool {
	p := s.Period
	if p == 0 {
		p = 14
	}
	if len(dks) < p+2 {
		return false
	}
	todayRSI := f.RSI值(p).Value(code, dks)
	prevRSI := f.RSI值(p).Value(code, dks[:len(dks)-1])
	return todayRSI > prevRSI
}

func t3bOracle回踩均线(s A回踩N日均线, code string, dks extend.Klines) bool {
	period := s.Period
	if period == 0 {
		period = 5
	}
	supportPeriod := s.SupportPeriod
	touchDays := s.MinTouchDays
	if touchDays == 0 {
		touchDays = 3
	}
	n := len(dks)
	minLen := period + touchDays + 1
	if supportPeriod > 0 && supportPeriod+touchDays+1 > minLen {
		minLen = supportPeriod + touchDays + 1
	}
	if n < minLen {
		return false
	}
	today := dks[n-1]
	yesterday := dks[n-2]
	maPeriodToday := f.A均线(period).Value(code, dks)
	maPeriodYesterday := f.A均线(period).Value(code, dks[:n-1])
	if today.Close.Float64() <= maPeriodToday {
		return false
	}
	if today.Close.Float64() <= yesterday.Close.Float64() {
		return false
	}
	if yesterday.Close.Float64() > maPeriodYesterday {
		return false
	}
	touched := false
	windowStart := n - touchDays - 1
	if windowStart < 0 {
		windowStart = 0
	}
	lowestLow := dks[windowStart].Low.Float64()
	for i := windowStart; i < n-1; i++ {
		maAtDay := f.A均线(period).Value(code, dks[:i+1])
		if dks[i].Low.Float64() <= maAtDay {
			touched = true
		}
		if dks[i].Low.Float64() < lowestLow {
			lowestLow = dks[i].Low.Float64()
		}
	}
	if !touched {
		return false
	}
	if supportPeriod > 0 {
		supportIdx := windowStart - 1
		if supportIdx < supportPeriod {
			supportIdx = supportPeriod - 1
		}
		maSupport := f.A均线(supportPeriod).Value(code, dks[:supportIdx+1])
		if lowestLow < maSupport {
			return false
		}
	}
	return true
}

func t3bOracle底部抬升(s A底部抬升, code string, dks extend.Klines) bool {
	s = s.withDefaults()
	if len(dks) < 60 {
		return false
	}
	highs, lows := findTrendUpPoints(dks, s.Window)
	if len(lows) < 2 {
		return false
	}
	l2, l1 := lows[0], lows[1]
	if len(highs) < 1 {
		return false
	}
	return isBottomUpShape(l1, l2, highs, s.Window)
}

func t3bOracle近N日涨幅(b A近N日涨幅小于, code string, dks extend.Klines) bool {
	days := b.Days
	if days == 0 {
		days = 5
	}
	max := b.Max
	if max == 0 {
		max = 15
	}
	n := len(dks)
	if n < days+1 {
		return false
	}
	startPrice := dks[n-1-days].Close.Float64()
	endPrice := dks[n-1].Close.Float64()
	if startPrice <= 0 {
		return false
	}
	rise := (endPrice - startPrice) / startPrice * 100
	return rise < max
}

// ---- 对拍主体 ----

func TestBuyT3B组件对拍(t *testing.T) {
	const seeds = 32

	// 一致性对拍：组件实现必须与 oracle 复刻逐用例相等（迁移前验证复刻正确）。
	check := func(label string, p interface{}, seed int, got, want bool, bits *strings.Builder) {
		if got != want {
			t.Fatalf("%s %+v seed=%d 对拍失败: 组件=%v oracle=%v", label, p, seed, got, want)
		}
		if got {
			bits.WriteByte('1')
		} else {
			bits.WriteByte('0')
		}
	}
	report := func(label string, p interface{}, bits string) {
		sum := sha256.Sum256([]byte(bits))
		t.Logf("[T3B-HASH] %s %+v sha256=%s true=%d/%d", label, p, hex.EncodeToString(sum[:]), strings.Count(bits, "1"), seeds)
	}

	// ① A站上N日均线：随机游走对拍
	for _, p := range []A站上N日均线{{Period: 5, Days: 3}, {Period: 20, Days: 5}, {Period: 0, Days: 0}, {Period: 10, Days: 2}} {
		var bits strings.Builder
		for seed := 0; seed < seeds; seed++ {
			ks := t3bCloseKlines(t3bRandWalk(seed, 80))
			check("①站上均线", p, seed, p.Buy("", ks), t3bOracle站上均线(p, "", ks), &bits)
		}
		report("①站上均线", p, bits.String())
	}

	// ② MAUp：随机游走对拍
	for _, p := range []MAUp{{Period: 5, Lookback: 1}, {Period: 20, Lookback: 5}, {Period: 20, Lookback: 5, MinSlope: 0.05}, {Period: 5, Lookback: 1, MinSlope: -0.5}} {
		var bits strings.Builder
		for seed := 0; seed < seeds; seed++ {
			ks := t3bCloseKlines(t3bRandWalk(seed, 80))
			check("②MAUp", p, seed, p.Buy("", ks), t3bOracleMAUp(p, "", ks), &bits)
		}
		report("②MAUp", p, bits.String())
	}

	// ③ RSI拐头：随机游走对拍
	for _, p := range []RSI拐头{{Period: 5}, {Period: 14}, {Period: 0}} {
		var bits strings.Builder
		for seed := 0; seed < seeds; seed++ {
			ks := t3bCloseKlines(t3bRandWalk(seed, 80))
			check("③RSI拐头", p, seed, p.Buy("", ks), t3bOracleRSI拐头(p, "", ks), &bits)
		}
		report("③RSI拐头", p, bits.String())
	}

	// ④ A回踩N日均线：随机游走对拍
	for _, p := range []A回踩N日均线{{Period: 5}, {Period: 5, SupportPeriod: 10}, {Period: 5, MinTouchDays: 2}, {Period: 8, SupportPeriod: 12}, {Period: 0}} {
		var bits strings.Builder
		for seed := 0; seed < seeds; seed++ {
			ks := t3bCloseKlines(t3bRandWalk(seed, 80))
			check("④回踩均线", p, seed, p.Buy("", ks), t3bOracle回踩均线(p, "", ks), &bits)
		}
		report("④回踩均线", p, bits.String())
	}

	// ⑤ A底部抬升：波形数据对拍（随机游走数据下恒 false 也一并对拍）
	for _, p := range []A底部抬升{{Window: 4}, {Window: 8}, {Window: 12}, {Window: 0}} {
		var bits strings.Builder
		for seed := 0; seed < seeds; seed++ {
			ks := t3bCloseKlines(t3bWave(seed, 90))
			check("⑤底部抬升wave", p, seed, p.Buy("", ks), t3bOracle底部抬升(p, "", ks), &bits)
			ks2 := t3bCloseKlines(t3bRandWalk(seed+1000, 90))
			check("⑤底部抬升walk", p, seed, p.Buy("", ks2), t3bOracle底部抬升(p, "", ks2), &bits)
		}
		report("⑤底部抬升", p, bits.String())
	}

	// ⑥ A近N日涨幅小于：随机游走对拍
	for _, p := range []A近N日涨幅小于{{Days: 3, Max: 5}, {Days: 5, Max: 15}, {Days: 0}, {Days: 5, Max: 0}} {
		var bits strings.Builder
		for seed := 0; seed < seeds; seed++ {
			ks := t3bCloseKlines(t3bRandWalk(seed, 80))
			check("⑥近N日涨幅", p, seed, p.Buy("", ks), t3bOracle近N日涨幅(p, "", ks), &bits)
		}
		report("⑥近N日涨幅", p, bits.String())
	}

	// 确定性边界用例：打印旧实现输出，供快照测试硬编码。
	edge := []struct {
		label string
		got   bool
	}{
		// ① 站上均线 period=5 days=3（minLen=8）
		{"①len7预热不足", (A站上N日均线{Period: 5, Days: 3}).Buy("", t3bCloseKlines([]float64{10, 10, 10, 10, 10, 11, 11}))},
		{"①len8连续3天站上", (A站上N日均线{Period: 5, Days: 3}).Buy("", t3bCloseKlines([]float64{10, 10, 10, 10, 10, 11, 11, 11}))},
		{"①len8全平close恰等MA", (A站上N日均线{Period: 5, Days: 3}).Buy("", t3bCloseKlines([]float64{10, 10, 10, 10, 10, 10, 10, 10}))},
		{"①中途跌破后仅2天", (A站上N日均线{Period: 5, Days: 3}).Buy("", t3bCloseKlines([]float64{10, 10, 10, 10, 10, 9, 11, 11}))},
		{"①默认len24预热不足", (A站上N日均线{}).Buy("", t3bCloseKlines(t3bRandWalk(7, 24)))},
		{"①默认len25随机", (A站上N日均线{}).Buy("", t3bCloseKlines(t3bRandWalk(7, 25)))},

		// ② MAUp period=5 lookback=1（minLen=6）
		{"②len5预热不足", (MAUp{Period: 5, Lookback: 1}).Buy("", t3bCloseKlines([]float64{10, 10.1, 10.2, 10.3, 10.4}))},
		{"②len6上涨", (MAUp{Period: 5, Lookback: 1}).Buy("", t3bCloseKlines([]float64{10, 10.1, 10.2, 10.3, 10.4, 10.5}))},
		{"②len6下跌", (MAUp{Period: 5, Lookback: 1}).Buy("", t3bCloseKlines([]float64{10, 9.9, 9.8, 9.7, 9.6, 9.5}))},
		{"②len6全平slope恰等0", (MAUp{Period: 5, Lookback: 1}).Buy("", t3bCloseKlines([]float64{10, 10, 10, 10, 10, 10}))},
		{"②minSlope=2上涨不达标", (MAUp{Period: 5, Lookback: 1, MinSlope: 2}).Buy("", t3bCloseKlines([]float64{10, 10.1, 10.2, 10.3, 10.4, 10.5}))},
		{"②minSlope极小负下跌", (MAUp{Period: 5, Lookback: 1, MinSlope: -100}).Buy("", t3bCloseKlines([]float64{10, 9.9, 9.8, 9.7, 9.6, 9.5}))},
		{"②默认len24预热不足", (MAUp{}).Buy("", t3bCloseKlines(t3bRandWalk(3, 24)))},
		{"②默认len25随机", (MAUp{}).Buy("", t3bCloseKlines(t3bRandWalk(3, 25)))},

		// ③ RSI拐头 p=5（minLen=7）
		{"③len6预热不足", (RSI拐头{Period: 5}).Buy("", t3bCloseKlines([]float64{10, 9.8, 9.6, 9.4, 9.2, 9.0}))},
		{"③len7先跌后涨", (RSI拐头{Period: 5}).Buy("", t3bCloseKlines([]float64{10, 9.8, 9.6, 9.4, 9.2, 9.0, 9.5}))},
		{"③len7连续下跌", (RSI拐头{Period: 5}).Buy("", t3bCloseKlines([]float64{10, 9.8, 9.6, 9.4, 9.2, 9.0, 8.8}))},
		{"③len7全平RSI恰等", (RSI拐头{Period: 5}).Buy("", t3bCloseKlines([]float64{10, 10, 10, 10, 10, 10, 10}))},
		{"③默认len15预热不足", (RSI拐头{}).Buy("", t3bCloseKlines(t3bRandWalk(5, 15)))},
		{"③默认len16随机", (RSI拐头{}).Buy("", t3bCloseKlines(t3bRandWalk(5, 16)))},

		// ④ 回踩均线 period=5 touchDays=3（minLen=9）
		{"④len8预热不足", (A回踩N日均线{Period: 5}).Buy("", t3bCloseKlines([]float64{10, 10, 10, 10, 10, 9, 9, 9.2}))},
		{"④标准回踩", (A回踩N日均线{Period: 5}).Buy("", t3bCloseKlines([]float64{10, 10, 10, 10, 10, 9, 9, 9.2, 11}))},
		{"④today恰等昨收", (A回踩N日均线{Period: 5}).Buy("", t3bCloseKlines([]float64{10, 10, 10, 10, 10, 9, 9, 9.2, 9.2}))},
		{"④无触碰", (A回踩N日均线{Period: 5}).Buy("", t3bOHLC([]float64{10, 10, 10, 10, 10, 9, 9, 9.2, 11}, []float64{8, 8, 8, 8, 8, 10.1, 10.1, 10.1, 11}))},
		{"④touchDays=2无触碰", (A回踩N日均线{Period: 5, MinTouchDays: 2}).Buy("", t3bOHLC([]float64{10, 10, 10, 10, 10, 9, 9, 9.2, 11}, []float64{8, 8, 8, 8, 8, 10.1, 10.1, 10.1, 11}))},
		{"④support=10标准14根", (A回踩N日均线{Period: 5, SupportPeriod: 10}).Buy("", t3bCloseKlines([]float64{10, 10, 10, 10, 10, 10, 10, 10, 10, 10, 9, 9, 9.2, 11}))},
		{"④support=10len13预热不足", (A回踩N日均线{Period: 5, SupportPeriod: 10}).Buy("", t3bCloseKlines([]float64{10, 10, 10, 10, 10, 10, 10, 10, 10, 10, 9, 9, 9.2}))},
		{"④上升趋势不破支撑", (A回踩N日均线{Period: 5, SupportPeriod: 10}).Buy("", t3bOHLC([]float64{8, 8.2, 8.4, 8.6, 8.8, 9.0, 9.2, 9.4, 9.6, 9.8, 9.6, 9.5, 9.5, 9.8}, []float64{8, 8.1, 8.3, 8.5, 8.7, 8.9, 9.1, 9.3, 9.5, 9.7, 9.2, 9.4, 9.3, 9.8}))},
		{"④上升趋势破支撑", (A回踩N日均线{Period: 5, SupportPeriod: 10}).Buy("", t3bOHLC([]float64{8, 8.2, 8.4, 8.6, 8.8, 9.0, 9.2, 9.4, 9.6, 9.8, 9.6, 9.5, 9.5, 9.8}, []float64{8, 8.1, 8.3, 8.5, 8.7, 8.9, 9.1, 9.3, 9.5, 9.7, 8.0, 9.4, 9.3, 9.8}))},
		{"④默认归一随机", (A回踩N日均线{}).Buy("", t3bCloseKlines(t3bRandWalk(11, 80)))},

		// ⑤ 底部抬升 window=8（minLen=60）
		{"⑤len59预热不足", (A底部抬升{}).Buy("", t3bCloseKlines(t3bWave(1, 59)))},
		{"⑤len60波形", (A底部抬升{}).Buy("", t3bCloseKlines(t3bWave(1, 60)))},
		{"⑤len90波形", (A底部抬升{}).Buy("", t3bCloseKlines(t3bWave(1, 90)))},
		{"⑤无漂移谷值持平", (A底部抬升{}).Buy("", t3bCloseKlines(t3bWaveFlat(90)))},
		{"⑤window过大仅1谷", (A底部抬升{Window: 30}).Buy("", t3bCloseKlines(t3bWave(1, 90)))},

		// ⑥ 近N日涨幅 days=5 max=15（minLen=6）
		{"⑥len5预热不足", (A近N日涨幅小于{Days: 5, Max: 15}).Buy("", t3bCloseKlines([]float64{10, 10, 10, 10, 10}))},
		{"⑥涨幅15恰等", (A近N日涨幅小于{Days: 5, Max: 15}).Buy("", t3bCloseKlines([]float64{10, 10, 10, 10, 10, 11.5}))},
		{"⑥涨幅10小于", (A近N日涨幅小于{Days: 5, Max: 15}).Buy("", t3bCloseKlines([]float64{10, 10, 10, 10, 10, 11}))},
		{"⑥涨幅20不小于", (A近N日涨幅小于{Days: 5, Max: 15}).Buy("", t3bCloseKlines([]float64{10, 10, 10, 10, 10, 12}))},
		{"⑥起点收盘0", (A近N日涨幅小于{Days: 5, Max: 15}).Buy("", t3bCloseKlines([]float64{0, 10, 10, 10, 10, 11}))},
		{"⑥默认归一随机", (A近N日涨幅小于{}).Buy("", t3bCloseKlines(t3bRandWalk(9, 6)))},
		{"⑥days=3涨幅边界", (A近N日涨幅小于{Days: 3, Max: 5}).Buy("", t3bCloseKlines([]float64{10, 10, 10, 10.5}))},
	}
	for _, e := range edge {
		t.Logf("[T3B-EDGE] %-32s => %v", e.label, e.got)
	}
}
