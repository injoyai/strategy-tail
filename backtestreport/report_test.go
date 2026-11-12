package backtestreport_test

import (
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/injoyai/strategy-tail/backtestreport"
)

func TestWriteEmbedsAccountAndClosedTrades(t *testing.T) {
	dir := t.TempDir()
	put(t, dir, "equity.csv", "variant,date,equity,benchmark_equity\n趋势 & 基准,2026-03-03,101000,100800\n趋势 & 基准,2026-03-02,100000,100000\n")
	put(t, dir, "trades.csv", "variant,code,sell_or_mark_time,net_return_pct,virtual\n趋势,sh000001,2026-03-02 10:10,1.25,false\n趋势,期末持仓,2026-03-03 15:00,30,true\n")

	body := "# 回测\n\n结论\n\n## 数据与覆盖\n\n样本"
	if err := backtestreport.Write(dir, body); err != nil {
		t.Fatal(err)
	}
	result, figures := published(t, dir)
	if len(figures) != 2 || !strings.Contains(result, "## 数据与覆盖\n\n样本") {
		t.Fatalf("报告未保留正文或缺少图表: %s", result)
	}
	if !strings.Contains(figures[0], "账户权益") || !strings.Contains(figures[0], "趋势 &amp; 基准") {
		t.Fatal("账户图缺少账户或基准序列")
	}
	if !strings.Contains(figures[1], "sh000001") || strings.Contains(figures[1], "期末持仓") {
		t.Fatal("平仓图未按 virtual 字段筛选成交")
	}
	if err := backtestreport.Write(dir, result); err != nil {
		t.Fatal(err)
	}
	if updated, _ := published(t, dir); updated != result {
		t.Fatalf("重复发布改变了报告: %s", updated)
	}
}

func TestWriteCustomSignalAndMissingData(t *testing.T) {
	dir := t.TempDir()
	body := "# 回测"
	if err := backtestreport.Write(dir, body); err != nil {
		t.Fatal(err)
	}
	report, figures := published(t, dir)
	if len(figures) != 0 || !strings.Contains(report, "没有可绘制") {
		t.Fatalf("缺数据时缺少说明: %s", report)
	}

	at := time.Date(2026, 3, 2, 10, 10, 0, 0, time.UTC)
	custom := backtestreport.Chart{
		Title: "量柱 [触发] <阈值>",
		Series: []backtestreport.Series{{
			Name:    "卖出",
			Scatter: true,
			Points:  []backtestreport.Point{{Time: at, Value: 0.93, Label: "触发"}},
		}},
	}
	if err := backtestreport.Write(dir, report, custom); err != nil {
		t.Fatal(err)
	}
	report, figures = published(t, dir)
	if len(figures) != 1 || !strings.Contains(figures[0], "&lt;阈值&gt;") || strings.Contains(report, "没有可绘制") {
		t.Fatalf("自定义图表未替换空区段: %s", report)
	}
}

func TestWriteKeepsPublishedReportOnInvalidData(t *testing.T) {
	dir := t.TempDir()
	put(t, dir, "report.md", "# 上一次报告\n")
	put(t, dir, "equity.csv", "variant,date,equity\n趋势,2026-03-02,NaN\n")
	if err := backtestreport.Write(dir, "# 本次报告"); err == nil {
		t.Fatal("接受了无效权益")
	}
	if report, figures := published(t, dir); report != "# 上一次报告\n" || len(figures) != 0 {
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
	if report, _ := published(t, dir); report != "# 上一次报告\n" {
		t.Fatal("失败的发布改变了已有报告")
	}
}

func put(t *testing.T, dir, name, text string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
}

func published(t *testing.T, dir string) (string, []string) {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(dir, "report.md"))
	if err != nil {
		t.Fatal(err)
	}
	references := regexp.MustCompile(`\((backtest-chart-[a-f0-9]+\.svg)\)`).FindAllSubmatch(body, -1)
	var figures []string
	for _, ref := range references {
		svg, err := os.ReadFile(filepath.Join(dir, string(ref[1])))
		if err != nil {
			t.Fatal(err)
		}
		decoder := xml.NewDecoder(bytes.NewReader(svg))
		for {
			if _, err := decoder.Token(); errors.Is(err, io.EOF) {
				break
			} else if err != nil {
				t.Fatalf("图表不是合法的 SVG: %v", err)
			}
		}
		figures = append(figures, string(svg))
	}
	return string(body), figures
}
