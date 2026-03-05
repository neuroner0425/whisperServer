package app

import (
	"fmt"
	"html"
	htmpl "html/template"
	"strconv"
	"strings"
)

func renderResultText(content string, withTimeline bool, totalSec *int) htmpl.HTML {
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
		htmlLines = append(htmlLines, html.EscapeString(strings.TrimSpace(line)))
	}
	return htmpl.HTML(strings.Join(htmlLines, "\n"))
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
