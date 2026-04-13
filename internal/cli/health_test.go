package cli

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/output"
)

// B7-03: the banner the user sees must match the exit code. Parameterized
// over every exit code healthBanner currently cares about so a future
// divergence fails loudly.
func TestHealthBanner_MatchesExitCode(t *testing.T) {
	tests := []struct {
		name      string
		exitCode  int
		wantLabel string
		wantColor string
	}{
		{"healthy/0", ExitOK, "HEALTHY", output.Green},
		{"daemon down/1", ExitError, "DAEMON DOWN", output.Red},
		{"daemon not running/3", ExitDaemonNotRunning, "DAEMON DOWN", output.Red},
		{"unhealthy/8", ExitUnhealthy, "UNHEALTHY", output.Red},
		{"unknown code falls back to unhealthy", 99, "UNHEALTHY", output.Red},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			label, color := healthBanner(tt.exitCode)
			if label != tt.wantLabel {
				t.Errorf("label = %q, want %q", label, tt.wantLabel)
			}
			if color != tt.wantColor {
				t.Errorf("color = %q, want %q", color, tt.wantColor)
			}
		})
	}
}

// B7-03: render path — feeding a healthy DTO with exit=0 must produce a
// HEALTHY banner and never the word UNHEALTHY. Feeding a non-zero exit
// must produce the matching label.
func TestRenderHealth_BannerMatchesExit(t *testing.T) {
	prev := output.GetFormat()
	output.SetFormat(output.FormatText)
	output.SetColorsEnabled(false)
	t.Cleanup(func() { output.SetFormat(prev) })

	tests := []struct {
		name     string
		health   *api.HealthDto
		exitCode int
		want     string
		notWant  string
	}{
		{
			name:     "healthy",
			health:   &api.HealthDto{Status: "healthy", Healthy: true},
			exitCode: ExitOK,
			want:     "HEALTHY",
			notWant:  "UNHEALTHY",
		},
		{
			name:     "unhealthy",
			health:   &api.HealthDto{Status: "unhealthy", Healthy: false},
			exitCode: ExitUnhealthy,
			want:     "UNHEALTHY",
			notWant:  " HEALTHY",
		},
		{
			name:     "daemon down",
			health:   nil,
			exitCode: ExitDaemonNotRunning,
			want:     "DAEMON DOWN",
			notWant:  "HEALTHY",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := captureStdout(t, func() {
				renderHealth(tt.health, tt.exitCode)
			})
			if !strings.Contains(out, tt.want) {
				t.Errorf("output missing %q:\n%s", tt.want, out)
			}
			if tt.notWant != "" && strings.Contains(out, tt.notWant) {
				t.Errorf("output should not contain %q:\n%s", tt.notWant, out)
			}
		})
	}
}

// captureStdout runs fn and returns everything it wrote to os.Stdout.
// Used for CLI render tests without pulling in a heavier harness.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	origStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	done := make(chan struct{})
	var buf bytes.Buffer
	go func() {
		_, _ = io.Copy(&buf, r)
		close(done)
	}()
	func() {
		defer func() {
			_ = w.Close()
			os.Stdout = origStdout
		}()
		fn()
	}()
	<-done
	return buf.String()
}
