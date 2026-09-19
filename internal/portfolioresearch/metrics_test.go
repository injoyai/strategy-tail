package portfolioresearch

// metrics_test.go v2 Task 6 组合报告指标测试（metrics.go）。
//
// 覆盖任务测试列表：
//   - 常数净值（收益 0、波动 0、Sharpe/Sortino/Calmar 除零保护、回撤 0）；
//   - 全正/全负收益序列；
//   - 单次回撤（最大回撤与持续期手算金标准）；
//   - 缺基准（超额/跟踪误差/信息比率 = unavailable + 原因，不阻止绝对收益）；
//   - 零方差（Sharpe/Sortino 除零保护）；
//   - 成本前后（毛 vs 净累计收益差 = 成本拖累，首日费用 0 时精确恒等式）；
//   - 基准手算（超额、跟踪误差、信息比率）；
//   - 跨年年化（年化公式 + 年度收益表）；
//   - 缺失交易日（非交易日不参与统计，年化按样本数连续化）；
//   - 组合质量（换手/现金/持仓/集中度/目标偏离/未成交统计）。
//
// 数值口径（与 metrics.go 文件头注释一致）：
//   - 年化收益 = (1+累计收益)^(252/n) - 1，n = 日收益样本数（净值点数 - 1）；
//   - 年化波动 = sqrt(252) × 日收益样本标准差（n-1 分母）；
//   - Sharpe = (年化收益 - rf) / 年化波动；Sortino 下行波动只计 r<0，年化同乘 sqrt(252)；
//   - 最大回撤 = min(净值/峰值 - 1)（≤0）；回撤持续期 = 峰值日到恢复到峰值/新高
//     的最大交易日差，未恢复的回撤计到序列末；
//   - Calmar = 年化收益 / |最大回撤|。

import (
	"math"
	"strings"
	"testing"
)

// ---- 常数净值：收益 0、波动 0、除零保护、回撤 0 ----

func TestConstantNav(t *testing.T) {
	s := PortfolioSeries{Days: []PortfolioDay{
		{Date: "2026-01-02", GrossNav: 100, NetNav: 100, CashRatio: 0.2, Holdings: 5},
		{Date: "2026-01-05", GrossNav: 100, NetNav: 100, CashRatio: 0.2, Holdings: 5},
		{Date: "2026-01-06", GrossNav: 100, NetNav: 100, CashRatio: 0.2, Holdings: 5},
		{Date: "2026-01-07", GrossNav: 100, NetNav: 100, CashRatio: 0.2, Holdings: 5},
		{Date: "2026-01-08", GrossNav: 100, NetNav: 100, CashRatio: 0.2, Holdings: 5},
	}}
	m, err := ComputeMetrics(s, nil, 0)
	if err != nil {
		t.Fatalf("ComputeMetrics 失败: %v", err)
	}
	if !approx(m.Net.CumulativeReturn, 0, 1e-12) {
		t.Fatalf("累计收益 got %.12g want 0", m.Net.CumulativeReturn)
	}
	if !approx(m.Net.AnnualReturn, 0, 1e-12) {
		t.Fatalf("年化收益 got %.12g want 0", m.Net.AnnualReturn)
	}
	if !approx(m.Net.AnnualVolatility, 0, 1e-12) {
		t.Fatalf("年化波动 got %.12g want 0", m.Net.AnnualVolatility)
	}
	// 零方差：Sharpe/Sortino/Calmar 除零保护（nil + 原因）。
	if m.Net.Sharpe != nil {
		t.Fatalf("常数净值 Sharpe 应为 nil（零方差），got %v", *m.Net.Sharpe)
	}
	if m.Net.SharpeReason == "" {
		t.Fatalf("Sharpe 为 nil 但缺少原因说明")
	}
	if m.Net.Sortino != nil {
		t.Fatalf("常数净值 Sortino 应为 nil（零方差），got %v", *m.Net.Sortino)
	}
	if m.Net.SortinoReason == "" {
		t.Fatalf("Sortino 为 nil 但缺少原因说明")
	}
	if m.Net.MaxDrawdown != 0 || m.Net.MaxDrawdownDuration != 0 {
		t.Fatalf("常数净值回撤应 0: dd=%g dur=%d", m.Net.MaxDrawdown, m.Net.MaxDrawdownDuration)
	}
	if m.Net.Calmar != nil {
		t.Fatalf("常数净值 Calmar 应为 nil（无回撤），got %v", *m.Net.Calmar)
	}
	if m.Net.CalmarReason == "" {
		t.Fatalf("Calmar 为 nil 但缺少原因说明")
	}
	if m.TradingDays != 4 {
		t.Fatalf("有效收益样本天数 got %d want 4", m.TradingDays)
	}
	// 毛/净一致（无费用）。
	if !approx(m.Gross.CumulativeReturn, 0, 1e-12) {
		t.Fatalf("毛累计收益 got %.12g want 0", m.Gross.CumulativeReturn)
	}
	// 缺基准：显式 unavailable + 原因，不阻止绝对收益报告。
	if m.Benchmark.Available {
		t.Fatalf("缺基准时 Benchmark 应为 unavailable")
	}
	if m.Benchmark.UnavailableReason != "missing_benchmark" {
		t.Fatalf("缺基准原因 got %q want missing_benchmark", m.Benchmark.UnavailableReason)
	}
	// 口径说明存在。
	if m.Conventions.TradingDaysPerYear != 252 {
		t.Fatalf("年化交易日数 got %d want 252", m.Conventions.TradingDaysPerYear)
	}
	if m.Conventions.AnnualizeFormula == "" {
		t.Fatalf("年化公式口径说明为空")
	}
}

// ---- 全正收益序列：波动 > 0、Sharpe 可算、Sortino nil（无下行）、回撤 0 ----

func TestAllPositiveReturns(t *testing.T) {
	// 净值 [100,101,103,106,110]；收益 [0.01, 0.01980198, 0.02912621, 0.03773585]。
	s := PortfolioSeries{Days: []PortfolioDay{
		{Date: "2026-01-02", GrossNav: 100, NetNav: 100},
		{Date: "2026-01-05", GrossNav: 101, NetNav: 101},
		{Date: "2026-01-06", GrossNav: 103, NetNav: 103},
		{Date: "2026-01-07", GrossNav: 106, NetNav: 106},
		{Date: "2026-01-08", GrossNav: 110, NetNav: 110},
	}}
	m, err := ComputeMetrics(s, nil, 0)
	if err != nil {
		t.Fatalf("ComputeMetrics 失败: %v", err)
	}
	if !approx(m.Net.CumulativeReturn, 0.10, 1e-9) {
		t.Fatalf("累计收益 got %.9g want 0.10", m.Net.CumulativeReturn)
	}
	// 年化 = 1.1^(252/4) - 1（4 个收益样本，交易日尺度年化）。
	wantAnnual := math.Pow(1.1, 252.0/4) - 1
	if !approx(m.Net.AnnualReturn, wantAnnual, 1e-9) {
		t.Fatalf("年化收益 got %.9g want %.9g", m.Net.AnnualReturn, wantAnnual)
	}
	// 年化波动手算：mean=0.0241660，Σdev²=4.28465e-4，std=0.0119508，
	// 年化 = 0.0119508×sqrt(252) = 0.189712。
	if !approx(m.Net.AnnualVolatility, 0.189712, 1e-4) {
		t.Fatalf("年化波动 got %.6g want 0.189712（±1e-4）", m.Net.AnnualVolatility)
	}
	if m.Net.Sharpe == nil {
		t.Fatalf("全正且有波动时 Sharpe 应可算")
	}
	if m.Net.Sortino != nil {
		t.Fatalf("全正收益 Sortino 应为 nil（无下行收益），got %v", *m.Net.Sortino)
	}
	if m.Net.MaxDrawdown != 0 || m.Net.MaxDrawdownDuration != 0 {
		t.Fatalf("全正收益回撤应 0: dd=%g dur=%d", m.Net.MaxDrawdown, m.Net.MaxDrawdownDuration)
	}
	if m.Net.Calmar != nil {
		t.Fatalf("全正收益 Calmar 应为 nil（无回撤），got %v", *m.Net.Calmar)
	}
}

// ---- 全负收益序列：回撤 = 累计跌幅、Sortino/Calmar 可算且为负 ----

func TestAllNegativeReturns(t *testing.T) {
	// 净值 [100,99,97,94,90]；收益全负；最大回撤 = 90/100-1 = -0.10，持续期 4。
	s := PortfolioSeries{Days: []PortfolioDay{
		{Date: "2026-01-02", GrossNav: 100, NetNav: 100},
		{Date: "2026-01-05", GrossNav: 99, NetNav: 99},
		{Date: "2026-01-06", GrossNav: 97, NetNav: 97},
		{Date: "2026-01-07", GrossNav: 94, NetNav: 94},
		{Date: "2026-01-08", GrossNav: 90, NetNav: 90},
	}}
	m, err := ComputeMetrics(s, nil, 0)
	if err != nil {
		t.Fatalf("ComputeMetrics 失败: %v", err)
	}
	if !approx(m.Net.CumulativeReturn, -0.10, 1e-9) {
		t.Fatalf("累计收益 got %.9g want -0.10", m.Net.CumulativeReturn)
	}
	if !approx(m.Net.MaxDrawdown, -0.10, 1e-9) {
		t.Fatalf("最大回撤 got %.9g want -0.10", m.Net.MaxDrawdown)
	}
	if m.Net.MaxDrawdownDuration != 4 {
		t.Fatalf("回撤持续期 got %d want 4（峰值 100@0 未恢复至序列末）", m.Net.MaxDrawdownDuration)
	}
	if m.Net.Sortino == nil || *m.Net.Sortino >= 0 {
		t.Fatalf("全负收益 Sortino 应可算且为负: %v", m.Net.Sortino)
	}
	if m.Net.Calmar == nil || *m.Net.Calmar >= 0 {
		t.Fatalf("全负收益 Calmar 应可算且为负: %v", m.Net.Calmar)
	}
	if m.Net.Sharpe == nil {
		t.Fatalf("全负收益 Sharpe 应可算（波动 > 0）")
	}
}

// ---- 单次回撤：最大回撤与持续期手算金标准 ----

func TestSingleDrawdown(t *testing.T) {
	// 净值 [100,120,110,105,130]。
	// 手算：峰值 120@1；105/120-1 = -0.125 为最大回撤；
	// 恢复日 130@4（≥120），持续期 = 4-1 = 3 个交易日。
	s := PortfolioSeries{Days: []PortfolioDay{
		{Date: "2026-01-02", GrossNav: 100, NetNav: 100},
		{Date: "2026-01-05", GrossNav: 120, NetNav: 120},
		{Date: "2026-01-06", GrossNav: 110, NetNav: 110},
		{Date: "2026-01-07", GrossNav: 105, NetNav: 105},
		{Date: "2026-01-08", GrossNav: 130, NetNav: 130},
	}}
	m, err := ComputeMetrics(s, nil, 0)
	if err != nil {
		t.Fatalf("ComputeMetrics 失败: %v", err)
	}
	if !approx(m.Net.MaxDrawdown, -0.125, 1e-12) {
		t.Fatalf("最大回撤 got %.12g want -0.125（105/120-1）", m.Net.MaxDrawdown)
	}
	if m.Net.MaxDrawdownDuration != 3 {
		t.Fatalf("回撤持续期 got %d want 3（峰值 120@1 → 恢复 130@4）", m.Net.MaxDrawdownDuration)
	}
	if !approx(m.Net.CumulativeReturn, 0.30, 1e-12) {
		t.Fatalf("累计收益 got %.12g want 0.30", m.Net.CumulativeReturn)
	}
	if m.Net.Calmar == nil {
		t.Fatalf("有回撤时 Calmar 应可算")
	}
	wantCalmar := (math.Pow(1.3, 252.0/4) - 1) / 0.125
	if !approx(*m.Net.Calmar, wantCalmar, 1e-9) {
		t.Fatalf("Calmar got %.9g want %.9g", *m.Net.Calmar, wantCalmar)
	}
}

// ---- 零方差：Sharpe/Sortino/Calmar 除零保护 ----

func TestZeroVarianceShort(t *testing.T) {
	s := PortfolioSeries{Days: []PortfolioDay{
		{Date: "2026-01-02", GrossNav: 100, NetNav: 100},
		{Date: "2026-01-05", GrossNav: 100, NetNav: 100},
		{Date: "2026-01-06", GrossNav: 100, NetNav: 100},
	}}
	m, err := ComputeMetrics(s, nil, 0)
	if err != nil {
		t.Fatalf("ComputeMetrics 失败: %v", err)
	}
	if !approx(m.Net.AnnualVolatility, 0, 1e-12) {
		t.Fatalf("零方差年化波动 got %.12g want 0", m.Net.AnnualVolatility)
	}
	if m.Net.Sharpe != nil || m.Net.Sortino != nil || m.Net.Calmar != nil {
		t.Fatalf("零方差应全部除零保护为 nil: Sharpe=%v Sortino=%v Calmar=%v",
			m.Net.Sharpe, m.Net.Sortino, m.Net.Calmar)
	}
	if m.Net.SharpeReason == "" || m.Net.SortinoReason == "" || m.Net.CalmarReason == "" {
		t.Fatalf("nil 指标缺少原因说明")
	}
}

// ---- 成本前后：毛 vs 净差异 = 成本拖累（首日费用 0 时精确恒等式）----

func TestCostDrag(t *testing.T) {
	// 毛净值 = 净净值 + 累计费用：100=100+0；110=105+5；115=110+5。
	// 毛累计 = 115/100-1 = 0.15；净累计 = 110/100-1 = 0.10；
	// 成本拖累 = Σfees/期初净净值 = 5/100 = 0.05 = 毛累计 - 净累计（首日费用 0 精确）。
	s := PortfolioSeries{Days: []PortfolioDay{
		{Date: "2026-01-02", GrossNav: 100, NetNav: 100, Fees: 0},
		{Date: "2026-01-05", GrossNav: 110, NetNav: 105, Fees: 5},
		{Date: "2026-01-06", GrossNav: 115, NetNav: 110, Fees: 0},
	}}
	m, err := ComputeMetrics(s, nil, 0)
	if err != nil {
		t.Fatalf("ComputeMetrics 失败: %v", err)
	}
	if !approx(m.Gross.CumulativeReturn, 0.15, 1e-12) {
		t.Fatalf("毛累计收益 got %.12g want 0.15", m.Gross.CumulativeReturn)
	}
	if !approx(m.Net.CumulativeReturn, 0.10, 1e-12) {
		t.Fatalf("净累计收益 got %.12g want 0.10", m.Net.CumulativeReturn)
	}
	if !approx(m.Quality.CostDrag, 0.05, 1e-12) {
		t.Fatalf("成本拖累 got %.12g want 0.05", m.Quality.CostDrag)
	}
	// 恒等式：毛累计 - 净累计 = 成本拖累。
	if diff := m.Gross.CumulativeReturn - m.Net.CumulativeReturn; !approx(diff, m.Quality.CostDrag, 1e-9) {
		t.Fatalf("毛-净累计差 %.12g != 成本拖累 %.12g", diff, m.Quality.CostDrag)
	}
}

// ---- 基准手算：超额、跟踪误差、信息比率 ----

func TestBenchmarkManual(t *testing.T) {
	// 组合净值 [100,101,103.02,101.9898]（收益 1%,2%,-1%）；基准 [100,100.5,101.505,100.48995]
	// （收益 0.5%,1%,-1%）；超额 [0.005,0.01,0]。
	// 年化超额 = mean×252 = 0.005×252 = 1.26；
	// 跟踪误差 = std(超额,n-1)×sqrt(252)：dev [0,0.005,-0.005]，Σdev²=5e-5，var=2.5e-5，
	//   std=0.005，TE = 0.005×sqrt(252) = 0.0793725；
	// 信息比率 = 1.26/0.0793725 = sqrt(252) = 15.8745。
	dates := []string{"2026-01-02", "2026-01-05", "2026-01-06", "2026-01-07"}
	port := PortfolioSeries{Days: []PortfolioDay{
		{Date: dates[0], GrossNav: 100, NetNav: 100},
		{Date: dates[1], GrossNav: 101, NetNav: 101},
		{Date: dates[2], GrossNav: 103.02, NetNav: 103.02},
		{Date: dates[3], GrossNav: 101.9898, NetNav: 101.9898},
	}}
	bench := &BenchmarkSeries{Days: []BenchmarkDay{
		{Date: dates[0], Nav: 100},
		{Date: dates[1], Nav: 100.5},
		{Date: dates[2], Nav: 101.505},
		{Date: dates[3], Nav: 100.48995},
	}}
	m, err := ComputeMetrics(port, bench, 0)
	if err != nil {
		t.Fatalf("ComputeMetrics 失败: %v", err)
	}
	if !m.Benchmark.Available {
		t.Fatalf("基准应可用: %s", m.Benchmark.UnavailableReason)
	}
	if m.Benchmark.AnnualExcess == nil || !approx(*m.Benchmark.AnnualExcess, 1.26, 1e-9) {
		t.Fatalf("年化超额 got %v want 1.26", m.Benchmark.AnnualExcess)
	}
	wantTE := 0.005 * math.Sqrt(252)
	if m.Benchmark.TrackingError == nil || !approx(*m.Benchmark.TrackingError, wantTE, 1e-9) {
		t.Fatalf("跟踪误差 got %v want %.9g", m.Benchmark.TrackingError, wantTE)
	}
	wantIR := math.Sqrt(252)
	if m.Benchmark.InformationRatio == nil || !approx(*m.Benchmark.InformationRatio, wantIR, 1e-6) {
		t.Fatalf("信息比率 got %v want %.9g", m.Benchmark.InformationRatio, wantIR)
	}
	if m.Benchmark.SampleDays != 3 {
		t.Fatalf("基准对齐样本 got %d want 3", m.Benchmark.SampleDays)
	}
}

// ---- 缺基准：unavailable + 原因，绝对收益不受影响 ----

func TestMissingBenchmark(t *testing.T) {
	s := PortfolioSeries{Days: []PortfolioDay{
		{Date: "2026-01-02", GrossNav: 100, NetNav: 100},
		{Date: "2026-01-05", GrossNav: 110, NetNav: 110},
		{Date: "2026-01-06", GrossNav: 121, NetNav: 121},
	}}
	m, err := ComputeMetrics(s, nil, 0)
	if err != nil {
		t.Fatalf("ComputeMetrics 失败: %v", err)
	}
	if m.Benchmark.Available {
		t.Fatalf("缺基准应 unavailable")
	}
	if m.Benchmark.UnavailableReason != "missing_benchmark" {
		t.Fatalf("原因 got %q want missing_benchmark", m.Benchmark.UnavailableReason)
	}
	if !approx(m.Net.CumulativeReturn, 0.21, 1e-9) {
		t.Fatalf("缺基准不应阻止绝对收益报告: got %.9g want 0.21", m.Net.CumulativeReturn)
	}
}

// ---- 基准对齐样本不足：unavailable + benchmark_insufficient_alignment ----

func TestBenchmarkInsufficientAlignment(t *testing.T) {
	// 组合 2 天（仅 1 个收益样本），基准 2 天 → 对齐样本仅 1 个 < 2 → unavailable + 原因。
	port := PortfolioSeries{Days: []PortfolioDay{
		{Date: "2026-01-02", GrossNav: 100, NetNav: 100},
		{Date: "2026-01-05", GrossNav: 105, NetNav: 105},
	}}
	bench := &BenchmarkSeries{Days: []BenchmarkDay{
		{Date: "2026-01-02", Nav: 100},
		{Date: "2026-01-05", Nav: 105},
	}}
	m, err := ComputeMetrics(port, bench, 0)
	if err != nil {
		t.Fatalf("ComputeMetrics 失败: %v", err)
	}
	if m.Benchmark.Available {
		t.Fatalf("对齐样本不足应 unavailable")
	}
	if m.Benchmark.UnavailableReason != "benchmark_insufficient_alignment" {
		t.Fatalf("原因 got %q want benchmark_insufficient_alignment", m.Benchmark.UnavailableReason)
	}
	if m.Benchmark.SampleDays != 1 {
		t.Fatalf("对齐样本 got %d want 1", m.Benchmark.SampleDays)
	}
}

// ---- 基准完全同步：跟踪误差 0，信息比率 nil + IRReason（除零保护）----

func TestBenchmarkZeroTE(t *testing.T) {
	// 组合与基准净值完全一致（超额全 0）→ 跟踪误差 0 → IR nil + IRReason。
	port := PortfolioSeries{Days: []PortfolioDay{
		{Date: "2026-01-02", GrossNav: 100, NetNav: 100},
		{Date: "2026-01-05", GrossNav: 101, NetNav: 101},
		{Date: "2026-01-06", GrossNav: 102, NetNav: 102},
		{Date: "2026-01-07", GrossNav: 103, NetNav: 103},
	}}
	bench := &BenchmarkSeries{Days: []BenchmarkDay{
		{Date: "2026-01-02", Nav: 100},
		{Date: "2026-01-05", Nav: 101},
		{Date: "2026-01-06", Nav: 102},
		{Date: "2026-01-07", Nav: 103},
	}}
	m, err := ComputeMetrics(port, bench, 0)
	if err != nil {
		t.Fatalf("ComputeMetrics 失败: %v", err)
	}
	if !m.Benchmark.Available {
		t.Fatalf("基准应可用: %s", m.Benchmark.UnavailableReason)
	}
	if m.Benchmark.TrackingError == nil || !approx(*m.Benchmark.TrackingError, 0, 1e-12) {
		t.Fatalf("跟踪误差应为 0: %v", m.Benchmark.TrackingError)
	}
	if m.Benchmark.InformationRatio != nil {
		t.Fatalf("TE=0 时信息比率应为 nil（除零保护），got %v", *m.Benchmark.InformationRatio)
	}
	if m.Benchmark.IRReason == "" {
		t.Fatalf("IR 为 nil 但缺少原因说明")
	}
	if !strings.Contains(m.Benchmark.IRReason, "跟踪误差为 0") {
		t.Fatalf("IRReason 应说明 TE=0: %q", m.Benchmark.IRReason)
	}
}

// ---- 跨年年化：年化公式 + 年度收益表 ----

func TestAnnualizedAcrossYears(t *testing.T) {
	// 净值 [100,110,121]，日期跨 2024-2025（2 个收益样本，各 +10%）。
	// 年化 = 1.21^(252/2) - 1；年度表 2024 = +10%、2025 = +10%（各 1 个样本）。
	s := PortfolioSeries{Days: []PortfolioDay{
		{Date: "2024-12-30", GrossNav: 100, NetNav: 100},
		{Date: "2024-12-31", GrossNav: 110, NetNav: 110},
		{Date: "2025-01-02", GrossNav: 121, NetNav: 121},
	}}
	m, err := ComputeMetrics(s, nil, 0)
	if err != nil {
		t.Fatalf("ComputeMetrics 失败: %v", err)
	}
	if !approx(m.Net.CumulativeReturn, 0.21, 1e-12) {
		t.Fatalf("累计收益 got %.12g want 0.21", m.Net.CumulativeReturn)
	}
	wantAnnual := math.Pow(1.21, 252.0/2) - 1
	if !approx(m.Net.AnnualReturn, wantAnnual, 1e-9) {
		t.Fatalf("跨年年化 got %.9g want %.9g", m.Net.AnnualReturn, wantAnnual)
	}
	if m.TradingDays != 2 {
		t.Fatalf("收益样本 got %d want 2", m.TradingDays)
	}
	if len(m.Stability.ByYear) != 2 {
		t.Fatalf("年度表应 2 年，got %d", len(m.Stability.ByYear))
	}
	y2024, y2025 := m.Stability.ByYear[0], m.Stability.ByYear[1]
	if y2024.Year != "2024" || y2025.Year != "2025" {
		t.Fatalf("年度顺序 got %s,%s want 2024,2025", y2024.Year, y2025.Year)
	}
	if !approx(y2024.NetReturn, 0.10, 1e-12) || !approx(y2025.NetReturn, 0.10, 1e-12) {
		t.Fatalf("年度收益 got %.12g,%.12g want 0.10,0.10", y2024.NetReturn, y2025.NetReturn)
	}
	if y2024.TradingDays != 1 || y2025.TradingDays != 1 {
		t.Fatalf("年度样本 got %d,%d want 1,1", y2024.TradingDays, y2025.TradingDays)
	}
	if !approx(y2024.GrossReturn, 0.10, 1e-12) {
		t.Fatalf("年度毛收益 got %.12g want 0.10", y2024.GrossReturn)
	}
}

// ---- 缺失交易日：非交易日不参与统计，年化按样本数连续化 ----

func TestMissingTradingDays(t *testing.T) {
	// 日期有间隔（01-06~01-08 缺失），收益只按输入顺序的净值比：+5%,+5%。
	// 年化用样本数 2（而非日历天数）：(1.1025)^(252/2) - 1。
	s := PortfolioSeries{Days: []PortfolioDay{
		{Date: "2026-01-02", GrossNav: 100, NetNav: 100},
		{Date: "2026-01-05", GrossNav: 105, NetNav: 105},
		{Date: "2026-01-09", GrossNav: 110.25, NetNav: 110.25},
	}}
	m, err := ComputeMetrics(s, nil, 0)
	if err != nil {
		t.Fatalf("ComputeMetrics 失败: %v", err)
	}
	if m.TradingDays != 2 {
		t.Fatalf("有效收益样本 got %d want 2", m.TradingDays)
	}
	if !approx(m.Net.CumulativeReturn, 0.1025, 1e-12) {
		t.Fatalf("累计收益 got %.12g want 0.1025", m.Net.CumulativeReturn)
	}
	wantAnnual := math.Pow(1.1025, 252.0/2) - 1
	if !approx(m.Net.AnnualReturn, wantAnnual, 1e-9) {
		t.Fatalf("缺失交易日年化 got %.9g want %.9g（按样本数 2 连续化）", m.Net.AnnualReturn, wantAnnual)
	}
}

// ---- 组合质量：换手/现金/持仓/集中度/目标偏离/未成交统计 ----

func TestQualityMetrics(t *testing.T) {
	s := PortfolioSeries{Days: []PortfolioDay{
		{
			Date:     "2026-01-02",
			GrossNav: 100, NetNav: 100,
			Fees: 0, Turnover: 0.1, CashRatio: 0.2, Holdings: 3,
			TargetWeights: map[string]float64{"A": 0.3, "B": 0.3, "C": 0.2},
			ActualWeights: map[string]float64{"A": 0.3, "B": 0.25, "C": 0.25},
			Rejections: []Rejection{
				{Code: "D", Side: SideBuy, Reason: UnfilledLimitUp, Shares: 100},
			},
		},
		{
			Date:     "2026-01-05",
			GrossNav: 101, NetNav: 101,
			Fees: 0.5, Turnover: 0.2, CashRatio: 0.1, Holdings: 4,
			TargetWeights: map[string]float64{"A": 0.4, "B": 0.3, "C": 0.2},
			ActualWeights: map[string]float64{"A": 0.4, "B": 0.3, "C": 0.2},
			Rejections: []Rejection{
				{Code: "E", Side: SideBuy, Reason: UnfilledSuspended, Shares: 50},
				{Code: "E", Side: SideBuy, Reason: UnfilledSuspended, Shares: 50},
			},
		},
	}}
	m, err := ComputeMetrics(s, nil, 0)
	if err != nil {
		t.Fatalf("ComputeMetrics 失败: %v", err)
	}
	// 日换手均值 = (0.1+0.2)/2 = 0.15；年换手 = 0.15×252 = 37.8。
	if !approx(m.Quality.DailyTurnover, 0.15, 1e-12) {
		t.Fatalf("日换手 got %.12g want 0.15", m.Quality.DailyTurnover)
	}
	if !approx(m.Quality.AnnualTurnover, 0.15*252, 1e-9) {
		t.Fatalf("年换手 got %.9g want %.9g", m.Quality.AnnualTurnover, 0.15*252)
	}
	// 成本拖累 = Σfees/期初净净值 = 0.5/100 = 0.005。
	if !approx(m.Quality.CostDrag, 0.005, 1e-12) {
		t.Fatalf("成本拖累 got %.12g want 0.005", m.Quality.CostDrag)
	}
	// 现金均值 = (0.2+0.1)/2 = 0.15；持仓数均值 = (3+4)/2 = 3.5。
	if !approx(m.Quality.AvgCashRatio, 0.15, 1e-12) {
		t.Fatalf("现金比例均值 got %.12g want 0.15", m.Quality.AvgCashRatio)
	}
	if !approx(m.Quality.AvgHoldings, 3.5, 1e-12) {
		t.Fatalf("持仓数均值 got %.12g want 3.5", m.Quality.AvgHoldings)
	}
	// 单票集中度：最大权重跨日最大 = max(0.3, 0.4) = 0.4。
	if !approx(m.Quality.MaxConcentration, 0.4, 1e-12) {
		t.Fatalf("最大单票权重 got %.12g want 0.4", m.Quality.MaxConcentration)
	}
	// HHI：day1 = 0.09+0.0625+0.0625 = 0.215；day2 = 0.16+0.09+0.04 = 0.29；均值 = 0.2525。
	if !approx(m.Quality.AvgHHI, 0.2525, 1e-12) {
		t.Fatalf("平均 HHI got %.12g want 0.2525", m.Quality.AvgHHI)
	}
	// 目标-实际偏离：day1 = |0.3-0.3|+|0.25-0.3|+|0.25-0.2| = 0.10；day2 = 0。
	if !approx(m.Quality.TargetDeviation.Mean, 0.05, 1e-12) {
		t.Fatalf("偏离均值 got %.12g want 0.05", m.Quality.TargetDeviation.Mean)
	}
	if !approx(m.Quality.TargetDeviation.Max, 0.10, 1e-12) {
		t.Fatalf("偏离最大 got %.12g want 0.10", m.Quality.TargetDeviation.Max)
	}
	// 未成交：总数 3（limit_up 1、suspended 2）。
	if m.Quality.Unfilled.Count != 3 {
		t.Fatalf("未成交总数 got %d want 3", m.Quality.Unfilled.Count)
	}
	if m.Quality.Unfilled.ByReason[UnfilledLimitUp] != 1 || m.Quality.Unfilled.ByReason[UnfilledSuspended] != 2 {
		t.Fatalf("未成交原因统计 got %v want limit_up=1 suspended=2", m.Quality.Unfilled.ByReason)
	}
}

// ---- 输入校验 ----

func TestPortfolioSeriesValidate(t *testing.T) {
	if err := (PortfolioSeries{}).Validate(); err == nil {
		t.Fatalf("空时序应校验失败")
	}
	// 日期无序。
	s := PortfolioSeries{Days: []PortfolioDay{
		{Date: "2026-01-05", GrossNav: 100, NetNav: 100},
		{Date: "2026-01-02", GrossNav: 100, NetNav: 100},
	}}
	if err := s.Validate(); err == nil {
		t.Fatalf("日期无序应校验失败")
	}
	// 净值非正。
	s = PortfolioSeries{Days: []PortfolioDay{
		{Date: "2026-01-02", GrossNav: 100, NetNav: 0},
	}}
	if err := s.Validate(); err == nil {
		t.Fatalf("净净值 0 应校验失败")
	}
	// 毛净值小于净净值（违反 毛 = 净 + 累计费用）。
	s = PortfolioSeries{Days: []PortfolioDay{
		{Date: "2026-01-02", GrossNav: 100, NetNav: 101},
	}}
	if err := s.Validate(); err == nil {
		t.Fatalf("毛净值 < 净净值应校验失败")
	}
	// 现金比例越界。
	s = PortfolioSeries{Days: []PortfolioDay{
		{Date: "2026-01-02", GrossNav: 100, NetNav: 100, CashRatio: 1.5},
	}}
	if err := s.Validate(); err == nil {
		t.Fatalf("现金比例 >1 应校验失败")
	}
}
