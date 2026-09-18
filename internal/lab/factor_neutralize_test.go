package lab

import (
	"math"
	"reflect"
	"strings"
	"testing"
)

// neutralSpec 便捷构造。
func neutralSpec(mode, winsor, std string) NeutralizationSpec {
	return NeutralizationSpec{Mode: mode, Winsorization: winsor, Standardization: std, IndustryDataset: "ind", SizeDataset: "size"}
}

func TestNeutralizeNoneZScore(t *testing.T) {
	obs := []ExposureObservation{
		{Code: "a", Value: 1}, {Code: "b", Value: 2}, {Code: "c", Value: 3},
		{Code: "d", Value: 4}, {Code: "e", Value: 5},
	}
	res := neutralizeCrossSection(obs, neutralSpec(neutralizationNone, winsorizationNone, standardizationZScore))
	if res.Status != neutralizationStatusOK || len(res.Values) != 5 {
		t.Fatalf("res = %+v", res)
	}
	// 手算：mean=3, std=sqrt(2.5)，z(a)=(1-3)/sqrt(2.5)≈-1.2649
	if want := -2 / math.Sqrt(2.5); math.Abs(res.Values["a"]-want) > 1e-12 {
		t.Fatalf("z(a) = %v, want %v", res.Values["a"], want)
	}
	sum := 0.0
	for _, v := range res.Values {
		sum += v
	}
	if math.Abs(sum) > 1e-12 {
		t.Fatalf("标准化后均值应为 0: %v", sum)
	}

	// 常数序列：无截面信息，unavailable 且 Values 为空（Null 语义）。
	res2 := neutralizeCrossSection(
		[]ExposureObservation{{Code: "a", Value: 7}, {Code: "b", Value: 7}},
		neutralSpec(neutralizationNone, winsorizationNone, standardizationZScore))
	if res2.Status != neutralizationStatusUnavailable || res2.Values != nil || res2.Reason == "" {
		t.Fatalf("常数序列应 unavailable: %+v", res2)
	}
}

func TestNeutralizeNoneRankWithTies(t *testing.T) {
	obs := []ExposureObservation{
		{Code: "a", Value: 10}, {Code: "b", Value: 30}, {Code: "c", Value: 20},
		{Code: "d", Value: 5}, {Code: "e", Value: 25},
	}
	res := neutralizeCrossSection(obs, neutralSpec(neutralizationNone, winsorizationNone, standardizationRank))
	if res.Status != neutralizationStatusOK {
		t.Fatalf("res = %+v", res)
	}
	// 秩 1..5：z = (r-3)/sqrt(24/12) = (r-3)/sqrt(2)；a=10 秩2、b=30 秩5、c=20 秩3、d=5 秩1、e=25 秩4。
	cases := map[string]float64{"a": -1 / math.Sqrt2, "b": 2 / math.Sqrt2, "c": 0, "d": -2 / math.Sqrt2, "e": 1 / math.Sqrt2}
	for code, want := range cases {
		if math.Abs(res.Values[code]-want) > 1e-12 {
			t.Fatalf("z(%s) = %v, want %v", code, res.Values[code], want)
		}
	}

	// 并列值取平均秩：[5,5,9] → 秩 1.5/1.5/3。
	res2 := neutralizeCrossSection(
		[]ExposureObservation{{Code: "a", Value: 5}, {Code: "b", Value: 5}, {Code: "c", Value: 9}},
		neutralSpec(neutralizationNone, winsorizationNone, standardizationRank))
	if res2.Status != neutralizationStatusOK || res2.Values["a"] != res2.Values["b"] {
		t.Fatalf("并列值应有相同 z: %+v", res2)
	}
	scale := math.Sqrt(8.0 / 12)
	if want := (1.5 - 2) / scale; math.Abs(res2.Values["a"]-want) > 1e-12 {
		t.Fatalf("平均秩 z = %v, want %v", res2.Values["a"], want)
	}
}

func TestNeutralizeMADWinsorizeClipsOutlier(t *testing.T) {
	vals := []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 1000}
	obs := make([]ExposureObservation, len(vals))
	for i, v := range vals {
		obs[i] = ExposureObservation{Code: string(rune('a' + i)), Value: v}
	}
	res := neutralizeCrossSection(obs, neutralSpec(neutralizationNone, winsorizationMAD, standardizationZScore))
	if res.Status != neutralizationStatusOK {
		t.Fatalf("res = %+v", res)
	}
	// 离群点被 clip：其 z 仍最大但不再发散（未 clip 时 |z| 远超 10）。
	maxZ := res.Values[string(rune('a'+19))]
	if !(maxZ > 1.5 && maxZ < 3.2) {
		t.Fatalf("离群点 z = %v, 应被 clip 到 3σ 附近", maxZ)
	}
	if res.Values[string(rune('a'+18))] >= maxZ {
		t.Fatalf("clip 不应破坏排序: %+v", res.Values)
	}
	// Notes 不得出现 MAD 退化披露（本例 MAD=5 非零）。
	for _, n := range res.Notes {
		if n == "MAD 为零，跳过去极值" {
			t.Fatalf("不应披露 MAD 退化: %v", res.Notes)
		}
	}
}

func TestNeutralizeIndustrySizeResidualOrthogonal(t *testing.T) {
	// 构造强行业效应 + 规模效应的因子值：残差应与截距/行业/log(size) 正交。
	obs := make([]ExposureObservation, 0, 30)
	for i := 0; i < 30; i++ {
		ind := "ind_a"
		if i%3 == 1 {
			ind = "ind_b"
		} else if i%3 == 2 {
			ind = "ind_c"
		}
		size := 100.0 * float64(i+1)
		value := 0.0
		switch ind { // 行业效应
		case "ind_a":
			value += 5
		case "ind_b":
			value += -3
		case "ind_c":
			value += 2
		}
		value += 0.01 * size // 规模效应
		if i == 7 {
			value += 0.37 // 一点噪声，避免完美拟合掩盖 bug
		}
		obs = append(obs, ExposureObservation{Code: code6(i), Value: value, Industry: ind, Size: size})
	}
	res := neutralizeCrossSection(obs, neutralSpec(neutralizationIndustrySize, winsorizationNone, standardizationZScore))
	if res.Status != neutralizationStatusOK || len(res.Values) != 30 {
		t.Fatalf("res = %+v", res)
	}
	// 正交性 1：残差均值为 0（含截距）。
	sum := 0.0
	for _, v := range res.Values {
		sum += v
	}
	if math.Abs(sum) > 1e-9 {
		t.Fatalf("残差均值应≈0: %v", sum)
	}
	// 正交性 2：残差对 [1, 行业 dummy, log(size)] 再回归，系数应≈0。
	// 等价做法：按行业分组的残差组内均值≈0；残差与 log(size) 的内积≈0。
	groupSum := map[string]float64{"ind_a": 0, "ind_b": 0, "ind_c": 0}
	dotSize := 0.0
	for i, o := range obs {
		r := res.Values[o.Code]
		groupSum[o.Industry] += r
		dotSize += r * math.Log(o.Size)
		_ = i
	}
	for ind, s := range groupSum {
		if math.Abs(s) > 1e-9 {
			t.Fatalf("行业 %s 残差组和应≈0: %v", ind, s)
		}
	}
	if math.Abs(dotSize) > 1e-7 {
		t.Fatalf("残差与 log(size) 内积应≈0: %v", dotSize)
	}
	// 中性化应显著压缩行业间均值差：原始因子组间均值差 > 残差组间均值差。
	origA, origB := 0.0, 0.0
	residA, residB := 0.0, 0.0
	for _, o := range obs {
		switch o.Industry {
		case "ind_a":
			origA += o.Value
			residA += res.Values[o.Code]
		case "ind_b":
			origB += o.Value
			residB += res.Values[o.Code]
		}
	}
	if math.Abs(origA-origB) <= math.Abs(residA-residB) {
		t.Fatalf("中性化应压缩行业均值差: 原始 %v vs 残差 %v", math.Abs(origA-origB), math.Abs(residA-residB))
	}
}

func code6(i int) string {
	return "c" + string(rune('0'+i/10)) + string(rune('0'+i%10))
}

func TestNeutralizeIndustrySizeInsufficientAndCollinear(t *testing.T) {
	mk := func(n int, sameSize bool) []ExposureObservation {
		obs := make([]ExposureObservation, 0, n)
		for i := 0; i < n; i++ {
			size := 100.0 * float64(i+1)
			if sameSize {
				size = 500
			}
			obs = append(obs, ExposureObservation{
				Code: code6(i), Value: float64(i), Industry: "ind_a", Size: size,
			})
		}
		return obs
	}
	// 样本不足：单行业需要 n>=max(10, 2*2)=10。
	res := neutralizeCrossSection(mk(9, false), neutralSpec(neutralizationIndustrySize, winsorizationNone, standardizationZScore))
	if res.Status != neutralizationStatusUnavailable || res.Values != nil {
		t.Fatalf("n=9 应样本不足: %+v", res)
	}
	// 单行业 + 市值全同：log(size) 与截距共线 → 奇异。
	res = neutralizeCrossSection(mk(20, true), neutralSpec(neutralizationIndustrySize, winsorizationNone, standardizationZScore))
	if res.Status != neutralizationStatusUnavailable || res.Values != nil {
		t.Fatalf("共线应 unavailable: %+v", res)
	}
	// 单行业 + 正常市值：设计矩阵 [1, logsize]，应成功。
	res = neutralizeCrossSection(mk(20, false), neutralSpec(neutralizationIndustrySize, winsorizationNone, standardizationZScore))
	if res.Status != neutralizationStatusOK || len(res.Values) != 20 {
		t.Fatalf("单行业回归应成功: %+v", res)
	}
}

func TestNeutralizeMissingExposureDisclosed(t *testing.T) {
	obs := make([]ExposureObservation, 0, 14)
	for i := 0; i < 12; i++ {
		obs = append(obs, ExposureObservation{
			Code: code6(i), Value: float64(i), Industry: "ind_a", Size: 100 * float64(i+1),
		})
	}
	obs = append(obs,
		ExposureObservation{Code: "zz1", Value: 5, Industry: "", Size: 100},    // 缺行业
		ExposureObservation{Code: "zz2", Value: 6, Industry: "ind_a", Size: 0}, // 市值非正
	)
	res := neutralizeCrossSection(obs, neutralSpec(neutralizationIndustrySize, winsorizationNone, standardizationZScore))
	if res.Status != neutralizationStatusOK {
		t.Fatalf("res = %+v", res)
	}
	if res.Dropped != 2 || res.Used != 12 {
		t.Fatalf("Used/Dropped = %d/%d, want 12/2", res.Used, res.Dropped)
	}
	// 缺暴露的代码不得出现在输出（不零填充）。
	if _, ok := res.Values["zz1"]; ok {
		t.Fatal("缺行业代码不应有值")
	}
	if _, ok := res.Values["zz2"]; ok {
		t.Fatal("缺市值代码不应有值")
	}
	joined := ""
	for _, n := range res.Notes {
		joined += n + ";"
	}
	if !strings.Contains(joined, "行业缺失丢弃: 1") || !strings.Contains(joined, "市值缺失丢弃: 1") {
		t.Fatalf("应披露缺失: %v", res.Notes)
	}
}

func TestNeutralizeInvalidValuesFiltered(t *testing.T) {
	obs := []ExposureObservation{
		{Code: "a", Value: math.NaN()},
		{Code: "b", Value: math.Inf(1)},
		{Code: " ", Value: 1},
		{Code: "c", Value: 2}, {Code: "d", Value: 3}, {Code: "e", Value: 4},
		{Code: "f", Value: 5}, {Code: "g", Value: 6},
	}
	res := neutralizeCrossSection(obs, neutralSpec(neutralizationNone, winsorizationNone, standardizationZScore))
	if res.Status != neutralizationStatusOK || res.Used != 5 || res.Dropped != 3 {
		t.Fatalf("res = %+v", res)
	}
	for _, bad := range []string{"a", "b", " "} {
		if _, ok := res.Values[bad]; ok {
			t.Fatalf("无效代码 %q 不应有值", bad)
		}
	}
}

func TestNeutralizeOrderInvariance(t *testing.T) {
	mk := func() []ExposureObservation {
		obs := make([]ExposureObservation, 0, 24)
		for i := 0; i < 24; i++ {
			ind := "ind_a"
			if i%2 == 1 {
				ind = "ind_b"
			}
			obs = append(obs, ExposureObservation{
				Code: code6(i), Value: float64((i*37)%23) - 11, Industry: ind, Size: 50 + 10*float64(i),
			})
		}
		return obs
	}
	spec := neutralSpec(neutralizationIndustrySize, winsorizationMAD, standardizationRank)
	base := neutralizeCrossSection(mk(), spec)
	if base.Status != neutralizationStatusOK {
		t.Fatalf("base = %+v", base)
	}
	shuffled := mk()
	// 逆序 + 交换若干对，构成不同输入顺序。
	for i, j := 0, len(shuffled)-1; i < j; i, j = i+1, j-1 {
		shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
	}
	alt := neutralizeCrossSection(shuffled, spec)
	if !reflect.DeepEqual(base, alt) {
		t.Fatalf("顺序不变性破坏:\nbase = %+v\nalt  = %+v", base, alt)
	}
}

func TestNeutralizeIllegalSpec(t *testing.T) {
	res := neutralizeCrossSection([]ExposureObservation{{Code: "a", Value: 1}}, neutralSpec("quantile", winsorizationNone, standardizationRank))
	if res.Status != neutralizationStatusUnavailable || res.Reason == "" {
		t.Fatalf("非法模式应 unavailable: %+v", res)
	}
	res = neutralizeCrossSection([]ExposureObservation{{Code: "a", Value: 1}}, neutralSpec(neutralizationNone, winsorizationNone, "quantile"))
	if res.Status != neutralizationStatusUnavailable {
		t.Fatalf("非法标准化应 unavailable: %+v", res)
	}
}
