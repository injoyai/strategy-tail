package backtestreport

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"
)

var palette = [...]string{
	"#1769b0", "#bc4d33", "#167c63", "#7748a1", "#85562b", "#a7356e", "#417585", "#3f4d65",
}

func svgText(text string) string {
	var escaped strings.Builder
	_ = xml.EscapeText(&escaped, []byte(text))
	return escaped.String()
}

func render(svg *bytes.Buffer, chart Chart) error {
	if strings.TrimSpace(chart.Title) == "" || len(chart.Series) == 0 {
		return fmt.Errorf("图表需要标题和数据序列")
	}

	var points []Series
	var first, last time.Time
	minimum, maximum := math.Inf(1), math.Inf(-1)
	for _, series := range chart.Series {
		if strings.TrimSpace(series.Name) == "" || len(series.Points) == 0 {
			return fmt.Errorf("数据序列需要名称和观测值")
		}
		series.Points = slices.Clone(series.Points)
		slices.SortStableFunc(series.Points, func(a, b Point) int { return a.Time.Compare(b.Time) })
		for _, point := range series.Points {
			if point.Time.IsZero() || math.IsNaN(point.Value) || math.IsInf(point.Value, 0) {
				return fmt.Errorf("序列 %q 存在无效的时间或数值", series.Name)
			}
			minimum = math.Min(minimum, point.Value)
			maximum = math.Max(maximum, point.Value)
		}
		start, end := series.Points[0].Time, series.Points[len(series.Points)-1].Time
		if first.IsZero() || start.Before(first) {
			first = start
		}
		if last.IsZero() || end.After(last) {
			last = end
		}
		points = append(points, series)
	}
	if chart.ZeroBaseline {
		minimum, maximum = math.Min(0, minimum), math.Max(0, maximum)
	}
	span := maximum - minimum
	padding := span * 0.08
	if padding == 0 {
		padding = math.Max(0.1, math.Abs(maximum)*0.05)
	}
	minimum -= padding
	maximum += padding
	if math.IsInf(maximum-minimum, 0) || maximum <= minimum {
		return fmt.Errorf("数值范围无法绘制")
	}

	const width = 900
	const left, right = 92.0, 864.0
	top := float64(94 + 24*((len(points)-1)/2))
	bottom := top + 266
	height := int(bottom) + 54
	x := func(at time.Time) float64 {
		if first.Equal(last) {
			return (left + right) / 2
		}
		return left + (right-left)*float64(at.Sub(first))/float64(last.Sub(first))
	}
	y := func(value float64) float64 {
		return bottom - (bottom-top)*((value-minimum)/(maximum-minimum))
	}
	write := func(format string, args ...any) { fmt.Fprintf(svg, format, args...) }

	write(`<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" viewBox="0 0 %d %d" role="img" aria-labelledby="title">`, width, height, width, height)
	write("<title id=\"title\">%s</title>", svgText(chart.Title))
	write(`<rect width="100%%" height="100%%" fill="#fff"/><g font-family="system-ui,'PingFang SC','Microsoft YaHei',sans-serif" fill="#243448">`)
	write(`<text x="32" y="40" font-size="23" font-weight="650">%s</text>`, svgText(chart.Title))
	for index, series := range points {
		color := palette[index%len(palette)]
		lx := left + float64(index%2)*386
		ly := float64(67 + (index/2)*24)
		if series.Scatter {
			write(`<circle cx="%.1f" cy="%.1f" r="4.5" fill="%s"/>`, lx+10, ly-4, color)
		} else {
			write(`<path d="M%.1f %.1f h20" stroke="%s" stroke-width="2.5"%s/>`, lx, ly-4, color, dash(index))
		}
		write(`<text x="%.1f" y="%.1f" font-size="14"><title>%s</title>%s</text>`, lx+27, ly, svgText(series.Name), svgText(legendLabel(series.Name)))
	}
	if chart.Unit != "" {
		write(`<text x="%.1f" y="%.1f" text-anchor="end" font-size="13" fill="#53657a">%s</text>`, left-12, top-12, svgText(chart.Unit))
	}
	for _, value := range valueTicks(minimum, maximum) {
		ty := y(value)
		write(`<path d="M%.1f %.1f H%.1f" stroke="#dbe3eb"/>`, left, ty, right)
		write(`<text x="%.1f" y="%.1f" text-anchor="end" font-size="13" fill="#53657a">%.6g</text>`, left-12, ty+4, value)
	}
	if chart.ZeroBaseline {
		write(`<path d="M%.1f %.1f H%.1f" stroke="#91a0b0" stroke-dasharray="5 4"/>`, left, y(0), right)
	}
	write(`<path d="M%.1f %.1f V%.1f H%.1f" stroke="#a4b1bf" fill="none"/>`, left, top, bottom, right)
	writeTimeTicks(svg, first, last, left, right, bottom)

	for index, series := range points {
		color := palette[index%len(palette)]
		if !series.Scatter && len(series.Points) > 1 {
			write(`<polyline fill="none" stroke="%s" stroke-width="2.3" stroke-linejoin="round" stroke-linecap="round"%s points="`, color, dash(index))
			for _, point := range series.Points {
				write("%.2f,%.2f ", x(point.Time), y(point.Value))
			}
			write(`"/>`)
			continue
		}
		for _, point := range series.Points {
			detail := fmt.Sprintf("%s · %s · %.6g %s", series.Name, point.Time.Format("2006-01-02 15:04"), point.Value, chart.Unit)
			if point.Label != "" {
				detail += " · " + point.Label
			}
			write(`<circle cx="%.2f" cy="%.2f" r="4.6" fill="%s" stroke="#fff" stroke-width="1.1"><title>%s</title></circle>`, x(point.Time), y(point.Value), color, svgText(detail))
		}
	}
	write("</g></svg>\n")
	return nil
}

func dash(index int) string {
	if index%2 == 1 {
		return ` stroke-dasharray="7 4"`
	}
	return ""
}

func legendLabel(name string) string {
	width := 0
	for i, r := range name {
		width++
		if r > 127 {
			width++
		}
		if width > 43 {
			return name[:i] + "…"
		}
	}
	return name
}

func valueTicks(minimum, maximum float64) []float64 {
	step := (maximum - minimum) / 4
	unit := math.Pow10(int(math.Floor(math.Log10(step))))
	if unit == 0 || math.IsInf(unit, 0) {
		return []float64{minimum, maximum}
	}
	for _, multiple := range []float64{1, 2, 2.5, 5, 10} {
		if rounded := unit * multiple; rounded >= step && !math.IsInf(rounded, 0) {
			step = rounded
			break
		}
	}
	first := math.Ceil(minimum/step) * step
	ticks := make([]float64, 0, 5)
	for i := range 5 {
		value := first + float64(i)*step
		if value > maximum {
			break
		}
		ticks = append(ticks, value)
	}
	return ticks
}

func writeTimeTicks(svg *bytes.Buffer, first, last time.Time, left, right, bottom float64) {
	span := last.Sub(first)
	if span == 0 {
		fmt.Fprintf(svg, `<text x="%.1f" y="%.1f" text-anchor="middle" font-size="13" fill="#53657a">%s</text>`,
			(left+right)/2, bottom+24, first.Format("2006-01-02 15:04"))
		return
	}
	layout := time.DateOnly
	if span < 24*time.Hour {
		layout = "01-02 15:04"
	}
	if span < 4*time.Minute {
		layout = "15:04:05"
	}
	var previous string
	for tick := range 5 {
		fraction := float64(tick) / 4
		label := first.Add(time.Duration(float64(span) * fraction)).Format(layout)
		if label == previous {
			continue
		}
		previous = label
		fmt.Fprintf(svg, `<text x="%.1f" y="%.1f" text-anchor="middle" font-size="13" fill="#53657a">%s</text>`,
			left+(right-left)*fraction, bottom+24, label)
	}
}
