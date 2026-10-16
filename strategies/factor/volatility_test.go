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

func TestN日下行波动(t *testing.T) {
	base := time.Date(2025, 1, 1, 0, 0, 0, 0, time.Local)

	// 收益率 {0.5, 0.5, -0.5}：仅 -0.5 计入 → sqrt(0.25/3)
	ks := closes(base, 10, 15, 22.5, 11.25)
	f := N日下行波动{Days: 3}
	wantVal(t, f.Name(), f.Value("sh600000", ks), math.Sqrt(0.25/3.0), 1e-9)

	// 全正收益 → 0（合法值）
	up := closes(base, 10, 20, 40, 80)
	wantVal(t, "全正收益", f.Value("sh600000", up), 0, 1e-12)

	// 窗口内前一日收盘为 0 → NaN
	wantNaN(t, "零收盘价", (N日下行波动{Days: 1}).Value("sh600000", closes(base, 0, 1)))

	// 数据不足：len=3 < Days+1=4
	wantNaN(t, "数据不足", f.Value("sh600000", ks[:3]))

	if g := (N日下行波动{}).Name(); g != "N日下行波动(20)" {
		t.Fatalf("默认参数名异常: %s", g)
	}
}

func TestN日收益偏度(t *testing.T) {
	base := time.Date(2025, 1, 1, 0, 0, 0, 0, time.Local)

	// 收益率 {0.5, 0.5, -0.5}：m2=2/9，m3=-2/27 → 偏度 = -1/√2（左偏）
	ks := closes(base, 10, 15, 22.5, 11.25)
	f := N日收益偏度{Days: 3}
	wantVal(t, f.Name(), f.Value("sh600000", ks), -1/math.Sqrt2, 1e-9)

	// 对称收益 {-0.05, 0, 0.05} → 0
	sym := closes(base, 10, 9.5, 9.5, 9.975)
	wantVal(t, "对称", f.Value("sh600000", sym), 0, 1e-12)

	// 收益恒定（收益恒为 1，可精确表示，m2=0）→ NaN
	steady := closes(base, 10, 20, 40, 80)
	wantNaN(t, "收益恒定", f.Value("sh600000", steady))

	// N < 3 → NaN
	wantNaN(t, "N过小", (N日收益偏度{Days: 2}).Value("sh600000", sym))

	// 窗口内前一日收盘为 0 → NaN
	wantNaN(t, "零收盘价", (N日收益偏度{Days: 3}).Value("sh600000", closes(base, 2, 0, 4, 8)))

	// 数据不足：len=3 < Days+1=4
	wantNaN(t, "数据不足", f.Value("sh600000", ks[:3]))

	if g := (N日收益偏度{}).Name(); g != "N日收益偏度(20)" {
		t.Fatalf("默认参数名异常: %s", g)
	}
}

func TestReturnSkewConstantReturns(t *testing.T) {
	base := time.Date(2025, 1, 1, 0, 0, 0, 0, time.Local)
	f := N日收益偏度{Days: 3}
	for _, cs := range [][]float64{
		{10, 11, 12.1, 13.31},
		{10, 9, 8.1, 7.29},
		{10, 10, 10, 10},
	} {
		wantNaN(t, "恒定收益", f.Value("sh600000", closes(base, cs...)))
	}
	// 低波动但存在真实差异的收益不能被当作浮点噪声剔除。
	ks := closes(base, 1000, 1000.001, 1000.003, 1000.006)
	v := f.Value("sh600000", ks)
	if math.IsNaN(v) || math.IsInf(v, 0) {
		t.Fatalf("真实低波动收益应有有限偏度，got %v", v)
	}
}

func TestNewVolatilityOversizedWindows(t *testing.T) {
	base := time.Date(2025, 1, 1, 0, 0, 0, 0, time.Local)
	ks := closes(base, 10, 11, 12, 13)
	for _, n := range []int{len(ks), math.MaxInt - 1, math.MaxInt} {
		for end := 0; end <= len(ks); end++ {
			wantNaN(t, "下行波动超长窗口", (N日下行波动{Days: n}).Value("sh600000", ks[:end]))
			wantNaN(t, "收益偏度超长窗口", (N日收益偏度{Days: n}).Value("sh600000", ks[:end]))
		}
	}
}

func TestN日Parkinson波动(t *testing.T) {
	base := time.Date(2025, 1, 1, 0, 0, 0, 0, time.Local)

	// 单根 h=2, l=1：sqrt(ln²2 / (4·ln2)) = sqrt(ln2)/2
	one := extend.Klines{mk(base, 0, 1, 2, 1, 1.5, 10000)}
	f := N日Parkinson波动{Days: 1}
	wantVal(t, f.Name(), f.Value("sh600000", one), math.Sqrt(math.Log(2))/2, 1e-12)

	// 两根 [h=2,l=1, 一字 h=l=3]：sqrt( ln²2/2 / (4·ln2) ) = sqrt(ln2/8)
	two := extend.Klines{
		mk(base, 0, 1, 2, 1, 1.5, 10000),
		mk(base, 1, 3, 3, 3, 3, 10000),
	}
	wantVal(t, "含一字线", (N日Parkinson波动{Days: 2}).Value("sh600000", two), math.Sqrt(math.Log(2)/8), 1e-12)

	// 全为一字线 → 0（合法值）
	doji := closes(base, 10, 10, 10)
	wantVal(t, "全一字线", (N日Parkinson波动{Days: 3}).Value("sh600000", doji), 0, 1e-12)

	// Low <= 0 → NaN
	wantNaN(t, "低点为零", (N日Parkinson波动{Days: 1}).Value("sh600000",
		extend.Klines{mk(base, 0, 0, 1, 0, 0.5, 10000)}))

	// High < Low（异常数据）→ NaN
	wantNaN(t, "高低倒挂", (N日Parkinson波动{Days: 1}).Value("sh600000",
		extend.Klines{mk(base, 0, 1, 0.5, 2, 0.7, 10000)}))

	// 数据不足：len=2 < Days=3
	wantNaN(t, "数据不足", (N日Parkinson波动{Days: 3}).Value("sh600000", two))

	if g := (N日Parkinson波动{}).Name(); g != "N日Parkinson波动(20)" {
		t.Fatalf("默认参数名异常: %s", g)
	}
}
