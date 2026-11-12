package backtestreport

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

var sourceTime = time.FixedZone("UTC+8", 8*60*60)

type csvChart struct {
	Chart
	periods []string
}

func (c *csvChart) add(series Series, period string) {
	c.Series = append(c.Series, series)
	c.periods = append(c.periods, period)
}

type csvGroup struct {
	name, period string
}

func equityChart(path string) (csvChart, error) {
	chart := csvChart{Chart: Chart{Title: "账户权益", Unit: "元"}}
	groups, benchmarks := make(map[csvGroup][]Point), make(map[csvGroup][]Point)
	var ordered []csvGroup
	err := readCSV(path, []string{"variant", "date", "equity"}, func(row csvRow) error {
		name, at, err := row.seriesTime("date")
		if err != nil {
			return err
		}
		value, err := row.number("equity")
		if err != nil {
			return err
		}
		group := csvGroup{name, row.get("period")}
		if _, exists := groups[group]; !exists {
			ordered = append(ordered, group)
		}
		groups[group] = append(groups[group], Point{Time: at, Value: value})
		if row.get("benchmark_equity") != "" {
			value, err := row.number("benchmark_equity")
			if err != nil {
				return err
			}
			benchmarks[group] = append(benchmarks[group], Point{Time: at, Value: value})
		}
		return nil
	})
	if err != nil {
		return csvChart{}, err
	}
	var variantsPerPeriod = make(map[string]int)
	for _, group := range ordered {
		chart.add(Series{Name: group.name, Points: groups[group]}, group.period)
		variantsPerPeriod[group.period]++
	}
	for _, group := range ordered {
		points := benchmarks[group]
		if len(points) == 0 {
			continue
		}
		partial := len(points) != len(groups[group])
		label := "基准"
		if partial {
			label += "（可用日期）"
		}
		if variantsPerPeriod[group.period] > 1 {
			label += " · " + group.name
		}
		chart.add(Series{Name: label, Points: points, Scatter: partial}, group.period)
	}
	return chart, nil
}

func tradeChart(path string) (csvChart, error) {
	chart := csvChart{Chart: Chart{Title: "逐笔平仓净收益率", Unit: "%", ZeroBaseline: true}}
	groups := make(map[csvGroup][]Point)
	var ordered []csvGroup
	err := readCSV(path, []string{"variant", "virtual", "net_return_pct"}, func(row csvRow) error {
		virtual, err := strconv.ParseBool(row.get("virtual"))
		if err != nil {
			return fmt.Errorf("virtual 须为布尔值")
		}
		if virtual {
			return nil
		}
		timeField := "sell_time"
		if _, ok := row.columns[timeField]; !ok {
			timeField = "sell_or_mark_time"
		}
		name, at, err := row.seriesTime(timeField)
		if err != nil {
			return err
		}
		value, err := row.number("net_return_pct")
		if err != nil {
			return err
		}
		group := csvGroup{name, row.get("period")}
		if _, exists := groups[group]; !exists {
			ordered = append(ordered, group)
		}
		groups[group] = append(groups[group], Point{Time: at, Value: value, Label: row.get("code")})
		return nil
	})
	for _, group := range ordered {
		chart.add(Series{Name: group.name, Points: groups[group], Scatter: true}, group.period)
	}
	return chart, err
}

type csvRow struct {
	columns map[string]int
	values  []string
}

func (row csvRow) get(name string) string {
	index, ok := row.columns[name]
	if !ok {
		return ""
	}
	return strings.TrimSpace(row.values[index])
}

func (row csvRow) number(field string) (float64, error) {
	value, err := strconv.ParseFloat(row.get(field), 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, fmt.Errorf("%s 须为有限数值", field)
	}
	return value, nil
}

func (row csvRow) seriesTime(field string) (string, time.Time, error) {
	name := row.get("variant")
	if name == "" {
		return "", time.Time{}, fmt.Errorf("variant 不能为空")
	}
	text := row.get(field)
	for _, layout := range []string{time.DateOnly, "2006-01-02 15:04", time.DateTime, time.RFC3339} {
		if at, err := time.ParseInLocation(layout, text, sourceTime); err == nil {
			return name, at.In(sourceTime), nil
		}
	}
	return "", time.Time{}, fmt.Errorf("%s 缺失或日期时间格式无效", field)
}

func readCSV(path string, required []string, visit func(csvRow) error) error {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer file.Close()

	name := filepath.Base(path)
	reader := csv.NewReader(file)
	headers, err := reader.Read()
	if err != nil {
		return fmt.Errorf("读取 %s 表头: %w", name, err)
	}
	columns := make(map[string]int, len(headers))
	for i, header := range headers {
		header = strings.TrimSpace(strings.TrimPrefix(header, "\uFEFF"))
		if _, exists := columns[header]; exists {
			return fmt.Errorf("%s 含有重复列 %s", name, header)
		}
		columns[header] = i
	}
	for _, field := range required {
		if _, exists := columns[field]; !exists {
			return fmt.Errorf("%s 缺少 %s 列", name, field)
		}
	}
	for {
		values, err := reader.Read()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("读取 %s: %w", name, err)
		}
		line, _ := reader.FieldPos(0)
		if err := visit(csvRow{columns: columns, values: values}); err != nil {
			return fmt.Errorf("%s 第 %d 行: %w", name, line, err)
		}
	}
}
