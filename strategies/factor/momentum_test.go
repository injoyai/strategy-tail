package factor

import (
	"math"
	"testing"
	"time"

	"github.com/injoyai/strategy-tail/lib/extend"
	"github.com/injoyai/tdx/protocol"
)

// ---- 测试工具（供 factor 包全部 *_test.go 共用）----

// mk 构造单根K线。
func mk(base time.Time, i int, o, h, l, c float64, vol int64) *extend.Kline {
	tm := base.AddDate(0, 0, i)
	return &extend.Kline{
		Unix: tm.Unix(),
		Kline: &protocol.Kline{
			Time: tm,
			Open: protocol.Yuan(o), Close: protocol.Yuan(c),
			High: protocol.Yuan(h), Low: protocol.Yuan(l),
			Volume: vol,
		},
	}
}

// closes 按收盘价序列构造等长K线（o=h=l=c，量恒 10000）。
func closes(base time.Time, cs ...float64) extend.Klines {
	ks := make(extend.Klines, 0, len(cs))
	for i, c := range cs {
		ks = append(ks, mk(base, i, c, c, c, c, 10000))
	}
	return ks
}

func wantNaN(t *testing.T, name string, v float64) {
	t.Helper()
	if !math.IsNaN(v) {
		t.Fatalf("%s 应返回 NaN, got %v", name, v)
	}
}

func wantVal(t *testing.T, name string, got, want, tol float64) {
	t.Helper()
	if math.IsNaN(got) || math.Abs(got-want) > tol {
		t.Fatalf("%s = %v, want %v(±%v)", name, got, want, tol)
	}
}

// ---- 动量族 ----

func TestN日动量(t *testing.T) {
	base := time.Date(2025, 1, 1, 0, 0, 0, 0, time.Local)
	// 收盘 1..10，Days=9：(10-1)/1 = 9
	ks := closes(base, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10)
	f := N日动量{Days: 9}
	wantVal(t, f.Name(), f.Value("sh600000", ks), 9, 1e-9)

	// 数据不足：len=9 < Days+1=10
	wantNaN(t, "数据不足", f.Value("sh600000", ks[:9]))

	// 零值参数走默认 20
	defaultName := (N日动量{}).Name()
	if defaultName != "N日动量(20)" {
		t.Fatalf("默认参数名异常: %s", defaultName)
	}
	wantNaN(t, "基准收盘为零", (N日动量{Days: 1}).Value("sh600000", closes(base, 0, 1)))
}

func Test均线偏离(t *testing.T) {
	base := time.Date(2025, 1, 1, 0, 0, 0, 0, time.Local)
	// 收盘 1..10，MA5=(6+7+8+9+10)/5=8，偏离 (10-8)/8 = 0.25
	ks := closes(base, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10)
	f := 均线偏离{Days: 5}
	wantVal(t, f.Name(), f.Value("sh600000", ks), 0.25, 1e-9)

	wantNaN(t, "数据不足", f.Value("sh600000", ks[:4]))
	wantNaN(t, "均线为零", (均线偏离{Days: 2}).Value("sh600000", closes(base, 0, 0)))
}

func TestN日斜率(t *testing.T) {
	base := time.Date(2025, 1, 1, 0, 0, 0, 0, time.Local)
	// 完美线性 ys=i+1（i=0..4）：slope=1, ȳ=3, 相对斜率 = 1/3
	ks := closes(base, 1, 2, 3, 4, 5)
	f := N日斜率{Days: 5}
	wantVal(t, f.Name(), f.Value("sh600000", ks), 1.0/3.0, 1e-9)

	// 水平序列：slope=0
	flat := closes(base, 5, 5, 5, 5, 5)
	wantVal(t, "水平", f.Value("sh600000", flat), 0, 1e-12)

	wantNaN(t, "数据不足", f.Value("sh600000", ks[:1]))
	wantNaN(t, "均价为零", (N日斜率{Days: 2}).Value("sh600000", closes(base, 0, 0)))
}
