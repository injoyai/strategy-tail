package sell

import (
	"testing"
	"time"

	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/lib/extend"
	f "github.com/injoyai/strategy-tail/strategies/factor"
)

// T4 持仓批次（时序/定位类）行为快照（台账附录 C：risk_holding_days /
// risk_holding_days_tail / risk_break_buy_price / risk_no_gap_up /
// risk_break_buy_open / sell_macd_down_after_buy）。
// 断言先按旧实现固定，迁移到 strategies/rule 内核后必须原样通过；
// oracle 逐语句内联旧实现，对每个前缀长度断言门面与 oracle 一致。
// 复用同包 t3cWalk/t3cWave/t3cCloses 与 T4 价格类 helper（t4Series 等），
// 其中买入日定位口径各组件不同（顺序/逆序日期串，计划第 4 节不统一）。

// ---- risk_holding_days（A持仓N天）----

// t4持仓Oracle 迁移前 A持仓N天.Sell 内联（逐语句等价）。
func t4持仓Oracle(daysIn int, code string, dks extend.Klines, buy core.Buy) bool {
	days := daysIn
	if days == 0 {
		days = 5
	}
	n := len(dks)
	if n == 0 {
		return false
	}
	buyIdx := -1
	for i, k := range dks {
		if !k.Time.Before(buy.Time) {
			buyIdx = i
			break
		}
	}
	if buyIdx < 0 {
		return false
	}
	return n-buyIdx-1 >= days
}

func TestT4A持仓N天对拍(t *testing.T) {
	// 边界：8 根 flats，买入 idx2，持仓恰等 5 天触发；4 天不触发。
	full := t4Series(t4Flats(8, 10.0))
	buy := t4BuyAt(full, 2, 10.0)

	if !(A持仓N天{Days: 5}).Sell("sh600000", full, buy) {
		t.Fatal("A持仓N天 持仓恰等 5 天应触发")
	}
	if (A持仓N天{Days: 5}).Sell("sh600000", full[:7], buy) {
		t.Fatal("A持仓N天 持仓 4 天不应触发")
	}
	if !(A持仓N天{}).Sell("sh600000", full, buy) {
		t.Fatal("A持仓N天 默认 5 天应触发")
	}
	if (A持仓N天{Days: 6}).Sell("sh600000", full, buy) {
		t.Fatal("A持仓N天 持仓 5 天未达 6 天不应触发")
	}

	seeds := []int64{1, 7, 42, 99}
	buyIdxSet := []int{0, 5, 12, 23}
	daysSet := []int{0, 1, 3, 5}

	fixtures := make([]extend.Klines, 0, len(seeds)+1)
	for _, seed := range seeds {
		fixtures = append(fixtures, t4Series(t4Walk(seed, 24)))
	}
	fixtures = append(fixtures, t4Series(t4Flats(24, 10.0)))

	triggered := 0
	for _, fx := range fixtures {
		for _, bi := range buyIdxSet {
			b := t4BuyAt(fx, bi, 10.0)
			for _, days := range daysSet {
				s := A持仓N天{Days: days}
				triggered += t4CrossCheck(t, "A持仓N天", fx, b,
					func(c string, d extend.Klines, x core.Buy) bool { return t4持仓Oracle(days, c, d, x) },
					s.Sell)
			}
		}
	}
	if triggered == 0 {
		t.Fatal("对拍夹具未覆盖任何触发样例，oracle 可能失效")
	}
}

// ---- risk_holding_days_tail（A持仓N天尾盘）----

// t4尾盘Oracle 迁移前 A持仓N天尾盘.Sell 内联（逐语句等价）。
func t4尾盘Oracle(s A持仓N天尾盘, code string, dks extend.Klines, buy core.Buy) bool {
	days := s.Days
	if days == 0 {
		days = 1
	}
	sellTime := s.Time
	if sellTime == "" {
		sellTime = "14:55:00"
	}
	n := len(dks)
	if n == 0 {
		return false
	}
	buyIdx := -1
	for i, k := range dks {
		if !k.Time.Before(buy.Time) {
			buyIdx = i
			break
		}
	}
	if buyIdx < 0 || n-buyIdx-1 < days {
		return false
	}
	tod := dks[n-1].Time.Format(time.TimeOnly)
	if tod == "00:00:00" {
		return true
	}
	return tod >= sellTime
}

func TestT4A持仓N天尾盘对拍(t *testing.T) {
	// 边界：午夜序列（退化日线）持仓达 1 天即触发；分钟序列按字符串比较。
	mid := t4Series(t4Flats(4, 10.0))
	midBuy := t4BuyAt(mid, 0, 10.0)

	if (A持仓N天尾盘{Days: 1}).Sell("sh600000", mid[:1], midBuy) {
		t.Fatal("A持仓N天尾盘 持仓 0 天不应触发")
	}
	if !(A持仓N天尾盘{Days: 1}).Sell("sh600000", mid[:2], midBuy) {
		t.Fatal("A持仓N天尾盘 00:00:00 退化日线应触发")
	}
	if !(A持仓N天尾盘{Days: 1, Time: "14:30:00"}).Sell("sh600000", mid[:2], midBuy) {
		t.Fatal("A持仓N天尾盘 午夜特判先于字符串比较应触发")
	}
	if !(A持仓N天尾盘{Days: 3}).Sell("sh600000", mid[:4], midBuy) {
		t.Fatal("A持仓N天尾盘 持仓 3 天+午夜应触发")
	}
	if (A持仓N天尾盘{Days: 3}).Sell("sh600000", mid[:3], midBuy) {
		t.Fatal("A持仓N天尾盘 持仓 2 天不应触发")
	}

	clk := t4SeriesClock(t4Flats(2, 10.0), 14, 55, 0)
	clkBuy := t4BuyAt(clk, 0, 10.0)
	if !(A持仓N天尾盘{Days: 1, Time: "14:55:00"}).Sell("sh600000", clk, clkBuy) {
		t.Fatal("A持仓N天尾盘 14:55:00 恰等应触发")
	}

	clk2 := t4SeriesClock(t4Flats(2, 10.0), 14, 54, 59)
	clk2Buy := t4BuyAt(clk2, 0, 10.0)
	if (A持仓N天尾盘{Days: 1, Time: "14:55:00"}).Sell("sh600000", clk2, clk2Buy) {
		t.Fatal("A持仓N天尾盘 14:54:59 未到 14:55 不应触发")
	}

	clk3 := t4SeriesClock(t4Flats(2, 10.0), 14, 30, 0)
	clk3Buy := t4BuyAt(clk3, 0, 10.0)
	if !(A持仓N天尾盘{Days: 1, Time: "14:30:00"}).Sell("sh600000", clk3, clk3Buy) {
		t.Fatal("A持仓N天尾盘 14:30:00 恰等应触发")
	}
	if (A持仓N天尾盘{Days: 1, Time: "14:55:00"}).Sell("sh600000", clk3, clk3Buy) {
		t.Fatal("A持仓N天尾盘 14:30:00 未到 14:55 不应触发")
	}

	seeds := []int64{1, 7, 42, 99}
	buyIdxSet := []int{0, 5, 12, 23}
	daysSet := []int{0, 1, 3}
	sellTimes := []string{"", "14:55:00", "14:30:00"}

	fixtures := make([]extend.Klines, 0, len(seeds)+1)
	for _, seed := range seeds {
		fixtures = append(fixtures, t4SeriesClock(t4Walk(seed, 24), 14, 55, 0))
	}
	fixtures = append(fixtures, t4SeriesClock(t4Flats(24, 10.0), 14, 55, 0))

	triggered := 0
	for _, fx := range fixtures {
		for _, bi := range buyIdxSet {
			b := t4BuyAt(fx, bi, 10.0)
			for _, days := range daysSet {
				for _, st := range sellTimes {
					s := A持仓N天尾盘{Days: days, Time: st}
					triggered += t4CrossCheck(t, "A持仓N天尾盘", fx, b,
						func(c string, d extend.Klines, x core.Buy) bool { return t4尾盘Oracle(s, c, d, x) },
						s.Sell)
				}
			}
		}
	}
	if triggered == 0 {
		t.Fatal("对拍夹具未覆盖任何触发样例，oracle 可能失效")
	}
}

// ---- risk_break_buy_price（A买入次日跌破买入价）----

// t4次日跌破买价Oracle 迁移前 A买入次日跌破买入价.Sell 内联（逐语句等价）。
func t4次日跌破买价Oracle(code string, dks extend.Klines, buy core.Buy) bool {
	n := len(dks)
	if n < 2 {
		return false
	}
	if dks[n-2].Time.Format(time.DateOnly) != buy.Time.Format(time.DateOnly) {
		return false
	}
	return dks[n-1].Close < buy.Price
}

func TestT4A买入次日跌破买入价对拍(t *testing.T) {
	// 边界：昨为买入日、今收 9500<10000 触发；今收==买价（Lt 严格）不触发。
	bars := t4Flats(3, 10.0)
	bars[1] = t4Bar{O: 10.0, H: 10.0, L: 9.5, C: 9.5}
	full := t4Series(bars)
	buy := t4BuyAt(full, 0, 10.0)

	if !(A买入次日跌破买入价{}).Sell("sh600000", full[:2], buy) {
		t.Fatal("A买入次日跌破买入价 次日跌破应触发")
	}
	if (A买入次日跌破买入价{}).Sell("sh600000", full, buy) {
		t.Fatal("A买入次日跌破买入价 末根前一根非买入日不应触发")
	}
	if (A买入次日跌破买入价{}).Sell("sh600000", t4Series(t4Flats(2, 10.0))[:2], buy) {
		t.Fatal("A买入次日跌破买入价 今收==买价不应触发")
	}
	if (A买入次日跌破买入价{}).Sell("sh600000", full[:2], t4BuyAt(full, 0, 0.0)) {
		t.Fatal("A买入次日跌破买入价 买价非正不应触发")
	}

	seeds := []int64{1, 7, 42, 99}
	buyIdxSet := []int{0, 5, 12, 23}
	priceSet := []float64{10.0, 0.0}

	fixtures := make([]extend.Klines, 0, len(seeds)+1)
	for _, seed := range seeds {
		fixtures = append(fixtures, t4Series(t4Walk(seed, 24)))
	}
	det := t4Flats(24, 10.0)
	det[1] = t4Bar{O: 10.0, H: 10.0, L: 9.5, C: 9.5}
	fixtures = append(fixtures, t4Series(det))

	s := A买入次日跌破买入价{}
	triggered := 0
	for _, fx := range fixtures {
		for _, bi := range buyIdxSet {
			for _, price := range priceSet {
				b := t4BuyAt(fx, bi, price)
				triggered += t4CrossCheck(t, "A买入次日跌破买入价", fx, b, t4次日跌破买价Oracle, s.Sell)
			}
		}
	}
	if triggered == 0 {
		t.Fatal("对拍夹具未覆盖任何触发样例，oracle 可能失效")
	}
}

// ---- risk_no_gap_up（A买入次日未高开）----

// t4次日未高开Oracle 迁移前 A买入次日未高开.Sell 内联（逐语句等价）。
func t4次日未高开Oracle(code string, dks extend.Klines, buy core.Buy) bool {
	n := len(dks)
	if n < 2 {
		return false
	}
	if dks[n-2].Time.Format(time.DateOnly) != buy.Time.Format(time.DateOnly) {
		return false
	}
	return dks[n-1].Open <= dks[n-2].Close
}

func TestT4A买入次日未高开对拍(t *testing.T) {
	// 边界：未高开 9500<=10000 触发；高开不触发；平开恰等 Le 触发。
	bars := []t4Bar{
		{O: 10.0, H: 10.0, L: 10.0, C: 10.0},
		{O: 9.5, H: 10.0, L: 9.5, C: 9.8},
	}
	full := t4Series(bars)
	buy := t4BuyAt(full, 0, 10.0)

	if !(A买入次日未高开{}).Sell("sh600000", full, buy) {
		t.Fatal("A买入次日未高开 未高开应触发")
	}

	hiBars := []t4Bar{
		{O: 10.0, H: 10.0, L: 10.0, C: 10.0},
		{O: 10.5, H: 10.5, L: 10.0, C: 10.0},
	}
	hiFull := t4Series(hiBars)
	if (A买入次日未高开{}).Sell("sh600000", hiFull, buy) {
		t.Fatal("A买入次日未高开 高开不应触发")
	}

	flatBars := []t4Bar{
		{O: 10.0, H: 10.0, L: 10.0, C: 10.0},
		{O: 10.0, H: 10.0, L: 10.0, C: 10.0},
	}
	flatFull := t4Series(flatBars)
	if !(A买入次日未高开{}).Sell("sh600000", flatFull, buy) {
		t.Fatal("A买入次日未高开 平开恰等应触发")
	}

	seeds := []int64{1, 7, 42, 99}
	buyIdxSet := []int{0, 5, 12, 23}
	priceSet := []float64{10.0, 0.0}

	fixtures := make([]extend.Klines, 0, len(seeds)+1)
	for _, seed := range seeds {
		fixtures = append(fixtures, t4Series(t4Walk(seed, 24)))
	}
	det := t4Flats(24, 10.0)
	det[1] = t4Bar{O: 9.5, H: 10.0, L: 9.5, C: 9.8}
	fixtures = append(fixtures, t4Series(det))

	s := A买入次日未高开{}
	triggered := 0
	for _, fx := range fixtures {
		for _, bi := range buyIdxSet {
			for _, price := range priceSet {
				b := t4BuyAt(fx, bi, price)
				triggered += t4CrossCheck(t, "A买入次日未高开", fx, b, t4次日未高开Oracle, s.Sell)
			}
		}
	}
	if triggered == 0 {
		t.Fatal("对拍夹具未覆盖任何触发样例，oracle 可能失效")
	}
}

// ---- risk_break_buy_open（A跌破买入日开盘）----

// t4跌破买入日开盘Oracle 迁移前 A跌破买入日开盘.Sell 内联（逐语句等价）。
func t4跌破买入日开盘Oracle(code string, dks extend.Klines, buy core.Buy) bool {
	n := len(dks)
	if n == 0 {
		return false
	}
	buyDate := buy.Time.Format(time.DateOnly)
	if dks[n-1].Time.Format(time.DateOnly) == buyDate {
		return false
	}
	for i := n - 1; i >= 0; i-- {
		if dks[i].Time.Format(time.DateOnly) == buyDate {
			return dks[n-1].Close < dks[i].Open
		}
	}
	return false
}

func TestT4A跌破买入日开盘对拍(t *testing.T) {
	// 边界：买入日当天不触发，其后跌破买入日开盘触发。
	bars := t4Flats(5, 10.0)
	bars[2] = t4Bar{O: 11.0, H: 12.0, L: 10.0, C: 10.5}
	full := t4Series(bars)
	buy := t4BuyAt(full, 2, 10.0)

	if (A跌破买入日开盘{}).Sell("sh600000", full[:3], buy) {
		t.Fatal("A跌破买入日开盘 买入日当天不应触发")
	}
	if !(A跌破买入日开盘{}).Sell("sh600000", full[:4], buy) {
		t.Fatal("A跌破买入日开盘 次日跌破买入日开盘应触发")
	}
	if !(A跌破买入日开盘{}).Sell("sh600000", full, buy) {
		t.Fatal("A跌破买入日开盘 末根跌破买入日开盘应触发")
	}
	eqFull := t4Series(t4Flats(4, 10.0))
	if (A跌破买入日开盘{}).Sell("sh600000", eqFull, t4BuyAt(eqFull, 2, 10.0)) {
		t.Fatal("A跌破买入日开盘 今收==买入日开盘不应触发")
	}
	outBuy := core.Buy{Code: "sh600000", Time: time.Date(2025, 12, 31, 0, 0, 0, 0, time.Local), Price: 10.0}
	if (A跌破买入日开盘{}).Sell("sh600000", full, outBuy) {
		t.Fatal("A跌破买入日开盘 买入日不在序列不应触发")
	}

	seeds := []int64{1, 7, 42, 99}
	buyIdxSet := []int{0, 5, 12, 23}

	fixtures := make([]extend.Klines, 0, len(seeds)+1)
	for _, seed := range seeds {
		fixtures = append(fixtures, t4Series(t4Walk(seed, 24)))
	}
	det := t4Flats(24, 10.0)
	det[0] = t4Bar{O: 11.0, H: 11.0, L: 10.0, C: 10.0}
	fixtures = append(fixtures, t4Series(det))

	s := A跌破买入日开盘{}
	triggered := 0
	for _, fx := range fixtures {
		for _, bi := range buyIdxSet {
			b := t4BuyAt(fx, bi, 10.0)
			triggered += t4CrossCheck(t, "A跌破买入日开盘", fx, b, t4跌破买入日开盘Oracle, s.Sell)
		}
	}
	if triggered == 0 {
		t.Fatal("对拍夹具未覆盖任何触发样例，oracle 可能失效")
	}
}

// ---- sell_macd_down_after_buy（MACD买入后连跌）----

// t4MACD连跌Oracle 迁移前 MACD买入后连跌.Sell 内联（逐语句等价）。
func t4MACD连跌Oracle(s MACD买入后连跌, code string, dks extend.Klines, buy core.Buy) bool {
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
	afterDays, days := s.AfterDays, s.Days
	if afterDays == 0 {
		afterDays = 1
	}
	if days == 0 {
		days = 1
	}
	n := len(dks)
	if n < days+1 {
		return false
	}
	buyIdx := -1
	buyDate := buy.Time.Format("2006-01-02")
	for i := n - 1; i >= 0; i-- {
		if dks[i].Time.Format("2006-01-02") == buyDate {
			buyIdx = i
			break
		}
	}
	if buyIdx < 0 || n-1-buyIdx < afterDays {
		return false
	}
	hist := (f.MACD柱值{Fast: fast, Slow: slow, Signal: signal}).Series(dks)
	if len(hist) != n {
		return false
	}
	return f.FallingStreak(hist) >= days
}

func TestT4MACD买入后连跌对拍(t *testing.T) {
	// 边界：柱恒 0 不回跌不触发；预热不足（n<Days+1）不触发。
	flatFull := t4Series(t4FromCloses(t3cCloses(40, 10.0, 10.0)))
	flatBuy := t4BuyAt(flatFull, 0, 10.0)

	if (MACD买入后连跌{}).Sell("sh600000", flatFull, flatBuy) {
		t.Fatal("MACD买入后连跌 柱恒 0 不应触发")
	}
	if (MACD买入后连跌{Days: 3}).Sell("sh600000", flatFull[:3], flatBuy) {
		t.Fatal("MACD买入后连跌 预热不足不应触发")
	}

	buyIdxSet := []int{0, 5, 20, 39}
	cases := []MACD买入后连跌{
		{},
		{Days: 2},
		{Days: 3},
		{AfterDays: 2},
		{AfterDays: 3},
		{Days: 2, AfterDays: 2},
		{Fast: 1, Slow: 2, Signal: 2},
		{Fast: 1, Slow: 2, Signal: 2, Days: 3, AfterDays: 2},
	}
	fixtures := []extend.Klines{
		t4Series(t4FromCloses(t3cWalk(1, 40))),
		t4Series(t4FromCloses(t3cWave(10.0, 150, 10, 20, 25))),
		t4Series(t4FromCloses(t3cWave(10.0, 300, 30, 1, 10))),
		t4Series(t4FromCloses(t3cWave(10.0, 150, 0, 40))),
		t4Series(t4FromCloses(t3cCloses(40, 10.0, 10.0))),
	}

	triggered := 0
	for _, fx := range fixtures {
		for _, bi := range buyIdxSet {
			b := t4BuyAt(fx, bi, 10.0)
			for _, s := range cases {
				triggered += t4CrossCheck(t, "MACD买入后连跌", fx, b,
					func(c string, d extend.Klines, x core.Buy) bool { return t4MACD连跌Oracle(s, c, d, x) },
					s.Sell)
			}
		}
	}
	if triggered == 0 {
		t.Fatal("对拍夹具未覆盖任何触发样例，oracle 可能失效")
	}
}
