package fsutil

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestAtomicWriteFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")

	data := []byte("hello world")
	if err := AtomicWriteFile(path, data, 0o644); err != nil {
		t.Fatalf("AtomicWriteFile: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Errorf("got %q, want %q", got, data)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o644 {
		t.Errorf("perm = %o, want 0644", perm)
	}
}

func TestAtomicWriteFileOverwrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")

	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := AtomicWriteFile(path, []byte("new"), 0o600); err != nil {
		t.Fatalf("AtomicWriteFile: %v", err)
	}

	got, _ := os.ReadFile(path)
	if string(got) != "new" {
		t.Errorf("got %q, want %q", got, "new")
	}

	info, _ := os.Stat(path)
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("perm = %o, want 0600", perm)
	}
}

func TestAtomicWriteFileBadDir(t *testing.T) {
	err := AtomicWriteFile("/nonexistent/dir/file.txt", []byte("x"), 0o644)
	if err == nil {
		t.Error("expected error for nonexistent directory")
	}
}
