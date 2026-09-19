package core

import (
	"math"
	"os"
	"strings"
	"testing"
)

func TestExportForwardReturnHTMLWritesReportAndEscapesBuyerName(t *testing.T) {
	chdirTemp(t)

	path, err := ExportForwardReturnHTML(
		`<script>alert("x")</script>`,
		[]ForwardReturnSummary{{Days: 5, Count: 1, AvgReturn: 2.5, WinRate: 100}},
		nil,
		[]int{5},
		10,
		10,
	)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if strings.Contains(content, `<script>alert("x")</script>`) {
		t.Fatal("buyer name should be escaped before embedding in HTML")
	}
	if !strings.Contains(content, `&lt;script&gt;alert(&#34;x&#34;)&lt;/script&gt;`) {
		t.Fatalf("escaped buyer name missing from report: %s", content)
	}
}

func TestExportForwardReturnHTMLReturnsSerializationError(t *testing.T) {
	chdirTemp(t)

	path, err := ExportForwardReturnHTML(
		"测试策略",
		[]ForwardReturnSummary{{Days: 5, AvgReturn: math.NaN()}},
		nil,
		[]int{5},
		10,
		10,
	)
	if err == nil || !strings.Contains(err.Error(), "序列化汇总统计") {
		t.Fatalf("ExportForwardReturnHTML() path=%q error=%v", path, err)
	}
	if path != "" {
		t.Fatalf("failed export should not return a path: %q", path)
	}
	if _, statErr := os.Stat("output"); !os.IsNotExist(statErr) {
		t.Fatalf("serialization failure should not create output, stat error=%v", statErr)
	}
}
