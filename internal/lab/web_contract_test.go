package lab

import (
	"strings"
	"testing"
)

// TestLabPageSupportsAnnualGroupChart guards the embedded UI contract: users can
// compare every complete year's full group-return curve or drill into one year
// without starting another analysis run.
func TestLabPageSupportsAnnualGroupChart(t *testing.T) {
	data, err := webFS.ReadFile("web/lab/index.html")
	if err != nil {
		t.Fatal(err)
	}
	page := string(data)
	for _, want := range []string{
		`id="resultScope"`,
		`add('annual', '年度对比')`,
		`function renderAnnualGroupChart(`,
		`currentGrouping(y, currentGroupingN)`,
		`function factorAxisNumber(`,
		`typeof g.factorMean === 'number'`,
		`type: 'value', name: unit === 'ratio'`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("embedded lab page missing annual group-chart contract %q", want)
		}
	}
}
