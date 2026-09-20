package lab

import (
	"bytes"
	"math"
	"regexp"
	"testing"
	"time"
)

// factor_trial_test.go 试验账本领域层测试（计划 Task 6 Step 1/4）：
// 受限 ID 格式、benjaminiHochberg 边界与正确性。

func TestNewTrialIDFormat(t *testing.T) {
	id, err := newTrialID(time.Date(2026, 1, 2, 3, 4, 5, int(678*time.Millisecond), time.UTC), bytes.NewReader([]byte{0xde, 0xad, 0xbe, 0xef}))
	if err != nil {
		t.Fatalf("newTrialID: %v", err)
	}
	// 固定时间与随机源 → ID 完全确定（不可变 ID 的可复现性）。
	want := "tr_20260102T030405678Z_deadbeef"
	if id != want {
		t.Fatalf("newTrialID = %q, want %q", id, want)
	}
	if !trialIDRe.MatchString(id) {
		t.Fatalf("ID %q 不匹配 %v", id, trialIDRe)
	}
	// nil 随机源走 crypto/rand，仍须匹配格式。
	if id, err := newTrialID(time.Now(), nil); err != nil || !trialIDRe.MatchString(id) {
		t.Fatalf("newTrialID(nil random) = %q, %v", id, err)
	}
}

func TestValidTrialID(t *testing.T) {
	valid := "tr_20260102T030405678Z_deadbeef"
	if !validTrialID(valid) {
		t.Fatalf("%q 应合法", valid)
	}
	for _, bad := range []string{
		"", "../x", "an_20260102T030405678Z_deadbeef", "tr_20260102T030405678Z_deadbeefX",
		"tr_20260102T030405678Z_DEADBEEF", "tr_20260102T03040567Z_deadbeef",
		"tr_20260102T030405678Z_deadbeef/../../etc", string(make([]byte, 64)),
	} {
		if validTrialID(bad) {
			t.Fatalf("%q 应非法（拒绝路径穿越与格式偏差）", bad)
		}
	}
}

func TestBenjaminiHochbergEmpty(t *testing.T) {
	qs, err := benjaminiHochberg(nil)
	if err != nil || len(qs) != 0 {
		t.Fatalf("空输入 = %v, %v", qs, err)
	}
}

// TestBenjaminiHochbergClassic 经典 BH 算例：ps=[0.01,0.04,0.03,0.005] →
// q=[0.02,0.04,0.04,0.02]（step-up 自大到小取最小单调校正）。
func TestBenjaminiHochbergClassic(t *testing.T) {
	ps := []float64{0.01, 0.04, 0.03, 0.005}
	want := []float64{0.02, 0.04, 0.04, 0.02}
	qs, err := benjaminiHochberg(ps)
	if err != nil {
		t.Fatalf("benjaminiHochberg: %v", err)
	}
	for i := range want {
		if math.Abs(qs[i]-want[i]) > 1e-12 {
			t.Fatalf("q[%d] = %v, want %v（输入顺序保持对应）", i, qs[i], want[i])
		}
	}
}

func TestBenjaminiHochbergTiesAndBounds(t *testing.T) {
	// 并列 p 值：q 相同。
	qs, err := benjaminiHochberg([]float64{0.5, 0.5})
	if err != nil || qs[0] != 0.5 || qs[1] != 0.5 {
		t.Fatalf("并列 p 值 = %v, %v", qs, err)
	}
	// 单调不减（按 p 升序）且截断到 1：p=[0.9,0.95,1.0] → 全部 1.0。
	qs, err = benjaminiHochberg([]float64{0.9, 0.95, 1.0})
	if err != nil {
		t.Fatalf("benjaminiHochberg: %v", err)
	}
	for i, q := range qs {
		if q != 1.0 {
			t.Fatalf("q[%d] = %v, want 1.0（截断）", i, q)
		}
	}
	// 单元素直通。
	if qs, err := benjaminiHochberg([]float64{0.07}); err != nil || qs[0] != 0.07 {
		t.Fatalf("单元素 = %v, %v", qs, err)
	}
}

func TestBenjaminiHochbergRejectsInvalid(t *testing.T) {
	for name, ps := range map[string][]float64{
		"负数": {0.1, -0.01},
		"大于1": {0.5, 1.5},
		"NaN": {math.NaN()},
	} {
		if _, err := benjaminiHochberg(ps); err == nil {
			t.Fatalf("%s 应返回错误（fail closed）", name)
		}
	}
}

// TestTrialStatusWhitelist 结果文件只允许四种终态。
func TestTrialStatusWhitelist(t *testing.T) {
	if finishedTrialStatuses[TrialStatusRunning] {
		t.Fatalf("running 不是完成态")
	}
	for _, s := range []TrialStatus{TrialStatusCompleted, TrialStatusFailed, TrialStatusCanceled, TrialStatusInsufficient} {
		if !finishedTrialStatuses[s] {
			t.Fatalf("%q 应为完成态", s)
		}
	}
	if !regexp.MustCompile(`^[a-z]+$`).MatchString(string(TrialStatusInsufficient)) {
		t.Fatalf("状态值应为小写枚举字符串")
	}
}
