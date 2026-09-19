package portfolioresearch

// attribution_test.go v2 Task 6 预测归因与组合归因测试（attribution.go）。
//
// 覆盖任务测试列表：
//   - 贡献求和（股票+现金+成本 = 组合收益，容差内；恒等式构造性断言）；
//   - 信号选择收益与执行偏离损失（实际-理想）；
//   - 行业贡献聚合（含缺行业归 unknown）；
//   - 现金贡献（现金比例 × 每日无风险利率，rf=0 时为 0）；
//   - 目标/实际权重偏离时间序列（实际 - 目标，Σ|偏离|）；
//   - 归因限制声明字段存在（统计分解非因果证明/非完整风险模型）；
//   - 留一法组合维度（收益/换手/回撤/IC 变化，差值 = LOO - Full）；
//   - 账本 → 留一比较用表现结果（与 ComputeMetrics 一致）；
//   - 预测归因视图组装（Redundancy + LOO + Limitation）。
//
// 恒等式（文档化，算术累计口径，容差 attributionTolerance=1e-9）：
//   StockCashSum = Σ股票贡献 + 现金贡献；
//   NetReturn    = StockCashSum - CostDrag；
//   ExecutionDeviation = StockCashSum - SelectionReturn（IdealWeights 缺失时 nil）。

import (
	"fmt"
	"math"
	"strings"
	"testing"
)

// ---- 贡献求和：股票+现金+成本 = 组合收益（容差内）----

func TestAttributionSumIdentity(t *testing.T) {
	// day1：A 0.6×0.10 + B 0.3×(-0.05) + 现金 0.1×0 = 0.045（无费用）；
	// day2：A 0.5×0.05 + B 0.4×0.10 + 现金 0.1×0 = 0.065，费用 1.0/100 = 0.01。
	// 股票贡献：A = 0.085，B = 0.025；现金 = 0；StockCashSum = 0.11；
	// CostDrag = 1/100 = 0.01；NetReturn = 0.10。
	in := AttributionInput{
		InitialEquity: 100,
		Days: []AttributionDay{
			{
				Date:          "2026-01-02",
				ActualWeights: map[string]float64{"A": 0.6, "B": 0.3},
				CashRatio:     0.1,
				Returns:       map[string]float64{"A": 0.10, "B": -0.05},
				GrossNav:      104.5, NetNav: 104.5,
			},
			{
				Date:          "2026-01-05",
				ActualWeights: map[string]float64{"A": 0.5, "B": 0.4},
				CashRatio:     0.1,
				Returns:       map[string]float64{"A": 0.05, "B": 0.10},
				Fees:          1.0,
				GrossNav:      111.2925, NetNav: 110.2925,
			},
		},
	}
	pa, err := ComputeAttribution(in)
	if err != nil {
		t.Fatalf("ComputeAttribution 失败: %v", err)
	}
	if len(pa.Stocks) != 2 {
		t.Fatalf("股票贡献数 got %d want 2", len(pa.Stocks))
	}
	if !approx(pa.Stocks[0].Contribution, 0.085, 1e-12) || !approx(pa.Stocks[1].Contribution, 0.025, 1e-12) {
		t.Fatalf("股票贡献 got %v,%v want 0.085,0.025", pa.Stocks[0].Contribution, pa.Stocks[1].Contribution)
	}
	if pa.Stocks[0].Code != "A" || pa.Stocks[1].Code != "B" {
		t.Fatalf("股票代码顺序 got %s,%s want A,B", pa.Stocks[0].Code, pa.Stocks[1].Code)
	}
	if !approx(pa.CashContribution, 0, 1e-12) {
		t.Fatalf("现金贡献（rf=0）got %.12g want 0", pa.CashContribution)
	}
	if !approx(pa.StockCashSum, 0.11, 1e-12) {
		t.Fatalf("毛归因收益 got %.12g want 0.11", pa.StockCashSum)
	}
	if !approx(pa.CostDrag, 0.01, 1e-12) {
		t.Fatalf("成本拖累 got %.12g want 0.01", pa.CostDrag)
	}
	if !approx(pa.NetReturn, 0.10, 1e-12) {
		t.Fatalf("净归因收益 got %.12g want 0.10", pa.NetReturn)
	}
	if !pa.Reconciled {
		t.Fatalf("恒等式应在容差内成立: StockCashSum=%.12g CostDrag=%.12g NetReturn=%.12g",
			pa.StockCashSum, pa.CostDrag, pa.NetReturn)
	}
	// 净值口径对照（复利）：毛 111.2925/100-1 = 0.112925；净 110.2925/100-1 = 0.102925。
	if pa.PortfolioGrossReturn == nil || !approx(*pa.PortfolioGrossReturn, 0.112925, 1e-9) {
		t.Fatalf("净值口径毛累计 got %v want 0.112925", pa.PortfolioGrossReturn)
	}
	if pa.PortfolioNetReturn == nil || !approx(*pa.PortfolioNetReturn, 0.102925, 1e-9) {
		t.Fatalf("净值口径净累计 got %v want 0.102925", pa.PortfolioNetReturn)
	}
	// 残差 = 复利交叉项（day1 的 4.5% 在 day2 复利）：0.102925 - 0.10 = 0.002925。
	if pa.Residual == nil || !approx(*pa.Residual, 0.002925, 1e-9) {
		t.Fatalf("归因残差 got %v want 0.002925", pa.Residual)
	}
}

// ---- 信号选择收益与执行偏离损失（实际-理想）----

func TestAttributionSelectionExecution(t *testing.T) {
	// 实际 A 0.5/B 0.5 → 收益 0.5×0.10+0.5×0.05 = 0.075；
	// 理想 A 0.8/B 0.2 → 0.8×0.10+0.2×0.05 = 0.09；
	// 执行偏离 = 0.075 - 0.09 = -0.015（执行偏离损失）。
	in := AttributionInput{
		InitialEquity: 100,
		Days: []AttributionDay{
			{
				Date:          "2026-01-02",
				ActualWeights: map[string]float64{"A": 0.5, "B": 0.5},
				Returns:       map[string]float64{"A": 0.10, "B": 0.05},
				IdealWeights:  map[string]float64{"A": 0.8, "B": 0.2},
			},
		},
	}
	pa, err := ComputeAttribution(in)
	if err != nil {
		t.Fatalf("ComputeAttribution 失败: %v", err)
	}
	if !approx(pa.StockCashSum, 0.075, 1e-12) {
		t.Fatalf("实际毛收益 got %.12g want 0.075", pa.StockCashSum)
	}
	if pa.SelectionReturn == nil || !approx(*pa.SelectionReturn, 0.09, 1e-12) {
		t.Fatalf("信号选择收益 got %v want 0.09", pa.SelectionReturn)
	}
	if pa.ExecutionDeviation == nil || !approx(*pa.ExecutionDeviation, -0.015, 1e-12) {
		t.Fatalf("执行偏离 got %v want -0.015", pa.ExecutionDeviation)
	}
}

// ---- 行业贡献聚合（含缺行业归 unknown）----

func TestAttributionIndustry(t *testing.T) {
	in := AttributionInput{
		InitialEquity: 100,
		Days: []AttributionDay{
			{
				Date:          "2026-01-02",
				ActualWeights: map[string]float64{"A": 0.4, "B": 0.3, "C": 0.2},
				CashRatio:     0.1,
				Returns:       map[string]float64{"A": 0.10, "B": 0.05, "C": 0.02},
				Industries:    map[string]string{"A": "X", "B": "Y", "C": "X"},
			},
			{
				Date:          "2026-01-05",
				ActualWeights: map[string]float64{"A": 0.4, "B": 0.3, "C": 0.2},
				CashRatio:     0.1,
				Returns:       map[string]float64{"A": 0.0, "B": 0.0, "C": 0.0},
				Industries:    map[string]string{"A": "X", "B": "Y", "C": "X"},
			},
		},
	}
	pa, err := ComputeAttribution(in)
	if err != nil {
		t.Fatalf("ComputeAttribution 失败: %v", err)
	}
	// 股票贡献：A=0.04、B=0.015、C=0.004；行业 X=0.044（2 只）、Y=0.015（1 只）。
	if len(pa.Industries) != 2 {
		t.Fatalf("行业数 got %d want 2", len(pa.Industries))
	}
	x, y := pa.Industries[0], pa.Industries[1]
	if x.Industry != "X" || !approx(x.Contribution, 0.044, 1e-12) || x.StockCount != 2 {
		t.Fatalf("行业 X got %+v want contribution 0.044 count 2", x)
	}
	if y.Industry != "Y" || !approx(y.Contribution, 0.015, 1e-12) || y.StockCount != 1 {
		t.Fatalf("行业 Y got %+v want contribution 0.015 count 1", y)
	}
}

// ---- 现金贡献：现金比例 × 每日无风险利率（rf>0）；rf=0 时为 0 ----

func TestAttributionCashRF(t *testing.T) {
	in := AttributionInput{
		InitialEquity: 100,
		RiskFreeRate:  0.05, // 年化 5%
		Days: []AttributionDay{
			{
				Date:          "2026-01-02",
				ActualWeights: map[string]float64{"A": 0.8},
				CashRatio:     0.2,
				Returns:       map[string]float64{"A": 0.01},
			},
		},
	}
	pa, err := ComputeAttribution(in)
	if err != nil {
		t.Fatalf("ComputeAttribution 失败: %v", err)
	}
	wantCash := 0.2 * (math.Pow(1.05, 1.0/252) - 1)
	if !approx(pa.CashContribution, wantCash, 1e-15) {
		t.Fatalf("现金贡献 got %.15g want %.15g", pa.CashContribution, wantCash)
	}
	if !approx(pa.Stocks[0].Contribution, 0.008, 1e-12) {
		t.Fatalf("股票贡献 got %.12g want 0.008", pa.Stocks[0].Contribution)
	}
	// rf=0 → 现金贡献恒 0。
	in.RiskFreeRate = 0
	pa0, err := ComputeAttribution(in)
	if err != nil {
		t.Fatalf("ComputeAttribution 失败: %v", err)
	}
	if !approx(pa0.CashContribution, 0, 1e-12) {
		t.Fatalf("rf=0 现金贡献应 0，got %.12g", pa0.CashContribution)
	}
}

// ---- 目标/实际权重偏离时间序列 ----

func TestAttributionWeightSeries(t *testing.T) {
	in := AttributionInput{
		InitialEquity: 100,
		Days: []AttributionDay{
			{
				Date:          "2026-01-02",
				ActualWeights: map[string]float64{"A": 0.4, "B": 0.4},
				CashRatio:     0.2,
				TargetWeights: map[string]float64{"A": 0.5, "B": 0.3},
				Returns:       map[string]float64{"A": 0.0, "B": 0.0},
			},
		},
	}
	pa, err := ComputeAttribution(in)
	if err != nil {
		t.Fatalf("ComputeAttribution 失败: %v", err)
	}
	if len(pa.WeightSeries) != 1 {
		t.Fatalf("权重偏离序列长度 got %d want 1", len(pa.WeightSeries))
	}
	w := pa.WeightSeries[0]
	if !approx(w.Deviation["A"], -0.1, 1e-12) || !approx(w.Deviation["B"], 0.1, 1e-12) {
		t.Fatalf("偏离 got A=%.12g B=%.12g want -0.1,0.1", w.Deviation["A"], w.Deviation["B"])
	}
	if !approx(w.SumAbsDeviation, 0.2, 1e-12) {
		t.Fatalf("Σ|偏离| got %.12g want 0.2", w.SumAbsDeviation)
	}
	// 未提供目标权重 → 不产出偏离序列。
	in.Days[0].TargetWeights = nil
	pa2, err := ComputeAttribution(in)
	if err != nil {
		t.Fatalf("ComputeAttribution 失败: %v", err)
	}
	if len(pa2.WeightSeries) != 0 {
		t.Fatalf("无目标权重应无偏离序列，got %d", len(pa2.WeightSeries))
	}
}

// ---- 归因限制声明字段存在 ----

func TestAttributionLimitation(t *testing.T) {
	pa, err := ComputeAttribution(AttributionInput{InitialEquity: 100})
	if err != nil {
		t.Fatalf("ComputeAttribution 失败: %v", err)
	}
	if pa.Limitation == "" {
		t.Fatalf("归因必须带限制声明")
	}
	if !strings.Contains(pa.Limitation, "统计分解") {
		t.Fatalf("限制声明应明确统计分解: %q", pa.Limitation)
	}
	if !strings.Contains(pa.Limitation, "因果") {
		t.Fatalf("限制声明应明确非因果证明: %q", pa.Limitation)
	}
	if !strings.Contains(pa.Limitation, "风险模型") {
		t.Fatalf("限制声明应明确非完整风险模型: %q", pa.Limitation)
	}
}

// ---- 输入校验：非法 rf fail closed（返回 error，含字段与范围，不产生 NaN 报告）----

func TestAttributionInvalidRiskFreeRate(t *testing.T) {
	base := AttributionInput{
		InitialEquity: 100,
		Days: []AttributionDay{
			{Date: "2026-01-02", ActualWeights: map[string]float64{"A": 0.9}, CashRatio: 0.1,
				Returns: map[string]float64{"A": 0.01}},
		},
	}
	bad := []struct {
		name string
		rf   float64
	}{
		{"rf=-1.5（底数非正）", -1.5},
		{"rf=-1（边界拒绝）", -1},
		{"rf=NaN", math.NaN()},
		{"rf=+Inf", math.Inf(1)},
		{"rf=-Inf", math.Inf(-1)},
	}
	for _, tc := range bad {
		in := base
		in.RiskFreeRate = tc.rf
		pa, err := ComputeAttribution(in)
		if err == nil {
			t.Fatalf("%s 应返回错误，got 无错误", tc.name)
		}
		if !strings.Contains(err.Error(), "RiskFreeRate") {
			t.Fatalf("%s 错误应含字段名: %v", tc.name, err)
		}
		if pa.CashContribution != 0 || pa.NetReturn != 0 {
			t.Fatalf("%s 不应产生归因输出（NaN 污染）: %+v", tc.name, pa)
		}
	}
	// rf=-1.5 的错误应含允许范围说明。
	_, err := ComputeAttribution(AttributionInput{RiskFreeRate: -1.5})
	if err == nil || !strings.Contains(err.Error(), "> -1") {
		t.Fatalf("rf=-1.5 错误应含允许范围: %v", err)
	}
}

// ---- 输入校验：权重/收益/现金/费用非有限 fail closed ----

func TestAttributionInvalidNonFinite(t *testing.T) {
	newBase := func() AttributionInput {
		return AttributionInput{
			InitialEquity: 100,
			Days: []AttributionDay{
				{Date: "2026-01-02", ActualWeights: map[string]float64{"A": 0.9}, CashRatio: 0.1,
					Returns: map[string]float64{"A": 0.01}},
			},
		}
	}
	cases := []struct {
		name   string
		mutate func(*AttributionDay)
		want   string
	}{
		{"ActualWeights NaN", func(d *AttributionDay) { d.ActualWeights["A"] = math.NaN() }, "ActualWeights"},
		{"ActualWeights +Inf", func(d *AttributionDay) { d.ActualWeights["A"] = math.Inf(1) }, "ActualWeights"},
		{"Returns NaN", func(d *AttributionDay) { d.Returns["A"] = math.NaN() }, "Returns"},
		{"Returns -Inf", func(d *AttributionDay) { d.Returns["A"] = math.Inf(-1) }, "Returns"},
		{"TargetWeights NaN", func(d *AttributionDay) { d.TargetWeights = map[string]float64{"A": math.NaN()} }, "TargetWeights"},
		{"IdealWeights NaN", func(d *AttributionDay) { d.IdealWeights = map[string]float64{"A": math.NaN()} }, "IdealWeights"},
		{"CashRatio NaN", func(d *AttributionDay) { d.CashRatio = math.NaN() }, "CashRatio"},
		{"Fees NaN", func(d *AttributionDay) { d.Fees = math.NaN() }, "Fees"},
		{"Fees -1", func(d *AttributionDay) { d.Fees = -1 }, "Fees"},
	}
	for _, tc := range cases {
		in := newBase()
		tc.mutate(&in.Days[0])
		pa, err := ComputeAttribution(in)
		if err == nil {
			t.Fatalf("%s 应返回错误，got 无错误", tc.name)
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s 错误应含字段 %s: %v", tc.name, tc.want, err)
		}
		if math.IsNaN(pa.CashContribution) || math.IsNaN(pa.NetReturn) {
			t.Fatalf("%s 不应产生 NaN 归因输出: %+v", tc.name, pa)
		}
	}
}

// ---- 权重偏离序列深拷贝：输出与输入隔离（防别名污染）----

func TestAttributionWeightSeriesCopy(t *testing.T) {
	target := map[string]float64{"A": 0.5, "B": 0.3}
	actual := map[string]float64{"A": 0.4, "B": 0.4}
	in := AttributionInput{
		InitialEquity: 100,
		Days: []AttributionDay{
			{Date: "2026-01-02", ActualWeights: actual, CashRatio: 0.2,
				TargetWeights: target, Returns: map[string]float64{"A": 0.0, "B": 0.0}},
		},
	}
	pa, err := ComputeAttribution(in)
	if err != nil {
		t.Fatalf("ComputeAttribution 失败: %v", err)
	}
	if len(pa.WeightSeries) != 1 {
		t.Fatalf("权重偏离序列长度 got %d want 1", len(pa.WeightSeries))
	}
	w := pa.WeightSeries[0]
	// 修改输入 map 不影响输出（深拷贝）。
	target["A"], target["B"] = 0.9, 0.9
	actual["A"], actual["B"] = 0.1, 0.1
	if got := w.Target["A"]; !approx(got, 0.5, 1e-12) {
		t.Fatalf("输出 Target 被输入污染: got %g want 0.5", got)
	}
	if got := w.Actual["A"]; !approx(got, 0.4, 1e-12) {
		t.Fatalf("输出 Actual 被输入污染: got %g want 0.4", got)
	}
	if got := w.Deviation["A"]; !approx(got, -0.1, 1e-12) {
		t.Fatalf("偏离被输入污染: got %g want -0.1", got)
	}
	if got := w.SumAbsDeviation; !approx(got, 0.2, 1e-12) {
		t.Fatalf("Σ|偏离|被输入污染: got %g want 0.2", got)
	}
}

// ---- 留一法组合维度：收益/换手/回撤/IC 变化 ----

func TestLeaveOneOutPortfolioImpact(t *testing.T) {
	f1 := series("f1", DirectionHigherIsBetter, 1, map[string][]TransformedRow{
		"2026-01-02": {trow("A", 1)},
	})
	f2 := series("f2", DirectionHigherIsBetter, 1, map[string][]TransformedRow{
		"2026-01-02": {trow("A", 2)},
	})
	ic := func(v float64) *float64 { return &v }
	runner := func(fs []FactorSeries) (PortfolioRunResult, error) {
		keys := make([]string, 0, len(fs))
		for _, f := range fs {
			keys = append(keys, f.Key)
		}
		switch strings.Join(keys, ",") {
		case "f1,f2":
			return PortfolioRunResult{OutOfSampleIC: ic(0.3), AnnualReturn: 0.10, AnnualTurnover: 1.0, MaxDrawdown: -0.05}, nil
		case "f2":
			return PortfolioRunResult{OutOfSampleIC: ic(0.2), AnnualReturn: 0.08, AnnualTurnover: 0.8, MaxDrawdown: -0.04}, nil
		case "f1":
			return PortfolioRunResult{OutOfSampleIC: ic(0.4), AnnualReturn: 0.12, AnnualTurnover: 1.2, MaxDrawdown: -0.06}, nil
		}
		return PortfolioRunResult{}, fmt.Errorf("未预期因子集合: %v", keys)
	}

	out, err := LeaveOneOutPortfolioImpact([]FactorSeries{f1, f2}, runner)
	if err != nil {
		t.Fatalf("LeaveOneOutPortfolioImpact 失败: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("应 2 个留一结果，got %d", len(out))
	}
	// 移除 f1（LOO = [f2]）：差值 = LOO - Full。
	o0 := out[0]
	if o0.RemovedFactor != "f1" {
		t.Fatalf("RemovedFactor got %s want f1", o0.RemovedFactor)
	}
	if !approx(o0.AnnualReturnChange, 0.08-0.10, 1e-12) {
		t.Fatalf("年化收益变化 got %.12g want -0.02", o0.AnnualReturnChange)
	}
	if !approx(o0.TurnoverChange, 0.8-1.0, 1e-12) {
		t.Fatalf("换手变化 got %.12g want -0.2", o0.TurnoverChange)
	}
	if !approx(o0.MaxDrawdownChange, -0.04-(-0.05), 1e-12) {
		t.Fatalf("回撤变化 got %.12g want 0.01", o0.MaxDrawdownChange)
	}
	if o0.ICChange == nil || !approx(*o0.ICChange, 0.2-0.3, 1e-12) {
		t.Fatalf("IC 变化 got %v want -0.1", o0.ICChange)
	}
	// 移除 f2（LOO = [f1]）。
	o1 := out[1]
	if o1.RemovedFactor != "f2" {
		t.Fatalf("RemovedFactor got %s want f2", o1.RemovedFactor)
	}
	if !approx(o1.AnnualReturnChange, 0.12-0.10, 1e-12) {
		t.Fatalf("年化收益变化 got %.12g want 0.02", o1.AnnualReturnChange)
	}
	if o1.ICChange == nil || !approx(*o1.ICChange, 0.4-0.3, 1e-12) {
		t.Fatalf("IC 变化 got %v want 0.1", o1.ICChange)
	}
	// nil 回调 → 错误。
	if _, err := LeaveOneOutPortfolioImpact([]FactorSeries{f1, f2}, nil); err == nil {
		t.Fatalf("nil 回调应返回错误")
	}
}

// ---- 账本 → 留一比较用表现结果（与 ComputeMetrics 一致）----

func TestRunResultFromLedgers(t *testing.T) {
	exec := execT5()
	d1 := DayInput{
		Target: tTarget("2026-01-04", map[string]int{"A": 100}),
		Market: tMarket("2026-01-05",
			map[string]float64{"A": 10}, map[string]float64{"A": 10}, map[string]float64{"A": 10}),
		Scores: map[string]float64{"A": 1.0},
	}
	markTradable(&d1.Market, "A")
	d2 := DayInput{
		Target: tTarget("2026-01-05", map[string]int{"A": 100}),
		Market: tMarket("2026-01-06",
			map[string]float64{"A": 10}, map[string]float64{"A": 10}, map[string]float64{"A": 11}),
	}
	markTradable(&d2.Market, "A")
	d3 := DayInput{
		Target: tTarget("2026-01-06", map[string]int{}),
		Market: tMarket("2026-01-07",
			map[string]float64{"A": 11}, map[string]float64{"A": 11}, map[string]float64{"A": 11}),
	}
	markTradable(&d3.Market, "A")

	ledgers, _, err := Simulate(tState(2000), []DayInput{d1, d2, d3}, exec)
	if err != nil {
		t.Fatalf("Simulate 失败: %v", err)
	}
	res, err := RunResultFromLedgers(ledgers, nil)
	if err != nil {
		t.Fatalf("RunResultFromLedgers 失败: %v", err)
	}
	m, err := ComputeMetrics(FromLedgers(ledgers), nil, 0)
	if err != nil {
		t.Fatalf("ComputeMetrics 失败: %v", err)
	}
	if !approx(res.AnnualReturn, m.Net.AnnualReturn, 1e-12) {
		t.Fatalf("年化收益 got %.12g want %.12g", res.AnnualReturn, m.Net.AnnualReturn)
	}
	if !approx(res.AnnualTurnover, m.Quality.AnnualTurnover, 1e-12) {
		t.Fatalf("年化换手 got %.12g want %.12g", res.AnnualTurnover, m.Quality.AnnualTurnover)
	}
	if !approx(res.MaxDrawdown, m.Net.MaxDrawdown, 1e-12) {
		t.Fatalf("最大回撤 got %.12g want %.12g", res.MaxDrawdown, m.Net.MaxDrawdown)
	}
	if res.OutOfSampleIC != nil {
		t.Fatalf("未提供 IC 应为 nil")
	}
	if math.IsNaN(res.AnnualReturn) || math.IsNaN(res.MaxDrawdown) {
		t.Fatalf("表现结果出现 NaN: %+v", res)
	}
}

// ---- 预测归因视图组装 ----

func TestPredictionAttributionView(t *testing.T) {
	red := RedundancyReport{Composite: CompositeIC{Mean: 0.1, ValidDays: 3}}
	loo := []LOOPortfolioImpact{{RemovedFactor: "f1", AnnualReturnChange: 0.01}}
	pa := BuildPredictionAttribution(red, loo)
	if pa.Redundancy.Composite.Mean != 0.1 || pa.Redundancy.Composite.ValidDays != 3 {
		t.Fatalf("Redundancy 应原样透传: %+v", pa.Redundancy.Composite)
	}
	if len(pa.LOOImpact) != 1 || pa.LOOImpact[0].RemovedFactor != "f1" {
		t.Fatalf("LOOImpact 应原样透传: %+v", pa.LOOImpact)
	}
	if !strings.Contains(pa.Limitation, "统计分解") {
		t.Fatalf("预测归因限制声明缺失: %q", pa.Limitation)
	}
}
