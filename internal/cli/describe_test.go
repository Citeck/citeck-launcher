package cli

import (
	"testing"

	"github.com/citeck/citeck-launcher/internal/api"
)

func TestFormatUptime(t *testing.T) {
	tests := []struct {
		name string
		ms   int64
		want string
	}{
		{"zero", 0, "—"},
		{"negative", -1, "—"},
		{"seconds", 45_000, "45s"},
		{"minutes+seconds", 125_000, "2m 5s"},
		{"hours+minutes+seconds", 3_725_000, "1h 2m 5s"},
		{"days+hours+minutes", 90_060_000, "1d 1h 1m"},
		{"exact hour", 3_600_000, "1h 0m 0s"},
		{"exact day", 86_400_000, "1d 0h 0m"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatUptime(tt.ms)
			if got != tt.want {
				t.Errorf("formatUptime(%d) = %q, want %q", tt.ms, got, tt.want)
			}
		})
	}
}

func TestMaskSecretEnv(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"DB_PASSWORD=hunter2", "DB_PASSWORD=***"},
		{"JWT_SECRET=abc123", "JWT_SECRET=***"},
		{"API_TOKEN=xyz", "API_TOKEN=***"},
		{"TLS_KEY=private", "TLS_KEY=***"},
		{"db_password=lower", "db_password=***"}, // case-insensitive
		{"NORMAL_VAR=visible", "NORMAL_VAR=visible"},
		{"MONKEY=banana", "MONKEY=banana"}, // KEY suffix, not _KEY
		{"PATH=/usr/bin", "PATH=/usr/bin"},
		{"no-equals", "no-equals"},
		{"EMPTY_PASSWORD=", "EMPTY_PASSWORD=***"},
	}
	for _, tt := range tests {
		got := api.MaskSecretEnv(tt.input)
		if got != tt.want {
			t.Errorf("maskSecretEnv(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
