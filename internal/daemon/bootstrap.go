package daemon

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	goruntime "runtime"
	"sync"
	"syscall"
	"time"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/docker"
	"github.com/citeck/citeck-launcher/internal/fsutil"
	"github.com/citeck/citeck-launcher/internal/git"
	"github.com/citeck/citeck-launcher/internal/h2migrate"
	"github.com/citeck/citeck-launcher/internal/license"
	"github.com/citeck/citeck-launcher/internal/namespace"
	"github.com/citeck/citeck-launcher/internal/storage"
	"github.com/citeck/citeck-launcher/internal/update"
)

// Daemon bootstrap: Start() drives the full startup sequence through named
// stage helpers (logging → dirs/config → single-instance guard → startup
// target → docker → store → secrets → namespace load → HTTP servers →
// shutdown wiring) and blocks serving the Unix socket until shutdown.

// ErrShutdownRequested is returned by Start when an external context is canceled.
var ErrShutdownRequested = errors.New("shutdown requested")

var (
	logInitOnce     sync.Once
	globalLogLevel  slog.LevelVar
	globalLogWriter *fsutil.RotatingWriter
)

// StartOptions controls daemon startup behavior.
type StartOptions struct {
	Foreground     bool
	Desktop        bool            // desktop mode: file-only logging, no signal handler
	NoUI           bool            // disable web UI (TCP listener)
	Offline        bool            // skip all git operations, fail if local data missing
	Version        string          // build version injected via ldflags
	MasterPassword string          // master password for secrets decryption (server mode)
	Ctx            context.Context // external context (desktop provides; nil = CLI uses signals)
	ReadyCh        chan<- string   // receives Web UI URL when ready (empty string if no UI); nil = ignored
	LogWriter      io.Writer       // additional log destination (desktop captures startup logs); nil = ignored
}

// setupDaemonLogging applies desktop mode and initializes the process-global
// rotating log writer + slog default. ORDER MATTERS: desktop mode MUST be
// applied before any filesystem path is resolved, because config.LogDir() /
// DaemonLogPath() (and every other path) branch on IsDesktopMode(). In the
// desktop thin-wrapper the daemon is a separate child of the SERVER binary
// launched with --desktop, so IsDesktopMode() is false on entry. If the log
// writer were initialized first it would target the SERVER log path
// (/opt/citeck/log), the open would fail for an unprivileged desktop user,
// RotatingWriter would swallow the error, and the desktop daemon would write
// no daemon.log at all.
func setupDaemonLogging(opts StartOptions) {
	if opts.Desktop {
		config.SetDesktopMode(true)
	}

	// Set up log rotation once — survives daemon restarts in desktop mode.
	// The RotatingWriter and slog default are process-global; re-creating them
	// on every Start() would close the previous writer while the new logger
	// references a fresh one, leaving a gap where slog writes to a closed writer.
	logInitOnce.Do(func() {
		logDir := config.LogDir()
		_ = os.MkdirAll(logDir, 0o755) //nolint:gosec // G301: log dir needs 0o755
		logPath := config.DaemonLogPath()
		globalLogWriter = fsutil.NewRotatingWriter(logPath, 50*1024*1024, 3)
	})
	// Rebuild slog handler on every Start — the optional LogWriter may change between retries.
	logDest := io.MultiWriter(os.Stderr, globalLogWriter)
	if opts.LogWriter != nil {
		logDest = io.MultiWriter(os.Stderr, globalLogWriter, opts.LogWriter)
	}
	logHandler := fsutil.NewCleanLogHandler(logDest, &globalLogLevel)
	slog.SetDefault(slog.New(logHandler))
}

// Start runs the full daemon lifecycle — logging, storage, Docker, config,
// TLS, ACME, runtime, and the HTTP/Unix-socket servers — and blocks until
// shutdown is requested.
func Start(opts StartOptions) error {
	setupDaemonLogging(opts)

	slog.Info("Starting daemon",
		"foreground", opts.Foreground,
		"desktop", opts.Desktop,
		"noUI", opts.NoUI,
		"home", config.HomeDir(),
	)

	socketPath := config.SocketPath()

	if err := ensureDaemonDirs(); err != nil {
		return err
	}
	daemonCfg := loadEffectiveDaemonConfig(opts)
	if err := guardSingleDaemonInstance(socketPath); err != nil {
		return err
	}

	wsID, nsID, err := resolveStartupTarget()
	if err != nil {
		return err
	}

	// Create Docker client
	// Server mode: no workspace in container names. Desktop: include workspace for Kotlin compat.
	dockerWorkspace := ""
	if config.IsDesktopMode() {
		dockerWorkspace = wsID
	}
	dockerClient, err := docker.NewClient(dockerWorkspace, nsID)
	if err != nil {
		return fmt.Errorf("create docker client: %w", err)
	}
	startupFailed := true
	defer func() {
		if startupFailed {
			_ = dockerClient.Close()
		}
	}()

	store, err := initStore()
	if err != nil {
		return err
	}
	defer func() {
		if startupFailed {
			_ = store.Close()
		}
	}()

	// Wire git package's persistent sync-state hook so the throttle window
	// (Kotlin parity: git-repo!instances) survives daemon restart. Without
	// this every workspace/bundle repo would re-pull on cold start.
	git.SetSyncStateStore(gitSyncStoreAdapter{store: store}, config.HomeDir())

	secretSvc, err := initSecretService(store, opts)
	if err != nil {
		return err
	}

	// Back-fill SecretID links for pre-secret-reference workspaces (and 1.x
	// H2-migrated ones) so the UI picker shows the association. Locked store
	// defers this to the unlock path (rebuildAuthCaches).
	migrateWorkspaceSecretLinks(store, secretSvc)

	// Load namespace state (config + bundle + secrets + generator + runtime).
	// loadNamespace returns a non-nil result with a nil NsConfig when there's
	// no namespace.yml on disk — the daemon still boots into the wizard.
	loaded, err := loadNamespace(loadNamespaceInput{
		Store:         store,
		SecretService: secretSvc,
		DockerClient:  dockerClient,
		DaemonCfg:     daemonCfg,
		Licenses:      license.NewService(secretSvc),
		WorkspaceID:   wsID,
		NamespaceID:   nsID,
		Offline:       opts.Offline,
		Desktop:       opts.Desktop,
	})
	if err != nil {
		return err
	}
	nsCfg := loaded.NsConfig

	// Startup orphan-sweep (desktop only): remove Docker resources for
	// namespaces that no longer exist in storage. These pile up from the
	// migration-test churn — a deleted/wiped namespace whose containers keep
	// running (detach leaves them up across restarts) squats the host ports the
	// active namespace needs and its data volumes eat disk. Running it BEFORE
	// the active namespace starts means start doesn't have to evict port
	// squatters first. Bounded + fail-safe inside sweepOrphanDockerResources.
	if config.IsDesktopMode() {
		sweepCtx, sweepCancel := context.WithTimeout(context.Background(), 90*time.Second)
		sweepOrphanDockerResources(sweepCtx, dockerClient, store, wsID, nsID)
		sweepCancel()
	}

	if nsCfg != nil {
		// Snapshot import is a USER action only — namespace creation with a
		// selected snapshot (handleCreateNamespace) or an explicit import from the
		// snapshots list. There is deliberately NO auto-import on daemon start: a
		// `snapshot:` field in the config is just a record of which snapshot the
		// namespace was created from, not a trigger. Re-importing it on boot would
		// clobber the namespace's live volumes — e.g. it restored a stale demo
		// snapshot over a 1.x→2.x migrated namespace and corrupted its postgres.
		if loaded.ShouldStart {
			loaded.Runtime.Start(loaded.AppDefs)
		}
	}

	bgCtx, bgCancel := context.WithCancel(context.Background()) //nolint:gosec // G118: bgCancel stored in Daemon struct, called in shutdown

	d := &Daemon{
		activeNs: &activeNamespace{
			workspaceID:     wsID,
			dockerClient:    dockerClient,
			runtime:         loaded.Runtime,
			nsConfig:        nsCfg,
			bundleDef:       loaded.BundleDef,
			workspaceConfig: loaded.WorkspaceConfig,
			appDefs:         loaded.AppDefs,
			cloudCfgServer:  loaded.CloudCfgServer,
			systemSecrets:   loaded.SystemSecrets,
			volumesBase:     loaded.VolumesBase,
			bundleError:     loaded.BundleError,
			wsSyncError:     loaded.WsSyncError,
		},
		store:         store,
		secretService: secretSvc,
		socketPath:    socketPath,
		version:       opts.Version,
		startTime:     time.Now(),
		bgCtx:         bgCtx,
		bgCancel:      bgCancel,
		daemonCfg:     daemonCfg,
		logWriter:     globalLogWriter,
		logLevel:      &globalLogLevel,
		desktop:       opts.Desktop,
		licenses:      license.NewService(secretSvc),
		eventRing:     newEventRing(eventReplayBufferSize),
	}

	// Opt-in bearer-token auth for the TCP Web UI/API (daemon.yml api_auth;
	// server mode only — the desktop wrapper drives the daemon over the Unix
	// socket / a trusted local chain and stays token-free). Token precedence:
	// explicit api_auth.token → persisted conf/api-token → freshly generated
	// 32-byte random token written to conf/api-token (0600).
	if daemonCfg.APIAuth.Enabled && !config.IsDesktopMode() {
		token, generated, tokenErr := config.EnsureAPIToken(daemonCfg)
		if tokenErr != nil {
			return fmt.Errorf("resolve api auth token: %w", tokenErr)
		}
		d.apiAuth = newAPIAuth(token)
		switch {
		case generated:
			slog.Info("API token auth enabled — generated a new token", "tokenPath", config.APITokenPath(), "hint", "run: citeck ui")
		case daemonCfg.APIAuth.Token != "":
			slog.Info("API token auth enabled (token from daemon.yml)")
		default:
			slog.Info("API token auth enabled", "tokenPath", config.APITokenPath(), "hint", "run: citeck ui")
		}
	}

	// Desktop auto-update service: discovers the GitHub `latest`
	// release and stages payloads. Server mode has no wrapper to apply a swap, so
	// the routes + service are desktop-only. Scope is linux-first: only linux
	// publishes the bare-binary payload, so gating here keeps the macOS/Windows
	// UI from showing an update it cannot install (the routes 404 when nil).
	if config.IsDesktopMode() && goruntime.GOOS == "linux" {
		d.updateSvc = update.NewService(opts.Version, config.UpdatesDir())
		go d.updateSvc.RunPeriodic(bgCtx)
	}

	// Low-disk monitor: a full data root is what silently broke a running
	// namespace (Docker ENOSPC → every container's stdout/health stalled →
	// liveness storm). A periodic WARN surfaces the condition in the daemon log
	// (and the dump) before it cascades. Best-effort, both modes.
	go d.runDiskMonitor(bgCtx)

	// Wire up event broadcasting + cloud-config lifecycle
	if loaded.Runtime != nil {
		cloudCfg := loaded.CloudCfgServer
		loaded.Runtime.SetEventCallback(func(evt api.EventDto) {
			d.handleRuntimeEvent(evt, cloudCfg)
		})
	}

	// Start ACME renewal service if Let's Encrypt is enabled
	d.startACMERenewalIfConfigured()

	listener, socketMux, err := d.initUnixServer()
	if err != nil {
		return err
	}
	readyURL := d.startWebUI(socketMux, nsCfg)

	slog.Info("Citeck Daemon started",
		"socket", socketPath,
		"webui", daemonCfg.Server.WebUI.Enabled,
		"tcp", daemonCfg.Server.WebUI.Listen,
		"pid", os.Getpid(),
	)

	d.wireShutdownSignals(opts)

	// Startup complete — disable cleanup defers
	startupFailed = false

	// Notify caller that the daemon is ready as soon as the HTTP listener
	// is up — do NOT wait for the namespace to finish reconciling. On a
	// cold start the namespace stays in STARTING for the full 5–10 minute
	// webapp boot, and an earlier WaitForInitialReconcile(15s) here meant
	// the desktop spinner blocked for a hard 15 seconds before the
	// dashboard appeared. The dashboard already renders STOPPED / STARTING
	// / PULLING fine and updates live via SSE — a brief STOPPED flicker is
	// vastly better UX than a 15 s blank wait.
	if opts.ReadyCh != nil {
		opts.ReadyCh <- readyURL
	}

	// Serve (blocks until shutdown)
	if err := d.server.Serve(listener); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("server error: %w", err)
	}

	// Always return ErrShutdownRequested — whether shutdown came from an external
	// context (desktop), SIGTERM, or the HTTP endpoint. The caller uses this to
	// trigger os.Exit and avoid the process lingering on background goroutines.
	return ErrShutdownRequested
}

// ensureDaemonDirs creates the daemon's directory layout (conf, data, logs,
// run — plus the workspaces root in desktop mode).
func ensureDaemonDirs() error {
	dirs := []string{config.ConfDir(), config.DataDir(), config.LogDir(), config.RunDir()}
	if config.IsDesktopMode() {
		dirs = append(dirs, config.WorkspacesDir())
	}
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0o755); err != nil { //nolint:gosec // G301: daemon dirs need 0o755 for Docker and service access
			return fmt.Errorf("create directory %s: %w", dir, err)
		}
	}
	return nil
}

// loadEffectiveDaemonConfig loads daemon.yml (defaults on failure) and applies
// CLI overrides (--no-ui).
func loadEffectiveDaemonConfig(opts StartOptions) config.DaemonConfig {
	daemonCfg, err := config.LoadDaemonConfig()
	if err != nil {
		slog.Warn("Failed to load daemon config, using defaults", "err", err)
		daemonCfg = config.DefaultDaemonConfig()
	}
	if opts.NoUI {
		daemonCfg.Server.WebUI.Enabled = false
	}
	return daemonCfg
}

// guardSingleDaemonInstance refuses to start when another daemon already
// listens on the Unix socket; a stale (non-answering) socket file is removed.
func guardSingleDaemonInstance(socketPath string) error {
	if conn, dialErr := net.DialTimeout("unix", socketPath, 2*time.Second); dialErr == nil {
		_ = conn.Close()
		return fmt.Errorf("another daemon is already running (socket %s is active)", socketPath)
	}
	// Socket exists but nobody listening — stale, safe to remove
	_ = os.Remove(socketPath)
	return nil
}

// resolveStartupTarget determines which (workspace, namespace) the daemon
// boots into. Server mode is fixed ("daemon", "default"). Desktop mode first
// runs the one-shot H2 → SQLite migration (it must precede any SQLite
// access), then restores the persisted selection from launcher_state and
// validates the workspace against the on-disk list.
func resolveStartupTarget() (wsID, nsID string, err error) {
	wsID, nsID = "daemon", "default"
	if !config.IsDesktopMode() {
		return wsID, nsID, nil
	}
	if migErr := migrateH2IfNeeded(); migErr != nil {
		return "", "", migErr
	}

	wsID, nsID = restorePersistedSelection()
	wsID, nsID = validateWorkspaceSelection(wsID, nsID)
	slog.Info("Desktop mode workspace", "wsID", wsID, "nsID", nsID)
	return wsID, nsID, nil
}

// restorePersistedSelection reads the previously-selected workspace +
// namespace from launcher_state. Entry into a namespace is always explicit
// (Welcome → Quick Start / Create / pick existing), so an absent / empty
// SelectedNs entry simply lands on Welcome — there is no first-namespace
// auto-pick.
func restorePersistedSelection() (wsID, nsID string) {
	wsID = "default"
	sqlStore, sqlErr := storage.NewSQLiteStore(config.HomeDir())
	if sqlErr != nil {
		return wsID, ""
	}
	defer func() { _ = sqlStore.Close() }()
	state, stateErr := sqlStore.GetState()
	if stateErr != nil || state == nil {
		slog.Debug("Startup: no launcher_state record", "err", stateErr)
		return wsID, ""
	}
	if state.WorkspaceID != "" {
		wsID = state.WorkspaceID
	}
	if state.SelectedNs != nil {
		nsID = state.SelectedNs[wsID]
	}
	slog.Debug("Startup: loaded launcher_state",
		"persistedWorkspaceID", state.WorkspaceID,
		"selectedNs", state.SelectedNs,
		"resolvedWsID", wsID,
		"resolvedNsID", nsID,
	)
	return wsID, nsID
}

// validateWorkspaceSelection checks the stored wsID against the on-disk
// workspace list (handles a stored wsID that no longer exists, e.g. after a
// workspace delete): fall back to the "default" workspace, or the first one,
// and drop the namespace selection (it belonged to the workspace that's gone).
func validateWorkspaceSelection(wsID, nsID string) (validWsID, validNsID string) {
	workspaces, listErr := config.ListWorkspaces()
	if listErr != nil || len(workspaces) == 0 {
		return wsID, nsID
	}
	for _, ws := range workspaces {
		if ws.ID == wsID {
			return wsID, nsID
		}
	}
	for _, ws := range workspaces {
		if ws.ID == "default" || ws.ID == "DEFAULT" {
			return ws.ID, ""
		}
	}
	return workspaces[0].ID, ""
}

// migrateH2IfNeeded auto-migrates the Kotlin 1.x H2 store to SQLite BEFORE any
// SQLite access (transparent upgrade). Pure-Go MVStore reader — no JAR, no
// JRE. Falls back internally to a filesystem-only reconstruction if storage.db
// is unreadable.
func migrateH2IfNeeded() error {
	needed, _ := h2migrate.NeedsMigration(config.HomeDir())
	if !needed {
		return nil
	}
	migStore, migErr := storage.NewSQLiteStore(config.HomeDir())
	if migErr != nil {
		return fmt.Errorf("open store for migration: %w", migErr)
	}
	result, migRunErr := h2migrate.Migrate(config.HomeDir(), migStore)
	_ = migStore.Close()
	if migRunErr != nil {
		// Option B: a failed/invalid migration is a blocker — do not
		// proceed into normal operation with partial/garbage data.
		// Remove the half-built launcher.db so the migration re-runs
		// cleanly on the next start (NeedsMigration keys off launcher.db
		// absence). storage.db is opened read-only and already backed
		// up to storage.db.kotlin-bak, so the source is intact and the
		// run is retryable once the defect is fixed.
		dbPath := filepath.Join(config.HomeDir(), "launcher.db")
		for _, p := range []string{dbPath, dbPath + "-wal", dbPath + "-shm"} {
			if rmErr := os.Remove(p); rmErr != nil && !os.IsNotExist(rmErr) {
				slog.Error("CRITICAL: failed to remove partial launcher.db after aborted migration — manual cleanup required before retry", "path", p, "err", rmErr)
			}
		}
		slog.Error("CRITICAL: H2 → SQLite migration failed — refusing to start", "err", migRunErr)
		return fmt.Errorf("namespace migration failed: %w", migRunErr)
	}
	if result != nil {
		slog.Info("H2 migration complete",
			"workspaces", result.Workspaces,
			"secrets", result.Secrets,
			"namespaces", result.Namespaces,
			"gitRepos", result.GitRepos,
		)
	}
	return nil
}

// initStore opens the storage backend: SQLite in desktop mode, flat files in
// server mode.
func initStore() (storage.Store, error) {
	if config.IsDesktopMode() {
		store, err := storage.NewSQLiteStore(config.HomeDir())
		if err != nil {
			return nil, fmt.Errorf("create sqlite store: %w", err)
		}
		return store, nil
	}
	store, err := storage.NewFileStore(config.ConfDir(), filepath.Join(config.DataDir(), "runtime"))
	if err != nil {
		return nil, fmt.Errorf("create file store: %w", err)
	}
	return store, nil
}

// initSecretService builds the transparent-encryption layer over the store and
// applies the per-mode unlock policy: auto-unlock on the default password,
// CLI-provided master password in server mode, deferred Web-UI unlock in
// desktop mode, and first-start encryption initialization.
func initSecretService(store storage.Store, opts StartOptions) (*storage.SecretService, error) {
	secretSvc, err := storage.NewSecretService(store)
	if err != nil {
		return nil, fmt.Errorf("create secret service: %w", err)
	}
	switch {
	case secretSvc.IsEncrypted() && secretSvc.IsDefaultPassword():
		// Default password — auto-unlock. In server mode this is the
		// expected steady state. In desktop mode it's a legacy artifact
		// from older builds that auto-initialized encryption with "citeck";
		// the SYSTEM-secrets refactor leaves user-secret encryption alone,
		// so a desktop install staying on the default password is harmless
		// until the user adds a real user secret (Harbor / nexus / git)
		// — at that point the UI prompts for a real master password.
		if unlockErr := secretSvc.Unlock(storage.DefaultMasterPassword); unlockErr != nil {
			slog.Warn("Auto-unlock with default password failed", "err", unlockErr)
		} else {
			slog.Info("Secrets auto-unlocked with default password")
		}
	case secretSvc.IsEncrypted() && config.IsDesktopMode():
		// Desktop mode: Web UI unlock flow — don't block startup. System
		// secrets are plain in launcher_state so the daemon can keep
		// running even with a locked user-secret store.
		slog.Info("User secrets are encrypted with custom password, waiting for unlock via Web UI")
	case secretSvc.IsEncrypted():
		// Server mode: unlock now with password from CLI.
		if opts.MasterPassword == "" {
			return nil, fmt.Errorf("secrets are encrypted but no master password provided")
		}
		if unlockErr := secretSvc.Unlock(opts.MasterPassword); unlockErr != nil {
			return nil, fmt.Errorf("unlock secrets: %w", unlockErr)
		}
		slog.Info("Secrets unlocked successfully")
	case !config.IsDesktopMode():
		// Server mode first start: pre-initialize encryption with the default
		// password so secrets generated later in this session are encrypted
		// immediately. Avoids the historical install-CLI / daemon race where
		// the daemon had encrypted=false in memory while files on disk were
		// already encrypted.
		if setupErr := secretSvc.SetMasterPassword(storage.DefaultMasterPassword, true); setupErr != nil {
			slog.Warn("Failed to set up default encryption", "err", setupErr)
		} else {
			slog.Info("Secrets encryption initialized with default password")
		}
	default:
		// Desktop mode first start: SecretService stays unencrypted and empty
		// (Kotlin v1.x parity — master password is set only when the user adds
		// their first user secret via the UI). SYSTEM secrets live in plain
		// launcher_state and are unaffected.
		slog.Info("Desktop mode: user-secret encryption deferred until first user secret is added")
	}
	return secretSvc, nil
}

// initUnixServer creates the shared route mux, builds the daemon's primary
// HTTP server around it, and binds the Unix socket (the trusted local-CLI
// transport). Returns the listener and the mux — localhost TCP reuses the
// same mux in startWebUI, so both transports serve identical routes.
func (d *Daemon) initUnixServer() (net.Listener, *http.ServeMux, error) {
	// Single mux for all routes. Localhost TCP is trusted (desktop thin
	// client), non-localhost requires mTLS. Both paths get full access.
	socketMux := http.NewServeMux()
	d.registerRoutes(socketMux)
	d.server = &http.Server{
		Handler:        d.unixHandler(socketMux),
		ReadTimeout:    30 * time.Second,
		WriteTimeout:   120 * time.Second, // kcadm.sh exec can take 30-60s on slow hardware
		MaxHeaderBytes: 1 << 20,           // 1MB
	}

	listener, err := net.Listen("unix", d.socketPath)
	if err != nil {
		return nil, nil, fmt.Errorf("listen on %s: %w", d.socketPath, err)
	}
	socketPerm := os.FileMode(0o600)
	if chmodErr := os.Chmod(d.socketPath, socketPerm); chmodErr != nil {
		slog.Warn("Failed to chmod socket", "path", d.socketPath, "err", chmodErr)
	}
	return listener, socketMux, nil
}

// unixHandler builds the middleware chain for the trusted Unix-socket
// transport. Deliberately NO token auth / CSRF / CORS: socket access is
// already restricted to the daemon's user by the 0600 file mode, so the
// local CLI keeps working unchanged even when daemon.yml api_auth is on.
func (d *Daemon) unixHandler(mux http.Handler) http.Handler {
	return RecoveryMiddleware(LoggingMiddleware(mux))
}

// serverTCPHandler builds the middleware chain for the server-mode TCP
// transport (direct browser access). Request-processing order:
// CORS → Recovery → Logging → SecurityHeaders → RateLimit →
// [token auth, when api_auth enabled] → [CSRF, unless mTLS] → mux.
// Token auth sits AFTER the rate limiter (token guessing is throttled per
// IP) and BEFORE CSRF (an unauthenticated request gets 401 AUTH_REQUIRED,
// not a misleading 403 CSRF error). mTLS-authenticated requests bypass the
// token check inside the middleware itself.
func (d *Daemon) serverTCPHandler(base http.Handler, tcpAddr string, mtlsActive bool) http.Handler {
	h := base
	if !mtlsActive {
		h = CSRFMiddleware(h)
	}
	if d.apiAuth != nil {
		h = d.apiAuth.Middleware(h)
	}
	h = RateLimitMiddleware(100, h)
	h = SecurityHeadersMiddleware(mtlsActive, h)
	h = LoggingMiddleware(h)
	h = RecoveryMiddleware(h)
	return CORSMiddleware(h, tcpAddr)
}

// webUITCPAllowed reports whether the daemon may bind the TCP Web UI listener.
// Desktop mode serves the UI over the Unix socket, so TCP is off unless the
// CITECK_DESKTOP_TCP E2E hatch is set. Server mode does not offer the Web UI at
// all: it binds only via the explicit CITECK_SERVER_WEBUI dev/E2E hatch, never
// through daemon.yml (server.webui.enabled is inert in server mode).
func webUITCPAllowed(desktopMode, allowDesktopTCP, allowServerTCP bool) bool {
	if desktopMode {
		return allowDesktopTCP
	}
	return allowServerTCP
}

// startWebUI binds the TCP listener for the Web UI (controlled by daemon.yml
// and --no-ui) and returns the ready URL, or "" when the UI is disabled or
// failed to start. Desktop mode: Wails proxies through the Unix socket
// directly — no TCP needed; the E2E-testing escape hatch CITECK_DESKTOP_TCP=1
// also binds TCP in desktop mode so Playwright can drive the same UI the user
// sees in the Wails window.
//
// The URL scheme is derived from the actual transport (https iff mTLS is
// active) right here where mtlsActive is known — previously the caller
// re-derived it from isLocalhostAddr, duplicating this block's logic.
func (d *Daemon) startWebUI(socketMux *http.ServeMux, nsCfg *namespace.Config) (readyURL string) {
	tcpAddr := d.daemonCfg.Server.WebUI.Listen
	allowDesktopTCP := os.Getenv("CITECK_DESKTOP_TCP") == "1"
	allowServerTCP := os.Getenv("CITECK_SERVER_WEBUI") == "1"
	desktopMode := config.IsDesktopMode()
	// Server mode does not offer the Web UI: the CLI/TUI is the supported server
	// interface, and this TCP listener would serve the full root-equivalent API.
	// So server.webui.enabled is inert in production — the only way to bind it in
	// server mode is the explicit CITECK_SERVER_WEBUI dev/E2E hatch (never via
	// daemon.yml). Desktop serves the UI over the Unix socket; its TCP listener
	// stays off too except the CITECK_DESKTOP_TCP hatch.
	if !desktopMode && d.daemonCfg.Server.WebUI.Enabled && !allowServerTCP {
		slog.Warn("Ignoring server.webui.enabled: true — the Web UI is not available in server mode; the CLI/TUI is the supported interface")
	}
	if !webUITCPAllowed(desktopMode, allowDesktopTCP, allowServerTCP) {
		if !desktopMode {
			slog.Info("Web UI disabled")
		}
		return ""
	}

	tcpListener, err := net.Listen("tcp", tcpAddr)
	if err != nil {
		slog.Warn("TCP listener failed, Web UI unavailable", "addr", tcpAddr, "err", err)
		return ""
	}
	localhost := isLocalhostAddr(tcpAddr)
	mtlsActive := false
	// Localhost TCP is trusted — give full access (socketMux), same as Unix socket.
	// Non-localhost requires mTLS for full access.
	var tcpBaseMux http.Handler = socketMux
	if !localhost {
		var mtlsErr error
		tcpListener, tcpBaseMux, mtlsActive, mtlsErr = d.setupMTLS(tcpListener, socketMux, nsCfg, tcpAddr)
		if mtlsErr != nil {
			slog.Error("mTLS setup failed — Web UI not started", "err", mtlsErr)
		}
	}
	if tcpListener == nil {
		return "" // mTLS setup failed; the listener was closed inside setupMTLS
	}

	tcpHandler := tcpBaseMux
	if config.IsDesktopMode() {
		// Desktop: requests come from Wails reverse proxy (trusted).
		// Skip CSRF/CORS — Wails AssetServer is the real origin. Token auth
		// is also skipped: the wrapper path is local and trusted (api_auth is
		// a server-mode feature; d.apiAuth is never set in desktop mode).
		tcpHandler = RateLimitMiddleware(100, tcpHandler)
		tcpHandler = LoggingMiddleware(tcpHandler)
		tcpHandler = RecoveryMiddleware(tcpHandler)
	} else {
		tcpHandler = d.serverTCPHandler(tcpBaseMux, tcpAddr, mtlsActive)
	}

	scheme := "http"
	if mtlsActive {
		scheme = "https"
	}
	d.tcpServer = &http.Server{
		Handler:        tcpHandler,
		ReadTimeout:    30 * time.Second,
		WriteTimeout:   30 * time.Second,
		IdleTimeout:    120 * time.Second,
		MaxHeaderBytes: 1 << 20,
	}

	host, port, _ := net.SplitHostPort(tcpAddr)
	displayHost := host
	if host == "" || host == "0.0.0.0" || host == "::" {
		displayHost = config.DetectDisplayIP()
	}
	readyURL = scheme + "://" + displayHost + ":" + port

	go func() {
		slog.Info("Web UI available", "url", readyURL, "listen", tcpAddr)
		if serveErr := d.tcpServer.Serve(tcpListener); serveErr != nil && serveErr != http.ErrServerClosed {
			slog.Error("TCP server error", "err", serveErr)
		}
	}()
	return readyURL
}

// wireShutdownSignals installs the shutdown trigger for the current run mode.
// Desktop quit DETACHES — containers are left running and re-adopted on the
// next launch via doStart's hash match — so the launcher and the Docker apps
// live independently (the kubelet principle). CLI/server signal shutdown
// (systemd is server-only) performs a full stop. Detach is also triggered
// explicitly via the HTTP endpoint for binary upgrades.
func (d *Daemon) wireShutdownSignals(opts StartOptions) {
	switch {
	case opts.Desktop && opts.Ctx != nil:
		// Desktop in-process mode (legacy): the Wails runner provides opts.Ctx,
		// canceled on quit. Closing the launcher is not a request to tear the
		// namespace down; explicit teardown stays available via the UI Stop
		// button. (The thin-wrapper desktop runs the daemon as a SEPARATE process
		// via runDaemonMode, which passes no Ctx — that path is handled below.)
		go func() {
			<-opts.Ctx.Done()
			slog.Info("External context canceled, detaching (containers left running)")
			d.shutdown(true)
		}()
	case opts.Desktop:
		// Thin-wrapper desktop mode: the daemon is a standalone child process
		// supervised by the Wails wrapper. It has no in-process parent context;
		// the wrapper drives a graceful exit by POSTing the shutdown endpoint
		// (with leave_running=true → detach) on quit, and SIGKILLs as a fallback.
		// A direct SIGTERM/SIGINT (supervisor fallback, or a stray signal) must
		// still DETACH, not full-stop — preserving the kubelet principle that
		// closing the launcher leaves the platform containers running.
		sigCh := make(chan os.Signal, 2)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		go func() {
			<-sigCh
			slog.Info("Shutdown signal received (desktop child), detaching (containers left running)")
			go func() {
				<-sigCh
				slog.Warn("Second signal received, forcing exit")
				os.Exit(1)
			}()
			d.shutdown(true)
		}()
		// Bind lifecycle to the wrapper: if it dies WITHOUT a graceful quit
		// (SIGKILL/crash), neither the shutdown POST nor a signal arrives, so a
		// pure signal handler would leave the daemon orphaned on macOS/Windows
		// (Linux has Pdeathsig). The watchdog polls the wrapper pid and detaches.
		d.watchWrapperLifecycle()
	default:
		// CLI mode: first SIGINT/SIGTERM → graceful, second → force exit
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
			d.shutdown(false)
		}()
	}
}

// sweepOrphanDockerResources removes Docker resources for namespaces that no
// longer exist in storage. Fail-safe: the keep set must be built completely
// from storage; if any listing fails, it does nothing rather than risk
// removing a live namespace's resources. The active namespace is always kept.
func sweepOrphanDockerResources(ctx context.Context, dc *docker.Client, store storage.Store, activeWsID, activeNsID string) {
	if dc == nil || store == nil {
		return
	}
	keep := map[string]bool{}
	if activeNsID != "" {
		keep[docker.OrphanKey(activeNsID, activeWsID)] = true
	}
	wss, err := store.ListWorkspaces()
	if err != nil {
		slog.Warn("Orphan-sweep skipped: cannot list workspaces", "err", err)
		return
	}
	for _, ws := range wss {
		nss, nsErr := store.ListNamespaces(ws.ID)
		if nsErr != nil {
			// Incomplete keep set → bail out entirely; never purge on doubt.
			slog.Warn("Orphan-sweep skipped: cannot list namespaces", "ws", ws.ID, "err", nsErr)
			return
		}
		for _, ns := range nss {
			keep[docker.OrphanKey(ns.ID, ws.ID)] = true
		}
	}
	if purged := dc.SweepOrphans(ctx, keep); len(purged) > 0 {
		slog.Info("Orphan-sweep removed leftover namespace resources",
			"count", len(purged), "namespaces", purged)
	}
}
