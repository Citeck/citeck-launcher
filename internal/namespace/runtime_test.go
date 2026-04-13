package namespace

import (
	"testing"
)

func TestFormatBytes(t *testing.T) {
	tests := []struct {
		bytes int64
		want  string
	}{
		{0, "0K"},
		{512, "0K"},
		{1024 * 1024, "1M"},
		{512 * 1024 * 1024, "512M"},
		{1024 * 1024 * 1024, "1.0G"},
		{3 * 1024 * 1024 * 1024, "3.0G"},
	}
	for _, tt := range tests {
		got := formatBytes(tt.bytes)
		if got != tt.want {
			t.Errorf("formatBytes(%d) = %s, want %s", tt.bytes, got, tt.want)
		}
	}
}
