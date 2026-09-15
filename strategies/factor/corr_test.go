package factor

import (
	"math"
	"testing"
	"time"
)

func TestPearson(t *testing.T) {
	// 完全正相关
	if v := Pearson([]float64{1, 2, 3, 4, 5}, []float64{2, 4, 6, 8, 10}); math.Abs(v-1) > 1e-9 {
		t.Fatalf("Pearson = %v, want 1", v)
	}
	// 完全负相关
	if v := Pearson([]float64{1, 2, 3, 4, 5}, []float64{10, 8, 6, 4, 2}); math.Abs(v+1) > 1e-9 {
		t.Fatalf("Pearson = %v, want -1", v)
	}
	// 零方差 → NaN
	wantNaN(t, "零方差", Pearson([]float64{1, 2, 3}, []float64{5, 5, 5}))
	// 长度不等 → NaN
	wantNaN(t, "长度不等", Pearson([]float64{1, 2}, []float64{1, 2, 3}))
	// 空数据 → NaN
	wantNaN(t, "空", Pearson(nil, nil))
	// xs 侧零方差 → NaN
	wantNaN(t, "xs零方差", Pearson([]float64{5, 5, 5}, []float64{1, 2, 3}))
}

func Test量价相关(t *testing.T) {
	base := time.Date(2025, 1, 1, 0, 0, 0, 0, time.Local)

	// 量随价同比例放大 → r=1
	cs := []float64{1, 2, 3, 4, 5}
	vols := []int64{100, 200, 300, 400, 500}
	ks := volKs(base, cs, vols)
	f := 量价相关{Days: 5}
	wantVal(t, f.Name(), f.Value("sh600000", ks), 1, 1e-9)

	// 恒量 → 零方差 → NaN
	flat := closes(base, 1, 2, 3, 4, 5)
	wantNaN(t, "恒量", f.Value("sh600000", flat))

	// 数据不足
	wantNaN(t, "数据不足", f.Value("sh600000", ks[:4]))

	if g := (量价相关{}).Name(); g != "量价相关(20)" {
		t.Fatalf("默认参数名异常: %s", g)
	}
}
