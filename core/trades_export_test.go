package core

import (
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/injoyai/tdx/protocol"
)

func makeExportTrade(code string, buy time.Time) Trade {
	return Trade{
		Code:          code,
		BuyTime:       buy,
		SellTime:      buy.AddDate(0, 0, 1),
		BuyPrice:      protocol.Yuan(10.05),
		SellPrice:     protocol.Yuan(10.30),
		BuyExecPrice:  protocol.Yuan(10.00),
		SellExecPrice: protocol.Yuan(10.35),
		BuyCost:       1005,
		SellIncome:    1030,
		Quantity:      100,
	}
}

// chdirTemp 切换到临时目录并注册还原（Windows 下不还原会导致 TempDir 清理失败）。
func chdirTemp(t *testing.T) {
	t.Helper()
	orig, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(orig) })
}

func TestExportTradesCSV(t *testing.T) {
	chdirTemp(t)
	buy := time.Date(2026, 1, 5, 15, 0, 0, 0, time.Local)
	trades := []Trade{makeExportTrade("sh600000", buy), makeExportTrade("sz000001", buy)}

	path := ExportTradesCSV("测试策略", "冒烟/矩阵:测试", trades)
	if path == "" {
		t.Fatal("期望生成文件，实际为空")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("文件不存在: %v", err)
	}
	checkedPath, err := ExportTradesCSVWithError("测试策略", "可检查", trades)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(checkedPath); err != nil {
		t.Fatalf("带错误返回的导出文件不存在: %v", err)
	}
	// 非法字符必须被替换，路径只在 output/trades/<策略名>/ 下
	wantDir := filepath.Join("output", "trades", "测试策略")
	if filepath.Dir(path) != wantDir {
		t.Fatalf("目录不符: got %s want %s", filepath.Dir(path), wantDir)
	}
	// 空交易不生成
	if p := ExportTradesCSV("测试策略", "空", nil); p != "" {
		t.Fatalf("空交易应返回空路径, got %s", p)
	}
}

func TestExportTradesHTML(t *testing.T) {
	chdirTemp(t)
	buy := time.Date(2026, 1, 5, 15, 0, 0, 0, time.Local)
	trades := []Trade{makeExportTrade("sh600000", buy)}

	// 不带 K 线数据源也能生成纯明细报告
	path := ExportTradesHTML("测试策略", "冒烟", trades, nil)
	if path == "" {
		t.Fatal("期望生成文件，实际为空")
	}
	buf, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content := string(buf)
	if len(content) < 1000 {
		t.Fatalf("HTML 内容过短: %d 字节", len(content))
	}
	for _, kw := range []string{"测试策略", "sh600000", "交易明细", "echarts"} {
		if !contains(content, kw) {
			t.Fatalf("HTML 缺少关键内容: %s", kw)
		}
	}
	if p := ExportTradesHTML("测试策略", "空", nil, nil); p != "" {
		t.Fatalf("空交易应返回空路径, got %s", p)
	}
}

func TestTradesExportNamePreventsSpecialDirectoryNames(t *testing.T) {
	for _, name := range []string{"", ".", ".."} {
		if got := TradesExportName(name); got != "unnamed" {
			t.Fatalf("TradesExportName(%q) = %q, want unnamed", name, got)
		}
	}
	if got := TradesExportName("CON"); got != "_CON" {
		t.Fatalf("TradesExportName(CON) = %q, want _CON", got)
	}
}

func TestExportTradesHTMLWithErrorEscapesStrategyAndReportsInvalidNumbers(t *testing.T) {
	chdirTemp(t)
	trade := makeExportTrade("sh600000", time.Date(2026, 1, 5, 15, 0, 0, 0, time.Local))

	path, err := ExportTradesHTMLWithError(`<script>alert("x")</script>`, "safe", []Trade{trade}, nil)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if contains(string(data), `<script>alert("x")</script>`) {
		t.Fatal("strategy name should be escaped in HTML title")
	}

	trade.SellIncome = math.NaN()
	path, err = ExportTradesHTMLWithError("测试策略", "invalid", []Trade{trade}, nil)
	if err == nil || !contains(err.Error(), "生成交易 HTML") {
		t.Fatalf("ExportTradesHTMLWithError() path=%q error=%v", path, err)
	}
	if path != "" {
		t.Fatalf("failed export should not return path %q", path)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
