// Package backtestreport publishes standalone HTML reports from backtest results.
package backtestreport

import (
	"bytes"
	"cmp"
	_ "embed"
	"encoding/json"
	"fmt"
	"html/template"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// Point is an observation or a chart marker at its source time.
type Point struct {
	Time  time.Time
	Value float64
	Label string // Optional detail shown in the chart readout.
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

//go:embed report.html.tmpl
var pageTemplate string

//go:embed report.css
var reportCSS template.CSS

//go:embed chart.js
var chartJS template.JS

var page = template.Must(template.New("report").Parse(pageTemplate))

type reportPage struct {
	Title               string
	Heading             template.HTML
	Intro, Body, Legacy template.HTML
	Charts              []reportChart
	CSS                 template.CSS
	JS                  template.JS
}

type reportChart struct {
	Title string
	Data  template.JS
}

// Write publishes report.html using Markdown prose and any available equity.csv,
// trades.csv, and strategy-specific charts. The input Markdown is left intact.
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

	report := reportPage{CSS: reportCSS, JS: chartJS}
	rebuilt := make(map[string]bool)
	addChart := func(chart Chart, periods []string, signedTrades bool) error {
		data, err := encodeChart(chart, periods, signedTrades)
		if err != nil {
			return fmt.Errorf("绘制 %q: %w", chart.Title, err)
		}
		report.Charts = append(report.Charts, reportChart{Title: chart.Title, Data: data})
		rebuilt[chart.Title] = true
		return nil
	}
	if len(equity.Series) > 0 {
		if err := addChart(equity.Chart, equity.periods, false); err != nil {
			return err
		}
	}
	if len(trades.Series) > 0 {
		if err := addChart(trades.Chart, trades.periods, true); err != nil {
			return err
		}
	}
	for _, chart := range extra {
		if err := addChart(chart, nil, false); err != nil {
			return err
		}
	}

	markdown, legacy, err := stripLegacyCharts(markdown, rebuilt)
	if err != nil {
		return err
	}
	report.Title, report.Heading, report.Intro, report.Body = reportMarkdown(markdown)
	report.Legacy = markdownBlocks(strings.Split(strings.TrimSpace(legacy), "\n"), 3)

	var output bytes.Buffer
	if err := page.Execute(&output, report); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	return publish(filepath.Join(dir, "report.html"), output.Bytes())
}

func encodeChart(chart Chart, periods []string, signedTrades bool) (template.JS, error) {
	if strings.TrimSpace(chart.Title) == "" || len(chart.Series) == 0 {
		return "", fmt.Errorf("图表需要标题和数据序列")
	}

	type plotPoint struct {
		Time  int64   `json:"t"`
		Value float64 `json:"v"`
		Label string  `json:"label,omitempty"`
	}
	type plotSeries struct {
		Name    string      `json:"name"`
		Period  string      `json:"period,omitempty"`
		Scatter bool        `json:"scatter,omitempty"`
		Points  []plotPoint `json:"points"`
	}
	data := struct {
		Series       []plotSeries `json:"series"`
		Unit         string       `json:"unit"`
		ZeroBaseline bool         `json:"zeroBaseline"`
		SignedTrades bool         `json:"signedTrades"`
	}{Unit: chart.Unit, ZeroBaseline: chart.ZeroBaseline, SignedTrades: signedTrades}

	minimum, maximum := math.Inf(1), math.Inf(-1)
	for index, series := range chart.Series {
		if strings.TrimSpace(series.Name) == "" || len(series.Points) == 0 {
			return "", fmt.Errorf("数据序列需要名称和观测值")
		}
		points := make([]plotPoint, 0, len(series.Points))
		for _, point := range series.Points {
			if point.Time.IsZero() || point.Time.Year() < 1 || point.Time.Year() > 9999 ||
				math.IsNaN(point.Value) || math.IsInf(point.Value, 0) {
				return "", fmt.Errorf("序列 %q 存在无效的时间或数值", series.Name)
			}
			minimum = math.Min(minimum, point.Value)
			maximum = math.Max(maximum, point.Value)
			points = append(points, plotPoint{point.Time.UnixMilli(), point.Value, point.Label})
		}
		slices.SortStableFunc(points, func(a, b plotPoint) int { return cmp.Compare(a.Time, b.Time) })
		encoded := plotSeries{Name: series.Name, Scatter: series.Scatter, Points: points}
		if periods != nil {
			encoded.Period = periods[index]
		}
		data.Series = append(data.Series, encoded)
	}
	if chart.ZeroBaseline {
		minimum, maximum = math.Min(0, minimum), math.Max(0, maximum)
	}
	span := maximum - minimum
	padding := max(span*0.1, math.Abs(maximum)*0.000001, 1e-12)
	if math.IsInf(maximum+padding-(minimum-padding), 0) {
		return "", fmt.Errorf("数值范围无法绘制")
	}

	// Marshal escapes HTML-significant characters before insertion into a script element.
	raw, err := json.Marshal(data)
	return template.JS(raw), err
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
