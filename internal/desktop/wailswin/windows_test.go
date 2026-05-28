//go:build (linux && gtk3) || darwin || windows

package wailswin

import (
	"testing"
)

// WindowManager interacts with Wails so its full handler can't be exercised
// without a running Wails application. These tests cover the pure helpers
// (key, defaults, capitalisation) that drive the routing decisions.

func TestWindowKey(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		kind, id  string
		expected  string
	}{
		{"empty id collapses to kind", "logs", "", "logs"},
		{"non-empty id is separated by pipe", "logs", "eapps", "logs|eapps"},
		{"editor app/file id keeps slashes", "editor", "eapps/app-def.yml", "editor|eapps/app-def.yml"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := windowKey(tc.kind, tc.id)
			if got != tc.expected {
				t.Fatalf("windowKey(%q, %q) = %q, want %q", tc.kind, tc.id, got, tc.expected)
			}
		})
	}
}

func TestDefaultRoute(t *testing.T) {
	t.Parallel()
	cases := []struct {
		kind, id, expected string
	}{
		{"logs", "eapps", "/window/logs/eapps"},
		{"daemon-logs", "", "/window/daemon-logs"},
		{"editor", "emodel", "/window/editor/emodel"},
	}
	for _, tc := range cases {
		got := defaultRoute(tc.kind, tc.id)
		if got != tc.expected {
			t.Fatalf("defaultRoute(%q, %q) = %q, want %q", tc.kind, tc.id, got, tc.expected)
		}
	}
}

func TestDefaultTitle(t *testing.T) {
	t.Parallel()
	cases := []struct {
		kind, id, expected string
	}{
		{"logs", "eapps", "Logs — eapps"},
		{"logs", "", "Logs"},
		{"editor", "emodel", "Editor — emodel"},
		{"editor", "", "Editor"},
		{"daemon-logs", "", "Launcher Logs"},
		{"custom", "x", "Custom — x"}, // fallback path: capitalise + id
		{"", "", ""},
	}
	for _, tc := range cases {
		got := defaultTitle(tc.kind, tc.id)
		if got != tc.expected {
			t.Fatalf("defaultTitle(%q, %q) = %q, want %q", tc.kind, tc.id, got, tc.expected)
		}
	}
}

func TestDefaultDimensions(t *testing.T) {
	t.Parallel()
	if w := defaultWidth("logs"); w != 1100 {
		t.Fatalf("logs width: got %d", w)
	}
	if w := defaultWidth("editor"); w != 1200 {
		t.Fatalf("editor width: got %d", w)
	}
	if w := defaultWidth("unknown"); w != 900 {
		t.Fatalf("fallback width: got %d", w)
	}
	if h := defaultHeight("logs"); h != 750 {
		t.Fatalf("logs height: got %d", h)
	}
}

func TestCapitalize(t *testing.T) {
	t.Parallel()
	cases := []struct{ in, out string }{
		{"", ""},
		{"a", "A"},
		{"editor", "Editor"},
		{"daemon-logs", "Daemon-logs"},
	}
	for _, tc := range cases {
		if got := capitalize(tc.in); got != tc.out {
			t.Fatalf("capitalize(%q) = %q, want %q", tc.in, got, tc.out)
		}
	}
}
