package daemon

import (
	"context"
	"encoding/json"
	"fmt"
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
)

// Daemon is the main daemon server.
type Daemon struct {
	dockerClient *docker.Client
	runtime      *namespace.Runtime
	nsConfig     *namespace.NamespaceConfig
	bundleDef    *bundle.BundleDef
	appDefs      []appdef.ApplicationDef
	server       *http.Server
	tcpServer    *http.Server
	socketPath   string
	volumesBase  string
	startTime    time.Time
	eventSubs    []chan api.EventDto
	eventMu      sync.Mutex
}

// Start runs the daemon in foreground mode.
func Start(foreground bool) error {
	slog.Info("Starting daemon", "foreground", foreground)

	socketPath := config.SocketPath()

	// Ensure directories exist
	for _, dir := range []string{config.ConfDir(), config.DataDir(), config.LogDir(), config.RunDir()} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create directory %s: %w", dir, err)
		}
	}

	// Clean up stale socket
	os.Remove(socketPath)

	// Create Docker client
	dockerClient, err := docker.NewClient("daemon", "default")
	if err != nil {
		return fmt.Errorf("create docker client: %w", err)
	}

	// Load namespace config
	nsCfgPath := config.NamespaceConfigPath()
	nsCfg, err := namespace.LoadNamespaceConfig(nsCfgPath)
	if err != nil {
		slog.Warn("No namespace config found", "path", nsCfgPath, "err", err)
		nsCfg = nil
	}

	var bundleDef *bundle.BundleDef
	var runtime *namespace.Runtime
	var appDefs []appdef.ApplicationDef
	var volumesBase string

	if nsCfg != nil {
		// Resolve bundle + workspace config
		resolver := bundle.NewResolver(config.DataDir())
		resolveResult, err := resolver.Resolve(nsCfg.BundleRef)
		if err != nil {
			slog.Error("Failed to resolve bundle", "ref", nsCfg.BundleRef, "err", err)
			resolveResult = &bundle.ResolveResult{Bundle: &bundle.EmptyBundleDef, Workspace: &bundle.WorkspaceConfig{}}
		}
		bundleDef = resolveResult.Bundle

		slog.Info("Using bundle", "ref", nsCfg.BundleRef, "apps", len(bundleDef.Applications))

		// Extract appfiles to volumes base
		volumesBase = filepath.Join(config.DataDir(), "runtime", nsCfg.ID)
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
		runtime = namespace.NewRuntime(nsCfg, dockerClient, "daemon", volumesBase)
		runtime.Start(appDefs)
	}

	d := &Daemon{
		dockerClient: dockerClient,
		runtime:      runtime,
		nsConfig:     nsCfg,
		bundleDef:    bundleDef,
		appDefs:      appDefs,
		socketPath:   socketPath,
		volumesBase:  volumesBase,
		startTime:    time.Now(),
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
	d.server = &http.Server{Handler: mux}

	// Listen on Unix socket (for local CLI)
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", socketPath, err)
	}
	if err := os.Chmod(socketPath, 0o666); err != nil {
		slog.Warn("Failed to chmod socket", "path", socketPath, "err", err)
	}

	// Also listen on TCP :8088 (for Web UI / Desktop app / remote access)
	const tcpAddr = ":8088"
	tcpListener, err := net.Listen("tcp", tcpAddr)
	if err != nil {
		slog.Warn("TCP listener failed, Web UI only on Unix socket", "addr", tcpAddr, "err", err)
	} else {
		d.tcpServer = &http.Server{Handler: mux}
		go func() {
			slog.Info("Web UI available", "url", "http://localhost"+tcpAddr)
			if err := d.tcpServer.Serve(tcpListener); err != nil && err != http.ErrServerClosed {
				slog.Error("TCP server error", "err", err)
			}
		}()
	}

	slog.Info("Citeck Daemon started", "socket", socketPath, "tcp", tcpAddr, "pid", os.Getpid())

	// Handle shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigCh
		slog.Info("Shutdown signal received")
		d.shutdown()
	}()

	// Serve
	if err := d.server.Serve(listener); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("server error: %w", err)
	}

	return nil
}

func (d *Daemon) shutdown() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if d.runtime != nil {
		d.runtime.Stop()
		d.runtime.Shutdown()
	}

	d.server.Shutdown(ctx)
	if d.tcpServer != nil {
		d.tcpServer.Shutdown(ctx)
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

func readJSON(r *http.Request, v any) error {
	return json.NewDecoder(r.Body).Decode(v)
}
