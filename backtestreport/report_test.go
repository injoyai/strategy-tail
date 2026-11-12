package backtestreport_test

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/injoyai/strategy-tail/backtestreport"
)

func TestWritePublishesInteractiveHTML(t *testing.T) {
	dir := t.TempDir()
	put(t, dir, "equity.csv", "variant,date,equity,benchmark_equity\n趋势 & 基准,2026-03-03,101000,100800\n趋势 & 基准,2026-03-02,100000,100000\n")
	put(t, dir, "trades.csv", "variant,code,sell_or_mark_time,net_return_pct,virtual\n趋势,sh000001,2026-03-02 10:10,1.25,false\n趋势,期末持仓,2026-03-03 15:00,30,true\n")
	body := "# 回测\n\n结论 **+1%**\n\n## 数据与覆盖\n\n| 指标 | 值 |\n| --- | ---: |\n| 权益 | 101000 |\n\n[明细](trades.csv)\n\n```sh\ngo run example.go\n```"
	if err := backtestreport.Write(dir, body); err != nil {
		t.Fatal(err)
	}
	report := published(t, dir)
	for _, want := range []string{
		"<h1>回测</h1>", "<strong>+1%</strong>", "<h2>数据与覆盖</h2>", "<table>",
		`<td class="numeric">101000</td>`, `href="trades.csv"`, "go run example.go", "<canvas", "账户权益", "逐笔平仓净收益率",
	} {
		if !strings.Contains(report, want) {
			t.Errorf("HTML 缺少 %q", want)
		}
	}
	charts := chartData(t, report)
	if len(charts) != 2 || !strings.Contains(charts[0], "趋势") || !strings.Contains(charts[0], "100800") {
		t.Fatalf("账户或基准序列缺失: %v", charts)
	}
	if strings.Index(charts[0], "100000") > strings.Index(charts[0], "101000") {
		t.Fatal("账户权益未按时间排序")
	}
	if !strings.Contains(charts[1], "sh000001") || strings.Contains(charts[1], "期末持仓") {
		t.Fatal("平仓图未按 virtual 字段筛选成交")
	}
	if err := backtestreport.Write(dir, body); err != nil {
		t.Fatal(err)
	}
	if updated := published(t, dir); updated != report {
		t.Fatal("相同输入重复发布改变了报告")
	}
	if _, err := os.Stat(filepath.Join(dir, "report.md")); !os.IsNotExist(err) {
		t.Fatalf("发布不应生成 report.md: %v", err)
	}
}

func TestWriteConvertsLegacyMarkdownWithoutDuplicatingCharts(t *testing.T) {
	dir := t.TempDir()
	body := "# 回测\n\n<!-- backtestreport:charts:start -->\n\n## 回测图表\n\n![账户权益](backtest-chart-0123456789abcdef.svg)\n\n![原始信号 \\[触发\\]](backtest-chart-fedcba9876543210.svg)\n\n<!-- backtestreport:charts:end -->\n\n## 结果\n\n正文"
	put(t, dir, "report.md", body)
	put(t, dir, "equity.csv", "variant,date,equity\n趋势,2026-03-02,100000\n")

	if err := backtestreport.Write(dir, body); err != nil {
		t.Fatal(err)
	}
	report := published(t, dir)
	if strings.Contains(report, "backtest-chart-0123456789abcdef.svg") || !strings.Contains(report, `<img src="backtest-chart-fedcba9876543210.svg" alt="原始信号 [触发]"`) {
		t.Fatal("应替换可重建的标准图并保留旧报告自带的信号图")
	}
	if got, err := os.ReadFile(filepath.Join(dir, "report.md")); err != nil || string(got) != body {
		t.Fatalf("原始 Markdown 发生变化: %v", err)
	}
}

func TestWriteCustomSignalAndMissingData(t *testing.T) {
	dir := t.TempDir()
	body := "# 回测\n\n<script>alert(1)</script>\n\n[危险链接](javascript:alert)"
	if err := backtestreport.Write(dir, body); err != nil {
		t.Fatal(err)
	}
	report := published(t, dir)
	if strings.Contains(report, "<canvas") || !strings.Contains(report, "没有可绘制") {
		t.Fatal("缺数据时缺少说明")
	}
	if strings.Contains(report, "<script>alert") || strings.Contains(report, `href="javascript:`) {
		t.Fatal("正文中的原始 HTML 或危险链接进入了报告")
	}

	at := time.Date(2026, 3, 2, 10, 10, 0, 0, time.UTC)
	custom := backtestreport.Chart{
		Title: "量柱 [触发] <阈值>",
		Series: []backtestreport.Series{{
			Name: "卖出", Scatter: true,
			Points: []backtestreport.Point{{Time: at, Value: 0.93, Label: "触发 </script><script>alert(1)</script>"}},
		}},
	}
	if err := backtestreport.Write(dir, body, custom); err != nil {
		t.Fatal(err)
	}
	report = published(t, dir)
	charts := chartData(t, report)
	if len(charts) != 1 || !strings.Contains(report, "量柱 [触发] &lt;阈值&gt;") || strings.Contains(report, "没有可绘制") {
		t.Fatal("自定义图表未替换空区段")
	}
	if strings.Contains(report, "</script><script>alert") || !strings.Contains(charts[0], "0.93") {
		t.Fatal("图表数据未安全嵌入")
	}
}

func TestWriteKeepsPublishedReportOnInvalidData(t *testing.T) {
	dir := t.TempDir()
	put(t, dir, "report.html", "上一次报告")
	put(t, dir, "equity.csv", "variant,date,equity\n趋势,2026-03-02,NaN\n")
	if err := backtestreport.Write(dir, "# 本次报告"); err == nil {
		t.Fatal("接受了无效权益")
	}
	if report := published(t, dir); report != "上一次报告" {
		t.Fatal("失败的发布改变了已有报告")
	}

	if err := os.Remove(filepath.Join(dir, "equity.csv")); err != nil {
		t.Fatal(err)
	}
	bad := backtestreport.Chart{Title: "信号", Series: []backtestreport.Series{{
		Name: "信号", Points: []backtestreport.Point{{Time: time.Now(), Value: math.Inf(1)}},
	}}}
	if err := backtestreport.Write(dir, "# 本次报告", bad); err == nil {
		t.Fatal("接受了无效的自定义图表")
	}
	if report := published(t, dir); report != "上一次报告" {
		t.Fatal("失败的发布改变了已有报告")
	}
}

func put(t *testing.T, dir, name, text string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
}

func published(t *testing.T, dir string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(dir, "report.html"))
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func chartData(t *testing.T, report string) []string {
	t.Helper()
	matches := regexp.MustCompile(`(?s)<script type="application/json">(.*?)</script>`).FindAllStringSubmatch(report, -1)
	var data []string
	for _, match := range matches {
		if !json.Valid([]byte(match[1])) {
			t.Fatal("图表数据不是合法 JSON")
		}
		data = append(data, match[1])
	}
	return data
}

func TestWriteKeepsSameVariantSeparateAcrossPeriods(t *testing.T) {
	dir := t.TempDir()
	put(t, dir, "equity.csv", "variant,period,date,equity,benchmark_equity\n趋势,长期,2026-03-02,100,100\n趋势,近期,2026-03-03,200,100\n趋势,长期,2026-03-03,110,101\n")
	put(t, dir, "trades.csv", "variant,period,sell_time,net_return_pct,virtual\n趋势,长期,2026-03-03,10,false\n趋势,近期,2026-03-03,20,false\n")
	if err := backtestreport.Write(dir, "# 区间对比"); err != nil {
		t.Fatal(err)
	}
	charts := chartData(t, published(t, dir))
	for index, want := range [][]int{{2, 1, 2, 1}, {1, 1}} {
		var chart struct {
			Series []struct {
				Period string
				Points []json.RawMessage
			}
		}
		if err := json.Unmarshal([]byte(charts[index]), &chart); err != nil {
			t.Fatal(err)
		}
		if len(chart.Series) != len(want) {
			t.Fatalf("区间合并错误: %v", chart.Series)
		}
		for i, series := range chart.Series {
			period := []string{"长期", "近期"}[i%2]
			if series.Period != period || len(series.Points) != want[i] {
				t.Fatalf("区间或观测值数量错误: %v", series)
			}
		}
	}
}
