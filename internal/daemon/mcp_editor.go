package daemon

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path"
	"slices"
	"strings"

	"github.com/citeck/citeck-launcher/internal/fsutil"
	"github.com/citeck/citeck-launcher/internal/launchermcp"
)

// mcpAppEditor delegates to the same handlers as the UI. In particular it must
// not bypass dependency-image gates, persistence reporting or reload locking.
func (d *Daemon) mcpAppEditor(ctx context.Context, tool string, a launchermcp.Arguments) (any, error) {
	if !safeIDPattern.MatchString(a.App) {
		return nil, errors.New("invalid app id")
	}
	switch tool {
	case "application_config_read":
		return d.mcpInvoke(ctx, d.handleGetAppConfig, http.MethodGet, "name", a.App, nil)
	case "application_config_write":
		return d.mcpInvoke(ctx, d.handlePutAppConfig, http.MethodPut, "name", a.App, []byte(a.Content))
	case "application_config_reset":
		return d.mcpInvoke(ctx, d.handleResetAppConfig, http.MethodPost, "name", a.App, nil)
	case "application_files_list":
		return d.mcpEditorFiles(a)
	}
	key := strings.TrimPrefix(a.Path, "./")
	if key == "." || key == "" || path.Clean(key) != key || strings.HasPrefix(key, "/") || strings.HasPrefix(key, "../") || strings.ContainsAny(key, "\\:") {
		return nil, errors.New("invalid relative application file path")
	}
	if !mcpEditableFile(key) {
		return nil, errors.New("file is not editable in the application editor")
	}
	return d.mcpMountedFile(tool, a, key)
}

// The same file-delta semantics as the UI, retaining the existing MCP rooted
// I/O protections and explicit reload workflow rather than opening arbitrary paths.
func (d *Daemon) mcpMountedFile(tool string, a launchermcp.Arguments, key string) (any, error) {
	if !d.longOp.TryLock(longOpRequest) {
		return nil, errors.New("LONG_OP_IN_PROGRESS")
	}
	defer d.longOp.Unlock()
	if !d.reloadMu.TryLock() {
		return nil, errors.New("RELOAD_IN_PROGRESS")
	}
	defer d.reloadMu.Unlock()
	act := d.active()
	def, ok := runtimeAppDef(act.runtime, a.App)
	if !ok || !isAppBindMount(def, "./"+key) {
		return nil, errors.New("file is not a bind mount of this application")
	}
	root, err := fsutil.OpenCanonicalRoot(act.volumesBase)
	if err != nil {
		return nil, fmt.Errorf("open application files: %w", err)
	}
	defer root.Close()
	parent, leaf, err := fsutil.RootedParent(root, key, false)
	if err != nil {
		return nil, fmt.Errorf("open application file parent: %w", err)
	}
	defer parent.Close()
	// Existing regular files only, never symlinks, devices or new arbitrary paths.
	f, err := fsutil.OpenRegularRooted(parent, leaf)
	if err != nil {
		return nil, fmt.Errorf("open application file: %w", err)
	}
	_ = f.Close()
	if tool == "application_file_read" {
		data, e := fsutil.ReadRooted(parent, leaf, launchermcp.MaxBytes)
		if e != nil {
			return nil, fmt.Errorf("read application file: %w", e)
		}
		baseline, _ := act.runtime.GeneratedFile(key)
		return map[string]string{"content": string(data), "baseline": string(baseline)}, nil
	}
	if len(a.Content) > launchermcp.MaxBytes {
		return nil, errors.New("application file exceeds MCP size limit")
	}
	_, generated := act.runtime.GeneratedFile(key)
	if tool == "application_file_reset" && !generated {
		// Like the UI, clear the delta even when no generator owns the file;
		// without a baseline there is no original content to invent.
		err = act.runtime.ResetEditedFile(a.App, key)
	} else {
		err = act.runtime.EditMountedFile(key, []byte(a.Content), tool == "application_file_reset", func(content []byte) error {
			return fsutil.AtomicWriteRooted(parent, leaf, content, 0o644)
		})
	}
	if err != nil {
		return nil, fmt.Errorf("edit application file: %w", err)
	}
	return map[string]any{"accepted": true, "applyRequired": true, "stateWriteError": act.runtime.StateWriteError()}, nil
}

// Keep in sync with web/src/lib/files.ts: these are text editor formats, not
// the old Spring-only MCP allowlist. Mount ownership is checked by the handlers.
func mcpEditableFile(key string) bool {
	return path.Base(key) == "Dockerfile" || slices.Contains([]string{"yaml", "yml", "json", "kt", "java", "js", "lua", "Dockerfile", "sh", "txt", "conf"}, strings.TrimPrefix(path.Ext(key), "."))
}
