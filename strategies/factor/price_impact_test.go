package factor

import (
	"math"
	"testing"
	"time"

	"github.com/injoyai/strategy-tail/lib/extend"
)

func TestTurnoverPerPriceMove(t *testing.T) {
	ks := closes(time.Date(2025, 1, 1, 0, 0, 0, 0, time.Local), 100, 125, 100, 100)
	ks[1].Amount, ks[2].Amount, ks[3].Amount = 500_000_000, 200_000_000, 200_000_000
	for _, tt := range []struct {
		name string
		days int
		end  int
		want float64
	}{
		{"上涨 25 个百分点", 1, 2, 0.2},
		{"下跌 20 个百分点", 1, 3, 0.1},
		{"两日累计成交额除以累计绝对涨跌幅", 2, 3, 7.0 / 45},
		{"平盘日成交额计入多日窗口", 3, 4, 0.2},
	} {
		f := Build("turnover_per_price_move", tt.days)
		if f == nil {
			t.Fatal("未注册每1%涨跌成交额")
		}
		wantVal(t, tt.name, f.Value("sh600000", ks[:tt.end]), tt.want, 1e-12)
	}
	f := Build("turnover_per_price_move", 0)
	if f.Name() != "每1%涨跌成交额(1)" {
		t.Fatalf("默认名称 = %q", f.Name())
	}
	wantNaN(t, "空输入", f.Value("", nil))
	wantNaN(t, "缺上日收盘", f.Value("", ks[:1]))
	wantNaN(t, "只含平盘日", f.Value("", ks))
	for _, tt := range []struct {
		name   string
		mutate func(extend.Klines)
	}{
		{"缺 K 线", func(v extend.Klines) { v[1] = nil }},
		{"缺内嵌行情", func(v extend.Klines) { v[0].Kline = nil }},
		{"昨收为零", func(v extend.Klines) { v[0].Close = 0 }},
		{"收盘为负", func(v extend.Klines) { v[1].Close = -1 }},
		{"非有限价格", func(v extend.Klines) { v[0].Close = math.Inf(1) }},
		{"非有限成交额", func(v extend.Klines) { v[1].Amount = math.Inf(1) }},
		{"无效成交额", func(v extend.Klines) { v[1].Amount = math.NaN() }},
		{"缺成交额", func(v extend.Klines) { v[1].Amount = 0 }},
	} {
		bars := cloneKs(ks[:2])
		tt.mutate(bars)
		wantNaN(t, tt.name, f.Value("", bars))
	}
}
