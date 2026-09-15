package factor

import "testing"

func TestAll(t *testing.T) {
	all := All()
	if len(all) != 14 {
		t.Fatalf("目录应有 14 项, got %d", len(all))
	}
	if all[0].Kind != "momentum" || all[0].Name != "N日动量(20)" {
		t.Fatalf("首项异常: %+v", all[0])
	}
	// JSON 字段非空（供 /api/factors 直出）
	for _, e := range all {
		if e.Kind == "" || e.Name == "" || e.Description == "" {
			t.Fatalf("目录项字段缺失: %+v", e)
		}
	}
	// kind 唯一，且 Build(kind,0) 的名字与目录名一致（锁定 registry Default 与因子内 daysOr fallback 不漂移）
	seen := map[string]bool{}
	for _, e := range all {
		if seen[e.Kind] {
			t.Fatalf("kind 重复: %s", e.Kind)
		}
		seen[e.Kind] = true
		if f := Build(e.Kind, 0); f == nil || f.Name() != e.Name {
			t.Fatalf("%s: Build 默认名 %v 与目录名 %q 不一致", e.Kind, f, e.Name)
		}
	}
}

func TestBuild(t *testing.T) {
	// days<=0 → 默认参数
	if f := Build("momentum", 0); f == nil || f.Name() != "N日动量(20)" {
		t.Fatalf("默认参数异常: %v", f)
	}
	// 显式参数
	if f := Build("momentum", 5); f == nil || f.Name() != "N日动量(5)" {
		t.Fatalf("参数传递异常: %v", f)
	}
	// 具体类型断言
	if _, ok := Build("kvalue", 0).(*K值); !ok {
		t.Fatalf("kvalue 应构造 *K值")
	}
	if _, ok := Build("body", 0).(*实体幅度); !ok {
		t.Fatalf("body 应构造 *实体幅度")
	}
	// 单根K线因子显式传正数 days 也被忽略
	if _, ok := Build("body", 5).(*实体幅度); !ok {
		t.Fatalf("body 应忽略 days")
	}
	// 未知 kind → nil
	if f := Build("no_such", 5); f != nil {
		t.Fatalf("未知 kind 应返回 nil, got %v", f)
	}
}
