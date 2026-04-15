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
// Optional minWidths sets a floor per column (e.g. to prevent the STATUS
// column from jumping around as statuses change during live rendering).
func FormatTable(headers []string, rows [][]string, minWidths ...int) string {
	if len(headers) == 0 {
		return ""
	}

	widths := make([]int, len(headers))
	for i, h := range headers {
		widths[i] = visibleLen(h)
	}
	// Apply minimum widths floor.
	for i := 0; i < len(minWidths) && i < len(widths); i++ {
		if minWidths[i] > widths[i] {
			widths[i] = minWidths[i]
		}
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
//
// Stopped counts apps that the user intentionally detached (STOPPED status).
// Live-status waiters need this as a separate bucket from Failed so the
// terminal-state check `running + failed + stopped == total` succeeds on
// namespaces with detached services — otherwise the loop hangs forever.
// STOPPING_FAILED is a failure (the stop action itself errored, not a
// user-requested detach) and goes into Failed alongside START_FAILED /
// PULL_FAILED / FAILED — consistent with isAppTerminalFailed and the
// red colorization in ColorizeStatus.
type AppTableResult struct {
	Table   string
	Running int
	Failed  int
	Stopped int
	Total   int
}

// kindOrder defines the display order for app groups — matches the Kotlin
// launcher's table layout (Core → Extensions → Additional → Third Party).
var kindOrder = []struct {
	key   string
	label string
}{
	{"CITECK_CORE", "Citeck Core"},
	{"CITECK_CORE_EXTENSION", "Citeck Core Extensions"},
	{"CITECK_ADDITIONAL", "Citeck Additional"},
	{"THIRD_PARTY", "Third Party"},
}

// FormatAppTable formats a list of apps into a grouped, aligned table with
// status counts. Apps are grouped by Kind (Citeck Core / Extensions /
// Additional / Third Party) with a bold group header between sections,
// matching the Kotlin launcher's table layout.
// This is the single source of truth for app table rendering — used by
// status, reload, setup, start.
func FormatAppTable(apps []api.AppDto) AppTableResult {
	total := len(apps)
	var running, failed, stopped int

	for _, app := range apps {
		switch app.Status {
		case "RUNNING":
			running++
		case "START_FAILED", "PULL_FAILED", "FAILED", "STOPPING_FAILED":
			failed++
		case "STOPPED":
			stopped++
		}
	}

	// Group apps by kind, sort alphabetically within each group.
	groups := make(map[string][]api.AppDto, len(kindOrder))
	for _, app := range apps {
		k := app.Kind
		if k == "" {
			k = "THIRD_PARTY"
		}
		groups[k] = append(groups[k], app)
	}
	for k := range groups {
		sort.Slice(groups[k], func(i, j int) bool { return groups[k][i].Name < groups[k][j].Name })
	}

	// Build a single table with group headers inserted as separator rows.
	// The header row is printed once; group labels appear as bold full-width
	// rows between sections (no column separators).
	headers := []string{"APP", "STATUS", "IMAGE", "CPU", "MEMORY"}
	var rows [][]string

	for _, g := range kindOrder {
		groupApps := groups[g.key]
		if len(groupApps) == 0 {
			continue
		}
		// Group header: bold label in the first column, rest empty.
		rows = append(rows, []string{Colorize(Bold, g.label), "", "", "", ""})
		for _, app := range groupApps {
			rows = append(rows, []string{
				"  " + app.Name,
				ColorizeStatus(app.Status),
				app.Image,
				app.CPU,
				app.Memory,
			})
		}
	}

	// Apps with unknown kinds (shouldn't happen, but defensive).
	knownKinds := make(map[string]bool, len(kindOrder))
	for _, g := range kindOrder {
		knownKinds[g.key] = true
	}
	var unknownApps []api.AppDto
	for _, app := range apps {
		k := app.Kind
		if k == "" {
			k = "THIRD_PARTY"
		}
		if !knownKinds[k] {
			unknownApps = append(unknownApps, app)
		}
	}
	if len(unknownApps) > 0 {
		sort.Slice(unknownApps, func(i, j int) bool { return unknownApps[i].Name < unknownApps[j].Name })
		rows = append(rows, []string{Colorize(Bold, "Other"), "", "", "", ""})
		for _, app := range unknownApps {
			rows = append(rows, []string{
				"  " + app.Name,
				ColorizeStatus(app.Status),
				app.Image,
				app.CPU,
				app.Memory,
			})
		}
	}

	// STATUS column (index 1) gets a fixed minimum width so the table
	// doesn't jump horizontally during live rendering as statuses change
	// (e.g. STARTING → RUNNING → STOPPING_FAILED). 15 = len("STOPPING_FAILED").
	const statusMinWidth = 15

	return AppTableResult{
		Table:   FormatTable(headers, rows, 0, statusMinWidth),
		Running: running,
		Failed:  failed,
		Stopped: stopped,
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
