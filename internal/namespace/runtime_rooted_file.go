package namespace

import (
	"fmt"
	"log/slog"
	"strings"
)

// EditGeneratedFile keeps baseline selection, one rooted disk replacement and
// delta bookkeeping under the same runtime lock. The caller owns the rooted
// filesystem boundary; unlike WriteEditedFile this method accepts no host path.
// reset affects exactly one delta. A persist refusal remains visible through
// StateWriteError and the normal dirty-debt retry, as for the native editor.
func (r *Runtime) EditGeneratedFile(key string, content []byte, reset bool, write func([]byte) error) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	baseline, ok := r.lastGenFiles[key]
	if !ok {
		return fmt.Errorf("generated file %q not found", key)
	}
	if reset {
		content = baseline
	}
	edit, err := MakeFileEdit(key[strings.LastIndex(key, "/")+1:], baseline, content)
	if err != nil {
		return fmt.Errorf("compute file delta: %w", err)
	}
	if operationErr := write(content); operationErr != nil {
		return operationErr
	}
	if reset {
		delete(r.editedFileEdits, key)
	} else {
		r.editedFileEdits[key] = edit
	}
	_ = r.persistUnderLock("edit-generated-file")
	if len(r.lastApps) > 0 {
		if err = r.cmdQueue.Enqueue(cmdRegenerate{apps: r.lastApps}); err != nil {
			slog.Warn("Failed to enqueue generated-file regeneration", "err", err)
		}
	}
	return nil
}

// GeneratedFileKeys returns names only; it never scans disk or copies document
// contents. MCP's eligibility filter applies the app's generated mount too.
func (r *Runtime) GeneratedFileKeys() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.lastGenFiles))
	for k := range r.lastGenFiles {
		out = append(out, k)
	}
	return out
}
