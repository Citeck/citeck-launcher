package output

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestFormatTable_BasicRendering(t *testing.T) {
	headers := []string{"APP", "STATUS", "IMAGE"}
	rows := [][]string{
		{"proxy", "RUNNING", "ecos-proxy:2.25"},
		{"gateway", "RUNNING", "ecos-gateway:3.3.0"},
	}

	result := FormatTable(headers, rows)

	if !strings.Contains(result, "APP") {
		t.Error("expected header APP in output")
	}
	if !strings.Contains(result, "proxy") {
		t.Error("expected proxy in output")
	}
	if !strings.Contains(result, "ecos-gateway:3.3.0") {
		t.Error("expected full image name in output")
	}
}

func TestFormatTable_ColumnWidths(t *testing.T) {
	headers := []string{"A", "B"}
	rows := [][]string{
		{"short", "x"},
		{"a-much-longer-value", "y"},
	}

	result := FormatTable(headers, rows)
	lines := strings.Split(result, "\n")

	// All lines should have same column alignment
	if len(lines) < 3 {
		t.Fatalf("expected 3 lines, got %d", len(lines))
	}

	// Header column "A" should be padded to width of "a-much-longer-value"
	headerLine := lines[0]
	if !strings.HasPrefix(headerLine, "A") {
		t.Error("header should start with A")
	}
}

func TestFormatTable_EmptyHeaders(t *testing.T) {
	result := FormatTable(nil, nil)
	if result != "" {
		t.Error("expected empty string for nil headers")
	}
}

func TestFormatTable_EmptyRows(t *testing.T) {
	headers := []string{"APP", "STATUS"}
	result := FormatTable(headers, nil)
	if !strings.Contains(result, "APP") {
		t.Error("expected headers even with no rows")
	}
}

func TestFormatKeyValue(t *testing.T) {
	pairs := [][2]string{
		{"Name", "production"},
		{"Status", "RUNNING"},
	}
	result := FormatKeyValue(pairs)
	if !strings.Contains(result, "Name:") {
		t.Error("expected Name: in output")
	}
	if !strings.Contains(result, "production") {
		t.Error("expected production in output")
	}
}

func TestPrintJSON_ValidJSON(t *testing.T) {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	PrintJSON(map[string]string{"key": "value"})

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	// Verify it's valid JSON
	var parsed map[string]string
	if err := json.Unmarshal([]byte(output), &parsed); err != nil {
		t.Fatalf("output is not valid JSON: %v\nOutput: %s", err, output)
	}
	if parsed["key"] != "value" {
		t.Errorf("expected key=value, got key=%s", parsed["key"])
	}
}

func TestPrintJSON_NoANSI(t *testing.T) {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	SetFormat(FormatJSON)
	PrintJSON(map[string]string{"status": "RUNNING"})
	SetFormat(FormatText) // restore

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	if strings.Contains(output, "\033[") {
		t.Error("JSON output should not contain ANSI escape codes")
	}
}

func TestFormatJSON_EmptyData(t *testing.T) {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	PrintJSON(map[string]any{})

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := strings.TrimSpace(buf.String())

	if output != "{}" {
		t.Errorf("expected {}, got %s", output)
	}
}

func TestSetFormat_JSONDisablesColors(t *testing.T) {
	SetColorsEnabled(true)
	SetFormat(FormatJSON)

	result := Colorize(Green, "test")
	if strings.Contains(result, "\033[") {
		t.Error("colors should be disabled in JSON mode")
	}

	// Restore
	SetFormat(FormatText)
	SetColorsEnabled(true)
}
