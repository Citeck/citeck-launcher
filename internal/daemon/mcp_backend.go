package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/launchermcp"
	"gopkg.in/yaml.v3"
)

type mcpBackend struct{ d *Daemon }

func strictMCPJSON(b []byte, out any) error {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		return fmt.Errorf("mcp backend: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return errors.New("one JSON object required")
	}
	return nil
}

//nolint:gocyclo // Closed tool dispatcher keeps target and operation selection together.
func (b mcpBackend) Call(ctx context.Context, tool string, a launchermcp.Arguments) (any, error) {
	d := b.d
	selection := tool == "workspace_select" || tool == "namespace_select"
	if selection {
		if !d.targetMu.TryLock() {
			return nil, errors.New("TARGET_BUSY: target operation in progress")
		}
		defer d.targetMu.Unlock()
	} else {
		d.targetMu.RLock()
		defer d.targetMu.RUnlock()
	}
	act := d.active()
	target := targetOf(act)
	if tool == "launcher_status" {
		return map[string]any{"version": d.version, "target": target, "available": true}, nil
	}
	if tool == "workspaces_list" {
		if d.store == nil {
			return nil, errors.New("daemon storage unavailable")
		}
		rows, err := d.listWorkspacesWithDefault()
		if err != nil {
			return nil, err
		}
		out := make([]map[string]any, 0, len(rows))
		for _, r := range rows {
			out = append(out, map[string]any{"id": r.ID, "name": r.Name, "active": r.ID == act.workspaceID})
		}
		return out, nil
	}
	if a.Target != target {
		return nil, fmt.Errorf("TARGET_CHANGED: expected workspace/namespace does not match active target")
	}
	if tool == "namespaces_list" {
		if d.store == nil {
			return nil, errors.New("daemon storage unavailable")
		}
		rows, err := d.store.ListNamespaces(target.WorkspaceID)
		if err != nil {
			return nil, fmt.Errorf("mcp backend: %w", err)
		}
		out := make([]map[string]string, 0, len(rows))
		for _, r := range rows {
			out = append(out, map[string]string{"id": r.ID, "workspaceId": target.WorkspaceID, "name": r.Name, "status": r.Status})
		}
		return out, nil
	}
	if tool == "daemon_logs" {
		return mcpDaemonLogs(a.Tail)
	}
	if tool == "workspace_select" {
		if !validateID(a.DestinationWorkspaceID) {
			return nil, errors.New("invalid destination workspace")
		}
		return d.mcpInvoke(ctx, d.handleActivateWorkspace, "POST", "id", a.DestinationWorkspaceID, nil)
	}
	if tool == "namespace_select" {
		if a.DestinationWorkspaceID != target.WorkspaceID || !validateID(a.DestinationNamespaceID) {
			return nil, errors.New("destination namespace must name the current workspace and a valid existing namespace")
		}
		return d.mcpInvoke(ctx, d.handleActivateNamespace, "POST", "id", a.DestinationNamespaceID, nil)
	}
	if tool == "system_dump_create" {
		return d.mcpDiagnostic(ctx, act, tool, a)
	}
	if act.runtime == nil || act.nsConfig == nil {
		return nil, errors.New("NOT_CONFIGURED: no namespace runtime")
	}
	switch tool {
	case "jvm_command", "heap_dump_create", "heap_dumps_list", "heap_dump_download":
		return d.mcpDiagnostic(ctx, act, tool, a)
	case "namespace_status":
		value, err := d.mcpInvoke(ctx, d.handleGetNamespace, "GET", "", "", nil)
		if err != nil {
			return nil, err
		}
		v, ok := value.(map[string]any)
		if !ok {
			return nil, errors.New("invalid namespace status")
		}
		apps := []map[string]any{}
		if rows, ok := v["apps"].([]any); ok {
			for _, raw := range rows {
				if row, ok := raw.(map[string]any); ok {
					m := map[string]any{}
					for _, k := range []string{"name", "status", "cpu", "memory", "detached", "held", "waitingFor", "restartCount"} {
						if x, ok := row[k]; ok {
							m[k] = x
						}
					}
					apps = append(apps, m)
				}
			}
		}
		out := map[string]any{"target": target, "apps": apps}
		for _, k := range []string{"status", "updating", "updateError", "updateErrorAt", "stateWriteError", "dependencyRollbackPending", "snapshotImportPending"} {
			if x, ok := v[k]; ok {
				out[k] = x
			}
		}
		return out, nil
	case "application_logs":
		if !safeIDPattern.MatchString(a.App) {
			return nil, errors.New("invalid app id")
		}
		app := act.runtime.FindApp(a.App)
		if app == nil {
			if _, known := act.runtime.GeneratedDef(a.App); known {
				return map[string]any{"content": "", "truncated": false}, nil
			}
			return nil, errors.New("APP_NOT_FOUND")
		}
		if app.ContainerID == "" {
			return map[string]any{"content": "", "truncated": false}, nil
		}
		if act.dockerClient == nil {
			return nil, errors.New("docker unavailable")
		}
		logCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		text, truncated, err := act.dockerClient.ContainerLogsBounded(logCtx, app.ContainerID, mcpTail(a.Tail), 48<<10)
		if err != nil {
			return nil, fmt.Errorf("mcp backend: %w", err)
		}
		return map[string]any{"content": text, "truncated": truncated}, nil
	case "application_settings_read", "application_settings_write", "application_settings_reset":
		return d.mcpAppSettings(ctx, act, tool, a)
	case "spring_files_list", "spring_file_read", "spring_file_write", "spring_file_reset":
		return d.mcpSpringFile(act, tool, a)
	}
	handlers := map[string]http.HandlerFunc{"namespace_start": d.handleStartNamespace, "namespace_stop": d.handleStopNamespace, "namespace_reload": d.handleReloadNamespace, "namespace_reload_preview": d.handleReloadPlan, "application_start": d.handleAppStart, "application_stop": d.handleAppStop, "application_restart": d.handleAppRestart}
	handler, ok := handlers[tool]
	if !ok {
		return nil, errors.New("unknown MCP tool")
	}
	if strings.HasPrefix(tool, "application_") && !safeIDPattern.MatchString(a.App) {
		return nil, errors.New("invalid app id")
	}
	return d.mcpInvoke(ctx, handler, "POST", "name", a.App, nil)
}

// Fixed handler adapters only. No caller-supplied URL, method or endpoint can
// reach this seam. The writer caps accumulation, but source readers (logs and
// files) have their own earlier limits and never use this adapter.
func (d *Daemon) mcpInvoke(ctx context.Context, h http.HandlerFunc, method, key, value string, body []byte) (any, error) {
	r, err := http.NewRequestWithContext(targetHeldContext(ctx), method, "http://local/", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("mcp backend: %w", err)
	}
	if key != "" {
		r.SetPathValue(key, value)
	}
	w := &mcpResponse{header: make(http.Header), code: 200}
	h(w, r)
	if w.overflow {
		return nil, errors.New("MCP response exceeds limit")
	}
	var out any
	if err = json.Unmarshal(w.body.Bytes(), &out); err != nil {
		return nil, errors.New("invalid daemon response")
	}
	if w.code >= 400 {
		var details struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		}
		_ = json.Unmarshal(w.body.Bytes(), &details)
		return nil, &launchermcp.ToolError{Status: w.code, Code: details.Code, Message: details.Message}
	}
	if result, ok := out.(map[string]any); ok {
		result["accepted"] = true
		if act := d.active(); act.runtime != nil {
			result["stateWriteError"] = act.runtime.StateWriteError()
		}
	}
	return out, nil
}

type mcpResponse struct {
	header   http.Header
	code     int
	body     bytes.Buffer
	overflow bool
}

func (w *mcpResponse) Header() http.Header  { return w.header }
func (w *mcpResponse) WriteHeader(code int) { w.code = code }
func (w *mcpResponse) Write(p []byte) (int, error) {
	if w.body.Len()+len(p) > launchermcp.MaxBytes {
		w.overflow = true
		return 0, errors.New("MCP response limit")
	}
	n, err := w.body.Write(p)
	if err != nil {
		return n, fmt.Errorf("capture MCP result: %w", err)
	}
	return n, nil
}
func mcpTail(n int) int {
	if n <= 0 {
		return 100
	}
	return min(n, 1000)
}
func mcpDaemonLogs(tail int) (any, error) {
	const maxLogBytes = 48 << 10
	data, err := readTailFile(config.DaemonLogPath(), maxLogBytes)
	if err != nil {
		return nil, errors.New("daemon log unavailable")
	}
	truncated := len(data) == maxLogBytes
	lines := strings.Split(string(data), "\n")
	n := mcpTail(tail)
	if len(lines) > n {
		lines = lines[len(lines)-n:]
		truncated = true
	}
	return map[string]any{"content": strings.Join(lines, "\n"), "truncated": truncated}, nil
}

//nolint:gocyclo // Generated eligibility, rooted I/O and validation are one guarded edit transaction.
func (d *Daemon) mcpSpringFile(act activeNamespace, tool string, a launchermcp.Arguments) (any, error) {
	if !safeIDPattern.MatchString(a.App) {
		return nil, errors.New("invalid app id")
	}
	// Serialize baseline, rooted I/O and delta with native reload/reset. Native
	// edits use Runtime's same lock, so unrelated deltas cannot be lost.
	// This is a tool adapter, not another HTTP route in gatedRoutes.
	if !d.longOp.TryLock(longOpRequest) {
		return nil, errors.New("LONG_OP_IN_PROGRESS")
	}
	defer d.longOp.Unlock()
	if !d.reloadMu.TryLock() {
		return nil, errors.New("RELOAD_IN_PROGRESS")
	}
	defer d.reloadMu.Unlock()
	def, ok := act.runtime.GeneratedDef(a.App)
	if !ok || !def.IsJVM {
		return nil, errors.New("spring props app not found")
	}
	prefix := "app/" + a.App + "/props/"
	mounted := false
	for _, v := range def.Volumes {
		parts := strings.Split(v, ":")
		if len(parts) >= 2 && strings.TrimSuffix(parts[0], "/") == "./"+strings.TrimSuffix(prefix, "/") && strings.TrimSuffix(parts[1], "/") == "/run/java.io/spring-props" {
			mounted = true
		}
	}
	if !mounted {
		return nil, errors.New("app has no generated Spring props mount")
	}
	eligible := func(key string) bool {
		if !strings.HasPrefix(key, prefix) {
			return false
		}
		leaf := strings.TrimPrefix(key, prefix)
		return leaf != "" && !strings.ContainsAny(leaf, "/\\") && strings.HasPrefix(leaf, "application") && (strings.HasSuffix(leaf, ".yml") || strings.HasSuffix(leaf, ".yaml"))
	}
	root, err := launchermcp.PropsRoot(act.volumesBase, a.App)
	if err != nil {
		return nil, fmt.Errorf("mcp backend: %w", err)
	}
	defer root.Close()
	if tool == "spring_files_list" {
		keys := []string{}
		for _, key := range act.runtime.GeneratedFileKeys() {
			if !eligible(key) {
				continue
			}
			f, e := launchermcp.OpenRegular(root, strings.TrimPrefix(key, prefix))
			if e == nil {
				_ = f.Close()
				keys = append(keys, key)
			}
		}
		sort.Strings(keys)
		return keys, nil
	}
	if !eligible(a.Path) {
		return nil, errors.New("ineligible Spring YAML path")
	}
	if _, ok = act.runtime.GeneratedFile(a.Path); !ok {
		return nil, errors.New("file is not in the generated baseline")
	}
	leaf := strings.TrimPrefix(a.Path, prefix)
	if tool == "spring_file_read" {
		data, e := launchermcp.ReadYAML(root, leaf)
		if e != nil {
			return nil, fmt.Errorf("mcp backend: %w", e)
		}
		return map[string]string{"content": string(data)}, nil
	}
	reset := tool == "spring_file_reset"
	err = act.runtime.EditGeneratedFile(a.Path, []byte(a.Content), reset, func(content []byte) error {
		if len(content) > launchermcp.MaxBytes {
			return errors.New("spring YAML exceeds 256 KiB")
		}
		decoder := yaml.NewDecoder(bytes.NewReader(content))
		documents := 0
		for {
			var doc yaml.Node
			e := decoder.Decode(&doc)
			if errors.Is(e, io.EOF) {
				break
			}
			if e != nil {
				return fmt.Errorf("invalid YAML: %w", e)
			}
			if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
				return errors.New("spring YAML documents must be mappings")
			}
			documents++
		}
		if documents == 0 {
			return errors.New("spring YAML must not be empty")
		}
		return launchermcp.WriteYAML(root, leaf, content)
	})
	if err != nil {
		return nil, fmt.Errorf("mcp backend: %w", err)
	}
	return map[string]any{"accepted": true, "applyRequired": true, "stateWriteError": act.runtime.StateWriteError()}, nil
}

// Compile-time assurance: a daemon backend never exposes a raw API proxy.
var _ launchermcp.Backend = mcpBackend{}
