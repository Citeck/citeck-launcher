package daemon

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPrepareDestPath_NonExistent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "does-not-exist", "file.txt")
	skip, err := prepareDestPath(path, []byte("hi"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if skip {
		t.Error("skip=true for non-existent path; want false")
	}
}

func TestPrepareDestPath_DirAtFilePath_RemovesAndAllowsWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dockerdir")
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	skip, err := prepareDestPath(path, []byte("content"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if skip {
		t.Error("skip=true after removing dir; want false")
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Errorf("stale dir not removed: %v", statErr)
	}
}

func TestPrepareDestPath_SameContent_Skips(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	content := []byte("same")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
	skip, err := prepareDestPath(path, content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !skip {
		t.Error("skip=false for identical content; want true")
	}
}

// Pre-existing .sh with correct content but missing +x bit must be chmod'd
// back to 0o755 via the skip path — the naïve early-return would leave the
// script non-executable forever, since differing content is the only other
// trigger that rewrites the file.
func TestPrepareDestPath_SameContentWrongPerm_RestoresExecutableBit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "init.sh")
	content := []byte("#!/bin/sh\necho hi")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
	skip, err := prepareDestPath(path, content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !skip {
		t.Error("skip=false for identical content; want true")
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o755 {
		t.Errorf(".sh perm=%o want 0755 (identical-content path must re-chmod)", fi.Mode().Perm())
	}
}

func TestPrepareDestPath_DifferentContent_DoesNotSkip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Same size, different bytes — must NOT skip.
	skip, err := prepareDestPath(path, []byte("new"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if skip {
		t.Error("skip=true for different content; want false")
	}
}

func TestWriteRuntimeFiles_WritesFilesWithCorrectPerm(t *testing.T) {
	dir := t.TempDir()
	files := map[string][]byte{
		"postgres/postgresql.conf": []byte("listen_addresses = '*'"),
		"postgres/init.sh":         []byte("#!/bin/sh\necho hi"),
		"nested/a/b/c.txt":         []byte("deep"),
	}
	writeRuntimeFiles(dir, files)

	confPath := filepath.Join(dir, "postgres", "postgresql.conf")
	fi, err := os.Stat(confPath)
	if err != nil {
		t.Fatalf("conf not written: %v", err)
	}
	if fi.Mode().Perm() != 0o644 {
		t.Errorf("conf perm=%o want 0644", fi.Mode().Perm())
	}

	shPath := filepath.Join(dir, "postgres", "init.sh")
	fi, err = os.Stat(shPath)
	if err != nil {
		t.Fatalf(".sh not written: %v", err)
	}
	if fi.Mode().Perm() != 0o755 {
		t.Errorf(".sh perm=%o want 0755", fi.Mode().Perm())
	}

	deepPath := filepath.Join(dir, "nested", "a", "b", "c.txt")
	if _, err := os.Stat(deepPath); err != nil {
		t.Errorf("deep path not created: %v", err)
	}
}

func TestWriteRuntimeFiles_RecoversFromDirInsteadOfFile(t *testing.T) {
	dir := t.TempDir()
	stale := filepath.Join(dir, "postgres", "postgresql.conf")
	if err := os.MkdirAll(stale, 0o755); err != nil {
		t.Fatal(err)
	}
	writeRuntimeFiles(dir, map[string][]byte{
		"postgres/postgresql.conf": []byte("listen_addresses = '*'"),
	})
	fi, err := os.Stat(stale)
	if err != nil {
		t.Fatalf("file not written after dir recovery: %v", err)
	}
	if !fi.Mode().IsRegular() {
		t.Errorf("expected regular file, got mode=%v", fi.Mode())
	}
}

func TestWriteRuntimeFiles_SkipsUnchangedPreservesMtime(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	content := []byte("stable")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
	// Rewind mtime to detect rewrites on identical content.
	past := time.Now().Add(-1 * time.Hour)
	if err := os.Chtimes(path, past, past); err != nil {
		t.Fatal(err)
	}
	origFi, _ := os.Stat(path)

	writeRuntimeFiles(dir, map[string][]byte{"a.txt": content})

	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !fi.ModTime().Equal(origFi.ModTime()) {
		t.Errorf("mtime changed on no-op write: old=%v new=%v", origFi.ModTime(), fi.ModTime())
	}
}
