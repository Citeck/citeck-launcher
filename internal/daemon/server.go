package daemon

import (
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
	"github.com/citeck/citeck-launcher/internal/appfiles"
	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/docker"
	"github.com/citeck/citeck-launcher/internal/fsutil"
	"github.com/citeck/citeck-launcher/internal/h2migrate"
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
	Desktop        bool           // desktop mode: file-only logging, no signal handler
	NoUI           bool           // disable web UI (TCP listener)
	Offline        bool           // skip all git operations, fail if local data missing
	Version        string         // build version injected via ldflags
	MasterPassword string         // master password for secrets decryption (server mode)
	Ctx            context.Context // external context (desktop provides; nil = CLI uses signals)
	ReadyCh        chan<- string   // receives Web UI URL when ready (empty string if no UI); nil = ignored
	LogWriter      io.Writer      // additional log destination (desktop captures startup logs); nil = ignored
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
	configMu        sync.RWMutex // protects nsConfig, bundleDef, appDefs, workspaceConfig, systemSecrets
	version         string
	bundleError     string // non-empty if bundle resolution failed
	acmeRenewal     *acme.RenewalService
	shutdownOnce    sync.Once
	bgCtx           context.Context    // canceled on daemon shutdown
	bgCancel        context.CancelFunc
	bgWg            sync.WaitGroup     // tracks background goroutines (snapshot, downloads)
	snapshotMu      sync.Mutex         // guards concurrent snapshot import/export
	daemonCfg       config.DaemonConfig
	eventSeq        atomic.Int64       // monotonic event sequence counter
	sseDropped      atomic.Int64       // SSE events dropped due to slow consumers
	logWriter       *fsutil.RotatingWriter
	logLevel        *slog.LevelVar
	systemSecrets   namespace.SystemSecrets // resolved JWT/OIDC secrets
	desktop         bool                   // desktop mode: log writer shared across restarts
	reloadMu        sync.Mutex             // guards concurrent reload requests
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
		// JAR is embedded in the binary. JRE downloaded from Adoptium if Java not in PATH.
		// Falls back to filesystem migration if JAR approach fails.
		migStatus := h2migrate.CheckMigration(config.HomeDir())
		if migStatus.Needed {
			javaPath := migStatus.JavaPath
			if !migStatus.HasJava {
				slog.Info("Java not found, downloading Adoptium JRE for migration")
				var dlErr error
				javaPath, dlErr = h2migrate.DownloadJRE(config.HomeDir())
				if dlErr != nil {
					slog.Error("JRE download failed", "err", dlErr)
				}
				defer h2migrate.CleanupJRE(config.HomeDir())
			}

			if migStore, migErr := storage.NewSQLiteStore(config.HomeDir()); migErr == nil {
				var result *h2migrate.MigrateResult
				if javaPath != "" {
					slog.Info("Running H2 migration", "java", javaPath)
					var migErr error
					result, migErr = h2migrate.RunJarMigration(config.HomeDir(), javaPath, migStore)
					if migErr != nil {
						slog.Error("JAR migration failed, trying filesystem fallback", "err", migErr)
					}
				}
				if result == nil {
					slog.Warn("Using filesystem fallback migration (no secrets, no namespace names)")
					result, _ = h2migrate.Migrate(config.HomeDir(), migStore)
				}
				if result != nil {
					slog.Info("H2 migration complete",
						"workspaces", result.Workspaces,
						"secrets", result.Secrets,
						"namespaces", result.Namespaces,
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
				if state.NamespaceID != "" {
					nsID = state.NamespaceID
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

	// Initialize SecretService (transparent encryption layer for all modes)
	secretSvc, err := storage.NewSecretService(store)
	if err != nil {
		return fmt.Errorf("create secret service: %w", err)
	}
	if secretSvc.IsEncrypted() {
		if secretSvc.IsDefaultPassword() {
			// Default password — auto-unlock
			if unlockErr := secretSvc.Unlock("citeck"); unlockErr != nil {
				slog.Warn("Auto-unlock with default password failed", "err", unlockErr)
			} else {
				slog.Info("Secrets auto-unlocked with default password")
			}
		} else if config.IsDesktopMode() {
			// Desktop mode: Web UI unlock flow — don't block startup
			slog.Info("Secrets are encrypted with custom password, waiting for unlock via Web UI")
		} else {
			// Server mode: unlock now with password from CLI
			if opts.MasterPassword == "" {
				return fmt.Errorf("secrets are encrypted but no master password provided")
			}
			if unlockErr := secretSvc.Unlock(opts.MasterPassword); unlockErr != nil {
				return fmt.Errorf("unlock secrets: %w", unlockErr)
			}
			slog.Info("Secrets unlocked successfully")
		}
	} else {
		// First start: set up encryption with the default password so that
		// secrets generated later in this session are encrypted immediately.
		// Previously, encryption was set up by the install CLI in a separate
		// process after the daemon had already saved secrets as plaintext,
		// causing a split-brain where the daemon's in-memory SecretService
		// had encrypted=false while files on disk were encrypted.
		if setupErr := secretSvc.SetMasterPassword(storage.DefaultMasterPassword, true); setupErr != nil {
			slog.Warn("Failed to set up default encryption", "err", setupErr)
		} else {
			slog.Info("Secrets encryption initialized with default password")
		}
	}

	// Load namespace config (mode-aware path)
	nsCfgPath := config.ResolveNamespaceConfigPath(wsID, nsID)
	nsCfg, err := namespace.LoadNamespaceConfig(nsCfgPath)
	if err != nil {
		slog.Warn("No namespace config found", "path", nsCfgPath, "err", err)
		nsCfg = nil
	}

	var bundleDef *bundle.Def
	var wsCfg *bundle.WorkspaceConfig
	var runtime *namespace.Runtime
	var appDefs []appdef.ApplicationDef
	var cloudCfgSrv *CloudConfigServer
	var bundleError string
	var systemSecrets namespace.SystemSecrets
	volumesBase := config.ResolveVolumesBase(wsID, nsID)

	// Resolve workspace config first — needed by wizard even without a namespace.
	bundlesDataDir := config.DataDir()
	if config.IsDesktopMode() {
		bundlesDataDir = filepath.Join(config.HomeDir(), "ws", wsID)
	}
	resolver := bundle.NewResolverWithAuth(bundlesDataDir, makeTokenLookup(secretSvc))
	// Server mode: never auto-pull git repos (use 'citeck workspace update' for manual sync).
	// Desktop mode: auto-pull with throttling. --offline flag: skip git entirely.
	if opts.Offline || !config.IsDesktopMode() {
		resolver.SetOffline(true)
	}
	wsCfg = resolver.ResolveWorkspaceOnly()

	if nsCfg != nil {
		if nsCfg.ID == "" {
			nsCfg.ID = nsID
		}

		// Resolve bundle (reuses resolver created above for workspace config).
		resolveResult, resolveErr := resolver.Resolve(nsCfg.BundleRef)
		if resolveErr != nil {
			if opts.Offline {
				return fmt.Errorf("offline mode: bundle %q not available locally — use 'citeck workspace import' to provide workspace data: %w", nsCfg.BundleRef, resolveErr)
			}
			// Fallback to cached bundle from persisted state (survives bundle file deletion/move)
			cachedState := namespace.LoadNsState(volumesBase, nsID)
			if cachedState != nil && cachedState.CachedBundle != nil && !cachedState.CachedBundle.IsEmpty() {
				slog.Warn("Bundle resolution failed, using cached bundle", "ref", nsCfg.BundleRef, "err", resolveErr,
					"cachedVersion", cachedState.CachedBundle.Key.Version, "cachedApps", len(cachedState.CachedBundle.Applications))
				resolveResult = &bundle.ResolveResult{Bundle: cachedState.CachedBundle, Workspace: &bundle.WorkspaceConfig{}}
			} else {
				slog.Error("Failed to resolve bundle and no cache available — daemon starts with 0 apps", "ref", nsCfg.BundleRef, "err", resolveErr)
				bundleError = resolveErr.Error()
				resolveResult = &bundle.ResolveResult{Bundle: &bundle.EmptyDef, Workspace: &bundle.WorkspaceConfig{}}
			}
		}
		bundleDef = resolveResult.Bundle
		wsCfg = resolveResult.Workspace

		slog.Info("Using bundle", "ref", nsCfg.BundleRef, "apps", len(bundleDef.Applications))

		// Self-signed cert: generate if TLS enabled + no cert paths + no LE
		ensureSelfSignedCert(nsCfg)

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
				var acmeErr error
				if !acmeClient.CertMatchesHost() {
					slog.Info("Obtaining Let's Encrypt certificate", "host", host)
					acmeCtx, acmeCancel := context.WithTimeout(context.Background(), 120*time.Second)
					acmeErr = acmeClient.ObtainCertificate(acmeCtx)
					acmeCancel()
					if acmeErr != nil {
						slog.Error("Let's Encrypt certificate obtainment failed", "err", acmeErr)
					} else {
						slog.Info("Let's Encrypt certificate obtained", "cert", certPath)
					}
				}

				// Update config to use ACME cert paths (only if cert is a regular file)
				if isRegularFile(certPath) {
					nsCfg.Proxy.TLS.CertPath = certPath
					nsCfg.Proxy.TLS.KeyPath = keyPath
				}

				// Fallback: if LE failed and no cert exists, generate self-signed so proxy still serves HTTPS
				if nsCfg.Proxy.TLS.CertPath == "" {
					slog.Warn("Let's Encrypt cert not available, falling back to self-signed", "reason", acmeErr)
					generateSelfSignedCertForConfig(nsCfg)
				}
			}
		}

		// Extract appfiles to volumes base
		if extractErr := appfiles.ExtractTo(volumesBase); extractErr != nil {
			slog.Error("Failed to extract appfiles", "err", extractErr)
		} else {
			slog.Info("Extracted appfiles", "dir", volumesBase)
		}

		// Resolve system secrets (JWT, OIDC) — migrate from plain files or generate new.
		// Skip when locked (desktop mode with encrypted secrets — resolved after Web UI unlock).
		if !secretSvc.IsLocked() {
			var sysErr error
			systemSecrets, sysErr = resolveSystemSecrets(secretSvc, opts.Desktop)
			if sysErr != nil {
				return fmt.Errorf("resolve system secrets: %w", sysErr)
			}
		}

		// Load persisted state for detached apps and status recovery.
		// Detached apps must be known BEFORE Generate() so the generator can
		// exclude them from proxy upstreams and compute DependsOnDetachedApps.
		persistedState := namespace.LoadNsState(volumesBase, nsID)
		var genOpts namespace.GenerateOpts
		genOpts.SecretReader = &secretReaderAdapter{svc: secretSvc}
		if persistedState != nil {
			genOpts.DetachedApps = make(map[string]bool)
			for _, name := range persistedState.ManualStoppedApps {
				genOpts.DetachedApps[name] = true
			}
		} else if resolveResult.Workspace != nil && nsCfg.Template != "" {
			// First start: seed detached apps from workspace template
			for _, tmpl := range resolveResult.Workspace.NamespaceTemplates {
				if tmpl.ID == nsCfg.Template && len(tmpl.DetachedApps) > 0 {
					genOpts.DetachedApps = make(map[string]bool, len(tmpl.DetachedApps))
					for _, name := range tmpl.DetachedApps {
						genOpts.DetachedApps[name] = true
					}
					slog.Info("Seeded detached apps from template", "template", nsCfg.Template, "apps", tmpl.DetachedApps)
					break
				}
			}
		}

		// Generate namespace
		genResp, genErr := namespace.Generate(nsCfg, bundleDef, resolveResult.Workspace, systemSecrets, genOpts)
		if genErr != nil {
			return fmt.Errorf("generate namespace %q: %w", nsID, genErr)
		}

		// Write generated files (cloud config YAMLs, etc.) to volumes base
		for filePath, content := range genResp.Files {
			destPath := filepath.Join(volumesBase, filePath)
			if mkdirErr := os.MkdirAll(filepath.Dir(destPath), 0o755); mkdirErr != nil { //nolint:gosec // G301: volume dirs need 0o755 for Docker access
				slog.Error("Failed to create dir for generated file", "path", destPath, "err", mkdirErr)
				continue
			}
			perm := os.FileMode(0o644)
			if strings.HasSuffix(filePath, ".sh") {
				perm = 0o755
			}
			if writeErr := fsutil.AtomicWriteFile(destPath, content, perm); writeErr != nil {
				slog.Error("Failed to write generated file", "path", destPath, "err", writeErr)
			}
		}
		slog.Info("Generated namespace", "apps", len(genResp.Applications), "files", len(genResp.Files))

		appDefs = genResp.Applications
		runtime = namespace.NewRuntime(nsCfg, dockerClient, volumesBase)

		// Cache the successfully resolved bundle for fallback on future resolve failures
		if !bundleDef.IsEmpty() {
			runtime.SetCachedBundle(bundleDef)
		}

		// Wire registry auth and operation history into runtime
		runtime.SetRegistryAuthFunc(makeRegistryAuthFunc(wsCfg, secretSvc))
		runtime.SetHistory(namespace.NewOperationHistory(config.LogDir()))

		// Apply daemon.yml overrides for reconciler and pull concurrency
		if daemonCfg.Reconciler.IntervalSeconds > 0 || daemonCfg.Reconciler.LivenessPeriodMs > 0 || daemonCfg.Reconciler.LivenessEnabled != nil {
			rcfg := namespace.DefaultReconcilerConfig()
			if daemonCfg.Reconciler.IntervalSeconds > 0 {
				rcfg.IntervalSeconds = daemonCfg.Reconciler.IntervalSeconds
			}
			if daemonCfg.Reconciler.LivenessPeriodMs > 0 {
				rcfg.LivenessPeriod = time.Duration(daemonCfg.Reconciler.LivenessPeriodMs) * time.Millisecond
			}
			if daemonCfg.Reconciler.LivenessEnabled != nil {
				rcfg.LivenessEnabled = *daemonCfg.Reconciler.LivenessEnabled
			}
			runtime.SetReconcilerConfig(rcfg)
		}
		if daemonCfg.Docker.PullConcurrency > 0 {
			runtime.SetPullConcurrency(daemonCfg.Docker.PullConcurrency)
		}
		if daemonCfg.Docker.StopTimeout > 0 {
			runtime.SetDefaultStopTimeout(daemonCfg.Docker.StopTimeout)
		}

		// Restore persisted state: manual stopped apps, edited apps, locked apps.
		// genOpts.DetachedApps was already populated above (from persisted state or template).
		if persistedState != nil {
			if len(persistedState.ManualStoppedApps) > 0 {
				stopped := make(map[string]bool)
				for _, name := range persistedState.ManualStoppedApps {
					stopped[name] = true
				}
				runtime.SetManualStoppedApps(stopped)
			}
			runtime.RestoreEditedApps(persistedState.EditedApps, persistedState.EditedLockedApps)
			runtime.RestoreRestartState(persistedState.RestartEvents, persistedState.RestartCounts)
		} else if len(genOpts.DetachedApps) > 0 {
			// First start with template detached apps — apply to runtime
			runtime.SetManualStoppedApps(genOpts.DetachedApps)
		}
		// Wire DependsOnDetachedApps so RestartApp can trigger regen for dependency apps
		runtime.SetDependsOnDetachedApps(genResp.DependsOnDetachedApps)

		// Start CloudConfigServer only in desktop mode — server-mode webapps
		// have SPRING_CLOUD_CONFIG_ENABLED=false and don't connect to it.
		// Desktop mode uses the config server for local development workflows.
		if config.IsDesktopMode() {
			cloudCfgSrv = NewCloudConfigServer()
			cloudCfgSrv.UpdateConfig(genResp.CloudConfig, systemSecrets.JWT)
			if startErr := cloudCfgSrv.Start(); startErr != nil {
				slog.Warn("CloudConfigServer failed to start", "err", startErr)
			}
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
				if restartErr := d.runtime.RestartApp("proxy"); restartErr != nil {
					slog.Error("ACME: restart proxy after renewal failed", "err", restartErr)
				}
			}
		})
		d.acmeRenewal.Start()
	}

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
	tcpAddr := daemonCfg.Server.WebUI.Listen
	if daemonCfg.Server.WebUI.Enabled && !config.IsDesktopMode() {
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

	// Wait for initial reconciliation before notifying caller — avoids
	// the webview seeing all apps as STOPPED for a few seconds.
	if d.runtime != nil && opts.ReadyCh != nil {
		waitCtx, waitCancel := context.WithTimeout(context.Background(), 15*time.Second)
		d.runtime.WaitForInitialReconcile(waitCtx)
		waitCancel()
	}

	// Notify caller that the daemon is ready
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
	resolver := bundle.NewResolverWithAuth(bundlesDataDir, makeTokenLookup(d.secretReaderFunc()))
	resolveResult, err := resolver.Resolve(nsCfg.BundleRef)
	if err != nil {
		// Fallback to cached bundle from persisted state
		cachedState := namespace.LoadNsState(d.volumesBase, nsID)
		if cachedState != nil && cachedState.CachedBundle != nil && !cachedState.CachedBundle.IsEmpty() {
			slog.Warn("Bundle resolution failed on reload, using cached bundle", "ref", nsCfg.BundleRef, "err", err,
				"cachedVersion", cachedState.CachedBundle.Key.Version)
			resolveResult = &bundle.ResolveResult{Bundle: cachedState.CachedBundle, Workspace: d.workspaceConfig}
		} else {
			return fmt.Errorf("resolve bundle: %w", err)
		}
	}

	_ = appfiles.ExtractTo(d.volumesBase)

	// Self-signed cert: generate if TLS enabled + no cert paths + no LE
	ensureSelfSignedCert(nsCfg)

	// Let's Encrypt: obtain certificate if needed; prepare renewal service for Phase 2
	var newRenewal *acme.RenewalService
	if nsCfg.Proxy.TLS.Enabled && nsCfg.Proxy.TLS.LetsEncrypt && nsCfg.Proxy.Host != "" && nsCfg.Proxy.Host != "localhost" {
		acmeClient := acme.NewClient(config.DataDir(), config.ConfDir(), nsCfg.Proxy.Host)
		var acmeErr error
		if !acmeClient.CertMatchesHost() {
			slog.Info("Obtaining Let's Encrypt certificate on reload", "host", nsCfg.Proxy.Host)
			acmeCtx, acmeCancel := context.WithTimeout(context.Background(), 120*time.Second)
			acmeErr = acmeClient.ObtainCertificate(acmeCtx)
			acmeCancel()
			if acmeErr != nil {
				slog.Error("Let's Encrypt failed on reload", "err", acmeErr)
			}
		}
		if acmeClient.CertMatchesHost() {
			nsCfg.Proxy.TLS.CertPath = acmeClient.CertPath()
			nsCfg.Proxy.TLS.KeyPath = acmeClient.KeyPath()
		}

		// Fallback: if LE cert not available, use self-signed so proxy still serves HTTPS
		if nsCfg.Proxy.TLS.CertPath == "" {
			slog.Warn("Let's Encrypt cert not available on reload, falling back to self-signed", "reason", acmeErr)
			generateSelfSignedCertForConfig(nsCfg)
		}

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
	}
	genResp, genErr := namespace.Generate(nsCfg, resolveResult.Bundle, resolveResult.Workspace, d.systemSecrets, genOpts)
	if genErr != nil {
		return fmt.Errorf("generate namespace: %w", genErr)
	}

	// Write generated files atomically (prevent partial writes on crash)
	for filePath, content := range genResp.Files {
		destPath := filepath.Join(d.volumesBase, filePath)
		if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil { //nolint:gosec // generated file dirs need 0o755 for container access
			slog.Error("Failed to create dir for generated file", "path", destPath, "err", err)
			continue
		}
		perm := os.FileMode(0o644)
		if strings.HasSuffix(filePath, ".sh") {
			perm = 0o755
		}
		if err := fsutil.AtomicWriteFile(destPath, content, perm); err != nil {
			slog.Error("Failed to write generated file", "path", destPath, "err", err)
		}
	}

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

	// Phase 3: regenerate runtime with updated config (async stop + start)
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
	evt.Seq = d.eventSeq.Add(1)
	d.eventMu.Lock()
	defer d.eventMu.Unlock()
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
	mux.HandleFunc("POST "+api.NamespaceAdminPassword, d.handleSetAdminPassword)
	mux.HandleFunc("GET "+api.RestartEvents, d.handleRestartEvents)
	mux.HandleFunc("GET /api/v1/diagnostics-file", d.handleDiagnosticsFile)

	// Config
	mux.HandleFunc("GET /api/v1/config", d.handleGetConfig)
	mux.HandleFunc("GET /api/v1/config/applied", d.handleGetAppliedConfig)
	mux.HandleFunc("PUT /api/v1/config", d.handlePutConfig)

	// App routes
	mux.HandleFunc("GET /api/v1/apps/{name}/logs", d.handleAppLogs)
	mux.HandleFunc("GET /api/v1/apps/{name}/inspect", d.handleAppInspect)
	mux.HandleFunc("POST /api/v1/apps/{name}/restart", d.handleAppRestart)
	mux.HandleFunc("POST /api/v1/apps/{name}/stop", d.handleAppStop)
	mux.HandleFunc("POST /api/v1/apps/{name}/start", d.handleAppStart)
	mux.HandleFunc("POST /api/v1/apps/{name}/exec", d.handleAppExec)
	mux.HandleFunc("GET /api/v1/apps/{name}/config", d.handleGetAppConfig)
	mux.HandleFunc("PUT /api/v1/apps/{name}/config", d.handlePutAppConfig)
	mux.HandleFunc("PUT /api/v1/apps/{name}/lock", d.handleAppLockToggle)
	mux.HandleFunc("GET /api/v1/apps/{name}/files", d.handleListAppFiles)
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

	// Namespaces
	mux.HandleFunc("GET "+api.Namespaces, d.handleListNamespaces)
	mux.HandleFunc("POST "+api.Namespaces, d.handleCreateNamespace)
	mux.HandleFunc("DELETE /api/v1/namespaces/{id}", d.handleDeleteNamespace)
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

func readJSON(r *http.Request, v any) error {
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(v); err != nil { // 1MB max
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
func buildRegistryAuthCache(reposByHost map[string]bundle.ImageRepo, reader secretReader) map[string]*docker.RegistryAuth {
	result := make(map[string]*docker.RegistryAuth)
	secrets, err := reader.ListSecrets()
	if err != nil {
		return result
	}
	// Build scope→value map from all secrets (single ListSecrets + GetSecret per secret)
	scopeValues := make(map[string]string)
	for _, s := range secrets {
		if s.Scope != "" {
			sec, err := reader.GetSecret(s.ID)
			if err != nil {
				continue // ErrSecretsLocked → skip gracefully
			}
			scopeValues[s.Scope] = sec.Value
		}
	}
	for host, repo := range reposByHost {
		if repo.AuthType == "" {
			continue
		}
		value := scopeValues[repo.AuthType]
		if value == "" {
			value = scopeValues[host]
		}
		if value == "" {
			// Kotlin migration compat: scope = "images-repo:{host}"
			value = scopeValues["images-repo:"+host]
		}
		if value == "" {
			continue
		}
		parts := strings.SplitN(value, ":", 2)
		if len(parts) != 2 {
			continue
		}
		result[host] = &docker.RegistryAuth{
			Username: parts[0],
			Password: parts[1],
			Registry: "https://" + host,
		}
	}
	return result
}

// resolveSystemSecrets reads or generates JWT and OIDC secrets.
// Priority: Store → plain files (with migration) → generate new.
// In desktop mode the admin password is always "admin" (the desktop
// launcher is a local dev tool, not a production deployment).
func resolveSystemSecrets(svc *storage.SecretService, desktop bool) (namespace.SystemSecrets, error) {
	var secrets namespace.SystemSecrets

	// JWT
	jwt, err := resolveOneSystemSecret(svc, "_jwt", func() string {
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
	oidc, err := resolveOneSystemSecret(svc, "_oidc", func() string {
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

	// ecos-app realm admin password. In server mode a random password is
	// generated on first start, persisted, and re-used on every reload.
	// In desktop mode we always use "admin" — it's a local dev tool.
	if desktop {
		secrets.AdminPassword = "admin"
	} else {
		adminPass, adminErr := resolveOneSystemSecret(svc, "_admin_password", func() string {
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

	// "citeck" service account password — shared between Keycloak master
	// realm (admin role) and RabbitMQ (monitoring tag). Always generated;
	// same for desktop and server modes. Prefer the new _citeck_sa key but
	// seamlessly migrate from the legacy _launcher_sa key written by older
	// launcher versions so existing installs keep their stable SA password.
	citeckSA, saErr := resolveOneSystemSecret(svc, "_citeck_sa", func() string {
		if legacy, err := svc.GetSecret("_launcher_sa"); err == nil && legacy.Value != "" {
			slog.Info("Migrating legacy _launcher_sa secret to _citeck_sa")
			return legacy.Value
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

	// Cleanup: once _citeck_sa exists in the Store, the legacy _launcher_sa
	// entry is obsolete. Delete it to avoid a stale encrypted file lingering
	// in conf/secrets/. Mirrors the `os.Remove(plainFile)` cleanup in
	// resolveOneSystemSecret for pre-encryption plain-file migrations. Errors
	// here are non-fatal — migration has already succeeded.
	if legacy, err := svc.GetSecret("_launcher_sa"); err == nil && legacy.Value != "" {
		if delErr := svc.DeleteSecret("_launcher_sa"); delErr != nil {
			slog.Warn("Failed to delete legacy _launcher_sa secret after migration", "err", delErr)
		} else {
			slog.Info("Deleted legacy _launcher_sa secret after migration to _citeck_sa")
		}
	}

	return secrets, nil
}

// resolveOneSystemSecret reads a system secret from Store, migrates from plain file, or generates new.
func resolveOneSystemSecret(svc *storage.SecretService, id string, generate func() string) (string, error) {
	// 1. Try Store
	sec, err := svc.GetSecret(id)
	if err == nil && sec.Value != "" {
		return sec.Value, nil
	}

	// 2. Fallback: read plain file (migration from pre-encryption launcher)
	plainFile := filepath.Join(config.ConfDir(), strings.TrimPrefix(id, "_")+"-secret")
	if data, readErr := os.ReadFile(plainFile); readErr == nil && len(data) > 0 { //nolint:gosec // path from trusted confDir
		value := string(data)
		if id == "_jwt" {
			value = migrateJWTSecretToStdBase64(value)
		}
		slog.Info("Migrating system secret from plain file to Store", "id", id)
		if saveErr := svc.SaveSecret(storage.Secret{
			SecretMeta: storage.SecretMeta{ID: id, Name: id, Type: storage.SecretSystem},
			Value:      value,
		}); saveErr != nil {
			return "", fmt.Errorf("save migrated secret %s: %w", id, saveErr)
		}
		_ = os.Remove(plainFile)
		return value, nil
	}

	// 3. Generate new
	value := generate()
	if value == "" {
		return "", fmt.Errorf("failed to generate secret %s", id)
	}
	slog.Info("Generated new system secret", "id", id)
	if saveErr := svc.SaveSecret(storage.Secret{
		SecretMeta: storage.SecretMeta{ID: id, Name: id, Type: storage.SecretSystem},
		Value:      value,
	}); saveErr != nil {
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
//nolint:nestif // snapshot import requires nested SHA256 verification and download fallback logic
func importSnapshotIfNeeded(nsCfg *namespace.Config, wsCfg *bundle.WorkspaceConfig, dc *docker.Client, volumesBase string) {
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
	importCtx, importCancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer importCancel()

	if needsDownload {
		if dlErr := snapshot.Download(importCtx, snapDef.URL, destPath, snapDef.SHA256, nil); dlErr != nil {
			slog.Error("Snapshot download failed", "url", snapDef.URL, "err", dlErr)
			return
		}
	}

	// Import
	if _, err := snapshot.Import(importCtx, dc, destPath, volumesBase); err != nil {
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
