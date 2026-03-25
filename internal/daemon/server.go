package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/niceteck/citeck-launcher/internal/api"
	"github.com/niceteck/citeck-launcher/internal/appdef"
	"github.com/niceteck/citeck-launcher/internal/appfiles"
	"github.com/niceteck/citeck-launcher/internal/bundle"
	"github.com/niceteck/citeck-launcher/internal/config"
	"github.com/niceteck/citeck-launcher/internal/docker"
	"github.com/niceteck/citeck-launcher/internal/namespace"
	"github.com/niceteck/citeck-launcher/internal/storage"
)

// StartOptions controls daemon startup behavior.
type StartOptions struct {
	Foreground bool
	NoUI       bool // disable web UI (TCP listener)
}

// Daemon is the main daemon server.
type Daemon struct {
	dockerClient   *docker.Client
	runtime        *namespace.Runtime
	nsConfig       *namespace.NamespaceConfig
	bundleDef      *bundle.BundleDef
	workspaceConfig *bundle.WorkspaceConfig
	appDefs        []appdef.ApplicationDef
	server         *http.Server
	tcpServer      *http.Server
	store          storage.Store
	socketPath     string
	volumesBase    string
	workspaceID    string
	startTime      time.Time
	eventSubs      []chan api.EventDto
	eventMu        sync.Mutex
	configMu       sync.Mutex // protects nsConfig, bundleDef, appDefs, workspaceConfig
	shutdownOnce   sync.Once
}

// Start runs the daemon.
func Start(opts StartOptions) error {
	slog.Info("Starting daemon",
		"foreground", opts.Foreground,
		"desktop", config.IsDesktopMode(),
		"noUI", opts.NoUI,
		"home", config.HomeDir(),
	)

	socketPath := config.SocketPath()

	// Ensure directories exist
	dirs := []string{config.ConfDir(), config.DataDir(), config.LogDir(), config.RunDir()}
	if config.IsDesktopMode() {
		dirs = append(dirs, config.WorkspacesDir())
	}
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create directory %s: %w", dir, err)
		}
	}

	// Load daemon config
	daemonCfg, err := config.LoadDaemonConfig()
	if err != nil {
		slog.Warn("Failed to load daemon config, using defaults", "err", err)
		daemonCfg = config.DefaultDaemonConfig()
	}

	// Override web UI with --no-ui flag
	if opts.NoUI {
		daemonCfg.Server.WebUI.Enabled = false
	}

	// Clean up stale socket
	os.Remove(socketPath)

	// Determine workspace and namespace IDs
	wsID := "daemon"
	nsID := "default"
	if config.IsDesktopMode() {
		wsID = "default"
		// Use first available workspace if "default" doesn't exist
		if workspaces, err := config.ListWorkspaces(); err == nil && len(workspaces) > 0 {
			found := false
			for _, ws := range workspaces {
				if ws.ID == "default" || ws.ID == "DEFAULT" {
					wsID = ws.ID
					found = true
					break
				}
			}
			if !found {
				wsID = workspaces[0].ID
			}
			// Use first namespace in the selected workspace
			for _, ws := range workspaces {
				if ws.ID == wsID && len(ws.Namespaces) > 0 {
					nsID = ws.Namespaces[0]
					break
				}
			}
		}
		slog.Info("Desktop mode workspace", "wsID", wsID, "nsID", nsID)
	}

	// Create Docker client
	dockerClient, err := docker.NewClient(wsID, nsID)
	if err != nil {
		return fmt.Errorf("create docker client: %w", err)
	}
	startupFailed := true
	defer func() {
		if startupFailed {
			dockerClient.Close()
		}
	}()

	// Initialize storage backend
	var store storage.Store
	if config.IsDesktopMode() {
		store, err = storage.NewSQLiteStore(config.HomeDir())
		if err != nil {
			return fmt.Errorf("create sqlite store: %w", err)
		}
	} else {
		store, err = storage.NewFileStore(config.ConfDir())
		if err != nil {
			return fmt.Errorf("create file store: %w", err)
		}
	}
	defer func() {
		if startupFailed {
			store.Close()
		}
	}()

	// Load namespace config (mode-aware path)
	nsCfgPath := config.ResolveNamespaceConfigPath(wsID, nsID)
	nsCfg, err := namespace.LoadNamespaceConfig(nsCfgPath)
	if err != nil {
		slog.Warn("No namespace config found", "path", nsCfgPath, "err", err)
		nsCfg = nil
	}

	var bundleDef *bundle.BundleDef
	var wsCfg *bundle.WorkspaceConfig
	var runtime *namespace.Runtime
	var appDefs []appdef.ApplicationDef
	volumesBase := config.ResolveVolumesBase(wsID, nsID)

	if nsCfg != nil {
		if nsCfg.ID == "" {
			nsCfg.ID = nsID
		}

		// Resolve bundle + workspace config (with auth from stored secrets)
		tokenLookup := func(authType string) string {
			if store == nil {
				return ""
			}
			secrets, err := store.ListSecrets()
			if err != nil {
				return ""
			}
			for _, s := range secrets {
				if string(s.Type) == authType || s.Scope == authType {
					sec, err := store.GetSecret(s.ID)
					if err == nil {
						return sec.Value
					}
				}
			}
			return ""
		}
		resolver := bundle.NewResolverWithAuth(config.DataDir(), tokenLookup)
		resolveResult, err := resolver.Resolve(nsCfg.BundleRef)
		if err != nil {
			slog.Error("Failed to resolve bundle", "ref", nsCfg.BundleRef, "err", err)
			resolveResult = &bundle.ResolveResult{Bundle: &bundle.EmptyBundleDef, Workspace: &bundle.WorkspaceConfig{}}
		}
		bundleDef = resolveResult.Bundle
		wsCfg = resolveResult.Workspace

		slog.Info("Using bundle", "ref", nsCfg.BundleRef, "apps", len(bundleDef.Applications))

		// Extract appfiles to volumes base
		if err := appfiles.ExtractTo(volumesBase); err != nil {
			slog.Error("Failed to extract appfiles", "err", err)
		} else {
			slog.Info("Extracted appfiles", "dir", volumesBase)
		}

		// Generate namespace
		genResp := namespace.Generate(nsCfg, bundleDef, resolveResult.Workspace)

		// Write generated files (cloud config YAMLs, etc.) to volumes base
		for filePath, content := range genResp.Files {
			destPath := filepath.Join(volumesBase, filePath)
			if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
				slog.Error("Failed to create dir for generated file", "path", destPath, "err", err)
				continue
			}
			if err := os.WriteFile(destPath, content, 0o644); err != nil {
				slog.Error("Failed to write generated file", "path", destPath, "err", err)
			}
		}
		slog.Info("Generated namespace", "apps", len(genResp.Applications), "files", len(genResp.Files))

		appDefs = genResp.Applications
		runtime = namespace.NewRuntime(nsCfg, dockerClient, wsID, volumesBase)
		runtime.Start(appDefs)
	}

	d := &Daemon{
		dockerClient:    dockerClient,
		runtime:         runtime,
		nsConfig:        nsCfg,
		bundleDef:       bundleDef,
		workspaceConfig: wsCfg,
		appDefs:         appDefs,
		store:           store,
		socketPath:      socketPath,
		volumesBase:     volumesBase,
		workspaceID:     wsID,
		startTime:       time.Now(),
	}

	// Wire up event broadcasting
	if d.runtime != nil {
		d.runtime.SetEventCallback(func(evt api.EventDto) {
			d.broadcastEvent(evt)
		})
	}

	// Create HTTP server
	mux := http.NewServeMux()
	d.registerRoutes(mux)
	d.server = &http.Server{
		Handler:        mux,
		ReadTimeout:    30 * time.Second,
		WriteTimeout:   120 * time.Second, // long for log streaming
		MaxHeaderBytes: 1 << 20,           // 1MB
	}

	// Listen on Unix socket (for local CLI)
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", socketPath, err)
	}
	if err := os.Chmod(socketPath, 0o666); err != nil {
		slog.Warn("Failed to chmod socket", "path", socketPath, "err", err)
	}

	// TCP listener for Web UI (controlled by daemon.yml and --no-ui flag)
	tcpAddr := daemonCfg.Server.WebUI.Listen
	if daemonCfg.Server.WebUI.Enabled {
		tcpListener, err := net.Listen("tcp", tcpAddr)
		if err != nil {
			slog.Warn("TCP listener failed, Web UI unavailable", "addr", tcpAddr, "err", err)
		} else {
			d.tcpServer = &http.Server{Handler: mux}
			go func() {
				slog.Info("Web UI available", "url", "http://"+tcpAddr)
				if err := d.tcpServer.Serve(tcpListener); err != nil && err != http.ErrServerClosed {
					slog.Error("TCP server error", "err", err)
				}
			}()
		}
	} else {
		slog.Info("Web UI disabled")
	}

	slog.Info("Citeck Daemon started",
		"socket", socketPath,
		"webui", daemonCfg.Server.WebUI.Enabled,
		"tcp", tcpAddr,
		"pid", os.Getpid(),
	)

	// Handle shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigCh
		slog.Info("Shutdown signal received")
		d.shutdown()
	}()

	// Startup complete — disable cleanup defers
	startupFailed = false

	// Serve (blocks until shutdown)
	if err := d.server.Serve(listener); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("server error: %w", err)
	}

	return nil
}

func (d *Daemon) shutdown() {
	d.shutdownOnce.Do(d.doShutdown)
}

func (d *Daemon) doShutdown() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if d.runtime != nil {
		d.runtime.Shutdown()
	}

	d.server.Shutdown(ctx)
	if d.tcpServer != nil {
		d.tcpServer.Shutdown(ctx)
	}
	if d.store != nil {
		d.store.Close()
	}
	d.dockerClient.Close()
	os.Remove(d.socketPath)

	slog.Info("Daemon stopped")
}

func (d *Daemon) broadcastEvent(evt api.EventDto) {
	d.eventMu.Lock()
	defer d.eventMu.Unlock()
	for _, ch := range d.eventSubs {
		select {
		case ch <- evt:
		default: // drop if subscriber is slow
		}
	}
}

func (d *Daemon) addSubscriber() chan api.EventDto {
	ch := make(chan api.EventDto, 64)
	d.eventMu.Lock()
	d.eventSubs = append(d.eventSubs, ch)
	d.eventMu.Unlock()
	return ch
}

func (d *Daemon) removeSubscriber(ch chan api.EventDto) {
	d.eventMu.Lock()
	defer d.eventMu.Unlock()
	for i, sub := range d.eventSubs {
		if sub == ch {
			d.eventSubs = append(d.eventSubs[:i], d.eventSubs[i+1:]...)
			break
		}
	}
	close(ch)
}

func (d *Daemon) registerRoutes(mux *http.ServeMux) {
	// Daemon routes
	mux.HandleFunc("GET "+api.DaemonStatus, d.handleDaemonStatus)
	mux.HandleFunc("POST "+api.DaemonShutdown, d.handleDaemonShutdown)

	// Namespace routes
	mux.HandleFunc("GET "+api.Namespace, d.handleGetNamespace)
	mux.HandleFunc("POST "+api.NamespaceStart, d.handleStartNamespace)
	mux.HandleFunc("POST "+api.NamespaceStop, d.handleStopNamespace)
	mux.HandleFunc("POST "+api.NamespaceReload, d.handleReloadNamespace)

	// App routes
	mux.HandleFunc("GET /api/v1/apps/{name}/logs", d.handleAppLogs)
	mux.HandleFunc("POST /api/v1/apps/{name}/restart", d.handleAppRestart)
	mux.HandleFunc("POST /api/v1/apps/{name}/stop", d.handleAppStop)
	mux.HandleFunc("POST /api/v1/apps/{name}/start", d.handleAppStart)
	mux.HandleFunc("GET /api/v1/apps/{name}/inspect", d.handleAppInspect)
	mux.HandleFunc("POST /api/v1/apps/{name}/exec", d.handleAppExec)

	// Config
	mux.HandleFunc("GET /api/v1/config", d.handleGetConfig)
	mux.HandleFunc("PUT /api/v1/config", d.handlePutConfig)

	// Events (SSE)
	mux.HandleFunc("GET "+api.Events, d.handleEvents)

	// Volumes
	mux.HandleFunc("GET /api/v1/volumes", d.handleListVolumes)
	mux.HandleFunc("DELETE /api/v1/volumes/{name}", d.handleDeleteVolume)

	// App config
	mux.HandleFunc("GET /api/v1/apps/{name}/config", d.handleGetAppConfig)
	mux.HandleFunc("PUT /api/v1/apps/{name}/config", d.handlePutAppConfig)

	// Daemon logs
	mux.HandleFunc("GET /api/v1/daemon/logs", d.handleDaemonLogs)

	// System dump
	mux.HandleFunc("GET /api/v1/system/dump", d.handleSystemDump)

	// Health
	mux.HandleFunc("GET "+api.Health, d.handleHealth)

	// Phase E1: Namespaces
	mux.HandleFunc("GET "+api.Namespaces, d.handleListNamespaces)
	mux.HandleFunc("POST "+api.Namespaces, d.handleCreateNamespace)
	mux.HandleFunc("DELETE /api/v1/namespaces/{id}", d.handleDeleteNamespace)
	mux.HandleFunc("GET "+api.Templates, d.handleGetTemplates)
	mux.HandleFunc("GET "+api.QuickStarts, d.handleGetQuickStarts)

	// Phase E3: Bundles
	mux.HandleFunc("GET "+api.Bundles, d.handleListBundles)

	// Phase F1: Secrets
	mux.HandleFunc("GET "+api.Secrets, d.handleListSecrets)
	mux.HandleFunc("POST "+api.Secrets, d.handleCreateSecret)
	mux.HandleFunc("DELETE /api/v1/secrets/{id}", d.handleDeleteSecret)
	mux.HandleFunc("GET /api/v1/secrets/{id}/test", d.handleTestSecret)

	// Phase 3E: Forms
	mux.HandleFunc("GET /api/v1/forms/{formId}", d.handleGetForm)

	// Phase F2: Diagnostics
	mux.HandleFunc("GET "+api.Diagnostics, d.handleGetDiagnostics)
	mux.HandleFunc("POST "+api.DiagnosticsFix, d.handleDiagnosticsFix)

	// Phase F3: Snapshots
	mux.HandleFunc("GET "+api.Snapshots, d.handleListSnapshots)
	mux.HandleFunc("POST "+api.SnapshotsExport, d.handleExportSnapshot)
	mux.HandleFunc("POST "+api.SnapshotsImport, d.handleImportSnapshot)

	// Web UI (fallback)
	mux.Handle("/", WebUIHandler())
}

// JSON helpers

func writeJSON(w http.ResponseWriter, v any) {
	data, err := json.Marshal(v)
	if err != nil {
		http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(data)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(api.ErrorDto{
		Error:   http.StatusText(code),
		Message: msg,
	})
}

// activeConfigPath returns the namespace config path for the currently loaded namespace.
func (d *Daemon) activeConfigPath() string {
	nsID := "default"
	if d.nsConfig != nil {
		nsID = d.nsConfig.ID
	}
	return config.ResolveNamespaceConfigPath(d.workspaceID, nsID)
}

func readJSON(r *http.Request, v any) error {
	return json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(v) // 1MB max
}
