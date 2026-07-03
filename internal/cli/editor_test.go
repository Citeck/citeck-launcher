package cli

import (
	"os"
	"testing"
)

func TestOpenInEditor_DetectsChange(t *testing.T) {
	orig := editorRunner
	defer func() { editorRunner = orig }()
	editorRunner = func(_ , path string) error {
		return os.WriteFile(path, []byte("changed: true\n"), 0o600)
	}

	edited, changed, err := openInEditor([]byte("changed: false\n"), ".yaml")
	if err != nil {
		t.Fatal(err)
	}
	if !changed || string(edited) != "changed: true\n" {
		t.Errorf("changed=%v edited=%q", changed, string(edited))
	}
}

func TestOpenInEditor_NoChangeAndCleanup(t *testing.T) {
	orig := editorRunner
	defer func() { editorRunner = orig }()
	var seenPath string
	editorRunner = func(_, path string) error { seenPath = path; return nil } // no write

	_, changed, err := openInEditor([]byte("x: 1\n"), ".yaml")
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Error("expected no change")
	}
	if _, err := os.Stat(seenPath); !os.IsNotExist(err) {
		t.Errorf("temp file %q not cleaned up", seenPath)
	}
}

func TestResolveEditor_PrefersCiteckEditor(t *testing.T) {
	t.Setenv("CITECK_EDITOR", "myed")
	t.Setenv("VISUAL", "vis")
	t.Setenv("EDITOR", "ed")
	if got := resolveEditor(); got != "myed" {
		t.Errorf("resolveEditor() = %q, want myed", got)
	}
}
