package daemon

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/fsutil"
	"github.com/citeck/citeck-launcher/internal/jvmattach"
	"github.com/citeck/citeck-launcher/internal/launchermcp"
	"github.com/citeck/citeck-launcher/internal/namespace"
	"gopkg.in/yaml.v3"
)

func heapName(name string) bool {
	return name != "" && !strings.HasPrefix(name, ".") && name == filepath.Base(name) && !strings.ContainsAny(name, "/\\:") && (strings.HasSuffix(name, ".hprof") || strings.HasSuffix(name, ".hprof.gz"))
}
func heapRoot(act activeNamespace, app string) (*os.Root, error) {
	if !safeIDPattern.MatchString(app) {
		return nil, errors.New("invalid app id")
	}
	if _, ok := runtimeAppDef(act.runtime, app); !ok {
		return nil, errors.New("APP_NOT_FOUND")
	}
	root, err := fsutil.OpenCanonicalRoot(act.volumesBase)
	if err != nil {
		return nil, fmt.Errorf("mcp diagnostics: %w", err)
	}
	defer root.Close()
	parent, _, err := fsutil.RootedParent(root, "export/"+app+"/.artifact", false)
	if err != nil {
		return nil, fmt.Errorf("open heap export directory: %w", err)
	}
	return parent, nil
}
func (d *Daemon) registerHeap(ctx context.Context, act activeNamespace, app, name string) (launchermcp.Artifact, error) {
	if !heapName(name) {
		return launchermcp.Artifact{}, errors.New("only a listed .hprof/.hprof.gz heap file is downloadable")
	}
	root, err := heapRoot(act, app)
	if err != nil {
		return launchermcp.Artifact{}, err
	}
	defer root.Close()
	f, err := fsutil.OpenRegularRooted(root, name)
	if err != nil {
		return launchermcp.Artifact{}, fmt.Errorf("mcp diagnostics: %w", err)
	}
	a, err := d.mcp.RegisterArtifact(launchermcp.TokenID(ctx), name, f, nil, false)
	if err != nil {
		return a, fmt.Errorf("register heap artifact: %w", err)
	}
	return a, nil
}

//nolint:gocyclo,nestif // Closed diagnostic dispatcher preserves per-operation refusal and cleanup.
func (d *Daemon) mcpDiagnostic(ctx context.Context, act activeNamespace, tool string, a launchermcp.Arguments) (any, error) {
	if d.mcp == nil {
		return nil, errors.New("MCP unavailable")
	}
	if tool == "system_dump_create" {
		var nsData, daemonData []byte
		var err error
		if act.nsConfig != nil {
			nsData, err = namespace.MarshalNamespaceConfig(maskNamespaceConfigSecrets(act.nsConfig))
			if err != nil {
				return nil, fmt.Errorf("mcp diagnostics: %w", err)
			}
		}
		if _, e := os.Stat(config.DaemonConfigPath()); e == nil {
			cfg := d.daemonCfg
			if cfg.APIAuth.Token != "" {
				cfg.APIAuth.Token = "***REDACTED***"
			}
			daemonData, err = yaml.Marshal(cfg)
			if err != nil {
				return nil, fmt.Errorf("mcp diagnostics: %w", err)
			}
		}
		artifact, err := d.mcp.CreateSystemArtifact(ctx, func(w io.Writer) error {
			return d.streamSystemDumpZip(ctx, w, act, d.buildDumpDataFor(ctx, act), nsData, daemonData)
		})
		if err != nil {
			return diagnosticError(ctx, err)
		}
		return map[string]any{"outcome": "completed", "artifact": artifact}, nil
	}
	if !safeIDPattern.MatchString(a.App) {
		return nil, errors.New("invalid app id")
	}
	if tool == "heap_dumps_list" {
		root, err := heapRoot(act, a.App)
		if err != nil {
			return nil, err
		}
		defer root.Close()
		dir, err := root.Open(".")
		if err != nil {
			return nil, fmt.Errorf("mcp diagnostics: %w", err)
		}
		defer dir.Close()
		entries, err := dir.ReadDir(1001)
		if err != nil && !errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("mcp diagnostics: %w", err)
		}
		if len(entries) > 1000 {
			return nil, errors.New("heap directory listing exceeds 1000 entries")
		}
		out := []map[string]any{}
		for _, e := range entries {
			if !heapName(e.Name()) {
				continue
			}
			f, err := fsutil.OpenRegularRooted(root, e.Name())
			if err != nil {
				continue
			}
			info, err := f.Stat()
			_ = f.Close()
			if err == nil {
				out = append(out, map[string]any{"name": e.Name(), "size": info.Size(), "modified": info.ModTime().UTC()})
			}
		}
		sort.Slice(out, func(i, j int) bool { return fmt.Sprint(out[i]["name"]) < fmt.Sprint(out[j]["name"]) })
		return out, nil
	}
	if tool == "heap_dump_download" {
		artifact, err := d.registerHeap(ctx, act, a.App, a.HeapFile)
		if err != nil {
			return nil, err
		}
		return artifact, nil
	}
	limited, budget := fsutil.WithOutputLimit(ctx, 32<<10)
	if tool == "heap_dump_create" {
		name, _, err := act.runtime.HeapDump(limited, a.App, time.Now())
		if err != nil {
			return diagnosticError(ctx, err)
		}
		artifact, err := d.registerHeap(ctx, act, a.App, name)
		if err != nil {
			return nil, err
		}
		return map[string]any{"outcome": "completed", "artifact": artifact}, nil
	}
	if a.Command == "" || strings.ContainsRune(a.Command, '\x00') {
		return nil, errors.New("jcmd command required; NUL is not allowed")
	}
	for _, v := range a.Args {
		if strings.ContainsRune(v, '\x00') {
			return nil, errors.New("NUL is not allowed in jcmd arguments")
		}
	}
	output, err := act.runtime.JVMCommand(limited, a.App, a.Command, a.Args...)
	if err != nil {
		verdict, failure := diagnosticError(ctx, err)
		if unknown, ok := verdict.(map[string]any); ok {
			unknown["output"] = output[:min(len(output), budget.Bytes)]
			unknown["truncated"] = budget.Truncated.Load() || len(output) > budget.Bytes
		}
		return verdict, failure
	}
	// Mocks/alternate runtime backends must not bypass the output cap either.
	truncated := budget.Truncated.Load() || len(output) > 32<<10
	if len(output) > 32<<10 {
		output = output[:32<<10]
	}
	outcome := "completed"
	if truncated {
		outcome = "unknown"
	}
	return map[string]any{"outcome": outcome, "output": output, "truncated": truncated, "replaySafe": false}, nil
}
func diagnosticError(ctx context.Context, err error) (any, error) {
	var dispatched *jvmattach.DispatchError
	if ctx.Err() != nil || (errors.As(err, &dispatched) && dispatched.Uncertain) {
		return map[string]any{"outcome": "unknown", "message": "Observation lost, canceled or timed out; a requested JVM action may still finish. Do not replay automatically.", "replaySafe": false}, nil
	}
	status := 500
	code := api.ErrCodeInvalidRequest
	switch {
	case errors.Is(err, namespace.ErrNotJVMApp):
		status = 400
	case errors.Is(err, namespace.ErrAppNotRunning):
		status = 409
	case errors.Is(err, namespace.ErrAppUnknown):
		status = 404
		code = api.ErrCodeAppNotFound
	}
	return nil, &launchermcp.ToolError{Status: status, Code: code, Message: err.Error()}
}
