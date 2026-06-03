package desktop

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/citeck/citeck-launcher/internal/update"
)

func TestSelectDaemonBinaryPrefersHealthyPayload(t *testing.T) {
	updatesDir := t.TempDir()

	// No payload → falls back to the bundled executable.
	got, err := selectDaemonBinaryFrom(updatesDir, "2.4.0")
	if err != nil {
		t.Fatal(err)
	}
	self, _ := os.Executable()
	if got != self {
		t.Fatalf("empty updates: got %q want bundled %q", got, self)
	}

	// Stage a good 2.5.0 newer than current → preferred.
	bin := filepath.Join(updatesDir, "2.5.0", "citeck-launcher")
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	_ = update.AddStaged(updatesDir, update.Entry{Version: "2.5.0", Path: bin})
	_ = update.MarkState(updatesDir, "2.5.0", update.StateGood)

	got, _ = selectDaemonBinaryFrom(updatesDir, "2.4.0")
	if got != bin {
		t.Fatalf("got %q want staged %q", got, bin)
	}

	// never-downgrade: current newer than staged → bundled.
	got, _ = selectDaemonBinaryFrom(updatesDir, "2.6.0")
	if got != self {
		t.Fatalf("never-downgrade: got %q want bundled", got)
	}
}
