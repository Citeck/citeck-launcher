package namespace

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"
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
}

const maxHistoryEntries = 1000
const truncateToEntries = 500
const rotateCheckInterval = 100 // check file size every N writes

func NewOperationHistory(logDir string) *OperationHistory {
	return &OperationHistory{
		path: filepath.Join(logDir, "operations.jsonl"),
	}
}

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
		return
	}

	f, fileErr := os.OpenFile(h.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if fileErr != nil {
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
	data, err := os.ReadFile(h.path)
	if err != nil {
		return
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) <= maxHistoryEntries {
		return
	}
	// Keep last N entries, atomic via temp file
	lines = lines[len(lines)-truncateToEntries:]
	tmpPath := h.path + ".tmp"
	if err := os.WriteFile(tmpPath, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		return
	}
	os.Rename(tmpPath, h.path)
}
