package backtestreport

import (
	"fmt"
	"html"
	"html/template"
	"net/url"
	"regexp"
	"strings"
)

const (
	chartStart = "<!-- backtestreport:charts:start -->"
	chartEnd   = "<!-- backtestreport:charts:end -->"
)

var (
	legacyFigure = regexp.MustCompile(`^!\[(.*)\]\(backtest-chart-[a-f0-9]+\.svg\)$`)
	headingLine  = regexp.MustCompile(`^(#{1,6})[ \t]+(.+)$`)
	listLine     = regexp.MustCompile(`^([-+*]|[0-9]+\.)[ \t]+(.+)$`)
	tableRule    = regexp.MustCompile(`^:?-{3,}:?$`)
	fenceLine    = regexp.MustCompile("^(`{3,}|~{3,})(.*)$")
	inlineToken  = regexp.MustCompile("\x60[^\x60]+\x60" + `|\*\*[^*]+\*\*|!?\[(?:\\.|[^\\\]])+\]\([^()\s]+\)|\*[^*]+\*`)
)

func stripLegacyCharts(markdown string, rebuilt map[string]bool) (string, string, error) {
	start, end := strings.Index(markdown, chartStart), strings.Index(markdown, chartEnd)
	if start < 0 && end < 0 {
		return markdown, "", nil
	}
	if start < 0 || end < start || strings.Count(markdown, chartStart) != 1 || strings.Count(markdown, chartEnd) != 1 {
		return "", "", fmt.Errorf("报告中的图表区段标记不完整")
	}
	var legacy []string
	for _, line := range strings.Split(markdown[start+len(chartStart):end], "\n") {
		text := strings.TrimSpace(line)
		if text == "## 回测图表" || text == "本次报告没有可绘制的权益、平仓成交或信号数据。" {
			continue
		}
		if figure := legacyFigure.FindStringSubmatch(text); figure != nil {
			name := strings.NewReplacer(`\[`, "[", `\]`, "]", `\\`, `\`).Replace(html.UnescapeString(figure[1]))
			if rebuilt[name] {
				continue
			}
		}
		legacy = append(legacy, line)
	}
	return markdown[:start] + "\n\n" + markdown[end+len(chartEnd):], strings.Join(legacy, "\n"), nil
}

func reportMarkdown(markdown string) (string, template.HTML, template.HTML, template.HTML) {
	markdown = strings.TrimSpace(strings.ReplaceAll(markdown, "\r\n", "\n"))
	lines := strings.Split(markdown, "\n")
	title := "回测报告"
	if match := headingLine.FindStringSubmatch(lines[0]); match != nil && match[1] == "#" {
		title, lines = match[2], lines[1:]
	}
	fence := ""
	for index, line := range lines {
		line = strings.TrimSpace(line)
		if fence == "" && strings.HasPrefix(line, "## ") {
			return title, template.HTML(markdownInline(title)), markdownBlocks(lines[:index], 2), markdownBlocks(lines[index:], 2)
		}
		if fence == "" {
			if match := fenceLine.FindStringSubmatch(line); match != nil {
				fence = match[1]
			}
		} else if closesFence(line, fence) {
			fence = ""
		}
	}
	return title, template.HTML(markdownInline(title)), markdownBlocks(lines, 2), ""
}

// markdownBlocks formats the report's Markdown conventions. Raw HTML is text.
func markdownBlocks(lines []string, minHeading int) template.HTML {
	var out strings.Builder
	for i := 0; i < len(lines); {
		line := strings.TrimSpace(lines[i])
		i++
		if line == "" {
			continue
		}
		if match := fenceLine.FindStringSubmatch(line); match != nil {
			start := i
			for i < len(lines) && !closesFence(strings.TrimSpace(lines[i]), match[1]) {
				i++
			}
			fmt.Fprintf(&out, "<pre><code>%s</code></pre>\n", html.EscapeString(strings.Join(lines[start:i], "\n")))
			if i < len(lines) {
				i++
			}
			continue
		}
		if match := headingLine.FindStringSubmatch(line); match != nil {
			level := max(len(match[1]), minHeading)
			fmt.Fprintf(&out, "<h%d>%s</h%d>\n", level, markdownInline(match[2]), level)
			continue
		}
		if i < len(lines) && isTable(line, lines[i]) {
			header, alignment := tableCells(line), tableCells(lines[i])
			out.WriteString(`<div class="table-scroll" tabindex="0" role="region" aria-label="报告数据表"><table><thead>`)
			tableRow(&out, "th", header, alignment)
			out.WriteString("</thead><tbody>")
			i++
			for i < len(lines) && strings.Contains(lines[i], "|") {
				tableRow(&out, "td", tableCells(lines[i]), alignment)
				i++
			}
			out.WriteString("</tbody></table></div>\n")
			continue
		}
		if match := listLine.FindStringSubmatch(line); match != nil {
			tag := "ul"
			ordered := strings.HasSuffix(match[1], ".")
			if ordered {
				tag = "ol"
			}
			fmt.Fprintf(&out, "<%s>", tag)
			for {
				fmt.Fprintf(&out, "<li>%s</li>", markdownInline(match[2]))
				if i == len(lines) {
					break
				}
				match = listLine.FindStringSubmatch(strings.TrimSpace(lines[i]))
				if match == nil || strings.HasSuffix(match[1], ".") != ordered {
					break
				}
				i++
			}
			fmt.Fprintf(&out, "</%s>\n", tag)
			continue
		}
		out.WriteString("<p>" + markdownInline(line))
		for i < len(lines) {
			next := strings.TrimSpace(lines[i])
			if next == "" || headingLine.MatchString(next) || listLine.MatchString(next) || fenceLine.MatchString(next) {
				break
			}
			out.WriteString(" " + markdownInline(next))
			i++
		}
		out.WriteString("</p>\n")
	}
	return template.HTML(out.String())
}

func closesFence(line, fence string) bool {
	return strings.HasPrefix(line, fence) && strings.Trim(line, fence[:1]) == ""
}

func tableCells(line string) []string {
	return strings.Split(strings.Trim(strings.TrimSpace(line), "|"), "|")
}

func isTable(header, rule string) bool {
	if !strings.Contains(header, "|") || !strings.Contains(rule, "|") {
		return false
	}
	cells := tableCells(rule)
	if len(tableCells(header)) != len(cells) {
		return false
	}
	for _, cell := range cells {
		if !tableRule.MatchString(strings.TrimSpace(cell)) {
			return false
		}
	}
	return true
}

func tableRow(out *strings.Builder, tag string, cells, alignment []string) {
	out.WriteString("<tr>")
	for i, rule := range alignment {
		text := ""
		if i < len(cells) {
			text = markdownInline(strings.TrimSpace(cells[i]))
		}
		attribute := ""
		if strings.HasSuffix(strings.TrimSpace(rule), ":") {
			attribute = ` class="numeric"`
		}
		fmt.Fprintf(out, "<%s%s>%s</%s>", tag, attribute, text, tag)
	}
	out.WriteString("</tr>")
}

func markdownInline(text string) string {
	var out strings.Builder
	start := 0
	for _, match := range inlineToken.FindAllStringIndex(text, -1) {
		out.WriteString(html.EscapeString(text[start:match[0]]))
		token := text[match[0]:match[1]]
		start = match[1]
		switch {
		case token[0] == '`':
			out.WriteString("<code>" + html.EscapeString(token[1:len(token)-1]) + "</code>")
		case strings.HasPrefix(token, "**"):
			out.WriteString("<strong>" + html.EscapeString(token[2:len(token)-2]) + "</strong>")
		case token[0] == '*':
			out.WriteString("<em>" + html.EscapeString(token[1:len(token)-1]) + "</em>")
		default:
			image := token[0] == '!'
			at := strings.LastIndex(token, "](")
			label := html.EscapeString(markdownLabel(strings.TrimPrefix(strings.TrimPrefix(token[:at], "!"), "[")))
			target := token[at+2 : len(token)-1]
			location, err := url.Parse(target)
			if err != nil || (location.Scheme != "" && location.Scheme != "https" && location.Scheme != "http") {
				out.WriteString(label)
			} else if image {
				fmt.Fprintf(&out, `<img src="%s" alt="%s" loading="lazy">`, html.EscapeString(location.String()), label)
			} else {
				fmt.Fprintf(&out, `<a href="%s">%s</a>`, html.EscapeString(location.String()), label)
			}
		}
	}
	out.WriteString(html.EscapeString(text[start:]))
	return out.String()
}

func markdownLabel(text string) string {
	return strings.NewReplacer(`\[`, "[", `\]`, "]", `\`, `\`).Replace(html.UnescapeString(text))
}
