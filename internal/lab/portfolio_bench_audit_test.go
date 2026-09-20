package lab

// portfolio_bench_audit_test.go v2 Task 11 性能基准（计划 Task 11 §性能）。
//
// 代表性负载：20 因子 × 300 只股票 × 3 年（约 750 交易日）。测量对象为
// v2 组合研究链路的核心计算（截面变换 → 合成 → 目标 → 执行 → 会计 → 指标
// → 归因），与 runner 的 transformFactors/combinePortfolio/runPortfolioDays
// 同函数同口径（跳过 researchrun 的 DB 加载 I/O，那是既有执行层职责）。
//
// 输出：峰值内存（采样 HeapAlloc）、总时长、产物总大小（report.json 序列化
// 字节 + 四类 CSV 估算字节）。机器环境在 TestAuditBenchmarkEnv 打印，供
// Task 12 发布门禁参考。
//
// 运行方式：
//
//	go test ./internal/lab -run TestAuditBenchmarkEnv -v          # 环境信息
//	go test ./internal/lab -bench BenchmarkAuditPortfolio -run ^$ -benchtime 1x

import (
	"encoding/json"
	"fmt"
	"math"
	"runtime"
	"testing"
	"time"

	"github.com/injoyai/strategy-tail/internal/portfolioresearch"
)

// benchPortfolioData 构造内存版 portfolioRunData：nFactor 因子 × nStock 股票
// × nDay 交易日（确定性伪随机，无系统随机源）。
func benchPortfolioData(nFactor, nStock, nDay int) *portfolioRunData {
	codes := make([]string, nStock)
	for j := range codes {
		codes[j] = fmt.Sprintf("sh%06d", 600000+j)
	}
	dates := make([]string, nDay)
	base := time.Date(2024, 1, 1, 0, 0, 0, 0, time.Local)
	for i := range dates {
		dates[i] = base.AddDate(0, 0, i).Format(time.DateOnly)
	}
	closeM := make(map[string]map[string]float64, nDay)
	openM := make(map[string]map[string]float64, nDay)
	values := make(map[string]map[string]map[string]float64, nFactor)
	for k := 0; k < nFactor; k++ {
		// key 与 factorKey(ref) 同构（候选/因子名/天数），transformFactors 可直接读取。
		values[fmt.Sprintf("fc_b%d/momentum/2", k)] = make(map[string]map[string]float64, nDay)
	}
	// 确定性伪随机：线性同余种子（无 math/rand 依赖，保证可复现）。
	seed := uint64(42)
	next := func() float64 {
		seed = seed*6364136223846793005 + 1442695040888963407
		return float64(seed>>33) / float64(1<<31)
	}
	for i, d := range dates {
		closeM[d] = make(map[string]float64, nStock)
		openM[d] = make(map[string]float64, nStock)
		for j, c := range codes {
			basePrice := 10 + 0.02*float64(i) + 0.1*float64(j%10)
			p := basePrice * (1 + 0.03*next())
			closeM[d][c] = math.Round(p*1000) / 1000
			openM[d][c] = closeM[d][c] * 0.998
			for k := 0; k < nFactor; k++ {
				vals := values[fmt.Sprintf("fc_b%d/momentum/2", k)]
				if vals[d] == nil {
					vals[d] = make(map[string]float64, nStock)
				}
				// 每 10 个因子取一个含 NaN 缺失（覆盖缺失路径）。
				v := 0.5*next() + 0.05*float64(j) - 0.03*float64(i%20)
				if k%10 == 3 && next() < 0.02 {
					v = math.NaN()
				}
				vals[d][c] = v
			}
		}
	}
	prevClose := make(map[string]map[string]float64, nDay)
	for i, d := range dates {
		if i == 0 {
			continue
		}
		prevClose[d] = closeM[dates[i-1]]
	}
	return &portfolioRunData{
		codes: codes, dates: dates, values: values,
		close: closeM, open: openM, prevClose: prevClose,
	}
}

// benchGoldenModel 基准模型：20 个因子（与负载一致）、TopN=50、等权秩、
// 标准费率。与 runner 链同构。
func benchGoldenModel() portfolioresearch.FactorModel {
	m := auditGoldenModel()
	refs := make([]portfolioresearch.ValidatedFactorRef, 0, 20)
	for k := 0; k < 20; k++ {
		refs = append(refs, portfolioresearch.ValidatedFactorRef{
			CandidateID: fmt.Sprintf("fc_b%d", k), CandidateRevision: 1,
			ValidationID: fmt.Sprintf("fv_b%d", k),
			FactorKind:   "momentum", FactorDays: 2, ImplementationVersion: 1,
			Direction:      portfolioresearch.DirectionHigherIsBetter,
			EvidenceClass:  portfolioresearch.EvidenceRetrospective,
			PrimaryHorizon: 1,
			DataSnapshot:   portfolioresearch.DataSnapshot{UniverseMode: "current_static", PriceSource: "local-klines"},
		})
	}
	m.ValidatedFactors = refs
	m.PortfolioPolicy = portfolioresearch.PortfolioPolicy{
		Selection: portfolioresearch.PortfolioSelectionTopN, TopN: 50, CashBuffer: 0.05,
	}
	return m
}

// memSampler 峰值内存采样（后台协程读 HeapAlloc，停止后返回峰值字节）。
func memSampler(stop <-chan struct{}, peak *uint64) {
	for {
		select {
		case <-stop:
			return
		default:
		}
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		if m.HeapAlloc > *peak {
			*peak = m.HeapAlloc
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// runAuditBenchmark 执行完整基准负载（20 因子 × 300 股 × 750 日），返回
// (总时长, 峰值内存, 产物总大小, 交易日数, 期末净值)。
func runAuditBenchmark(b testing.TB) (time.Duration, uint64, int64, int, float64) {
	const (
		nFactor = 20
		nStock  = 300
		nDay    = 750
	)
	data := benchPortfolioData(nFactor, nStock, nDay)
	model := benchGoldenModel()

	stop := make(chan struct{})
	var peak uint64
	go memSampler(stop, &peak)

	start := time.Now()
	r := &Runner{}
	transformed, err := r.transformFactors(model, data, nil)
	if err != nil {
		b.Fatalf("transformFactors: %v", err)
	}
	// 与 runner 同链：变换输出 → buildFactorSeries → combinePortfolio。
	factors := buildFactorSeries(model, transformed)
	scores, err := combinePortfolio(factors, model.TransformPipeline.Missing, nil, false)
	if err != nil {
		b.Fatalf("combinePortfolio: %v", err)
	}
	ledgers, _, err := runPortfolioDays(model, scores, data, data.dates, portfolioNotional, nil, nil)
	if err != nil {
		b.Fatalf("runPortfolioDays: %v", err)
	}
	series := portfolioresearch.FromLedgers(ledgers)
	metrics, err := portfolioresearch.ComputeMetrics(series, nil, 0)
	if err != nil {
		b.Fatalf("ComputeMetrics: %v", err)
	}
	attribution, err := buildAttribution(ledgers, map[string]portfolioresearch.TargetPortfolio{}, data, series, portfolioNotional)
	if err != nil {
		b.Fatalf("ComputeAttribution: %v", err)
	}
	rep := &PortfolioReport{
		SchemaVersion: 1, ModelID: model.ModelID, ModelRevision: 1,
		Metrics: metrics, Attribution: attribution,
		Nav: make([]NavPoint, 0, len(series.Days)),
	}
	for _, d := range series.Days {
		rep.Nav = append(rep.Nav, NavPoint{Date: d.Date, GrossNav: d.GrossNav, NetNav: d.NetNav})
	}
	buf, err := json.Marshal(rep)
	if err != nil {
		b.Fatalf("report.json 序列化: %v", err)
	}
	elapsed := time.Since(start)
	close(stop)
	// 产物总大小 = report.json 字节 + 四类 CSV 估算（行数 × 行字节）。
	rowBytes := int64(0)
	for _, l := range ledgers {
		rowBytes += int64(len(l.Intents))*40 + int64(len(l.Fills))*72 + int64(len(l.EndHoldings))*24
	}
	csvEstimate := rowBytes + int64(len(ledgers))*64
	endNav := 0.0
	if len(series.Days) > 0 {
		endNav = series.Days[len(series.Days)-1].NetNav
	}
	return elapsed, peak, int64(len(buf)) + csvEstimate, len(ledgers), endNav
}

// BenchmarkAuditPortfolio 性能基准入口（go test -bench BenchmarkAuditPortfolio）。
func BenchmarkAuditPortfolio(b *testing.B) {
	b.ReportAllocs()
	elapsed, peak, size, days, endNav := runAuditBenchmark(b)
	b.ReportMetric(float64(elapsed.Microseconds())/1e6, "sec/op")
	b.ReportMetric(float64(peak)/1024/1024, "peakMB")
	b.ReportMetric(float64(size), "artifactBytes")
	b.ReportMetric(float64(days), "tradingDays")
	b.ReportMetric(endNav, "endNav")
}

// TestAuditBenchmarkEnv 机器环境信息（go version/OS/CPU/内存）+ 一次完整负载
// 计时（供 Task 12 发布门禁参考；测试内直接输出，不写 .md）。
func TestAuditBenchmarkEnv(t *testing.T) {
	elapsed, peak, size, days, endNav := runAuditBenchmark(t)
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	t.Logf("机器环境: go=%s os=%s arch=%s cpu=%d",
		runtime.Version(), runtime.GOOS, runtime.GOARCH, runtime.NumCPU())
	t.Logf("负载: 20 因子 × 300 股票 × 750 交易日（%d 个有效交易日）", days)
	t.Logf("总时长: %.3f s", elapsed.Seconds())
	t.Logf("峰值内存(HeapAlloc 采样): %.1f MB", float64(peak)/1024/1024)
	t.Logf("总分配内存: %.1f MB", float64(m.TotalAlloc)/1024/1024)
	t.Logf("产物总大小估算: %.2f MB（report.json 序列化 + 四类 CSV 行估算）", float64(size)/1024/1024)
	t.Logf("期末净值: %.6f（净口径）", endNav)
	if endNav <= 0 || math.IsNaN(endNav) {
		t.Fatalf("基准负载期末净值非法: %v", endNav)
	}
}
