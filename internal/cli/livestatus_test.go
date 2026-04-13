package cli

import "testing"

func TestIsAppTerminalFailed(t *testing.T) {
	cases := map[string]bool{
		"RUNNING":         false,
		"STARTING":        false,
		"PULLING":         false,
		"READY_TO_PULL":   false,
		"READY_TO_START":  false,
		"DEPS_WAITING":    false,
		"STOPPED":         false,
		"STOPPING":        false,
		"":                false,
		"START_FAILED":    true,
		"PULL_FAILED":     true,
		"FAILED":          true,
		"STOPPING_FAILED": true,
	}
	for status, want := range cases {
		if got := isAppTerminalFailed(status); got != want {
			t.Errorf("isAppTerminalFailed(%q) = %v, want %v", status, got, want)
		}
	}
}
