package lab

import (
	"strings"
	"testing"
)

// readLabFile 读取 go:embed 的 web/lab 下静态文件（用于前端契约检查）。
func readLabFile(t *testing.T, name string) string {
	t.Helper()
	data, err := webFS.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// TestLabPageSupportsAnnualGroupChart guards the embedded UI contract: users can
// compare every complete year's full group-return curve or drill into one year
// without starting another analysis run. 页面已迁移为 Vue 模块化，契约驻留在
// Tab4Factor.js（模板与图表渲染）与 format.js（分组/坐标换算）中。
func TestLabPageSupportsAnnualGroupChart(t *testing.T) {
	page := readLabFile(t, "web/lab/js/components/Tab4Factor.js") +
		"\n" + readLabFile(t, "web/lab/js/format.js")
	for _, want := range []string{
		`id="resultScope"`,
		`'年度对比'`,
		`renderAnnualGroupChart`,
		`currentGrouping(y, state.currentGroupingN)`,
		`factorAxisNumber`,
		`typeof g.factorMean === 'number'`,
		`type: 'value', name: unit === 'ratio'`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("embedded lab UI missing annual group-chart contract %q", want)
		}
	}
}

// TestLabPageGroupsFactorSelectors guards the catalog classification contract:
// both research and strategy factor selectors are grouped from backend Category
// metadata, while option values remain the stable factor kind.
func TestLabPageGroupsFactorSelectors(t *testing.T) {
	page := readLabFile(t, "web/lab/js/format.js") +
		"\n" + readLabFile(t, "web/lab/js/components/Tab1Run.js") +
		"\n" + readLabFile(t, "web/lab/js/components/Tab4Factor.js")
	for _, want := range []string{
		`export function groupFactors(`,
		`e.category || '其他'`,
		`optgroup`,
		`v-for="g in factorGroups"`,
		`v-model="factorKind"`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("embedded lab UI missing grouped factor-selector contract %q", want)
		}
	}
}
