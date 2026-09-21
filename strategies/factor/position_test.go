package factor

import (
	"math"
	"testing"
	"time"

	"github.com/injoyai/strategy-tail/lib/extend"
)

func TestN日高低位(t *testing.T) {
	base := time.Date(2025, 1, 1, 0, 0, 0, 0, time.Local)

	// 收盘 1..10，Days=10：c=HHV=10, LLV=1 → 1
	ks := closes(base, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10)
	f := N日高低位{Days: 10}
	wantVal(t, f.Name(), f.Value("sh600000", ks), 1, 1e-9)

	// 递减序列收盘在最低点 → 0
	down := closes(base, 10, 9, 8, 7, 6, 5, 4, 3, 2, 1)
	wantVal(t, "最低点", f.Value("sh600000", down), 0, 1e-9)

	// 带影线：极值取 High/Low 而非收盘 — High 极值 6、Low 极值 4、c=5 → 0.5（若误用收盘极值则 HHV=LLV=5 → NaN）
	shadows := extend.Klines{
		mk(base, 0, 5, 6, 5, 5, 10000),
		mk(base, 1, 5, 5, 4, 5, 10000),
		mk(base, 2, 5, 5, 4, 5, 10000),
	}
	f3 := N日高低位{Days: 3}
	wantVal(t, "影线极值", f3.Value("sh600000", shadows), 0.5, 1e-9)

	// HHV=LLV → NaN
	flat := closes(base, 5, 5, 5, 5, 5)
	f2 := N日高低位{Days: 5}
	wantNaN(t, "水平", f2.Value("sh600000", flat))

	// 数据不足
	wantNaN(t, "数据不足", f.Value("sh600000", ks[:9]))

	if g := (N日高低位{}).Name(); g != "N日高低位(60)" {
		t.Fatalf("默认参数名异常: %s", g)
	}
}

func Test收盘分位(t *testing.T) {
	base := time.Date(2025, 1, 1, 0, 0, 0, 0, time.Local)

	// 单调上涨 1..5（Days=5）：全部收盘 ≤ 今收 → 1
	ks := closes(base, 1, 2, 3, 4, 5)
	f := 收盘分位{Days: 5}
	wantVal(t, f.Name(), f.Value("sh600000", ks), 1, 1e-9)

	// 单调下跌 5..1：仅今收 ≤ 今收 → 1/5 = 0.2
	down := closes(base, 5, 4, 3, 2, 1)
	wantVal(t, "最低点", f.Value("sh600000", down), 0.2, 1e-9)

	// 并列：[2,1,3,2,2]（今收=2）：≤2 的有 2,1,2,2 → 4/5 = 0.8
	tie := closes(base, 2, 1, 3, 2, 2)
	wantVal(t, "并列", f.Value("sh600000", tie), 0.8, 1e-9)

	// 全平价 → 1.0（与 N日高低位 的 NaN 不同：并列按 ≤ 计入）
	flat := closes(base, 5, 5, 5, 5, 5)
	wantVal(t, "全平", f.Value("sh600000", flat), 1, 1e-9)

	// 窗口截取：[1,5,5,5,2]（Days=3）窗口为最近 3 根 [5,5,2] → 仅 2 ≤ 2 → 1/3；若误用全序列则为 2/5=0.4
	win := closes(base, 1, 5, 5, 5, 2)
	f3 := 收盘分位{Days: 3}
	wantVal(t, "窗口截取", f3.Value("sh600000", win), 1.0/3.0, 1e-9)

	// 数据不足
	wantNaN(t, "数据不足", f.Value("sh600000", ks[:4]))

	if g := (收盘分位{}).Name(); g != "收盘分位(120)" {
		t.Fatalf("默认参数名异常: %s", g)
	}
}

func TestK值(t *testing.T) {
	base := time.Date(2025, 1, 1, 0, 0, 0, 0, time.Local)

	// 单调上涨 1..10（Days=9）：每日 RSV=100 → K9 = 100 - 50*(2/3)^9
	ks := closes(base, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10)
	f := K值{Days: 9}
	wantVal(t, f.Name(), f.Value("sh600000", ks), 100-50*math.Pow(2.0/3.0, 9), 1e-9)

	// 单调下跌 10..1：每日 RSV=0 → K9 = 50*(2/3)^9
	down := closes(base, 10, 9, 8, 7, 6, 5, 4, 3, 2, 1)
	wantVal(t, "下跌", f.Value("sh600000", down), 50*math.Pow(2.0/3.0, 9), 1e-9)

	// 数据不足
	wantNaN(t, "数据不足", f.Value("sh600000", ks[:8]))

	if g := (K值{}).Name(); g != "K值(9)" {
		t.Fatalf("默认参数名异常: %s", g)
	}
}
