package cli

import "testing"

// TestSnapshotImportCmd_HasDetachFlag verifies that `citeck snapshot import`
// exposes the `-d` / `--detach` flag (default false). The flag suppresses the
// wait-for-RUNNING behavior introduced after the silent-exit UX bug.
func TestSnapshotImportCmd_HasDetachFlag(t *testing.T) {
	cmd := newSnapshotImportCmd()

	flag := cmd.Flags().Lookup("detach")
	if flag == nil {
		t.Fatal("expected --detach flag on `snapshot import` command")
	}
	if flag.Shorthand != "d" {
		t.Errorf("expected shorthand -d, got -%s", flag.Shorthand)
	}
	if flag.DefValue != "false" {
		t.Errorf("expected default false, got %s", flag.DefValue)
	}
}
