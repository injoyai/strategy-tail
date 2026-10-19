package lab

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/injoyai/strategy-tail/lib/extend"
)

func TestPracticeAuditShanghaiCloseOnUTCHost(t *testing.T) {
	old := time.Local
	time.Local = time.UTC
	t.Cleanup(func() { time.Local = old })
	shanghai := time.FixedZone("Asia/Shanghai", 8*60*60)
	day := time.Date(2026, 10, 19, 0, 0, 0, 0, shanghai)
	for _, tc := range []struct {
		hour, minute int
		completed    bool
	}{{14, 59, false}, {15, 0, true}, {16, 0, true}} {
		now := time.Date(2026, 10, 19, tc.hour, tc.minute, 0, 0, shanghai)
		if got := practiceDayCompleted(day.Unix(), now); got != tc.completed {
			t.Errorf("上海 %02d:%02d completed=%v，want %v", tc.hour, tc.minute, got, tc.completed)
		}
	}
}

func TestPracticeAuditCancelOnLastHit(t *testing.T) {
	ks := practiceTestKlines([]float64{10})
	r, ps, _ := newPracticeRunnerForTest(t, fakePracticeSource{codes: []string{"sh600000"}, klines: map[string]extend.Klines{"sh600000": ks}})
	spec := practiceTestSpec()
	sess, err := ps.CreatePreparing("test", 1, spec, practiceTestCost, 2024, 2024, "cancel-last")
	if err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	err = r.runPracticePrepare(sess, spec, func(int) bool { close(stop); return true }, stop)
	if !errors.Is(err, errStopped) {
		t.Errorf("末次命中时取消应停止出题，got %v", err)
	}
	saved, market, err := ps.Get(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Phase != practicePhaseCancelled || market != nil {
		t.Fatalf("已取消的题目不可发布：phase=%s market=%v", saved.Phase, market != nil)
	}
}

func TestPracticeAuditSessionIdentityMismatch(t *testing.T) {
	store := NewPracticeStore(t.TempDir())
	sess, err := store.CreatePreparing("test", 1, practiceTestSpec(), practiceTestCost, 2024, 2024, "identity")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Publish(sess.ID, practiceTestMarket(t, []float64{10}), 0); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(store.root, sess.ID, practiceSessionFile)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	otherID := "practice_20260101T000000000Z_ffffffff"
	text := strings.Replace(string(raw), sess.ID, otherID, 1)
	if err := os.WriteFile(path, []byte(text), 0644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Get(sess.ID); !errors.Is(err, errPracticeCorrupt) {
		t.Fatalf("文件内 ID 与请求不符应拒绝，got %v", err)
	}
}

func TestPracticeAuditReviewFeesAndShortWindow(t *testing.T) {
	m := practiceTestMarket(t, []float64{10, 11, 12})
	s := practiceTestSession(m, 0)
	for _, cmd := range []practiceCommand{
		practiceTestCmd("buy", "buy", 1), practiceTestCmd("next", "next", 2),
		practiceTestCmd("sell", "sell", 3), practiceTestCmd("finish", "finish", 4),
	} {
		if _, err := s.applyCommand(m, cmd); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := json.Marshal(practiceReviewViewOf(s, m))
	if err != nil {
		t.Fatal(err)
	}
	var review struct {
		ExtraDays *int `json:"extraDays"`
		Trades    []struct {
			Fee *float64 `json:"fee"`
		} `json:"trades"`
	}
	if err := json.Unmarshal(raw, &review); err != nil {
		t.Fatal(err)
	}
	if review.ExtraDays == nil || *review.ExtraDays != 1 {
		t.Errorf("应明确披露结束后只有 1 日复盘行情，got %v", review.ExtraDays)
	}
	for i, want := range []float64{s.BuyCostYuan - 10*float64(s.Quantity), 11*float64(s.Quantity) - s.SellIncomeYuan} {
		if review.Trades[i].Fee == nil || !practiceAlmost(*review.Trades[i].Fee, want) {
			t.Errorf("第 %d 笔手续费缺失或错误，want %v", i, want)
		}
	}
}

func TestPracticeAuditInitialHistoryWindow(t *testing.T) {
	closes := make([]float64, 200)
	for i := range closes {
		closes[i] = 10
	}
	m := practiceTestMarket(t, closes)
	sess := practiceTestSession(m, 150)
	v := practiceSessionViewOf(sess, m)
	if len(v.Days) != 121 || v.Days[0].Day != -120 || v.Days[len(v.Days)-1].Day != 0 {
		t.Fatalf("初始应显示前 120 日及信号日，got %d 根、起点 %d", len(v.Days), v.Days[0].Day)
	}
	if v.Days[0].MA20 == nil {
		t.Fatal("裁剪显示窗口不能丢失预热指标")
	}
}

func TestPracticeAuditReviewAfterLongObservation(t *testing.T) {
	closes := make([]float64, 70)
	for i := range closes {
		closes[i] = 10 + float64(i)
	}
	m := practiceTestMarket(t, closes)
	s := practiceTestSession(m, 0)
	apply := func(action, id string) {
		t.Helper()
		if _, err := s.applyCommand(m, practiceTestCmd(action, id, s.Revision)); err != nil {
			t.Fatal(err)
		}
	}
	apply("buy", "buy")
	apply("next", "next")
	apply("sell", "sell")
	realized := s.evaluate(m)
	for s.CurrentIndex < 30 {
		apply("next", practiceCmdID(s.CurrentIndex))
	}
	if s.Phase != practicePhaseActive || s.evaluate(m) != realized {
		t.Fatal("卖后观察超过 20 日仍应保持进行中，且不能改变成交评价")
	}
	apply("finish", "finish")
	rv := practiceReviewViewOf(s, m)
	if rv.ExtraDays != 20 || rv.ReviewEndDay != 50 || len(rv.Days) != 51 {
		t.Fatalf("复盘应保留 30 日观察过程，再额外展示 20 日：extra=%d end=%d len=%d", rv.ExtraDays, rv.ReviewEndDay, len(rv.Days))
	}
}
