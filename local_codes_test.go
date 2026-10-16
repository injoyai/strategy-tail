package common

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/injoyai/strategy-tail/lib/extend"
	"github.com/injoyai/strategy-tail/researchdata"
)

func TestLocalMarketAndUniverse(t *testing.T) {
	oldPull, oldManage, oldRoot := Pull, Manage, runtimeRoot
	t.Cleanup(func() { Pull, Manage, runtimeRoot = oldPull, oldManage, oldRoot })
	Manage = nil // 历史股票池不应需要 TDX 当前在市列表。
	runtimeRoot = t.TempDir()
	root := t.TempDir()
	dir := filepath.Join(root, extend.DirDay)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	for _, code := range []string{"sh600000", "sz000003", "sz300001", "sh000001"} {
		if err := os.WriteFile(filepath.Join(dir, code+".db"), nil, 0644); err != nil {
			t.Fatal(err)
		}
	}
	Pull = &extend.PullKline{Config: extend.PullKlineConfig{Dir: root}}
	want := []string{"sh600000", "sz000003"}
	if got := GetNoPriceLimitCodes(); !slices.Equal(got, want) {
		t.Fatalf("本地主板代码 = %v, want %v", got, want)
	}
	u := newDefaultUniverse()
	codes, err := u.CodesBetween(time.Time{}, time.Now())
	if err != nil || !slices.Equal(codes, append(want, "sz300001")) {
		t.Fatalf("本地默认股票池 = %v, %v", codes, err)
	}
	if ok, err := u.Contains("sz000003", time.Now()); err != nil || !ok {
		t.Fatalf("本地退市代码被排除: %v, %v", ok, err)
	}
	snap := u.Snapshot()
	if err := snap.Validate(); err != nil {
		t.Fatal(err)
	}
	if !snap.IncludeDelisted || snap.MembershipPIT != researchdata.UniversePITUnverified || snap.Size != 3 {
		t.Fatalf("本地池快照错误: %+v", snap)
	}
	// 成员检查和质量快照复用本轮枚举，即使目录暂时不可读也不重新扫描。
	moved := dir + ".snapshot"
	if err := os.Rename(dir, moved); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 100; i++ {
		if ok, err := u.Contains("sz000003", time.Now()); err != nil || !ok {
			t.Fatalf("成员检查应复用股票池: %v, %v", ok, err)
		}
	}
	if ok, err := u.Contains("sh600001", time.Now()); err != nil || ok {
		t.Fatalf("不存在的成员: %v, %v", ok, err)
	}
	if got := u.Snapshot(); got != snap {
		t.Fatalf("质量快照不应重新枚举: %+v", got)
	}
	if _, err := u.Codes(time.Now()); err == nil {
		t.Fatal("新一轮枚举仍须传递目录读取错误")
	}
	if err := os.Rename(moved, dir); err != nil {
		t.Fatal(err)
	}
	// 股票池保持惰性读取，新库不需要重启后才生效。
	if err := os.WriteFile(filepath.Join(dir, "sh600001.db"), nil, 0644); err != nil {
		t.Fatal(err)
	}
	if codes, err := u.Codes(time.Now()); err != nil || len(codes) != 4 {
		t.Fatalf("没有读取新增本地库: %v, %v", codes, err)
	}
	if ok, err := u.Contains("sh600001", time.Now()); err != nil || !ok {
		t.Fatalf("新一轮枚举没有刷新成员: %v, %v", ok, err)
	}
	Pull = nil
	if _, err := Market沪深主板.LocalCodes(); err == nil {
		t.Fatal("未初始化读取器应报错")
	}
	fresh := &localUniverse{}
	if _, err := fresh.Contains("sh600000", time.Now()); err == nil {
		t.Fatal("首次成员查询失败不能被缓存成空池")
	}
	if got := fresh.Snapshot(); got.Size != 0 {
		t.Fatalf("未初始化股票池的快照大小应为 0: %+v", got)
	}
}
