package portfolioresearch

import (
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"
)

// validation_test.go v2 Task 8 领域层测试：非重叠测试窗构造、门禁求值协议
// （passed/failed/insufficient/error 语义）、证据降级取最弱值、验证 hash
// 不可变身份、泄漏隔离（外层测试收益变化不影响冻结权重/门禁）与确定性
// （无测试后选择路径）。

// testDates 生成 n 个严格升序的 YYYY-MM-DD 测试日期（天号 1-28 防进位）。
func testDates(n int) []string {
	out := make([]string, n)
	for i := 0; i < n; i++ {
		out[i] = fmt.Sprintf("2024-%02d-%02d", i/28+1, i%28+1)
	}
	return out
}

// testModelHash 合法 64 位十六进制模型 hash。
func testModelHash() string { return strings.Repeat("ab", 32) }

// validValidationSpec 全门禁配置的合法冻结规格（各阈值选择使默认窗口可通过）。
func validValidationSpec() PortfolioValidationSpec {
	return PortfolioValidationSpec{
		ModelRef:   ModelRef{ModelID: "fm_20260917T150100000Z_99aabbcc", Revision: 1, Hash: testModelHash()},
		WindowRule: WindowRule{TrainDays: 20, TestDays: 10, Step: 10},
		Gates: GateSpec{
			MinValidWindows:         2,
			MinTradingDays:          20,
			MinNetReturn:            0.01,
			MaxDrawdown:             -0.2,
			MaxCostDrag:             0.05,
			MinInformationRatio:     0.5,
			MinExcessStability:      0.5,
			MaxTurnover:             5,
			MaxCashResidual:         0.2,
			MaxUnfilledRate:         0.1,
			MaxConcentration:        0.2,
			MinDirectionConsistency: 0.5,
			MinBaselineIncrement:    0.0,
			MaxDegradedWindows:      1,
		},
		Benchmark:           BenchmarkSpec{ID: "hs300"},
		EvidenceRequirement: EvidenceRetrospective,
	}
}

// validWindowMetrics 全门禁可通过的窗口指标（30 个交易日，IR/excess 可用）。
func validWindowMetrics() ValidationWindowMetrics {
	ir := 1.0
	excess := 0.06
	return ValidationWindowMetrics{
		TradingDays:       30,
		NetReturn:         0.05,
		MaxDrawdown:       -0.05,
		CostDrag:          0.01,
		AnnualTurnover:    2.0,
		AvgCashRatio:      0.05,
		UnfilledRate:      0.02,
		MaxConcentration:  0.1,
		InformationRatio:  &ir,
		AnnualExcess:      &excess,
		BaselineIncrement: 0.02,
	}
}

// okWindow 构造一个 ok 窗口（指标默认可通过门禁）。
func okWindow(idx int, muts ...func(*ValidationWindowOutcome)) ValidationWindowOutcome {
	m := validWindowMetrics()
	o := ValidationWindowOutcome{
		ValidationID:  "pv_x",
		Index:         idx,
		TrainStart:    fmt.Sprintf("2024-%02d-%02d", 1, 1),
		TrainEnd:      fmt.Sprintf("2024-%02d-%02d", 1, 20),
		TestStart:     fmt.Sprintf("2024-%02d-%02d", 1, 21),
		TestEnd:       fmt.Sprintf("2024-%02d-%02d", 2, 20),
		State:         WindowStateOK,
		Metrics:       &m,
		EvidenceClass: EvidenceRetrospective,
	}
	for _, mf := range muts {
		mf(&o)
	}
	return o
}

// gateResultByName 按名称查找门禁结果。
func gateResultByName(t *testing.T, gates []GateResult, name string) GateResult {
	t.Helper()
	for _, g := range gates {
		if g.Name == name {
			return g
		}
	}
	t.Fatalf("缺少门禁 %q（全部: %v）", name, gates)
	return GateResult{}
}

// ---- 窗口构造 ----

func TestBuildValidationWindows_NonOverlapping(t *testing.T) {
	dates := testDates(100)
	rule := WindowRule{TrainDays: 20, TestDays: 10, Step: 10}
	plans, err := BuildValidationWindows(dates, rule)
	if err != nil {
		t.Fatalf("构建窗口失败: %v", err)
	}
	// 100 日：窗 0 [0,20)+[20,30)、窗 1 [10,30)+[30,40)、... 窗 6 [60,80)+[80,90)、
	// 窗 7 [70,90)+[90,100) → 共 8 窗。
	if len(plans) != 8 {
		t.Fatalf("期望 8 个窗口，实际 %d", len(plans))
	}
	for i, p := range plans {
		if p.Index != i+1 {
			t.Fatalf("窗口序号应为 1 起始: %d", p.Index)
		}
		if p.TrainStart >= p.TestStart {
			t.Fatalf("窗 %d 训练必须早于测试: %s >= %s", p.Index, p.TrainStart, p.TestStart)
		}
	}
	// 测试窗互不重叠（含相邻且不重叠断言）。
	for i := 0; i < len(plans); i++ {
		for j := i + 1; j < len(plans); j++ {
			a, b := plans[i], plans[j]
			if a.TestStart <= b.TestEnd && b.TestStart <= a.TestEnd {
				t.Fatalf("窗 %d 与 %d 测试窗重叠: %s~%s vs %s~%s", a.Index, b.Index, a.TestStart, a.TestEnd, b.TestStart, b.TestEnd)
			}
		}
	}
	// 相邻窗边界：窗 k 测试结束 < 窗 k+1 测试开始（或相邻）。
	for i := 1; i < len(plans); i++ {
		if plans[i].TestStart < plans[i-1].TestEnd {
			t.Fatalf("窗 %d 测试开始早于窗 %d 测试结束（重叠）", plans[i].Index, plans[i-1].Index)
		}
	}
	// 训练窗只使用该窗测试开始前的数据。
	for _, p := range plans {
		train, err := TrainingDatesForWindow(dates, p)
		if err != nil {
			t.Fatalf("取训练日期失败: %v", err)
		}
		for _, d := range train {
			if d >= p.TestStart {
				t.Fatalf("窗 %d 训练日期 %s 进入测试期（泄漏）", p.Index, d)
			}
		}
	}
	// 确定性：相同输入相同输出。
	again, err := BuildValidationWindows(dates, rule)
	if err != nil {
		t.Fatalf("重复构建失败: %v", err)
	}
	if !reflect.DeepEqual(plans, again) {
		t.Fatalf("窗口构建不确定: %v vs %v", plans, again)
	}
}

func TestBuildValidationWindows_StepGapAndInsufficientTail(t *testing.T) {
	dates := testDates(55)
	// Step=15 > TestDays=10：测试窗之间留 5 日空隙（允许），仍互不重叠。
	plans, err := BuildValidationWindows(dates, WindowRule{TrainDays: 20, TestDays: 10, Step: 15})
	if err != nil {
		t.Fatalf("构建失败: %v", err)
	}
	if len(plans) != 2 {
		t.Fatalf("55 日（train20/test10/step15）期望 2 窗，实际 %d", len(plans))
	}
	for i := 1; i < len(plans); i++ {
		if plans[i].TestStart < plans[i-1].TestEnd {
			t.Fatalf("测试窗重叠: %s < %s", plans[i].TestStart, plans[i-1].TestEnd)
		}
	}
	// 日期不足：只够 1 窗。
	short, err := BuildValidationWindows(testDates(30), WindowRule{TrainDays: 20, TestDays: 10, Step: 10})
	if err != nil {
		t.Fatalf("构建失败: %v", err)
	}
	if len(short) != 1 {
		t.Fatalf("30 日期望 1 窗，实际 %d", len(short))
	}
	// 完全不足 → 0 窗（不报错；由门禁判 insufficient）。
	zero, err := BuildValidationWindows(testDates(10), WindowRule{TrainDays: 20, TestDays: 10, Step: 10})
	if err != nil || len(zero) != 0 {
		t.Fatalf("10 日期望 0 窗: %v", err)
	}
}

func TestBuildValidationWindows_InvalidInput(t *testing.T) {
	if _, err := BuildValidationWindows(nil, WindowRule{TrainDays: 20, TestDays: 10, Step: 10}); err == nil {
		t.Fatalf("空日期应报错")
	}
	if _, err := BuildValidationWindows([]string{"2024-01-02", "2024-01-01"}, WindowRule{TrainDays: 20, TestDays: 10, Step: 10}); err == nil {
		t.Fatalf("乱序日期应报错")
	}
	if _, err := BuildValidationWindows(testDates(10), WindowRule{TrainDays: 20, TestDays: 10, Step: 5}); err == nil {
		t.Fatalf("step < testDays 应报错（测试窗会重叠）")
	}
	if _, err := BuildValidationWindows(testDates(10), WindowRule{TrainDays: 0, TestDays: 10, Step: 10}); err == nil {
		t.Fatalf("trainDays=0 应报错")
	}
}

// ---- 门禁求值协议 ----

func TestEvaluateValidation_PassedAllGates(t *testing.T) {
	spec := validValidationSpec()
	windows := []ValidationWindowOutcome{okWindow(1), okWindow(2), okWindow(3)}
	res, err := spec.Evaluate(windows)
	if err != nil {
		t.Fatalf("Evaluate 失败: %v", err)
	}
	if res.Verdict != GateStatusPassed {
		t.Fatalf("全部门禁满足应 passed，实际 %q（%s）", res.Verdict, res.Message)
	}
	if res.ValidWindowCount != 3 || res.WindowCount != 3 {
		t.Fatalf("窗口计数错误: %d/%d", res.ValidWindowCount, res.WindowCount)
	}
	if res.EvidenceClass != EvidenceRetrospective {
		t.Fatalf("聚合证据应为 retrospective: %q", res.EvidenceClass)
	}
	if len(res.Gates) != len(gateNames) {
		t.Fatalf("逐项门禁应披露 %d 项，实际 %d", len(gateNames), len(res.Gates))
	}
	for _, g := range res.Gates {
		if g.Result == gateFail {
			t.Fatalf("门禁 %s 不应失败: %+v", g.Name, g)
		}
	}
}

func TestEvaluateValidation_FailedGate(t *testing.T) {
	spec := validValidationSpec()
	// 窗 2、3 净收益为负 → 净收益均值与方向一致性两门禁失败。
	w2 := okWindow(2, func(o *ValidationWindowOutcome) {
		o.Metrics.NetReturn = -0.1
	})
	w3 := okWindow(3, func(o *ValidationWindowOutcome) {
		o.Metrics.NetReturn = -0.05
	})
	res, err := spec.Evaluate([]ValidationWindowOutcome{okWindow(1), w2, w3})
	if err != nil {
		t.Fatalf("Evaluate 失败: %v", err)
	}
	if res.Verdict != GateStatusFailed {
		t.Fatalf("存在失败门禁应 failed，实际 %q", res.Verdict)
	}
	nr := gateResultByName(t, res.Gates, "net_return")
	if nr.Result != gateFail {
		t.Fatalf("net_return 应 fail: %+v", nr)
	}
	dc := gateResultByName(t, res.Gates, "direction_consistency")
	if dc.Result != gateFail {
		t.Fatalf("direction_consistency 应 fail: %+v", dc)
	}
}

func TestEvaluateValidation_InsufficientWhenFewWindows(t *testing.T) {
	spec := validValidationSpec() // MinValidWindows=2
	res, err := spec.Evaluate([]ValidationWindowOutcome{okWindow(1)})
	if err != nil {
		t.Fatalf("Evaluate 失败: %v", err)
	}
	if res.Verdict != GateStatusInsufficient {
		t.Fatalf("有效窗口不足应 insufficient（不是 failed），实际 %q", res.Verdict)
	}
	// 交易门不足同样 insufficient。
	spec2 := validValidationSpec()
	spec2.Gates.MinTradingDays = 100
	w := okWindow(1, func(o *ValidationWindowOutcome) { o.Metrics.TradingDays = 30 })
	res2, err := spec2.Evaluate([]ValidationWindowOutcome{w, okWindow(2)})
	if err != nil {
		t.Fatalf("Evaluate 失败: %v", err)
	}
	if res2.Verdict != GateStatusInsufficient {
		t.Fatalf("交易日不足应 insufficient，实际 %q", res2.Verdict)
	}
	// 零有效窗口（全部 insufficient 状态）→ insufficient。
	res3, err := spec.Evaluate([]ValidationWindowOutcome{
		{Index: 1, State: WindowStateInsufficient, Message: "数据不足",
			TrainStart: "2024-01-01", TrainEnd: "2024-01-20", TestStart: "2024-01-21", TestEnd: "2024-01-30",
			EvidenceClass: EvidenceRetrospective},
	})
	if err != nil {
		t.Fatalf("Evaluate 失败: %v", err)
	}
	if res3.Verdict != GateStatusInsufficient {
		t.Fatalf("零有效窗口应 insufficient，实际 %q", res3.Verdict)
	}
}

func TestEvaluateValidation_ErrorVerdict(t *testing.T) {
	spec := validValidationSpec()
	wErr := ValidationWindowOutcome{
		ValidationID: "pv_x", Index: 2,
		TrainStart: "2024-01-01", TrainEnd: "2024-01-20",
		TestStart: "2024-01-21", TestEnd: "2024-01-30",
		State:         WindowStateError,
		Message:       "行情数据读取失败",
		EvidenceClass: EvidenceRetrospective,
	}
	res, err := spec.Evaluate([]ValidationWindowOutcome{okWindow(1), wErr})
	if err != nil {
		t.Fatalf("Evaluate 失败: %v", err)
	}
	if res.Verdict != GateStatusError {
		t.Fatalf("执行错误应 error（不伪装统计失败），实际 %q", res.Verdict)
	}
	if res.Message == "" {
		t.Fatalf("error 结论必须说明原因")
	}
	for _, g := range res.Gates {
		if g.Result != gateUnknown {
			t.Fatalf("error 结论不应评估统计门禁: %s=%s", g.Name, g.Result)
		}
	}
	// 门禁披露 Actual 应传真实已知值（1 个有效窗 × 30 交易日），不写死 0。
	mv := gateResultByName(t, res.Gates, "min_valid_windows")
	if mv.Actual != "1" {
		t.Fatalf("error 结论 min_valid_windows Actual 应为真实有效窗数 1，实际 %q", mv.Actual)
	}
	td := gateResultByName(t, res.Gates, "min_trading_days")
	if td.Actual != "30" {
		t.Fatalf("error 结论 min_trading_days Actual 应为真实交易日数 30，实际 %q", td.Actual)
	}
	if res.ValidWindowCount != 1 {
		t.Fatalf("error 结论有效窗数应为 1，实际 %d", res.ValidWindowCount)
	}
}

func TestEvaluateValidation_ExploratoryCannotPass(t *testing.T) {
	spec := validValidationSpec() // 要求 retrospective
	windows := []ValidationWindowOutcome{
		okWindow(1, func(o *ValidationWindowOutcome) { o.EvidenceClass = EvidenceExploratory }),
		okWindow(2, func(o *ValidationWindowOutcome) { o.EvidenceClass = EvidenceExploratory }),
		okWindow(3, func(o *ValidationWindowOutcome) { o.EvidenceClass = EvidenceExploratory }),
	}
	res, err := spec.Evaluate(windows)
	if err != nil {
		t.Fatalf("Evaluate 失败: %v", err)
	}
	if res.Verdict == GateStatusPassed {
		t.Fatalf("exploratory 输入不能得到正式 passed")
	}
	if res.EvidenceClass != EvidenceExploratory {
		t.Fatalf("聚合证据应为 exploratory（最弱）: %q", res.EvidenceClass)
	}
	ev := gateResultByName(t, res.Gates, "evidence_requirement")
	if ev.Result != gateFail {
		t.Fatalf("evidence_requirement 应 fail: %+v", ev)
	}
}

func TestAggregateEvidenceClass_TakesMin(t *testing.T) {
	windows := []ValidationWindowOutcome{
		okWindow(1, func(o *ValidationWindowOutcome) { o.EvidenceClass = EvidenceProspective }),
		okWindow(2, func(o *ValidationWindowOutcome) { o.EvidenceClass = EvidenceExploratory }),
		okWindow(3, func(o *ValidationWindowOutcome) { o.EvidenceClass = EvidenceRetrospective }),
	}
	ec, err := AggregateEvidenceClass(windows)
	if err != nil {
		t.Fatalf("汇总失败: %v", err)
	}
	if ec != EvidenceExploratory {
		t.Fatalf("任一输入降级时输出取最弱值（min）: %q", ec)
	}
	if _, err := AggregateEvidenceClass(nil); err == nil {
		t.Fatalf("空列表应报错")
	}
}

func TestEvaluateValidation_DeterministicNoPostTestSelection(t *testing.T) {
	spec := validValidationSpec()
	windows := []ValidationWindowOutcome{okWindow(1), okWindow(2), okWindow(3)}
	res1, err := spec.Evaluate(windows)
	if err != nil {
		t.Fatalf("Evaluate 失败: %v", err)
	}
	res2, err := spec.Evaluate(windows)
	if err != nil {
		t.Fatalf("Evaluate 失败: %v", err)
	}
	if !reflect.DeepEqual(res1, res2) {
		t.Fatalf("结论不确定（存在测试后选择路径）: %+v vs %+v", res1, res2)
	}
	// 测试结果变化只改变 actual，不改变冻结 spec：结论由 spec 唯一决定。
	res3, err := spec.Evaluate([]ValidationWindowOutcome{
		okWindow(1), okWindow(2, func(o *ValidationWindowOutcome) { o.Metrics.NetReturn = -0.2 }), okWindow(3),
	})
	if err != nil {
		t.Fatalf("Evaluate 失败: %v", err)
	}
	if res3.Verdict != GateStatusFailed {
		t.Fatalf("测试结果变化应改变结论: %q", res3.Verdict)
	}
	// spec（含门禁配置）自身不被测试结果修改：hash 恒等。
	h1, err := PortfolioValidationSpecHash(spec)
	if err != nil {
		t.Fatalf("hash 失败: %v", err)
	}
	h2, err := PortfolioValidationSpecHash(spec)
	if err != nil {
		t.Fatalf("hash 失败: %v", err)
	}
	if h1 != h2 {
		t.Fatalf("冻结 spec hash 变化（测试结果不应影响门禁配置）")
	}
}

func TestSpecHash_FrozenIdentity(t *testing.T) {
	base := validValidationSpec()
	h, err := PortfolioValidationSpecHash(base)
	if err != nil {
		t.Fatalf("hash 失败: %v", err)
	}
	if len(h) != 64 {
		t.Fatalf("hash 应为 64 位十六进制: %q", h)
	}
	// 任一字段变化 → 不同 hash（修改模型或门禁必须创建新验证）。
	cases := map[string]func(*PortfolioValidationSpec){
		"modelId":      func(s *PortfolioValidationSpec) { s.ModelRef.ModelID = "fm_20260917T150200000Z_88cceeaa" },
		"revision":     func(s *PortfolioValidationSpec) { s.ModelRef.Revision = 2 },
		"modelHash":    func(s *PortfolioValidationSpec) { s.ModelRef.Hash = strings.Repeat("cd", 32) },
		"trainDays":    func(s *PortfolioValidationSpec) { s.WindowRule.TrainDays = 30 },
		"testDays":     func(s *PortfolioValidationSpec) { s.WindowRule.TestDays = 20 },
		"step":         func(s *PortfolioValidationSpec) { s.WindowRule.Step = 20 },
		"minNetReturn": func(s *PortfolioValidationSpec) { s.Gates.MinNetReturn = 0.02 },
		"maxDrawdown":  func(s *PortfolioValidationSpec) { s.Gates.MaxDrawdown = -0.1 },
		"benchmark":    func(s *PortfolioValidationSpec) { s.Benchmark = BenchmarkSpec{ID: "csi500"} },
		"evidenceReq":  func(s *PortfolioValidationSpec) { s.EvidenceRequirement = EvidenceProspective },
	}
	for name, mut := range cases {
		s := base
		mut(&s)
		h2, err := PortfolioValidationSpecHash(s)
		if err != nil {
			t.Fatalf("%s: hash 失败: %v", name, err)
		}
		if h2 == h {
			t.Fatalf("%s 变化未改变验证 hash（身份未冻结）", name)
		}
	}
	// 语义等价（空 EvidenceRequirement ≡ 默认 retrospective）→ 相同 hash。
	s := base
	s.EvidenceRequirement = ""
	h2, err := PortfolioValidationSpecHash(s)
	if err != nil {
		t.Fatalf("hash 失败: %v", err)
	}
	if h2 != h {
		t.Fatalf("空证据要求应归一为默认 retrospective，同一身份")
	}
	// -0.0 与 +0.0 语义相同 → 相同 hash。
	s = base
	s.Gates.MinBaselineIncrement = math.Copysign(0, -1)
	h3, err := PortfolioValidationSpecHash(s)
	if err != nil {
		t.Fatalf("hash 失败: %v", err)
	}
	if h3 != h {
		t.Fatalf("-0.0 应归一为 +0.0，同一身份")
	}
}

func TestSpecHash_RejectsInvalid(t *testing.T) {
	bad := validValidationSpec()
	bad.Gates.MinNetReturn = math.NaN()
	if _, err := PortfolioValidationSpecHash(bad); err == nil {
		t.Fatalf("NaN 门禁应拒绝（fail closed）")
	}
	bad2 := validValidationSpec()
	bad2.ModelRef.Hash = "xyz"
	if err := bad2.Validate(); err == nil {
		t.Fatalf("非法 model hash 应拒绝")
	}
	bad3 := validValidationSpec()
	bad3.EvidenceRequirement = EvidenceExploratory
	if err := bad3.Validate(); err == nil {
		t.Fatalf("exploratory 不允许作为正式验证要求")
	}
}

// ---- 泄漏隔离 ----

// leakFactors 两个因子（方向统一后的 Final），逐日截面 3 只股票。
func leakFactors(dates []string) []FactorSeries {
	rows := func(d string) []TransformedRow {
		return []TransformedRow{
			{Code: "S1", Final: 1.0},
			{Code: "S2", Final: 0.5},
			{Code: "S3", Final: 0.0},
		}
	}
	daysA := map[string][]TransformedRow{}
	daysB := map[string][]TransformedRow{}
	for _, d := range dates {
		daysA[d] = rows(d)
		daysB[d] = rows(d)
	}
	return []FactorSeries{
		{Key: "mom", Direction: DirectionHigherIsBetter, Horizon: 3, Days: daysA},
		{Key: "vol", Direction: DirectionHigherIsBetter, Horizon: 3, Days: daysB},
	}
}

// leakPrices 每日期 3 只股票的价格（随日期递增，有限正数）。
func leakPrices(dates []string) map[string]map[string]float64 {
	p := map[string]map[string]float64{}
	for i, d := range dates {
		base := 100.0 + float64(i)
		p[d] = map[string]float64{"S1": base, "S2": base * 0.9, "S3": base * 0.8}
	}
	return p
}

// viewForDates 按给定日期构建训练视图（只含这些日期的价格——测试窗数据在
// 类型上无法进入训练器）。
func viewForDates(dates []string, price map[string]map[string]float64) TrainingView {
	view := TrainingView{Dates: append([]string{}, dates...), Price: map[string]map[string]float64{}}
	for _, d := range dates {
		view.Price[d] = price[d]
	}
	return view
}

// TestLeak_OuterTestReturnsDoNotAffectFrozenWeights 外层测试收益变化不影响
// 该窗权重/门禁配置：权重只由训练窗数据形成；测试窗收益剧烈变化后，用
// 相同的训练视图重新训练得到逐位相同的权重快照。
func TestLeak_OuterTestReturnsDoNotAffectFrozenWeights(t *testing.T) {
	dates := testDates(60)
	rule := WindowRule{TrainDays: 20, TestDays: 10, Step: 10}
	plans, err := BuildValidationWindows(dates, rule)
	if err != nil {
		t.Fatalf("构建窗口失败: %v", err)
	}
	if len(plans) == 0 {
		t.Fatalf("应至少 1 个窗口")
	}
	plan := plans[0]
	price := leakPrices(dates)
	factors := leakFactors(dates)
	trainDates, err := TrainingDatesForWindow(dates, plan)
	if err != nil {
		t.Fatalf("取训练日期失败: %v", err)
	}
	// 训练视图只含训练期日期（测试窗数据无法进入）。
	for _, d := range trainDates {
		if d >= plan.TestStart {
			t.Fatalf("训练日期泄漏测试窗: %s", d)
		}
	}
	view := viewForDates(trainDates, price)
	spec := RollingICSpec{WindowYears: 1, Shrinkage: 0.5, MaxAbsWeight: 0.8, Fallback: CombinationFallbackEqualWeight}
	snap1, err := TrainRollingIC(spec, view, factors)
	if err != nil {
		t.Fatalf("首次训练失败: %v", err)
	}
	if len(snap1.FinalWeights) != 2 {
		t.Fatalf("应产出 2 因子权重快照: %v", snap1.FinalWeights)
	}

	// 剧烈修改测试窗收益（外层测试收益变化）。
	for _, d := range dates {
		if d >= plan.TestStart && d <= plan.TestEnd {
			for code := range price[d] {
				price[d][code] *= 1000
			}
		}
	}
	view2 := viewForDates(trainDates, price) // 训练视图日期不变、训练期价格不变
	snap2, err := TrainRollingIC(spec, view2, factors)
	if err != nil {
		t.Fatalf("重训失败: %v", err)
	}
	if !reflect.DeepEqual(snap1.FinalWeights, snap2.FinalWeights) {
		t.Fatalf("外层测试收益变化影响了冻结权重: %v vs %v", snap1.FinalWeights, snap2.FinalWeights)
	}
	if snap1.TrainStart != plan.TrainStart || snap1.TrainEnd != plan.TrainEnd {
		t.Fatalf("权重快照训练区间应与计划一致: %s~%s vs %s~%s", snap1.TrainStart, snap1.TrainEnd, plan.TrainStart, plan.TrainEnd)
	}
}

// TestTrainingDatesForWindow_StructuralIsolation 训练日期切片结构上排除测试窗
// （测试窗收益无法进入训练器）。
func TestTrainingDatesForWindow_StructuralIsolation(t *testing.T) {
	dates := testDates(60)
	plans, err := BuildValidationWindows(dates, WindowRule{TrainDays: 20, TestDays: 10, Step: 10})
	if err != nil {
		t.Fatalf("构建失败: %v", err)
	}
	for _, p := range plans {
		train, err := TrainingDatesForWindow(dates, p)
		if err != nil {
			t.Fatalf("窗 %d: %v", p.Index, err)
		}
		if len(train) != 20 {
			t.Fatalf("窗 %d 训练日数应为 20，实际 %d", p.Index, len(train))
		}
		if train[0] != p.TrainStart || train[len(train)-1] != p.TrainEnd {
			t.Fatalf("窗 %d 训练区间与计划不一致", p.Index)
		}
		for _, d := range train {
			if d >= p.TestStart {
				t.Fatalf("窗 %d 训练切片包含测试日 %s（泄漏）", p.Index, d)
			}
		}
	}
	// 训练区间不在日期列表 → 报错。
	if _, err := TrainingDatesForWindow(testDates(10), plans[0]); err == nil {
		t.Fatalf("训练区间越界应报错")
	}
}

// TestTrainingDatesForWindow_SingleDayWindow 单日训练窗（TrainDays=1，
// TrainStart==TrainEnd）：返回恰好该单日日期切片，不报错。
func TestTrainingDatesForWindow_SingleDayWindow(t *testing.T) {
	dates := testDates(40)
	plans, err := BuildValidationWindows(dates, WindowRule{TrainDays: 1, TestDays: 10, Step: 10})
	if err != nil {
		t.Fatalf("构建失败: %v", err)
	}
	if len(plans) != 3 {
		t.Fatalf("40 日（train1/test10/step10）期望 3 窗，实际 %d", len(plans))
	}
	for _, p := range plans {
		if p.TrainStart != p.TrainEnd {
			t.Fatalf("单日训练窗起止应相同: %s vs %s", p.TrainStart, p.TrainEnd)
		}
		train, err := TrainingDatesForWindow(dates, p)
		if err != nil {
			t.Fatalf("单日训练窗取训练日期失败: %v", err)
		}
		if len(train) != 1 || train[0] != p.TrainStart {
			t.Fatalf("单日训练窗应返回恰好该单日: %v", train)
		}
		if train[0] >= p.TestStart {
			t.Fatalf("单日训练切片包含测试日 %s（泄漏）", train[0])
		}
	}
}

// TestTrainingDatesForWindow_Boundary 边界：TrainStart/TrainEnd 单侧越界仍报
// 明确错误；TrainEnd 在列表首/末的截取正确。
func TestTrainingDatesForWindow_Boundary(t *testing.T) {
	dates := testDates(30)
	// TrainStart 越界（早于列表首）、TrainEnd 在列表内 → 报错。
	if _, err := TrainingDatesForWindow(dates, ValidationWindowPlan{
		TrainStart: "2024-01-00", TrainEnd: dates[0],
		TestStart: dates[1], TestEnd: dates[10],
	}); err == nil {
		t.Fatalf("TrainStart 越界应报错")
	}
	// TrainStart 在列表内、TrainEnd 越界（晚于列表末）→ 报错。
	if _, err := TrainingDatesForWindow(dates, ValidationWindowPlan{
		TrainStart: dates[0], TrainEnd: "2099-12-31",
		TestStart: "2099-12-31", TestEnd: "2099-12-31",
	}); err == nil {
		t.Fatalf("TrainEnd 越界应报错")
	}
	// TrainEnd 恰在列表末：正常返回截取区间（start=10, end=29）。
	train, err := TrainingDatesForWindow(dates, ValidationWindowPlan{
		TrainStart: dates[10], TrainEnd: dates[29],
		TestStart: "2099-12-31", TestEnd: "2099-12-31",
	})
	if err != nil {
		t.Fatalf("TrainEnd 在列表末应正常截取: %v", err)
	}
	if len(train) != 20 || train[0] != dates[10] || train[len(train)-1] != dates[29] {
		t.Fatalf("截取区间错误: %v", train)
	}
}

// ---- 逐窗解释 ----

func TestEvaluateWindowGates_PerWindowExplainable(t *testing.T) {
	spec := validValidationSpec()
	w := okWindow(1)
	gates := spec.EvaluateWindowGates(w)
	if len(gates) == 0 {
		t.Fatalf("每窗必须携带逐项门禁结果")
	}
	byName := map[string]GateResult{}
	for _, g := range gates {
		byName[g.Name] = g
	}
	if byName["net_return"].Result != gatePass {
		t.Fatalf("达标窗 net_return 应 pass: %+v", byName["net_return"])
	}
	// 不达标窗：净收益为负 → fail。
	wBad := okWindow(2, func(o *ValidationWindowOutcome) { o.Metrics.NetReturn = -0.3 })
	gb := spec.EvaluateWindowGates(wBad)
	if gateResultByName(t, gb, "net_return").Result != gateFail {
		t.Fatalf("负收益窗 net_return 应 fail")
	}
	// insufficient 窗口：全部 unknown 且带原因。
	wIns := ValidationWindowOutcome{
		ValidationID: "pv_x", Index: 3,
		TrainStart: "2024-01-01", TrainEnd: "2024-01-20",
		TestStart: "2024-01-21", TestEnd: "2024-01-30",
		State: WindowStateInsufficient, Message: "截面不足",
		EvidenceClass: EvidenceRetrospective,
	}
	gi := spec.EvaluateWindowGates(wIns)
	for _, g := range gi {
		if g.Result != gateUnknown || g.Reason == "" {
			t.Fatalf("insufficient 窗门禁应 unknown 且带原因: %+v", g)
		}
	}
}

// TestValidationWindowOutcome_Validate 窗口结果校验：训练必须早于测试
// （泄漏隔离）、ok 窗口必须携带指标、非法状态拒绝。
func TestValidationWindowOutcome_Validate(t *testing.T) {
	if err := okWindow(1).Validate(); err != nil {
		t.Fatalf("合法窗口应通过: %v", err)
	}
	leak := okWindow(1, func(o *ValidationWindowOutcome) { o.TrainEnd = "2024-02-20"; o.TestStart = "2024-02-10" })
	if err := leak.Validate(); err == nil {
		t.Fatalf("训练晚于测试应拒绝（泄漏隔离）")
	}
	noMetrics := okWindow(1, func(o *ValidationWindowOutcome) { o.Metrics = nil })
	if err := noMetrics.Validate(); err == nil {
		t.Fatalf("ok 窗口缺指标应拒绝")
	}
	badState := okWindow(1, func(o *ValidationWindowOutcome) { o.State = "weird" })
	if err := badState.Validate(); err == nil {
		t.Fatalf("非法状态应拒绝")
	}
	nanMetrics := okWindow(1, func(o *ValidationWindowOutcome) { o.Metrics.NetReturn = math.NaN() })
	if err := nanMetrics.Validate(); err == nil {
		t.Fatalf("NaN 指标应拒绝（fail closed）")
	}
}
