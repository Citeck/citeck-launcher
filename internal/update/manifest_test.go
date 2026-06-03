package update

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFakeBinary(t *testing.T, dir, version string) string {
	t.Helper()
	p := filepath.Join(dir, version, "citeck-launcher")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestManifestRoundTripAndSelectBest(t *testing.T) {
	dir := t.TempDir()

	// Empty dir → no manifest → SelectBest returns ok=false.
	if _, ok := SelectBest(dir, "2.4.0"); ok {
		t.Fatal("SelectBest on empty dir should be ok=false")
	}

	// Stage 2.5.0 and 2.6.0; mark both good.
	for _, v := range []string{"2.5.0", "2.6.0"} {
		p := writeFakeBinary(t, dir, v)
		if err := AddStaged(dir, Entry{Version: v, Path: p, SHA256: "x"}); err != nil {
			t.Fatal(err)
		}
		if err := MarkState(dir, v, StateGood); err != nil {
			t.Fatal(err)
		}
	}

	// SelectBest picks the newest good > current.
	got, ok := SelectBest(dir, "2.4.0")
	if !ok || filepath.Base(filepath.Dir(got)) != "2.6.0" {
		t.Fatalf("SelectBest = %q ok=%v, want .../2.6.0", got, ok)
	}

	// Fail 2.6.0 → rollback target is the previous good (2.5.0), NOT bundled.
	if err := MarkState(dir, "2.6.0", StateFailed); err != nil {
		t.Fatal(err)
	}
	got, ok = SelectBest(dir, "2.4.0")
	if !ok || filepath.Base(filepath.Dir(got)) != "2.5.0" {
		t.Fatalf("after fail, SelectBest = %q ok=%v, want .../2.5.0", got, ok)
	}

	// never-downgrade: current already newer than every good → ok=false.
	if _, downOK := SelectBest(dir, "2.9.0"); downOK {
		t.Fatal("SelectBest must not pick a version <= current (never-downgrade)")
	}

	// pending counts as selectable (under health-gate).
	p := writeFakeBinary(t, dir, "2.7.0")
	if err := AddStaged(dir, Entry{Version: "2.7.0", Path: p}); err != nil {
		t.Fatal(err)
	}
	if err := MarkState(dir, "2.7.0", StatePending); err != nil {
		t.Fatal(err)
	}
	got, _ = SelectBest(dir, "2.4.0")
	if filepath.Base(filepath.Dir(got)) != "2.7.0" {
		t.Fatalf("pending should be selectable, got %q", got)
	}

	// missing file on disk is skipped even if manifest says good.
	_ = os.RemoveAll(filepath.Join(dir, "2.7.0"))
	_ = os.RemoveAll(filepath.Join(dir, "2.6.0"))
	got, ok = SelectBest(dir, "2.4.0")
	if !ok || filepath.Base(filepath.Dir(got)) != "2.5.0" {
		t.Fatalf("missing-file entries must be skipped, got %q ok=%v", got, ok)
	}
}

func TestFailedNewerThan(t *testing.T) {
	dir := t.TempDir()

	// No manifest → empty.
	if v := FailedNewerThan(dir, "2.4.0"); v != "" {
		t.Fatalf("empty: got %q want \"\"", v)
	}

	p := writeFakeBinary(t, dir, "2.6.0")
	if err := AddStaged(dir, Entry{Version: "2.6.0", Path: p}); err != nil {
		t.Fatal(err)
	}
	// staged/pending/good must NOT count as failed.
	if v := FailedNewerThan(dir, "2.4.0"); v != "" {
		t.Fatalf("non-failed: got %q want \"\"", v)
	}

	if err := MarkState(dir, "2.6.0", StateFailed); err != nil {
		t.Fatal(err)
	}
	if v := FailedNewerThan(dir, "2.4.0"); v != "2.6.0" {
		t.Fatalf("failed newer: got %q want 2.6.0", v)
	}
	// never-downgrade: a failed version <= current is ignored.
	if v := FailedNewerThan(dir, "2.6.0"); v != "" {
		t.Fatalf("failed not newer: got %q want \"\"", v)
	}
}

func TestLoadRejectsCorruptJSON(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte("{bad"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir); err == nil {
		t.Fatal("corrupt JSON must return an error")
	}
}
