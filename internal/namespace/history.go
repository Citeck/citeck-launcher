package namespace

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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

// OperationHistory records operations to a JSONL file.
type OperationHistory struct {
	path string
}

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
}
