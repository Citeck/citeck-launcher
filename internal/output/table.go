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

// visibleLen returns the visible width of a string, excluding ANSI escape codes.
func visibleLen(s string) int {
	return DisplayWidth(ansiRE.ReplaceAllString(s, ""))
}

// DisplayWidth returns the terminal display width of a string: RUNES, not
// bytes, with East Asian Wide characters counted as the two columns they
// actually occupy. Byte length is wrong for every table whose cells or headers
// are translated — "Зависимость" is 11 columns and 22 bytes, so a byte-padded
// column is a table whose rows do not line up with their own header in five of
// the eight locales.
func DisplayWidth(s string) int {
	w := 0
	for _, r := range s {
		if isWideRune(r) {
			w += 2
		} else {
			w++
		}
	}
	return w
}

// isWideRune reports whether r is an East Asian Wide character (CJK
// ideographs, kana, Hangul, fullwidth forms), which a terminal renders in two
// columns.
func isWideRune(r rune) bool {
	return (r >= 0x1100 && r <= 0x115F) || // Hangul Jamo
		r == 0x2329 || r == 0x232A || // angle brackets
		(r >= 0x2E80 && r <= 0x303E) || // CJK Radicals, Kangxi, CJK Symbols
		(r >= 0x3040 && r <= 0x33BF) || // Hiragana, Katakana, Bopomofo, CJK Compat
		(r >= 0x3400 && r <= 0x4DBF) || // CJK Unified Ext A
		(r >= 0x4E00 && r <= 0xA4CF) || // CJK Unified, Yi
		(r >= 0xA960 && r <= 0xA97C) || // Hangul Jamo Extended-A
		(r >= 0xAC00 && r <= 0xD7A3) || // Hangul Syllables
		(r >= 0xF900 && r <= 0xFAFF) || // CJK Compat Ideographs
		(r >= 0xFE30 && r <= 0xFE6B) || // CJK Compat Forms
		(r >= 0xFF01 && r <= 0xFF60) || // Fullwidth Forms
		(r >= 0xFFE0 && r <= 0xFFE6) || // Fullwidth Signs
		(r >= 0x20000 && r <= 0x2FFFD) || // CJK Ext B-F
		(r >= 0x30000 && r <= 0x3FFFD) // CJK Ext G+
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
	// AnyEdited is true when at least one app carries a user config edit
	// (an ApplicationDef override or an edited mounted file). Callers use it
	// to print the "* config edited" legend under the table.
	AnyEdited bool
}

// EditedMarker is the glyph appended to an app's name in the table when the app
// has a user config edit (override patch or edited mounted file).
const EditedMarker = " *"

// appNameCell renders the indented app name plus a dim edited-marker when the
// app carries a user config edit, so `citeck status` visibly flags which apps
// diverge from their generated defaults.
func appNameCell(app api.AppDto) string {
	name := "  " + app.Name
	if app.Edited || app.EditedFilesCount > 0 {
		return name + Colorize(Dim, EditedMarker)
	}
	return name
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
				appNameCell(app),
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
				appNameCell(app),
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

	anyEdited := false
	for _, app := range apps {
		if app.Edited || app.EditedFilesCount > 0 {
			anyEdited = true
			break
		}
	}

	return AppTableResult{
		Table:     FormatTable(headers, rows, 0, statusMinWidth),
		Running:   running,
		Failed:    failed,
		Stopped:   stopped,
		Total:     total,
		AnyEdited: anyEdited,
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
