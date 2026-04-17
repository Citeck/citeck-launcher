package namespace

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/citeck/citeck-launcher/internal/fsutil"
)

// OperationRecord is a single entry in the operation history.
type OperationRecord struct {
	Timestamp string `json:"ts"`
	Operation string `json:"op"`
	App       string `json:"app,omitempty"`
	Result    string `json:"result"`
	Duration  int64  `json:"duration,omitempty"`
	Error     string `json:"error,omitempty"`
	Apps      int    `json:"apps,omitempty"`
}

// OperationHistory records operations to a JSONL file with automatic rotation.
type OperationHistory struct {
	path     string
	counter  atomic.Int64 // in-memory write counter, avoids reading file on every Record
	rotateMu sync.Mutex   // protects rotateIfNeeded against concurrent execution
}

const maxHistoryEntries = 1000
const truncateToEntries = 500
const rotateCheckInterval = 100 // check file size every N writes

// NewOperationHistory creates a new operation history writer in the given log directory.
func NewOperationHistory(logDir string) *OperationHistory {
	return &OperationHistory{
		path: filepath.Join(logDir, "operations.jsonl"),
	}
}

// Record appends an operation entry to the history log file.
func (h *OperationHistory) Record(op, app, result string, duration time.Duration, err error, appCount int) {
	rec := OperationRecord{
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Operation: op,
		App:       app,
		Result:    result,
		Duration:  duration.Milliseconds(),
		Apps:      appCount,
	}
	if err != nil {
		rec.Error = err.Error()
	}

	data, jsonErr := json.Marshal(rec)
	if jsonErr != nil {
		slog.Warn("Failed to marshal operation record", "err", jsonErr)
		return
	}

	f, fileErr := os.OpenFile(h.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if fileErr != nil {
		slog.Warn("Failed to open history file", "path", h.path, "err", fileErr)
		return
	}
	defer f.Close()

	fmt.Fprintf(f, "%s\n", data)

	// Check rotation only every N writes to avoid reading the file on every append
	if h.counter.Add(1)%rotateCheckInterval == 0 {
		h.rotateIfNeeded()
	}
}

func (h *OperationHistory) rotateIfNeeded() {
	h.rotateMu.Lock()
	defer h.rotateMu.Unlock()

	data, err := os.ReadFile(h.path)
	if err != nil {
		return
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) <= maxHistoryEntries {
		return
	}
	// Keep last N entries, atomic write
	lines = lines[len(lines)-truncateToEntries:]
	if err := fsutil.AtomicWriteFile(h.path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		slog.Warn("Failed to rotate history file", "path", h.path, "err", err)
	}
}
