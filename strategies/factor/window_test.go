package factor

import (
	"reflect"
	"testing"
	"time"

	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/lib/extend"
	"github.com/injoyai/strategy-tail/strategies/util"
)

func TestWindowFactors(t *testing.T) {
	ks := extend.Klines{
		mk(time.Time{}, 0, 10, 13, 8, 10, 100),
		mk(time.Time{}, 1, 10, 12, 7, 11, 200),
		mk(time.Time{}, 2, 11, 14, 9, 12, 900),
	}
	for _, tt := range []struct {
		factor core.Factor
		want   float64
	}{
		{A最高价(2), 14},
		{A最低价(2), 7},
		{A均量(2), 550},
		{A前N日均量(2), 150},
	} {
		wantVal(t, tt.factor.Name(), tt.factor.Value("", ks), tt.want, 0)
		wantNaN(t, tt.factor.Name(), tt.factor.Value("", nil))
		wantNaN(t, tt.factor.Name(), tt.factor.Value("", ks[:1]))
	}
	// 前日窗口不受今日极值影响；原行情 LLV 的零值语义也必须保留。
	wantVal(t, "昨日高点", A最高价(2).Value("", ks[:2]), 13, 0)
	ks[2].Low = 0.0
	wantVal(t, "末日零低点", A最低价(2).Value("", ks), 0, 0)
	ks[1].Low, ks[2].Low = 0.0, 9.0
	wantVal(t, "沿用行情零值处理", A最低价(2).Value("", ks), 9, 0)
	// 原始价格比较不经过浮点，避免大数在转换时丢掉一厘。
	ks[2].High = 1.8014398509481984e+13
	if got := A最高价(2).Price(ks); got != ks[2].High {
		t.Fatalf("原始最高价 = %v, want %v", got, ks[2].High)
	}
	ks[2].Close = ks[2].High
	if got := A均线(1).Price(ks); got != ks[2].Close {
		t.Fatalf("原始均线 = %v, want %v", got, ks[2].Close)
	}
	if A最低价(2).Price(ks[:1]) != 0.0 || A最高价(2).Price(nil) != 0.0 {
		t.Fatal("原始价格缺数据返回 0，数值因子缺数据返回 NaN")
	}
	for _, factor := range []core.Factor{A最高价(0), A最低价(0), A均量(0)} {
		wantNaN(t, "默认窗口数据不足", factor.Value("", ks))
	}
}

func TestSmoothedMACDFactor(t *testing.T) {
	ks := closes(time.Time{}, 10, 11, 13, 8, 9, 14)
	for _, f := range []MACD平滑柱值{{}, {Fast: 2, Slow: 4, Signal: 2, Smooth: 3}, {Smooth: 1}, {Smooth: -1}} {
		fast, slow, signal := (MACD柱值{Fast: f.Fast, Slow: f.Slow, Signal: f.Signal}).params()
		smooth := f.Smooth
		if smooth == 0 {
			smooth = 5
		}
		want := util.SmoothedMACDHistogram(ks, fast, slow, signal, smooth)
		if !reflect.DeepEqual(f.Series(ks), want) {
			t.Fatalf("%s 未保留原平滑序列", f.Name())
		}
		for end := 1; end <= len(ks); end++ {
			wantVal(t, "逐前缀无前视", f.Value("", ks[:end]), want[end-1], 0)
		}
		wantNaN(t, "空平滑MACD", f.Value("", nil))
	}
	if (MACD平滑柱值{}).Name() != (MACD平滑柱值{Fast: 12, Slow: 26, Signal: 9, Smooth: 5}).Name() ||
		(MACD平滑柱值{Smooth: 2}).Name() == (MACD平滑柱值{Smooth: 3}).Name() {
		t.Fatal("平滑MACD名称必须区分计算参数")
	}
}
