package portfolioresearch

import (
	"fmt"
	"math"
	"reflect"
	"sort"
	"testing"
)

// transform_test.go v2 Task 2 截面变换流水线测试。
//
// 金标准覆盖：5-10 只股票手算 rank/z-score/方向反转；NaN/Inf/全相同值/
// 极小截面/并列值；中位数只用当日截面；分位边界不读取未来日期；中性化矩阵
// 奇异与行业/市值缺失；输入顺序变化不改变按代码对齐的输出。
// 完成门槛三禁令：全样本 Min-Max、未来填充、NaN→0 隐式转换均有断言。

// ---- 测试辅助 ----

func csRow(date, code string, v float64) CrossSectionRow {
	return CrossSectionRow{Date: date, Code: code, Value: v, Valid: true, Tradable: true}
}

func csRowMissing(date, code, reason string) CrossSectionRow {
	return CrossSectionRow{Date: date, Code: code, Value: math.NaN(), Valid: false, Tradable: true, MissingReason: reason}
}

func csRowInvalid(date, code string, v float64) CrossSectionRow {
	return CrossSectionRow{Date: date, Code: code, Value: v, Valid: false, Tradable: true}
}

// rankPipeline 以 rank 标准化为默认的流水线（可继续改写去极值/中性化）。
func rankPipeline(missing string) TransformPipeline {
	return TransformPipeline{
		Missing:     missing,
		Winsorize:   WinsorizeSpec{Mode: TransformWinsorizeNone},
		Neutralize:  NeutralizeSpec{Mode: TransformNeutralizeNone},
		Standardize: TransformStandardizeRank,
	}
}

func neutralizePipeline() TransformPipeline {
	p := rankPipeline(TransformMissingExclude)
	p.Neutralize = NeutralizeSpec{Mode: TransformNeutralizeIndustrySize, IndustryDataset: "test_ind", SizeDataset: "test_size"}
	return p
}

func testCfg() TransformConfig {
	return TransformConfig{MinValidStocks: 3, NeutralizeFailure: NeutralizeFailureFail}
}

func mustTransform(t *testing.T, p TransformPipeline, cfg TransformConfig, dir string, rows []CrossSectionRow, exp RiskExposureProvider) TransformResult {
	t.Helper()
	res, err := Transform(p, cfg, dir, rows, exp)
	if err != nil {
		t.Fatalf("Transform 失败: %v", err)
	}
	return res
}

func stepNames(d TransformDiagnostics) []string {
	names := make([]string, len(d.Steps))
	for i, s := range d.Steps {
		names[i] = s.Step
	}
	return names
}

func stepBy(t *testing.T, d TransformDiagnostics, name string) StepStats {
	t.Helper()
	for _, s := range d.Steps {
		if s.Step == name {
			return s
		}
	}
	t.Fatalf("缺少步骤 %q（实际 %v）", name, stepNames(d))
	return StepStats{}
}

func rowByCode(t *testing.T, res TransformResult, code string) TransformedRow {
	t.Helper()
	for _, r := range res.Rows {
		if r.Code == code {
			return r
		}
	}
	t.Fatalf("缺少代码 %q 的输出行", code)
	return TransformedRow{}
}

func assertFloat(t *testing.T, got, want, tol float64, msg string) {
	t.Helper()
	if math.Abs(got-want) > tol {
		t.Fatalf("%s: got %v, want %v", msg, got, want)
	}
}

func assertNaN(t *testing.T, v float64, msg string) {
	t.Helper()
	if !math.IsNaN(v) {
		t.Fatalf("%s: 应为 NaN，实际 %v", msg, v)
	}
}

func hasEvent(evs []QualityEvent, kind string) bool {
	for _, ev := range evs {
		if ev.Kind == kind {
			return true
		}
	}
	return false
}

// floatEqual NaN 视为相等（reflect.DeepEqual 对 NaN 返回 false，不能用于
// 含 NaN 字段的输出行比较）。
func floatEqual(a, b float64) bool {
	if math.IsNaN(a) && math.IsNaN(b) {
		return true
	}
	return a == b
}

// transformedRowsEqual 顺序无关的行比较（NaN 感知，-0.0 与 0.0 经 == 视为相等）。
func transformedRowsEqual(a, b []TransformedRow) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		x, y := a[i], b[i]
		if x.Code != y.Code || x.Masked != y.Masked || x.Missing != y.Missing ||
			x.Filled != y.Filled || x.Winsorized != y.Winsorized || x.Neutralized != y.Neutralized ||
			x.MissingReason != y.MissingReason {
			return false
		}
		if !floatEqual(x.Original, y.Original) || !floatEqual(x.Final, y.Final) ||
			!floatEqual(x.FilledWith, y.FilledWith) || !floatEqual(x.NeutralizedFrom, y.NeutralizedFrom) ||
			!floatEqual(x.MissingIndicator, y.MissingIndicator) {
			return false
		}
	}
	return true
}

// ---- 金标准：手算 rank / z-score / 方向反转 ----

// TestTransformRankHandComputed 5 只股票手算 rank 与方向反转（金标准）。
// 值 [1,2,3,4,5]，n=5：score = 2r/(n+1)-1 = [-2/3,-1/3,0,1/3,2/3]。
func TestTransformRankHandComputed(t *testing.T) {
	rows := []CrossSectionRow{
		csRow("2026-01-05", "A", 1),
		csRow("2026-01-05", "B", 2),
		csRow("2026-01-05", "C", 3),
		csRow("2026-01-05", "D", 4),
		csRow("2026-01-05", "E", 5),
	}
	want := map[string]float64{"A": -2.0 / 3, "B": -1.0 / 3, "C": 0, "D": 1.0 / 3, "E": 2.0 / 3}

	res := mustTransform(t, rankPipeline(TransformMissingExclude), testCfg(), DirectionHigherIsBetter, rows, nil)
	for code, w := range want {
		assertFloat(t, rowByCode(t, res, code).Final, w, 1e-9, code+" rank")
	}
	// 6 个步骤齐全且顺序固定。
	wantSteps := []string{StepMask, StepMissing, StepWinsorize, StepNeutralize, StepStandardize, StepDirection}
	if !reflect.DeepEqual(stepNames(res.Diagnostics), wantSteps) {
		t.Fatalf("步骤顺序错误: %v", stepNames(res.Diagnostics))
	}
	if res.Diagnostics.Degraded {
		t.Fatal("无降级场景不应标记 Degraded")
	}
	if got := stepBy(t, res.Diagnostics, StepMask).MissingCount; got != 0 {
		t.Fatalf("掩码数应为 0: %d", got)
	}
	if got := stepBy(t, res.Diagnostics, StepStandardize).Samples; got != 5 {
		t.Fatalf("标准化样本数应为 5: %d", got)
	}
	// 原始值保留（审计索引）。
	if got := rowByCode(t, res, "C").Original; got != 3 {
		t.Fatalf("Original 应保留原始值 3: %v", got)
	}

	// lower_is_better：方向反转（冻结方向，测试窗不自动翻转）。
	resFlipped := mustTransform(t, rankPipeline(TransformMissingExclude), testCfg(), DirectionLowerIsBetter, rows, nil)
	for code, w := range want {
		assertFloat(t, rowByCode(t, resFlipped, code).Final, -w, 1e-9, code+" 反转")
	}
}

// TestTransformZScoreHandComputed 5 只股票手算 z-score（总体标准差）。
// 值 [1,2,3,4,5]：mean=3，std=√2，z=[-√2,-√2/2,0,√2/2,√2]。
func TestTransformZScoreHandComputed(t *testing.T) {
	p := rankPipeline(TransformMissingExclude)
	p.Standardize = TransformStandardizeZScore
	rows := []CrossSectionRow{
		csRow("2026-01-05", "A", 1),
		csRow("2026-01-05", "B", 2),
		csRow("2026-01-05", "C", 3),
		csRow("2026-01-05", "D", 4),
		csRow("2026-01-05", "E", 5),
	}
	want := map[string]float64{"A": -math.Sqrt2, "B": -math.Sqrt2 / 2, "C": 0, "D": math.Sqrt2 / 2, "E": math.Sqrt2}
	res := mustTransform(t, p, testCfg(), DirectionHigherIsBetter, rows, nil)
	for code, w := range want {
		assertFloat(t, rowByCode(t, res, code).Final, w, 1e-9, code+" zscore")
	}
	resF := mustTransform(t, p, testCfg(), DirectionLowerIsBetter, rows, nil)
	for code, w := range want {
		assertFloat(t, rowByCode(t, resF, code).Final, -w, 1e-9, code+" zscore 反转")
	}
}

// TestTransformTiesHandComputed 并列值共享平均秩（金标准手算）。
// 值 [1,2,2,3]，n=4：平均秩 [1,2.5,2.5,4] → score = 2r/5-1 = [-0.6,0,0,0.6]。
func TestTransformTiesHandComputed(t *testing.T) {
	rows := []CrossSectionRow{
		csRow("2026-01-05", "A", 1),
		csRow("2026-01-05", "B", 2),
		csRow("2026-01-05", "C", 2),
		csRow("2026-01-05", "D", 3),
	}
	res := mustTransform(t, rankPipeline(TransformMissingExclude), testCfg(), DirectionHigherIsBetter, rows, nil)
	want := map[string]float64{"A": -0.6, "B": 0, "C": 0, "D": 0.6}
	for code, w := range want {
		assertFloat(t, rowByCode(t, res, code).Final, w, 1e-9, code+" 并列 rank")
	}
}

// ---- 金标准：NaN / Inf / 全相同值 / 极小截面 ----

// TestTransformMissingExclude exclude 策略：NaN/Inf/无效行剔除，绝不 NaN→0。
func TestTransformMissingExclude(t *testing.T) {
	rows := []CrossSectionRow{
		csRow("2026-01-05", "A", 1),
		csRowMissing("2026-01-05", "B", MissingReasonNotCalculated), // NaN
		csRow("2026-01-05", "C", 2),
		{Date: "2026-01-05", Code: "D", Value: math.Inf(1), Valid: true, Tradable: true}, // +Inf
		csRowInvalid("2026-01-05", "E", 99),                                              // Valid=false
	}
	res := mustTransform(t, rankPipeline(TransformMissingExclude), testCfg(), DirectionHigherIsBetter, rows, nil)
	if got := stepBy(t, res.Diagnostics, StepMissing).MissingCount; got != 3 {
		t.Fatalf("缺失数应为 3（B/D/E）: %d", got)
	}
	for _, code := range []string{"B", "D", "E"} {
		r := rowByCode(t, res, code)
		if !r.Missing {
			t.Fatalf("%s 应标记 Missing", code)
		}
		assertNaN(t, r.Final, code+" Final 应为 NaN")
		assertNaN(t, r.Original, code+" Original 应为 NaN（输入无效）")
		if r.Final == 0 {
			t.Fatalf("%s Final 不得为 0（NaN→0 禁令）", code)
		}
	}
	// 有效行 rank（n=2）：A=-1/3, C=1/3。
	assertFloat(t, rowByCode(t, res, "A").Final, -1.0/3, 1e-9, "A rank")
	assertFloat(t, rowByCode(t, res, "C").Final, 1.0/3, 1e-9, "C rank")
	// 缺失原因透传（审计）。
	if got := rowByCode(t, res, "B").MissingReason; got != MissingReasonNotCalculated {
		t.Fatalf("MissingReason 应透传: %q", got)
	}
}

// TestTransformMissingCrossSectionMedian 当日截面中位数填充（金标准：
// 中位数只使用当日截面，填充不跨日期、不 NaN→0）。
func TestTransformMissingCrossSectionMedian(t *testing.T) {
	p := rankPipeline(TransformMissingCrossSectionMedian)
	// 值 [1,2,NaN,4,100]：当日有效 [1,2,4,100] 中位数 (2+4)/2=3。
	rows := []CrossSectionRow{
		csRow("2026-01-05", "A", 1),
		csRow("2026-01-05", "B", 2),
		csRowMissing("2026-01-05", "C", MissingReasonNotCalculated),
		csRow("2026-01-05", "D", 4),
		csRow("2026-01-05", "E", 100),
	}
	res := mustTransform(t, p, testCfg(), DirectionHigherIsBetter, rows, nil)
	rC := rowByCode(t, res, "C")
	if !rC.Filled {
		t.Fatal("C 应被填充")
	}
	if rC.FilledWith != 3 {
		t.Fatalf("填充值应为当日截面中位数 3: %v", rC.FilledWith)
	}
	if rC.MissingIndicator != 1 {
		t.Fatalf("缺失指示暴露应为 1: %v", rC.MissingIndicator)
	}
	if rC.Missing {
		t.Fatal("填充后不应标记 Missing")
	}
	// 填充后截面 [1,2,3,4,100] rank：C 为中间值 → 0。
	assertFloat(t, rC.Final, 0, 1e-9, "C 填充后 rank")
	assertFloat(t, rowByCode(t, res, "E").Final, 2.0/3, 1e-9, "E rank")
	if got := stepBy(t, res.Diagnostics, StepMissing).MissingCount; got != 1 {
		t.Fatalf("缺失数应为 1: %d", got)
	}
	if res.Diagnostics.Degraded {
		t.Fatal("中位数可用时不应降级")
	}
}

// TestTransformMissingMedianAllMissing 截面全 NaN：中位数不可用 → 质量事件，
// 保持缺失，不填充 0。
func TestTransformMissingMedianAllMissing(t *testing.T) {
	p := rankPipeline(TransformMissingCrossSectionMedian)
	rows := []CrossSectionRow{
		csRowMissing("2026-01-05", "A", MissingReasonNotCalculated),
		csRowMissing("2026-01-05", "B", MissingReasonNotCalculated),
	}
	res := mustTransform(t, p, testCfg(), DirectionHigherIsBetter, rows, nil)
	if !res.Diagnostics.Degraded {
		t.Fatal("全缺失应降级")
	}
	if !hasEvent(res.Diagnostics.AllQualityEvents(), QualityEventAllMissing) {
		t.Fatalf("应输出 all_missing 质量事件: %+v", res.Diagnostics.AllQualityEvents())
	}
	for _, code := range []string{"A", "B"} {
		r := rowByCode(t, res, code)
		if !r.Missing {
			t.Fatalf("%s 应保持 Missing", code)
		}
		if r.Filled {
			t.Fatalf("%s 不应被填充", code)
		}
		assertNaN(t, r.Final, code+" Final 应为 NaN")
	}
}

// TestTransformMissingRenormalizeAvailable 显式变体：缺失股票保留在截面输出
// 中（供组合层按可用因子重归一化权重），不填充、不参与当日统计。
func TestTransformMissingRenormalizeAvailable(t *testing.T) {
	p := rankPipeline(TransformMissingRenormalizeAvailable)
	rows := []CrossSectionRow{
		csRow("2026-01-05", "A", 1),
		csRowMissing("2026-01-05", "B", MissingReasonNotCalculated),
		csRow("2026-01-05", "C", 2),
		csRow("2026-01-05", "D", 3),
	}
	res := mustTransform(t, p, testCfg(), DirectionHigherIsBetter, rows, nil)
	rB := rowByCode(t, res, "B")
	if !rB.Missing {
		t.Fatal("B 应标记 Missing（保留给重归一化）")
	}
	if rB.Filled {
		t.Fatal("B 不应被填充")
	}
	assertNaN(t, rB.Final, "B Final 应为 NaN")
	// 有效股票 rank 只基于 [1,2,3]：n=3 → [-1/2, 0, 1/2]。
	assertFloat(t, rowByCode(t, res, "A").Final, -0.5, 1e-9, "A rank")
	assertFloat(t, rowByCode(t, res, "D").Final, 0.5, 1e-9, "D rank")
	if got := stepBy(t, res.Diagnostics, StepMissing).MissingCount; got != 1 {
		t.Fatalf("缺失数应为 1: %d", got)
	}
}

// TestTransformAllSameValues 全相同值：rank 全部 0（并列平均秩有定义）；
// z-score 零方差 → 全部 0 + 质量事件（不除零、不 NaN）。
func TestTransformAllSameValues(t *testing.T) {
	rows := []CrossSectionRow{
		csRow("2026-01-05", "A", 5),
		csRow("2026-01-05", "B", 5),
		csRow("2026-01-05", "C", 5),
		csRow("2026-01-05", "D", 5),
		csRow("2026-01-05", "E", 5),
	}
	res := mustTransform(t, rankPipeline(TransformMissingExclude), testCfg(), DirectionHigherIsBetter, rows, nil)
	for _, code := range []string{"A", "B", "C", "D", "E"} {
		assertFloat(t, rowByCode(t, res, code).Final, 0, 1e-12, code+" 全相同 rank")
	}
	if res.Diagnostics.Degraded {
		t.Fatal("rank 模式全相同值不应降级（秩有定义）")
	}

	p := rankPipeline(TransformMissingExclude)
	p.Standardize = TransformStandardizeZScore
	resZ := mustTransform(t, p, testCfg(), DirectionHigherIsBetter, rows, nil)
	for _, code := range []string{"A", "B", "C", "D", "E"} {
		assertFloat(t, rowByCode(t, resZ, code).Final, 0, 1e-12, code+" 全相同 zscore")
	}
	if !resZ.Diagnostics.Degraded {
		t.Fatal("零方差 z-score 应标记降级")
	}
	if !stepBy(t, resZ.Diagnostics, StepStandardize).Degraded {
		t.Fatal("standardize 步骤应标记降级")
	}
	if !hasEvent(resZ.Diagnostics.AllQualityEvents(), QualityEventZeroStd) {
		t.Fatal("应输出 zero_std 质量事件")
	}
}

// TestTransformTinyCrossSection 极小截面：有效股票数 < 最小要求 → 去极值跳过
// 并输出不足质量事件（不沿用上一日边界）；标准化仍产出确定值。
func TestTransformTinyCrossSection(t *testing.T) {
	p := rankPipeline(TransformMissingExclude)
	p.Winsorize = WinsorizeSpec{Mode: TransformWinsorizeQuantile, Quantile: 0.1}
	cfg := testCfg()
	cfg.MinValidStocks = 10
	rows := []CrossSectionRow{
		csRow("2026-01-05", "A", 1),
		csRow("2026-01-05", "B", 2),
		csRow("2026-01-05", "C", 3),
	}
	res := mustTransform(t, p, cfg, DirectionHigherIsBetter, rows, nil)
	st := stepBy(t, res.Diagnostics, StepWinsorize)
	if !st.Degraded {
		t.Fatal("去极值应降级跳过")
	}
	if st.TruncatedCount != 0 {
		t.Fatalf("去极值跳过时截断数应为 0: %d", st.TruncatedCount)
	}
	if !res.Diagnostics.Degraded {
		t.Fatal("整体应标记降级")
	}
	// 标准化照常：n=3 → [-0.5, 0, 0.5]。
	assertFloat(t, rowByCode(t, res, "A").Final, -0.5, 1e-9, "A")
	assertFloat(t, rowByCode(t, res, "B").Final, 0, 1e-9, "B")
	assertFloat(t, rowByCode(t, res, "C").Final, 0.5, 1e-9, "C")
}

// TestTransformSingleStock 单只股票截面：rank 无离差 → 0，不除零不报错。
func TestTransformSingleStock(t *testing.T) {
	rows := []CrossSectionRow{csRow("2026-01-05", "A", 7)}
	res := mustTransform(t, rankPipeline(TransformMissingExclude), testCfg(), DirectionHigherIsBetter, rows, nil)
	assertFloat(t, rowByCode(t, res, "A").Final, 0, 1e-12, "单股票 rank")
}

// TestTransformEmptyCrossSection 空截面：不足质量事件，不报错。
func TestTransformEmptyCrossSection(t *testing.T) {
	res := mustTransform(t, rankPipeline(TransformMissingExclude), testCfg(), DirectionHigherIsBetter, nil, nil)
	if !res.Diagnostics.Degraded {
		t.Fatal("空截面应降级")
	}
	if !hasEvent(res.Diagnostics.AllQualityEvents(), QualityEventInsufficient) {
		t.Fatal("空截面应输出 insufficient 质量事件")
	}
	if len(res.Rows) != 0 {
		t.Fatalf("空截面输出应为空: %d", len(res.Rows))
	}
}

// ---- 金标准：去极值手算 ----

// TestTransformWinsorizeQuantileHandComputed 分位去极值手算。
// 值 [1,2,3,4,100]，q=0.2（类型 7：h=(n-1)q=0.8 / 3.2）：
// 下界 = 1+0.8*(2-1) = 1.8，上界 = 4+0.2*(100-4) = 23.2。
// 1→1.8、100→23.2 截断，其余不变；截断后 rank 不变 [-2/3,-1/3,0,1/3,2/3]。
func TestTransformWinsorizeQuantileHandComputed(t *testing.T) {
	p := rankPipeline(TransformMissingExclude)
	p.Winsorize = WinsorizeSpec{Mode: TransformWinsorizeQuantile, Quantile: 0.2}
	rows := []CrossSectionRow{
		csRow("2026-01-05", "A", 1),
		csRow("2026-01-05", "B", 2),
		csRow("2026-01-05", "C", 3),
		csRow("2026-01-05", "D", 4),
		csRow("2026-01-05", "E", 100),
	}
	res := mustTransform(t, p, testCfg(), DirectionHigherIsBetter, rows, nil)
	if got := stepBy(t, res.Diagnostics, StepWinsorize).TruncatedCount; got != 2 {
		t.Fatalf("截断数应为 2（1 和 100）: %d", got)
	}
	if !rowByCode(t, res, "A").Winsorized {
		t.Fatal("值 1 应被截断")
	}
	if !rowByCode(t, res, "E").Winsorized {
		t.Fatal("值 100 应被截断")
	}
	if rowByCode(t, res, "C").Winsorized {
		t.Fatal("值 3 不应被截断")
	}
	want := map[string]float64{"A": -2.0 / 3, "B": -1.0 / 3, "C": 0, "D": 1.0 / 3, "E": 2.0 / 3}
	for code, w := range want {
		assertFloat(t, rowByCode(t, res, code).Final, w, 1e-9, code+" 截断后 rank")
	}
	// 原始值保留（审计索引）。
	if got := rowByCode(t, res, "E").Original; got != 100 {
		t.Fatalf("Original 应保留 100: %v", got)
	}
}

// TestTransformWinsorizeMADHandComputed MAD 去极值手算。
// 值 [1,2,3,4,100]：median=3，MAD=median(|x-3|)=median([2,1,0,1,97])=1，
// 边界 = 3 ± 3*1.4826*1 = [-1.4478, 7.4478]。仅 100 截断到 7.4478。
func TestTransformWinsorizeMADHandComputed(t *testing.T) {
	p := rankPipeline(TransformMissingExclude)
	p.Winsorize = WinsorizeSpec{Mode: TransformWinsorizeMAD, MADK: 3}
	rows := []CrossSectionRow{
		csRow("2026-01-05", "A", 1),
		csRow("2026-01-05", "B", 2),
		csRow("2026-01-05", "C", 3),
		csRow("2026-01-05", "D", 4),
		csRow("2026-01-05", "E", 100),
	}
	res := mustTransform(t, p, testCfg(), DirectionHigherIsBetter, rows, nil)
	if got := stepBy(t, res.Diagnostics, StepWinsorize).TruncatedCount; got != 1 {
		t.Fatalf("截断数应为 1: %d", got)
	}
	rE := rowByCode(t, res, "E")
	if !rE.Winsorized {
		t.Fatal("值 100 应被截断")
	}
	// 截断后值 = 7.4478，仍为截面最大 → rank 2/3。
	assertFloat(t, rE.Final, 2.0/3, 1e-9, "E 截断后 rank")
	if rowByCode(t, res, "A").Winsorized {
		t.Fatal("值 1 不应被截断（1 > -1.4478）")
	}
}

// TestTransformWinsorizeMADZeroMAD MAD=0（半数以上值等于中位数）时边界退化为
// 中位数：不产生 NaN/Inf，等值股票不截断，其余截断到中位数。
func TestTransformWinsorizeMADZeroMAD(t *testing.T) {
	p := rankPipeline(TransformMissingExclude)
	p.Winsorize = WinsorizeSpec{Mode: TransformWinsorizeMAD, MADK: 3}
	rows := []CrossSectionRow{
		csRow("2026-01-05", "A", 1),
		csRow("2026-01-05", "B", 1),
		csRow("2026-01-05", "C", 1),
		csRow("2026-01-05", "D", 2),
		csRow("2026-01-05", "E", 100),
	}
	res := mustTransform(t, p, testCfg(), DirectionHigherIsBetter, rows, nil)
	if got := stepBy(t, res.Diagnostics, StepWinsorize).TruncatedCount; got != 2 {
		t.Fatalf("截断数应为 2（2 与 100 截断到中位数 1）: %d", got)
	}
	if rowByCode(t, res, "A").Winsorized {
		t.Fatal("A=1 等于中位数不应截断")
	}
	if !rowByCode(t, res, "D").Winsorized || !rowByCode(t, res, "E").Winsorized {
		t.Fatal("D=2 与 E=100 应被截断到中位数")
	}
	// 截断后全为 1 → rank 全 0（不 NaN）。
	for _, code := range []string{"A", "B", "C", "D", "E"} {
		assertFloat(t, rowByCode(t, res, code).Final, 0, 1e-12, code+" 全相同 rank")
	}
}

// ---- 金标准：中性化手算与失败协议 ----

// fakeExposures 测试用风险暴露提供者（绝不触碰真实数据源）。
type fakeExposures struct {
	industries map[string]string
	sizes      map[string]float64
	err        error
}

func (f fakeExposures) Exposures(date string, codes []string) ([]string, []float64, error) {
	if f.err != nil {
		return nil, nil, f.err
	}
	inds := make([]string, len(codes))
	sizes := make([]float64, len(codes))
	for i, c := range codes {
		inds[i] = f.industries[c]
		sizes[i] = f.sizes[c]
	}
	return inds, sizes, nil
}

// badLenExposures 返回长度不匹配的暴露（防御性校验）。
type badLenExposures struct{}

func (badLenExposures) Exposures(date string, codes []string) ([]string, []float64, error) {
	return []string{"A"}, nil, nil
}

// TestTransformNeutralizeHandComputed 行业+市值中性化手算（金标准）。
// 4 只股票：A1(A,size=1,val=2) A2(A,size=2,val=3) B1(B,size=1,val=6) B2(B,size=2,val=5)。
// 设计矩阵 [1|I_B|size]（参考行业 A），正规方程解 β=[2.5,3,0]，
// 拟合 [2.5,2.5,5.5,5.5]，残差 [-0.5,0.5,0.5,-0.5]；
// 残差秩 score = 2r/5-1 = [-0.4,-0.4,0.4,0.4]。
func TestTransformNeutralizeHandComputed(t *testing.T) {
	rows := []CrossSectionRow{
		csRow("2026-01-05", "A1", 2),
		csRow("2026-01-05", "A2", 3),
		csRow("2026-01-05", "B1", 6),
		csRow("2026-01-05", "B2", 5),
	}
	exp := fakeExposures{
		industries: map[string]string{"A1": "A", "A2": "A", "B1": "B", "B2": "B"},
		sizes:      map[string]float64{"A1": 1, "A2": 2, "B1": 1, "B2": 2},
	}
	res := mustTransform(t, neutralizePipeline(), testCfg(), DirectionHigherIsBetter, rows, exp)
	want := map[string]float64{"A1": -0.4, "A2": 0.4, "B1": 0.4, "B2": -0.4}
	for code, w := range want {
		r := rowByCode(t, res, code)
		if !r.Neutralized {
			t.Fatalf("%s 应标记 Neutralized", code)
		}
		assertFloat(t, r.Final, w, 1e-9, code+" 中性化后 rank")
	}
	// 原始与中性化对照（设计 §6.3）。
	assertFloat(t, rowByCode(t, res, "A1").NeutralizedFrom, 2, 1e-9, "A1 中性化前值")
	assertFloat(t, rowByCode(t, res, "B1").NeutralizedFrom, 6, 1e-9, "B1 中性化前值")
	// 回归样本数/秩/残差覆盖。
	st := stepBy(t, res.Diagnostics, StepNeutralize)
	if st.Samples != 4 {
		t.Fatalf("回归样本数应为 4: %d", st.Samples)
	}
	if st.RegressionRank != 3 {
		t.Fatalf("回归秩应为 3: %d", st.RegressionRank)
	}
	if st.ResidualCoverage != 1 {
		t.Fatalf("残差覆盖应为 1: %v", st.ResidualCoverage)
	}
	if res.Diagnostics.Degraded {
		t.Fatal("中性化成功不应降级")
	}
}

// TestTransformNeutralizeFailurePolicies 中性化矩阵奇异与行业/市值缺失的
// 失败协议：fail 返回错误；skip_with_degradation 降级跳过并保留中性化前值。
func TestTransformNeutralizeFailurePolicies(t *testing.T) {
	rows := []CrossSectionRow{
		csRow("2026-01-05", "A1", 2),
		csRow("2026-01-05", "A2", 3),
		csRow("2026-01-05", "B1", 6),
		csRow("2026-01-05", "B2", 5),
	}
	p := neutralizePipeline()
	cfg := testCfg()

	// 奇异：size 列与 I_B 列完全共线（size = I_B）。
	singularExp := fakeExposures{
		industries: map[string]string{"A1": "A", "A2": "A", "B1": "B", "B2": "B"},
		sizes:      map[string]float64{"A1": 0, "A2": 0, "B1": 1, "B2": 1},
	}
	if _, err := Transform(p, cfg, DirectionHigherIsBetter, rows, singularExp); err == nil {
		t.Fatal("矩阵奇异 + fail 应返回错误")
	}
	cfgSkip := cfg
	cfgSkip.NeutralizeFailure = NeutralizeFailureSkipWithDegradation
	res, err := Transform(p, cfgSkip, DirectionHigherIsBetter, rows, singularExp)
	if err != nil {
		t.Fatalf("skip_with_degradation 不应返回错误: %v", err)
	}
	if !res.Diagnostics.Degraded {
		t.Fatal("应标记降级")
	}
	evs := res.Diagnostics.AllQualityEvents()
	if !hasEvent(evs, QualityEventMatrixSingular) || !hasEvent(evs, QualityEventNeutralizeSkipped) {
		t.Fatalf("应输出 matrix_singular 与 neutralize_skipped 事件: %+v", evs)
	}
	for _, code := range []string{"A1", "A2", "B1", "B2"} {
		if rowByCode(t, res, code).Neutralized {
			t.Fatalf("%s 不应被中性化（跳过）", code)
		}
	}
	// 跳过中性化后 rank = 原值 [2,3,6,5]：A1=-0.6, A2=-0.2, B2=0.2, B1=0.6。
	assertFloat(t, rowByCode(t, res, "A1").Final, -0.6, 1e-9, "A1")
	assertFloat(t, rowByCode(t, res, "A2").Final, -0.2, 1e-9, "A2")
	assertFloat(t, rowByCode(t, res, "B1").Final, 0.6, 1e-9, "B1")
	assertFloat(t, rowByCode(t, res, "B2").Final, 0.2, 1e-9, "B2")

	// 行业缺失（空串）：fail 报错。
	missingInd := fakeExposures{
		industries: map[string]string{"A1": "A", "A2": "A", "B1": "B", "B2": ""},
		sizes:      map[string]float64{"A1": 1, "A2": 2, "B1": 1, "B2": 2},
	}
	if _, err := Transform(p, cfg, DirectionHigherIsBetter, rows, missingInd); err == nil {
		t.Fatal("行业缺失 + fail 应返回错误")
	}
	// 市值缺失（NaN）：fail 报错；skip 降级跳过。
	missingSize := fakeExposures{
		industries: map[string]string{"A1": "A", "A2": "A", "B1": "B", "B2": "B"},
		sizes:      map[string]float64{"A1": 1, "A2": 2, "B1": 1, "B2": math.NaN()},
	}
	if _, err := Transform(p, cfg, DirectionHigherIsBetter, rows, missingSize); err == nil {
		t.Fatal("市值缺失 + fail 应返回错误")
	}
	res2, err := Transform(p, cfgSkip, DirectionHigherIsBetter, rows, missingSize)
	if err != nil {
		t.Fatalf("skip_with_degradation 不应返回错误: %v", err)
	}
	if !res2.Diagnostics.Degraded || !hasEvent(res2.Diagnostics.AllQualityEvents(), QualityEventExposureMissing) {
		t.Fatalf("市值缺失应降级并输出 exposure_missing 事件: %+v", res2.Diagnostics.AllQualityEvents())
	}

	// 提供者出错：fail 报错；skip 降级。
	providerErr := fakeExposures{err: fmt.Errorf("数据源不可用")}
	if _, err := Transform(p, cfg, DirectionHigherIsBetter, rows, providerErr); err == nil {
		t.Fatal("提供者出错 + fail 应返回错误")
	}
	res3, err := Transform(p, cfgSkip, DirectionHigherIsBetter, rows, providerErr)
	if err != nil {
		t.Fatalf("skip_with_degradation 不应返回错误: %v", err)
	}
	if !res3.Diagnostics.Degraded || !hasEvent(res3.Diagnostics.AllQualityEvents(), QualityEventExposureError) {
		t.Fatal("提供者出错应降级并输出 exposure_error 事件")
	}

	// 缺少提供者：直接报错（fail closed）。
	if _, err := Transform(p, cfg, DirectionHigherIsBetter, rows, nil); err == nil {
		t.Fatal("中性化缺提供者应报错")
	}
	// 提供者返回长度不匹配：fail 报错。
	if _, err := Transform(p, cfg, DirectionHigherIsBetter, rows, badLenExposures{}); err == nil {
		t.Fatal("暴露长度不匹配应报错")
	}
}

// ---- 顺序无关 ----

// TestTransformOrderIndependence 输入顺序变化不改变按代码对齐的输出。
func TestTransformOrderIndependence(t *testing.T) {
	rows := []CrossSectionRow{
		csRow("2026-01-05", "A1", 2),
		csRow("2026-01-05", "A2", 3),
		csRowMissing("2026-01-05", "C1", MissingReasonNotCalculated),
		csRow("2026-01-05", "B1", 6),
		csRow("2026-01-05", "B2", 5),
		csRow("2026-01-05", "D1", 1),
	}
	exp := fakeExposures{
		industries: map[string]string{"A1": "A", "A2": "A", "B1": "B", "B2": "B", "C1": "C", "D1": "A"},
		sizes:      map[string]float64{"A1": 1, "A2": 2, "B1": 1, "B2": 2, "C1": 1, "D1": 3},
	}
	p := neutralizePipeline()
	p.Missing = TransformMissingCrossSectionMedian
	base := mustTransform(t, p, testCfg(), DirectionHigherIsBetter, rows, exp)

	// 固定置换洗牌（Fisher-Yates 结果固定，避免依赖随机源）。
	perm := []int{5, 0, 3, 1, 4, 2}
	shuffled := make([]CrossSectionRow, len(rows))
	for i, pi := range perm {
		shuffled[i] = rows[pi]
	}
	re := mustTransform(t, p, testCfg(), DirectionHigherIsBetter, shuffled, exp)
	if !transformedRowsEqual(base.Rows, re.Rows) {
		t.Fatalf("输入顺序变化不应改变输出:\n%+v\n%+v", base.Rows, re.Rows)
	}
	if !reflect.DeepEqual(base.Diagnostics, re.Diagnostics) {
		t.Fatalf("输入顺序变化不应改变诊断: %+v vs %+v", base.Diagnostics, re.Diagnostics)
	}
	// 输出按代码升序。
	codes := make([]string, len(re.Rows))
	for i, r := range re.Rows {
		codes[i] = r.Code
	}
	if !sort.StringsAreSorted(codes) {
		t.Fatalf("输出应按代码升序: %v", codes)
	}
}

// ---- 完成门槛三禁令 ----

// TestTransformNoFullSampleMinMax 禁令一：禁止全样本 Min-Max。
// 构造两日截面，第二日输出只依赖第二日数据（无跨日状态、无全样本归一化）。
func TestTransformNoFullSampleMinMax(t *testing.T) {
	p := rankPipeline(TransformMissingExclude)
	day1 := []CrossSectionRow{
		csRow("2026-01-02", "A", 1),
		csRow("2026-01-02", "B", 2),
		csRow("2026-01-02", "C", 3),
	}
	day2 := []CrossSectionRow{
		csRow("2026-01-03", "A", 100),
		csRow("2026-01-03", "B", 200),
		csRow("2026-01-03", "C", 300),
	}
	alone := mustTransform(t, p, testCfg(), DirectionHigherIsBetter, day2, nil)
	mustTransform(t, p, testCfg(), DirectionHigherIsBetter, day1, nil)
	after := mustTransform(t, p, testCfg(), DirectionHigherIsBetter, day2, nil)
	if !transformedRowsEqual(alone.Rows, after.Rows) {
		t.Fatalf("第二日输出不得依赖第一日（全样本 Min-Max 禁令）:\n%+v\n%+v", alone.Rows, after.Rows)
	}
	if !reflect.DeepEqual(alone.Diagnostics, after.Diagnostics) {
		t.Fatal("第二日诊断不得依赖第一日")
	}
	// day2 的 rank 只用 day2 截面：n=3 → [-0.5, 0, 0.5]。
	assertFloat(t, rowByCode(t, alone, "A").Final, -0.5, 1e-9, "day2 A")
	assertFloat(t, rowByCode(t, alone, "C").Final, 0.5, 1e-9, "day2 C")
}

// TestTransformWinsorizeBoundsCurrentDayOnly 分位边界只使用当日截面：
// 两日分布不同，各自截断行为只由当日数据决定，不读取未来/历史日期。
func TestTransformWinsorizeBoundsCurrentDayOnly(t *testing.T) {
	p := rankPipeline(TransformMissingExclude)
	p.Winsorize = WinsorizeSpec{Mode: TransformWinsorizeQuantile, Quantile: 0.2}
	day1 := []CrossSectionRow{
		csRow("2026-01-02", "A", 1),
		csRow("2026-01-02", "B", 2),
		csRow("2026-01-02", "C", 3),
		csRow("2026-01-02", "D", 4),
		csRow("2026-01-02", "E", 100),
	}
	day2 := []CrossSectionRow{
		csRow("2026-01-03", "A", 50),
		csRow("2026-01-03", "B", 51),
		csRow("2026-01-03", "C", 52),
		csRow("2026-01-03", "D", 53),
		csRow("2026-01-03", "E", 54),
	}
	// day1：边界 [1.8, 23.2]，截断 1 与 100。
	r1 := mustTransform(t, p, testCfg(), DirectionHigherIsBetter, day1, nil)
	if got := stepBy(t, r1.Diagnostics, StepWinsorize).TruncatedCount; got != 2 {
		t.Fatalf("day1 截断数应为 2: %d", got)
	}
	// day2 单独跑：边界 [50.8, 53.2]，截断 50 与 54 —— 与 day1 无关。
	alone := mustTransform(t, p, testCfg(), DirectionHigherIsBetter, day2, nil)
	if got := stepBy(t, alone.Diagnostics, StepWinsorize).TruncatedCount; got != 2 {
		t.Fatalf("day2 截断数应为 2（50 与 54）: %d", got)
	}
	// 先 day1 再 day2：day2 边界不沿用 day1（"不能沿用上一日边界"）。
	mustTransform(t, p, testCfg(), DirectionHigherIsBetter, day1, nil)
	after := mustTransform(t, p, testCfg(), DirectionHigherIsBetter, day2, nil)
	if !transformedRowsEqual(alone.Rows, after.Rows) {
		t.Fatal("day2 去极值不得沿用 day1 边界")
	}
}

// TestTransformNoFutureData 禁令二：无未来数据传入。
// Transform 只接收当日截面（结构性保证无未来信息参与统计）。
func TestTransformNoFutureData(t *testing.T) {
	day := []CrossSectionRow{
		csRow("2026-01-03", "A", 1),
		csRow("2026-01-03", "B", 2),
		csRowMissing("2026-01-03", "C", MissingReasonNotCalculated),
	}
	res := mustTransform(t, rankPipeline(TransformMissingCrossSectionMedian), testCfg(), DirectionHigherIsBetter, day, nil)
	// 中位数只用当日 [1,2] = 1.5；任何未来值都不存在于输入中。
	if got := rowByCode(t, res, "C").FilledWith; got != 1.5 {
		t.Fatalf("填充值应为当日中位数 1.5: %v", got)
	}
}

// TestTransformMedianUsesCurrentDayOnly 中位数只使用当日截面（金标准）。
// 两日中位数不同，填充值各自取自当日，不跨日期。
func TestTransformMedianUsesCurrentDayOnly(t *testing.T) {
	p := rankPipeline(TransformMissingCrossSectionMedian)
	day1 := []CrossSectionRow{
		csRow("2026-01-02", "A", 1),
		csRow("2026-01-02", "B", 10),
		csRowMissing("2026-01-02", "C", MissingReasonNotCalculated),
	}
	day2 := []CrossSectionRow{
		csRow("2026-01-03", "A", 100),
		csRow("2026-01-03", "B", 300),
		csRowMissing("2026-01-03", "C", MissingReasonNotCalculated),
	}
	r1 := mustTransform(t, p, testCfg(), DirectionHigherIsBetter, day1, nil)
	if got := rowByCode(t, r1, "C").FilledWith; got != 5.5 {
		t.Fatalf("day1 填充值应为 5.5（当日截面中位数）: %v", got)
	}
	r2 := mustTransform(t, p, testCfg(), DirectionHigherIsBetter, day2, nil)
	if got := rowByCode(t, r2, "C").FilledWith; got != 200 {
		t.Fatalf("day2 填充值应为 200（当日截面中位数，禁止跨日期填充）: %v", got)
	}
}

// TestTransformNoNaNZeroSilent 禁令三：NaN→0 不被静默接受。
// 有效 [1,2,4,100]（偶数）中位数 (2+4)/2=3：填充必须是显式的当日中位数并
// 打上 Filled + MissingIndicator 标记；若实现把 NaN 静默变 0，FilledWith 会
// 是 0 且无标记。
func TestTransformNoNaNZeroSilent(t *testing.T) {
	rows := []CrossSectionRow{
		csRow("2026-01-05", "A", 1),
		csRow("2026-01-05", "B", 2),
		csRowMissing("2026-01-05", "M", MissingReasonNotCalculated),
		csRow("2026-01-05", "D", 4),
		csRow("2026-01-05", "E", 100),
	}
	res := mustTransform(t, rankPipeline(TransformMissingCrossSectionMedian), testCfg(), DirectionHigherIsBetter, rows, nil)
	rM := rowByCode(t, res, "M")
	if !rM.Filled {
		t.Fatal("缺失行必须显式标记 Filled")
	}
	if rM.FilledWith != 3 {
		t.Fatalf("填充值必须是当日截面中位数 3（NaN→0 禁令）: %v", rM.FilledWith)
	}
	if rM.MissingIndicator != 1 {
		t.Fatalf("缺失指示暴露应为 1: %v", rM.MissingIndicator)
	}
	// 中位数恰为 0 时填充 0 是显式填充（Filled=true + MissingIndicator=1），
	// 不是静默 NaN→0。
	rowsZero := []CrossSectionRow{
		csRow("2026-01-05", "A", -1),
		csRow("2026-01-05", "B", 1),
		csRowMissing("2026-01-05", "M", MissingReasonNotCalculated),
	}
	resZero := mustTransform(t, rankPipeline(TransformMissingCrossSectionMedian), testCfg(), DirectionHigherIsBetter, rowsZero, nil)
	rM0 := rowByCode(t, resZero, "M")
	if !rM0.Filled || rM0.MissingIndicator != 1 || rM0.FilledWith != 0 {
		t.Fatalf("中位数为 0 的填充必须显式标记（Filled=true, MissingIndicator=1, FilledWith=0）: %+v", rM0)
	}
}

// ---- 掩码与输入校验 ----

// TestTransformMask 不可交易掩码：剔除出截面统计，行保留 Masked 标记。
func TestTransformMask(t *testing.T) {
	rows := []CrossSectionRow{
		csRow("2026-01-05", "A", 1),
		{Date: "2026-01-05", Code: "S", Value: 50, Valid: true, Tradable: false}, // 停牌
		csRow("2026-01-05", "B", 2),
	}
	res := mustTransform(t, rankPipeline(TransformMissingExclude), testCfg(), DirectionHigherIsBetter, rows, nil)
	if got := stepBy(t, res.Diagnostics, StepMask).MissingCount; got != 1 {
		t.Fatalf("掩码数应为 1: %d", got)
	}
	rS := rowByCode(t, res, "S")
	if !rS.Masked {
		t.Fatal("S 应标记 Masked")
	}
	assertNaN(t, rS.Final, "S Final 应为 NaN")
	// 有效股票 rank 只用 [1,2]：n=2 → [-1/3, 1/3]。
	assertFloat(t, rowByCode(t, res, "A").Final, -1.0/3, 1e-9, "A")
	assertFloat(t, rowByCode(t, res, "B").Final, 1.0/3, 1e-9, "B")
}

// TestTransformAllMasked 全部不可交易 → 不足质量事件。
func TestTransformAllMasked(t *testing.T) {
	rows := []CrossSectionRow{
		{Date: "2026-01-05", Code: "A", Value: 1, Valid: true, Tradable: false},
		{Date: "2026-01-05", Code: "B", Value: 2, Valid: true, Tradable: false},
	}
	res := mustTransform(t, rankPipeline(TransformMissingExclude), testCfg(), DirectionHigherIsBetter, rows, nil)
	if !res.Diagnostics.Degraded {
		t.Fatal("全部掩码应降级")
	}
	if !hasEvent(res.Diagnostics.AllQualityEvents(), QualityEventInsufficient) {
		t.Fatal("应输出 insufficient 事件")
	}
}

// TestTransformInputValidation 非法输入：重复代码/日期不一致/空代码/非法方向/
// 非法流水线/中性化缺提供者/非法失败协议 全部报错。
func TestTransformInputValidation(t *testing.T) {
	p := rankPipeline(TransformMissingExclude)
	dup := []CrossSectionRow{
		csRow("2026-01-05", "A", 1),
		csRow("2026-01-05", "A", 2),
	}
	if _, err := Transform(p, testCfg(), DirectionHigherIsBetter, dup, nil); err == nil {
		t.Fatal("重复代码应报错")
	}
	mixed := []CrossSectionRow{
		csRow("2026-01-05", "A", 1),
		csRow("2026-01-06", "B", 2),
	}
	if _, err := Transform(p, testCfg(), DirectionHigherIsBetter, mixed, nil); err == nil {
		t.Fatal("日期不一致应报错")
	}
	emptyCode := []CrossSectionRow{{Date: "2026-01-05", Code: "", Value: 1, Valid: true, Tradable: true}}
	if _, err := Transform(p, testCfg(), DirectionHigherIsBetter, emptyCode, nil); err == nil {
		t.Fatal("空代码应报错")
	}
	single := []CrossSectionRow{csRow("2026-01-05", "A", 1)}
	if _, err := Transform(p, testCfg(), "bogus", single, nil); err == nil {
		t.Fatal("非法方向应报错")
	}
	bad := p
	bad.Missing = "bogus"
	if _, err := Transform(bad, testCfg(), DirectionHigherIsBetter, single, nil); err == nil {
		t.Fatal("非法流水线应报错")
	}
	if _, err := Transform(neutralizePipeline(), testCfg(), DirectionHigherIsBetter, single, nil); err == nil {
		t.Fatal("中性化缺提供者应报错")
	}
	cfg := testCfg()
	cfg.NeutralizeFailure = "bogus"
	if _, err := Transform(neutralizePipeline(), cfg, DirectionHigherIsBetter, single, fakeExposures{}); err == nil {
		t.Fatal("非法失败协议应报错")
	}
}

// TestTransformStepOrderAndDiagnostics 组合流水线逐步诊断：6 步骤顺序固定，
// 每步样本数/缺失数/截断数正确，正常场景无质量事件。
func TestTransformStepOrderAndDiagnostics(t *testing.T) {
	p := rankPipeline(TransformMissingCrossSectionMedian)
	p.Winsorize = WinsorizeSpec{Mode: TransformWinsorizeQuantile, Quantile: 0.2}
	rows := []CrossSectionRow{
		csRow("2026-01-05", "A", 1),
		csRow("2026-01-05", "B", 2),
		csRowMissing("2026-01-05", "M", MissingReasonNotCalculated),
		csRow("2026-01-05", "D", 4),
		csRow("2026-01-05", "E", 100),
	}
	res := mustTransform(t, p, testCfg(), DirectionHigherIsBetter, rows, nil)
	wantSteps := []string{StepMask, StepMissing, StepWinsorize, StepNeutralize, StepStandardize, StepDirection}
	if !reflect.DeepEqual(stepNames(res.Diagnostics), wantSteps) {
		t.Fatalf("步骤顺序错误: %v", stepNames(res.Diagnostics))
	}
	// mask：5 输入，0 掩码。
	if st := stepBy(t, res.Diagnostics, StepMask); st.Samples != 5 || st.MissingCount != 0 {
		t.Fatalf("mask 诊断错误: %+v", st)
	}
	// missing：1 缺失，中位数填充（[1,2,4,100] 中位数 3）。
	if st := stepBy(t, res.Diagnostics, StepMissing); st.Samples != 5 || st.MissingCount != 1 {
		t.Fatalf("missing 诊断错误: %+v", st)
	}
	// winsorize：5 有效值，边界 [1.8, 23.2]，截断 1 与 100 → 2。
	if st := stepBy(t, res.Diagnostics, StepWinsorize); st.Samples != 5 || st.TruncatedCount != 2 {
		t.Fatalf("winsorize 诊断错误: %+v", st)
	}
	// neutralize：none → 无回归。
	if st := stepBy(t, res.Diagnostics, StepNeutralize); st.Samples != 5 || st.RegressionRank != 0 {
		t.Fatalf("neutralize 诊断错误: %+v", st)
	}
	// standardize / direction：5 样本。
	if st := stepBy(t, res.Diagnostics, StepStandardize); st.Samples != 5 {
		t.Fatalf("standardize 诊断错误: %+v", st)
	}
	if st := stepBy(t, res.Diagnostics, StepDirection); st.Samples != 5 {
		t.Fatalf("direction 诊断错误: %+v", st)
	}
	if res.Diagnostics.Degraded {
		t.Fatal("正常场景不应降级")
	}
	if len(res.Diagnostics.AllQualityEvents()) != 0 {
		t.Fatalf("正常场景不应有质量事件: %+v", res.Diagnostics.AllQualityEvents())
	}
}
