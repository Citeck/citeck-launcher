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
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/citeck/citeck-launcher/internal/acme"
	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/appfiles"
	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/docker"
	"github.com/citeck/citeck-launcher/internal/namespace"
	"github.com/citeck/citeck-launcher/internal/snapshot"
	"github.com/citeck/citeck-launcher/internal/storage"
	"github.com/citeck/citeck-launcher/internal/tlsutil"
)

// StartOptions controls daemon startup behavior.
type StartOptions struct {
	Foreground bool
	NoUI       bool   // disable web UI (TCP listener)
	Version    string // build version injected via ldflags
}

// Daemon is the main daemon server.
type Daemon struct {
	dockerClient    *docker.Client
	runtime         *namespace.Runtime
	nsConfig        *namespace.NamespaceConfig
	bundleDef       *bundle.BundleDef
	workspaceConfig *bundle.WorkspaceConfig
	appDefs         []appdef.ApplicationDef
	server          *http.Server
	tcpServer       *http.Server
	cloudCfgServer  *CloudConfigServer
	store           storage.Store
	socketPath      string
	volumesBase     string
	workspaceID     string
	startTime       time.Time
	eventSubs       []chan api.EventDto
	eventMu         sync.Mutex
	configMu        sync.RWMutex // protects nsConfig, bundleDef, appDefs, workspaceConfig
	version         string
	bundleError     string // non-empty if bundle resolution failed
	acmeRenewal     *acme.RenewalService
	shutdownOnce    sync.Once
	bgCtx           context.Context    // cancelled on daemon shutdown
	bgCancel        context.CancelFunc
	bgWg            sync.WaitGroup     // tracks background goroutines (snapshot, downloads)
	snapshotMu      sync.Mutex         // guards concurrent snapshot import/export
	daemonCfg       config.DaemonConfig
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

	// Check if another daemon is already running via socket lock
	if conn, err := net.DialTimeout("unix", socketPath, 2*time.Second); err == nil {
		conn.Close()
		return fmt.Errorf("another daemon is already running (socket %s is active)", socketPath)
	}
	// Socket exists but nobody listening — stale, safe to remove
	os.Remove(socketPath)

	// Determine workspace and namespace IDs
	wsID := "daemon"
	nsID := "default"
	if config.IsDesktopMode() {
		wsID = "default"

		// Try SQLiteStore for preferred workspace/namespace from launcher_state
		if sqlStore, sqlErr := storage.NewSQLiteStore(config.HomeDir()); sqlErr == nil {
			if state, stateErr := sqlStore.GetState(); stateErr == nil {
				if state.WorkspaceID != "" {
					wsID = state.WorkspaceID
				}
				if state.NamespaceID != "" {
					nsID = state.NamespaceID
				}
			}
			sqlStore.Close()
		}

		// Fallback: use first available workspace if stored one doesn't exist
		if workspaces, err := config.ListWorkspaces(); err == nil && len(workspaces) > 0 {
			// Verify the stored wsID exists
			wsExists := false
			for _, ws := range workspaces {
				if ws.ID == wsID {
					wsExists = true
					break
				}
			}
			if !wsExists {
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
			}
			// Use first namespace in the selected workspace if nsID still default
			if nsID == "default" {
				for _, ws := range workspaces {
					if ws.ID == wsID && len(ws.Namespaces) > 0 {
						nsID = ws.Namespaces[0]
						break
					}
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
	var cloudCfgSrv *CloudConfigServer
	var bundleError string
	volumesBase := config.ResolveVolumesBase(wsID, nsID)

	if nsCfg != nil {
		if nsCfg.ID == "" {
			nsCfg.ID = nsID
		}

		// Resolve bundle + workspace config (with auth from stored secrets)
		resolver := bundle.NewResolverWithAuth(config.DataDir(), makeTokenLookup(store))
		resolveResult, err := resolver.Resolve(nsCfg.BundleRef)
		if err != nil {
			slog.Error("Failed to resolve bundle — daemon starts with 0 apps", "ref", nsCfg.BundleRef, "err", err)
			bundleError = err.Error()
			resolveResult = &bundle.ResolveResult{Bundle: &bundle.EmptyBundleDef, Workspace: &bundle.WorkspaceConfig{}}
		}
		bundleDef = resolveResult.Bundle
		wsCfg = resolveResult.Workspace

		slog.Info("Using bundle", "ref", nsCfg.BundleRef, "apps", len(bundleDef.Applications))

		// TLS certificate provisioning at startup
		// Self-signed: generate if TLS enabled + no cert paths + no LE
		if nsCfg.Proxy.TLS.Enabled && !nsCfg.Proxy.TLS.LetsEncrypt && nsCfg.Proxy.TLS.CertPath == "" {
			host := nsCfg.Proxy.Host
			if host == "" {
				host = "localhost"
			}
			tlsDir := filepath.Join(config.ConfDir(), "tls")
			os.MkdirAll(tlsDir, 0o755)
			certPath := filepath.Join(tlsDir, "server.crt")
			keyPath := filepath.Join(tlsDir, "server.key")
			if !isRegularFile(certPath) {
				slog.Info("Generating self-signed certificate", "host", host)
				if err := generateSelfSignedCert(certPath, keyPath, host); err != nil {
					slog.Error("Failed to generate self-signed cert", "err", err)
				}
			}
			nsCfg.Proxy.TLS.CertPath = certPath
			nsCfg.Proxy.TLS.KeyPath = keyPath
		}

		// Let's Encrypt: obtain certificate if configured and not yet present
		if nsCfg.Proxy.TLS.Enabled && nsCfg.Proxy.TLS.LetsEncrypt {
			host := nsCfg.Proxy.Host
			if host == "" || host == "localhost" {
				slog.Warn("Let's Encrypt requires a public hostname, skipping")
			} else {
				acmeClient := acme.NewClient(config.DataDir(), config.ConfDir(), host)
				certPath := acmeClient.CertPath()
				keyPath := acmeClient.KeyPath()

				// Obtain cert if not yet present or if host changed
				if !acmeClient.CertMatchesHost() {
					slog.Info("Obtaining Let's Encrypt certificate", "host", host)
					acmeCtx, acmeCancel := context.WithTimeout(context.Background(), 120*time.Second)
					err := acmeClient.ObtainCertificate(acmeCtx)
					acmeCancel()
					if err != nil {
						slog.Error("Let's Encrypt certificate obtainment failed", "err", err)
					} else {
						slog.Info("Let's Encrypt certificate obtained", "cert", certPath)
					}
				}

				// Update config to use ACME cert paths (only if cert is a regular file)
				if isRegularFile(certPath) {
					nsCfg.Proxy.TLS.CertPath = certPath
					nsCfg.Proxy.TLS.KeyPath = keyPath
				}
			}
		}

		// Extract appfiles to volumes base
		if err := appfiles.ExtractTo(volumesBase); err != nil {
			slog.Error("Failed to extract appfiles", "err", err)
		} else {
			slog.Info("Extracted appfiles", "dir", volumesBase)
		}

		// Load persisted state for detached apps and status recovery
		persistedState := namespace.LoadNsState(volumesBase, nsID)
		var genOpts namespace.GenerateOpts
		if persistedState != nil {
			genOpts.DetachedApps = make(map[string]bool)
			for _, name := range persistedState.ManualStoppedApps {
				genOpts.DetachedApps[name] = true
			}
		}

		// Generate namespace
		genResp := namespace.Generate(nsCfg, bundleDef, resolveResult.Workspace, genOpts)

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

		// Wire registry auth and operation history into runtime
		runtime.SetRegistryAuthFunc(makeRegistryAuthFunc(wsCfg, store))
		runtime.SetHistory(namespace.NewOperationHistory(config.LogDir()))

		// Apply daemon.yml overrides for reconciler and pull concurrency
		if daemonCfg.Reconciler.IntervalSeconds > 0 || daemonCfg.Reconciler.LivenessPeriodMs > 0 {
			rcfg := namespace.DefaultReconcilerConfig()
			if daemonCfg.Reconciler.IntervalSeconds > 0 {
				rcfg.IntervalSeconds = daemonCfg.Reconciler.IntervalSeconds
			}
			if daemonCfg.Reconciler.LivenessPeriodMs > 0 {
				rcfg.LivenessPeriod = time.Duration(daemonCfg.Reconciler.LivenessPeriodMs) * time.Millisecond
			}
			runtime.SetReconcilerConfig(rcfg)
		}
		if daemonCfg.Docker.PullConcurrency > 0 {
			runtime.SetPullConcurrency(daemonCfg.Docker.PullConcurrency)
		}

		// Restore persisted state: manual stopped apps, edited apps, locked apps
		if persistedState != nil {
			if len(persistedState.ManualStoppedApps) > 0 {
				stopped := make(map[string]bool)
				for _, name := range persistedState.ManualStoppedApps {
					stopped[name] = true
				}
				runtime.SetManualStoppedApps(stopped)
			}
			runtime.RestoreEditedApps(persistedState.EditedApps, persistedState.EditedLockedApps)
		}

		// Start CloudConfigServer with generated ext cloud config
		cloudCfgSrv = NewCloudConfigServer()
		cloudCfgSrv.UpdateConfig(genResp.CloudConfig)
		if err := cloudCfgSrv.Start(); err != nil {
			slog.Warn("CloudConfigServer failed to start", "err", err)
		}

		// Status recovery: if previous status was RUNNING/STARTING/STALLED → start namespace
		// If STOPPING → leave stopped (interrupted stop completed by clean restart)
		shouldStart := true
		if persistedState != nil && persistedState.Status == namespace.NsStatusStopping {
			slog.Info("Previous status was STOPPING — not auto-starting")
			shouldStart = false
		}
		// Snapshot auto-import: run synchronously BEFORE start so volumes are populated
		if nsCfg.Snapshot != "" {
			slog.Info("Running snapshot auto-import before namespace start", "snapshot", nsCfg.Snapshot)
			importSnapshotIfNeeded(nsCfg, wsCfg, dockerClient, volumesBase)
		}

		if shouldStart {
			runtime.Start(appDefs)
		}
	}

	bgCtx, bgCancel := context.WithCancel(context.Background())

	d := &Daemon{
		dockerClient:    dockerClient,
		runtime:         runtime,
		nsConfig:        nsCfg,
		bundleDef:       bundleDef,
		workspaceConfig: wsCfg,
		appDefs:         appDefs,
		cloudCfgServer:  cloudCfgSrv,
		store:           store,
		socketPath:      socketPath,
		volumesBase:     volumesBase,
		workspaceID:     wsID,
		version:         opts.Version,
		bundleError:     bundleError,
		startTime:       time.Now(),
		bgCtx:           bgCtx,
		bgCancel:        bgCancel,
		daemonCfg:       daemonCfg,
	}

	// Wire up event broadcasting
	if d.runtime != nil {
		d.runtime.SetEventCallback(func(evt api.EventDto) {
			d.broadcastEvent(evt)
		})
	}

	// Start ACME renewal service if Let's Encrypt is enabled
	if nsCfg != nil && nsCfg.Proxy.TLS.Enabled && nsCfg.Proxy.TLS.LetsEncrypt && nsCfg.Proxy.Host != "" {
		acmeClient := acme.NewClient(config.DataDir(), config.ConfDir(), nsCfg.Proxy.Host)
		d.acmeRenewal = acme.NewRenewalService(acmeClient, func() {
			if d.runtime != nil {
				if err := d.runtime.RestartApp("proxy"); err != nil {
					slog.Error("ACME: restart proxy after renewal failed", "err", err)
				}
			}
		})
		d.acmeRenewal.Start()
	}

	// Create HTTP server
	mux := http.NewServeMux()
	d.registerRoutes(mux)
	d.server = &http.Server{
		Handler:        mux,
		ReadTimeout:    30 * time.Second,
		WriteTimeout:   0, // 0 for SSE/log streaming on Unix socket
		MaxHeaderBytes: 1 << 20, // 1MB
	}

	// Listen on Unix socket (for local CLI)
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", socketPath, err)
	}
	socketPerm := os.FileMode(0o600)
	if config.IsDesktopMode() {
		socketPerm = 0o666
	}
	if err := os.Chmod(socketPath, socketPerm); err != nil {
		slog.Warn("Failed to chmod socket", "path", socketPath, "err", err)
	}

	// TCP listener for Web UI (controlled by daemon.yml and --no-ui flag)
	tcpAddr := daemonCfg.Server.WebUI.Listen
	if daemonCfg.Server.WebUI.Enabled {
		tcpListener, err := net.Listen("tcp", tcpAddr)
		if err != nil {
			slog.Warn("TCP listener failed, Web UI unavailable", "addr", tcpAddr, "err", err)
		} else {
			// Wrap middleware for TCP connections.
			// Order (outermost first): CORS → TokenAuth → Logging → mux
			// CORS must be outermost so OPTIONS preflight bypasses auth.
			var tcpHandler http.Handler = mux
			tcpHandler = RateLimitMiddleware(100, tcpHandler)
			tcpHandler = LoggingMiddleware(tcpHandler)
			if daemonCfg.Server.Token != "" {
				tcpHandler = TokenAuthMiddleware(daemonCfg.Server.Token, tcpHandler)
				slog.Info("Token auth enabled on TCP listener")
			}
			tcpHandler = CORSMiddleware(tcpHandler)
			d.tcpServer = &http.Server{
				Handler:        tcpHandler,
				ReadTimeout:    30 * time.Second,
				WriteTimeout:   0, // 0 for SSE streaming — use per-handler timeouts for non-SSE
				IdleTimeout:    120 * time.Second,
				MaxHeaderBytes: 1 << 20,
			}
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

	// Handle shutdown — second signal forces immediate exit
	sigCh := make(chan os.Signal, 2)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigCh
		slog.Info("Shutdown signal received")
		go func() {
			<-sigCh
			slog.Warn("Second signal received, forcing exit")
			os.Exit(1)
		}()
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

	// Cancel background goroutines (downloads, snapshot imports) and wait
	d.bgCancel()
	d.bgWg.Wait()

	if d.runtime != nil {
		d.runtime.Shutdown()
	}
	if d.cloudCfgServer != nil {
		d.cloudCfgServer.Stop()
	}
	d.configMu.RLock()
	renewal := d.acmeRenewal
	d.configMu.RUnlock()
	if renewal != nil {
		renewal.Stop()
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
		default:
			slog.Warn("Event dropped for slow subscriber", "type", evt.Type)
		}
	}
}

const maxSSESubscribers = 100

func (d *Daemon) addSubscriber() (chan api.EventDto, bool) {
	d.eventMu.Lock()
	defer d.eventMu.Unlock()
	if len(d.eventSubs) >= maxSSESubscribers {
		return nil, false
	}
	ch := make(chan api.EventDto, 256)
	d.eventSubs = append(d.eventSubs, ch)
	return ch, true
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
	mux.HandleFunc("PUT /api/v1/apps/{name}/lock", d.handleAppLockToggle)
	mux.HandleFunc("GET /api/v1/apps/{name}/files", d.handleListAppFiles)
	mux.HandleFunc("GET /api/v1/apps/{name}/files/{path...}", d.handleGetAppFile)
	mux.HandleFunc("PUT /api/v1/apps/{name}/files/{path...}", d.handlePutAppFile)

	// Daemon logs
	mux.HandleFunc("GET /api/v1/daemon/logs", d.handleDaemonLogs)

	// System dump
	mux.HandleFunc("GET /api/v1/system/dump", d.handleSystemDump)

	// Health + Metrics
	mux.HandleFunc("GET "+api.Health, d.handleHealth)
	mux.HandleFunc("GET /api/v1/metrics", d.handleMetrics)

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

	// Forms
	mux.HandleFunc("GET /api/v1/forms/{formId}", d.handleGetForm)

	// Phase F2: Diagnostics
	mux.HandleFunc("GET "+api.Diagnostics, d.handleGetDiagnostics)
	mux.HandleFunc("POST "+api.DiagnosticsFix, d.handleDiagnosticsFix)

	// Phase F3: Snapshots
	mux.HandleFunc("GET "+api.Snapshots, d.handleListSnapshots)
	mux.HandleFunc("POST "+api.SnapshotsExport, d.handleExportSnapshot)
	mux.HandleFunc("POST "+api.SnapshotsImport, d.handleImportSnapshot)
	mux.HandleFunc("POST "+api.SnapshotsDownload, d.handleDownloadSnapshot)
	mux.HandleFunc("GET "+api.WorkspaceSnapshots, d.handleWorkspaceSnapshots)
	mux.HandleFunc("PUT /api/v1/snapshots/{name}", d.handleRenameSnapshot)

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
	d.configMu.RLock()
	defer d.configMu.RUnlock()
	nsID := "default"
	if d.nsConfig != nil {
		nsID = d.nsConfig.ID
	}
	return config.ResolveNamespaceConfigPath(d.workspaceID, nsID)
}

func readJSON(r *http.Request, v any) error {
	return json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(v) // 1MB max
}

// makeTokenLookup creates a function that looks up auth tokens from the secret store.
func makeTokenLookup(store storage.Store) bundle.TokenLookupFunc {
	return func(authType string) string {
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
}

// makeRegistryAuthFunc creates a function that returns Docker registry credentials
// by matching image host against workspace config's imageReposByHost and looking up
// stored secrets at call time (not cached — reflects secret create/delete/rotate).
func makeRegistryAuthFunc(wsCfg *bundle.WorkspaceConfig, store storage.Store) namespace.RegistryAuthFunc {
	if wsCfg == nil || store == nil {
		return nil
	}
	reposByHost := wsCfg.ImageReposByHost()

	return func(img string) *docker.RegistryAuth {
		host := img
		if idx := strings.Index(host, "/"); idx > 0 {
			host = host[:idx]
		}
		repo, ok := reposByHost[host]
		if !ok || repo.AuthType == "" {
			return nil
		}
		// Look up secrets live from the store (reflects latest secret mutations)
		value := lookupSecretValue(store, repo.AuthType, host)
		if value == "" {
			return nil
		}
		parts := strings.SplitN(value, ":", 2)
		if len(parts) != 2 {
			return nil
		}
		return &docker.RegistryAuth{
			Username: parts[0],
			Password: parts[1],
			Registry: "https://" + host,
		}
	}
}

// lookupSecretValue looks up a secret value by matching scope against authType or host.
// Scope-based matching only — avoids cross-registry credential leakage from type-based fallback.
func lookupSecretValue(store storage.Store, authType, host string) string {
	secrets, err := store.ListSecrets()
	if err != nil {
		return ""
	}
	for _, s := range secrets {
		if s.Scope != "" && (s.Scope == authType || s.Scope == host) {
			sec, err := store.GetSecret(s.ID)
			if err != nil {
				continue
			}
			return sec.Value
		}
	}
	return ""
}

// importSnapshotIfNeeded checks for the snapshot field in namespace config and imports
// the snapshot if it hasn't been imported yet (tracked by a marker file).
func importSnapshotIfNeeded(nsCfg *namespace.NamespaceConfig, wsCfg *bundle.WorkspaceConfig, dc *docker.Client, volumesBase string) {
	if nsCfg.Snapshot == "" || wsCfg == nil {
		return
	}

	markerDir := filepath.Join(volumesBase, "snapshots")
	os.MkdirAll(markerDir, 0o755)
	markerFile := filepath.Join(markerDir, "imported-"+nsCfg.ID)

	// Check marker — if already imported this snapshot, skip
	if data, err := os.ReadFile(markerFile); err == nil {
		if strings.TrimSpace(string(data)) == nsCfg.Snapshot {
			slog.Info("Snapshot already imported", "snapshot", nsCfg.Snapshot, "ns", nsCfg.ID)
			return
		}
	}

	snapDef := bundle.FindSnapshot(wsCfg, nsCfg.Snapshot)
	if snapDef == nil {
		slog.Warn("Snapshot not found in workspace config", "id", nsCfg.Snapshot)
		return
	}

	slog.Info("Auto-importing snapshot on startup", "snapshot", snapDef.Name, "ns", nsCfg.ID)

	// Download to snapshots dir — strip query params for safe filename
	fileName := safeSnapshotFileName(snapDef.URL)
	if !strings.HasSuffix(fileName, ".zip") {
		fileName += ".zip"
	}
	destPath := filepath.Join(markerDir, fileName)

	// Download if needed; verify SHA256 of existing file
	needsDownload := true
	if _, err := os.Stat(destPath); err == nil {
		if snapDef.SHA256 != "" {
			if actual, err := snapshot.FileSHA256(destPath); err == nil && strings.EqualFold(actual, snapDef.SHA256) {
				needsDownload = false
			} else {
				os.Remove(destPath) // corrupted — re-download
			}
		} else {
			needsDownload = false
		}
	}
	if needsDownload {
		if dlErr := snapshot.Download(context.Background(), snapDef.URL, destPath, snapDef.SHA256, nil); dlErr != nil {
			slog.Error("Snapshot download failed", "url", snapDef.URL, "err", dlErr)
			return
		}
	}

	// Import
	if _, err := snapshot.Import(context.Background(), dc, destPath, volumesBase); err != nil {
		slog.Error("Snapshot import failed", "err", err)
		return
	}

	// Write marker
	os.WriteFile(markerFile, []byte(nsCfg.Snapshot), 0o644)
	slog.Info("Snapshot auto-import completed", "snapshot", nsCfg.Snapshot, "ns", nsCfg.ID)
}

// isRegularFile returns true if path exists and is a regular file.
func isRegularFile(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.Mode().IsRegular()
}

// generateSelfSignedCert creates a self-signed TLS certificate (365-day validity).
func generateSelfSignedCert(certPath, keyPath, host string) error {
	return tlsutil.GenerateSelfSignedCert(certPath, keyPath, []string{host}, 365)
}
