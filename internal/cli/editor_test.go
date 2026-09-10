package cli

import (
	"os"
	"testing"
)

func TestOpenInEditor_DetectsChange(t *testing.T) {
	orig := editorRunner
	defer func() { editorRunner = orig }()
	editorRunner = func(_, path string) error {
		return os.WriteFile(path, []byte("changed: true\n"), 0o600)
	}

	edited, changed, tmp, err := openInEditor([]byte("changed: false\n"), ".yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmp)
	if !changed || string(edited) != "changed: true\n" {
		t.Errorf("changed=%v edited=%q", changed, string(edited))
	}
	// The temp file belongs to the CALLER now: openInEditor hands the path back
	// and removes nothing, because only the caller knows whether the edit was
	// delivered. Removing it here is what threw the operator's text away on
	// every failure that was not a 400.
	st, err := os.Stat(tmp)
	if err != nil {
		t.Fatalf("temp file must survive openInEditor: %v", err)
	}
	// An app config can carry env values (tokens, passwords): owner-only.
	if perm := st.Mode().Perm(); perm != 0o600 {
		t.Errorf("temp file mode = %o, want 600", perm)
	}
}

func TestOpenInEditor_NoChangeHandsBackTheSamePath(t *testing.T) {
	orig := editorRunner
	defer func() { editorRunner = orig }()
	var seenPath string
	editorRunner = func(_, path string) error { seenPath = path; return nil } // no write

	_, changed, tmp, err := openInEditor([]byte("x: 1\n"), ".yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmp)
	if changed {
		t.Error("expected no change")
	}
	if tmp != seenPath {
		t.Errorf("returned path %q, editor saw %q", tmp, seenPath)
	}
	if _, err := os.Stat(tmp); err != nil {
		t.Errorf("temp file must survive openInEditor: %v", err)
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
