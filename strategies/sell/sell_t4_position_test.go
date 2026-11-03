package sell

import (
	"testing"
	"time"

	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/lib/extend"
	f "github.com/injoyai/strategy-tail/strategies/factor"
	"github.com/injoyai/tdx/v2/protocol"
)

// T4 持仓批次（价格类）行为快照（台账附录 C：risk_profit / risk_fixed_tp /
// risk_fixed_sl / risk_atr_stop / risk_atr_takeprofit / risk_trailing_takeprofit /
// risk_trailing_stop / risk_takeprofit_stoploss）。
// 断言先按旧实现固定，迁移到 strategies/rule 内核后必须原样通过；
// helper 一律 t4 前缀，避免与并行迁移组的同名符号冲突。
// 价格类组件带逐前缀对拍：以旧实现逐语句内联为 oracle，对随机游走与整形
// 行情的每个前缀长度断言门面与 oracle 一致，覆盖收益率无效、阈值线无效、
// 峰值回扫 break、定位失败与 ATR 预热等边界。

type t4Bar struct{ O, H, L, C float64 }

// t4K 用给定时刻构造单根 K 线，量恒定。
func t4K(t time.Time, b t4Bar) *extend.Kline {
	return &extend.Kline{
		Unix:  t.Unix(),
		Kline: &protocol.Kline{Time: t, Open: b.O, High: b.H, Low: b.L, Close: b.C, Volume: 100},
	}
}

// t4Series 按自然日（午夜）为每根 K 线编号。
func t4Series(bars []t4Bar) extend.Klines { return t4SeriesClock(bars, 0, 0, 0) }

// t4SeriesClock 同上，但把当日时刻固定为给定时钟。
func t4SeriesClock(bars []t4Bar, hour, min, sec int) extend.Klines {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.Local)
	ks := make(extend.Klines, len(bars))
	for i, b := range bars {
		day := base.AddDate(0, 0, i)
		ks[i] = t4K(time.Date(day.Year(), day.Month(), day.Day(), hour, min, sec, 0, time.Local), b)
	}
	return ks
}

// t4Flats n 根四价同值 K 线。
func t4Flats(n int, c float64) []t4Bar {
	out := make([]t4Bar, n)
	for i := range out {
		out[i] = t4Bar{O: c, H: c, L: c, C: c}
	}
	return out
}

// t4Walk 随机游走 OHLC：固定种子可复现，保证 high>=max(open,close)>=min>=low。
func t4Walk(seed int64, n int) []t4Bar {
	rng := int64(seed)
	rnd := func(mod int) int {
		rng = rng*6364136223846793005 + 1442695040888963407
		return int(uint64(rng)>>33) % mod
	}
	out := make([]t4Bar, n)
	c := 10.0
	for i := 0; i < n; i++ {
		c += float64(rnd(301)-150) / 1000.0
		if c < 2.0 {
			c = 2.0
		}
		o := c - float64(rnd(101))/1000.0 + 0.05
		hi := max(o, c) + float64(rnd(151))/1000.0
		lo := min(o, c) - float64(rnd(151))/1000.0
		if lo < 0.1 {
			lo = 0.1
		}
		out[i] = t4Bar{O: o, H: hi, L: lo, C: c}
	}
	return out
}

// t4FromCloses 四价同值序列（仅供 ATR 等只用单价的组件）。
func t4FromCloses(closes []float64) []t4Bar {
	out := make([]t4Bar, len(closes))
	for i, c := range closes {
		out[i] = t4Bar{O: c, H: c, L: c, C: c}
	}
	return out
}

// t4BuyAt 以第 idx 根的日期为买入日构造买单。
func t4BuyAt(ks extend.Klines, idx int, price float64) core.Buy {
	return core.Buy{Code: "sh600000", Time: ks[idx].Time, Price: price}
}

// t4CrossCheck 逐前缀对拍门面与 oracle，返回 oracle 触发的前缀数。
func t4CrossCheck(t *testing.T, name string, full extend.Klines, buy core.Buy,
	oracle func(string, extend.Klines, core.Buy) bool, facade func(string, extend.Klines, core.Buy) bool) int {
	t.Helper()
	triggered := 0
	for l := 0; l <= len(full); l++ {
		want := oracle("sh600000", full[:l], buy)
		if got := facade("sh600000", full[:l], buy); got != want {
			t.Fatalf("%s 前缀 %d 根: got %v want %v", name, l, got, want)
		}
		if want {
			triggered++
		}
	}
	return triggered
}

// ---- risk_profit（A盈利）----

// t4盈利Oracle 迁移前 A盈利.Sell 内联（逐语句等价）。
func t4盈利Oracle(s A盈利, code string, dks extend.Klines, buy core.Buy) bool {
	pct := float64(s)
	if pct == 0 {
		pct = 0.05
	}
	n := len(dks)
	if n == 0 {
		return false
	}
	buyPrice := buy.Price
	if buyPrice <= 0 {
		return false
	}
	return (dks[n-1].Close-buyPrice)/buyPrice >= pct
}

func TestT4A盈利对拍(t *testing.T) {
	// 边界：末收恰等 +5% 触发，+6% 不触发；买价非正/空行情不触发。
	bars := t4Flats(5, 10.0)
	bars[4] = t4Bar{O: 10.0, H: 10.5, L: 10.0, C: 10.5}
	full := t4Series(bars)
	buy := t4BuyAt(full, 0, 10.0)

	if !(A盈利(0.05)).Sell("sh600000", full, buy) {
		t.Fatal("A盈利 +5% 恰等应触发")
	}
	if (A盈利(0.06)).Sell("sh600000", full, buy) {
		t.Fatal("A盈利 +6% 不应触发")
	}
	if (A盈利(0.05)).Sell("sh600000", full, core.Buy{Code: "sh600000", Time: full[0].Time, Price: 0.0}) {
		t.Fatal("A盈利 买价非正不应触发")
	}
	if (A盈利(0.05)).Sell("sh600000", nil, buy) {
		t.Fatal("A盈利 空行情不应触发")
	}

	seeds := []int64{1, 7, 42, 99}
	buyIdxSet := []int{0, 5, 12, 23}
	priceSet := []float64{10.0, 0.0, 0.5}
	pctSet := []float64{0, 0.02, 0.05, 0.1}

	fixtures := make([]extend.Klines, 0, len(seeds)+1)
	for _, seed := range seeds {
		fixtures = append(fixtures, t4Series(t4Walk(seed, 24)))
	}
	det := t4Flats(24, 10.0)
	det[23] = t4Bar{O: 10.0, H: 10.6, L: 10.0, C: 10.6}
	fixtures = append(fixtures, t4Series(det))

	triggered := 0
	for _, fx := range fixtures {
		for _, bi := range buyIdxSet {
			for _, price := range priceSet {
				b := t4BuyAt(fx, bi, price)
				for _, pct := range pctSet {
					s := A盈利(pct)
					triggered += t4CrossCheck(t, "A盈利", fx, b,
						func(c string, d extend.Klines, x core.Buy) bool { return t4盈利Oracle(s, c, d, x) },
						s.Sell)
				}
			}
		}
	}
	if triggered == 0 {
		t.Fatal("对拍夹具未覆盖任何触发样例，oracle 可能失效")
	}
}

// ---- risk_fixed_tp（A固定止盈）----

// t4固定止盈Oracle 迁移前 A固定止盈.Sell 内联（逐语句等价）。
func t4固定止盈Oracle(s A固定止盈, code string, dks extend.Klines, buy core.Buy) bool {
	pct := float64(s)
	if pct == 0 {
		pct = 0.05
	}
	n := len(dks)
	if n == 0 {
		return false
	}
	buyPrice := buy.Price
	if buyPrice <= 0 {
		return false
	}
	return (dks[n-1].Close-buyPrice)/buyPrice >= pct
}

func TestT4A固定止盈对拍(t *testing.T) {
	bars := t4Flats(5, 10.0)
	bars[4] = t4Bar{O: 10.0, H: 10.5, L: 10.0, C: 10.5}
	full := t4Series(bars)
	buy := t4BuyAt(full, 0, 10.0)

	if !(A固定止盈(0.05)).Sell("sh600000", full, buy) {
		t.Fatal("A固定止盈 +5% 恰等应触发")
	}
	if (A固定止盈(0.06)).Sell("sh600000", full, buy) {
		t.Fatal("A固定止盈 +6% 不应触发")
	}
	if (A固定止盈(0.05)).Sell("sh600000", nil, buy) {
		t.Fatal("A固定止盈 空行情不应触发")
	}

	seeds := []int64{1, 7, 42, 99}
	buyIdxSet := []int{0, 5, 12, 23}
	priceSet := []float64{10.0, 0.0, 0.5}
	pctSet := []float64{0, 0.02, 0.05, 0.1}

	fixtures := make([]extend.Klines, 0, len(seeds)+1)
	for _, seed := range seeds {
		fixtures = append(fixtures, t4Series(t4Walk(seed, 24)))
	}
	det := t4Flats(24, 10.0)
	det[23] = t4Bar{O: 10.0, H: 10.6, L: 10.0, C: 10.6}
	fixtures = append(fixtures, t4Series(det))

	triggered := 0
	for _, fx := range fixtures {
		for _, bi := range buyIdxSet {
			for _, price := range priceSet {
				b := t4BuyAt(fx, bi, price)
				for _, pct := range pctSet {
					s := A固定止盈(pct)
					triggered += t4CrossCheck(t, "A固定止盈", fx, b,
						func(c string, d extend.Klines, x core.Buy) bool { return t4固定止盈Oracle(s, c, d, x) },
						s.Sell)
				}
			}
		}
	}
	if triggered == 0 {
		t.Fatal("对拍夹具未覆盖任何触发样例，oracle 可能失效")
	}
}

// ---- risk_fixed_sl（A固定止损）----

// t4固定止损Oracle 迁移前 A固定止损.Sell 内联（逐语句等价）。
func t4固定止损Oracle(s A固定止损, code string, dks extend.Klines, buy core.Buy) bool {
	pct := s.Pct
	if pct == 0 {
		pct = 0.05
	}
	n := len(dks)
	if n == 0 {
		return false
	}
	buyPrice := buy.Price
	if buyPrice <= 0 {
		return false
	}
	return (dks[n-1].Close-buyPrice)/buyPrice <= -pct
}

func TestT4A固定止损对拍(t *testing.T) {
	// 边界：末收 9500 → -5% 恰等触发，-6% 阈值不触发。
	bars := t4Flats(5, 10.0)
	bars[4] = t4Bar{O: 10.0, H: 10.0, L: 9.5, C: 9.5}
	full := t4Series(bars)
	buy := t4BuyAt(full, 0, 10.0)

	if !(A固定止损{Pct: 0.05}).Sell("sh600000", full, buy) {
		t.Fatal("A固定止损 -5% 恰等应触发")
	}
	if (A固定止损{Pct: 0.06}).Sell("sh600000", full, buy) {
		t.Fatal("A固定止损 -5% 未达 -6% 不应触发")
	}
	if (A固定止损{Pct: 0.05}).Sell("sh600000", full, core.Buy{Code: "sh600000", Time: full[0].Time, Price: 0.0}) {
		t.Fatal("A固定止损 买价非正不应触发")
	}

	seeds := []int64{1, 7, 42, 99}
	buyIdxSet := []int{0, 5, 12, 23}
	priceSet := []float64{10.0, 0.0, 0.5}
	pctSet := []float64{0, 0.02, 0.05, 0.1}

	fixtures := make([]extend.Klines, 0, len(seeds)+1)
	for _, seed := range seeds {
		fixtures = append(fixtures, t4Series(t4Walk(seed, 24)))
	}
	det := t4Flats(24, 10.0)
	det[23] = t4Bar{O: 10.0, H: 10.0, L: 9.4, C: 9.4}
	fixtures = append(fixtures, t4Series(det))

	triggered := 0
	for _, fx := range fixtures {
		for _, bi := range buyIdxSet {
			for _, price := range priceSet {
				b := t4BuyAt(fx, bi, price)
				for _, pct := range pctSet {
					s := A固定止损{Pct: pct}
					triggered += t4CrossCheck(t, "A固定止损", fx, b,
						func(c string, d extend.Klines, x core.Buy) bool { return t4固定止损Oracle(s, c, d, x) },
						s.Sell)
				}
			}
		}
	}
	if triggered == 0 {
		t.Fatal("对拍夹具未覆盖任何触发样例，oracle 可能失效")
	}
}

// ---- risk_atr_stop（A_ATR止损）----

// t4ATR止损Oracle 迁移前 A_ATR止损.Sell 内联（逐语句等价）。
func t4ATR止损Oracle(s A_ATR止损, code string, dks extend.Klines, buy core.Buy) bool {
	period := s.Period
	if period == 0 {
		period = 14
	}
	mul := s.Multiple
	if mul == 0 {
		mul = 2.5
	}
	n := len(dks)
	if n == 0 {
		return false
	}
	buyPrice := buy.Price
	if buyPrice <= 0 {
		return false
	}
	atr := f.ATR值(period).Value(code, dks)
	return dks[n-1].Close < buyPrice-mul*atr
}

func TestT4A_ATR止损对拍(t *testing.T) {
	// 边界：14×10000+8000，period14 需 15 根，l=15 触发 / l=14 预热不触发。
	closes := t3cCloses(15, 10.0, 8.0)
	full := t4Series(t4FromCloses(closes))
	buy := t4BuyAt(full, 0, 10.0)

	if !(A_ATR止损{Period: 14}).Sell("sh600000", full, buy) {
		t.Fatal("A_ATR止损 period14 默认倍率 2.5，l=15 应触发")
	}
	if (A_ATR止损{Period: 14}).Sell("sh600000", full[:14], buy) {
		t.Fatal("A_ATR止损 l=14 预热不足不应触发")
	}
	if !(A_ATR止损{Period: 5, Multiple: 1.0}).Sell("sh600000", full, buy) {
		t.Fatal("A_ATR止损 period5 mul1.0 线 9600 应触发")
	}
	if (A_ATR止损{Period: 14}).Sell("sh600000", full, core.Buy{Code: "sh600000", Time: full[0].Time, Price: 0.0}) {
		t.Fatal("A_ATR止损 买价非正不应触发")
	}

	seeds := []int64{1, 7, 42, 99}
	buyIdxSet := []int{0, 5, 12, 23}
	priceSet := []float64{10.0, 0.0, 0.5}
	periodSet := []int{0, 5, 14}
	mulSet := []float64{0, 1.0, 2.5}

	fixtures := make([]extend.Klines, 0, len(seeds)+1)
	for _, seed := range seeds {
		fixtures = append(fixtures, t4Series(t4Walk(seed, 24)))
	}
	fixtures = append(fixtures, t4Series(t4FromCloses(t3cCloses(24, 10.0, 8.0))))

	triggered := 0
	for _, fx := range fixtures {
		for _, bi := range buyIdxSet {
			for _, price := range priceSet {
				b := t4BuyAt(fx, bi, price)
				for _, period := range periodSet {
					for _, mul := range mulSet {
						s := A_ATR止损{Period: period, Multiple: mul}
						triggered += t4CrossCheck(t, "A_ATR止损", fx, b,
							func(c string, d extend.Klines, x core.Buy) bool { return t4ATR止损Oracle(s, c, d, x) },
							s.Sell)
					}
				}
			}
		}
	}
	if triggered == 0 {
		t.Fatal("对拍夹具未覆盖任何触发样例，oracle 可能失效")
	}
}

// ---- risk_atr_takeprofit（A_ATR止盈）----

// t4ATR止盈Oracle 迁移前 A_ATR止盈.Sell 内联（逐语句等价）。
func t4ATR止盈Oracle(s A_ATR止盈, code string, dks extend.Klines, buy core.Buy) bool {
	period := s.Period
	if period == 0 {
		period = 14
	}
	mul := s.Multiple
	if mul == 0 {
		mul = 2.0
	}
	n := len(dks)
	if n == 0 {
		return false
	}
	buyPrice := buy.Price
	if buyPrice <= 0 {
		return false
	}
	atr := f.ATR值(period).Value(code, dks)
	return dks[n-1].Close >= buyPrice+mul*atr
}

func TestT4A_ATR止盈对拍(t *testing.T) {
	// 边界：14×10000+12000，l=15 触发 / l=14 预热不触发。
	closes := t3cCloses(15, 10.0, 12.0)
	full := t4Series(t4FromCloses(closes))
	buy := t4BuyAt(full, 0, 10.0)

	if !(A_ATR止盈{Period: 14}).Sell("sh600000", full, buy) {
		t.Fatal("A_ATR止盈 period14 默认倍率 2.0，l=15 应触发")
	}
	if (A_ATR止盈{Period: 14}).Sell("sh600000", full[:14], buy) {
		t.Fatal("A_ATR止盈 l=14 预热不足不应触发")
	}
	if !(A_ATR止盈{Period: 5, Multiple: 1.0}).Sell("sh600000", full, buy) {
		t.Fatal("A_ATR止盈 period5 mul1.0 线 10400 应触发")
	}
	if (A_ATR止盈{Period: 14}).Sell("sh600000", full, core.Buy{Code: "sh600000", Time: full[0].Time, Price: 0.0}) {
		t.Fatal("A_ATR止盈 买价非正不应触发")
	}

	seeds := []int64{1, 7, 42, 99}
	buyIdxSet := []int{0, 5, 12, 23}
	priceSet := []float64{10.0, 0.0, 0.5}
	periodSet := []int{0, 5, 14}
	mulSet := []float64{0, 1.0, 2.0}

	fixtures := make([]extend.Klines, 0, len(seeds)+1)
	for _, seed := range seeds {
		fixtures = append(fixtures, t4Series(t4Walk(seed, 24)))
	}
	fixtures = append(fixtures, t4Series(t4FromCloses(t3cCloses(24, 10.0, 12.0))))

	triggered := 0
	for _, fx := range fixtures {
		for _, bi := range buyIdxSet {
			for _, price := range priceSet {
				b := t4BuyAt(fx, bi, price)
				for _, period := range periodSet {
					for _, mul := range mulSet {
						s := A_ATR止盈{Period: period, Multiple: mul}
						triggered += t4CrossCheck(t, "A_ATR止盈", fx, b,
							func(c string, d extend.Klines, x core.Buy) bool { return t4ATR止盈Oracle(s, c, d, x) },
							s.Sell)
					}
				}
			}
		}
	}
	if triggered == 0 {
		t.Fatal("对拍夹具未覆盖任何触发样例，oracle 可能失效")
	}
}

// ---- risk_trailing_takeprofit（A移动止盈）----

// t4移动止盈Oracle 迁移前 A移动止盈.Sell 内联（逐语句等价）。
func t4移动止盈Oracle(s A移动止盈, code string, dks extend.Klines, buy core.Buy) bool {
	activate := s.ActivateProfitPct
	if activate == 0 {
		activate = 0.05
	}
	retreat := s.RetreatPct
	if retreat == 0 {
		retreat = 0.5
	}
	n := len(dks)
	if n == 0 {
		return false
	}
	buyPrice := buy.Price
	if buyPrice <= 0 {
		return false
	}
	maxProfit := 0.0
	for i := n - 1; i >= 0; i-- {
		if dks[i].Time.Before(buy.Time) {
			break
		}
		profit := (dks[i].High - buyPrice) / buyPrice
		if profit > maxProfit {
			maxProfit = profit
		}
	}
	if maxProfit < activate {
		return false
	}
	current := (dks[n-1].Close - buyPrice) / buyPrice
	return current <= maxProfit*(1-retreat)
}

func TestT4A移动止盈对拍(t *testing.T) {
	// 边界：峰值含买入日当天；激活 5% 回吐 50%，cur 恰等回吐线触发。
	bars := []t4Bar{
		{O: 10.0, H: 12.0, L: 10.0, C: 10.5},
		{O: 10.5, H: 11.0, L: 10.0, C: 11.0},
	}
	full := t4Series(bars)
	buy := t4BuyAt(full, 0, 10.0)

	if !(A移动止盈{ActivateProfitPct: 0.05, RetreatPct: 0.5}).Sell("sh600000", full[:1], buy) {
		t.Fatal("A移动止盈 l=1 峰值含买入日当天，cur 0.05<=线 0.1 应触发")
	}
	if !(A移动止盈{ActivateProfitPct: 0.05, RetreatPct: 0.5}).Sell("sh600000", full, buy) {
		t.Fatal("A移动止盈 l=2 cur 0.1 恰等回吐线 0.1 应触发")
	}
	if (A移动止盈{ActivateProfitPct: 0.25, RetreatPct: 0.5}).Sell("sh600000", full, buy) {
		t.Fatal("A移动止盈 峰值 0.2<激活 0.25 不应触发")
	}
	if (A移动止盈{ActivateProfitPct: 0.05, RetreatPct: 0.8}).Sell("sh600000", full, buy) {
		t.Fatal("A移动止盈 回吐线 0.04，cur 0.1 不应触发")
	}
	if (A移动止盈{ActivateProfitPct: 0.05, RetreatPct: 0.5}).Sell("sh600000", full, t4BuyAt(full, 1, 10.0)) {
		t.Fatal("A移动止盈 于 idx1 买入回扫仅 0.1，cur 0.1>线 0.05 不应触发")
	}

	seeds := []int64{1, 7, 42, 99}
	buyIdxSet := []int{0, 5, 12, 23}
	priceSet := []float64{10.0, 0.0, 0.5}
	activateSet := []float64{0, 0.05, 0.2}
	retreatSet := []float64{0, 0.5, 0.8}

	fixtures := make([]extend.Klines, 0, len(seeds)+1)
	for _, seed := range seeds {
		fixtures = append(fixtures, t4Series(t4Walk(seed, 24)))
	}
	det := t4Flats(24, 10.0)
	det[0] = t4Bar{O: 10.0, H: 12.0, L: 10.0, C: 10.5}
	det[1] = t4Bar{O: 10.5, H: 11.0, L: 10.0, C: 11.0}
	fixtures = append(fixtures, t4Series(det))

	triggered := 0
	for _, fx := range fixtures {
		for _, bi := range buyIdxSet {
			for _, price := range priceSet {
				b := t4BuyAt(fx, bi, price)
				for _, activate := range activateSet {
					for _, retreat := range retreatSet {
						s := A移动止盈{ActivateProfitPct: activate, RetreatPct: retreat}
						triggered += t4CrossCheck(t, "A移动止盈", fx, b,
							func(c string, d extend.Klines, x core.Buy) bool { return t4移动止盈Oracle(s, c, d, x) },
							s.Sell)
					}
				}
			}
		}
	}
	if triggered == 0 {
		t.Fatal("对拍夹具未覆盖任何触发样例，oracle 可能失效")
	}
}

// ---- risk_trailing_stop（A追踪止损）----

// t4追踪止损Oracle 迁移前 A追踪止损.Sell 内联（逐语句等价）。
func t4追踪止损Oracle(s A追踪止损, code string, dks extend.Klines, buy core.Buy) bool {
	if s.Drawdown <= 0 {
		return false
	}
	n := len(dks)
	if n == 0 {
		return false
	}
	buyPrice := buy.Price
	if buyPrice <= 0 {
		return false
	}
	buyIdx := -1
	buyDate := buy.Time.Format("2006-01-02")
	for i, k := range dks {
		if k.Time.Format("2006-01-02") == buyDate {
			buyIdx = i
			break
		}
	}
	if buyIdx < 0 {
		return false
	}
	peak := 0.0
	for i := buyIdx; i < n; i++ {
		c := dks[i].Close
		if c > peak {
			peak = c
		}
	}
	if peak <= 0 {
		return false
	}
	current := dks[n-1].Close
	return (peak-current)/peak >= s.Drawdown
}

func TestT4A追踪止损对拍(t *testing.T) {
	// 边界：10000→12500→10000，回撤恰等 20% 触发；非正回撤门面直接 false。
	closes := []float64{10.0, 12.5, 10.0}
	full := t4Series(t4FromCloses(closes))
	buy := t4BuyAt(full, 0, 10.0)

	if !(A追踪止损{Drawdown: 0.2}).Sell("sh600000", full, buy) {
		t.Fatal("A追踪止损 回撤 20% 恰等应触发")
	}
	if (A追踪止损{Drawdown: 0.21}).Sell("sh600000", full, buy) {
		t.Fatal("A追踪止损 回撤 20% 未达 21% 不应触发")
	}
	if (A追踪止损{}).Sell("sh600000", full, buy) {
		t.Fatal("A追踪止损 回撤 0 门面应直接 false")
	}
	if (A追踪止损{Drawdown: -0.1}).Sell("sh600000", full, buy) {
		t.Fatal("A追踪止损 负回撤门面应直接 false")
	}
	if (A追踪止损{Drawdown: 0.2}).Sell("sh600000", full, core.Buy{Code: "sh600000", Time: full[0].Time, Price: 0.0}) {
		t.Fatal("A追踪止损 买价非正不应触发")
	}

	seeds := []int64{1, 7, 42, 99}
	buyIdxSet := []int{0, 5, 12, 23}
	priceSet := []float64{10.0, 0.0}
	ddSet := []float64{0.02, 0.05, 0.2}

	fixtures := make([]extend.Klines, 0, len(seeds)+1)
	for _, seed := range seeds {
		fixtures = append(fixtures, t4Series(t4Walk(seed, 24)))
	}
	det := t3cCloses(24, 10.0, 10.0)
	det[1] = 12.5
	fixtures = append(fixtures, t4Series(t4FromCloses(det)))

	triggered := 0
	for _, fx := range fixtures {
		for _, bi := range buyIdxSet {
			for _, price := range priceSet {
				b := t4BuyAt(fx, bi, price)
				for _, dd := range ddSet {
					s := A追踪止损{Drawdown: dd}
					triggered += t4CrossCheck(t, "A追踪止损", fx, b,
						func(c string, d extend.Klines, x core.Buy) bool { return t4追踪止损Oracle(s, c, d, x) },
						s.Sell)
				}
			}
		}
	}
	if triggered == 0 {
		t.Fatal("对拍夹具未覆盖任何触发样例，oracle 可能失效")
	}
}

// ---- risk_takeprofit_stoploss（A止盈止损）----

// t4止盈止损Oracle 迁移前 A止盈止损.Sell 内联（逐语句等价）。
func t4止盈止损Oracle(s A止盈止损, code string, dks extend.Klines, buy core.Buy) bool {
	if s.TakeProfit <= 0 && s.StopLoss <= 0 {
		return false
	}
	n := len(dks)
	if n == 0 {
		return false
	}
	buyPrice := buy.Price
	if buyPrice <= 0 {
		return false
	}
	buyIdx := -1
	buyDate := buy.Time.Format("2006-01-02")
	for i, k := range dks {
		if k.Time.Format("2006-01-02") == buyDate {
			buyIdx = i
			break
		}
	}
	if buyIdx < 0 {
		return false
	}
	for i := buyIdx + 1; i < n; i++ {
		closePrice := dks[i].Close
		rate := (closePrice - buyPrice) / buyPrice
		if s.TakeProfit > 0 && rate >= s.TakeProfit {
			return true
		}
		if s.StopLoss > 0 && rate <= -s.StopLoss {
			return true
		}
	}
	return false
}

func TestT4A止盈止损对拍(t *testing.T) {
	// 边界：买入日当天不计，次日 +10% 恰等触发；止损侧 -5% 恰等触发。
	bars := []t4Bar{
		{O: 10.0, H: 10.0, L: 10.0, C: 10.0},
		{O: 10.0, H: 10.0, L: 10.0, C: 10.0},
		{O: 10.0, H: 10.0, L: 10.0, C: 12.0},
		{O: 12.0, H: 12.0, L: 11.0, C: 11.0},
	}
	full := t4Series(bars)
	buy := t4BuyAt(full, 2, 10.0)

	if (A止盈止损{TakeProfit: 0.1}).Sell("sh600000", full[:3], buy) {
		t.Fatal("A止盈止损 买入日当天不计，l=3 不应触发")
	}
	if !(A止盈止损{TakeProfit: 0.1}).Sell("sh600000", full, buy) {
		t.Fatal("A止盈止损 次日 +10% 恰等应触发")
	}
	if (A止盈止损{}).Sell("sh600000", full, buy) {
		t.Fatal("A止盈止损 止盈止损均非正不应触发")
	}

	slBars := []t4Bar{
		{O: 10.0, H: 10.0, L: 10.0, C: 10.0},
		{O: 10.0, H: 10.0, L: 10.0, C: 10.0},
		{O: 10.0, H: 10.0, L: 10.0, C: 12.0},
		{O: 12.0, H: 12.0, L: 9.5, C: 9.5},
	}
	slFull := t4Series(slBars)
	slBuy := t4BuyAt(slFull, 2, 10.0)
	if !(A止盈止损{StopLoss: 0.05}).Sell("sh600000", slFull, slBuy) {
		t.Fatal("A止盈止损 次日 -5% 恰等应触发")
	}
	if !(A止盈止损{TakeProfit: -0.1, StopLoss: 0.05}).Sell("sh600000", slFull, slBuy) {
		t.Fatal("A止盈止损 止盈侧关闭时止损侧仍应触发")
	}

	seeds := []int64{1, 7, 42, 99}
	buyIdxSet := []int{0, 5, 12, 23}
	priceSet := []float64{10.0, 0.0}
	tpSlSet := [][2]float64{{0.1, 0.05}, {0.1, 0}, {0, 0.05}, {0, 0}, {-0.1, 0.05}}

	fixtures := make([]extend.Klines, 0, len(seeds)+1)
	for _, seed := range seeds {
		fixtures = append(fixtures, t4Series(t4Walk(seed, 24)))
	}
	det := t4Flats(24, 10.0)
	det[1] = t4Bar{O: 10.0, H: 12.0, L: 10.0, C: 12.0}
	fixtures = append(fixtures, t4Series(det))

	triggered := 0
	for _, fx := range fixtures {
		for _, bi := range buyIdxSet {
			for _, price := range priceSet {
				b := t4BuyAt(fx, bi, price)
				for _, tpSl := range tpSlSet {
					s := A止盈止损{TakeProfit: tpSl[0], StopLoss: tpSl[1]}
					triggered += t4CrossCheck(t, "A止盈止损", fx, b,
						func(c string, d extend.Klines, x core.Buy) bool { return t4止盈止损Oracle(s, c, d, x) },
						s.Sell)
				}
			}
		}
	}
	if triggered == 0 {
		t.Fatal("对拍夹具未覆盖任何触发样例，oracle 可能失效")
	}
}
