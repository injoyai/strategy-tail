package lab

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	common "github.com/injoyai/strategy-tail"
	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/lib/extend"
	"github.com/injoyai/strategy-tail/researchdata"
	sb "github.com/injoyai/strategy-tail/strategies/buy"
	"github.com/injoyai/tdx/lib/xorms"
	"github.com/injoyai/tdx/protocol"
)

// runner_report_test.go Task 6：Runner 双模式元数据（source/strategySpec/comparison）、
// TradeStatsJSON camelCase 输出与旧报告大写键的 Go 反序列化兼容、
// Comparison 摘要合同（实施文档 §4.4）。

// simpleE2ESpec 简单模式端到端配置：无趋势预设 + 两个恒真区间过滤
// （K值/高低位天生 ∈[0,100]），保证过滤不阻断基线交易，便于断言保留率。
func simpleE2ESpec() StrategySpec {
	return StrategySpec{
		Version:      1,
		BasePresetID: "pullback_ma5_plain",
		FactorFilters: []FactorFilterSpec{
			{Kind: "kvalue", Days: 9, Operator: "between", Min: fptr(0), Max: fptr(100)},
			{Kind: "position", Days: 60, Operator: "between", Min: fptr(0), Max: fptr(100)},
		},
		Exit: ExitSpec{HoldingDays: 1},
		Run: StrategyRunSpec{StartYear: 2025, EndYear: 2025,
			SampleMode: "codes", SampleCodes: []string{"sh600000"}},
	}
}

// TestTradeStatsJSONMarshalCamelCase TradeStatsJSON 输出 camelCase；
// +Inf 盈亏比序列化为 null（JSON 不支持 Inf，core.TradeStats 原样会失败）。
func TestTradeStatsJSONMarshalCamelCase(t *testing.T) {
	s := TradeStatsJSON{Total: 5, Win: 3, Loss: 2, WinRate: 60, WinSum: 30, LossSum: 10,
		ProfitFactor: fptr(3), AvgProfit: 4, MaxProfit: 9, MaxLoss: -4}
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	got := string(b)
	for _, key := range []string{
		`"total":5`, `"win":3`, `"loss":2`, `"winRate":60`, `"winSum":30`,
		`"lossSum":10`, `"profitFactor":3`, `"avgProfit":4`, `"maxProfit":9`, `"maxLoss":-4`,
	} {
		if !strings.Contains(got, key) {
			t.Fatalf("缺少 %s: %s", key, got)
		}
	}
	for _, legacy := range []string{"Total", "WinRate", "ProfitFactor", "AvgProfit", "MaxLoss"} {
		if strings.Contains(got, legacy) {
			t.Fatalf("不应输出旧大写键 %s: %s", legacy, got)
		}
	}

	// 无亏损且有盈利 → +Inf → null
	inf := newTradeStatsJSON(core.TradeStats{Total: 2, Win: 2, ProfitFactor: math.Inf(1)})
	if inf.ProfitFactor != nil {
		t.Fatalf("+Inf 盈亏比应为 nil: %v", *inf.ProfitFactor)
	}
	b, err = json.Marshal(inf)
	if err != nil {
		t.Fatalf("+Inf 序列化失败: %v", err)
	}
	if !strings.Contains(string(b), `"profitFactor":null`) {
		t.Fatalf("+Inf 应输出 null: %s", b)
	}

	// 亏损为 0 且无盈利（ProfitFactor=0）不是 +Inf，正常输出
	zero := newTradeStatsJSON(core.TradeStats{Total: 1, Loss: 1})
	if zero.ProfitFactor == nil || *zero.ProfitFactor != 0 {
		t.Fatalf("0 盈亏比应原样输出: %v", zero.ProfitFactor)
	}
}

// TestTradeStatsJSONLegacyUnmarshal 旧报告（core.TradeStats 无 tag 的大写键）
// 仍能被 Go 反序列化读取（encoding/json 大小写不敏感匹配）。
func TestTradeStatsJSONLegacyUnmarshal(t *testing.T) {
	legacy := `{
	  "config": {"startYear":2025,"endYear":2025,"sampleMode":"codes","sampleCodes":["sh600000"],"holdingDays":1,"scriptName":"matrix"},
	  "startedAt": "2025-01-01T10:00:00+08:00",
	  "finishedAt": "2025-01-01T10:01:00+08:00",
	  "variants": [{"name":"基线·全买","stats":{
	    "Total":5,"Win":3,"Loss":2,"WinRate":60,"WinSum":30,"LossSum":10,
	    "ProfitFactor":3,"AvgProfit":4,"MaxProfit":9,"MaxLoss":-4},"trades":[]}],
	  "coverage": {"requested":1,"completed":1,"skipped":0}
	}`
	var rep Report
	if err := json.Unmarshal([]byte(legacy), &rep); err != nil {
		t.Fatal(err)
	}
	if len(rep.Variants) != 1 {
		t.Fatalf("变体数 = %d, want 1", len(rep.Variants))
	}
	s := rep.Variants[0].Stats
	if s.Total != 5 || s.Win != 3 || s.Loss != 2 || s.WinRate != 60 ||
		s.WinSum != 30 || s.LossSum != 10 || s.AvgProfit != 4 ||
		s.MaxProfit != 9 || s.MaxLoss != -4 {
		t.Fatalf("旧大写键未读取: %+v", s)
	}
	if s.ProfitFactor == nil || *s.ProfitFactor != 3 {
		t.Fatalf("旧 ProfitFactor 未读取: %v", s.ProfitFactor)
	}
}

// TestBuildComparisonSummary Comparison 生成合同：基准/组合/单条件摘要与
// 保留率；基准交易为 0 时 RetentionRate 为 null；变体数与 Variants() 合同
// 不符时不生成（防高级模式误生成多条件 Comparison）。
func TestBuildComparisonSummary(t *testing.T) {
	spec := StrategySpec{
		Version:      1,
		BasePresetID: "pullback_ma5_plain",
		FactorFilters: []FactorFilterSpec{
			{Kind: "kvalue", Days: 9, Operator: "between", Min: fptr(0), Max: fptr(100)},
			{Kind: "position", Days: 60, Operator: "between", Min: fptr(0), Max: fptr(100)},
		},
	}
	variants := []VariantReport{
		{Name: "基准 · 无趋势 · 收回 MA5", Stats: TradeStatsJSON{Total: 10}},
		{Name: "单条件 1 · K值(9)", Stats: TradeStatsJSON{Total: 8}},
		{Name: "单条件 2 · N日高低位(60)", Stats: TradeStatsJSON{Total: 6}},
		{Name: "组合增强 · 无趋势 · 收回 MA5", Stats: TradeStatsJSON{Total: 4}},
	}
	cmp := buildComparison(spec, variants)
	if cmp == nil {
		t.Fatal("应生成 Comparison")
	}
	if cmp.BaselineVariant != variants[0].Name || cmp.CombinedVariant != variants[3].Name {
		t.Fatalf("基准/组合名称异常: %+v", cmp)
	}
	if cmp.BaselineTrades != 10 || cmp.CombinedTrades != 4 {
		t.Fatalf("基准/组合交易数异常: %+v", cmp)
	}
	if cmp.RetentionRate == nil || math.Abs(*cmp.RetentionRate-0.4) > 1e-9 {
		t.Fatalf("组合保留率 = %v, want 0.4", cmp.RetentionRate)
	}
	if len(cmp.FactorVariants) != 2 {
		t.Fatalf("单条件摘要数 = %d, want 2", len(cmp.FactorVariants))
	}
	want := []FactorVariantSummary{
		{Index: 1, Kind: "kvalue", Days: 9, Variant: variants[1].Name, Trades: 8, RetentionRate: fptr(0.8)},
		{Index: 2, Kind: "position", Days: 60, Variant: variants[2].Name, Trades: 6, RetentionRate: fptr(0.6)},
	}
	for i, w := range want {
		got := cmp.FactorVariants[i]
		if got.Index != w.Index || got.Kind != w.Kind || got.Days != w.Days ||
			got.Variant != w.Variant || got.Trades != w.Trades {
			t.Fatalf("单条件摘要[%d] = %+v, want %+v", i, got, w)
		}
		if got.RetentionRate == nil || math.Abs(*got.RetentionRate-*w.RetentionRate) > 1e-9 {
			t.Fatalf("单条件摘要[%d] 保留率 = %v, want %v", i, got.RetentionRate, *w.RetentionRate)
		}
	}

	// 基准交易为 0 → 全部 RetentionRate 为 null（JSON null，不伪装成 0）
	zero := append([]VariantReport(nil), variants...)
	zero[0].Stats.Total = 0
	cmp = buildComparison(spec, zero)
	if cmp == nil || cmp.RetentionRate != nil {
		t.Fatalf("基准为 0 时保留率应为 null: %+v", cmp)
	}
	for i, fv := range cmp.FactorVariants {
		if fv.RetentionRate != nil {
			t.Fatalf("基准为 0 时单条件[%d]保留率应为 null: %+v", i, fv)
		}
	}

	// 变体数与 N+2 不一致 → 不生成（高级多变体脚本防误判）
	if buildComparison(spec, variants[:3]) != nil {
		t.Fatal("变体数不符不应生成 Comparison")
	}

	// N=1：组合增强与单条件 1 等价未重复生成，共 2 个变体，
	// 组合字段指向单条件变体（保留率 = 单条件/基准）
	one := StrategySpec{
		Version:      1,
		BasePresetID: "pullback_ma5_plain",
		FactorFilters: []FactorFilterSpec{
			{Kind: "kvalue", Days: 9, Operator: "between", Min: fptr(0), Max: fptr(100)},
		},
	}
	oneVariants := []VariantReport{
		{Name: "基准 · 无趋势 · 收回 MA5", Stats: TradeStatsJSON{Total: 10}},
		{Name: "单条件 1 · K值(9)", Stats: TradeStatsJSON{Total: 5}},
	}
	cmp = buildComparison(one, oneVariants)
	if cmp == nil {
		t.Fatal("N=1 应生成 Comparison")
	}
	if cmp.BaselineTrades != 10 || cmp.CombinedTrades != 5 || cmp.CombinedVariant != oneVariants[1].Name {
		t.Fatalf("N=1 基准/组合异常: %+v", cmp)
	}
	if cmp.RetentionRate == nil || math.Abs(*cmp.RetentionRate-0.5) > 1e-9 {
		t.Fatalf("N=1 组合保留率 = %v, want 0.5", cmp.RetentionRate)
	}
	if len(cmp.FactorVariants) != 1 ||
		cmp.FactorVariants[0].Variant != oneVariants[1].Name || cmp.FactorVariants[0].Trades != 5 {
		t.Fatalf("N=1 单条件摘要异常: %+v", cmp.FactorVariants)
	}
	// N=1 但变体数不符（缺单条件）→ 不生成
	if buildComparison(one, oneVariants[:1]) != nil {
		t.Fatal("N=1 变体数不符不应生成 Comparison")
	}

	// N=0：仅基准 1 个变体，组合字段与基准指向同一变体，无单条件摘要
	none := StrategySpec{Version: 1, BasePresetID: "pullback_ma5_plain"}
	noneVariants := []VariantReport{
		{Name: "基准 · 无趋势 · 收回 MA5", Stats: TradeStatsJSON{Total: 10}},
	}
	cmp = buildComparison(none, noneVariants)
	if cmp == nil {
		t.Fatal("N=0 应生成 Comparison")
	}
	if cmp.BaselineVariant != noneVariants[0].Name || cmp.CombinedVariant != noneVariants[0].Name {
		t.Fatalf("N=0 基准/组合应指向同一变体: %+v", cmp)
	}
	if cmp.RetentionRate == nil || math.Abs(*cmp.RetentionRate-1) > 1e-9 {
		t.Fatalf("N=0 组合保留率 = %v, want 1", cmp.RetentionRate)
	}
	if len(cmp.FactorVariants) != 0 {
		t.Fatalf("N=0 单条件摘要数 = %d, want 0", len(cmp.FactorVariants))
	}
	// N=0 但变体数不符 → 不生成
	if buildComparison(none, append(noneVariants, VariantReport{Name: "多余", Stats: TradeStatsJSON{Total: 1}})) != nil {
		t.Fatal("N=0 变体数不符不应生成 Comparison")
	}
}

// TestRunnerAdvancedReportSourceScript 高级模式报告 source=script，
// 不带 strategySpec/comparison；报告落盘路径与既有行为一致。
func TestRunnerAdvancedReportSourceScript(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	old := common.Pull
	common.Pull = &extend.PullKline{Config: extend.PullKlineConfig{Dir: dir}}
	t.Cleanup(func() { common.Pull = old })
	writeFakeDayDB(t, dir, "sh600000", 250, time.Date(2024, 6, 1, 0, 0, 0, 0, time.Local))

	r := NewRunner()
	cfg := RunConfig{StartYear: 2025, EndYear: 2025,
		SampleMode: "codes", SampleCodes: []string{"sh600000"}, HoldingDays: 1,
		ScriptName: "matrix"}
	variants := []core.Variant{{Name: "基线·全买", Buyer: sb.And{sb.A价格{Min: 2, Max: 120}, sb.A过滤涨停{}}}}
	if err := r.Start(cfg, variants); err != nil {
		t.Fatal(err)
	}
	waitRunnerDone(t, r)

	rep := r.LatestReport()
	if rep == nil {
		t.Fatal("无最新报告")
	}
	if rep.Source != "script" {
		t.Fatalf("高级模式 source = %q, want script", rep.Source)
	}
	if rep.StrategySpec != nil || rep.Comparison != nil {
		t.Fatalf("高级模式不应有 strategySpec/comparison: %+v", rep)
	}
	if rep.Coverage.Completed != 1 || len(rep.Variants) != 1 || rep.Variants[0].Stats.Total == 0 {
		t.Fatalf("高级模式基础行为被破坏: coverage=%+v variants=%d", rep.Coverage, len(rep.Variants))
	}

	// 落盘 report.json：source=script，无 strategySpec/comparison 键
	b, err := os.ReadFile(filepath.Join("output", "trades", rep.RunID(), "report.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"source": "script"`) { // MarshalIndent 冒号带空格
		t.Fatalf("report.json 缺少 source=script: %s", b)
	}
	if strings.Contains(string(b), "strategySpec") || strings.Contains(string(b), "comparison") {
		t.Fatalf("report.json 不应有 strategySpec/comparison: %s", b)
	}
}

// TestRunnerSimpleReportComparison 简单模式端到端：source=simple、
// StrategySpec 原样进报告、N+2 变体顺序、Comparison 摘要与保留率、
// 落盘产物（report.json + CSV + HTML）保持原行为。
func TestRunnerSimpleReportComparison(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	old := common.Pull
	common.Pull = &extend.PullKline{Config: extend.PullKlineConfig{Dir: dir}}
	t.Cleanup(func() { common.Pull = old })
	writePresetDayDB(t, dir, "sh600000", 600, time.Date(2024, 6, 1, 0, 0, 0, 0, time.Local))

	spec := simpleE2ESpec()
	if err := spec.Validate(); err != nil {
		t.Fatal(err)
	}
	variants, err := spec.Variants()
	if err != nil {
		t.Fatal(err)
	}
	if len(variants) != 4 {
		t.Fatalf("变体数 = %d, want 4", len(variants))
	}

	r := NewRunner()
	if err := r.StartStrategy(spec.RunConfig(), variants, spec); err != nil {
		t.Fatal(err)
	}
	waitRunnerDone(t, r)

	rep := r.LatestReport()
	if rep == nil {
		t.Fatal("无最新报告")
	}
	if rep.Source != "simple" {
		t.Fatalf("简单模式 source = %q, want simple", rep.Source)
	}
	if rep.StrategySpec == nil || !reflect.DeepEqual(*rep.StrategySpec, spec) {
		t.Fatalf("报告 StrategySpec 与原始 spec 不一致: %+v", rep.StrategySpec)
	}

	// N+2 顺序：基准 → 单条件 1..N → 组合增强（报告保持 Variants 顺序）
	if len(rep.Variants) != 4 {
		t.Fatalf("报告变体数 = %d, want 4", len(rep.Variants))
	}
	for i, vr := range rep.Variants {
		if vr.Name != variants[i].Name {
			t.Fatalf("变体[%d] 顺序错乱: %q, want %q", i, vr.Name, variants[i].Name)
		}
	}

	// 交易明细与统计口径保持原行为
	base := rep.Variants[0]
	if base.Stats.Total == 0 || len(base.Trades) != base.Stats.Total {
		t.Fatalf("基线应产生交易且明细一致: total=%d trades=%d", base.Stats.Total, len(base.Trades))
	}

	// Comparison 摘要与各变体计数一致，保留率由后端计算
	cmp := rep.Comparison
	if cmp == nil {
		t.Fatal("简单模式应生成 Comparison")
	}
	if cmp.BaselineVariant != base.Name || cmp.BaselineTrades != base.Stats.Total {
		t.Fatalf("Comparison 基准异常: %+v", cmp)
	}
	combined := rep.Variants[len(rep.Variants)-1]
	if cmp.CombinedVariant != combined.Name || cmp.CombinedTrades != combined.Stats.Total {
		t.Fatalf("Comparison 组合异常: %+v", cmp)
	}
	if combined.Stats.Total > base.Stats.Total {
		t.Fatalf("组合交易数不应超过基准: %d > %d", combined.Stats.Total, base.Stats.Total)
	}
	if base.Stats.Total > 0 {
		if cmp.RetentionRate == nil ||
			math.Abs(*cmp.RetentionRate-float64(combined.Stats.Total)/float64(base.Stats.Total)) > 1e-9 {
			t.Fatalf("组合保留率 = %v, want %v", cmp.RetentionRate,
				float64(combined.Stats.Total)/float64(base.Stats.Total))
		}
	}
	if len(cmp.FactorVariants) != 2 {
		t.Fatalf("单条件摘要数 = %d, want 2", len(cmp.FactorVariants))
	}
	for i, fv := range cmp.FactorVariants {
		vr := rep.Variants[i+1]
		if fv.Index != i+1 || fv.Variant != vr.Name || fv.Trades != vr.Stats.Total {
			t.Fatalf("单条件摘要[%d] = %+v, 变体 %+v", i, fv, vr)
		}
		if fv.Kind != spec.FactorFilters[i].Kind || fv.Days != spec.FactorFilters[i].Days {
			t.Fatalf("单条件摘要[%d] kind/days = %s/%d, want %s/%d",
				i, fv.Kind, fv.Days, spec.FactorFilters[i].Kind, spec.FactorFilters[i].Days)
		}
		if base.Stats.Total > 0 && fv.RetentionRate == nil {
			t.Fatalf("单条件摘要[%d] 保留率不应为 null", i)
		}
	}

	// 落盘产物：output/trades/simple/ 下 report.json + 每变体 CSV + 汇总 HTML
	b, err := os.ReadFile(filepath.Join("output", "trades", rep.RunID(), "report.json"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)
	if !strings.Contains(text, `"source": "simple"`) || // MarshalIndent 冒号带空格
		!strings.Contains(text, "strategySpec") || !strings.Contains(text, "comparison") {
		t.Fatalf("report.json 缺少简单模式元数据: %s", text)
	}
	// JSON 往返：报告中的 StrategySpec 反序列化后与原始 spec 一致
	var disk Report
	if err := json.Unmarshal(b, &disk); err != nil {
		t.Fatal(err)
	}
	if disk.StrategySpec == nil || !reflect.DeepEqual(*disk.StrategySpec, spec) {
		t.Fatalf("落盘 StrategySpec 与原始 spec 不一致: %+v", disk.StrategySpec)
	}
	csvs, _ := filepath.Glob(filepath.Join("output", "trades", "simple", "*.csv"))
	if len(csvs) < 4 {
		t.Fatalf("每变体 CSV 未落盘: %d 个", len(csvs))
	}
	htmls, _ := filepath.Glob(filepath.Join("output", "trades", "simple", "*_summary.html"))
	if len(htmls) == 0 {
		t.Fatal("汇总 HTML 未落盘")
	}
}

// TestRunnerUniverseAndProvenance Task 4：universe 依赖注入回退与 DataProvenance
// 快照。当前静态股票池必须显式暴露 unverified PIT，数据来源快照不得伪装已验证。
func TestRunnerUniverseAndProvenance(t *testing.T) {
	// universe 未显式注入时回退 current_static：PIT 恒为 unverified。
	r := NewRunnerWithData(nil)
	snap := r.universe.Snapshot()
	if snap.Mode != researchdata.UniverseModeCurrentStatic || snap.MembershipPIT != researchdata.UniversePITUnverified {
		t.Fatalf("回退快照 = %+v", snap)
	}
	if err := snap.Validate(); err != nil {
		t.Fatal(err)
	}

	// 显式注入自定义股票池生效。
	custom := researchdata.NewStaticUniverse(researchdata.StaticUniverseConfig{ID: "my_pool", Source: "test"})
	r2 := NewRunnerWithDeps(nil, custom)
	if snap := r2.universe.Snapshot(); snap.ID != "my_pool" {
		t.Fatalf("注入池快照 = %+v", snap)
	}

	// 数据来源快照：版本与复权口径不可得时写 unknown，PIT 恒为 unverified。
	dp := r2.dataProvenance()
	if dp.PriceSource != "tdx_local" || dp.PriceVersion != "unknown" || dp.Adjustment != "unknown" {
		t.Fatalf("数据来源快照 = %+v", dp)
	}
	if dp.PITState != pitUnverified {
		t.Fatalf("PITState = %q, want unverified（当前快照无修订历史）", dp.PITState)
	}
	if dp.ResearchDataView != "none" {
		t.Fatalf("未注入 View 时 ResearchDataView = %q, want none", dp.ResearchDataView)
	}
	if _, err := time.Parse(time.RFC3339, dp.SnapshotAt); err != nil {
		t.Fatalf("SnapshotAt 非 RFC3339: %v", err)
	}

	// 注入 View 后 ResearchDataView 标识相应变化。
	r3 := NewRunnerWithDeps(researchdata.NewHub(), custom)
	if dp := r3.dataProvenance(); dp.ResearchDataView != "injected" {
		t.Fatalf("注入 View 后 ResearchDataView = %q, want injected", dp.ResearchDataView)
	}
}

// waitRunnerDone 轮询至任务完成；出错即失败。
func waitRunnerDone(t *testing.T, r *Runner) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		st := r.Status()
		switch st["state"] {
		case "done":
			return
		case "error":
			t.Fatalf("回测失败: %v", st["error"])
		}
		if time.Now().After(deadline) {
			t.Fatalf("回测超时未完成: %v", st)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// writePresetDayDB 写入可触发预设（阴线收回 MA5）的伪造日K：
// 阳/阴线交替，阴线盘中跌破 MA5、收盘收回（close 略高于 MA5），
// 涨跌幅极小（不触涨停过滤），FloatStock 保证流通市值 ≥ 20 亿。
func writePresetDayDB(t *testing.T, dir, code string, days int, base time.Time) {
	t.Helper()
	db, err := xorms.NewSqlite(filepath.Join(dir, extend.DirDay, code+".db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Sync2(new(extend.Kline)); err != nil {
		t.Fatal(err)
	}
	rows := make([]*extend.Kline, 0, days)
	for i := 0; i < days; i++ {
		tm := base.AddDate(0, 0, i)
		k := &protocol.Kline{
			Time:   tm,
			Open:   protocol.Yuan(10.08),
			Close:  protocol.Yuan(10.28),
			High:   protocol.Yuan(10.32),
			Low:    protocol.Yuan(10.00),
			Volume: 10000,
		}
		if i%2 == 1 { // 阴线：盘中跌破 MA5（low 9.95），尾盘收回（close 10.32 ≥ MA5≈10.30）
			k = &protocol.Kline{
				Time:   tm,
				Open:   protocol.Yuan(10.58),
				Close:  protocol.Yuan(10.32),
				High:   protocol.Yuan(10.62),
				Low:    protocol.Yuan(9.95),
				Volume: 10000,
			}
		}
		rows = append(rows, &extend.Kline{
			Unix:       tm.Unix(),
			Kline:      k,
			FloatStock: 2_000_000_000, // close 10.3 × 20亿股 ≈ 206 亿流通市值
			TotalStock: 3_000_000_000,
		})
	}
	if _, err := db.Insert(rows); err != nil {
		t.Fatal(err)
	}
}

// waitRunnerDone 轮询至任务完成；出错即失败。
