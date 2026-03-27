package fsutil

import (
	"fmt"
	"os"
	"sync"
)

// RotatingWriter is a thread-safe log file writer that rotates when maxBytes is reached.
// Old files are renamed with numeric suffixes (.1, .2, etc.) up to maxFiles.
type RotatingWriter struct {
	mu       sync.Mutex
	path     string
	maxBytes int64
	maxFiles int
	file     *os.File
	size     int64
}

// NewRotatingWriter creates a rotating writer for the given log file path.
func NewRotatingWriter(path string, maxBytes int64, maxFiles int) *RotatingWriter {
	rw := &RotatingWriter{path: path, maxBytes: maxBytes, maxFiles: maxFiles}
	rw.openOrCreate()
	return rw
}

func (rw *RotatingWriter) openOrCreate() {
	f, err := os.OpenFile(rw.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	info, _ := f.Stat()
	rw.file = f
	if info != nil {
		rw.size = info.Size()
	}
}

func (rw *RotatingWriter) Write(p []byte) (int, error) {
	rw.mu.Lock()
	defer rw.mu.Unlock()
	if rw.file == nil {
		rw.openOrCreate()
		if rw.file == nil {
			return 0, fmt.Errorf("log file not available")
		}
	}
	if rw.size+int64(len(p)) > rw.maxBytes {
		rw.rotate()
	}
	n, err := rw.file.Write(p)
	rw.size += int64(n)
	return n, err
}

// Close closes the underlying log file.
func (rw *RotatingWriter) Close() error {
	rw.mu.Lock()
	defer rw.mu.Unlock()
	if rw.file != nil {
		err := rw.file.Close()
		rw.file = nil
		return err
	}
	return nil
}

func (rw *RotatingWriter) rotate() {
	if rw.file != nil {
		rw.file.Close()
		rw.file = nil
	}
	// Delete oldest file, then shift: .2 → .3, .1 → .2, current → .1
	os.Remove(fmt.Sprintf("%s.%d", rw.path, rw.maxFiles))
	for i := rw.maxFiles - 1; i >= 1; i-- {
		src := fmt.Sprintf("%s.%d", rw.path, i)
		dst := fmt.Sprintf("%s.%d", rw.path, i+1)
		os.Rename(src, dst)
	}
	os.Rename(rw.path, rw.path+".1")
	rw.openOrCreate()
}
