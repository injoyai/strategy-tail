package lab

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/lib/extend"
)

func trainingFixture(t testing.TB, total int) (*TrainingStore, *practiceBank) {
	t.Helper()
	s := NewTrainingStore(t.TempDir())
	req := practiceStartRequest{StrategyID: "frozen", StrategyVersion: 1, FromYear: 2024, ToYear: 2024, RequestID: "bank-request"}
	b, e := s.createBank(req, practiceTestSpec(), practiceTestCost)
	if e != nil {
		t.Fatal(e)
	}
	ks := practiceTestKlines([]float64{10, 11, 12, 13, 14, 15})
	m, e := buildPracticeMarket("sh600000", ks, practiceMarketSource, practiceNowUTC())
	if e != nil {
		t.Fatal(e)
	}
	for i := 0; i < total; i++ {
		if e = s.appendStock(b, m, []int{0}); e != nil {
			t.Fatal(e)
		}
	}
	b.Phase = "ready"
	b.ReadyAt = practiceNowUTC()
	b.Coverage = &practiceScanCoverage{Codes: total, WithData: total, Scanned: total, Hits: total}
	if e = s.writeBank(b); e != nil {
		t.Fatal(e)
	}
	return s, b
}
func trainingDo(t *testing.T, s *TrainingStore, tr *practiceTraining, action string) *practiceTraining {
	t.Helper()
	cmd := trainingCommand{Action: action, CommandID: fmt.Sprintf("command-%d", tr.Revision), QuestionID: tr.Attempts[len(tr.Attempts)-1].Session.ID, ExpectedRevision: tr.Revision}
	next, e := s.Apply(tr.ID, cmd)
	if e != nil {
		t.Fatalf("%s: %v", action, e)
	}
	return next
}
func TestTrainingLifecyclePersistenceAndIsolation(t *testing.T) {
	s, b := trainingFixture(t, 3)
	tr, e := s.Start(b.ID, "start")
	if e != nil {
		t.Fatal(e)
	}
	replay, e := s.Start(b.ID, "start")
	if e != nil || replay.ID != tr.ID {
		t.Fatalf("start replay %v", e)
	}
	if _, e = s.Start("other", "start"); !errors.Is(e, errPracticeRequestConflict) {
		t.Fatal(e)
	}
	if _, e = s.Report(tr.ID); !errors.Is(e, errPracticeNotFinished) {
		t.Fatal(e)
	}
	if _, e = s.Review(tr.ID, tr.Attempts[0].Session.ID); !errors.Is(e, errPracticeNotFinished) {
		t.Fatal(e)
	}
	v, e := s.View(tr.ID)
	if e != nil {
		t.Fatal(e)
	}
	raw, _ := json.Marshal(v)
	for _, secret := range []string{"sh600000", "2024-01-02", "signalIndex", "blocks", "unix", "dataEndDate"} {
		if bytes.Contains(raw, []byte(secret)) {
			t.Fatalf("leaked %s", secret)
		}
	}
	tr = trainingDo(t, s, tr, "buy")
	tr = trainingDo(t, s, tr, "next")
	tr = trainingDo(t, s, tr, "sell")
	tr = trainingDo(t, s, tr, "finish")
	if tr.Phase != "active" || tr.Attempts[0].Result.Status != "sold" {
		t.Fatal("single finish ended training")
	}
	first := tr.Attempts[0].Session.ID
	// Restart has no action or draw side effects.
	s = NewTrainingStore(s.root)
	v, e = s.View(tr.ID)
	if e != nil || v["revision"] != tr.Revision {
		t.Fatalf("restart %v", e)
	}
	cmd := trainingCommand{Action: "next_question", CommandID: "next-retry", QuestionID: first, ExpectedRevision: tr.Revision}
	tr, e = s.Apply(tr.ID, cmd)
	if e != nil {
		t.Fatal(e)
	}
	replay, e = s.Apply(tr.ID, cmd)
	if e != nil || len(replay.Attempts) != 2 {
		t.Fatal("duplicate draw", e)
	}
	// Old page cannot mutate the new question or end the newer training.
	cmd.CommandID = "stale"
	cmd.Action = "buy"
	if _, e = s.Apply(tr.ID, cmd); !errors.Is(e, errPracticeVersionConflict) {
		t.Fatal(e)
	}
	tr = trainingDo(t, s, tr, "skip")
	tr = trainingDo(t, s, tr, "next_question")
	tr = trainingDo(t, s, tr, "buy")
	tr = trainingDo(t, s, tr, "next")
	tr = trainingDo(t, s, tr, "end_training")
	r, e := s.Report(tr.ID)
	if e != nil {
		t.Fatal(e)
	}
	if r.Practiced != 3 || r.Closed != 1 || r.Untraded != 1 || r.Open != 1 || *r.WinRate != 100 {
		t.Fatalf("report %+v", r)
	}
	if r.Fees <= r.Questions[0].Fees || r.OpenPnL <= 0 || r.PositionValue <= 0 {
		t.Fatalf("open fees/valuation %+v", r)
	}
	// No duplicated bank index despite random draws.
	seen := map[int]bool{}
	for _, a := range tr.Attempts {
		if seen[a.Block] {
			t.Fatal("duplicate question")
		}
		seen[a.Block] = true
	}
	before, _ := json.Marshal(r)
	ps := NewPracticeStore(t.TempDir())
	retake, e := s.Retake(ps, tr.ID, first, "retry")
	if e != nil {
		t.Fatal(e)
	}
	again, e := s.Retake(ps, tr.ID, first, "retry")
	if e != nil || again.ID != retake.ID || retake.SourceSessionID != first {
		t.Fatal("retake idempotency", e)
	}
	// Retake trades are saved independently and cannot revise the report.
	if _, _, e = ps.Apply(retake.ID, practiceCommand{Action: "skip", CommandID: "retake-finish", ExpectedRevision: 1}); e != nil {
		t.Fatal(e)
	}
	after, e := s.Report(tr.ID)
	if e != nil {
		t.Fatal(e)
	}
	afterRaw, _ := json.Marshal(after)
	if !bytes.Equal(before, afterRaw) {
		t.Fatal("retake changed report")
	}
	second, e := s.Start(b.ID, "new-training")
	if e != nil || second.ID == tr.ID {
		t.Fatal(e)
	}
	second = trainingDo(t, s, second, "end_training")
	r2, e := s.Report(second.ID)
	if e != nil || r2.Practiced != 1 || r2.WinRate != nil || r2.AverageReturn != nil {
		t.Fatalf("isolated report %+v %v", r2, e)
	}
}
func TestTrainingSingleQuestionAutoEndAndConcurrentNext(t *testing.T) {
	s, b := trainingFixture(t, 1)
	tr, _ := s.Start(b.ID, "start")
	tr = trainingDo(t, s, tr, "skip")
	if tr.Phase != "finished" {
		t.Fatal("last question must end training")
	}
	r, e := s.Report(tr.ID)
	if e != nil || r.Untraded != 1 || r.WinRate != nil {
		t.Fatal(e)
	}
	s, b = trainingFixture(t, 5)
	tr, _ = s.Start(b.ID, "concurrent")
	tr = trainingDo(t, s, tr, "skip")
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, e := s.Apply(tr.ID, trainingCommand{Action: "next_question", CommandID: fmt.Sprint(i), QuestionID: tr.Attempts[0].Session.ID, ExpectedRevision: tr.Revision})
			errs <- e
		}(i)
	}
	wg.Wait()
	close(errs)
	successes, conflicts := 0, 0
	for e := range errs {
		if e == nil {
			successes++
		} else if errors.Is(e, errPracticeVersionConflict) {
			conflicts++
		} else {
			t.Fatal(e)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatal(successes, conflicts)
	}
}
func TestTrainingFiveQuestionStatsAndBreakeven(t *testing.T) {
	tr := &practiceTraining{ID: "t", BankID: "b", Phase: "finished"}
	b := &practiceBank{ID: "b", Total: 100, Spec: practiceTestSpec()}
	for _, q := range []trainingQuestionResult{
		{Status: "sold", BuyCost: 100, SellIncome: 110, RealizedPnL: 10, Fees: 1},
		{Status: "sold", BuyCost: 200, SellIncome: 240, RealizedPnL: 40, Fees: 2},
		{Status: "sold", BuyCost: 100, SellIncome: 95, RealizedPnL: -5, Fees: 3},
		{Status: "untraded"},
		{Status: "open", BuyCost: 100, PositionValue: 120, OpenPnL: 20, Fees: 4},
	} {
		copy := q
		tr.Attempts = append(tr.Attempts, trainingAttempt{Session: practiceSession{Phase: practicePhaseFinished}, Result: &copy})
	}
	r, e := trainingReportOf(tr, b)
	if e != nil {
		t.Fatal(e)
	}
	if r.Practiced != 5 || r.Closed != 3 || r.Untraded != 1 || r.Open != 1 || r.Wins != 2 || r.Losses != 1 || math.Abs(*r.WinRate-200.0/3) > 1e-9 || math.Abs(*r.AverageReturn-25.0/3) > 1e-9 || r.RealizedPnL != 45 || r.Fees != 10 {
		t.Fatalf("five-question %+v", r)
	}
	zero := trainingQuestionResult{Status: "sold", BuyCost: 50, SellIncome: 50}
	tr.Attempts = append(tr.Attempts, trainingAttempt{Session: practiceSession{Phase: practicePhaseFinished}, Result: &zero})
	r, e = trainingReportOf(tr, b)
	if e != nil || r.Breakeven != 1 || *r.WinRate != 50 {
		t.Fatalf("breakeven %+v %v", r, e)
	}
}
func TestTrainingHTTPAndBankPreparation(t *testing.T) {
	s, h := newPracticeServerForTest(t)
	st, e := s.builderStore.Create(practiceTestSpec())
	if e != nil {
		t.Fatal(e)
	}
	req := practiceStartRequest{StrategyID: st.ID, StrategyVersion: st.Version, FromYear: 2024, ToYear: 2024, RequestID: "bank"}
	code, raw := practiceTryReq(t, h, "POST", "/api/practice-banks", req)
	if code != 202 {
		t.Fatalf("%d %s", code, raw)
	}
	var accepted struct {
		ID string `json:"id"`
	}
	json.Unmarshal(raw, &accepted)
	var b *practiceBank
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		s.trainingStore.mu.Lock()
		b, e = s.trainingStore.bank(accepted.ID)
		s.trainingStore.mu.Unlock()
		if e != nil {
			t.Fatal(e)
		}
		if b.Phase != "preparing" {
			break
		}
		time.Sleep(time.Millisecond * 5)
	}
	if b.Phase != "ready" || b.Total != 8 || b.Coverage.Hits != 8 {
		t.Fatalf("bank %+v", b)
	}
	// Freeze source and strategy: no subsequent training/draw reads live market.
	s.runner.practiceSource = fakePracticeSource{}
	code, raw = practiceTryReq(t, h, "POST", "/api/practice-banks", req)
	if code != 202 {
		t.Fatalf("replay %d %s", code, raw)
	}
	code, raw = practiceTryReq(t, h, "POST", "/api/practice-trainings", map[string]any{"bankId": b.ID, "requestId": "train"})
	if code != 200 {
		t.Fatalf("start %d %s", code, raw)
	}
	var v struct {
		ID       string              `json:"id"`
		Revision int64               `json:"revision"`
		Session  practiceSessionView `json:"session"`
	}
	json.Unmarshal(raw, &v)
	for _, secret := range []string{"sh600000", "unix", "signals", "2024-01-02"} {
		if strings.Contains(string(raw), secret) {
			t.Fatal("leak", secret)
		}
	}
	code, _ = practiceTryReq(t, h, "GET", "/api/practice-trainings/"+v.ID+"/report", nil)
	if code != 409 {
		t.Fatal(code)
	}
	code, raw = practiceTryReq(t, h, "POST", "/api/practice-trainings/"+v.ID+"/actions", trainingCommand{Action: "end_training", CommandID: "end", QuestionID: v.Session.ID, ExpectedRevision: v.Revision})
	if code != 200 {
		t.Fatalf("end %d %s", code, raw)
	}
	code, raw = practiceTryReq(t, h, "GET", "/api/practice-trainings/"+v.ID+"/report", nil)
	if code != 200 || !bytes.Contains(raw, []byte(`"practiced":1`)) {
		t.Fatal(code, string(raw))
	}
	// Cross-origin mutation and unknown fields rejected by the existing boundary.
	body, _ := json.Marshal(map[string]any{"bankId": b.ID, "requestId": "cross"})
	request := httptest.NewRequest("POST", "/api/practice-trainings", bytes.NewReader(body))
	request.Header.Set("Origin", "https://evil.invalid")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, request)
	if rec.Code != http.StatusForbidden {
		t.Fatal(rec.Code)
	}
	code, _ = practiceTryReq(t, h, "POST", "/api/practice-trainings", map[string]any{"bankId": b.ID, "requestId": "extra", "future": "leak"})
	if code != 400 {
		t.Fatal(code)
	}
	// Legacy records are not rewritten or grouped into training.
	legacy, e := s.practiceStore.List()
	if e != nil || len(legacy) != 0 {
		t.Fatal(legacy, e)
	}
}
func TestTrainingBankInterruptedCancelledAndCoverage(t *testing.T) {
	s, b := trainingFixture(t, 1)
	b.Phase = "preparing"
	if e := s.writeBank(b); e != nil {
		t.Fatal(e)
	}
	restarted := NewTrainingStore(s.root)
	if e := restarted.recoverBanks(); e != nil {
		t.Fatal(e)
	}
	stored, e := restarted.bank(b.ID)
	if e != nil || stored.FailReason != practiceFailInterrupt {
		t.Fatal(e)
	}
	if _, e = restarted.Start(b.ID, "cannot"); !errors.Is(e, errPracticeNotFinished) {
		t.Fatal(e)
	}
	r := NewRunner()
	r.trainingStore = NewTrainingStore(t.TempDir())
	r.practiceSource = fakePracticeSource{codes: []string{"missing", "empty", "valid"}, klines: map[string]extend.Klines{"empty": {}, "valid": practiceTestKlines([]float64{10, 11})}}
	b, e = r.trainingStore.createBank(practiceStartRequest{RequestID: "coverage", FromYear: 2024, ToYear: 2024}, practiceTestSpec(), core.Cost{})
	if e != nil {
		t.Fatal(e)
	}
	if e = r.preparePracticeBank(b, make(chan struct{})); e != nil {
		t.Fatal(e)
	}
	if b.Total != 2 || b.Coverage.Failed != 1 || b.Coverage.WithData != 1 || b.Coverage.Hits != 2 {
		t.Fatalf("coverage %+v", b.Coverage)
	}
	b, e = r.trainingStore.createBank(practiceStartRequest{RequestID: "cancel", FromYear: 2024, ToYear: 2024}, practiceTestSpec(), core.Cost{})
	if e != nil {
		t.Fatal(e)
	}
	stop := make(chan struct{})
	close(stop)
	if e = r.preparePracticeBank(b, stop); !errors.Is(e, errStopped) || b.Phase != "cancelled" {
		t.Fatal(e, b.Phase)
	}
	if _, e = r.trainingStore.bank("../../etc"); !errors.Is(e, errPracticeNotFound) {
		t.Fatal(e)
	}
}
func TestTrainingReviewFutureCannotChangeResult(t *testing.T) {
	s, b := trainingFixture(t, 1)
	tr, _ := s.Start(b.ID, "open")
	tr = trainingDo(t, s, tr, "buy")
	tr = trainingDo(t, s, tr, "end_training")
	r, _ := s.Report(tr.ID)
	row := r.Questions[0]
	if row.OpenPnL >= 0 {
		t.Fatal("buy fees should produce loss at current close")
	}
	rv, e := s.Review(tr.ID, row.ID)
	if e != nil {
		t.Fatal(e)
	}
	if rv.ExtraDays == 0 {
		t.Fatal("fixture needs future review bars")
	}
	r2, _ := s.Report(tr.ID)
	if !reflect.DeepEqual(r, r2) {
		t.Fatal("review changed frozen report")
	}
}

// 1,000 stocks × 1,000 signals: records the actual persisted million-question
// fixture, heap growth between stocks, disk footprint and training reuse time.
func BenchmarkTrainingBankMillionQuestions(b *testing.B) {
	for n := 0; n < b.N; n++ {
		s := NewTrainingStore(b.TempDir())
		bank, e := s.createBank(practiceStartRequest{RequestID: "capacity"}, practiceTestSpec(), practiceTestCost)
		if e != nil {
			b.Fatal(e)
		}
		prices := make([]float64, 1000)
		signals := make([]int, 1000)
		for i := range prices {
			prices[i] = 10
			signals[i] = i
		}
		m, e := buildPracticeMarket("sh600000", practiceTestKlines(prices), practiceMarketSource, practiceNowUTC())
		if e != nil {
			b.Fatal(e)
		}
		runtime.GC()
		var baseline, peak runtime.MemStats
		runtime.ReadMemStats(&baseline)
		maxHeap := baseline.HeapAlloc
		for stock := 0; stock < 1000; stock++ {
			if e = s.appendStock(bank, m, signals); e != nil {
				b.Fatal(e)
			}
			if stock%100 == 0 {
				runtime.GC()
				runtime.ReadMemStats(&peak)
				if peak.HeapAlloc > maxHeap {
					maxHeap = peak.HeapAlloc
				}
			}
		}
		bank.Phase = "ready"
		if e = s.writeBank(bank); e != nil {
			b.Fatal(e)
		}
		if bank.Total != 1000000 {
			b.Fatal(bank.Total)
		}
		start := time.Now()
		tr, e := s.Start(bank.ID, "reuse")
		if e != nil {
			b.Fatal(e)
		}
		reuse := time.Since(start)
		if len(tr.Attempts) != 1 || len(tr.Shuffle) > 1 {
			b.Fatal("eager question list")
		}
		var disk int64
		filepath.Walk(s.root, func(_ string, info os.FileInfo, err error) error {
			if err == nil && !info.IsDir() {
				disk += info.Size()
			}
			return err
		})
		b.ReportMetric(float64(disk)/1024/1024, "disk-MiB")
		b.ReportMetric(float64(maxHeap-baseline.HeapAlloc)/1024/1024, "retained-heap-MiB")
		b.ReportMetric(float64(reuse.Microseconds())/1000, "reuse-ms")
	}
}

func TestTrainingBankMultipleSignalsAndOrphanRecovery(t *testing.T) {
	s, b := trainingFixture(t, 0)
	m := practiceTestMarket(t, []float64{10, 11, 12, 13, 14, 15})
	for i := 0; i < 3; i++ {
		if e := s.appendStock(b, m, []int{0, 1, 2, 3, 4, 5}); e != nil {
			t.Fatal(e)
		}
	}
	b.Phase = "ready"
	if e := s.writeBank(b); e != nil {
		t.Fatal(e)
	}
	// Simulate process exit after mkdir, before initial manifest commit.
	orphan, _ := newPrefixedID("training", time.Now(), nil)
	d, _ := s.dir("trainings", orphan)
	os.MkdirAll(d, 0755)
	orphanBank, _ := newPrefixedID("bank", time.Now(), nil)
	d, _ = s.dir("banks", orphanBank)
	os.MkdirAll(d, 0755)
	tr, e := s.Start(b.ID, "all")
	if e != nil {
		t.Fatal(e)
	}
	seen := map[string]bool{}
	for i := 0; i < 18; i++ {
		a := tr.Attempts[len(tr.Attempts)-1]
		key := fmt.Sprintf("%d:%d", a.Block, a.Signal)
		if seen[key] {
			t.Fatal("duplicate", key)
		}
		seen[key] = true
		tr = trainingDo(t, s, tr, "skip")
		if i < 17 {
			tr = trainingDo(t, s, tr, "next_question")
		}
	}
	if tr.Phase != "finished" || len(seen) != 18 {
		t.Fatal("full bank not exhausted")
	}
	banks, e := s.Banks()
	if e != nil || len(banks) != 1 {
		t.Fatal(e, len(banks))
	}
}
