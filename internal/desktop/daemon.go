package desktop

import (
	"sync"
	"sync/atomic"
)

// DaemonStatus holds the current daemon state, observable by the UI layer.
type DaemonStatus struct {
	lastError atomic.Value // stores string
	failures  atomic.Int32
	logBuf    LogBuffer
}

// SetError records the last daemon error.
func (s *DaemonStatus) SetError(err error) {
	if err != nil {
		s.lastError.Store(err.Error())
	} else {
		s.lastError.Store("")
	}
}

// LastError returns the last daemon error message, or empty string.
func (s *DaemonStatus) LastError() string {
	v, _ := s.lastError.Load().(string)
	return v
}

// Failures returns the current consecutive failure count.
func (s *DaemonStatus) Failures() int {
	return int(s.failures.Load())
}

// LogLines returns the last N startup log lines (captured before daemon was ready).
func (s *DaemonStatus) LogLines() string {
	return s.logBuf.String()
}

// LogWriter returns the io.Writer for capturing daemon startup logs.
func (s *DaemonStatus) LogWriter() *LogBuffer {
	return &s.logBuf
}

// LogBuffer is a thread-safe ring buffer that captures recent log output.
type LogBuffer struct {
	mu   sync.Mutex
	buf  []byte
	size int
}

const maxLogBufSize = 64 * 1024 // 64KB — enough for startup logs

// Write implements io.Writer. Keeps only the last maxLogBufSize bytes.
func (b *LogBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf = append(b.buf, p...)
	if len(b.buf) > maxLogBufSize {
		b.buf = b.buf[len(b.buf)-maxLogBufSize:]
	}
	b.size += len(p)
	return len(p), nil
}

// String returns the buffered content.
func (b *LogBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.buf)
}

// Clear resets the buffer.
func (b *LogBuffer) Clear() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf = b.buf[:0]
	b.size = 0
}
