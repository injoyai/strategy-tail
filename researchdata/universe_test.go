package researchdata

import (
	"testing"
	"time"
)

func tuDate(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 0, 0, 0, 0, time.Local)
}

func TestStaticUniverseMembership(t *testing.T) {
	calls := 0
	u := NewStaticUniverse(StaticUniverseConfig{
		ID:     "test_static",
		Source: "unit",
		Codes: func() []string {
			calls++
			return []string{"sz000002", "sh600000", "sz000002"}
		},
	})

	// 静态池与时间无关：过去/未来同一列表，升序去重。
	for _, asOf := range []time.Time{tuDate(2000, 1, 1), tuDate(2030, 6, 1)} {
		codes, err := u.Codes(asOf)
		if err != nil {
			t.Fatalf("Codes(%s) 返回错误: %v", asOf, err)
		}
		want := []string{"sh600000", "sz000002"}
		if len(codes) != len(want) || codes[0] != want[0] || codes[1] != want[1] {
			t.Fatalf("Codes(%s) = %v, want %v（升序去重）", asOf, codes, want)
		}
	}

	between, err := u.CodesBetween(tuDate(2000, 1, 1), tuDate(2010, 1, 1))
	if err != nil {
		t.Fatalf("CodesBetween 返回错误: %v", err)
	}
	if len(between) != 2 {
		t.Fatalf("CodesBetween = %v, want 当前列表", between)
	}

	ok, err := u.Contains("sh600000", tuDate(1990, 1, 1))
	if err != nil || !ok {
		t.Fatalf("Contains 已知代码 = %v, %v; want true, nil", ok, err)
	}
	ok, err = u.Contains("sz999999", tuDate(2020, 1, 1))
	if err != nil || ok {
		t.Fatalf("Contains 未知代码 = %v, %v; want false, nil", ok, err)
	}

	// 惰性求值：仅在需要时调用 provider。
	if calls == 0 {
		t.Fatal("provider 未被调用")
	}

	snap := u.Snapshot()
	if err := snap.Validate(); err != nil {
		t.Fatalf("Snapshot 校验失败: %v", err)
	}
	if snap.Mode != UniverseModeCurrentStatic {
		t.Fatalf("Mode = %q, want current_static", snap.Mode)
	}
	// 完成条件：当前静态股票池不得被误标为专业验证数据。
	if snap.MembershipPIT != UniversePITUnverified {
		t.Fatalf("MembershipPIT = %q, want unverified（静态池恒为降级）", snap.MembershipPIT)
	}
	if snap.IncludeDelisted {
		t.Fatal("静态池不应声称包含退市股票")
	}
	if snap.ID != "test_static" || snap.Source != "unit" || snap.Size != 2 {
		t.Fatalf("Snapshot = %+v", snap)
	}
	if snap.CoverageStart != "" || snap.CoverageEnd != "" {
		t.Fatalf("静态池不应有历史覆盖日期: %+v", snap)
	}
}

func TestUniverseSnapshotValidate(t *testing.T) {
	base := UniverseSnapshot{
		ID: "u1", Source: "unit", Mode: UniverseModeHistoricalMembership,
		MembershipPIT: UniversePITVerified,
	}
	if err := base.Validate(); err != nil {
		t.Fatalf("合法快照报错: %v", err)
	}

	cases := []struct {
		name string
		mut  func(*UniverseSnapshot)
	}{
		{"空ID", func(s *UniverseSnapshot) { s.ID = " " }},
		{"空来源", func(s *UniverseSnapshot) { s.Source = "" }},
		{"非法模式", func(s *UniverseSnapshot) { s.Mode = "today" }},
		{"非法PIT", func(s *UniverseSnapshot) { s.MembershipPIT = "verified_by_ai" }},
	}
	for _, tc := range cases {
		s := base
		tc.mut(&s)
		if err := s.Validate(); err == nil {
			t.Fatalf("%s: 应报错", tc.name)
		}
	}
}
