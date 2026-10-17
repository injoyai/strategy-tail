package builder

import (
	"encoding/json"
	"testing"
)

// defaultParamsAsJSON 组件目录默认参数经 JSON round-trip（模拟前端提交形态）。
func defaultParamsAsJSON(t *testing.T, entry ComponentEntry) map[string]any {
	t.Helper()
	m := map[string]any{}
	for _, def := range entry.Params {
		if def.Default != nil {
			m[def.Key] = def.Default
		}
	}
	buf, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("组件 %s 默认参数序列化失败: %v", entry.ID, err)
	}
	var out map[string]any
	if err := json.Unmarshal(buf, &out); err != nil {
		t.Fatalf("组件 %s 默认参数反序列化失败: %v", entry.ID, err)
	}
	return out
}

// TestBuilderCatalogFactoryCoverage 目录条目均有工厂且默认参数可构造；
// 工厂 ID 全部在目录中（防目录与工厂漂移）；默认参数通过参数校验。
func TestBuilderCatalogFactoryCoverage(t *testing.T) {
	catalog := builderCatalog()
	if len(catalog) == 0 {
		t.Fatal("组件目录为空")
	}
	buyIDs := map[string]bool{}
	sellIDs := map[string]bool{}
	for _, entry := range catalog {
		if entry.ID == "" || entry.Name == "" {
			t.Fatalf("组件缺少 ID/名称: %+v", entry)
		}
		switch entry.Role {
		case roleBuy:
			buyIDs[entry.ID] = true
		case roleSell, roleRisk:
			sellIDs[entry.ID] = true
		default:
			t.Fatalf("组件 %s 角色未知: %s", entry.ID, entry.Role)
		}
	}
	for id := range buyFactories {
		if !buyIDs[id] {
			t.Fatalf("买入工厂 %s 不在目录中", id)
		}
	}
	for id := range sellFactories {
		if !sellIDs[id] {
			t.Fatalf("卖出工厂 %s 不在目录中", id)
		}
	}
	for _, entry := range catalog {
		params := defaultParamsAsJSON(t, entry)
		if err := validateParams(entry, params); err != nil {
			t.Fatalf("组件 %s 默认参数未通过校验: %v", entry.ID, err)
		}
		var err error
		if entry.Role == roleBuy {
			_, err = buyFactories[entry.ID](params)
		} else {
			_, err = sellFactories[entry.ID](params)
		}
		if err != nil {
			t.Fatalf("组件 %s 默认参数构造失败: %v", entry.ID, err)
		}
		// 缺省参数（零值语义）也必须可构造
		if entry.Role == roleBuy {
			if _, err = buyFactories[entry.ID](map[string]any{}); err != nil {
				t.Fatalf("组件 %s 空参数构造失败: %v", entry.ID, err)
			}
		} else {
			if _, err = sellFactories[entry.ID](map[string]any{}); err != nil {
				t.Fatalf("组件 %s 空参数构造失败: %v", entry.ID, err)
			}
		}
	}
	// 不可用清单条目不能与可运行目录重复
	seen := map[string]bool{}
	for _, entry := range catalog {
		seen[entry.ID] = true
	}
	for _, u := range builderUnavailable() {
		if u.Reason == "" {
			t.Fatalf("不可用组件 %s 缺少原因说明", u.ID)
		}
		if seen[u.ID] {
			t.Fatalf("组件 %s 同时出现在可运行目录与不可用清单", u.ID)
		}
	}
}
