package wailswin

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestMacDesktopBuildsKeepPrivateMacAPIs: without -tags private_mac_apis
// Wails 3.0.0 compiles OpenDevTools to a no-op on macOS, so the tray DevTools
// item and F12 stop working with no error anywhere — and devtools_darwin.go,
// which they also need, drops out of the build. Both macOS build paths must
// keep the tag: the Makefile (build-desktop, dev-desktop) and the release
// script. No build tag on purpose: this runs in CI on Linux.
func TestMacDesktopBuildsKeepPrivateMacAPIs(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	cases := []struct {
		file string
		re   *regexp.Regexp
	}{
		{"Makefile", regexp.MustCompile(`(?m)^DESKTOP_TAGS\s*:=\s*(\S+)`)},
		{filepath.Join("packaging", "macos", "release.sh"), regexp.MustCompile(`go build -tags "?([^\s"]+)`)},
	}
	for _, c := range cases {
		data, err := os.ReadFile(filepath.Join(root, c.file))
		if err != nil {
			t.Fatalf("read %s: %v", c.file, err)
		}
		m := c.re.FindSubmatch(data)
		if m == nil {
			t.Fatalf("%s: no desktop build tags found (pattern %s)", c.file, c.re)
		}
		tags := strings.Split(string(m[1]), ",")
		found := false
		for _, tag := range tags {
			if tag == "private_mac_apis" {
				found = true
			}
		}
		if !found {
			t.Errorf("%s builds the desktop with tags %q, missing private_mac_apis (DevTools on macOS)", c.file, m[1])
		}
	}
}
