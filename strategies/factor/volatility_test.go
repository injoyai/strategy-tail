package factor

import (
	"math"
	"testing"
	"time"

	"github.com/injoyai/strategy-tail/lib/extend"
)

func TestN日波动(t *testing.T) {
	base := time.Date(2025, 1, 1, 0, 0, 0, 0, time.Local)

	// 恒定翻倍收益率（1,1,1）→ 标准差 0
	ks := closes(base, 10, 20, 40, 80)
	f := N日波动{Days: 3}
	wantVal(t, f.Name(), f.Value("sh600000", ks), 0, 1e-12)

	// 收益率 {0.5, 0.5, -0.5}：均值 1/6，总体方差 = (1/9+1/9+4/9)/3 = 2/9
	ks2 := closes(base, 10, 15, 22.5, 11.25)
	wantVal(t, "混合", f.Value("sh600000", ks2), math.Sqrt(2.0/9.0), 1e-9)

	// 数据不足：len=3 < Days+1=4
	wantNaN(t, "数据不足", f.Value("sh600000", ks[:3]))

	// 窗口内前一日收盘为 0 → NaN
	wantNaN(t, "零收盘价", (N日波动{Days: 1}).Value("sh600000", closes(base, 0, 1)))

	if g := (N日波动{}).Name(); g != "N日波动(20)" {
		t.Fatalf("默认参数名异常: %s", g)
	}
}

func TestN日振幅(t *testing.T) {
	base := time.Date(2025, 1, 1, 0, 0, 0, 0, time.Local)

	// 收盘 1..10（o=h=l=c），近5日：HHV=10, LLV=6 → (10-6)/6
	ks := closes(base, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10)
	f := N日振幅{Days: 5}
	wantVal(t, f.Name(), f.Value("sh600000", ks), 4.0/6.0, 1e-9)

	// LLV=0 → NaN（extend.LLV 以 0 为哨兵初值，低点 0 之后跟正低点会被覆盖；
	// 此处两根低点均为 0 才稳定返回 0）
	bad := extend.Klines{
		mk(base, 0, 10, 10, 0, 10, 10000),
		mk(base, 1, 10, 11, 0, 10, 10000),
	}
	f2 := N日振幅{Days: 2}
	wantNaN(t, "LLV为0", f2.Value("sh600000", bad))

	// 数据不足
	wantNaN(t, "数据不足", f.Value("sh600000", ks[:4]))

	if g := (N日振幅{}).Name(); g != "N日振幅(20)" {
		t.Fatalf("默认参数名异常: %s", g)
	}
}
