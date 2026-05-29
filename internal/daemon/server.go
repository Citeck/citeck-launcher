package daemon

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
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
	"sync/atomic"
	"syscall"
	"time"

	"github.com/citeck/citeck-launcher/internal/acme"
	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/docker"
	"github.com/citeck/citeck-launcher/internal/fsutil"
	"github.com/citeck/citeck-launcher/internal/git"
	"github.com/citeck/citeck-launcher/internal/h2migrate"
	"github.com/citeck/citeck-launcher/internal/license"
	"github.com/citeck/citeck-launcher/internal/namespace"
	"github.com/citeck/citeck-launcher/internal/snapshot"
	"github.com/citeck/citeck-launcher/internal/storage"
	"github.com/citeck/citeck-launcher/internal/tlsutil"
)

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

// secretReader is the minimal interface for reading secrets.
// Satisfied by both storage.Store (server mode) and *storage.SecretService (desktop mode).
type secretReader interface {
	ListSecrets() ([]storage.SecretMeta, error)
	GetSecret(id string) (*storage.Secret, error)
}

// secretWriter is the minimal interface for writing secrets.
// Satisfied by both storage.Store (server mode) and *storage.SecretService (desktop mode).
type secretWriter interface {
	SaveSecret(secret storage.Secret) error
}

// Daemon is the main daemon server.
type Daemon struct {
	dockerClient    *docker.Client
	runtime         *namespace.Runtime
	nsConfig        *namespace.Config
	bundleDef       *bundle.Def
	workspaceConfig *bundle.WorkspaceConfig
	appDefs         []appdef.ApplicationDef
	server          *http.Server
	tcpServer       *http.Server
	cloudCfgServer  *CloudConfigServer
	store           storage.Store
	secretService   *storage.SecretService // always non-nil; wraps store with transparent encryption
	socketPath      string
	volumesBase     string
	workspaceID     string
	startTime       time.Time
	eventSubs       []chan api.EventDto
	eventMu         sync.Mutex
	eventRing       *eventRing // bounded replay buffer for SSE reconnects (Last-Event-ID)
	configMu        sync.RWMutex // protects nsConfig, bundleDef, appDefs, workspaceConfig, systemSecrets
	version         string
	bundleError     string // non-empty if bundle resolution failed
	acmeRenewal     *acme.RenewalService
	shutdownOnce    sync.Once
	bgCtx           context.Context // canceled on daemon shutdown
	bgCancel        context.CancelFunc
	bgWg            sync.WaitGroup // tracks background goroutines (snapshot, downloads)
	snapshotMu      sync.Mutex     // guards concurrent snapshot import/export
	daemonCfg       config.DaemonConfig
	// eventSeq is the monotonic SSE event counter. All mutations (.Add) and
	// the cutoff Load happen under eventMu — the atomic type is retained
	// purely for Load() ergonomics from addSubscriber's lock holder and the
	// rare read from diagnostics; treat the field as logically protected by
	// eventMu, not as concurrent-safe by itself.
	eventSeq        atomic.Int64
	sseDropped      atomic.Int64 // SSE events dropped due to slow consumers
	logWriter       *fsutil.RotatingWriter
	logLevel        *slog.LevelVar
	systemSecrets   namespace.SystemSecrets // resolved JWT/OIDC secrets
	desktop         bool                    // desktop mode: log writer shared across restarts
	reloadMu        sync.Mutex              // guards concurrent reload requests
	licenses        *license.Service        // user-added enterprise licenses
}

// secretReaderFunc returns the SecretService as a secretReader (transparent decryption).
func (d *Daemon) secretReaderFunc() secretReader {
	return d.secretService
}

// secretWriterFunc returns the SecretService as a secretWriter (transparent encryption).
func (d *Daemon) secretWriterFunc() secretWriter {
	return d.secretService
}

// nsSecretReader returns a namespace.SecretReader backed by the daemon's SecretService.
func (d *Daemon) nsSecretReader() namespace.SecretReader {
	return &secretReaderAdapter{svc: d.secretService}
}

// secretReaderAdapter adapts *storage.SecretService to namespace.SecretReader.
type secretReaderAdapter struct {
	svc *storage.SecretService
}

func (a *secretReaderAdapter) GetSecretValue(key string) (string, error) {
	s, err := a.svc.GetSecret(key)
	if err != nil {
		return "", fmt.Errorf("get secret %q: %w", key, err)
	}
	return s.Value, nil
}

// collectExtraLicensesFrom queries a license.Service for user-added licenses
// and converts them to the bundle.LicenseInstance shape the generator merges
// with workspace-declared ones. Returns nil if svc is nil, the store is
// locked, or the listing errors out — the generator falls back to workspace-
// only licenses in that case, so a locked SecretService never aborts
// namespace generation. Split off into a free function so both the daemon
// startup path (where *Daemon doesn't exist yet) and the reload path (where
// it does) share one implementation.
func collectExtraLicensesFrom(svc *license.Service) []bundle.LicenseInstance {
	if svc == nil {
		return nil
	}
	list, err := svc.List()
	if err != nil {
		// Locked SecretService is the common case (desktop mode before unlock).
		// Don't fail generation; emit only workspace-declared licenses.
		slog.Debug("Skipping user-added licenses for namespace generation", "err", err)
		return nil
	}
	if len(list) == 0 {
		return nil
	}
	out := make([]bundle.LicenseInstance, 0, len(list))
	for _, lic := range list {
		// Round-trip through JSON so the per-field shape converges on the
		// bundle.LicenseInstance schema (string dates, any-typed content +
		// signatures). license.Instance's MarshalJSON already emits the same
		// JSON keys; bundle.LicenseInstance.UnmarshalJSON re-parses them into
		// the YAML-source-of-truth shape. This avoids a hand-written field
		// copy that would drift when either side adds a field.
		raw, err := json.Marshal(lic)
		if err != nil {
			slog.Warn("Marshal license for generator merge failed", "id", lic.ID, "err", err)
			continue
		}
		var bl bundle.LicenseInstance
		if err := json.Unmarshal(raw, &bl); err != nil {
			slog.Warn("Convert license to bundle shape failed", "id", lic.ID, "err", err)
			continue
		}
		out = append(out, bl)
	}
	return out
}

// startACMERenewalIfConfigured starts the ACME renewal service when the
// active namespace uses Let's Encrypt. No-op when ACME isn't configured or
// a renewal service is already running. Shared between initial daemon Start
// and the live namespace-switch path.
func (d *Daemon) startACMERenewalIfConfigured() {
	d.configMu.RLock()
	nsCfg := d.nsConfig
	existing := d.acmeRenewal
	d.configMu.RUnlock()
	if existing != nil {
		return
	}
	if nsCfg == nil || !nsCfg.Proxy.TLS.Enabled || !nsCfg.Proxy.TLS.LetsEncrypt || nsCfg.Proxy.Host == "" {
		return
	}
	acmeClient := acme.NewClient(config.DataDir(), config.ConfDir(), nsCfg.Proxy.Host)
	svc := acme.NewRenewalService(acmeClient, func() {
		if d.runtime != nil {
			if restartErr := d.runtime.RestartApp("proxy"); restartErr != nil {
				slog.Error("ACME: restart proxy after renewal failed", "err", restartErr)
			}
		}
	})
	d.configMu.Lock()
	d.acmeRenewal = svc
	d.configMu.Unlock()
	svc.Start()
}

// rebuildAuthCaches rebuilds token lookup and registry auth caches from current secrets,
// then retries any pull-failed apps.
func (d *Daemon) rebuildAuthCaches() {
	if d.runtime == nil {
		return
	}
	d.configMu.RLock()
	wsCfg := d.workspaceConfig
	d.configMu.RUnlock()
	d.runtime.SetRegistryAuthFunc(makeRegistryAuthFunc(wsCfg, d.secretReaderFunc()))
	retried := d.runtime.RetryPullFailedApps()
	if retried > 0 {
		slog.Info("Retrying pull-failed apps after secrets change", "count", retried)
	}
}

// Start runs the daemon.
//
//nolint:gocyclo,nestif // Start() orchestrates the full daemon lifecycle: storage, Docker, config, TLS, ACME, runtime, and HTTP servers
func Start(opts StartOptions) error {
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

	if opts.Desktop {
		config.SetDesktopMode(true)
	}

	slog.Info("Starting daemon",
		"foreground", opts.Foreground,
		"desktop", opts.Desktop,
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
		if err := os.MkdirAll(dir, 0o755); err != nil { //nolint:gosec // G301: daemon dirs need 0o755 for Docker and service access
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
	if conn, dialErr := net.DialTimeout("unix", socketPath, 2*time.Second); dialErr == nil {
		_ = conn.Close()
		return fmt.Errorf("another daemon is already running (socket %s is active)", socketPath)
	}
	// Socket exists but nobody listening — stale, safe to remove
	_ = os.Remove(socketPath)

	// Determine workspace and namespace IDs
	wsID := "daemon"
	nsID := "default"
	if config.IsDesktopMode() {
		wsID = "default"

		// Auto-migrate H2 → SQLite BEFORE any SQLite access (transparent upgrade from Kotlin).
		// Pure-Go MVStore reader — no JAR, no JRE. Falls back internally to a
		// filesystem-only reconstruction if storage.db is unreadable.
		if needed, _ := h2migrate.NeedsMigration(config.HomeDir()); needed {
			if migStore, migErr := storage.NewSQLiteStore(config.HomeDir()); migErr == nil {
				result, migRunErr := h2migrate.Migrate(config.HomeDir(), migStore)
				if migRunErr != nil {
					slog.Error("H2 migration failed", "err", migRunErr)
				} else if result != nil {
					slog.Info("H2 migration complete",
						"workspaces", result.Workspaces,
						"secrets", result.Secrets,
						"namespaces", result.Namespaces,
						"gitRepos", result.GitRepos,
					)
				}
				_ = migStore.Close()
			}
		}

		// Try SQLiteStore for preferred workspace/namespace from launcher_state
		if sqlStore, sqlErr := storage.NewSQLiteStore(config.HomeDir()); sqlErr == nil {
			if state, stateErr := sqlStore.GetState(); stateErr == nil {
				if state.WorkspaceID != "" {
					wsID = state.WorkspaceID
				}
				if selected := state.NamespaceID(); selected != "" {
					nsID = selected
				}
			}
			_ = sqlStore.Close()
		}

		// Fallback: use first available workspace if stored one doesn't exist
		if workspaces, listErr := config.ListWorkspaces(); listErr == nil && len(workspaces) > 0 {
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
			_ = store.Close()
		}
	}()

	// Wire git package's persistent sync-state hook so the throttle window
	// (Kotlin parity: git-repo!instances) survives daemon restart. Without
	// this every workspace/bundle repo would re-pull on cold start.
	git.SetSyncStateStore(gitSyncStoreAdapter{store: store}, config.HomeDir())

	// Initialize SecretService (transparent encryption layer for all modes)
	secretSvc, err := storage.NewSecretService(store)
	if err != nil {
		return fmt.Errorf("create secret service: %w", err)
	}
	if secretSvc.IsEncrypted() {
		if secretSvc.IsDefaultPassword() {
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
		} else if config.IsDesktopMode() {
			// Desktop mode: Web UI unlock flow — don't block startup. System
			// secrets are plain in launcher_state so the daemon can keep
			// running even with a locked user-secret store.
			slog.Info("User secrets are encrypted with custom password, waiting for unlock via Web UI")
		} else {
			// Server mode: unlock now with password from CLI.
			if opts.MasterPassword == "" {
				return fmt.Errorf("secrets are encrypted but no master password provided")
			}
			if unlockErr := secretSvc.Unlock(opts.MasterPassword); unlockErr != nil {
				return fmt.Errorf("unlock secrets: %w", unlockErr)
			}
			slog.Info("Secrets unlocked successfully")
		}
	} else if !config.IsDesktopMode() {
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
	} else {
		// Desktop mode first start: SecretService stays unencrypted and empty
		// (Kotlin v1.x parity — master password is set only when the user adds
		// their first user secret via the UI). SYSTEM secrets live in plain
		// launcher_state and are unaffected.
		slog.Info("Desktop mode: user-secret encryption deferred until first user secret is added")
	}

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
	bundleDef := loaded.BundleDef
	wsCfg := loaded.WorkspaceConfig
	runtime := loaded.Runtime
	appDefs := loaded.AppDefs
	cloudCfgSrv := loaded.CloudCfgServer
	systemSecrets := loaded.SystemSecrets
	volumesBase := loaded.VolumesBase
	bundleError := loaded.BundleError

	if nsCfg != nil {
		// Snapshot auto-import: run synchronously BEFORE start so volumes are populated
		if nsCfg.Snapshot != "" {
			slog.Info("Running snapshot auto-import before namespace start", "snapshot", nsCfg.Snapshot)
			importSnapshotIfNeeded(nsCfg, wsCfg, dockerClient, wsID, volumesBase)
		}
		if loaded.ShouldStart {
			runtime.Start(appDefs)
		}
	}

	bgCtx, bgCancel := context.WithCancel(context.Background()) //nolint:gosec // G118: bgCancel stored in Daemon struct, called in shutdown

	d := &Daemon{
		dockerClient:    dockerClient,
		runtime:         runtime,
		nsConfig:        nsCfg,
		bundleDef:       bundleDef,
		workspaceConfig: wsCfg,
		appDefs:         appDefs,
		cloudCfgServer:  cloudCfgSrv,
		store:           store,
		secretService:   secretSvc,
		socketPath:      socketPath,
		volumesBase:     volumesBase,
		workspaceID:     wsID,
		version:         opts.Version,
		bundleError:     bundleError,
		systemSecrets:   systemSecrets,
		startTime:       time.Now(),
		bgCtx:           bgCtx,
		bgCancel:        bgCancel,
		daemonCfg:       daemonCfg,
		logWriter:       globalLogWriter,
		logLevel:        &globalLogLevel,
		desktop:         opts.Desktop,
		licenses:        license.NewService(secretSvc),
		eventRing:       newEventRing(eventReplayBufferSize),
	}

	// Wire up event broadcasting
	if d.runtime != nil {
		d.runtime.SetEventCallback(func(evt api.EventDto) {
			d.broadcastEvent(evt)
		})
	}

	// Start ACME renewal service if Let's Encrypt is enabled
	d.startACMERenewalIfConfigured()

	// Create HTTP server — single mux for all routes.
	// Localhost TCP is trusted (desktop thin client), non-localhost requires mTLS.
	// Both paths get full access to socketMux.
	socketMux := http.NewServeMux()
	d.registerRoutes(socketMux)
	d.server = &http.Server{
		Handler:        RecoveryMiddleware(LoggingMiddleware(socketMux)),
		ReadTimeout:    30 * time.Second,
		WriteTimeout:   120 * time.Second, // kcadm.sh exec can take 30-60s on slow hardware
		MaxHeaderBytes: 1 << 20,           // 1MB
	}

	// Listen on Unix socket (for local CLI)
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", socketPath, err)
	}
	socketPerm := os.FileMode(0o600)
	if err := os.Chmod(socketPath, socketPerm); err != nil {
		slog.Warn("Failed to chmod socket", "path", socketPath, "err", err)
	}

	// TCP listener for Web UI (controlled by daemon.yml and --no-ui flag).
	// Desktop mode: Wails proxies through the Unix socket directly — no TCP needed.
	// E2E-testing escape hatch: CITECK_DESKTOP_TCP=1 also binds TCP in desktop
	// mode so Playwright can drive the same UI the user sees in the Wails window.
	tcpAddr := daemonCfg.Server.WebUI.Listen
	allowDesktopTCP := os.Getenv("CITECK_DESKTOP_TCP") == "1"
	if daemonCfg.Server.WebUI.Enabled && (!config.IsDesktopMode() || allowDesktopTCP) {
		tcpListener, err := net.Listen("tcp", tcpAddr)
		if err != nil {
			slog.Warn("TCP listener failed, Web UI unavailable", "addr", tcpAddr, "err", err)
		} else {
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

			tcpHandler := tcpBaseMux
			if config.IsDesktopMode() {
				// Desktop: requests come from Wails reverse proxy (trusted).
				// Skip CSRF/CORS — Wails AssetServer is the real origin.
				tcpHandler = RateLimitMiddleware(100, tcpHandler)
				tcpHandler = LoggingMiddleware(tcpHandler)
				tcpHandler = RecoveryMiddleware(tcpHandler)
			} else {
				// Server mode: direct browser access needs CSRF + CORS
				if !mtlsActive {
					tcpHandler = CSRFMiddleware(tcpHandler)
				}
				tcpHandler = RateLimitMiddleware(100, tcpHandler)
				tcpHandler = SecurityHeadersMiddleware(mtlsActive, tcpHandler)
				tcpHandler = LoggingMiddleware(tcpHandler)
				tcpHandler = RecoveryMiddleware(tcpHandler)
			}

			if tcpListener != nil {
				if !config.IsDesktopMode() {
					tcpHandler = CORSMiddleware(tcpHandler, tcpAddr)
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
				go func() {
					host, port, _ := net.SplitHostPort(tcpAddr)
					displayHost := host
					if host == "" || host == "0.0.0.0" || host == "::" {
						displayHost = config.DetectDisplayIP()
					}
					slog.Info("Web UI available", "url", scheme+"://"+displayHost+":"+port, "listen", tcpAddr)
					if err := d.tcpServer.Serve(tcpListener); err != nil && err != http.ErrServerClosed {
						slog.Error("TCP server error", "err", err)
					}
				}()
			}
		}
	} else if !config.IsDesktopMode() {
		slog.Info("Web UI disabled")
	}

	// Notify ready URL
	readyURL := ""
	if daemonCfg.Server.WebUI.Enabled && d.tcpServer != nil {
		scheme := "http"
		if !isLocalhostAddr(tcpAddr) {
			scheme = "https"
		}
		host, port, _ := net.SplitHostPort(tcpAddr)
		if host == "" || host == "0.0.0.0" || host == "::" {
			host = config.DetectDisplayIP()
		}
		readyURL = scheme + "://" + host + ":" + port
	}

	slog.Info("Citeck Daemon started",
		"socket", socketPath,
		"webui", daemonCfg.Server.WebUI.Enabled,
		"tcp", tcpAddr,
		"pid", os.Getpid(),
	)

	// Handle shutdown: external context (desktop) or signal-based (CLI).
	// Both paths perform a full shutdown (containers stopped) — the detach
	// (leave-running) path is only triggered explicitly via the HTTP endpoint.
	if opts.Ctx != nil {
		// Desktop mode: context provided externally (Wails owns lifecycle)
		go func() {
			<-opts.Ctx.Done()
			slog.Info("External context canceled, shutting down")
			d.shutdown(false)
		}()
	} else {
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

func (d *Daemon) shutdown(leaveRunning bool) {
	d.shutdownOnce.Do(func() { d.doShutdown(leaveRunning) })
}

func (d *Daemon) doShutdown(leaveRunning bool) {
	// Phase 1: Cancel background goroutines with 10s timeout
	d.bgCancel()
	bgDone := make(chan struct{})
	go func() { d.bgWg.Wait(); close(bgDone) }()
	select {
	case <-bgDone:
	case <-time.After(10 * time.Second):
		slog.Warn("Background goroutines did not finish in 10s")
	}

	// Phase 2: Stop services that can mutate runtime state BEFORE the runtime
	// itself winds down. The ACME renewal service in particular schedules
	// `runtime.RestartApp("proxy")` callbacks on its own context — if a renewal
	// fires during runtime.ShutdownDetached() it would tear down the proxy
	// container that detach mode is supposed to leave running. Stopping the
	// renewal first guarantees no late callbacks racing the runtime teardown.
	if d.cloudCfgServer != nil {
		d.cloudCfgServer.Stop()
	}
	d.configMu.RLock()
	renewal := d.acmeRenewal
	d.configMu.RUnlock()
	if renewal != nil {
		renewal.Stop()
	}

	// Phase 3: Shutdown runtime. When leaveRunning is set, the runtime exits
	// without stopping containers — the next daemon will adopt them via
	// doStart's hash-matching path. Used for binary upgrades.
	if d.runtime != nil {
		if leaveRunning {
			d.runtime.ShutdownDetached()
		} else {
			d.runtime.Shutdown()
		}
	}

	// Phase 4: Drain HTTP connections with 10s timeout
	httpCtx, httpCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer httpCancel()
	_ = d.server.Shutdown(httpCtx)
	if d.tcpServer != nil {
		_ = d.tcpServer.Shutdown(httpCtx)
	}
	if d.store != nil {
		_ = d.store.Close()
	}
	_ = d.dockerClient.Close()
	_ = os.Remove(d.socketPath)

	slog.Info("Daemon stopped")
	// In desktop mode, the log writer is shared across daemon restarts — don't close it.
	// In CLI mode (single Start), close the writer on exit.
	if d.logWriter != nil && !d.desktop {
		_ = d.logWriter.Close()
	}
}

// doReload performs the core reload logic: load config, resolve bundle, generate, write files,
// update shared state, and regenerate runtime. Caller must hold reloadMu.
//
//nolint:nestif // reload orchestrates config read, git pull, bundle resolution, ACME cert obtainment, and runtime regeneration
func (d *Daemon) doReload() error {
	d.configMu.RLock()
	if d.nsConfig == nil || d.runtime == nil {
		d.configMu.RUnlock()
		return fmt.Errorf("no namespace configured")
	}
	nsID := d.nsConfig.ID
	d.configMu.RUnlock()

	// Phase 1: slow I/O outside lock (config read, git pull, bundle resolution)
	nsCfg, err := namespace.LoadNamespaceConfig(config.ResolveNamespaceConfigPath(d.workspaceID, nsID))
	if err != nil {
		return fmt.Errorf("reload config: %w", err)
	}

	bundlesDataDir := config.DataDir()
	if config.IsDesktopMode() {
		bundlesDataDir = filepath.Join(config.HomeDir(), "ws", d.workspaceID)
	}
	resolver := bundle.NewResolverWithAuth(bundlesDataDir, makeTokenLookup(d.secretReaderFunc())).
		WithWorkspaceRepo(d.resolveActiveWorkspaceRepoOpts())
	resolveResult, err := resolver.Resolve(nsCfg.BundleRef)
	bundleFallback := false
	if err != nil {
		// Fallback to cached bundle from persisted state
		cachedState := namespace.LoadNsState(d.volumesBase, nsID)
		if cachedState != nil && cachedState.CachedBundle != nil && !cachedState.CachedBundle.IsEmpty() {
			slog.Warn("Bundle resolution failed on reload, using cached bundle", "ref", nsCfg.BundleRef, "err", err,
				"cachedVersion", cachedState.CachedBundle.Key.Version)
			resolveResult = &bundle.ResolveResult{Bundle: cachedState.CachedBundle, Workspace: d.workspaceConfig}
			bundleFallback = true
		} else {
			return fmt.Errorf("resolve bundle: %w", err)
		}
	}

	// Appfiles are intentionally NOT extracted here — same rule as Start().
	// writeRuntimeFiles(genResp.Files) below is the single source of truth
	// for bind-mount contents, avoiding a double-write that would revert a
	// generator-customized file (proxy lua with rendered secrets, realm JSON,
	// keycloak init script) back to its embedded template default.

	// Self-signed cert: generate if TLS enabled + no cert paths + no LE
	ensureSelfSignedCert(nsCfg)

	// Let's Encrypt: obtain certificate if needed; prepare renewal service for Phase 2
	var newRenewal *acme.RenewalService
	if acmeClient := ensureACMECert(nsCfg, "on reload"); acmeClient != nil {
		newRenewal = acme.NewRenewalService(acmeClient, func() {
			if d.runtime != nil {
				if err := d.runtime.RestartApp("proxy"); err != nil {
					slog.Error("ACME: restart proxy after renewal failed", "err", err)
				}
			}
		})
	}

	var genOpts namespace.GenerateOpts
	genOpts.SecretReader = d.nsSecretReader()
	if d.runtime != nil {
		genOpts.DetachedApps = d.runtime.ManualStoppedApps()
		// Overlay user-edited disk content into the hash input so a UI-edited
		// bind-mount file forces container recreate on this regenerate.
		genOpts.EditedFileOverlay = d.runtime.EditedFileOverlay(d.volumesBase)
	}
	// User-added licenses (encrypted store) merge with workspace-declared ones
	// in the eapps cloud-config. Locked SecretService yields nil and we fall
	// back to workspace-only licenses — reload never aborts on a locked store.
	genOpts.ExtraLicenses = collectExtraLicensesFrom(d.licenses)
	genResp, genErr := namespace.Generate(nsCfg, resolveResult.Bundle, resolveResult.Workspace, d.systemSecrets, genOpts)
	if genErr != nil {
		return fmt.Errorf("generate namespace: %w", genErr)
	}

	// Apply the full runtime file set. The generator owns everything —
	// embedded defaults it copied and mutated, plus files it built from
	// scratch. writeRuntimeFiles handles dir-in-place-of-file recovery
	// (a Docker quirk when a bind-mount source was wiped out-of-band).
	// EditedFilesSnapshot tells writeRuntimeFiles to skip user-edited
	// bind-mount files so Web-UI edits survive reload/regenerate.
	var editedFiles map[string]bool
	if d.runtime != nil {
		editedFiles = d.runtime.EditedFilesSnapshot()
	}
	writeRuntimeFiles(d.volumesBase, genResp.Files, editedFiles)

	// Phase 2: update shared state briefly under write lock
	d.configMu.Lock()
	d.nsConfig = nsCfg
	d.bundleDef = resolveResult.Bundle
	d.workspaceConfig = resolveResult.Workspace
	d.appDefs = genResp.Applications
	// Update ACME renewal service under lock to prevent data race with shutdown
	if d.acmeRenewal != nil {
		d.acmeRenewal.Stop()
	}
	d.acmeRenewal = newRenewal
	d.configMu.Unlock()
	if newRenewal != nil {
		newRenewal.Start()
	}

	if d.cloudCfgServer != nil {
		d.cloudCfgServer.UpdateConfig(genResp.CloudConfig, d.systemSecrets.JWT)
	}
	d.runtime.SetRegistryAuthFunc(makeRegistryAuthFunc(resolveResult.Workspace, d.secretReaderFunc()))
	d.runtime.SetDependsOnDetachedApps(genResp.DependsOnDetachedApps)

	// Phase 3: regenerate runtime with updated config (async stop + start).
	// When the bundle had to fall back to the cached on-disk copy (e.g. git
	// pull failed), the generated Applications set can come back smaller than
	// the live runtime's r.apps — handing that to Regenerate would mark every
	// missing app for removal and tear down running containers we don't have
	// authoritative info to remove. Skip the regenerate in that case so the
	// runtime keeps its current apps; the user fixes the bundle source and
	// re-runs reload to get the real desired set applied.
	currentAppCount := 0
	if d.runtime != nil {
		currentAppCount = d.runtime.AppCount()
	}
	if bundleFallback && len(genResp.Applications) < currentAppCount {
		slog.Warn("Bundle fallback produced a smaller app set; preserving current runtime",
			"current", currentAppCount, "fallback", len(genResp.Applications))
		return nil
	}
	d.runtime.Regenerate(genResp.Applications, nsCfg, resolveResult.Bundle)
	return nil
}

// isLocalhostAddr returns true if the listen address is bound to localhost only.
func isLocalhostAddr(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return true // parse error → assume localhost (safe default)
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		return false
	}
	if host == "localhost" || host == "::1" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// setupMTLS configures mTLS on the TCP listener for non-localhost access.
// Returns the (possibly wrapped) listener, handler, whether mTLS is active, and any error.
// On error, the listener is closed and returned as nil.
func (d *Daemon) setupMTLS(ln net.Listener, handler http.Handler, nsCfg *namespace.Config, tcpAddr string) (net.Listener, http.Handler, bool, error) {
	caPool, certCount, err := tlsutil.LoadCACertPool(config.WebUICADir())
	if err != nil {
		_ = ln.Close()
		return nil, handler, false, fmt.Errorf("load client CA pool: %w", err)
	}
	if certCount == 0 {
		_ = ln.Close()
		return nil, handler, false, fmt.Errorf("no client certs in %s — run: citeck webui cert --name admin", config.WebUICADir())
	}

	// Ensure server cert exists
	webuiTLSDir := config.WebUITLSDir()
	os.MkdirAll(webuiTLSDir, 0o755) //nolint:gosec // G301: TLS dir needs 0o755
	serverCertPath := filepath.Join(webuiTLSDir, "server.crt")
	serverKeyPath := filepath.Join(webuiTLSDir, "server.key")
	if _, statErr := os.Stat(serverCertPath); os.IsNotExist(statErr) {
		certHost := resolveServerCertHost(tcpAddr, nsCfg)
		slog.Info("Generating Web UI server certificate", "host", certHost)
		if genErr := tlsutil.GenerateSelfSignedCert(serverCertPath, serverKeyPath, []string{certHost}, 36500); genErr != nil {
			ln.Close()
			return nil, handler, false, fmt.Errorf("generate server cert: %w", genErr)
		}
	}

	serverCert, err := tls.LoadX509KeyPair(serverCertPath, serverKeyPath)
	if err != nil {
		ln.Close()
		return nil, handler, false, fmt.Errorf("load server cert: %w", err)
	}

	handler = MTLSIdentityMiddleware(handler)

	// Dynamic cert pool reload via GetConfigForClient
	var caMu sync.Mutex
	var caMtime time.Time
	cachedPool := caPool

	tlsCfg := &tls.Config{
		Certificates: []tls.Certificate{serverCert},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    caPool,
		MinVersion:   tls.VersionTLS13,
	}
	tlsCfg.GetConfigForClient = func(_ *tls.ClientHelloInfo) (*tls.Config, error) {
		caMu.Lock()
		defer caMu.Unlock()
		info, statErr := os.Stat(config.WebUICADir())
		if statErr != nil {
			return nil, nil // use existing config
		}
		if info.ModTime().After(caMtime) {
			newPool, n, loadErr := tlsutil.LoadCACertPool(config.WebUICADir())
			if loadErr == nil {
				cachedPool = newPool
				caMtime = info.ModTime()
				slog.Info("Reloaded client CA pool", "count", n)
			}
		}
		fresh := tlsCfg.Clone()
		fresh.ClientCAs = cachedPool
		fresh.GetConfigForClient = nil // returned config must not carry the callback
		return fresh, nil
	}

	slog.Info("mTLS enabled on Web UI", "trustedCerts", certCount)
	return tls.NewListener(ln, tlsCfg), handler, true, nil
}

// resolveServerCertHost determines the hostname for the server certificate SAN.
func resolveServerCertHost(tcpAddr string, nsCfg *namespace.Config) string {
	host, _, _ := net.SplitHostPort(tcpAddr)
	if host == "" || host == "0.0.0.0" || host == "::" {
		if nsCfg != nil && nsCfg.Proxy.Host != "" && nsCfg.Proxy.Host != "localhost" {
			return nsCfg.Proxy.Host
		}
		return "localhost"
	}
	return host
}

func (d *Daemon) broadcastEvent(evt api.EventDto) {
	// Seq assignment, ring push, and fanout all happen under eventMu so a
	// subscriber added between Add and fanout cannot observe a published seq
	// before the event reaches its channel. Paired with addSubscriber, which
	// snapshots eventSeq under the same lock — that snapshot is the cutoff
	// the replay path uses to avoid duplicating live deliveries.
	d.eventMu.Lock()
	defer d.eventMu.Unlock()
	evt.Seq = d.eventSeq.Add(1)
	if d.eventRing != nil {
		d.eventRing.push(evt)
	}
	for _, ch := range d.eventSubs {
		select {
		case ch <- evt:
		default:
			d.sseDropped.Add(1)
			slog.Warn("Event dropped for slow subscriber", "type", evt.Type)
		}
	}
}

const maxSSESubscribers = 100

// eventReplayBufferSize caps the ring buffer used by SSE reconnects. ~500 events
// covers typical disconnect windows even under pull-progress bursts; older
// events force the client to do a full resync via the existing gap-detection.
const eventReplayBufferSize = 500

// addSubscriber registers a new SSE channel and returns the cutoff Seq for
// replay filtering. Events with Seq <= cutoff were broadcast before the
// channel joined the subscriber list and will NOT arrive on `ch`; events with
// Seq > cutoff are guaranteed to arrive live. Both pieces of state are read
// under eventMu so broadcastEvent cannot interleave between them.
func (d *Daemon) addSubscriber() (ch chan api.EventDto, cutoffSeq int64, ok bool) {
	d.eventMu.Lock()
	defer d.eventMu.Unlock()
	if len(d.eventSubs) >= maxSSESubscribers {
		return nil, 0, false
	}
	ch = make(chan api.EventDto, 256)
	d.eventSubs = append(d.eventSubs, ch)
	return ch, d.eventSeq.Load(), true
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

// registerRoutes registers all API routes on the shared mux.
// Both Unix socket and TCP listeners use the same mux — localhost TCP is trusted
// (desktop thin client), non-localhost requires mTLS (which is also fully authenticated).
func (d *Daemon) registerRoutes(mux *http.ServeMux) {
	// Daemon
	mux.HandleFunc("GET "+api.DaemonStatus, d.handleDaemonStatus)
	mux.HandleFunc("POST "+api.DaemonShutdown, d.handleDaemonShutdown)
	mux.HandleFunc("PUT /api/v1/daemon/loglevel", d.handleSetLogLevel)
	mux.HandleFunc("GET "+api.DaemonLogs, d.handleDaemonLogs)

	// Namespace
	mux.HandleFunc("GET "+api.Namespace, d.handleGetNamespace)
	mux.HandleFunc("POST "+api.NamespaceStart, d.handleStartNamespace)
	mux.HandleFunc("POST "+api.NamespaceStop, d.handleStopNamespace)
	mux.HandleFunc("POST "+api.NamespaceReload, d.handleReloadNamespace)
	mux.HandleFunc("POST "+api.NamespaceUpgrade, d.handleUpgradeNamespace)
	mux.HandleFunc("GET "+api.NamespaceEdit, d.handleGetNamespaceEdit)
	mux.HandleFunc("PUT "+api.NamespaceEdit, d.handlePutNamespaceEdit)
	mux.HandleFunc("GET "+api.NamespaceCreateDefaults, d.handleNamespaceCreateDefaults)
	mux.HandleFunc("POST "+api.NamespaceAdminPassword, d.handleSetAdminPassword)
	mux.HandleFunc("GET "+api.RestartEvents, d.handleRestartEvents)
	mux.HandleFunc("GET /api/v1/diagnostics-file", d.handleDiagnosticsFile)

	// Config
	mux.HandleFunc("GET /api/v1/config", d.handleGetConfig)
	mux.HandleFunc("GET /api/v1/config/applied", d.handleGetAppliedConfig)
	mux.HandleFunc("PUT /api/v1/config", d.handlePutConfig)

	// App routes
	// Collection-level retry endpoint is registered BEFORE the {name}-templated
	// routes — Go's net/http mux matches more-specific paths first, but
	// registering the static path up top documents that "retry-pull-failed"
	// is not a per-app action.
	mux.HandleFunc("POST "+api.AppsRetryPullFailed, d.handleAppsRetryPullFailed)
	mux.HandleFunc("GET /api/v1/apps/{name}/logs", d.handleAppLogs)
	mux.HandleFunc("GET /api/v1/apps/{name}/inspect", d.handleAppInspect)
	mux.HandleFunc("POST /api/v1/apps/{name}/restart", d.handleAppRestart)
	mux.HandleFunc("POST /api/v1/apps/{name}/stop", d.handleAppStop)
	mux.HandleFunc("POST /api/v1/apps/{name}/start", d.handleAppStart)
	mux.HandleFunc("POST /api/v1/apps/{name}/exec", d.handleAppExec)
	mux.HandleFunc("GET /api/v1/apps/{name}/config", d.handleGetAppConfig)
	mux.HandleFunc("PUT /api/v1/apps/{name}/config", d.handlePutAppConfig)
	mux.HandleFunc("POST /api/v1/apps/{name}/config/reset", d.handleResetAppConfig)
	mux.HandleFunc("PUT /api/v1/apps/{name}/lock", d.handleAppLockToggle)
	mux.HandleFunc("GET /api/v1/apps/{name}/files", d.handleListAppFiles)
	mux.HandleFunc("POST /api/v1/apps/{name}/files/reset", d.handleResetAppFile)
	mux.HandleFunc("GET /api/v1/apps/{name}/files/{path...}", d.handleGetAppFile)
	mux.HandleFunc("PUT /api/v1/apps/{name}/files/{path...}", d.handlePutAppFile)

	// Events (SSE)
	mux.HandleFunc("GET "+api.Events, d.handleEvents)

	// Volumes
	mux.HandleFunc("GET /api/v1/volumes", d.handleListVolumes)
	mux.HandleFunc("DELETE /api/v1/volumes/{name}", d.handleDeleteVolume)

	// Health + Metrics
	mux.HandleFunc("GET "+api.Health, d.handleHealth)
	mux.HandleFunc("GET /api/v1/metrics", d.handleMetrics)

	// System dump
	mux.HandleFunc("GET /api/v1/system/dump", d.handleSystemDump)
	mux.HandleFunc("POST "+api.SystemOpenDir, d.handleSystemOpenDir)

	// Workspace operations
	mux.HandleFunc("POST "+api.WorkspaceUpdate, d.handleWorkspaceUpdate)

	// Multi-workspace CRUD + activate (desktop-only — handlers return 404 in server mode).
	mux.HandleFunc("GET "+api.Workspaces, d.handleListWorkspaces)
	mux.HandleFunc("POST "+api.Workspaces, d.handleCreateWorkspace)
	mux.HandleFunc("GET /api/v1/workspaces/{id}", d.handleGetWorkspace)
	mux.HandleFunc("PUT /api/v1/workspaces/{id}", d.handleUpdateWorkspace)
	mux.HandleFunc("DELETE /api/v1/workspaces/{id}", d.handleDeleteWorkspace)
	mux.HandleFunc("POST /api/v1/workspaces/{id}/activate", d.handleActivateWorkspace)

	// Git operations
	mux.HandleFunc("POST "+api.GitSkipPull, d.handleGitSkipPull)

	// Namespaces
	mux.HandleFunc("GET "+api.Namespaces, d.handleListNamespaces)
	mux.HandleFunc("POST "+api.Namespaces, d.handleCreateNamespace)
	mux.HandleFunc("DELETE /api/v1/namespaces/{id}", d.handleDeleteNamespace)
	mux.HandleFunc("POST /api/v1/namespaces/{id}/activate", d.handleActivateNamespace)
	mux.HandleFunc("POST /api/v1/namespaces/deactivate", d.handleDeactivateNamespace)
	mux.HandleFunc("GET "+api.Templates, d.handleGetTemplates)
	mux.HandleFunc("GET "+api.QuickStarts, d.handleGetQuickStarts)

	// Bundles
	mux.HandleFunc("GET "+api.Bundles, d.handleListBundles)

	// Secrets
	mux.HandleFunc("GET "+api.Secrets, d.handleListSecrets)
	mux.HandleFunc("POST "+api.Secrets, d.handleCreateSecret)
	mux.HandleFunc("DELETE /api/v1/secrets/{id}", d.handleDeleteSecret)
	mux.HandleFunc("GET /api/v1/secrets/{id}/test", d.handleTestSecret)

	// Secrets encryption management
	mux.HandleFunc("GET "+api.SecretsStatus, d.handleGetSecretsStatus)
	mux.HandleFunc("POST "+api.SecretsUnlock, d.handleUnlockSecrets)
	mux.HandleFunc("POST "+api.SecretsSetupPassword, d.handleSetupPassword)
	mux.HandleFunc("POST "+api.SecretsReset, d.handleResetSecrets)

	// Licenses (enterprise license management)
	mux.HandleFunc("GET /api/v1/licenses", d.handleListLicenses)
	mux.HandleFunc("POST /api/v1/licenses", d.handleCreateLicense)
	mux.HandleFunc("DELETE /api/v1/licenses/{id}", d.handleDeleteLicense)

	// Migration (master password for Kotlin encrypted secrets)
	mux.HandleFunc("GET "+api.MigrationStatus, d.handleGetMigrationStatus)
	mux.HandleFunc("POST "+api.MigrationMasterPassword, d.handleSubmitMasterPassword)

	// Forms
	mux.HandleFunc("GET /api/v1/forms/{formId}", d.handleGetForm)

	// Diagnostics
	mux.HandleFunc("GET "+api.Diagnostics, d.handleGetDiagnostics)
	mux.HandleFunc("POST "+api.DiagnosticsFix, d.handleDiagnosticsFix)

	// Snapshots
	mux.HandleFunc("GET "+api.Snapshots, d.handleListSnapshots)
	mux.HandleFunc("POST "+api.SnapshotsExport, d.handleExportSnapshot)
	mux.HandleFunc("POST "+api.SnapshotsImport, d.handleImportSnapshot)
	mux.HandleFunc("POST "+api.SnapshotsDownload, d.handleDownloadSnapshot)
	mux.HandleFunc("GET "+api.WorkspaceSnapshots, d.handleWorkspaceSnapshots)
	mux.HandleFunc("PUT /api/v1/snapshots/{name}", d.handleRenameSnapshot)
	mux.HandleFunc("DELETE /api/v1/snapshots/{name}", d.handleDeleteSnapshot)

	// Desktop-only: second-launch focus hand-off (Kotlin AppLocalSocket parity).
	// Server mode has no native window to raise; route is not registered there.
	if config.IsDesktopMode() {
		mux.HandleFunc("POST /desktop/focus", d.handleDesktopFocus)
	}

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

func writeError(w http.ResponseWriter, httpCode int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(httpCode)
	json.NewEncoder(w).Encode(api.ErrorDto{
		Error:   http.StatusText(httpCode),
		Message: msg,
	})
}

// writeErrorCode writes a JSON error response with a machine-readable error code.
func writeErrorCode(w http.ResponseWriter, httpCode int, errCode, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(httpCode)
	json.NewEncoder(w).Encode(api.ErrorDto{
		Error:   http.StatusText(httpCode),
		Code:    errCode,
		Message: msg,
	})
}

// writeInternalError logs the full error and returns a generic 500 response to the client.
func writeInternalError(w http.ResponseWriter, err error) {
	slog.Error("handler error", "err", err)
	writeErrorCode(w, http.StatusInternalServerError, api.ErrCodeInternalError, "internal error")
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

// readJSON decodes a JSON request body with a 1 MiB hard ceiling. MaxBytesReader
// is preferred over io.LimitReader because it also caps r.ContentLength-
// honoring handlers, signals the client cleanly with a 413-shaped error, and
// short-circuits streaming decode paths.
func readJSON(r *http.Request, v any) error {
	r.Body = http.MaxBytesReader(nil, r.Body, 1<<20)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		return fmt.Errorf("decode JSON body: %w", err)
	}
	return nil
}

// makeTokenLookup creates a function that looks up auth tokens from the secret store.
// Tokens are pre-fetched at creation time into an immutable map for efficiency.
// Rebuilt on each reload to reflect secret mutations.
func makeTokenLookup(reader secretReader) bundle.TokenLookupFunc {
	if reader == nil {
		return func(string) string { return "" }
	}
	// Pre-fetch all secrets into a lookup map
	tokensByScope := make(map[string]string)
	secrets, err := reader.ListSecrets()
	if err == nil {
		for _, s := range secrets {
			sec, err := reader.GetSecret(s.ID)
			if err != nil {
				continue // ErrSecretsLocked → skip gracefully
			}
			if string(s.Type) != "" {
				tokensByScope[string(s.Type)] = sec.Value
			}
			if s.Scope != "" {
				tokensByScope[s.Scope] = sec.Value
			}
		}
	}
	return func(authType string) string {
		return tokensByScope[authType]
	}
}

// makeRegistryAuthFunc creates a function that returns Docker registry credentials
// by matching image host against workspace config's imageReposByHost.
// Registry secrets are pre-fetched into a map at creation time for efficiency.
// The function is rebuilt on namespace reload to reflect secret mutations.
func makeRegistryAuthFunc(wsCfg *bundle.WorkspaceConfig, reader secretReader) namespace.RegistryAuthFunc {
	if wsCfg == nil || reader == nil {
		return nil
	}
	reposByHost := wsCfg.ImageReposByHost()

	// Pre-fetch all registry credentials into an immutable map
	authByHost := buildRegistryAuthCache(reposByHost, reader)
	if len(authByHost) == 0 {
		return nil
	}

	return func(img string) *docker.RegistryAuth {
		host := img
		if idx := strings.Index(host, "/"); idx > 0 {
			host = host[:idx]
		}
		auth, ok := authByHost[host]
		if !ok {
			return nil
		}
		return auth
	}
}

// buildRegistryAuthCache pre-fetches all registry secrets into a map keyed by host.
//
// Username and password are read from the typed Secret fields (BASIC_AUTH parity
// with Kotlin AuthSecret.Basic). A legacy "user:pass" packed Value is split
// here as a last-resort fallback for any secret that somehow survived the
// FileStore / SQLite-v3 migration paths without a Username column populated.
func buildRegistryAuthCache(reposByHost map[string]bundle.ImageRepo, reader secretReader) map[string]*docker.RegistryAuth {
	result := make(map[string]*docker.RegistryAuth)
	secrets, err := reader.ListSecrets()
	if err != nil {
		return result
	}
	scopeSecrets := make(map[string]*storage.Secret)
	for _, s := range secrets {
		if s.Scope != "" {
			sec, err := reader.GetSecret(s.ID)
			if err != nil {
				continue // ErrSecretsLocked → skip gracefully
			}
			scopeSecrets[s.Scope] = sec
		}
	}
	addAuth := func(host string, sec *storage.Secret) {
		username, password := sec.Username, sec.Value
		if username == "" {
			parts := strings.SplitN(sec.Value, ":", 2)
			if len(parts) != 2 {
				return
			}
			username, password = parts[0], parts[1]
		}
		if username == "" || password == "" {
			return
		}
		result[host] = &docker.RegistryAuth{
			Username: username,
			Password: password,
			Registry: "https://" + host,
		}
	}
	for host, repo := range reposByHost {
		if repo.AuthType == "" {
			continue
		}
		sec := scopeSecrets[repo.AuthType]
		if sec == nil {
			sec = scopeSecrets[host]
		}
		if sec == nil {
			// Kotlin migration compat: scope = "images-repo:{host}"
			sec = scopeSecrets["images-repo:"+host]
		}
		if sec == nil {
			continue
		}
		addAuth(host, sec)
	}
	// Kotlin v1.x parity: a secret with scope "images-repo:<host>" should
	// authenticate pulls from <host> even when workspace-v1.yml doesn't
	// declare that host under imageRepos. Kotlin built the secret ID from
	// the image host directly. Skipping this fallback strands migrated
	// secrets for any registry the current workspace config no longer lists
	// (e.g. enterprise-registry.citeck.ru when only harbor.citeck.ru is
	// declared) — pulls silently degrade to anonymous → 401.
	const scopePrefix = "images-repo:"
	for scope, sec := range scopeSecrets {
		host, ok := strings.CutPrefix(scope, scopePrefix)
		if !ok || host == "" {
			continue
		}
		if _, already := result[host]; already {
			continue
		}
		addAuth(host, sec)
	}
	return result
}

// resolveSystemSecrets reads or generates JWT, OIDC, admin, and citeck-SA values.
//
// System secrets are plain (unencrypted) in launcher_state — they only protect
// the local machine itself (Kotlin v1.x parity: JWT was a hardcoded constant,
// OIDC was hardcoded in realm.json, KK admin password was "admin"). Their
// secrecy adds nothing on a developer workstation, and on a server they're
// already constrained by the binary's own filesystem permissions. The
// SecretService keeps holding USER-added auth secrets (Harbor / nexus / git
// tokens) where encryption matters — those reach external resources.
//
// Migration paths supported:
//   - Read from launcher_state plain (new home).
//   - Read from SecretService (older installs where SYSTEM rows were stored
//     encrypted alongside user secrets); migrated to plain on first read.
//   - Read from conf/secrets/<id>-secret plain file (pre-Store launcher);
//     migrated to plain on first read.
//   - Generate fresh.
//
// In desktop mode the admin password is always "admin" and citeck SA defaults
// to "citeck" — the Kotlin v1.x developer-tool convention.
func resolveSystemSecrets(store storage.Store, svc *storage.SecretService, desktop bool) (namespace.SystemSecrets, error) {
	var secrets namespace.SystemSecrets

	// JWT
	jwt, err := resolveOneSystemSecret(store, svc, "_jwt", func() string {
		b := make([]byte, 64)
		if _, err := rand.Read(b); err != nil {
			slog.Error("Failed to generate JWT secret", "err", err)
			return ""
		}
		return base64.StdEncoding.EncodeToString(b)
	})
	if err != nil {
		return secrets, fmt.Errorf("resolve JWT secret: %w", err)
	}
	secrets.JWT = jwt

	// OIDC
	oidc, err := resolveOneSystemSecret(store, svc, "_oidc", func() string {
		b := make([]byte, 32)
		if _, randErr := rand.Read(b); randErr != nil {
			slog.Error("Failed to generate OIDC secret", "err", randErr)
			return ""
		}
		return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
	})
	if err != nil {
		return secrets, fmt.Errorf("resolve OIDC secret: %w", err)
	}
	secrets.OIDC = oidc

	// ecos-app realm admin password.
	if desktop {
		secrets.AdminPassword = "admin"
	} else {
		adminPass, adminErr := resolveOneSystemSecret(store, svc, "_admin_password", func() string {
			p, genErr := namespace.GenerateSimpleAdminPassword()
			if genErr != nil {
				slog.Error("Failed to generate admin password", "err", genErr)
				return ""
			}
			return p
		})
		if adminErr != nil {
			return secrets, fmt.Errorf("resolve admin password: %w", adminErr)
		}
		secrets.AdminPassword = adminPass
	}

	// citeck SA password. Desktop default is "citeck" (username = password
	// convenience on a dev workstation), server keeps a 32-char random.
	citeckSA, saErr := resolveOneSystemSecret(store, svc, "_citeck_sa", func() string {
		if legacy, err := svc.GetSecret("_launcher_sa"); err == nil && legacy.Value != "" {
			slog.Info("Migrating legacy _launcher_sa secret to _citeck_sa")
			return legacy.Value
		}
		if desktop {
			return "citeck"
		}
		p, genErr := namespace.GenerateCiteckSAPassword()
		if genErr != nil {
			slog.Error("Failed to generate citeck SA password", "err", genErr)
			return ""
		}
		return p
	})
	if saErr != nil {
		return secrets, fmt.Errorf("resolve citeck SA: %w", saErr)
	}
	if citeckSA == "" {
		return secrets, fmt.Errorf("citeck SA password is empty (generation failed)")
	}
	secrets.CiteckSA = citeckSA

	// Legacy cleanup: delete _launcher_sa from BOTH the new plain state and
	// the SecretService once migration produced a fresh _citeck_sa. Errors
	// non-fatal — migration already succeeded.
	_ = store.SetStateValue(sysSecretKey("_launcher_sa"), "")
	if legacy, err := svc.GetSecret("_launcher_sa"); err == nil && legacy.Value != "" {
		if delErr := svc.DeleteSecret("_launcher_sa"); delErr != nil {
			slog.Warn("Failed to delete legacy _launcher_sa secret after migration", "err", delErr)
		} else {
			slog.Info("Deleted legacy _launcher_sa secret after migration to _citeck_sa")
		}
	}

	return secrets, nil
}

// sysSecretKey returns the launcher_state key for a system secret id.
// Keeps the `_sys_` prefix as the source-of-truth namespace for plain
// system values so they never collide with SecretService-managed user secrets.
func sysSecretKey(id string) string {
	return "_sys" + id // e.g. "_jwt" → "_sys_jwt"
}

// resolveOneSystemSecret returns a system secret value, sourcing it in this
// priority order and migrating older locations into the new plain state:
//   1. launcher_state plain (new home — `_sys<id>`).
//   2. SecretService SYSTEM row (older installs); migrate to plain + delete.
//   3. conf/secrets/<id-without-underscore>-secret plain file (pre-Store
//      launcher); migrate to plain + delete.
//   4. Generate fresh.
func resolveOneSystemSecret(store storage.Store, svc *storage.SecretService, id string, generate func() string) (string, error) {
	stateKey := sysSecretKey(id)

	// 1. Try launcher_state plain
	if v, err := store.GetStateValue(stateKey); err == nil && v != "" {
		return v, nil
	}

	// 2. Fallback: SecretService SYSTEM row — migrate to plain + delete.
	if sec, err := svc.GetSecret(id); err == nil && sec.Value != "" {
		value := sec.Value
		if id == "_jwt" {
			value = migrateJWTSecretToStdBase64(value)
		}
		if saveErr := store.SetStateValue(stateKey, value); saveErr != nil {
			return "", fmt.Errorf("save migrated secret %s: %w", id, saveErr)
		}
		slog.Info("Migrated SYSTEM secret out of SecretService into plain state", "id", id)
		if delErr := svc.DeleteSecret(id); delErr != nil {
			slog.Warn("Failed to delete migrated SYSTEM secret from SecretService", "id", id, "err", delErr)
		}
		return value, nil
	}

	// 3. Fallback: read plain file (pre-Store launcher migration).
	plainFile := filepath.Join(config.ConfDir(), strings.TrimPrefix(id, "_")+"-secret")
	if data, readErr := os.ReadFile(plainFile); readErr == nil && len(data) > 0 { //nolint:gosec // path from trusted confDir
		value := string(data)
		if id == "_jwt" {
			value = migrateJWTSecretToStdBase64(value)
		}
		slog.Info("Migrating system secret from plain file to launcher_state", "id", id)
		if saveErr := store.SetStateValue(stateKey, value); saveErr != nil {
			return "", fmt.Errorf("save migrated secret %s: %w", id, saveErr)
		}
		_ = os.Remove(plainFile)
		return value, nil
	}

	// 4. Generate new.
	value := generate()
	if value == "" {
		return "", fmt.Errorf("failed to generate secret %s", id)
	}
	slog.Info("Generated new system secret", "id", id)
	if saveErr := store.SetStateValue(stateKey, value); saveErr != nil {
		return "", fmt.Errorf("save generated secret %s: %w", id, saveErr)
	}
	return value, nil
}

// migrateJWTSecretToStdBase64 ensures the JWT secret uses standard base64 encoding.
// Old launcher versions used RawURLEncoding (no padding, URL-safe alphabet). If detected,
// the secret is re-encoded to StdEncoding. The caller persists the corrected value.
func migrateJWTSecretToStdBase64(stored string) string {
	if _, err := base64.StdEncoding.DecodeString(stored); err == nil {
		return stored // already standard base64
	}
	raw, err := base64.RawURLEncoding.DecodeString(stored)
	if err != nil {
		slog.Warn("JWT secret is not valid base64, keeping as-is", "err", err)
		return stored
	}
	slog.Info("Migrated JWT secret from RawURLEncoding to StdEncoding")
	return base64.StdEncoding.EncodeToString(raw)
}

// importSnapshotIfNeeded checks for the snapshot field in namespace config and imports
// the snapshot if it hasn't been imported yet (tracked by a marker file).
//
// The marker (`imported-<nsID>`) stays in the per-namespace `volumesBase/snapshots/`
// dir because it is namespace-scoped state. The archive itself lives in the
// workspace-shared cache `<AppDir>/ws/<wsID>/snapshots/<snapshotID>.zip`, matching
// Kotlin's WorkspaceSnapshots layout so multiple namespaces
// in the same workspace share a single download.
//
//nolint:nestif // snapshot import requires nested SHA256 verification and download fallback logic
func importSnapshotIfNeeded(nsCfg *namespace.Config, wsCfg *bundle.WorkspaceConfig, dc *docker.Client, wsID, volumesBase string) {
	if nsCfg.Snapshot == "" || wsCfg == nil {
		return
	}

	markerDir := filepath.Join(volumesBase, "snapshots")
	os.MkdirAll(markerDir, 0o755) //nolint:gosec // G301: marker dir needs 0o755
	markerFile := filepath.Join(markerDir, "imported-"+nsCfg.ID)

	// Check marker — if already imported this snapshot, skip
	if data, err := os.ReadFile(markerFile); err == nil { //nolint:gosec // G304: markerFile is derived from internal config
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

	// Workspace-shared cache path: <AppDir>/ws/<wsID>/snapshots/<snapshotID>.zip
	cacheDir := config.WorkspaceSnapshotsDir(wsID)
	if err := os.MkdirAll(cacheDir, 0o755); err != nil { //nolint:gosec // G301: snapshot cache needs 0o755
		slog.Error("Create workspace snapshots cache dir", "err", err)
		return
	}
	destPath := filepath.Join(cacheDir, nsCfg.Snapshot+".zip")

	// Fast-path: cached file with matching SHA — skip download.
	needsDownload := true
	if _, err := os.Stat(destPath); err == nil { //nolint:gosec // G304: destPath built from validated wsID + snapshot id
		if snapDef.SHA256 != "" {
			if actual, hashErr := snapshot.FileSHA256(destPath); hashErr == nil && strings.EqualFold(actual, snapDef.SHA256) {
				needsDownload = false
			} else {
				// Preserve stale file as `_outdated_<ts>` (Kotlin parity);
				// fall back to delete on rename failure so the next attempt
				// has a clean destination.
				if renameErr := snapshot.StashOutdatedFile(destPath); renameErr != nil {
					slog.Debug("Stash outdated cached snapshot failed; removing", "path", destPath, "err", renameErr)
					_ = os.Remove(destPath)
				}
			}
		} else {
			needsDownload = false
		}
	}
	importCtx, importCancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer importCancel()

	if needsDownload {
		// Kotlin-parity retry: 100 total / 3 without progress / 3s delay.
		if dlErr := snapshot.DownloadWithRetry(importCtx, nil, snapDef.URL, destPath, snapDef.SHA256, nil); dlErr != nil {
			slog.Error("Snapshot download failed", "url", snapDef.URL, "err", dlErr)
			return
		}
	}

	// Import
	if _, err := snapshot.Import(importCtx, dc, destPath, volumesBase, nil); err != nil {
		slog.Error("Snapshot import failed", "err", err)
		return
	}

	// Write marker
	os.WriteFile(markerFile, []byte(nsCfg.Snapshot), 0o644) //nolint:gosec // G306: marker file is non-sensitive
	slog.Info("Snapshot auto-import completed", "snapshot", nsCfg.Snapshot, "ns", nsCfg.ID)
}

// isRegularFile returns true if path exists and is a regular file.
func isRegularFile(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.Mode().IsRegular()
}

// prepareDestPath handles the pre-write checks for a single runtime file:
//   - If the path is a directory (Docker auto-created it), remove it so we can
//     write a regular file in its place.
//   - If the path is an existing regular file with the same size and identical
//     contents, return skip=true so the caller can skip the write (avoids
//     touching the mtime and keeps the container deployment hash stable).
//
// destPath is always filepath.Join(baseDir, relPath) where baseDir is the
// trusted namespace runtime directory — the path is not user-supplied.
func prepareDestPath(destPath string, content []byte) (skip bool, err error) {
	fi, statErr := os.Stat(destPath)
	if statErr != nil {
		return false, nil // path does not exist yet — proceed with write
	}
	if fi.IsDir() {
		// Case 1: Docker auto-created a dir instead of a file; remove it.
		if err := os.RemoveAll(destPath); err != nil {
			return false, fmt.Errorf("remove dir at runtime file path: %w", err)
		}
		return false, nil
	}
	// Case 2: same size — compare bytes, skip if unchanged.
	if !fi.Mode().IsRegular() || int64(len(content)) != fi.Size() {
		return false, nil
	}
	existing, readErr := os.ReadFile(destPath) //nolint:gosec // G304: destPath is filepath.Join(trusted baseDir, relPath)
	if readErr != nil || !bytes.Equal(existing, content) {
		return false, nil
	}
	// Before skipping, make sure the mode still matches what we'd write —
	// a .sh that somehow lost its executable bit (umask change, prior
	// launcher version bug, manual edit) would otherwise never recover,
	// since we'd never rewrite the file.
	wantPerm := os.FileMode(0o644)
	if strings.HasSuffix(destPath, ".sh") {
		wantPerm = 0o755
	}
	if fi.Mode().Perm() != wantPerm {
		_ = os.Chmod(destPath, wantPerm)
	}
	return true, nil
}

// writeRuntimeFiles applies the generator's file map (the full set of
// files any app can bind-mount) to disk under baseDir. Single source of
// truth — nothing else writes into this directory tree. Handles three
// edge cases that the naïve loop-and-WriteFile version did not:
//
//  1. A host path exists as an EMPTY DIRECTORY where the container
//     expects a file. Docker auto-creates a directory whenever it needs
//     to bind-mount a path that doesn't exist on the host; if postgres
//     was recreated while its config was missing, we end up with
//     /opt/citeck/data/runtime/.../postgres/postgresql.conf as a dir
//     and postgres chokes with "configuration file contains errors".
//  2. Content is identical — skip the atomic-rename dance entirely, so
//     unchanged files don't get a new mtime each regenerate (preserves
//     the container deployment hash so Docker doesn't pointlessly
//     recreate containers whose files didn't really change).
//  3. Parent directory doesn't exist yet — MkdirAll first.
//
// Shell scripts (.sh) are written 0755, everything else 0644. The optional
// `edited` map (keys: "<app>/<rel-path>", no leading "./") flags files whose
// on-disk content was modified by the user through the Web UI; those entries
// are skipped so user edits survive reload/regenerate. Passing nil disables
// the skip behavior (initial materialization paths where the user-edit
// set has not yet been restored use nil).
func writeRuntimeFiles(baseDir string, files map[string][]byte, edited map[string]bool) {
	for filePath, content := range files {
		if edited[filePath] {
			slog.Debug("Skipping user-edited file", "path", filePath)
			continue
		}
		destPath := filepath.Join(baseDir, filePath)
		skip, prepErr := prepareDestPath(destPath, content)
		if prepErr != nil {
			slog.Error("Failed to remove stale dir at file path", "path", destPath, "err", prepErr)
			continue
		}
		if skip {
			continue
		}
		if mkdirErr := os.MkdirAll(filepath.Dir(destPath), 0o755); mkdirErr != nil { //nolint:gosec // G301: dirs need 0o755 for Docker bind-mount access
			slog.Error("Failed to create dir for generated file", "path", destPath, "err", mkdirErr)
			continue
		}
		perm := os.FileMode(0o644)
		if strings.HasSuffix(filePath, ".sh") {
			perm = 0o755
		}
		if writeErr := fsutil.AtomicWriteFile(destPath, content, perm); writeErr != nil {
			slog.Error("Failed to write generated file", "path", destPath, "err", writeErr)
			continue
		}
		// fsutil.AtomicWriteFile respects umask for the temp file; re-chmod
		// to the exact perm we want (matters for .sh which need 0755).
		if chmodErr := os.Chmod(destPath, perm); chmodErr != nil {
			slog.Warn("Failed to chmod generated file", "path", destPath, "err", chmodErr)
		}
	}
}

// readEditedFileOverlay reads on-disk content for every persisted user-edit
// key under volumesBase. Used at daemon startup before the runtime exists, so
// the first Generate call sees the user's edits in its VolumesContentHash
// input and recreates containers whose mounted files were changed in a prior
// session. Missing/unreadable files are skipped — writeRuntimeFiles will
// rematerialize the default the next time that key falls out of editedFiles.
func readEditedFileOverlay(volumesBase string, keys []string) map[string][]byte {
	if len(keys) == 0 {
		return nil
	}
	out := make(map[string][]byte, len(keys))
	for _, k := range keys {
		abs := filepath.Join(volumesBase, k)
		data, err := os.ReadFile(abs) //nolint:gosec // G304: key is constrained to volumesBase by the original write
		if err != nil {
			continue
		}
		out[k] = data
	}
	return out
}

// ensureACMECert obtains or refreshes the Let's Encrypt certificate for the
// proxy when TLS + LE are enabled, then wires nsCfg.Proxy.TLS.{CertPath,KeyPath}
// to the resulting cert. Falls back to generating a self-signed cert if LE
// fails and no usable cert is present. Returns the acme.Client when LE was
// attempted (so the caller can build a renewal service), or nil otherwise.
//
// contextLabel is appended to log messages to distinguish Start vs reload flows
// (e.g. "on reload"); pass "" to suppress.
func ensureACMECert(nsCfg *namespace.Config, contextLabel string) *acme.Client {
	if !nsCfg.Proxy.TLS.Enabled || !nsCfg.Proxy.TLS.LetsEncrypt {
		return nil
	}
	host := nsCfg.Proxy.Host
	if host == "" || host == "localhost" {
		slog.Warn("Let's Encrypt requires a public hostname, skipping", "host", host, "context", contextLabel)
		return nil
	}
	acmeClient := acme.NewClient(config.DataDir(), config.ConfDir(), host)
	acmeErr := obtainACMECertIfNeeded(acmeClient, host, contextLabel)
	if acmeClient.CertMatchesHost() {
		nsCfg.Proxy.TLS.CertPath = acmeClient.CertPath()
		nsCfg.Proxy.TLS.KeyPath = acmeClient.KeyPath()
	}
	if nsCfg.Proxy.TLS.CertPath == "" {
		slog.Warn("Let's Encrypt cert not available, falling back to self-signed", "reason", acmeErr, "context", contextLabel)
		generateSelfSignedCertForConfig(nsCfg)
	}
	return acmeClient
}

// obtainACMECertIfNeeded drives a single LE obtain attempt for `acmeClient`
// when the on-disk cert doesn't match the host. Honors the persisted rate-limit
// marker (written by RenewalService on LE 429 / "too many" errors). Returns
// any obtain error (or nil if the cert is already good or rate-limit was the
// reason to skip).
func obtainACMECertIfNeeded(acmeClient *acme.Client, host, contextLabel string) error {
	if acmeClient.CertMatchesHost() {
		return nil
	}
	if limited, retryAfter, rlErr := acme.IsRateLimited(config.DataDir(), host); rlErr == nil && limited {
		slog.Warn("Let's Encrypt rate-limit marker active, skipping obtain", "host", host, "retryAfter", retryAfter, "context", contextLabel)
		return fmt.Errorf("rate-limited until %s", retryAfter.Format(time.RFC3339))
	}
	label := "Obtaining Let's Encrypt certificate"
	if contextLabel != "" {
		label += " " + contextLabel
	}
	slog.Info(label, "host", host)
	acmeCtx, acmeCancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer acmeCancel()
	if err := acmeClient.ObtainCertificate(acmeCtx); err != nil {
		slog.Error("Let's Encrypt certificate obtainment failed", "err", err, "context", contextLabel)
		return fmt.Errorf("obtain LE certificate: %w", err)
	}
	slog.Info("Let's Encrypt certificate obtained", "cert", acmeClient.CertPath())
	return nil
}

// ensureSelfSignedCert generates a self-signed cert if TLS is enabled without LE and no cert is configured.
func ensureSelfSignedCert(nsCfg *namespace.Config) {
	if !nsCfg.Proxy.TLS.Enabled || nsCfg.Proxy.TLS.LetsEncrypt || nsCfg.Proxy.TLS.CertPath != "" {
		return
	}
	generateSelfSignedCertForConfig(nsCfg)
}

// generateSelfSignedCertForConfig generates a self-signed cert and updates the config paths.
// Called directly as LE fallback (bypassing the LetsEncrypt guard in ensureSelfSignedCert).
func generateSelfSignedCertForConfig(nsCfg *namespace.Config) {
	host := nsCfg.Proxy.Host
	if host == "" {
		host = "localhost"
	}
	tlsDir := filepath.Join(config.ConfDir(), "tls")
	os.MkdirAll(tlsDir, 0o755) //nolint:gosec // G301: TLS dir needs 0o755
	certPath := filepath.Join(tlsDir, "server.crt")
	keyPath := filepath.Join(tlsDir, "server.key")
	if !isRegularFile(certPath) {
		slog.Info("Generating self-signed certificate", "host", host)
		if err := tlsutil.GenerateSelfSignedCert(certPath, keyPath, []string{host}, 365); err != nil {
			slog.Error("Failed to generate self-signed cert", "err", err)
		}
	}
	nsCfg.Proxy.TLS.CertPath = certPath
	nsCfg.Proxy.TLS.KeyPath = keyPath
}
