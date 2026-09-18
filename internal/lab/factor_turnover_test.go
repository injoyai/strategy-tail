package lab

import (
	"testing"
)

// factor_turnover_test.go：分组换手纯函数的手算锁定。
// turnover(t,p) = 1 - |G(t) ∩ G(t-p)| / |G(t)|；两期组规模如实披露，
// 组缺失 → null，并列块导致规模变化时不截断成员。

func tvTurnoverMembers(ss ...[]string) []map[string]struct{} {
	ms := make([]map[string]struct{}, len(ss))
	for i, s := range ss {
		m := make(map[string]struct{}, len(s))
		for _, c := range s {
			m[c] = struct{}{}
		}
		ms[i] = m
	}
	return ms
}

func TestGroupTurnoverSeriesHandComputed(t *testing.T) {
	dates := []string{"2024-01-02", "2024-01-03", "2024-01-04"}
	ms := tvTurnoverMembers(
		[]string{"a", "b", "c"},
		[]string{"a", "b", "d"},
		[]string{"b", "d", "e"},
	)
	got := groupTurnoverSeries(dates, ms, 1)
	if len(got) != 2 {
		t.Fatalf("点数 = %d, want 2（首日无前一期）", len(got))
	}
	if got[0].Date != "2024-01-03" || got[1].Date != "2024-01-04" {
		t.Fatalf("日期错误: %v, %v", got[0].Date, got[1].Date)
	}
	// d2: 1 - |{a,b,c}∩{a,b,d}|/3 = 1/3；两期规模均 3
	if got[0].CurrentSize != 3 || got[0].PriorSize == nil || *got[0].PriorSize != 3 {
		t.Fatalf("d2 规模 = %d/%v, want 3/3", got[0].CurrentSize, got[0].PriorSize)
	}
	if got[0].Turnover == nil || !nearlyEq(*got[0].Turnover, 1.0/3.0) {
		t.Fatalf("d2 turnover = %v, want 1/3", got[0].Turnover)
	}
	// d3: 1 - |{a,b,d}∩{b,d,e}|/3 = 1/3
	if got[1].Turnover == nil || !nearlyEq(*got[1].Turnover, 1.0/3.0) {
		t.Fatalf("d3 turnover = %v, want 1/3", got[1].Turnover)
	}
}

func TestGroupTurnoverSeriesPeriodTwo(t *testing.T) {
	dates := []string{"d1", "d2", "d3"}
	ms := tvTurnoverMembers(
		[]string{"a", "b", "c"},
		[]string{"a", "b", "d"},
		[]string{"b", "d", "e"},
	)
	got := groupTurnoverSeries(dates, ms, 2)
	if len(got) != 1 || got[0].Date != "d3" {
		t.Fatalf("应只有 d3 一点，得到 %v", got)
	}
	// 1 - |{a,b,c}∩{b,d,e}|/3 = 1 - 1/3 = 2/3
	if got[0].Turnover == nil || !nearlyEq(*got[0].Turnover, 2.0/3.0) {
		t.Fatalf("turnover = %v, want 2/3", got[0].Turnover)
	}
}

func TestGroupTurnoverSeriesBounds(t *testing.T) {
	dates := []string{"d1", "d2"}
	stable := tvTurnoverMembers([]string{"a", "b"}, []string{"a", "b"})
	if got := groupTurnoverSeries(dates, stable, 1); len(got) != 1 ||
		got[0].Turnover == nil || *got[0].Turnover != 0 {
		t.Fatalf("成员不变 turnover 应为 0，得到 %v", got)
	}
	swapped := tvTurnoverMembers([]string{"a", "b"}, []string{"c", "d"})
	if got := groupTurnoverSeries(dates, swapped, 1); len(got) != 1 ||
		got[0].Turnover == nil || *got[0].Turnover != 1 {
		t.Fatalf("成员全换 turnover 应为 1，得到 %v", got)
	}
	// period 超出可用前缀长度：无点
	if got := groupTurnoverSeries(dates, stable, 5); len(got) != 0 {
		t.Fatalf("period 超出应无点，得到 %v", got)
	}
	// period<1 为合同违反：返回 nil，不冒充数据
	if got := groupTurnoverSeries(dates, stable, 0); got != nil {
		t.Fatalf("period=0 应返回 nil，得到 %v", got)
	}
	// dates 与 members 长度不一致：合同违反
	if got := groupTurnoverSeries(dates, stable[:1], 1); got != nil {
		t.Fatalf("长度不一致应返回 nil，得到 %v", got)
	}
}

func TestGroupTurnoverSeriesMissingGroups(t *testing.T) {
	dates := []string{"d1", "d2", "d3"}
	ms := []map[string]struct{}{
		nil, // d1 缺失
		{"a": {}, "b": {}},
		{}, // d3 空集等同缺失
	}
	got := groupTurnoverSeries(dates, ms, 1)
	if len(got) != 1 {
		t.Fatalf("点数 = %d, want 1", len(got))
	}
	// d2 有当前组但前一期缺失：turnover null、PriorSize null、CurrentSize 如实记录
	if got[0].Date != "d2" {
		t.Fatalf("应只产生 d2 一点，得到 %s", got[0].Date)
	}
	if got[0].Turnover != nil {
		t.Fatalf("前期缺失时 turnover 应为 null，得到 %v", *got[0].Turnover)
	}
	if got[0].PriorSize != nil {
		t.Fatalf("前期缺失时 PriorSize 应为 null，得到 %d", *got[0].PriorSize)
	}
	if got[0].CurrentSize != 2 {
		t.Fatalf("CurrentSize = %d, want 2", got[0].CurrentSize)
	}
}

func TestGroupTurnoverSeriesSizeChangeNotTruncated(t *testing.T) {
	// 并列块导致规模变化：G(t)=5 人、G(t-1)=3 人，交集 2（a,b）。
	// 不截断成员制造固定组数：分母用当前规模 5，两期规模如实披露。
	dates := []string{"d1", "d2"}
	ms := tvTurnoverMembers(
		[]string{"a", "b", "c"},
		[]string{"a", "b", "d", "e", "f"},
	)
	got := groupTurnoverSeries(dates, ms, 1)
	if len(got) != 1 {
		t.Fatalf("点数 = %d, want 1", len(got))
	}
	if got[0].CurrentSize != 5 || got[0].PriorSize == nil || *got[0].PriorSize != 3 {
		t.Fatalf("规模 = %d/%v, want 5/3", got[0].CurrentSize, got[0].PriorSize)
	}
	if got[0].Turnover == nil || !nearlyEq(*got[0].Turnover, 0.6) {
		t.Fatalf("turnover = %v, want 0.6", got[0].Turnover)
	}
}
