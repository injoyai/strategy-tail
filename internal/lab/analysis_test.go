package lab

import (
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/injoyai/strategy-tail/internal/researchrun"
)

func nearlyEq(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

// analysisDays 构造 year 年 month 月起的 n 个连续自然日（仅作为日期键使用）。
func analysisDays(year, month, n int) []time.Time {
	days := make([]time.Time, 0, n)
	for i := 0; i < n; i++ {
		days = append(days, time.Date(year, time.Month(month), i+1, 0, 0, 0, 0, time.Local))
	}
	return days
}

// mockICSample 构造多段逐日观测：每段 3 只股票，dir>0 时因子名次与收益同向
// （Spearman IC=+1），dir<0 时反向（IC=−1）。
func mockICSample(days [][]time.Time, dirs []float64) (map[time.Time]map[string]float64, map[time.Time]map[string]float64) {
	vals := map[time.Time]map[string]float64{}
	rets := map[time.Time]map[string]float64{}
	for i, seg := range days {
		for _, day := range seg {
			vals[day] = map[string]float64{}
			rets[day] = map[string]float64{}
			for j, code := range []string{"sh600001", "sh600002", "sh600003"} {
				v := float64(j + 1)
				r := v
				if dirs[i] < 0 {
					r = -v
				}
				vals[day][code] = v
				rets[day][code] = r
			}
		}
	}
	return vals, rets
}

// TestAggregateICTradeDayWeighted 全区间 IC 按交易日等权：交易日多的年份权重更大，
// 不得先算年度均值再等权（12 日 vs 24 日那样会让两年各占一半权重）。
func TestAggregateICTradeDayWeighted(t *testing.T) {
	y1 := analysisDays(2024, 3, 12) // 12 个 IC=+1 交易日
	y2 := analysisDays(2025, 4, 24) // 24 个 IC=−1 交易日
	vals, rets := mockICSample([][]time.Time{y1, y2}, []float64{1, -1})
	all := append(append([]time.Time{}, y1...), y2...)

	got := aggregateIC(all, vals, rets)
	if len(got.Daily) != 36 || got.Stats.Pairs != 36 {
		t.Fatalf("全区间 Daily/Pairs = %d/%d, want 36/36", len(got.Daily), got.Stats.Pairs)
	}
	// 手算 (12×1 + 24×(−1))/36 = −1/3；按年度均值等权会得到 0
	if !nearlyEq(got.Stats.Mean, -1.0/3) {
		t.Fatalf("全区间 Mean = %v, want -1/3（交易日等权）", got.Stats.Mean)
	}
	if nearlyEq(got.Stats.Mean, 0) {
		t.Fatal("全区间不得等于年度均值的简单平均")
	}
	// 逐日 IC 序列按升序日期完整输出（无效日为 null）
	if got.Daily[0].Date != "2024-03-01" || got.Daily[35].Date != "2025-04-24" {
		t.Fatalf("Daily 首末 = %s/%s", got.Daily[0].Date, got.Daily[35].Date)
	}

	groups := groupDaysByYear(all)
	if len(groups) != 2 || len(groups[0]) != 12 || len(groups[1]) != 24 {
		t.Fatalf("groupDaysByYear = %v", groups)
	}
	if s := aggregateIC(groups[0], vals, rets).Stats; s.Pairs != 12 || !nearlyEq(s.Mean, 1) {
		t.Fatalf("2024 年度 = %+v, want Pairs 12 Mean 1", s)
	}
	if s := aggregateIC(groups[1], vals, rets).Stats; s.Pairs != 24 || !nearlyEq(s.Mean, -1) {
		t.Fatalf("2025 年度 = %+v, want Pairs 24 Mean -1", s)
	}
	if p := aggregateIC(groups[0], vals, rets).Stats.Pairs + aggregateIC(groups[1], vals, rets).Stats.Pairs; p != got.Stats.Pairs {
		t.Fatalf("年度有效日之和 %d ≠ 全区间 %d（应来自同一批观测）", p, got.Stats.Pairs)
	}
}

// TestAggregateICPairsKeptWhenInsufficient 有效日不足 minPairs 时保留样本数、
// 逐日 IC 仍完整，不得用 0 冒充“真实 IC 为零”。
func TestAggregateICPairsKeptWhenInsufficient(t *testing.T) {
	days := analysisDays(2026, 1, 5)
	vals, rets := mockICSample([][]time.Time{days}, []float64{1})
	got := aggregateIC(days, vals, rets)
	if len(got.Daily) != 5 || got.Stats.Pairs != 5 {
		t.Fatalf("Daily/Pairs = %d/%d, want 5/5（保留样本数）", len(got.Daily), got.Stats.Pairs)
	}
	if got.Stats.Mean != 0 || got.Stats.Std != 0 || got.Stats.TStat != 0 {
		t.Fatalf("样本不足的汇总应为 0，由 Pairs 表达不足: %+v", got.Stats)
	}
	for _, d := range got.Daily {
		if d.IC == nil || !nearlyEq(*d.IC, 1) {
			t.Fatalf("逐日 IC 应保留: %+v", d)
		}
	}
}

// TestGroupDaysByYear 升序交易日按自然年切连续分组；空输入返回 nil。
func TestGroupDaysByYear(t *testing.T) {
	if g := groupDaysByYear(nil); g != nil {
		t.Fatalf("空输入应返回 nil: %v", g)
	}
	days := []time.Time{
		time.Date(2024, 12, 30, 0, 0, 0, 0, time.Local),
		time.Date(2024, 12, 31, 0, 0, 0, 0, time.Local),
		time.Date(2025, 1, 2, 0, 0, 0, 0, time.Local),
		time.Date(2025, 1, 3, 0, 0, 0, 0, time.Local),
		time.Date(2025, 1, 6, 0, 0, 0, 0, time.Local),
	}
	g := groupDaysByYear(days)
	if len(g) != 2 || len(g[0]) != 2 || len(g[1]) != 3 {
		t.Fatalf("分组规模 = %v", g)
	}
	if !g[0][0].Equal(days[0]) || !g[1][2].Equal(days[4]) {
		t.Fatalf("分组顺序被破坏: %v", g)
	}
}

// TestSortFailures 失败明细按 code/year/stage 排序，报告与测试可稳定比对。
func TestSortFailures(t *testing.T) {
	fs := []researchrun.Failure{
		{Code: "sh600002", Year: 2025, Stage: "day"},
		{Code: "sh600001", Year: 2025, Stage: "period"},
		{Code: "sh600001", Year: 2024, Stage: "day"},
		{Code: "sh600001", Year: 2024, Stage: "minute"},
	}
	sortFailures(fs)
	want := []struct {
		code  string
		year  int
		stage string
	}{
		{"sh600001", 2024, "day"}, {"sh600001", 2024, "minute"},
		{"sh600001", 2025, "period"}, {"sh600002", 2025, "day"},
	}
	for i, w := range want {
		if fs[i].Code != w.code || fs[i].Year != w.year || fs[i].Stage != w.stage {
			t.Fatalf("fs[%d] = %+v, want %+v", i, fs[i], w)
		}
	}
}

// TestAvgRanks 升序名次、并列取均值。
func TestAvgRanks(t *testing.T) {
	got := avgRanks([]float64{1, 2, 2, 3})
	want := []float64{1, 2.5, 2.5, 4}
	for i := range want {
		if !nearlyEq(got[i], want[i]) {
			t.Fatalf("avgRanks[%d] = %v, want %v", i, got[i], want[i])
		}
	}
}

// TestSpearmanIC 完全单调 ±1、并列衰减、弱相关确定性小值。
func TestSpearmanIC(t *testing.T) {
	vals := []float64{1, 2, 3, 4, 5}
	if ic := spearmanIC(vals, []float64{0.1, 0.2, 0.3, 0.4, 0.5}); !nearlyEq(ic, 1) {
		t.Fatalf("同向 IC = %v, want 1", ic)
	}
	if ic := spearmanIC(vals, []float64{0.5, 0.4, 0.3, 0.2, 0.1}); !nearlyEq(ic, -1) {
		t.Fatalf("反向 IC = %v, want -1", ic)
	}
	// 并列：因子 [1,2,2,3]（平均名次 [1,2.5,2.5,4]）对 [0.1,0.3,0.2,0.4]（名次 [1,3,2,4]）
	// 的 Pearson = 4.5/√22.5 = 3/√10 ≈ 0.9487
	if ic := spearmanIC([]float64{1, 2, 2, 3}, []float64{0.1, 0.3, 0.2, 0.4}); !nearlyEq(ic, 3/math.Sqrt(10)) {
		t.Fatalf("并列 IC = %v, want %v", ic, 3/math.Sqrt(10))
	}
	// 弱相关（排名 [3,5,1,2,4]）：手算 Pearson = -0.1
	if ic := spearmanIC([]float64{3, 5, 1, 2, 4}, vals); !nearlyEq(ic, -0.1) {
		t.Fatalf("弱相关 IC = %v, want -0.1", ic)
	}
}

// TestICStats 均值/总体标准差/t 统计量；样本不足全 0；NaN 剔除。
func TestICStats(t *testing.T) {
	ics := make([]float64, 10)
	for i := range ics {
		if i%2 == 0 {
			ics[i] = 0.1
		} else {
			ics[i] = 0.3
		}
	}
	s := icStats(ics)
	if s.Pairs != 10 || !nearlyEq(s.Mean, 0.2) || !nearlyEq(s.Std, 0.1) {
		t.Fatalf("icStats = %+v", s)
	}
	// t = 0.2/(0.1/√10) = 2√10
	if math.Abs(s.TStat-2*math.Sqrt(10)) > 1e-9 {
		t.Fatalf("TStat = %v", s.TStat)
	}

	// 有效样本 9 < minPairs → 全 0
	short := append([]float64{0.5}, make([]float64, 8)...)
	if s = icStats(short); s.Pairs != 9 || s.Mean != 0 || s.Std != 0 || s.TStat != 0 {
		t.Fatalf("样本不足应全 0: %+v", s)
	}

	// NaN 剔除后仍达 minPairs
	s = icStats([]float64{0.1, 0.3, math.NaN(), 0.1, 0.3, 0.1, 0.3, 0.1, 0.3, 0.1, 0.3})
	if s.Pairs != 10 || !nearlyEq(s.Mean, 0.2) {
		t.Fatalf("NaN 未剔除: %+v", s)
	}
}

// TestGroupingValidate 分组配置校验：缺省 quantile、bins 四断点严格递增。
func TestGroupingValidate(t *testing.T) {
	// 缺省（mode 空）与显式 quantile 合法，且 cuts 必须为空
	if err := (GroupingConfig{}).Validate(); err != nil {
		t.Fatalf("空配置应合法: %v", err)
	}
	if err := (GroupingConfig{Mode: "quantile"}).Validate(); err != nil {
		t.Fatalf("quantile 应合法: %v", err)
	}
	for _, c := range []GroupingConfig{
		{Cuts: []float64{1, 2, 3, 4}},                                  // mode 空带 cuts
		{Mode: "quantile", Cuts: []float64{1, 2, 3, 4}},                // quantile 带 cuts
		{Mode: "median"},                                               // 未知模式
		{Mode: "bins", Cuts: []float64{1, 2, 3}},                       // 断点数不足
		{Mode: "bins", Cuts: []float64{1, 2, 3, 4, 5}},                 // 断点数超出
		{Mode: "bins"},                                                 // 缺断点
		{Mode: "bins", Cuts: []float64{1, math.NaN(), 3, 4}},           // NaN
		{Mode: "bins", Cuts: []float64{1, 2, math.Inf(1), 4}},          // Inf
		{Mode: "bins", Cuts: []float64{1, 3, 2, 4}},                    // 非递增
		{Mode: "bins", Cuts: []float64{0, 0, 1, 2}},                    // 重复
		{Mode: "bins", Cuts: []float64{math.Copysign(0, -1), 0, 1, 2}}, // -0 与 0 重复
	} {
		if err := c.Validate(); err == nil {
			t.Fatalf("应报错: %+v", c)
		}
	}
	// 合法 bins
	if err := (GroupingConfig{Mode: "bins", Cuts: []float64{-0.05, -0.02, 0, 0.02}}).Validate(); err != nil {
		t.Fatalf("合法 bins 应通过: %v", err)
	}
	// 分组数：缺省 0、2、20 合法；越界非法；bins 断点数 = 组数-1
	for _, c := range []GroupingConfig{
		{Groups: 1}, {Groups: 21}, {Groups: -3},
		{Mode: "quantile", Groups: 7, Cuts: []float64{1, 2}}, // quantile 不接受断点
		{Mode: "bins", Groups: 7, Cuts: []float64{1, 2, 3, 4}}, // 7 组需 6 断点
	} {
		if err := c.Validate(); err == nil {
			t.Fatalf("应报错: %+v", c)
		}
	}
	for _, c := range []GroupingConfig{
		{Groups: 0}, {Groups: 2}, {Groups: 20},
		{Mode: "quantile", Groups: 7},
		{Mode: "bins", Groups: 7, Cuts: []float64{1, 2, 3, 4, 5, 6}},
	} {
		if err := c.Validate(); err != nil {
			t.Fatalf("应合法: %+v: %v", c, err)
		}
	}
	if got := (GroupingConfig{}).groupCount(); got != 5 {
		t.Fatalf("缺省 groupCount = %d, want 5", got)
	}
	if got := (GroupingConfig{Groups: 7}).groupCount(); got != 7 {
		t.Fatalf("groupCount = %d, want 7", got)
	}
}

// TestQuantileAssign 分位分组纯函数：等频、并列块不拆分、确定性。
func TestQuantileAssign(t *testing.T) {
	// 唯一值 n=10：值升序两组一组，组号 [0,0,1,1,2,2,3,3,4,4]
	obs := make([]factorObs, 10)
	for i := range obs {
		obs[i] = factorObs{Code: fmt.Sprintf("sh%06d", i+1), Value: float64(i + 1)}
	}
	g := quantileAssign(obs, 5)
	for i, want := range []int{0, 0, 1, 1, 2, 2, 3, 3, 4, 4} {
		if g[i] != want {
			t.Fatalf("g[%d] = %d, want %d", i, g[i], want)
		}
	}
	// 输入乱序：组号跟随观测（按 code 核对）
	shuffled := []factorObs{obs[3], obs[9], obs[0], obs[6], obs[1], obs[8], obs[2], obs[5], obs[4], obs[7]}
	gs := quantileAssign(shuffled, 5)
	for i, o := range shuffled {
		want := int(o.Value-1) / 2 // 值 1..10 → 组 0..4
		if gs[i] != want {
			t.Fatalf("乱序 g[%d](code=%s) = %d, want %d", i, o.Code, gs[i], want)
		}
	}
	// n=7 不能被五整除：组大小 [2,1,2,1,1]（中点公式 floor(k*5/7)）
	seven := make([]factorObs, 7)
	for i := range seven {
		seven[i] = factorObs{Code: fmt.Sprintf("c%d", i), Value: float64(i + 1)}
	}
	g = quantileAssign(seven, 5)
	sizes := make([]int, 5)
	for _, gi := range g {
		sizes[gi]++
	}
	for i, want := range []int{2, 1, 2, 1, 1} {
		if sizes[i] != want {
			t.Fatalf("n=7 组大小 = %v, want [2 1 2 1 1]", sizes)
		}
	}
	// 并列跨边界：6 个 1（块归 Q2）+ 4 个 2（块归 Q4），块不拆分
	tie := make([]factorObs, 10)
	for i := range tie {
		v := 1.0
		if i >= 6 {
			v = 2.0
		}
		tie[i] = factorObs{Code: fmt.Sprintf("t%d", i), Value: v}
	}
	g = quantileAssign(tie, 5)
	for i, gi := range g {
		want := 1
		if i >= 6 {
			want = 3
		}
		if gi != want {
			t.Fatalf("并列块 g[%d] = %d, want %d（整块不拆分）", i, gi, want)
		}
	}
	// 全部相等：整块按中点归 Q3
	same := make([]factorObs, 5)
	for i := range same {
		same[i] = factorObs{Code: fmt.Sprintf("s%d", i), Value: 7}
	}
	if g = quantileAssign(same, 5); g[0] != 2 {
		t.Fatalf("全相等应归 Q3, got %v", g)
	}
	// 确定性：相同输入多次运行组成员完全一致
	first := quantileAssign(tie, 5)
	for run := 0; run < 50; run++ {
		again := quantileAssign(tie, 5)
		for i := range first {
			if first[i] != again[i] {
				t.Fatalf("第 %d 次运行结果不一致", run)
			}
		}
	}
	// g=7：n=10 唯一值 → 组号 floor(i*7/10)：[0,0,1,2,2,3,4,4,5,6]
	g7 := quantileAssign(obs, 7)
	for i, want := range []int{0, 0, 1, 2, 2, 3, 4, 4, 5, 6} {
		if g7[i] != want {
			t.Fatalf("g7[%d] = %d, want %d", i, g7[i], want)
		}
	}
	// g=7、n=3：票数不足组数 → 只占用位置 0,2,4（floor(i*7/6)：0,2,4）
	three := make([]factorObs, 3)
	for i := range three {
		three[i] = factorObs{Code: fmt.Sprintf("c%d", i), Value: float64(i + 1)}
	}
	gt := quantileAssign(three, 7)
	for i, want := range []int{0, 2, 4} {
		if gt[i] != want {
			t.Fatalf("gt[%d] = %d, want %d", i, gt[i], want)
		}
	}
}

// TestBinsAssign 固定区间分组：断点值归左组，(lower, upper] 语义，开放端。
func TestBinsAssign(t *testing.T) {
	cuts := []float64{-0.05, -0.02, 0, 0.02}
	cases := []struct {
		v    float64
		want int
	}{
		{-0.06, 0}, {-0.05, 0}, {-0.049, 1}, {-0.02, 1}, {-0.019, 2},
		{0, 2}, {0.019, 3}, {0.02, 3}, {0.021, 4}, {100, 4},
	}
	for _, c := range cases {
		if got := binGroup(c.v, cuts); got != c.want {
			t.Fatalf("binGroup(%v) = %d, want %d", c.v, got, c.want)
		}
	}
}

// TestPercentileType7 Type-7 线性插值百分位手算用例（输入须已升序）。
func TestPercentileType7(t *testing.T) {
	cases := []struct {
		x             []float64
		p25, med, p75 float64
	}{
		{[]float64{5}, 5, 5, 5},                   // n=1
		{[]float64{1, 3}, 1.5, 2, 2.5},            // n=2
		{[]float64{1, 2, 4}, 1.5, 2, 3},           // n=3 奇数
		{[]float64{1, 2, 3, 10}, 1.75, 2.5, 4.75}, // n=4 偶数
	}
	for i, c := range cases {
		for _, w := range []struct {
			p    float64
			want float64
		}{{0.25, c.p25}, {0.5, c.med}, {0.75, c.p75}} {
			if got := percentileSorted(c.x, w.p); !nearlyEq(got, w.want) {
				t.Fatalf("case %d p=%v: %v, want %v", i, w.p, got, w.want)
			}
		}
	}
}

// TestFactorGroupStats 组内因子值分布统计：空组 nil、单组、总体标准差。
func TestFactorGroupStats(t *testing.T) {
	if s := valueStats(nil); s != nil {
		t.Fatal("空输入应返回 nil")
	}
	// 单样本：P25=Median=P75=值，总体 std=0
	s := valueStats([]float64{5})
	if s == nil || !nearlyEq(s.Min, 5) || !nearlyEq(s.P25, 5) || !nearlyEq(s.Median, 5) ||
		!nearlyEq(s.Mean, 5) || !nearlyEq(s.P75, 5) || !nearlyEq(s.Max, 5) || s.Std != 0 {
		t.Fatalf("单样本统计 = %+v", s)
	}
	// n=4：x=[1,2,3,10] mean=4，总体 var=(9+4+1+36)/4=12.5
	s = valueStats([]float64{10, 1, 3, 2}) // 乱序输入，函数内部排序
	if s == nil || !nearlyEq(s.Min, 1) || !nearlyEq(s.Max, 10) || !nearlyEq(s.Mean, 4) ||
		!nearlyEq(s.P25, 1.75) || !nearlyEq(s.Median, 2.5) || !nearlyEq(s.P75, 4.75) {
		t.Fatalf("n=4 统计 = %+v", s)
	}
	if !nearlyEq(s.Std, math.Sqrt(12.5)) {
		t.Fatalf("总体 Std = %v, want √12.5", s.Std)
	}
}
