package portfolioresearch

// target_test.go v2 Task 4 目标组合与约束管线测试。
//
// 覆盖任务要求的 6 类场景 + 完成门槛：
//   - 分数并列稳定选择、候选不足、行业集中、单票上限；
//   - 现有持仓导致换手预算不足（理想 vs 约束后差异）；
//   - 低价/高价股票整手取整；
//   - 现金不足、现金缓冲、零可交易候选；
//   - 约束顺序固定且结果确定；
//   - 权重和、现金（1-权重和）、最小持仓数不变量；
//   - 每步 ConstraintAdjustment 存在且含 before/after/reason/affected；
//   - 完成门槛：无 NaN、无负权重、冲突返回可解释 insufficient。

import (
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"
)

// ---- 测试辅助 ----

// tPolicy 便捷构造组合政策。
func tPolicy(selection string, topN int, quantile, maxStock, maxIndustry, maxTurnover float64, minHoldings int, cashBuffer float64) PortfolioPolicy {
	return PortfolioPolicy{
		Selection:         selection,
		TopN:              topN,
		TopQuantile:       quantile,
		MaxStockWeight:    maxStock,
		MaxIndustryWeight: maxIndustry,
		MaxTurnover:       maxTurnover,
		MinHoldings:       minHoldings,
		CashBuffer:        cashBuffer,
	}
}

// tExec 便捷构造执行参数（LotSize=100，A 股整手）。
func tExec() ExecutionSpec {
	return ExecutionSpec{
		Rebalance: RebalanceWeekly,
		FillAt:    FillNextOpen,
		LotSize:   100,
		Cost: CostSpec{
			CommissionRate:  0.0003,
			StampDutyRate:   0.001,
			TransferFeeRate: 0,
			Slippage:        0.01,
			MinCommission:   5,
		},
	}
}

// tInput 便捷构造输入（Notional=100 万元，日期固定）。
func tInput(scores map[string]float64, tradable map[string]bool, industries map[string]string, prices map[string]float64, holdings map[string]float64, cashWeight float64) TargetInput {
	return TargetInput{
		Date:       "2026-09-18",
		Scores:     scores,
		Tradable:   tradable,
		Industries: industries,
		Prices:     prices,
		Holdings:   holdings,
		CashWeight: cashWeight,
		Notional:   1e6,
	}
}

// mapSum 权重 map 求和（测试辅助）。
func mapSum(m map[string]float64) float64 {
	s := 0.0
	for _, v := range m {
		s += v
	}
	return s
}

// assertIntMap 精确断言 int map。
func assertIntMap(t *testing.T, got, want map[string]int, msg string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: 长度不同 got=%d want=%d（got=%v）", msg, len(got), len(want), got)
	}
	for k, wv := range want {
		gv, ok := got[k]
		if !ok {
			t.Fatalf("%s: 缺少键 %q（got=%v）", msg, k, got)
		}
		if gv != wv {
			t.Fatalf("%s: %s got %d want %d", msg, k, gv, wv)
		}
	}
}

// assertPortfolioInvariants 完成门槛不变量断言：
// 无 NaN/负权重、权重和 ≈ 1-CashBuffer、现金 = 1-ΣEffective ≥ CashBuffer、
// 股数 ≥ 0 且为整手倍数。
func assertPortfolioInvariants(t *testing.T, pw PortfolioWeights, cashBuffer float64, lotSize int) {
	t.Helper()
	targetSum := 1 - cashBuffer
	sumW, sumE := 0.0, 0.0
	for c, w := range pw.Weights {
		if math.IsNaN(w) || math.IsInf(w, 0) {
			t.Fatalf("权重出现 NaN/Inf: %s=%v", c, w)
		}
		if w < -1e-9 {
			t.Fatalf("权重为负（long-only 违例）: %s=%v", c, w)
		}
		sumW += w
		sh, ok := pw.Shares[c]
		if !ok {
			t.Fatalf("权重股票 %s 缺少目标股数", c)
		}
		if sh < 0 || sh%lotSize != 0 {
			t.Fatalf("目标股数非法（应 ≥0 且为整手倍数 %d）: %s=%d", lotSize, c, sh)
		}
	}
	for c, e := range pw.Effective {
		if math.IsNaN(e) || math.IsInf(e, 0) {
			t.Fatalf("可执行权重出现 NaN/Inf: %s=%v", c, e)
		}
		if e < -1e-9 {
			t.Fatalf("可执行权重为负: %s=%v", c, e)
		}
		sumE += e
	}
	if len(pw.Weights) > 0 && math.Abs(sumW-targetSum) > 1e-6 {
		t.Fatalf("权重和 %v 不接近目标 %v（容差 1e-6）", sumW, targetSum)
	}
	if math.Abs(pw.Cash-(1-sumE)) > 1e-9 {
		t.Fatalf("现金 %v ≠ 1-ΣEffective %v", pw.Cash, 1-sumE)
	}
	if pw.Cash < cashBuffer-1e-6 {
		t.Fatalf("现金 %v 低于现金缓冲 %v", pw.Cash, cashBuffer)
	}
}

// wantInsufficient 断言错误为 *InsufficientError 且步骤匹配。
func wantInsufficient(t *testing.T, err error, step string) *InsufficientError {
	t.Helper()
	if err == nil {
		t.Fatalf("期望 *InsufficientError（步骤 %s），实际无错误", step)
	}
	var ie *InsufficientError
	if !errors.As(err, &ie) {
		t.Fatalf("期望 *InsufficientError，实际 %T: %v", err, err)
	}
	if ie.Step != step {
		t.Fatalf("无解步骤 got=%q want=%q（错误: %v）", ie.Step, step, err)
	}
	if ie.Reason == "" {
		t.Fatalf("无解原因不能为空: %v", err)
	}
	return ie
}

// ---- 基础场景与现金缓冲 ----

// TestBaseHappy 基础场景：TopN=3 等权、现金缓冲 0.05、无换手限制。
// 选中 A/B/C（分数最高 3 只），等权 = 0.95/3；理想与约束后一致。
// 整手取整：31600 股（316 手），可执行权重 0.316。
func TestBaseHappy(t *testing.T) {
	pol := tPolicy(PortfolioSelectionTopN, 3, 0, 0, 0, 0, 0, 0.05)
	in := tInput(
		map[string]float64{"A": 3, "B": 2, "C": 1, "D": 0.5, "E": 0.2},
		map[string]bool{"A": true, "B": true, "C": true, "D": true, "E": true},
		map[string]string{"A": "ind1", "B": "ind1", "C": "ind2", "D": "ind2", "E": "ind2"},
		map[string]float64{"A": 10, "B": 10, "C": 10, "D": 10, "E": 10},
		map[string]float64{},
		1.0,
	)
	tp, err := BuildTarget(pol, tExec(), in)
	if err != nil {
		t.Fatalf("BuildTarget 错误: %v", err)
	}
	want := map[string]float64{"A": 0.95 / 3, "B": 0.95 / 3, "C": 0.95 / 3}
	for _, pw := range []PortfolioWeights{tp.Ideal, tp.Constrained} {
		assertFloatMap(t, pw.Weights, want, 1e-6, "等权权重")
		assertFloatMap(t, pw.Effective, map[string]float64{"A": 0.316, "B": 0.316, "C": 0.316}, 1e-9, "整手后可执行权重")
		assertIntMap(t, pw.Shares, map[string]int{"A": 31600, "B": 31600, "C": 31600}, "目标股数")
		assertPortfolioInvariants(t, pw, 0.05, 100)
	}
	if math.Abs(tp.Ideal.Cash-0.052) > 1e-9 {
		t.Fatalf("现金 got=%v want≈0.052", tp.Ideal.Cash)
	}
}

// TestCashBufferApplied 现金缓冲生效：缓冲 0.2 → 权重和 = 0.8，现金 ≥ 0.2。
func TestCashBufferApplied(t *testing.T) {
	pol := tPolicy(PortfolioSelectionTopN, 2, 0, 0, 0, 0, 0, 0.2)
	in := tInput(
		map[string]float64{"A": 3, "B": 2, "C": 1},
		map[string]bool{"A": true, "B": true, "C": true},
		nil,
		map[string]float64{"A": 10, "B": 10, "C": 10},
		map[string]float64{},
		1.0,
	)
	tp, err := BuildTarget(pol, tExec(), in)
	if err != nil {
		t.Fatalf("BuildTarget 错误: %v", err)
	}
	for _, pw := range []PortfolioWeights{tp.Ideal, tp.Constrained} {
		assertFloatMap(t, pw.Weights, map[string]float64{"A": 0.4, "B": 0.4}, 1e-6, "现金缓冲等权权重")
		assertPortfolioInvariants(t, pw, 0.2, 100)
		if math.Abs(pw.Cash-0.2) > 1e-6 {
			t.Fatalf("现金 got=%v want≈0.2", pw.Cash)
		}
	}
}

// TestCashBufferFullCashNoNaN 纯现金配置（CashBuffer=1 → targetSum=0）：换手步
// 直接跳过（无可投资权重），不因 wT 缩放处 targetSum 除零产生 NaN、不误报
// "内部错误"，输出全零权重目标（全额现金）。
func TestCashBufferFullCashNoNaN(t *testing.T) {
	pol := tPolicy(PortfolioSelectionTopN, 2, 0, 0, 0, 0.2, 0, 1.0)
	in := tInput(
		map[string]float64{"A": 3, "B": 2},
		map[string]bool{"A": true, "B": true},
		nil,
		map[string]float64{"A": 10, "B": 10},
		map[string]float64{},
		1.0,
	)
	tp, err := BuildTarget(pol, tExec(), in)
	if err != nil {
		t.Fatalf("BuildTarget 错误: %v", err)
	}
	for _, pw := range []PortfolioWeights{tp.Ideal, tp.Constrained} {
		for c, w := range pw.Weights {
			if math.IsNaN(w) || math.IsInf(w, 0) || math.Abs(w) > 1e-9 {
				t.Fatalf("纯现金配置权重应为 0: %s=%v", c, w)
			}
		}
		if math.Abs(pw.Cash-1) > 1e-9 {
			t.Fatalf("纯现金配置现金 got=%v want=1", pw.Cash)
		}
		assertPortfolioInvariants(t, pw, 1.0, 100)
	}
}

// ---- 选择：并列、分位、候选不足 ----

// TestSelectionTieBreakDeterministic 分数并列时按代码字典序破平，两次调用一致。
func TestSelectionTieBreakDeterministic(t *testing.T) {
	pol := tPolicy(PortfolioSelectionTopN, 3, 0, 0, 0, 0, 0, 0)
	in := tInput(
		map[string]float64{"A": 5, "B": 5, "C": 4, "D": 4, "E": 3},
		map[string]bool{"A": true, "B": true, "C": true, "D": true, "E": true},
		nil,
		map[string]float64{"A": 10, "B": 10, "C": 10, "D": 10, "E": 10},
		map[string]float64{},
		1.0,
	)
	tp1, err := BuildTarget(pol, tExec(), in)
	if err != nil {
		t.Fatalf("BuildTarget 错误: %v", err)
	}
	tp2, err := BuildTarget(pol, tExec(), in)
	if err != nil {
		t.Fatalf("第二次 BuildTarget 错误: %v", err)
	}
	// 并列 5 分的 A/B 按代码升序入选；4 分只选 C（代码序 C 在 D 前）。
	want := map[string]float64{"A": 1.0 / 3, "B": 1.0 / 3, "C": 1.0 / 3}
	assertFloatMap(t, tp1.Ideal.Weights, want, 1e-6, "并列破平选择")
	if !reflect.DeepEqual(tp1, tp2) {
		t.Fatalf("两次调用结果不一致: %+v vs %+v", tp1, tp2)
	}
}

// TestSelectionTopQuantile 最高分位选择：q=0.2 且 n=10 → 选 2 只；q=0.05 → 1 只。
func TestSelectionTopQuantile(t *testing.T) {
	scores := map[string]float64{}
	prices := map[string]float64{}
	tradable := map[string]bool{}
	for i := 1; i <= 10; i++ {
		c := string(rune('A' + i - 1))
		scores[c] = float64(11 - i)
		prices[c] = 10
		tradable[c] = true
	}
	for _, tc := range []struct {
		q    float64
		want int
	}{
		{0.2, 2},
		{0.05, 1},
		{1.0, 10},
	} {
		pol := tPolicy(PortfolioSelectionTopQuantile, 0, tc.q, 0, 0, 0, 0, 0)
		in := tInput(scores, tradable, nil, prices, map[string]float64{}, 1.0)
		tp, err := BuildTarget(pol, tExec(), in)
		if err != nil {
			t.Fatalf("q=%v BuildTarget 错误: %v", tc.q, err)
		}
		if got := len(tp.Ideal.Weights); got != tc.want {
			t.Fatalf("q=%v 入选数量 got=%d want=%d（权重 %v）", tc.q, got, tc.want, tp.Ideal.Weights)
		}
	}
}

// TestSelectionCandidatesInsufficient 候选不足 TopN 时全选；全选仍不足最小持仓数时 insufficient。
func TestSelectionCandidatesInsufficient(t *testing.T) {
	scores := map[string]float64{"A": 2, "B": 1}
	tradable := map[string]bool{"A": true, "B": true}
	prices := map[string]float64{"A": 10, "B": 10}
	// 全选：TopN=5、MinHoldings=0 → 等权 0.475 各。
	pol := tPolicy(PortfolioSelectionTopN, 5, 0, 0, 0, 0, 0, 0.05)
	in := tInput(scores, tradable, nil, prices, map[string]float64{}, 1.0)
	tp, err := BuildTarget(pol, tExec(), in)
	if err != nil {
		t.Fatalf("BuildTarget 错误: %v", err)
	}
	assertFloatMap(t, tp.Ideal.Weights, map[string]float64{"A": 0.475, "B": 0.475}, 1e-6, "候选不足全选")
	// 全选仍不足最小持仓数 → insufficient(selection)。
	pol2 := tPolicy(PortfolioSelectionTopN, 5, 0, 0, 0, 0, 3, 0.05)
	in2 := tInput(scores, tradable, nil, prices, map[string]float64{}, 1.0)
	_, err = BuildTarget(pol2, tExec(), in2)
	wantInsufficient(t, err, StepSelection)
}

// ---- 单票上限与行业上限 ----

// TestStockCapTooTightInsufficient 单票上限过紧：2 只 × 0.4 < 权重和 1 → insufficient(stock_cap)。
func TestStockCapTooTightInsufficient(t *testing.T) {
	pol := tPolicy(PortfolioSelectionTopN, 2, 0, 0.4, 0, 0, 0, 0)
	in := tInput(
		map[string]float64{"A": 3, "B": 2},
		map[string]bool{"A": true, "B": true},
		nil,
		map[string]float64{"A": 10, "B": 10},
		map[string]float64{},
		1.0,
	)
	_, err := BuildTarget(pol, tExec(), in)
	wantInsufficient(t, err, StepStockCap)
}

// TestStockCapRedistributesAfterTradability 可交易预检查剔除 D 后，
// 释放的权重按剩余空间水填充到 A/B/C（单票上限 0.3，0.2+0.2×0.1/0.3=0.2667）。
func TestStockCapRedistributesAfterTradability(t *testing.T) {
	pol := tPolicy(PortfolioSelectionTopN, 4, 0, 0.3, 0, 0, 0, 0.2)
	in := tInput(
		map[string]float64{"A": 4, "B": 3, "C": 2, "D": 1},
		map[string]bool{"A": true, "B": true, "C": true, "D": false},
		nil,
		map[string]float64{"A": 10, "B": 10, "C": 10, "D": 10},
		map[string]float64{},
		1.0,
	)
	tp, err := BuildTarget(pol, tExec(), in)
	if err != nil {
		t.Fatalf("BuildTarget 错误: %v", err)
	}
	// 权重和目标 = 0.8；等权 0.2；D 不可交易 → 其 0.2 按剩余空间 0.1/只 分配给 A/B/C。
	expect := map[string]float64{"A": 0.8 / 3, "B": 0.8 / 3, "C": 0.8 / 3}
	assertFloatMap(t, tp.Ideal.Weights, expect, 1e-6, "剔除 D 后水填充")
	assertFloatMap(t, tp.Constrained.Weights, expect, 1e-6, "剔除 D 后水填充（约束后）")
	// 可交易预检查调整记录：受影响 = [D]。
	adj := adjustmentOf(t, tp.IdealAdjustments, StepTradability)
	if !reflect.DeepEqual(adj.AffectedStocks, []string{"D"}) {
		t.Fatalf("tradability 受影响股票 got=%v want=[D]", adj.AffectedStocks)
	}
}

// TestIndustryCapRedistributes 行业集中超上限：ind1(A,B)=0.6667 > 0.6，
// 行业内按比例缩放到 0.6（A=B=0.3），超额 0.0667 按剩余空间给 C（0.4）。
func TestIndustryCapRedistributes(t *testing.T) {
	pol := tPolicy(PortfolioSelectionTopN, 3, 0, 0.5, 0.6, 0, 0, 0)
	in := tInput(
		map[string]float64{"A": 3, "B": 2, "C": 1},
		map[string]bool{"A": true, "B": true, "C": true},
		map[string]string{"A": "ind1", "B": "ind1", "C": "ind2"},
		map[string]float64{"A": 10, "B": 10, "C": 10},
		map[string]float64{},
		1.0,
	)
	tp, err := BuildTarget(pol, tExec(), in)
	if err != nil {
		t.Fatalf("BuildTarget 错误: %v", err)
	}
	assertFloatMap(t, tp.Ideal.Weights, map[string]float64{"A": 0.3, "B": 0.3, "C": 0.4}, 1e-6, "行业上限水填充")
	if adj := adjustmentOf(t, tp.IdealAdjustments, StepIndustryCap); len(adj.AffectedStocks) == 0 {
		t.Fatalf("industry_cap 步骤应有受影响股票（Before=%v After=%v）", adj.Before, adj.After)
	}
}

// TestIndustryCapTooTightInsufficient 全部股票同一行业且行业上限 < 权重和 → insufficient(industry_cap)。
func TestIndustryCapTooTightInsufficient(t *testing.T) {
	pol := tPolicy(PortfolioSelectionTopN, 3, 0, 0, 0.4, 0, 0, 0)
	in := tInput(
		map[string]float64{"A": 3, "B": 2, "C": 1},
		map[string]bool{"A": true, "B": true, "C": true},
		map[string]string{"A": "ind1", "B": "ind1", "C": "ind1"},
		map[string]float64{"A": 10, "B": 10, "C": 10},
		map[string]float64{},
		1.0,
	)
	_, err := BuildTarget(pol, tExec(), in)
	wantInsufficient(t, err, StepIndustryCap)
}

// ---- 换手预算 ----

// TestTurnoverBudgetLimits 现有持仓导致换手预算不足：
// 当前 A:0.5/B:0.4/现金 0.1，理想 A/B/C 各 0.3，预算 0.1。
// 两阶段求解：D=0 无阶段一；s=0.1/0.3=1/3 → A=0.4333, B=0.3667, C=0.1。
func TestTurnoverBudgetLimits(t *testing.T) {
	pol := tPolicy(PortfolioSelectionTopN, 3, 0, 0, 0, 0.1, 2, 0.1)
	in := tInput(
		map[string]float64{"A": 3, "B": 2, "C": 1},
		map[string]bool{"A": true, "B": true, "C": true},
		nil,
		map[string]float64{"A": 10, "B": 10, "C": 10},
		map[string]float64{"A": 0.5, "B": 0.4},
		0.1,
	)
	tp, err := BuildTarget(pol, tExec(), in)
	if err != nil {
		t.Fatalf("BuildTarget 错误: %v", err)
	}
	ideal := map[string]float64{"A": 0.3, "B": 0.3, "C": 0.3}
	constrained := map[string]float64{"A": 0.433333333333, "B": 0.366666666667, "C": 0.1}
	assertFloatMap(t, tp.Ideal.Weights, ideal, 1e-6, "理想目标（无换手约束）")
	assertFloatMap(t, tp.Constrained.Weights, constrained, 1e-6, "约束后目标")
	if reflect.DeepEqual(tp.Ideal.Weights, tp.Constrained.Weights) {
		t.Fatalf("换手预算应使约束后目标与理想目标不同")
	}
	// 换手口径：单边 = Σ|Δw|/2 ≤ 预算。
	if tw := turnoverOf(tp.Constrained.Weights, in.Holdings); tw > 0.1+1e-9 {
		t.Fatalf("约束后换手 %v 超过预算 0.1", tw)
	}
	assertPortfolioInvariants(t, tp.Constrained, 0.1, 100)
}

// TestTurnoverTwoStageWithSumShift 换手两阶段求解（需要先对齐权重和）：
// 当前 A:0.5/B:0.1/现金 0.4（Σ=0.6），目标权重和 0.9，预算 0.2。
// 阶段一：D=0.3 按 (w*-h)+ 比例投入 → g={0.5,0.22,0.18}；阶段二：s=0.05/0.2=0.25。
// → w={0.45,0.24,0.21}，总换手 = 0.2 = 预算。
func TestTurnoverTwoStageWithSumShift(t *testing.T) {
	pol := tPolicy(PortfolioSelectionTopN, 3, 0, 0, 0, 0.2, 0, 0.1)
	in := tInput(
		map[string]float64{"A": 3, "B": 2, "C": 1},
		map[string]bool{"A": true, "B": true, "C": true},
		nil,
		map[string]float64{"A": 10, "B": 10, "C": 10},
		map[string]float64{"A": 0.5, "B": 0.1},
		0.4,
	)
	tp, err := BuildTarget(pol, tExec(), in)
	if err != nil {
		t.Fatalf("BuildTarget 错误: %v", err)
	}
	want := map[string]float64{"A": 0.45, "B": 0.24, "C": 0.21}
	assertFloatMap(t, tp.Constrained.Weights, want, 1e-6, "两阶段换手求解")
	if tw := turnoverOf(tp.Constrained.Weights, in.Holdings); tw > 0.2+1e-9 {
		t.Fatalf("约束后换手 %v 超过预算 0.2", tw)
	}
	if got := mapSum(tp.Constrained.Weights); math.Abs(got-0.9) > 1e-6 {
		t.Fatalf("约束后权重和 got=%v want≈0.9", got)
	}
}

// TestTurnoverBudgetTooTightInsufficient 换手预算不足以达到目标权重和 → insufficient(turnover)。
// 当前 A:0.1/B:0.1/现金 0.8（Σ=0.2），目标权重和 0.9，至少需要 |0.9-0.2|/2=0.35，预算 0.1。
func TestTurnoverBudgetTooTightInsufficient(t *testing.T) {
	pol := tPolicy(PortfolioSelectionTopN, 2, 0, 0, 0, 0.1, 0, 0.1)
	in := tInput(
		map[string]float64{"A": 3, "B": 2},
		map[string]bool{"A": true, "B": true},
		nil,
		map[string]float64{"A": 10, "B": 10},
		map[string]float64{"A": 0.1, "B": 0.1},
		0.8,
	)
	tp, err := BuildTarget(pol, tExec(), in)
	ie := wantInsufficient(t, err, StepTurnover)
	// 理想目标仍应输出（供归因比较）。
	if len(tp.Ideal.Weights) == 0 {
		t.Fatalf("换手无解时仍应输出理想目标: %+v", tp)
	}
	_ = ie
}

// TestTurnoverOverCapHoldingInsufficient 现有持仓超过单票上限且换手预算不足以修正 → insufficient(turnover)。
// 当前 A:0.35（上限 0.3），预算 0.03 → 只能降到 0.32 > 0.3。
func TestTurnoverOverCapHoldingInsufficient(t *testing.T) {
	pol := tPolicy(PortfolioSelectionTopN, 4, 0, 0.3, 0, 0.03, 0, 0.1)
	in := tInput(
		map[string]float64{"A": 4, "B": 3, "C": 2, "D": 1},
		map[string]bool{"A": true, "B": true, "C": true, "D": true},
		nil,
		map[string]float64{"A": 10, "B": 10, "C": 10, "D": 10},
		map[string]float64{"A": 0.35, "B": 0.2, "C": 0.2, "D": 0.15},
		0.1,
	)
	_, err := BuildTarget(pol, tExec(), in)
	wantInsufficient(t, err, StepTurnover)
}

// ---- 不可交易持仓的换手保护 ----

// TestTurnoverProtectsUntradableHoldings 期初不可交易持仓在换手求解中受保护：
// 候选 A 停牌（Tradable=false）且期初持仓 A=0.3，换手预算有限时约束后目标必须
// 保持 w[A] == 期初 h[A]（不产生"虚拟卖出"），可交易股票在扣除锁定权重后的
// 预算内调整；理想目标（无换手约束）仍按步骤 6 剔除 A 后水填充。
// 期望：步骤 6 水填充 wStar={B:0.3,C:0.3,D:0.3}；锁定 A=0.3 → targetT=0.6 →
// wT 各 0.2；阶段一 g={B:0.3,C:0.2,D:0.1}；阶段二 s2=0.5 →
// w={A:0.3,B:0.25,C:0.2,D:0.15}，总换手 = 0.1 = 预算。
func TestTurnoverProtectsUntradableHoldings(t *testing.T) {
	pol := tPolicy(PortfolioSelectionTopN, 4, 0, 0.4, 0, 0.1, 2, 0.1)
	in := tInput(
		map[string]float64{"A": 4, "B": 3, "C": 2, "D": 1},
		map[string]bool{"A": false, "B": true, "C": true, "D": true},
		nil,
		map[string]float64{"A": 10, "B": 10, "C": 10, "D": 10},
		map[string]float64{"A": 0.3, "B": 0.3, "C": 0.2},
		0.2,
	)
	tp, err := BuildTarget(pol, tExec(), in)
	if err != nil {
		t.Fatalf("BuildTarget 错误: %v", err)
	}
	// 理想目标：步骤 6 剔除 A，B/C/D 水填充到 0.3（不含 A）。
	assertFloatMap(t, tp.Ideal.Weights, map[string]float64{"B": 0.3, "C": 0.3, "D": 0.3}, 1e-6, "理想目标")
	// 约束后目标：不可交易持仓 A 保持期初权重 0.3，可交易股票在预算内调整。
	want := map[string]float64{"A": 0.3, "B": 0.25, "C": 0.2, "D": 0.15}
	assertFloatMap(t, tp.Constrained.Weights, want, 1e-6, "约束后目标")
	if math.Abs(tp.Constrained.Weights["A"]-in.Holdings["A"]) > 1e-9 {
		t.Fatalf("不可交易持仓 A 权重 got=%v want=期初 %v（不得产生虚拟卖出）", tp.Constrained.Weights["A"], in.Holdings["A"])
	}
	if tw := turnoverOf(tp.Constrained.Weights, in.Holdings); tw > 0.1+1e-9 {
		t.Fatalf("约束后换手 %v 超过预算 0.1", tw)
	}
	assertPortfolioInvariants(t, tp.Constrained, 0.1, 100)
	// 换手调整记录：A 换手后仍保持期初权重（Before 无 A，After 并入 A=0.3）。
	adj := adjustmentOf(t, tp.Adjustments, StepTurnover)
	if math.Abs(adj.After["A"]-in.Holdings["A"]) > 1e-9 {
		t.Fatalf("换手调整后 A 权重 got=%v want=期初 %v（不得产生虚拟卖出）", adj.After["A"], in.Holdings["A"])
	}
}

// TestTurnoverUntradableBudgetEnough 换手预算足以覆盖锁定持仓与可交易调整时，
// 直接返回"锁定持仓 + 缩放后可交易目标"（不可交易持仓 A 保持 0.3，
// turnover(ideal, h) = 0.15 ≤ 预算 0.2，直达目标不做两阶段混合）。
func TestTurnoverUntradableBudgetEnough(t *testing.T) {
	pol := tPolicy(PortfolioSelectionTopN, 4, 0, 0.4, 0, 0.2, 2, 0.1)
	in := tInput(
		map[string]float64{"A": 4, "B": 3, "C": 2, "D": 1},
		map[string]bool{"A": false, "B": true, "C": true, "D": true},
		nil,
		map[string]float64{"A": 10, "B": 10, "C": 10, "D": 10},
		map[string]float64{"A": 0.3, "B": 0.3, "C": 0.2},
		0.2,
	)
	tp, err := BuildTarget(pol, tExec(), in)
	if err != nil {
		t.Fatalf("BuildTarget 错误: %v", err)
	}
	want := map[string]float64{"A": 0.3, "B": 0.2, "C": 0.2, "D": 0.2}
	assertFloatMap(t, tp.Constrained.Weights, want, 1e-6, "预算充足直达目标")
	if tw := turnoverOf(tp.Constrained.Weights, in.Holdings); tw > 0.2+1e-9 {
		t.Fatalf("约束后换手 %v 超过预算 0.2", tw)
	}
	assertPortfolioInvariants(t, tp.Constrained, 0.1, 100)
}

// TestTurnoverDirectBranchChecksStockCap 直达分支（换手预算充足）返回前必须校验
// 单票上限：期初不可交易锁定持仓 A=0.35 来自期初组合、不经过步骤 4/5 上限水填充，
// 合并回 ideal 后 A=0.35 > 单票上限 0.3；预算 0.2 ≥ 理想换手 0.1583 走直达分支，
// 必须返回 insufficient(turnover)（不静默输出 A=0.35 的超限目标）。
// 手算：步骤 6 后 wStar={B:0.3,C:0.3,D:0.3}；lockSum=0.35 → targetT=0.55 →
// wT 各 = 0.3×0.55/0.9 = 0.1833；ideal={A:0.35,B/C/D:0.1833}；
// turnover(ideal,h) = (0+0.1167+0.0167+0.1833)/2 = 0.1583 ≤ 0.2。
func TestTurnoverDirectBranchChecksStockCap(t *testing.T) {
	pol := tPolicy(PortfolioSelectionTopN, 4, 0, 0.3, 0, 0.2, 0, 0.1)
	in := tInput(
		map[string]float64{"A": 4, "B": 3, "C": 2, "D": 1},
		map[string]bool{"A": false, "B": true, "C": true, "D": true},
		nil,
		map[string]float64{"A": 10, "B": 10, "C": 10, "D": 10},
		map[string]float64{"A": 0.35, "B": 0.3, "C": 0.2},
		0.15,
	)
	tp, err := BuildTarget(pol, tExec(), in)
	ie := wantInsufficient(t, err, StepTurnover)
	if !strings.Contains(ie.Reason, "单票上限") {
		t.Fatalf("无解原因应指向单票上限: %s", ie.Reason)
	}
	// 理想目标仍应输出（供归因比较）。
	if len(tp.Ideal.Weights) == 0 {
		t.Fatalf("换手无解时仍应输出理想目标: %+v", tp)
	}
}

// TestTurnoverDirectBranchChecksIndustryCap 行业上限同样路径：期初不可交易锁定
// 持仓 A=0.45（行业 ind1）合并回 ideal 后 ind1 权重 0.45 > 行业上限 0.4，
// 预算充足（tw=0.1 ≤ 0.2）走直达分支，必须返回 insufficient(turnover)。
// 手算：步骤 6 后 wStar={B:0.3,C:0.3,D:0.3}（各行业独立、不触限）；
// lockSum=0.45 → targetT=0.45 → wT 各 = 0.3×0.45/0.9 = 0.15；
// ideal={A:0.45,B/C/D:0.15}；turnover(ideal,h) = (0+0.05+0+0.15)/2 = 0.1 ≤ 0.2。
func TestTurnoverDirectBranchChecksIndustryCap(t *testing.T) {
	pol := tPolicy(PortfolioSelectionTopN, 4, 0, 0, 0.4, 0.2, 0, 0.1)
	in := tInput(
		map[string]float64{"A": 4, "B": 3, "C": 2, "D": 1},
		map[string]bool{"A": false, "B": true, "C": true, "D": true},
		map[string]string{"A": "ind1", "B": "ind2", "C": "ind3", "D": "ind4"},
		map[string]float64{"A": 10, "B": 10, "C": 10, "D": 10},
		map[string]float64{"A": 0.45, "B": 0.2, "C": 0.15},
		0.2,
	)
	tp, err := BuildTarget(pol, tExec(), in)
	ie := wantInsufficient(t, err, StepTurnover)
	if !strings.Contains(ie.Reason, "行业上限") {
		t.Fatalf("无解原因应指向行业上限: %s", ie.Reason)
	}
	// 理想目标仍应输出（供归因比较）。
	if len(tp.Ideal.Weights) == 0 {
		t.Fatalf("换手无解时仍应输出理想目标: %+v", tp)
	}
}

// TestTurnoverUntradableHoldingBudgetInsufficient 期初不可交易持仓权重 + 所需调整
// 超过换手预算 → insufficient(turnover)，且 Reason 明确说明不可交易持仓受保护。
// 期初 A=0.5（停牌不可交易）、B=0.2、现金 0.3；目标权重和 0.9，至少需要
// |0.9-0.7|/2=0.1 的换手，预算 0.05 → 无解。
func TestTurnoverUntradableHoldingBudgetInsufficient(t *testing.T) {
	pol := tPolicy(PortfolioSelectionTopN, 3, 0, 0.6, 0, 0.05, 0, 0.1)
	in := tInput(
		map[string]float64{"A": 4, "B": 3, "C": 2},
		map[string]bool{"A": false, "B": true, "C": true},
		nil,
		map[string]float64{"A": 10, "B": 10, "C": 10},
		map[string]float64{"A": 0.5, "B": 0.2},
		0.3,
	)
	tp, err := BuildTarget(pol, tExec(), in)
	ie := wantInsufficient(t, err, StepTurnover)
	if !strings.Contains(ie.Reason, "不可交易") {
		t.Fatalf("无解原因应说明不可交易持仓受保护: %s", ie.Reason)
	}
	// 理想目标仍应输出（供归因比较）。
	if len(tp.Ideal.Weights) == 0 {
		t.Fatalf("换手无解时仍应输出理想目标: %+v", tp)
	}
}

// TestTurnoverUntradableLockedExceedsTarget 期初不可交易持仓权重和超过目标权重和
// （锁定占用全部预算，可交易部分无可分配权重）→ insufficient(turnover)，不静默放宽。
// 期初 A=0.95（停牌不可交易）、现金 0.05；目标权重和 0.9 < 0.95 → 无解。
func TestTurnoverUntradableLockedExceedsTarget(t *testing.T) {
	pol := tPolicy(PortfolioSelectionTopN, 2, 0, 1.0, 0, 0.1, 0, 0.1)
	in := tInput(
		map[string]float64{"A": 4, "B": 3},
		map[string]bool{"A": false, "B": true},
		nil,
		map[string]float64{"A": 10, "B": 10},
		map[string]float64{"A": 0.95},
		0.05,
	)
	tp, err := BuildTarget(pol, tExec(), in)
	ie := wantInsufficient(t, err, StepTurnover)
	if !strings.Contains(ie.Reason, "不可交易持仓权重和") {
		t.Fatalf("无解原因应说明不可交易持仓锁死预算: %s", ie.Reason)
	}
	// 理想目标仍应输出（供归因比较）。
	if len(tp.Ideal.Weights) == 0 {
		t.Fatalf("换手无解时仍应输出理想目标: %+v", tp)
	}
}

// TestTurnoverUntradableLockedAtTargetBoundary 期初不可交易持仓权重和略超目标权重和
// 但在容差（1e-6）内（锁定恰好占满目标），targetT 钳位为 0，不产生微负权重、
// 不误报内部错误；约束后目标 = 锁定持仓（可交易部分权重为 0），可解。
func TestTurnoverUntradableLockedAtTargetBoundary(t *testing.T) {
	pol := tPolicy(PortfolioSelectionTopN, 2, 0, 1.0, 0, 0.1, 0, 0.1)
	in := tInput(
		map[string]float64{"A": 4, "B": 3},
		map[string]bool{"A": false, "B": true},
		nil,
		map[string]float64{"A": 10, "B": 10},
		map[string]float64{"A": 0.9000005},
		0.0999995,
	)
	tp, err := BuildTarget(pol, tExec(), in)
	if err != nil {
		t.Fatalf("BuildTarget 错误: %v", err)
	}
	// 约束后目标：A 保持期初权重（锁定量略超目标，容差内视为占满），B 无可分配权重。
	if math.Abs(tp.Constrained.Weights["A"]-in.Holdings["A"]) > 1e-9 {
		t.Fatalf("不可交易持仓 A 权重 got=%v want=期初 %v", tp.Constrained.Weights["A"], in.Holdings["A"])
	}
	assertPortfolioInvariants(t, tp.Constrained, 0.1, 100)
}

// ---- 整手取整与现金可行性 ----

// TestLotRoundingCheapAndExpensive 低价/高价股票整手向下取整：
// A(价格 30) 目标 475000 元 → 158 手 = 15800 股（可执行权重 0.474）；
// B(价格 5) 目标 475000 元 → 950 手 = 95000 股（精确 0.475）；现金 = 1-0.949 = 0.051。
func TestLotRoundingCheapAndExpensive(t *testing.T) {
	pol := tPolicy(PortfolioSelectionTopN, 2, 0, 0, 0, 0, 0, 0.05)
	in := tInput(
		map[string]float64{"A": 2, "B": 1},
		map[string]bool{"A": true, "B": true},
		nil,
		map[string]float64{"A": 30, "B": 5},
		map[string]float64{},
		1.0,
	)
	tp, err := BuildTarget(pol, tExec(), in)
	if err != nil {
		t.Fatalf("BuildTarget 错误: %v", err)
	}
	for _, pw := range []PortfolioWeights{tp.Ideal, tp.Constrained} {
		assertIntMap(t, pw.Shares, map[string]int{"A": 15800, "B": 95000}, "整手取整股数")
		assertFloatMap(t, pw.Effective, map[string]float64{"A": 0.474, "B": 0.475}, 1e-9, "整手后可执行权重")
		if math.Abs(pw.Cash-0.051) > 1e-9 {
			t.Fatalf("现金 got=%v want≈0.051", pw.Cash)
		}
		assertPortfolioInvariants(t, pw, 0.05, 100)
	}
	// 整手取整调整记录存在。
	adj := adjustmentOf(t, tp.IdealAdjustments, StepLotRounding)
	if len(adj.AffectedStocks) == 0 {
		t.Fatalf("lot_rounding 步骤应有受影响股票（A 产生整手残留）")
	}
}

// TestCashInsufficientDropsLowScoreBuy 现金不足：B(价格 6000) 目标金额 50 万 < 一手 60 万，
// 按分数优先级（B 分数低）放弃买入；A 正常买入。MinHoldings=2 时无解。
func TestCashInsufficientDropsLowScoreBuy(t *testing.T) {
	prices := map[string]float64{"A": 10, "B": 6000}
	scores := map[string]float64{"A": 2, "B": 1}
	tradable := map[string]bool{"A": true, "B": true}
	// MinHoldings=0：B 放弃买入，可执行持仓只剩 A。
	pol := tPolicy(PortfolioSelectionTopN, 2, 0, 0, 0, 0, 0, 0)
	in := tInput(scores, tradable, nil, prices, map[string]float64{}, 1.0)
	tp, err := BuildTarget(pol, tExec(), in)
	if err != nil {
		t.Fatalf("BuildTarget 错误: %v", err)
	}
	for _, pw := range []PortfolioWeights{tp.Ideal, tp.Constrained} {
		assertIntMap(t, pw.Shares, map[string]int{"A": 50000, "B": 0}, "现金不足缩减股数")
		assertFloatMap(t, pw.Effective, map[string]float64{"A": 0.5, "B": 0}, 1e-9, "现金不足缩减可执行权重")
		// 分析权重保留研究意图（Σ=1），可执行权重把 B 减为 0。
		assertPortfolioInvariants(t, pw, 0, 100)
	}
	adj := adjustmentOf(t, tp.IdealAdjustments, StepCashFeasibility)
	if !reflect.DeepEqual(adj.AffectedStocks, []string{"B"}) {
		t.Fatalf("cash_feasibility 受影响股票 got=%v want=[B]", adj.AffectedStocks)
	}
	// MinHoldings=2：可执行持仓 1 < 2 → insufficient(cash_feasibility)。
	pol2 := tPolicy(PortfolioSelectionTopN, 2, 0, 0, 0, 0, 2, 0)
	in2 := tInput(scores, tradable, nil, prices, map[string]float64{}, 1.0)
	_, err = BuildTarget(pol2, tExec(), in2)
	wantInsufficient(t, err, StepCashFeasibility)
}

// ---- 零可交易候选 ----

// TestZeroTradableCandidates 零可交易候选：MinHoldings>0 → insufficient(tradability)；
// MinHoldings=0 → 空目标（当日跳过，全额现金）。
func TestZeroTradableCandidates(t *testing.T) {
	scores := map[string]float64{"A": 3, "B": 2, "C": 1}
	allFalse := map[string]bool{"A": false, "B": false, "C": false}
	prices := map[string]float64{"A": 10, "B": 10, "C": 10}
	// MinHoldings=3 → 可交易 0 < 3 → insufficient。
	pol := tPolicy(PortfolioSelectionTopN, 3, 0, 0, 0, 0, 3, 0.05)
	in := tInput(scores, allFalse, nil, prices, map[string]float64{}, 1.0)
	_, err := BuildTarget(pol, tExec(), in)
	wantInsufficient(t, err, StepTradability)
	// MinHoldings=0 → 空目标。
	pol0 := tPolicy(PortfolioSelectionTopN, 3, 0, 0, 0, 0, 0, 0.05)
	in0 := tInput(scores, allFalse, nil, prices, map[string]float64{}, 1.0)
	tp, err := BuildTarget(pol0, tExec(), in0)
	if err != nil {
		t.Fatalf("BuildTarget 错误: %v", err)
	}
	for _, pw := range []PortfolioWeights{tp.Ideal, tp.Constrained} {
		if len(pw.Weights) != 0 || len(pw.Shares) != 0 {
			t.Fatalf("空目标应无权重/股数: %+v", pw)
		}
		if math.Abs(pw.Cash-1) > 1e-9 {
			t.Fatalf("空目标现金 got=%v want=1", pw.Cash)
		}
	}
}

// TestEmptyScoresWithNoMinHoldings 无任何候选分数且 MinHoldings=0 → 空目标，不报错。
func TestEmptyScoresWithNoMinHoldings(t *testing.T) {
	pol := tPolicy(PortfolioSelectionTopN, 3, 0, 0, 0, 0, 0, 0.05)
	in := tInput(map[string]float64{}, map[string]bool{}, nil, map[string]float64{}, map[string]float64{}, 1.0)
	tp, err := BuildTarget(pol, tExec(), in)
	if err != nil {
		t.Fatalf("BuildTarget 错误: %v", err)
	}
	if len(tp.Ideal.Weights) != 0 {
		t.Fatalf("空选日应输出空目标: %+v", tp.Ideal)
	}
}

// ---- 约束顺序、确定性与调整记录 ----

// TestAdjustmentStepsOrderedAndComplete 约束顺序固定：9 步（整手与现金可行化拆为两步记录），
// 理想目标不含换手预算步；每步记录含 before/after/reason/affected。
func TestAdjustmentStepsOrderedAndComplete(t *testing.T) {
	pol := tPolicy(PortfolioSelectionTopN, 3, 0, 0.5, 0.6, 0.1, 0, 0.1)
	in := tInput(
		map[string]float64{"A": 3, "B": 2, "C": 1},
		map[string]bool{"A": true, "B": true, "C": true},
		map[string]string{"A": "ind1", "B": "ind1", "C": "ind2"},
		map[string]float64{"A": 10, "B": 10, "C": 10},
		map[string]float64{"A": 0.3, "B": 0.3, "C": 0.3},
		0.1,
	)
	tp, err := BuildTarget(pol, tExec(), in)
	if err != nil {
		t.Fatalf("BuildTarget 错误: %v", err)
	}
	wantSteps := []string{
		StepRanking, StepSelection, StepEqualWeight, StepStockCap, StepIndustryCap,
		StepTradability, StepTurnover, StepLotRounding, StepCashFeasibility, StepTarget,
	}
	gotSteps := make([]string, len(tp.Adjustments))
	for i, a := range tp.Adjustments {
		gotSteps[i] = a.Step
	}
	if !reflect.DeepEqual(gotSteps, wantSteps) {
		t.Fatalf("约束步骤顺序 got=%v want=%v", gotSteps, wantSteps)
	}
	// 理想目标 = 约束后步骤减去换手预算步。
	wantIdeal := append([]string{}, wantSteps[:6]...)
	wantIdeal = append(wantIdeal, wantSteps[7:]...)
	gotIdeal := make([]string, len(tp.IdealAdjustments))
	for i, a := range tp.IdealAdjustments {
		gotIdeal[i] = a.Step
	}
	if !reflect.DeepEqual(gotIdeal, wantIdeal) {
		t.Fatalf("理想目标步骤 got=%v want=%v", gotIdeal, wantIdeal)
	}
	// 每步记录完整性。
	for _, a := range append(append([]ConstraintAdjustment{}, tp.IdealAdjustments...), tp.Adjustments...) {
		if a.Before == nil || a.After == nil {
			t.Fatalf("步骤 %s 缺 before/after", a.Step)
		}
		if a.Reason == "" {
			t.Fatalf("步骤 %s 缺 reason", a.Step)
		}
		if a.AffectedStocks == nil {
			t.Fatalf("步骤 %s 缺 affected", a.Step)
		}
	}
}

// TestDeterministic 同输入两次调用逐位一致（含调整记录）。
func TestDeterministic(t *testing.T) {
	pol := tPolicy(PortfolioSelectionTopN, 3, 0, 0.5, 0.6, 0.1, 0, 0.1)
	in := tInput(
		map[string]float64{"A": 3, "B": 2, "C": 1, "D": 0.5},
		map[string]bool{"A": true, "B": true, "C": true, "D": true},
		map[string]string{"A": "ind1", "B": "ind1", "C": "ind2", "D": "ind2"},
		map[string]float64{"A": 10, "B": 10, "C": 10, "D": 10},
		map[string]float64{"A": 0.4, "B": 0.3, "C": 0.2},
		0.1,
	)
	tp1, err := BuildTarget(pol, tExec(), in)
	if err != nil {
		t.Fatalf("BuildTarget 错误: %v", err)
	}
	tp2, err := BuildTarget(pol, tExec(), in)
	if err != nil {
		t.Fatalf("第二次 BuildTarget 错误: %v", err)
	}
	if !reflect.DeepEqual(tp1, tp2) {
		t.Fatalf("同输入两次调用结果不一致:\n%+v\n%+v", tp1, tp2)
	}
}

// TestIdealEqualsConstrainedWhenUnlimited 无换手限制（MaxTurnover=0）时理想与约束后一致。
func TestIdealEqualsConstrainedWhenUnlimited(t *testing.T) {
	pol := tPolicy(PortfolioSelectionTopN, 2, 0, 0, 0, 0, 0, 0.05)
	in := tInput(
		map[string]float64{"A": 3, "B": 2},
		map[string]bool{"A": true, "B": true},
		nil,
		map[string]float64{"A": 10, "B": 10},
		map[string]float64{"A": 0.4, "B": 0.3},
		0.3,
	)
	tp, err := BuildTarget(pol, tExec(), in)
	if err != nil {
		t.Fatalf("BuildTarget 错误: %v", err)
	}
	if !reflect.DeepEqual(tp.Ideal, tp.Constrained) {
		t.Fatalf("无换手限制时理想与约束后应一致:\n%+v\n%+v", tp.Ideal, tp.Constrained)
	}
}

// TestMinHoldingsInvariant 最小持仓数不变量：可解场景约束后持仓数 ≥ MinHoldings。
func TestMinHoldingsInvariant(t *testing.T) {
	pol := tPolicy(PortfolioSelectionTopN, 4, 0, 0, 0, 0.1, 2, 0.1)
	in := tInput(
		map[string]float64{"A": 4, "B": 3, "C": 2, "D": 1},
		map[string]bool{"A": true, "B": true, "C": true, "D": true},
		nil,
		map[string]float64{"A": 10, "B": 10, "C": 10, "D": 10},
		map[string]float64{"A": 0.3, "B": 0.3, "C": 0.3},
		0.1,
	)
	tp, err := BuildTarget(pol, tExec(), in)
	if err != nil {
		t.Fatalf("BuildTarget 错误: %v", err)
	}
	for name, pw := range map[string]PortfolioWeights{"理想": tp.Ideal, "约束后": tp.Constrained} {
		if got := len(pw.Weights); got < 2 {
			t.Fatalf("%s 持仓数 got=%d < MinHoldings=2", name, got)
		}
	}
}

// ---- 输入校验 ----

// TestInputValidationErrors 非法输入直接报错（fail closed）。
func TestInputValidationErrors(t *testing.T) {
	pol := tPolicy(PortfolioSelectionTopN, 2, 0, 0, 0, 0, 0, 0.05)
	// NaN 分数。
	badScore := tInput(map[string]float64{"A": math.NaN()}, map[string]bool{"A": true}, nil, map[string]float64{"A": 10}, map[string]float64{}, 1.0)
	if _, err := BuildTarget(pol, tExec(), badScore); err == nil {
		t.Fatal("NaN 分数应报错")
	}
	// 期初持仓 + 现金 ≠ 1。
	badHoldings := tInput(map[string]float64{"A": 1}, map[string]bool{"A": true}, nil, map[string]float64{"A": 10}, map[string]float64{"A": 0.5}, 0.4)
	if _, err := BuildTarget(pol, tExec(), badHoldings); err == nil {
		t.Fatal("持仓权重和 + 现金 ≠ 1 应报错")
	}
	// 负持仓权重。
	negHoldings := tInput(map[string]float64{"A": 1}, map[string]bool{"A": true}, nil, map[string]float64{"A": 10}, map[string]float64{"A": -0.1}, 1.1)
	if _, err := BuildTarget(pol, tExec(), negHoldings); err == nil {
		t.Fatal("负持仓权重应报错")
	}
	// Notional 非法。
	badNotional := tInput(map[string]float64{"A": 1}, map[string]bool{"A": true}, nil, map[string]float64{"A": 10}, map[string]float64{}, 1.0)
	badNotional.Notional = 0
	if _, err := BuildTarget(pol, tExec(), badNotional); err == nil {
		t.Fatal("Notional=0 应报错")
	}
	// 选中股票缺价格（行业上限未启用，价格在整手步骤校验）。
	noPrice := tInput(map[string]float64{"A": 2, "B": 1}, map[string]bool{"A": true, "B": true}, nil, map[string]float64{"A": 10}, map[string]float64{}, 1.0)
	if _, err := BuildTarget(pol, tExec(), noPrice); err == nil {
		t.Fatal("选中股票缺价格应报错")
	}
	// 行业上限启用时选中股票缺行业。
	polInd := tPolicy(PortfolioSelectionTopN, 2, 0, 0, 0.5, 0, 0, 0.05)
	noInd := tInput(map[string]float64{"A": 2, "B": 1}, map[string]bool{"A": true, "B": true}, map[string]string{"A": "ind1"}, map[string]float64{"A": 10, "B": 10}, map[string]float64{}, 1.0)
	if _, err := BuildTarget(polInd, tExec(), noInd); err == nil {
		t.Fatal("行业上限启用时选中股票缺行业应报错")
	}
	// 行业上限启用时期初持仓缺行业（持仓股票不在候选中，只触发 Holdings 校验）。
	polIndHold := tPolicy(PortfolioSelectionTopN, 2, 0, 0, 0.5, 0, 0, 0.05)
	noIndHold := tInput(map[string]float64{"A": 2, "B": 1}, map[string]bool{"A": true, "B": true}, map[string]string{"A": "ind1", "B": "ind2"}, map[string]float64{"A": 10, "B": 10}, map[string]float64{"X": 0.5}, 0.5)
	if _, err := BuildTarget(polIndHold, tExec(), noIndHold); err == nil {
		t.Fatal("行业上限启用时期初持仓缺行业应报错")
	}
}

// ---- 辅助 ----

// adjustmentOf 取指定步骤的调整记录。
func adjustmentOf(t *testing.T, adjustments []ConstraintAdjustment, step string) ConstraintAdjustment {
	t.Helper()
	for _, a := range adjustments {
		if a.Step == step {
			return a
		}
	}
	t.Fatalf("缺少步骤 %s 的调整记录（全部: %v）", step, adjustments)
	return ConstraintAdjustment{}
}

// turnoverOf 单边换手 = Σ|Δw|/2（测试独立实现，双保险校验实现口径正确性）。
func turnoverOf(w, h map[string]float64) float64 {
	keys := map[string]bool{}
	for c := range w {
		keys[c] = true
	}
	for c := range h {
		keys[c] = true
	}
	s := 0.0
	for c := range keys {
		s += math.Abs(w[c] - h[c])
	}
	return s / 2
}
