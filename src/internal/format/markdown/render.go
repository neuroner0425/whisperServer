package markdown

import (
	"fmt"
	"html"
	htmpl "html/template"
	"regexp"
	"strconv"
	"strings"
)

var (
	lineRe1 = regexp.MustCompile(`\[(\d{2}):(\d{2}):(\d{2}\.\d+)`)
	lineRe2 = regexp.MustCompile(`\[(\d{2}):(\d{2}):(\d{2})`)
)

func RenderResultText(content string, withTimeline bool, totalSec *int) htmpl.HTML {
	lines := strings.Split(content, "\n")
	htmlLines := make([]string, 0, len(lines))
	for _, line := range lines {
		if withTimeline && strings.Contains(line, "]") {
			parts := strings.SplitN(line, "]", 2)
			timeline := parts[0] + "]"
			body := ""
			if len(parts) > 1 {
				body = html.EscapeString(strings.TrimSpace(parts[1]))
			}
			safeTimeline := html.EscapeString(timeline)
			percent := 0
			if totalSec != nil && *totalSec > 0 {
				percent = int((parseStartSec(parts[0]) / float64(*totalSec)) * 100)
			}
			bar := ""
			pct := ""
			if totalSec != nil && *totalSec > 0 {
				bar = fmt.Sprintf(`<span style="display:inline-block;width:80px;height:8px;background:#eee;border-radius:4px;vertical-align:middle;margin-right:6px;overflow:hidden;"><span style="display:inline-block;height:8px;background:#2563eb;width:%d%%;border-radius:4px;"></span></span>`, percent)
				pct = fmt.Sprintf(`<span style="color:#888;font-size:0.95em;">(%d%%)</span>`, percent)
			}
			htmlLines = append(htmlLines, fmt.Sprintf(`<div style="margin-bottom:4px;">%s<span style="color:#2563eb;font-weight:bold;">%s</span> %s %s</div>`, bar, safeTimeline, body, pct))
			continue
		}
		if strings.TrimSpace(line) == "" {
			continue
		}
		htmlLines = append(htmlLines, "<div>"+html.EscapeString(strings.TrimSpace(line))+"</div>")
	}
	return htmpl.HTML(strings.Join(htmlLines, "\n"))
}

func RenderMarkdownText(content string) htmpl.HTML {
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	htmlLines := make([]string, 0, len(lines))
	inList := false
	inTable := false
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		switch {
		case strings.HasPrefix(line, "### "):
			htmlLines, inList, inTable = closeMarkdownBlocks(htmlLines, inList, inTable)
			htmlLines = append(htmlLines, "<h3>"+html.EscapeString(strings.TrimSpace(strings.TrimPrefix(line, "### ")))+"</h3>")
		case strings.HasPrefix(line, "## "):
			htmlLines, inList, inTable = closeMarkdownBlocks(htmlLines, inList, inTable)
			htmlLines = append(htmlLines, "<h2>"+html.EscapeString(strings.TrimSpace(strings.TrimPrefix(line, "## ")))+"</h2>")
		case strings.HasPrefix(line, "# "):
			htmlLines, inList, inTable = closeMarkdownBlocks(htmlLines, inList, inTable)
			htmlLines = append(htmlLines, "<h1>"+html.EscapeString(strings.TrimSpace(strings.TrimPrefix(line, "# ")))+"</h1>")
		case strings.HasPrefix(line, "- "):
			if inTable {
				htmlLines = append(htmlLines, "</table>")
				inTable = false
			}
			if !inList {
				htmlLines = append(htmlLines, "<ul>")
				inList = true
			}
			htmlLines = append(htmlLines, "<li>"+html.EscapeString(strings.TrimSpace(strings.TrimPrefix(line, "- ")))+"</li>")
		case strings.HasPrefix(line, "|") && strings.HasSuffix(line, "|"):
			if inList {
				htmlLines = append(htmlLines, "</ul>")
				inList = false
			}
			if isMarkdownDividerRow(line) {
				continue
			}
			if !inTable {
				htmlLines = append(htmlLines, `<table class="document-table">`)
				inTable = true
			}
			htmlLines = append(htmlLines, renderMarkdownTableRow(line))
		case line == "---":
			htmlLines, inList, inTable = closeMarkdownBlocks(htmlLines, inList, inTable)
			htmlLines = append(htmlLines, "<hr>")
		case line == "":
			htmlLines, inList, inTable = closeMarkdownBlocks(htmlLines, inList, inTable)
		default:
			htmlLines, inList, inTable = closeMarkdownBlocks(htmlLines, inList, inTable)
			htmlLines = append(htmlLines, "<p>"+html.EscapeString(line)+"</p>")
		}
	}
	if inList {
		htmlLines = append(htmlLines, "</ul>")
	}
	if inTable {
		htmlLines = append(htmlLines, "</table>")
	}
	return htmpl.HTML(strings.Join(htmlLines, "\n"))
}

func closeMarkdownBlocks(htmlLines []string, inList, inTable bool) ([]string, bool, bool) {
	if inList {
		htmlLines = append(htmlLines, "</ul>")
	}
	if inTable {
		htmlLines = append(htmlLines, "</table>")
	}
	return htmlLines, false, false
}

func isMarkdownDividerRow(line string) bool {
	parts := splitMarkdownTableLine(line)
	if len(parts) == 0 {
		return false
	}
	for _, part := range parts {
		part = strings.TrimSpace(part)
		part = strings.Trim(part, ":")
		if part == "" || strings.Trim(part, "-") != "" {
			return false
		}
	}
	return true
}

func renderMarkdownTableRow(line string) string {
	parts := splitMarkdownTableLine(line)
	cells := make([]string, 0, len(parts))
	for _, part := range parts {
		cells = append(cells, "<td>"+html.EscapeString(strings.TrimSpace(part))+"</td>")
	}
	return "<tr>" + strings.Join(cells, "") + "</tr>"
}

func splitMarkdownTableLine(line string) []string {
	line = strings.TrimPrefix(line, "|")
	line = strings.TrimSuffix(line, "|")
	rawParts := strings.Split(line, "|")
	parts := make([]string, 0, len(rawParts))
	for _, part := range rawParts {
		parts = append(parts, strings.TrimSpace(part))
	}
	return parts
}

func parseStartSec(timeline string) float64 {
	if m := lineRe1.FindStringSubmatch(timeline); len(m) == 4 {
		h, _ := strconv.ParseFloat(m[1], 64)
		mm, _ := strconv.ParseFloat(m[2], 64)
		ss, _ := strconv.ParseFloat(m[3], 64)
		return h*3600 + mm*60 + ss
	}
	if m := lineRe2.FindStringSubmatch(timeline); len(m) == 4 {
		h, _ := strconv.ParseFloat(m[1], 64)
		mm, _ := strconv.ParseFloat(m[2], 64)
		ss, _ := strconv.ParseFloat(m[3], 64)
		return h*3600 + mm*60 + ss
	}
	return 0
}
