package lab

import (
	"slices"
	"testing"
	"time"

	common "github.com/injoyai/strategy-tail"
)

func TestResolveCodesUsesStoredDayCodes(t *testing.T) {
	oldPull, oldManage := common.Pull, common.Manage
	t.Cleanup(func() { common.Pull, common.Manage = oldPull, oldManage })
	common.Manage = nil
	root := t.TempDir()
	common.Pull = fixturePull(t, root)
	for _, code := range []string{"sh600000", "sz000003", "sz300001", "sh000001"} {
		writeFakeDayDB(t, root, code, 1, time.Now())
	}
	r := &Runner{}
	want := []string{"sh600000", "sz000003"}
	got, err := r.resolveCodes(RunConfig{SampleMode: "all"}, nil)
	if err != nil || !slices.Equal(got, want) {
		t.Fatalf("全量本地代码 = %v, %v; want %v", got, err, want)
	}
	for _, size := range []int{1, 10} {
		got, err := r.resolveCodes(RunConfig{SampleMode: "random", SampleSize: size}, nil)
		if err != nil || len(got) != min(size, len(want)) {
			t.Fatalf("随机本地代码 = %v, %v", got, err)
		}
		for _, code := range got {
			if !slices.Contains(want, code) {
				t.Fatalf("随机样本包含本地主板以外代码: %s", code)
			}
		}
	}
	common.Pull = nil
	if _, err := r.resolveCodes(RunConfig{SampleMode: "all"}, nil); err == nil {
		t.Fatal("本地数据不可用时应报错")
	}
	got, err = r.resolveCodes(RunConfig{SampleMode: "codes", SampleCodes: []string{"sz000003"}}, nil)
	if err != nil || !slices.Equal(got, []string{"sz000003"}) {
		t.Fatalf("显式指定代码应保持原行为: %v, %v", got, err)
	}
}
