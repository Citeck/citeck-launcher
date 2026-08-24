package desktop_test

import (
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Windows decides whether to hand a process a console window from a single byte
// in the PE optional header: IMAGE_SUBSYSTEM_WINDOWS_CUI (3) gets a console,
// IMAGE_SUBSYSTEM_WINDOWS_GUI (2) does not. Go emits 3 by default for
// GOOS=windows, and the only thing that flips it is `-H windowsgui` in the
// link flags. Without it the desktop wrapper — a webview app that never reads
// stdin and whose logs the user cannot act on — opened a second, black console
// window next to itself on every launch, carrying the Wails banner and the
// supervisor's own log lines (reported on 2.11.0, Windows 10 22H2).
//
// The flag lives in exactly one place, packaging/windows/release.sh, and that
// script is the only thing that builds the shipped .exe (release-go.yml calls it
// through `make release-desktop-windows`). Nothing else in the tree would notice
// it going missing, and the failure is invisible to every CI job here: the
// artifact builds and runs, it just grows a window on someone else's desktop.
//
// So rather than assert on the presence of a substring, this test extracts the
// real link flags from the script and links a stub binary with them, then reads
// the subsystem byte back off the result. That way a typo, a quoting mistake or
// a flag that a future Go release stops honoring all fail here.
const (
	imageSubsystemWindowsGUI = 2
	imageSubsystemWindowsCUI = 3
)

func TestWindowsDesktopReleaseLinksAGUISubsystemBinary(t *testing.T) {
	ldflags := windowsReleaseLdflags(t)

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module stub\n\ngo 1.22\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.go"),
		[]byte("package main\n\nvar version = \"dev\"\n\nfunc main() { _ = version }\n"), 0o600))

	out := filepath.Join(dir, "stub.exe")
	cmd := exec.Command("go", "build", "-ldflags", ldflags, "-o", out, ".")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOOS=windows", "GOARCH=amd64", "CGO_ENABLED=0")
	if combined, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("cannot cross-compile for windows in this environment: %v\n%s", err, combined)
	}

	got := peSubsystem(t, out)
	require.NotEqual(t, imageSubsystemWindowsCUI, got,
		"packaging/windows/release.sh links a console-subsystem binary (%q) — the desktop "+
			"wrapper will open a console window beside itself on every launch; add -H windowsgui",
		ldflags)
	require.Equal(t, imageSubsystemWindowsGUI, got, "unexpected PE subsystem for %q", ldflags)
}

// windowsReleaseLdflags returns the -ldflags argument the release script passes
// to the `go build` that produces citeck-launcher.exe, with the shell's
// ${VERSION} expansion stood in for. Line continuations are folded first so the
// flag can keep living on its own line in the script.
func windowsReleaseLdflags(t *testing.T) string {
	t.Helper()

	path := filepath.Join("..", "..", "packaging", "windows", "release.sh")
	raw, err := os.ReadFile(path) //nolint:gosec // fixed repo-relative path
	if os.IsNotExist(err) {
		t.Skipf("%s not present in this checkout", path)
	}
	require.NoError(t, err)

	script := strings.ReplaceAll(string(raw), "\\\n", " ")
	var buildLine string
	for line := range strings.SplitSeq(script, "\n") {
		if strings.Contains(line, "go build") && strings.Contains(line, "citeck-launcher.exe") {
			buildLine = line
			break
		}
	}
	require.NotEmpty(t, buildLine, "no `go build ... citeck-launcher.exe` line in %s", path)

	m := regexp.MustCompile(`-ldflags\s+"([^"]*)"`).FindStringSubmatch(buildLine)
	require.Len(t, m, 2, "no quoted -ldflags in the windows desktop build line: %s", buildLine)
	return strings.ReplaceAll(m[1], "${VERSION}", "0.0.0-test")
}

// peSubsystem reads the Subsystem field of a PE image. It sits at a fixed offset
// 68 into the optional header for both PE32 and PE32+, which is why this needs
// no format-width branch.
func peSubsystem(t *testing.T, path string) int {
	t.Helper()

	data, err := os.ReadFile(path) //nolint:gosec // path is produced by this test
	require.NoError(t, err)
	require.Greater(t, len(data), 0x40, "%s is too small to be a PE image", path)

	peOff := int(binary.LittleEndian.Uint32(data[0x3c:]))
	require.Greater(t, len(data), peOff+4+20+68+2, "%s truncated before the optional header", path)
	require.Equal(t, []byte("PE\x00\x00"), data[peOff:peOff+4], "%s has no PE signature", path)

	// PE signature (4) + COFF file header (20) + offset of Subsystem within the
	// optional header (68).
	return int(binary.LittleEndian.Uint16(data[peOff+4+20+68:]))
}
