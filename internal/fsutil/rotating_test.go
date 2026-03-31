package fsutil

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestRotation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.log")
	rw := NewRotatingWriter(path, 100, 3)
	defer rw.Close()

	// Write 80 bytes
	data := strings.Repeat("A", 80) + "\n"
	rw.Write([]byte(data))

	// Write another 80 bytes — should trigger rotation
	data2 := strings.Repeat("B", 80) + "\n"
	rw.Write([]byte(data2))

	// Verify .1 has old content, current has new content
	rotated, err := os.ReadFile(path + ".1")
	if err != nil {
		t.Fatalf("expected %s.1 to exist: %v", path, err)
	}
	if !strings.HasPrefix(string(rotated), "AAAA") {
		t.Fatalf("rotated file should contain old data, got: %s", string(rotated)[:20])
	}

	current, _ := os.ReadFile(path)
	if !strings.HasPrefix(string(current), "BBBB") {
		t.Fatalf("current file should contain new data, got: %s", string(current)[:20])
	}
}

func TestMultipleRotations(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.log")
	rw := NewRotatingWriter(path, 50, 3)
	defer rw.Close()

	// Write 4 chunks — causes 3 rotations, .3 should have oldest data
	for i := 0; i < 4; i++ {
		data := fmt.Sprintf("%s\n", strings.Repeat(string(rune('A'+i)), 49))
		rw.Write([]byte(data))
	}

	// .1 should exist (has C data), .2 (has B data), .3 (has A data)
	for i := 1; i <= 3; i++ {
		fpath := fmt.Sprintf("%s.%d", path, i)
		if _, err := os.Stat(fpath); err != nil {
			t.Fatalf("expected %s to exist", fpath)
		}
	}

	// Current file should have newest data (D)
	current, _ := os.ReadFile(path)
	if !strings.HasPrefix(string(current), "DDD") {
		t.Fatalf("current file should contain D data, got: %s", string(current)[:10])
	}

	// .1 should have C data
	f1, _ := os.ReadFile(path + ".1")
	if !strings.HasPrefix(string(f1), "CCC") {
		t.Fatalf(".1 should contain C data, got: %s", string(f1)[:10])
	}

	// .3 should have oldest data (A)
	f3, _ := os.ReadFile(path + ".3")
	if !strings.HasPrefix(string(f3), "AAA") {
		t.Fatalf(".3 should contain A data, got: %s", string(f3)[:10])
	}
}

func TestConcurrentWrites(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.log")
	rw := NewRotatingWriter(path, 500, 3)
	defer rw.Close()

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				data := fmt.Sprintf("goroutine %d write %d\n", id, j)
				if _, err := rw.Write([]byte(data)); err != nil {
					t.Errorf("write error: %v", err)
					return
				}
			}
		}(i)
	}
	wg.Wait()
}

func TestStaleFdDetection(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.log")
	rw := NewRotatingWriter(path, 1<<20, 3) // 1MB — no rotation during test
	defer rw.Close()

	rw.Write([]byte("before delete\n"))

	// Delete the file while fd is still open
	os.Remove(path)

	// On Linux, writes to the unlinked fd succeed silently.
	// Write staleCheckInterval times to trigger the stat check.
	for i := 0; i < staleCheckInterval; i++ {
		rw.Write([]byte("x\n"))
	}

	// After the check boundary, the file should be recreated
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("log file should be recreated after stale-fd detection: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("recreated log file should contain data")
	}
}

func TestClose(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.log")
	rw := NewRotatingWriter(path, 1000, 3)

	rw.Write([]byte("hello\n"))
	if err := rw.Close(); err != nil {
		t.Fatalf("close error: %v", err)
	}

	// After close, file handle is nil. Write re-opens the file automatically
	// (self-healing behavior — the writer recovers from transient failures).
	_, err := rw.Write([]byte("after close\n"))
	if err != nil {
		t.Fatalf("write after close should succeed (self-healing): %v", err)
	}

	// Verify data was written
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "after close") {
		t.Fatal("expected 'after close' in log file after self-healing write")
	}

	// Double close should not panic
	if err := rw.Close(); err != nil {
		t.Fatalf("double close error: %v", err)
	}
}
