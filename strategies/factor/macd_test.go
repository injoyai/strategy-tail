package factor

import (
	"math"
	"testing"
	"time"

	"github.com/injoyai/strategy-tail/strategies/util"
)

func TestMACD柱强度和增量(t *testing.T) {
	base := time.Date(2025, 1, 1, 0, 0, 0, 0, time.Local)
	ks := closes(base,
		30, 29, 28, 27, 26, 25, 24, 23, 22, 21,
		20, 19, 18, 17, 16, 15, 14, 13, 12, 11,
		10, 9, 8, 7, 6, 5, 4, 3, 2, 1,
		2, 3, 4, 5, 6, 7, 8, 9, 10, 11,
	)
	hist := util.MACDHistogram(ks, 12, 26, 9)
	close := ks[len(ks)-1].Close.Float64()

	wantVal(t, "MACD柱强度", (MACD柱强度{}).Value("sh600000", ks), hist[len(hist)-1]/close, 1e-12)
	wantVal(t, "MACD柱增量", (MACD柱增量{}).Value("sh600000", ks),
		(hist[len(hist)-1]-hist[len(hist)-2])/close, 1e-12)

	wantNaN(t, "MACD柱强度数据不足", (MACD柱强度{}).Value("sh600000", ks[:34]))
	wantNaN(t, "MACD柱增量数据不足", (MACD柱增量{}).Value("sh600000", ks[:34]))
	wantNaN(t, "MACD柱强度收盘为零", (MACD柱强度{}).Value("sh600000", closes(base, make([]float64, 35)...)))
}

func TestMACD低位位置(t *testing.T) {
	base := time.Date(2025, 1, 1, 0, 0, 0, 0, time.Local)
	ks := closes(base,
		30, 29, 28, 27, 26, 25, 24, 23, 22, 21,
		20, 19, 18, 17, 16, 15, 14, 13, 12, 11,
		10, 9, 8, 7, 6, 5, 4, 3, 2, 1,
		2, 3, 4, 5, 6, 7, 8, 9, 10, 11,
	)
	f := MACD低位位置{Days: 4}
	hist := util.MACDHistogram(ks, 12, 26, 9)
	window := hist[len(hist)-1-4 : len(hist)-1]
	lo, hi := window[0], window[0]
	for _, v := range window[1:] {
		lo = math.Min(lo, v)
		hi = math.Max(hi, v)
	}
	want := (hist[len(hist)-2] - lo) / (hi - lo)
	wantVal(t, f.Name(), f.Value("sh600000", ks), want, 1e-12)

	if got := (MACD低位位置{}).Name(); got != "MACD低位位置(4)" {
		t.Fatalf("默认名称 = %q, want MACD低位位置(4)", got)
	}
	wantNaN(t, "MACD低位位置数据不足", f.Value("sh600000", ks[:34]))
}

func TestMACD负柱连续天数(t *testing.T) {
	base := time.Date(2025, 1, 1, 0, 0, 0, 0, time.Local)
	cs := make([]float64, 40)
	for i := range cs {
		cs[i] = 100
		if i >= 30 {
			cs[i] = 100 - float64((i-29)*(i-29))
		}
	}
	ks := closes(base, cs...)
	hist := util.MACDHistogram(ks, 12, 26, 9)
	want := 0
	for i := len(hist) - 1; i >= 0 && hist[i] < 0; i-- {
		want++
	}
	got := (MACD负柱连续天数{}).Value("sh600000", ks)
	wantVal(t, "MACD负柱连续天数", got, float64(want), 0)
	wantNaN(t, "MACD负柱连续天数数据不足", (MACD负柱连续天数{}).Value("sh600000", ks[:34]))
}

func TestMACD柱连续增长天数(t *testing.T) {
	base := time.Date(2025, 1, 1, 0, 0, 0, 0, time.Local)
	ks := closes(base,
		30, 29, 28, 27, 26, 25, 24, 23, 22, 21,
		20, 19, 18, 17, 16, 15, 14, 13, 12, 11,
		10, 9, 8, 7, 6, 5, 4, 3, 2, 1,
		2, 3, 4, 5, 6, 7, 8, 9, 10, 11,
	)
	hist := util.MACDHistogram(ks, 12, 26, 9)
	want := 0
	for i := len(hist) - 1; i > 0 && hist[i] > hist[i-1]; i-- {
		want++
	}
	wantVal(t, "MACD柱连续增长天数", (MACD柱连续增长天数{}).Value("sh600000", ks), float64(want), 0)

	falling := append(append([]float64(nil),
		30, 29, 28, 27, 26, 25, 24, 23, 22, 21,
		20, 19, 18, 17, 16, 15, 14, 13, 12, 11,
		10, 9, 8, 7, 6, 5, 4, 3, 2, 1,
		2, 3, 4, 5, 6, 7, 8, 9, 10, 11), 0.1)
	wantVal(t, "当日未增长", (MACD柱连续增长天数{}).Value("sh600000", closes(base, falling...)), 0, 0)
	wantNaN(t, "MACD柱连续增长天数数据不足", (MACD柱连续增长天数{}).Value("sh600000", ks[:34]))
}

func Test均线最弱日斜率(t *testing.T) {
	base := time.Date(2025, 1, 1, 0, 0, 0, 0, time.Local)
	ks := closes(base, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10)
	f := 均线最弱日斜率{Days: 5}

	// 5 日均线依次为 3、4、5、6、7、8；最近 5 步的最小相对斜率为 (8-7)/7。
	wantVal(t, f.Name(), f.Value("sh600000", ks), 1.0/7.0, 1e-12)
	wantNaN(t, "均线最弱日斜率数据不足", f.Value("sh600000", ks[:9]))
	wantNaN(t, "均线最弱日斜率均线为零", (均线最弱日斜率{Days: 2}).Value("sh600000", closes(base, 0, 0, 0, 0, 0, 0, 0)))
}
