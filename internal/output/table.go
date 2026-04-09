package output

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/citeck/citeck-launcher/internal/api"
)

// ansiRE matches ANSI escape sequences for stripping when calculating visible width.
var ansiRE = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// FormatTable formats headers and rows into an aligned text table.
// Handles ANSI color codes in cell values — alignment is based on visible width.
func FormatTable(headers []string, rows [][]string) string {
	if len(headers) == 0 {
		return ""
	}

	widths := make([]int, len(headers))
	for i, h := range headers {
		widths[i] = visibleLen(h)
	}
	for _, row := range rows {
		for i := 0; i < len(row) && i < len(widths); i++ {
			if vl := visibleLen(row[i]); vl > widths[i] {
				widths[i] = vl
			}
		}
	}

	var sb strings.Builder

	// Header
	for i, h := range headers {
		if i > 0 {
			sb.WriteString("  ")
		}
		sb.WriteString(padVisible(h, widths[i]))
	}
	sb.WriteString("\n")

	// Rows
	for _, row := range rows {
		for i := range len(headers) {
			if i > 0 {
				sb.WriteString("  ")
			}
			cell := ""
			if i < len(row) {
				cell = row[i]
			}
			sb.WriteString(padVisible(cell, widths[i]))
		}
		sb.WriteString("\n")
	}

	return strings.TrimRight(sb.String(), "\n")
}

// visibleLen returns the visible length of a string, excluding ANSI escape codes.
func visibleLen(s string) int {
	return len(ansiRE.ReplaceAllString(s, ""))
}

// padVisible pads a string to the given visible width, accounting for ANSI codes.
func padVisible(s string, width int) string {
	vl := visibleLen(s)
	if vl >= width {
		return s
	}
	return s + strings.Repeat(" ", width-vl)
}

// AppTableResult holds the formatted table and app counts.
type AppTableResult struct {
	Table   string
	Running int
	Failed  int
	Total   int
}

// FormatAppTable formats a list of apps into a sorted, aligned table with status counts.
// This is the single source of truth for app table rendering — used by status, reload, setup, start.
func FormatAppTable(apps []api.AppDto) AppTableResult {
	total := len(apps)
	var running, failed int

	sort.Slice(apps, func(i, j int) bool { return apps[i].Name < apps[j].Name })

	headers := []string{"APP", "STATUS", "IMAGE", "CPU", "MEMORY"}
	rows := make([][]string, 0, len(apps))

	for _, app := range apps {
		switch app.Status {
		case "RUNNING":
			running++
		case "START_FAILED", "PULL_FAILED", "FAILED":
			failed++
		}

		rows = append(rows, []string{
			app.Name,
			ColorizeStatus(app.Status),
			app.Image,
			app.CPU,
			app.Memory,
		})
	}

	return AppTableResult{
		Table:   FormatTable(headers, rows),
		Running: running,
		Failed:  failed,
		Total:   total,
	}
}

// FormatKeyValue formats key-value pairs into an aligned two-column layout.
func FormatKeyValue(pairs [][2]string) string {
	maxKey := 0
	for _, p := range pairs {
		if len(p[0]) > maxKey {
			maxKey = len(p[0])
		}
	}

	var sb strings.Builder
	for _, p := range pairs {
		fmt.Fprintf(&sb, "%-*s  %s\n", maxKey, p[0]+":", p[1])
	}
	return strings.TrimRight(sb.String(), "\n")
}
