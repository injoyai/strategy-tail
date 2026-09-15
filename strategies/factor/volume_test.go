package factor

import (
	"testing"
	"time"

	"github.com/injoyai/strategy-tail/lib/extend"
)

// volKs 按收盘与成交量序列构造K线。
func volKs(base time.Time, cs []float64, vols []int64) extend.Klines {
	ks := make(extend.Klines, 0, len(cs))
	for i := range cs {
		ks = append(ks, mk(base, i, cs[i], cs[i], cs[i], cs[i], vols[i]))
	}
	return ks
}

func Test量比(t *testing.T) {
	base := time.Date(2025, 1, 1, 0, 0, 0, 0, time.Local)

	// 今量 500，前 4 日均量 (100+200+300+400)/4=250 → 2
	ks := volKs(base, []float64{10, 10, 10, 10, 10}, []int64{100, 200, 300, 400, 500})
	f := 量比{Days: 4}
	wantVal(t, f.Name(), f.Value("sh600000", ks), 2, 1e-9)

	// 恒量 → 1
	flat := volKs(base, []float64{10, 10, 10, 10}, []int64{10000, 10000, 10000, 10000})
	f2 := 量比{Days: 3}
	wantVal(t, "恒量", f2.Value("sh600000", flat), 1, 1e-9)

	// 前 N 日均量=0 → NaN
	zero := volKs(base, []float64{10, 10, 10}, []int64{0, 0, 500})
	f3 := 量比{Days: 2}
	wantNaN(t, "均量为0", f3.Value("sh600000", zero))

	// 数据不足：len=2 < Days+1=3
	wantNaN(t, "数据不足", f.Value("sh600000", ks[:2]))

	if g := (量比{}).Name(); g != "量比(5)" {
		t.Fatalf("默认参数名异常: %s", g)
	}
}

func Test量分位(t *testing.T) {
	base := time.Date(2025, 1, 1, 0, 0, 0, 0, time.Local)

	// {1,2,3,4,5} 今量 5 → 5/5 全部 ≤ → 1
	up := volKs(base, []float64{10, 10, 10, 10, 10}, []int64{1, 2, 3, 4, 5})
	f := 量分位{Days: 5}
	wantVal(t, f.Name(), f.Value("sh600000", up), 1, 1e-9)

	// {5,4,3,2,1} 今量 1 → 仅 1 天 ≤ → 0.2
	down := volKs(base, []float64{10, 10, 10, 10, 10}, []int64{5, 4, 3, 2, 1})
	wantVal(t, "递减", f.Value("sh600000", down), 0.2, 1e-9)

	// {1,2,2} 今量 2 → 历史日与今量并列计入 ≤ → 1.0（误写 < 会得 1/3）
	tie := volKs(base, []float64{10, 10, 10}, []int64{1, 2, 2})
	wantVal(t, "并列", (量分位{Days: 3}).Value("sh600000", tie), 1, 1e-9)

	// 数据不足
	wantNaN(t, "数据不足", f.Value("sh600000", up[:4]))

	if g := (量分位{}).Name(); g != "量分位(60)" {
		t.Fatalf("默认参数名异常: %s", g)
	}
}

func Test放量占比(t *testing.T) {
	base := time.Date(2025, 1, 1, 0, 0, 0, 0, time.Local)

	// {100,100,100,300}：均值 150，阈值 225 → 仅 300 放量 → 0.25
	ks := volKs(base, []float64{10, 10, 10, 10}, []int64{100, 100, 100, 300})
	f := 放量占比{Days: 4}
	wantVal(t, f.Name(), f.Value("sh600000", ks), 0.25, 1e-9)

	// 恒量 → 0
	flat := volKs(base, []float64{10, 10, 10}, []int64{100, 100, 100})
	f2 := 放量占比{Days: 3}
	wantVal(t, "恒量", f2.Value("sh600000", flat), 0, 1e-12)

	// {3,3,1,1}：均值 2，阈值 3.0，vol==3 算严格大于不成立 → 0（误写 >= 会得 0.5）
	eq := volKs(base, []float64{10, 10, 10, 10}, []int64{3, 3, 1, 1})
	wantVal(t, "等于阈值不算放量", (放量占比{Days: 4}).Value("sh600000", eq), 0, 1e-12)

	// 数据不足
	wantNaN(t, "数据不足", f.Value("sh600000", ks[:3]))

	if g := (放量占比{}).Name(); g != "放量占比(20)" {
		t.Fatalf("默认参数名异常: %s", g)
	}
}
