package lab

import (
	"errors"
	"fmt"
	"math"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/internal/lab/builder"
	"github.com/injoyai/strategy-tail/lib/extend"
	sb "github.com/injoyai/strategy-tail/strategies/buy"
	"github.com/injoyai/tdx/v2/protocol"
)

// practice_prepare_test.go 盘感练习出题测试：年份/买入侧校验、扫描求值、
// 蓄水池等概率、失败与取消路径，以及经 Runner.StartPractice 的端到端出题。
// 全部使用临时目录与注入的固定行情，不触碰真实行情库与策略库。

// fakePracticeSource 固定行情来源：按 map 提供日线；无记录的代码返回错误
// （模拟本地库无数据），显式空切片表示"有股票但无行情"。
type fakePracticeSource struct {
	codes  []string
	klines map[string]extend.Klines
}

func (f fakePracticeSource) Codes() ([]string, error) { return f.codes, nil }

func (f fakePracticeSource) DayKlinesAll(code string) (extend.Klines, error) {
	ks, ok := f.klines[code]
	if !ok {
		return nil, fmt.Errorf("本地无 %s 日线", code)
	}
	return ks, nil
}

// practiceTestSpec 最小可用买入策略：buy_all（全部样本买入），历史前缀恒命中。
func practiceTestSpec() builder.BuilderSpec {
	return builder.BuilderSpec{
		Version: 1,
		Name:    "盘感练习测试",
		Buy:     &builder.BuilderNode{Type: "condition", ComponentID: "buy_all"},
	}
}

// practiceTestBuyerFn 测试策略的 Buyer 工厂（practiceScanParams.Buyer 签名）。
// BuilderSpec.Buyer 为指针接收者方法值，须在可寻址变量上取用。
func practiceTestBuyerFn() func() (core.Buyer, error) {
	spec := practiceTestSpec()
	return func() (core.Buyer, error) { return spec.Buyer() }
}

// practiceTestKlinesFrom 从 start（含）起每日一根的整元日 K（O=H=L=C）。
func practiceTestKlinesFrom(start time.Time, closes []float64) extend.Klines {
	ks := make(extend.Klines, len(closes))
	for i, c := range closes {
		at := start.AddDate(0, 0, i)
		price := c
		ks[i] = &extend.Kline{
			Unix: at.Unix(),
			Kline: &protocol.Kline{
				Time: at, Open: price, High: price, Low: price, Close: price, Volume: 10000,
			},
		}
	}
	return ks
}

// newPracticeRunnerForTest 构造带临时存储与固定行情来源的 Runner。
func newPracticeRunnerForTest(t *testing.T, src practiceKlineSource) (*Runner, *PracticeStore, *BuilderStore) {
	t.Helper()
	r := NewRunner()
	ps := NewPracticeStore(t.TempDir())
	bs := NewBuilderStore(t.TempDir())
	r.SetPracticeStores(ps, bs)
	r.practiceSource = src
	return r, ps, bs
}

// waitForPracticePhase 轮询直到会话离开 preparing（出题任务完成）。
func waitForPracticePhase(t *testing.T, ps *PracticeStore, id string) (*practiceSession, *practiceMarket) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		sess, m, err := ps.Get(id)
		if err != nil {
			t.Fatalf("读取会话 %s 失败: %v", id, err)
		}
		if sess.Phase != practicePhasePreparing {
			return sess, m
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("出题任务 5s 内未完成: %s", id)
	return nil, nil
}

// TestPracticeValidateYears 年份范围校验：正数、升序、不早于 1990、不超当前年。
func TestPracticeValidateYears(t *testing.T) {
	cur := time.Now().Year()
	for _, tc := range []struct {
		from, to int
		wantErr  bool
	}{
		{2020, 2024, false},
		{2024, 2024, false},
		{2024, 2020, true},    // 倒序
		{0, 2024, true},       // 非正数
		{-1, 2024, true},      // 负数
		{1989, 2000, true},    // 早于 1990
		{2020, cur + 1, true}, // 超当前年
	} {
		err := practiceValidateYears(tc.from, tc.to)
		if (err != nil) != tc.wantErr {
			t.Fatalf("practiceValidateYears(%d,%d): err=%v wantErr=%v", tc.from, tc.to, err, tc.wantErr)
		}
	}
}

// TestPracticeValidateBuySide 投影只保留买入侧：卖出侧剥离、空买入拦截、
// save 模式校验仍生效（空名拒绝）。
func TestPracticeValidateBuySide(t *testing.T) {
	// 空买入（save 模式可保存的策略）出题拒绝。
	_, err := practiceValidateBuySide(builder.BuilderSpec{Version: 1, Name: "无买入"})
	if !errors.Is(err, errPracticeBuyEmptyProjected) {
		t.Fatalf("空买入应拒绝: %v", err)
	}

	// 合法策略：卖出侧不进入投影。
	spec := practiceTestSpec()
	spec.SellSignal = &builder.BuilderNode{Type: "condition", ComponentID: "buy_all"}
	proj, err := practiceValidateBuySide(spec)
	if err != nil {
		t.Fatalf("合法策略应通过: %v", err)
	}
	if proj.SellSignal != nil {
		t.Fatal("投影应剥离卖出侧")
	}
	if proj.Buy == nil || proj.Buy.ComponentID != "buy_all" {
		t.Fatalf("投影应保留买入侧: %+v", proj.Buy)
	}
	if _, err := proj.Buyer(); err != nil {
		t.Fatalf("投影买入侧应可构建: %v", err)
	}

	// 空名走 save 校验拒绝。
	spec2 := practiceTestSpec()
	spec2.Name = ""
	if _, err := practiceValidateBuySide(spec2); err == nil {
		t.Fatal("空名应被 save 校验拒绝")
	}
}

// TestPracticeScanReservoir 蓄水池抽样：命中序号为跨股票全局计数；
// choose(n==1) 取第一命中，choose 恒真取最后命中，统计数完整。
func TestPracticeScanReservoir(t *testing.T) {
	src := fakePracticeSource{
		codes: []string{"sh600001", "sh600002"},
		klines: map[string]extend.Klines{
			"sh600001": practiceTestKlines([]float64{10, 11, 12}), // 3 次命中
			"sh600002": practiceTestKlines([]float64{10, 11}),     // 2 次命中
		},
	}
	// 记录 choose 被调用的全局序号。
	var seq []int
	pick := func(n int) bool { seq = append(seq, n); return false }

	_, cov, err := practiceScan(practiceScanParams{
		Codes: src.codes, Source: src, FromYear: 2024, ToYear: 2024,
		Now: time.Now(), Buyer: practiceTestBuyerFn(), Choose: pick,
	})
	if err != nil {
		t.Fatalf("practiceScan: %v", err)
	}
	if len(seq) != 5 {
		t.Fatalf("命中序号应有 5 个（跨股票累计）: %v", seq)
	}
	for i, n := range seq {
		if n != i+1 {
			t.Fatalf("第 %d 次命中序号应为 %d: %d", i, i+1, n)
		}
	}
	if cov.Hits != 5 || cov.WithData != 2 || cov.Scanned != 2 || cov.Failed != 0 {
		t.Fatalf("覆盖统计不正确: %+v", cov)
	}

	// 第一命中：A 股票索引 0。
	firstSeq := 0
	cand, _, err := practiceScan(practiceScanParams{
		Codes: src.codes, Source: src, FromYear: 2024, ToYear: 2024,
		Now: time.Now(), Buyer: practiceTestBuyerFn(),
		Choose: func(n int) bool { firstSeq++; return n == 1 },
	})
	if err != nil {
		t.Fatalf("practiceScan: %v", err)
	}
	if cand == nil || cand.Code != "sh600001" || cand.Index != 0 || firstSeq != 5 {
		t.Fatalf("第一命中抽取不正确: %+v seq=%d", cand, firstSeq)
	}

	// 恒真：最后一个命中（B 股票索引 1）。
	cand, _, err = practiceScan(practiceScanParams{
		Codes: src.codes, Source: src, FromYear: 2024, ToYear: 2024,
		Now: time.Now(), Buyer: practiceTestBuyerFn(),
		Choose: func(n int) bool { return true },
	})
	if err != nil {
		t.Fatalf("practiceScan: %v", err)
	}
	if cand == nil || cand.Code != "sh600002" || cand.Index != 1 {
		t.Fatalf("最后命中抽取不正确: %+v", cand)
	}
}

// TestPracticeScanYearFilter 年份过滤：范围外的历史前缀不求值、不计命中。
func TestPracticeScanYearFilter(t *testing.T) {
	start := time.Date(2023, 12, 25, 0, 0, 0, 0, time.UTC)
	closes := make([]float64, 10) // 2023-12-25 .. 2024-01-03：2023 年 7 根，2024 年 3 根
	for i := range closes {
		closes[i] = 10
	}
	src := fakePracticeSource{
		codes:  []string{"sh600001"},
		klines: map[string]extend.Klines{"sh600001": practiceTestKlinesFrom(start, closes)},
	}

	var seq []int
	cand, cov, err := practiceScan(practiceScanParams{
		Codes: src.codes, Source: src, FromYear: 2024, ToYear: 2024,
		Now: time.Now(), Buyer: practiceTestBuyerFn(),
		Choose: func(n int) bool { seq = append(seq, n); return true },
	})
	if err != nil {
		t.Fatalf("practiceScan: %v", err)
	}
	if cov.Hits != 3 || len(seq) != 3 {
		t.Fatalf("2024 年只应有 3 次命中: hits=%d seq=%v", cov.Hits, seq)
	}
	if cand == nil || cand.Index != 9 {
		t.Fatalf("choose 恒真应取最后命中（索引 9）: %+v", cand)
	}
}

// panicBuyer 求值即 panic 的买入策略（验证单股票失败不污染整体）。
type panicBuyer struct{}

func (panicBuyer) Name() string                   { return "panic" }
func (panicBuyer) Buy(string, extend.Klines) bool { panic("boom") }

// TestPracticeScanBuyerPanic 求值 panic 记入覆盖统计并继续，候选不回写。
func TestPracticeScanBuyerPanic(t *testing.T) {
	// 只放一只股票：其他股票读取失败会混入 Failed 计数，干扰本断言。
	src := fakePracticeSource{
		codes:  []string{"sh600001"},
		klines: map[string]extend.Klines{"sh600001": practiceTestKlines([]float64{10, 11})},
	}
	cand, cov, err := practiceScan(practiceScanParams{
		Codes: src.codes, Source: src, FromYear: 2024, ToYear: 2024,
		Now:   time.Now(),
		Buyer: func() (core.Buyer, error) { return panicBuyer{}, nil },
	})
	if err != nil {
		t.Fatalf("panic 应被单股票兜底而不是中断扫描: %v", err)
	}
	if cand != nil {
		t.Fatalf("panic 股票不应产生候选: %+v", cand)
	}
	if cov.Scanned != 1 || cov.Failed != 1 || len(cov.Failures) != 1 {
		t.Fatalf("覆盖统计不正确: %+v", cov)
	}
	if cov.Failures[0].Code != "sh600001" || !strings.Contains(cov.Failures[0].Message, "求值异常") {
		t.Fatalf("失败明细不正确: %+v", cov.Failures[0])
	}
}

// TestPracticeScanTrimsUnfinished 未收盘尾部行情剔除：15:00 收盘判定与截尾。
func TestPracticeScanTrimsUnfinished(t *testing.T) {
	shanghai := time.FixedZone("Asia/Shanghai", 8*60*60)
	day := time.Date(2024, 1, 2, 0, 0, 0, 0, shanghai)
	uncompleted := day.Unix()                   // 当日 00:00，收盘线 15:00
	completed := day.Add(15 * time.Hour).Unix() // 当日 15:00 起
	nowBefore := day.Add(14 * time.Hour)        // 14:00 未收盘
	nowAfter := day.Add(16 * time.Hour)         // 16:00 已收盘

	if practiceDayCompleted(uncompleted, nowBefore) {
		t.Fatal("15:00 前应视为未收盘")
	}
	if !practiceDayCompleted(uncompleted, nowAfter) {
		t.Fatal("15:00 后应视为已收盘")
	}
	if !practiceDayCompleted(completed, nowAfter) {
		t.Fatal("已过当日收盘线应视为已收盘")
	}

	ks := practiceTestKlines([]float64{10, 11, 12})
	if got := practiceTrimUnfinished(ks, time.Now().AddDate(2, 0, 0)); len(got) != 3 {
		t.Fatalf("历史行情不应被截尾: %d", len(got))
	}

	// 本地时间过去三根（01-01/02/03 00:00），01-03 14:00 时尾根未收盘。
	locals := practiceTestKlinesFrom(time.Date(2024, 1, 1, 0, 0, 0, 0, shanghai), []float64{10, 11, 12})
	if got := practiceTrimUnfinished(locals, day.AddDate(0, 0, 1).Add(14*time.Hour)); len(got) != 2 {
		t.Fatalf("未收盘尾根应剔除: %d", len(got))
	}
	if got := practiceTrimUnfinished(locals, day.AddDate(0, 0, 1).Add(16*time.Hour)); len(got) != 3 {
		t.Fatalf("收盘后不应截尾: %d", len(got))
	}
}

// TestPracticeScanCoverageFailuresCap 失败明细上限：恒 100 条、计数精确。
func TestPracticeScanCoverageFailuresCap(t *testing.T) {
	codes := make([]string, 120)
	for i := range codes {
		codes[i] = fmt.Sprintf("sh%06d", i)
	}
	src := fakePracticeSource{codes: codes} // 全部无数据 → 读取失败
	_, cov, err := practiceScan(practiceScanParams{
		Codes: codes, Source: src, FromYear: 2024, ToYear: 2024,
		Now: time.Now(), Buyer: practiceTestBuyerFn(),
	})
	if err != nil {
		t.Fatalf("practiceScan: %v", err)
	}
	if cov.Codes != 120 || cov.Failed != 120 || len(cov.Failures) != 100 {
		t.Fatalf("失败明细应封顶 100 条: codes=%d failed=%d 明细=%d", cov.Codes, cov.Failed, len(cov.Failures))
	}
}

// TestPracticeScanStop 停止信号：扫描以 errStopped 结束。
func TestPracticeScanStop(t *testing.T) {
	stop := make(chan struct{})
	close(stop)
	src := fakePracticeSource{
		codes:  []string{"sh600001"},
		klines: map[string]extend.Klines{"sh600001": practiceTestKlines([]float64{10, 11})},
	}
	_, _, err := practiceScan(practiceScanParams{
		Codes: src.codes, Source: src, FromYear: 2024, ToYear: 2024,
		Now: time.Now(), Buyer: practiceTestBuyerFn(), Stop: stop,
	})
	if !errors.Is(err, errStopped) {
		t.Fatalf("已停止的扫描应返回 errStopped: %v", err)
	}
}

// TestPracticeStartPracticeEndToEnd 经 Runner.StartPractice 端到端出题：
// 会话落盘、快照注入与覆盖统计齐备。
func TestPracticeStartPracticeEndToEnd(t *testing.T) {
	src := fakePracticeSource{
		codes: []string{"sh600001", "sh600002"}, // 无数据 + 有数据
		klines: map[string]extend.Klines{
			"sh600002": practiceTestKlines([]float64{10, 10.5, 11, 10.8, 11.2}),
		},
	}
	r, ps, bs := newPracticeRunnerForTest(t, src)
	r.practiceChoose = func(n int) bool { return n == 1 } // 取第一命中
	st, err := bs.Create(practiceTestSpec())
	if err != nil {
		t.Fatalf("创建策略: %v", err)
	}

	res, err := r.StartPractice(practiceStartRequest{
		StrategyID: st.ID, StrategyVersion: st.Version,
		FromYear: 2024, ToYear: 2024, RequestID: "req-e2e",
	})
	if err != nil || !res.Started {
		t.Fatalf("出题受理失败: %+v %v", res, err)
	}
	sess, m := waitForPracticePhase(t, ps, res.SessionID)

	if sess.Phase != practicePhaseActive || sess.Stage != practiceStageWaiting ||
		sess.Revision != 1 || sess.SignalIndex != 0 || sess.CurrentIndex != 0 {
		t.Fatalf("出题完成状态不正确: %+v", sess)
	}
	if sess.Cost.Slippage != 0.0 || sess.BudgetYuan != practiceBudgetYuan {
		t.Fatalf("开题快照不正确: cost=%+v budget=%v", sess.Cost, sess.BudgetYuan)
	}
	if m == nil || len(m.Days) != 5 || m.Code != "sh600002" {
		t.Fatalf("行情快照不正确: %+v", m)
	}
	if m.Coverage == nil || m.Coverage.Codes != 2 || m.Coverage.WithData != 1 ||
		m.Coverage.Scanned != 1 || m.Coverage.Failed != 1 || m.Coverage.Hits != 5 {
		t.Fatalf("扫描覆盖不正确: %+v", m.Coverage)
	}
}

// TestPracticeStartPracticeNoMatch 范围内无命中：failed + no_match。
func TestPracticeStartPracticeNoMatch(t *testing.T) {
	// 股票存在但显式空行情：不报失败明细，也无命中。
	src := fakePracticeSource{codes: []string{"sh600001"}, klines: map[string]extend.Klines{}}
	src.klines["sh600001"] = extend.Klines{}
	r, ps, bs := newPracticeRunnerForTest(t, src)
	st, err := bs.Create(practiceTestSpec())
	if err != nil {
		t.Fatalf("创建策略: %v", err)
	}
	res, err := r.StartPractice(practiceStartRequest{
		StrategyID: st.ID, StrategyVersion: st.Version,
		FromYear: 2024, ToYear: 2024, RequestID: "req-empty",
	})
	if err != nil {
		t.Fatalf("出题受理失败: %v", err)
	}
	sess, _ := waitForPracticePhase(t, ps, res.SessionID)
	if sess.Phase != practicePhaseFailed || sess.FailReason != practiceFailNoMatch {
		t.Fatalf("应失败为 no_match: %+v", sess)
	}
}

// TestPracticeStartPracticeIdempotent 同 requestID 幂等：完成后重放不开新任务，
// 异参数冲突。
func TestPracticeStartPracticeIdempotent(t *testing.T) {
	src := fakePracticeSource{
		codes:  []string{"sh600001"},
		klines: map[string]extend.Klines{"sh600001": practiceTestKlines([]float64{10, 11})},
	}
	r, ps, bs := newPracticeRunnerForTest(t, src)
	st, err := bs.Create(practiceTestSpec())
	if err != nil {
		t.Fatalf("创建策略: %v", err)
	}
	req := practiceStartRequest{
		StrategyID: st.ID, StrategyVersion: st.Version,
		FromYear: 2024, ToYear: 2024, RequestID: "req-idem",
	}
	res, err := r.StartPractice(req)
	if err != nil {
		t.Fatalf("首次出题失败: %v", err)
	}
	waitForPracticePhase(t, ps, res.SessionID)

	// 任务结束后同请求重放：幂等返回已有会话。launchTask 先置 running=false
	// 再释放任务锁，waitFor 返回时可能仍在释放，Busy 短暂重试兜底。
	var again *practiceStartResult
	deadline := time.Now().Add(5 * time.Second)
	for {
		again, err = r.StartPractice(req)
		if !errors.Is(err, errPracticeBusy) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("幂等重放持续 Busy: %v", err)
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("幂等重放失败: %v", err)
	}
	if again.Started || again.SessionID != res.SessionID {
		t.Fatalf("幂等重放应返回原会话: %+v", again)
	}

	// 同 requestID 异参数冲突（年份范围须先过校验，用合法的 2023-2023）。
	req.FromYear, req.ToYear = 2023, 2023
	if _, err := r.StartPractice(req); !errors.Is(err, errPracticeRequestConflict) {
		t.Fatalf("异参数应冲突: %v", err)
	}
}

// TestPracticeStartPracticeBusy 任务互斥：出题占用期间再次出题拒绝。
func TestPracticeStartPracticeBusy(t *testing.T) {
	r, _, bs := newPracticeRunnerForTest(t, fakePracticeSource{codes: []string{"sh600001"}})
	st, err := bs.Create(practiceTestSpec())
	if err != nil {
		t.Fatalf("创建策略: %v", err)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, err := r.StartPractice(practiceStartRequest{
		StrategyID: st.ID, StrategyVersion: st.Version,
		FromYear: 2024, ToYear: 2024, RequestID: "req-busy",
	}); !errors.Is(err, errPracticeBusy) {
		t.Fatalf("任务运行中出题应拒绝: %v", err)
	}
}

// TestPracticeStartPracticeStrategyVersion 策略来源校验：不存在 404、版本
// 不符 409、空买入出题拒绝（可保存但不可出题）。
func TestPracticeStartPracticeStrategyVersion(t *testing.T) {
	r, _, bs := newPracticeRunnerForTest(t, fakePracticeSource{codes: []string{"sh600001"}})
	st, err := bs.Create(practiceTestSpec())
	if err != nil {
		t.Fatalf("创建策略: %v", err)
	}
	req := practiceStartRequest{StrategyID: st.ID, FromYear: 2024, ToYear: 2024, RequestID: "req-v"}

	// 不存在的策略。
	if _, err := r.StartPractice(practiceStartRequest{StrategyID: "stg-nope", StrategyVersion: 1,
		FromYear: 2024, ToYear: 2024, RequestID: "req-missing"}); !errors.Is(err, errPracticeStrategyNotFound) {
		t.Fatalf("策略不存在应拒绝: %v", err)
	}
	// 版本不符。
	req.StrategyVersion = st.Version + 1
	if _, err := r.StartPractice(req); !errors.Is(err, errPracticeStrategyVersion) {
		t.Fatalf("版本不符应拒绝: %v", err)
	}
	// 空买入：可保存但出题拒绝。
	empty, err := bs.Create(builder.BuilderSpec{Version: 1, Name: "空买入"})
	if err != nil {
		t.Fatalf("创建空买入策略: %v", err)
	}
	if _, err := r.StartPractice(practiceStartRequest{StrategyID: empty.ID, StrategyVersion: empty.Version,
		FromYear: 2024, ToYear: 2024, RequestID: "req-empty-buy"}); !errors.Is(err, errPracticeBuyEmptyProjected) {
		t.Fatalf("空买入应拒绝: %v", err)
	}
}

// 固定多股票、多年 MACD 前缀，用于对比扫描吞吐；不访问真实数据。
func BenchmarkPracticeScanMACD(b *testing.B) {
	closes := make([]float64, 1600)
	for i := range closes {
		closes[i] = 10 + math.Sin(float64(i)*0.15) + float64(i%13)*0.01
	}
	ks := practiceTestKlinesFrom(time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC), closes)
	src := fakePracticeSource{klines: map[string]extend.Klines{}}
	for i := 0; i < 16; i++ {
		code := fmt.Sprintf("sh%06d", 600000+i)
		src.codes = append(src.codes, code)
		src.klines[code] = ks
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, cov, err := practiceScan(practiceScanParams{Codes: src.codes, Source: src,
			FromYear: 2020, ToYear: 2026, Now: time.Now(), Workers: runtime.GOMAXPROCS(0),
			Buyer: func() (core.Buyer, error) { return sb.MACD反转{}, nil }, Choose: practiceReservoirChoose})
		if err != nil || cov.Hits == 0 {
			b.Fatalf("scan: %v, %+v", err, cov)
		}
	}
}

func TestPracticeParallelScanPreservesSamplingAndProgress(t *testing.T) {
	src := fakePracticeSource{codes: []string{"a", "missing", "empty", "b"}, klines: map[string]extend.Klines{
		"a": practiceTestKlines([]float64{10, 11, 12}), "empty": {}, "b": practiceTestKlines([]float64{10, 11}),
	}}
	for _, workers := range []int{1, 4} {
		var draws, progress []int
		cand, cov, err := practiceScan(practiceScanParams{Codes: src.codes, Source: src, Workers: workers,
			FromYear: 2024, ToYear: 2024, Now: time.Now(), Buyer: practiceTestBuyerFn(),
			Choose: func(n int) bool { draws = append(draws, n); return n == 4 },
			OnProgress: func(done, total int) {
				if total != 4 {
					t.Fatalf("total=%d", total)
				}
				progress = append(progress, done)
			},
		})
		if err != nil || cand == nil || cand.Code != "b" || cand.Index != 0 {
			t.Fatalf("workers=%d cand=%+v err=%v", workers, cand, err)
		}
		if !reflect.DeepEqual(draws, []int{1, 2, 3, 4, 5}) || !reflect.DeepEqual(progress, []int{1, 2, 3, 4}) {
			t.Fatalf("draws=%v progress=%v", draws, progress)
		}
		if cov.Scanned != 2 || cov.WithData != 2 || cov.Hits != 5 || cov.Failed != 1 {
			t.Fatalf("coverage=%+v", cov)
		}
	}
}

type practiceBarrierSource struct {
	entered chan struct{}
	release <-chan struct{}
	ks      extend.Klines
}

func (s practiceBarrierSource) Codes() ([]string, error) { return []string{"a", "b", "c", "d"}, nil }
func (s practiceBarrierSource) DayKlinesAll(string) (extend.Klines, error) {
	s.entered <- struct{}{}
	<-s.release
	return s.ks, nil
}

func TestPracticeParallelScanHasBoundedWorkersAndCancels(t *testing.T) {
	release := make(chan struct{})
	stop := make(chan struct{})
	src := practiceBarrierSource{entered: make(chan struct{}, 4), release: release, ks: practiceTestKlines([]float64{10})}
	done := make(chan error, 1)
	go func() {
		_, _, err := practiceScan(practiceScanParams{Codes: []string{"a", "b", "c", "d", "e"}, Source: src, Workers: 99,
			FromYear: 2024, ToYear: 2024, Now: time.Now(), Buyer: practiceTestBuyerFn(), Stop: stop})
		done <- err
	}()
	for i := 0; i < 4; i++ {
		select {
		case <-src.entered:
		case <-time.After(time.Second):
			close(stop)
			close(release)
			t.Fatal("扫描没有并行执行")
		}
	}
	select {
	case <-src.entered:
		t.Error("并行数量超过四只")
	default:
	}
	close(stop)
	close(release)
	select {
	case err := <-done:
		if !errors.Is(err, errStopped) {
			t.Fatalf("err=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("取消未生效")
	}
	select {
	case <-src.entered:
		t.Fatal("取消后仍加载下一批股票")
	default:
	}
}
