// Package backtestreport publishes Markdown reports with figures from backtest results.
package backtestreport

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"html"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Point is an observation or a chart marker at its source time.
type Point struct {
	Time  time.Time
	Value float64
	Label string // Optional detail shown when opening the SVG.
}

// Series is plotted chronologically. Scatter shows individual observations.
type Series struct {
	Name    string
	Points  []Point
	Scatter bool
}

// Chart describes a time series, such as a strategy's intraday signal and threshold.
type Chart struct {
	Title        string
	Unit         string
	Series       []Series
	ZeroBaseline bool // Include a visible zero reference for returns and signed values.
}

const (
	chartStart = "<!-- backtestreport:charts:start -->"
	chartEnd   = "<!-- backtestreport:charts:end -->"
)

// Write publishes report.md with local SVG figures. It reads equity.csv and
// trades.csv when present and then appends any strategy-specific charts.
// The Markdown can be an existing report from a previous Write call.
func Write(dir, markdown string, extra ...Chart) error {
	if strings.TrimSpace(dir) == "" || strings.TrimSpace(markdown) == "" {
		return fmt.Errorf("报告需要输出目录和 Markdown 正文")
	}

	equity, err := equityChart(filepath.Join(dir, "equity.csv"))
	if err != nil {
		return err
	}
	trades, err := tradeChart(filepath.Join(dir, "trades.csv"))
	if err != nil {
		return err
	}
	var charts []Chart
	for _, standard := range []Chart{equity, trades} {
		if len(standard.Series) > 0 {
			charts = append(charts, standard)
		}
	}
	charts = append(charts, extra...)

	figures := make(map[string][]byte, len(charts))
	var section strings.Builder
	section.WriteString(chartStart + "\n\n## 回测图表\n\n")
	if len(charts) == 0 {
		section.WriteString("本次报告没有可绘制的权益、平仓成交或信号数据。\n\n")
	}
	for _, chart := range charts {
		var svg bytes.Buffer
		if err := render(&svg, chart); err != nil {
			return fmt.Errorf("绘制 %q: %w", chart.Title, err)
		}
		data := svg.Bytes()
		digest := sha256.Sum256(data)
		name := fmt.Sprintf("backtest-chart-%x.svg", digest[:8])
		figures[name] = data
		title := strings.NewReplacer("\\", "\\\\", "[", "\\[", "]", "\\]", "\n", " ", "\r", " ").Replace(html.EscapeString(chart.Title))
		fmt.Fprintf(&section, "![%s](%s)\n\n", title, name)
	}
	section.WriteString(chartEnd)
	result, err := withCharts(markdown, section.String())
	if err != nil {
		return err
	}

	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	for name, data := range figures {
		if err := publish(filepath.Join(dir, name), data); err != nil {
			return err
		}
	}
	return publish(filepath.Join(dir, "report.md"), []byte(result))
}

func withCharts(markdown, section string) (string, error) {
	start, end := strings.Index(markdown, chartStart), strings.Index(markdown, chartEnd)
	if start >= 0 || end >= 0 {
		if start < 0 || end < start || strings.Count(markdown, chartStart) != 1 || strings.Count(markdown, chartEnd) != 1 {
			return "", fmt.Errorf("报告中的图表区段标记不完整")
		}
		return markdown[:start] + section + markdown[end+len(chartEnd):], nil
	}

	// Place figures after the opening paragraph and before the first section.
	if at := strings.Index(markdown, "\n## "); at >= 0 {
		return markdown[:at] + "\n\n" + section + "\n\n" + markdown[at+1:], nil
	}
	return markdown + "\n\n" + section + "\n", nil
}

func publish(path string, data []byte) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".backtest-report-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	_, writeErr := file.Write(data)
	closeErr := file.Close()
	if writeErr != nil {
		return writeErr
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(file.Name(), path)
}
