package output

import (
	"fmt"
	"strings"
)

// FormatTable formats headers and rows into an aligned text table.
func FormatTable(headers []string, rows [][]string) string {
	if len(headers) == 0 {
		return ""
	}

	widths := make([]int, len(headers))
	for i, h := range headers {
		widths[i] = len(h)
	}
	for _, row := range rows {
		for i := 0; i < len(row) && i < len(widths); i++ {
			if len(row[i]) > widths[i] {
				widths[i] = len(row[i])
			}
		}
	}

	var sb strings.Builder

	// Header
	for i, h := range headers {
		if i > 0 {
			sb.WriteString("  ")
		}
		sb.WriteString(pad(h, widths[i]))
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
			sb.WriteString(pad(cell, widths[i]))
		}
		sb.WriteString("\n")
	}

	return strings.TrimRight(sb.String(), "\n")
}

func pad(s string, width int) string {
	if len(s) >= width {
		return s
	}
	return s + strings.Repeat(" ", width-len(s))
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
