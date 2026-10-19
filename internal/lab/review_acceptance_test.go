package lab

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReplayMissingKlineDoesNotPublish(t *testing.T) {
	t.Chdir(t.TempDir())
	rep, replays, _ := replayFixture(t)
	if err := saveReport(rep, replays, nil); err == nil {
		t.Fatal("缺少已成交股票的行情快照时不能发布完整报告")
	}
	if _, err := os.Stat(filepath.Join("output", "trades", rep.RunID(), "report.json")); !os.IsNotExist(err) {
		t.Fatalf("失败后不应发布报告: %v", err)
	}
}

type practicePanicSource struct{ fakePracticeSource }

func (practicePanicSource) Codes() ([]string, error) { panic("private-stock-2025-01-02") }

func TestPracticePreparePanicDoesNotExposeIdentity(t *testing.T) {
	r, ps, bs := newPracticeRunnerForTest(t, practicePanicSource{})
	st, err := bs.Create(practiceTestSpec())
	if err != nil {
		t.Fatal(err)
	}
	sess, err := ps.CreatePreparing(st.ID, st.Version, st.Spec, practiceTestCost, 2024, 2024, "panic-privacy")
	if err != nil {
		t.Fatal(err)
	}
	err = r.runPracticePrepare(sess, st.Spec, nil, make(chan struct{}))
	if err == nil || strings.Contains(err.Error(), "private-stock") {
		t.Fatalf("异常必须转换为匿名错误: %v", err)
	}
	saved, _, err := ps.Get(sess.ID)
	if err != nil || saved.Phase != practicePhaseFailed {
		t.Fatalf("异常后必须保存失败终态: %+v %v", saved, err)
	}
}

func TestReplayRejectsMismatchedPayload(t *testing.T) {
	for _, field := range []string{"variant", "date", "length"} {
		t.Run(field, func(t *testing.T) {
			t.Chdir(t.TempDir())
			rep, replays, loaded := replayFixture(t)
			if err := saveReport(rep, replays, loaded); err != nil {
				t.Fatal(err)
			}
			rv := replays[0]
			switch field {
			case "variant":
				rv.VariantIndex++
			case "date":
				rv.Dates[0] = "2025-01-03"
			case "length":
				rv.Dates = nil
			}
			b, err := json.Marshal(rv)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join("output", "trades", rep.RunID(), "replay", "variant-0.json"), b, 0644); err != nil {
				t.Fatal(err)
			}
			if _, err := readReplay(rep.RunID(), 0); err == nil {
				t.Fatal("错配的回放数据必须被拒绝")
			}
		})
	}
}

func TestPracticeStartReplayWhileTaskBusy(t *testing.T) {
	r, ps, bs := newPracticeRunnerForTest(t, fakePracticeSource{})
	st, err := bs.Create(practiceTestSpec())
	if err != nil {
		t.Fatal(err)
	}
	req := practiceStartRequest{StrategyID: st.ID, StrategyVersion: st.Version, FromYear: 2024, ToYear: 2024, RequestID: "lost-response"}
	sess, err := ps.CreatePreparing(st.ID, st.Version, st.Spec, practiceTestCost, 2024, 2024, req.RequestID)
	if err != nil {
		t.Fatal(err)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	got, err := r.StartPractice(req)
	if err != nil || got.SessionID != sess.ID || got.Started {
		t.Fatalf("正在出题的同一请求应返回原会话: %+v %v", got, err)
	}
}
