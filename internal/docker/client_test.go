package docker

import "testing"

func TestParseMemory(t *testing.T) {
	tests := []struct {
		input string
		want  int64
	}{
		{"128m", 128 * 1024 * 1024},
		{"1g", 1024 * 1024 * 1024},
		{"512k", 512 * 1024},
		{"256M", 256 * 1024 * 1024},
		{"3G", 3 * 1024 * 1024 * 1024},
		{"", 0},
		{"64m", 64 * 1024 * 1024},
	}
	for _, tt := range tests {
		got := parseMemory(tt.input)
		if got != tt.want {
			t.Errorf("parseMemory(%q) = %d, want %d", tt.input, got, tt.want)
		}
	}
}

func TestStripDockerLogHeaders(t *testing.T) {
	// Docker log headers are 8 bytes
	input := "\x01\x00\x00\x00\x00\x00\x00\x05Hello\n\x01\x00\x00\x00\x00\x00\x00\x05World"
	result := stripDockerLogHeaders(input)
	if result != "Hello\nWorld" {
		t.Errorf("unexpected result: %q", result)
	}
}
