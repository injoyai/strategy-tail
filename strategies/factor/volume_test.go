package factor

import (
	"math"
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

// turnKs 按收盘、成交量与换手率（百分数）序列构造K线。
func turnKs(base time.Time, cs []float64, vols []int64, turns []float64) extend.Klines {
	ks := volKs(base, cs, vols)
	for i := range ks {
		ks[i].Turnover = turns[i]
	}
	return ks
}

// ocKs 按开盘、收盘与成交量序列构造K线（高=max(开,收)、低=min(开,收)）。
func ocKs(base time.Time, os, cs []float64, vols []int64) extend.Klines {
	ks := make(extend.Klines, 0, len(cs))
	for i := range cs {
		ks = append(ks, mk(base, i, os[i], math.Max(os[i], cs[i]), math.Min(os[i], cs[i]), cs[i], vols[i]))
	}
	return ks
}

func Test平均换手率(t *testing.T) {
	base := time.Date(2025, 1, 1, 0, 0, 0, 0, time.Local)

	// 换手率 {1,2,3,4} → 均值 2.5（百分数）
	ks := turnKs(base, []float64{10, 10, 10, 10}, []int64{100, 100, 100, 100}, []float64{1, 2, 3, 4})
	f := 平均换手率{Days: 4}
	wantVal(t, f.Name(), f.Value("sh600000", ks), 2.5, 1e-9)

	// 无成交日（量为 0、换手率 0）按 0 计入 → (2+0+4+2)/4 = 2
	zeroVol := turnKs(base, []float64{10, 10, 10, 10}, []int64{100, 0, 100, 100}, []float64{2, 0, 4, 2})
	wantVal(t, "无成交日", f.Value("sh600000", zeroVol), 2, 1e-9)

	// 有成交但换手率缺失（Turnover 为 0）→ NaN
	missing := turnKs(base, []float64{10, 10, 10, 10}, []int64{100, 100, 100, 100}, []float64{2, 0, 4, 2})
	wantNaN(t, "换手率缺失", f.Value("sh600000", missing))

	// 数据不足：len=3 < Days=4
	wantNaN(t, "数据不足", f.Value("sh600000", ks[:3]))

	if g := (平均换手率{}).Name(); g != "平均换手率(20)" {
		t.Fatalf("默认参数名异常: %s", g)
	}
}

func Test换手率分位(t *testing.T) {
	base := time.Date(2025, 1, 1, 0, 0, 0, 0, time.Local)

	// {1,2,3,2} 今换手率 2 → ≤2 的有 {1,2,2} → 3/4
	ks := turnKs(base, []float64{10, 10, 10, 10}, []int64{100, 100, 100, 100}, []float64{1, 2, 3, 2})
	f := 换手率分位{Days: 4}
	wantVal(t, f.Name(), f.Value("sh600000", ks), 0.75, 1e-9)

	// 今换手率缺失（有成交但 Turnover 为 0）→ NaN
	noToday := turnKs(base, []float64{10, 10, 10, 10}, []int64{100, 100, 100, 100}, []float64{1, 2, 3, 0})
	wantNaN(t, "今换手率缺失", f.Value("sh600000", noToday))

	// 历史缺失日不计入分子分母：第 1 日缺失，有效 {2,3,2}，≤2 的有 {2,2} → 2/3
	skip := turnKs(base, []float64{10, 10, 10, 10}, []int64{100, 100, 100, 100}, []float64{0, 2, 3, 2})
	wantVal(t, "历史缺失跳过", f.Value("sh600000", skip), 2.0/3.0, 1e-9)

	// 无成交日（量 0、换手率 0）是合法的 0 值，参与统计
	withZero := turnKs(base, []float64{10, 10, 10, 10}, []int64{100, 0, 100, 100}, []float64{2, 0, 3, 2})
	wantVal(t, "无成交日按0计入", f.Value("sh600000", withZero), 0.75, 1e-9)

	// 数据不足
	wantNaN(t, "数据不足", f.Value("sh600000", ks[:3]))

	if g := (换手率分位{}).Name(); g != "换手率分位(60)" {
		t.Fatalf("默认参数名异常: %s", g)
	}
}

func Test量波动(t *testing.T) {
	base := time.Date(2025, 1, 1, 0, 0, 0, 0, time.Local)

	// vols {100,200,300,400}：均值 250，总体标准差 √12500，cv = √12500/250 = 1/√5
	ks := volKs(base, []float64{10, 10, 10, 10}, []int64{100, 200, 300, 400})
	f := 量波动{Days: 4}
	wantVal(t, f.Name(), f.Value("sh600000", ks), 1/math.Sqrt(5), 1e-9)

	// 恒量 → 0
	flat := volKs(base, []float64{10, 10, 10}, []int64{100, 100, 100})
	wantVal(t, "恒量", (量波动{Days: 3}).Value("sh600000", flat), 0, 1e-12)

	// 窗口全部零量 → 均量 0 → NaN
	zero := volKs(base, []float64{10, 10}, []int64{0, 0})
	wantNaN(t, "均量为0", (量波动{Days: 2}).Value("sh600000", zero))

	// 数据不足
	wantNaN(t, "数据不足", f.Value("sh600000", ks[:3]))

	if g := (量波动{}).Name(); g != "量波动(20)" {
		t.Fatalf("默认参数名异常: %s", g)
	}
}

func Test阳线量比与阴线量比(t *testing.T) {
	base := time.Date(2025, 1, 1, 0, 0, 0, 0, time.Local)

	// [阳100, 阳200, 阴300, 阴400]：均量 250
	// 阳线日均量 150 → 0.6；阴线日均量 350 → 1.4
	ks := ocKs(base,
		[]float64{10, 10, 11, 11},
		[]float64{11, 11, 10, 10},
		[]int64{100, 200, 300, 400})
	up := 阳线量比{Days: 4}
	down := 阴线量比{Days: 4}
	wantVal(t, up.Name(), up.Value("sh600000", ks), 0.6, 1e-9)
	wantVal(t, down.Name(), down.Value("sh600000", ks), 1.4, 1e-9)

	// 平盘日不计入分子：[平100, 阳300] → 阳线日均量 300/均量 200 = 1.5；无阴线 → NaN
	flat := ocKs(base,
		[]float64{10, 10},
		[]float64{10, 11},
		[]int64{100, 300})
	wantVal(t, "平盘不计分子", (阳线量比{Days: 2}).Value("sh600000", flat), 1.5, 1e-9)
	wantNaN(t, "无阴线", (阴线量比{Days: 2}).Value("sh600000", flat))

	// 无阳线 → NaN；阴线量比 = 150/150 = 1
	onlyDown := ocKs(base,
		[]float64{11, 11},
		[]float64{10, 10},
		[]int64{100, 200})
	wantNaN(t, "无阳线", (阳线量比{Days: 2}).Value("sh600000", onlyDown))
	wantVal(t, "仅阴线", (阴线量比{Days: 2}).Value("sh600000", onlyDown), 1, 1e-9)

	// 全零量但有阳线 → 均量 0 → NaN
	allZero := ocKs(base,
		[]float64{10, 10},
		[]float64{11, 11},
		[]int64{0, 0})
	wantNaN(t, "均量为0", (阳线量比{Days: 2}).Value("sh600000", allZero))

	// 数据不足
	wantNaN(t, "数据不足", up.Value("sh600000", ks[:3]))

	if g := (阳线量比{}).Name(); g != "阳线量比(20)" {
		t.Fatalf("默认参数名异常: %s", g)
	}
	if g := (阴线量比{}).Name(); g != "阴线量比(20)" {
		t.Fatalf("默认参数名异常: %s", g)
	}
}
