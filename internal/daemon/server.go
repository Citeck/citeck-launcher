package daemon

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/citeck/citeck-launcher/internal/acme"
	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/docker"
	"github.com/citeck/citeck-launcher/internal/fsutil"
	"github.com/citeck/citeck-launcher/internal/license"
	"github.com/citeck/citeck-launcher/internal/namespace"
	"github.com/citeck/citeck-launcher/internal/storage"
	"github.com/citeck/citeck-launcher/internal/tlsutil"
	"github.com/citeck/citeck-launcher/internal/update"
)

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

// activeNamespace groups EVERY per-namespace (and per-workspace) field that is
// swapped when the active namespace or workspace changes. Keeping them in ONE
// struct behind one pointer makes field-by-field tearing structurally
// impossible: historically these lived as ~12 loose fields on Daemon, which
// produced teardown drift (bundleError missed in one of three teardown sites)
// and a systemSecrets read race.
//
// Concurrency model: mutex-guarded MUTABLE struct with value-copy snapshots
// (not an immutable atomic.Pointer swap).
//   - Daemon.activeNs is guarded by configMu. It is nil only in zero-value
//     test Daemons; production allocates it at construction and it is never
//     set back to nil (teardown swaps in a fresh struct).
//   - Readers call d.active(), which copies the whole struct under RLock —
//     one consistent view of all 12 fields per call.
//   - Writers hold configMu for WRITING and either mutate fields in place
//     (doReloadEx Phase-2 commit, admin-password rotation, ACME re-arm) or
//     swap the pointer (installLoadedNamespace, clearActiveNamespaceLocked,
//     SwitchWorkspace).
//
// In-place mutation (rather than immutable rebuild-and-swap) is deliberate:
// handleSetAdminPassword rotates systemSecrets.AdminPassword under configMu
// WITHOUT holding reloadMu, so a rotation can interleave with a doReloadEx
// that is between its Phase-1 snapshot and Phase-2 commit. The reload commit
// does not write systemSecrets, so with in-place mutation the rotation always
// lands on the live struct — an immutable swap rebuilt from the reload's
// Phase-1 snapshot would silently revert the rotation.
//
// Field membership = everything clearActiveNamespaceLocked resets plus the
// workspace-scoped trio (workspaceID, dockerClient, workspaceConfig) that
// only SwitchWorkspace replaces. Invariant (see loadNamespace /
// installLoadedNamespace): dockerClient is ALWAYS scoped to the active
// (workspaceID, nsConfig.ID) pair — derived/validated at the loadNamespace
// choke-point, asserted at the install swap.
type activeNamespace struct {
	workspaceID     string
	nsConfig        *namespace.Config
	bundleDef       *bundle.Def
	workspaceConfig *bundle.WorkspaceConfig
	appDefs         []appdef.ApplicationDef
	systemSecrets   namespace.SystemSecrets // resolved JWT/OIDC/admin secrets
	volumesBase     string
	bundleError     string // non-empty if bundle resolution failed
	// wsSyncError is non-empty when the ACTIVE workspace points at a CUSTOM
	// repo URL whose git sync failed AND no cached workspace config was usable
	// (bundle.Resolver.WorkspaceSyncError contract). Welcome-data endpoints
	// (quick starts, workspace snapshots) surface it as 502 WS_REPO_SYNC_FAILED
	// instead of silently serving the built-in fallback workspace. Workspace-
	// scoped like workspaceConfig: carried over by clearActiveNamespaceLocked,
	// replaced by SwitchWorkspace / namespace load / reload.
	wsSyncError    string
	cloudCfgServer *CloudConfigServer
	acmeRenewal    *acme.RenewalService
	runtime        *namespace.Runtime
	dockerClient   *docker.Client
}

// Daemon is the main daemon server.
type Daemon struct {
	server        *http.Server
	tcpServer     *http.Server
	store         storage.Store
	secretService *storage.SecretService // always non-nil; wraps store with transparent encryption
	socketPath    string
	startTime     time.Time
	eventSubs     []chan api.EventDto
	eventMu       sync.Mutex
	eventRing     *eventRing // bounded replay buffer for SSE reconnects (Last-Event-ID)
	// configMu guards activeNs (the pointer AND the pointee's fields). Read
	// per-namespace state via ONE d.active() snapshot per handler — never
	// lock-free, never field-by-field across separate lock acquisitions.
	// Mutations happen under the write lock: pointer swaps via
	// installLoadedNamespace / clearActiveNamespaceLocked / SwitchWorkspace,
	// in-place field writes via d.activeLocked(). See the activeNamespace doc
	// for the full contract, including why in-place mutation is kept.
	configMu sync.RWMutex
	// activeNs holds ALL swap-on-activate per-namespace state. Guarded by
	// configMu; nil only in zero-value test Daemons (d.active() tolerates it).
	activeNs     *activeNamespace
	version      string
	shutdownOnce sync.Once
	bgCtx        context.Context // canceled on daemon shutdown
	bgCancel     context.CancelFunc
	bgWg         sync.WaitGroup // tracks background goroutines (snapshot, downloads)
	snapshotMu   sync.Mutex     // guards concurrent snapshot import/export
	daemonCfg    config.DaemonConfig
	// eventSeq is the monotonic SSE event counter. All mutations (.Add) and
	// the cutoff Load happen under eventMu — the atomic type is retained
	// purely for Load() ergonomics from addSubscriber's lock holder and the
	// rare read from diagnostics; treat the field as logically protected by
	// eventMu, not as concurrent-safe by itself.
	eventSeq   atomic.Int64
	sseDropped atomic.Int64 // SSE events dropped due to slow consumers
	logWriter  *fsutil.RotatingWriter
	logLevel   *slog.LevelVar
	desktop    bool             // desktop mode: log writer shared across restarts
	reloadMu   sync.Mutex       // guards concurrent reload requests
	licenses   *license.Service // user-added enterprise licenses
	updateSvc  *update.Service  // desktop auto-update service (nil in server mode)
	// execClient is a test seam for handlers that drive CLI tools inside
	// containers (kcadm.sh / rabbitmqctl / setup.py). nil in production —
	// dockerExec() falls back to dockerClient.
	execClient containerExecer
	// rotationApps is a test seam for the admin-password handler's container
	// lookup. nil in production — adminRotationApps() falls back to runtime.
	rotationApps appFinder
	// planInputsFn is a test seam for handleReloadPlan's resolve+generate
	// phase (git pull + bundle resolution + generator — unreachable from unit
	// tests). nil in production — reloadPlanInputs() falls back to
	// resolveReloadPlanInputs.
	planInputsFn func(act activeNamespace) (*reloadPlanInputs, error)
	// wsCfgResolveFn is a test seam for the workspace-repo config resolve (real
	// git clone/pull — unreachable from unit tests). Shared by both
	// resolveWorkspaceConfigForSwitch (SwitchWorkspace) and
	// resolveActiveWorkspaceConfig (read-path self-heal / Force Update). nil in
	// production — both fall back to the bundle resolver. Same pattern as
	// planInputsFn.
	wsCfgResolveFn func(ws storage.WorkspaceDto) (*bundle.WorkspaceConfig, error)
	// apiAuth enforces the opt-in bearer-token/session auth on the
	// server-mode TCP transport (daemon.yml api_auth). nil when disabled
	// (default) — TCP then behaves exactly as before (CSRF gate only).
	// See apiauth.go for the contract and bypass matrix.
	apiAuth *apiAuth
	// imagePulls tracks explicit image pulls triggered from the drawer's image
	// popup, keyed by image ref → *imagePullState. Decoupled from the runtime
	// state machine: the UI polls the inspect endpoint for pulling/error state.
	imagePulls sync.Map
}

// active returns ONE consistent value-copy snapshot of the active-namespace
// state under configMu. All fields in the returned struct come from the same
// lock acquisition, so a handler that reads 2+ per-namespace fields gets a
// torn-free view (e.g. dockerClient is guaranteed to match nsConfig). The
// copy is a stale-but-consistent snapshot: a concurrent swap/mutation is not
// reflected in it, exactly like the per-field copies handlers took before.
// Zero-value Daemons (tests) with a nil activeNs get an all-zero snapshot.
func (d *Daemon) active() activeNamespace {
	d.configMu.RLock()
	defer d.configMu.RUnlock()
	if d.activeNs == nil {
		return activeNamespace{}
	}
	return *d.activeNs
}

// activeLocked returns the MUTABLE active-namespace struct for in-place field
// writes, allocating the empty value on first use (zero-value test Daemons).
// Caller MUST hold configMu for writing.
func (d *Daemon) activeLocked() *activeNamespace {
	if d.activeNs == nil {
		d.activeNs = &activeNamespace{}
	}
	return d.activeNs
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
	act := d.active()
	if act.acmeRenewal != nil {
		return
	}
	nsCfg := act.nsConfig
	if nsCfg == nil || !nsCfg.Proxy.TLS.Enabled || !nsCfg.Proxy.TLS.LetsEncrypt || nsCfg.Proxy.Host == "" {
		return
	}
	acmeClient := acme.NewClient(config.DataDir(), config.ConfDir(), nsCfg.Proxy.Host)
	svc := acme.NewRenewalService(acmeClient, func() {
		if rt := d.active().runtime; rt != nil {
			if restartErr := rt.RestartApp("proxy"); restartErr != nil {
				slog.Error("ACME: restart proxy after renewal failed", "err", restartErr)
			}
		}
	})
	d.configMu.Lock()
	d.activeLocked().acmeRenewal = svc
	d.configMu.Unlock()
	svc.Start()
}

// rebuildAuthCaches rebuilds token lookup and registry auth caches from current secrets,
// then retries any pull-failed apps. It also invalidates the license cache —
// a locked SecretService caches a "no licenses" List() result, and this method
// runs exactly when secret visibility changes (unlock, secret CRUD,
// master-password setup/reset), so the next List() re-reads the real store.
func (d *Daemon) rebuildAuthCaches() {
	if d.licenses != nil {
		d.licenses.Refresh()
	}
	// Secret visibility just changed — back-fill legacy workspace→secret links
	// that a locked store deferred at bootstrap (no-op once linked).
	migrateWorkspaceSecretLinks(d.store, d.secretService)
	act := d.active()
	if act.runtime == nil {
		return
	}
	regBindings, _ := d.store.ListRegistryBindings(act.workspaceID)
	act.runtime.SetRegistryAuthFunc(makeRegistryAuthFunc(act.workspaceConfig, d.secretReaderFunc(), regBindings))
	retried := act.runtime.RetryPullFailedApps()
	if retried > 0 {
		slog.Info("Retrying pull-failed apps after secrets change", "count", retried)
	}
}

// lowDiskWarnGB / diskCriticalGB are the free-space thresholds (GB) shared by
// runDiskMonitor and the on-demand Diagnostics disk check, so the background log
// and the UI agree. Warn below lowDiskWarnGB; the Diagnostics check escalates to
// "error" below diskCriticalGB.
const (
	lowDiskWarnGB  = 5.0
	diskCriticalGB = 1.0
)

// diskMonitorInterval is how often runDiskMonitor samples free space.
const diskMonitorInterval = 10 * time.Minute

// runDiskMonitor periodically samples free space on the launcher-home
// filesystem and WARNs while it is below lowDiskWarnGB. On a single-user
// desktop the home dir and Docker's (rootless) data root share this filesystem,
// so it is a good proxy for the disk Docker writes to. A full data root is what
// silently broke a running namespace (Docker can't write → containers stall →
// liveness storm), so surfacing it early — in the daemon log, the dump, and a
// `disk_low` SSE event driving the web banner — gives an actionable signal
// before the cascade. Best-effort; exits on ctx.
func (d *Daemon) runDiskMonitor(ctx context.Context) {
	ticker := time.NewTicker(diskMonitorInterval)
	defer ticker.Stop()
	wasLow := false
	check := func() {
		path := config.HomeDir()
		freeGB, totalGB, err := diskSpace(path)
		if err != nil {
			return
		}
		wasLow = d.processDiskSample(path, freeGB, totalGB, wasLow)
	}
	check() // sample immediately so a boot-time low-disk condition is logged at once
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			check()
		}
	}
}

// processDiskSample applies one disk-space sample: it logs the low/recovered
// condition (WARN repeats every tick while low — daemon-log behavior is
// unchanged) and broadcasts a `disk_low` / `disk_ok` SSE event on state
// CHANGE only, so the web banner appears once per crossing and clears on
// recovery instead of re-firing every 10 minutes. Returns the new low-state
// for the caller to carry across ticks.
func (d *Daemon) processDiskSample(path string, freeGB, totalGB float64, wasLow bool) (isLow bool) {
	isLow = freeGB < lowDiskWarnGB
	switch {
	case isLow:
		slog.Warn("Low disk space on the launcher-home filesystem — containers may fail to start or run",
			"freeGB", fmt.Sprintf("%.1f", freeGB), "totalGB", fmt.Sprintf("%.1f", totalGB))
		if !wasLow {
			d.broadcastEvent(diskEvent("disk_low", path, freeGB))
		}
	case wasLow:
		slog.Info("Disk space recovered above the low-disk threshold",
			"freeGB", fmt.Sprintf("%.1f", freeGB))
		d.broadcastEvent(diskEvent("disk_ok", path, freeGB))
	}
	return isLow
}

// diskEvent builds the SSE payload for disk_low / disk_ok. Free space is
// converted from the monitor's GB float back to bytes — sub-byte precision is
// irrelevant for the banner ("3.2 GB free").
func diskEvent(evtType, path string, freeGB float64) api.EventDto {
	return api.EventDto{
		Type:           evtType,
		Timestamp:      time.Now().UnixMilli(),
		Path:           path,
		FreeBytes:      int64(freeGB * (1 << 30)),
		ThresholdBytes: int64(lowDiskWarnGB * (1 << 30)),
	}
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
	// One snapshot up front: shutdown tears down the namespace that is active
	// NOW; a torn per-field view here is exactly what activeNamespace prevents.
	act := d.active()
	if act.cloudCfgServer != nil {
		act.cloudCfgServer.Stop()
	}
	if act.acmeRenewal != nil {
		act.acmeRenewal.Stop()
	}

	// Phase 3: Shutdown runtime. When leaveRunning is set, the runtime exits
	// without stopping containers — the next daemon will adopt them via
	// doStart's hash-matching path. Used for binary upgrades.
	if act.runtime != nil {
		if leaveRunning {
			act.runtime.ShutdownDetached()
		} else {
			act.runtime.Shutdown()
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
	if act.dockerClient != nil {
		_ = act.dockerClient.Close()
	}
	_ = os.Remove(d.socketPath)

	slog.Info("Daemon stopped")
	// In desktop mode, the log writer is shared across daemon restarts — don't close it.
	// In CLI mode (single Start), close the writer on exit.
	if d.logWriter != nil && !d.desktop {
		_ = d.logWriter.Close()
	}
}

// doReload re-resolves the namespace config + bundle (respecting the per-repo
// git pull throttle) and reconciles the already-running runtime, recreating
// only changed apps. Thin wrapper over doReloadEx. Caller must hold reloadMu.
func (d *Daemon) doReload() error { return d.doReloadEx(false, false) }

// doReloadEx is the shared reload / force-update core. Caller must hold reloadMu.
//
//   - forceGitPull bypasses the per-repo PullPeriod throttle so a "Force Update"
//     pulls the workspace / bundle repos unconditionally and picks up new bundle
//     versions immediately. Kotlin 1.x parity: forceUpdate only flips the git
//     policy to REQUIRED — image pulling stays normal (a present release tag is
//     reused; only :snapshot tags refresh), so force never re-pulls release tags.
//   - startNotRegenerate applies the freshly-resolved app set by STARTING the
//     runtime — used by "Force Update And Start" on a STOPPED namespace, where
//     there is nothing running to regenerate. When false the set is handed to
//     Regenerate (recreate changed) on the running runtime.
func (d *Daemon) doReloadEx(forceGitPull, startNotRegenerate bool) error {
	// One consistent snapshot: nsConfig/runtime/workspaceConfig/systemSecrets/
	// workspaceID/volumesBase all come from the same lock acquisition.
	// (systemSecrets in particular: handleSetAdminPassword mutates it under
	// configMu but not reloadMu, so lock-free reads raced with a rotation.)
	// reloadMu (held by our caller) excludes every pointer-swap path
	// (install/clear/switch), so the snapshot's identity fields stay current
	// for the whole reload.
	act := d.active()
	if act.nsConfig == nil || act.runtime == nil {
		return fmt.Errorf("no namespace configured")
	}
	nsID := act.nsConfig.ID
	fallbackWS := act.workspaceConfig
	sysSecrets := act.systemSecrets

	// Phase 1: slow I/O outside lock (config read, git pull, bundle resolution)
	nsCfg, err := d.loadNamespaceConfigFromStore(act.workspaceID, nsID)
	if err != nil {
		return fmt.Errorf("reload config: %w", err)
	}

	resolver := bundle.NewResolverWithAuth(config.BundlesDataDir(act.workspaceID), makeTokenLookup(d.secretReaderFunc())).
		WithWorkspaceRepo(d.resolveActiveWorkspaceRepoOpts()).
		WithWorkspaceOverlay(workspaceConfigOverlay(d.store, act.workspaceID))
	if forceGitPull {
		resolver = resolver.WithForcePull()
	}
	resolveResult, bundleFallback, resolveErr := resolveBundleWithCacheFallback(
		resolver, nsCfg.BundleRef, d.store, act.workspaceID, nsID, fallbackWS, "on reload", true)
	if resolveErr != nil {
		return fmt.Errorf("resolve bundle: %w", resolveErr)
	}
	// Refresh the workspace-repo sync error from this reload's resolver pass so
	// the Welcome 502 gate (quick starts / snapshots) tracks reality: a custom
	// repo that recovered clears it; one that broke (and has no cached config)
	// sets it. Recording only — the cached-bundle fallback above is unchanged.
	wsSyncError := ""
	if syncErr := resolver.WorkspaceSyncError(); syncErr != nil {
		wsSyncError = syncErr.Error()
	}

	// Appfiles are intentionally NOT extracted here — same rule as Start().
	// writeRuntimeFiles (inside generateAndWriteRuntimeFiles below) is the
	// single source of truth for bind-mount contents, avoiding a double-write
	// that would revert a generator-customized file (proxy lua with rendered
	// secrets, realm JSON, keycloak init script) back to its embedded
	// template default.

	// Certs (self-signed / Let's Encrypt); prepare renewal service for Phase 2.
	var newRenewal *acme.RenewalService
	if acmeClient := ensureProxyTLSCerts(nsCfg, "on reload"); acmeClient != nil {
		newRenewal = acme.NewRenewalService(acmeClient, func() {
			if rt := d.active().runtime; rt != nil {
				if err := rt.RestartApp("proxy"); err != nil {
					slog.Error("ACME: restart proxy after renewal failed", "err", err)
				}
			}
		})
	}

	var genOpts namespace.GenerateOpts
	genOpts.SecretReader = d.nsSecretReader()
	genOpts.DetachedApps = act.runtime.ManualStoppedApps()
	// File edits are merged onto their templates inside Generate (both disk and
	// VolumesContentHash reflect the merged result) — no separate skip set.
	fileEdits := act.runtime.FileEditsSnapshot()
	genOpts.EditedFileEdits = fileEdits
	genOpts.DiskContent = readDiskContent(act.volumesBase, fileEdits)
	// User-added licenses (encrypted store) merge with workspace-declared ones
	// in the eapps cloud-config. Locked SecretService yields nil and we fall
	// back to workspace-only licenses — reload never aborts on a locked store.
	genOpts.ExtraLicenses = collectExtraLicensesFrom(d.licenses)
	genResp, genErr := generateAndWriteRuntimeFiles(nsCfg, resolveResult, sysSecrets, genOpts, act.volumesBase, nil)
	if genErr != nil {
		return genErr
	}
	act.runtime.SetLastGenFiles(genResp.BaselineFiles)
	act.runtime.SetGeneratedDefs(genResp.Applications)

	// Phase 2: update shared state briefly under write lock. In-place
	// mutation of the live activeNamespace (not a rebuild-and-swap) so a
	// concurrent admin-password rotation that landed on systemSecrets during
	// Phase 1 is preserved — see the activeNamespace doc.
	d.configMu.Lock()
	a := d.activeLocked()
	a.nsConfig = nsCfg
	a.bundleDef = resolveResult.Bundle
	a.workspaceConfig = resolveResult.Workspace
	a.wsSyncError = wsSyncError
	a.appDefs = genResp.Applications
	// Reload succeeded with a freshly-resolved bundle — clear any boot-time
	// bundle resolution error so the UI banner doesn't survive a successful
	// reload until the next namespace activation.
	a.bundleError = ""
	// Update ACME renewal service under lock to prevent data race with shutdown
	if a.acmeRenewal != nil {
		a.acmeRenewal.Stop()
	}
	a.acmeRenewal = newRenewal
	d.configMu.Unlock()
	if newRenewal != nil {
		newRenewal.Start()
	}

	if act.cloudCfgServer != nil {
		act.cloudCfgServer.UpdateConfig(genResp.CloudConfig, sysSecrets.JWT)
	}
	regBindings, _ := d.store.ListRegistryBindings(act.workspaceID)
	act.runtime.SetRegistryAuthFunc(makeRegistryAuthFunc(resolveResult.Workspace, d.secretReaderFunc(), regBindings))
	act.runtime.SetDependsOnDetachedApps(genResp.DependsOnDetachedApps)

	// Phase 3: regenerate runtime with updated config (async stop + start).
	// When the bundle had to fall back to the cached on-disk copy (e.g. git
	// pull failed), the generated Applications set can come back smaller than
	// the live runtime's r.apps — handing that to Regenerate would mark every
	// missing app for removal and tear down running containers we don't have
	// authoritative info to remove. Skip the regenerate in that case so the
	// runtime keeps its current apps; the user fixes the bundle source and
	// re-runs reload to get the real desired set applied.
	// Force Update And Start on a STOPPED namespace: nothing is running, so the
	// bundle-fallback "preserve current runtime" guard does not apply — just
	// start the freshly-resolved set (Kotlin 1.x: forceUpdate ⇒ generate + start
	// all apps). Images pull per the normal stage rules.
	if startNotRegenerate {
		act.runtime.Start(genResp.Applications)
		return nil
	}

	currentAppCount := act.runtime.AppCount()
	if bundleFallback && len(genResp.Applications) < currentAppCount {
		slog.Warn("Bundle fallback produced a smaller app set; preserving current runtime",
			"current", currentAppCount, "fallback", len(genResp.Applications))
		return nil
	}
	act.runtime.Regenerate(genResp.Applications, nsCfg, resolveResult.Bundle)
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

// registerRoutes registers all API routes on the shared mux.
// Both Unix socket and TCP listeners use the same mux — localhost TCP is trusted
// (desktop thin client), non-localhost requires mTLS (which is also fully authenticated).
func (d *Daemon) registerRoutes(mux *http.ServeMux) {
	// Daemon
	mux.HandleFunc("GET "+api.DaemonStatus, d.handleDaemonStatus)
	mux.HandleFunc("PUT "+api.UIPrefs, d.handlePutUIPrefs)
	mux.HandleFunc("POST "+api.DaemonShutdown, d.handleDaemonShutdown)
	mux.HandleFunc("PUT /api/v1/daemon/loglevel", d.handleSetLogLevel)
	mux.HandleFunc("GET "+api.DaemonLogs, d.handleDaemonLogs)

	// Namespace
	mux.HandleFunc("GET "+api.Namespace, d.handleGetNamespace)
	mux.HandleFunc("POST "+api.NamespaceStart, d.handleStartNamespace)
	mux.HandleFunc("POST "+api.NamespaceStop, d.handleStopNamespace)
	mux.HandleFunc("POST "+api.NamespaceReload, d.handleReloadNamespace)
	mux.HandleFunc("GET "+api.NamespaceReloadPlan, d.handleReloadPlan)
	mux.HandleFunc("POST "+api.NamespaceUpgrade, d.handleUpgradeNamespace)
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
	mux.HandleFunc("GET /api/v1/apps/{name}/image", d.handleAppImageInspect)
	mux.HandleFunc("POST /api/v1/apps/{name}/image/pull", d.handleAppImagePull)
	mux.HandleFunc("POST /api/v1/apps/{name}/restart", d.handleAppRestart)
	mux.HandleFunc("POST /api/v1/apps/{name}/stop", d.handleAppStop)
	mux.HandleFunc("POST /api/v1/apps/{name}/start", d.handleAppStart)
	mux.HandleFunc("POST /api/v1/apps/{name}/exec", d.handleAppExec)
	mux.HandleFunc("GET /api/v1/apps/{name}/config", d.handleGetAppConfig)
	mux.HandleFunc("PUT /api/v1/apps/{name}/config", d.handlePutAppConfig)
	mux.HandleFunc("POST /api/v1/apps/{name}/config/reset", d.handleResetAppConfig)
	mux.HandleFunc("DELETE /api/v1/apps/{name}/restart-events", d.handleClearAppRestartEvents)
	mux.HandleFunc("GET /api/v1/apps/{name}/files", d.handleListAppFiles)
	mux.HandleFunc("POST /api/v1/apps/{name}/files/reset", d.handleResetAppFile)
	mux.HandleFunc("GET /api/v1/apps/{name}/files/{path...}", d.handleGetAppFile)
	mux.HandleFunc("PUT /api/v1/apps/{name}/files/{path...}", d.handlePutAppFile)

	// Events (SSE)
	mux.HandleFunc("GET "+api.Events, d.handleEvents)

	// Volumes
	mux.HandleFunc("GET /api/v1/volumes", d.handleListVolumes)
	mux.HandleFunc("GET /api/v1/volumes/{name}/size", d.handleVolumeSize)
	mux.HandleFunc("DELETE /api/v1/volumes/{name}", d.handleDeleteVolume)

	// Health + Metrics
	mux.HandleFunc("GET "+api.Health, d.handleHealth)
	mux.HandleFunc("GET /api/v1/metrics", d.handleMetrics)

	// System dump
	mux.HandleFunc("GET /api/v1/system/dump", d.handleSystemDump)
	mux.HandleFunc("POST "+api.SystemOpenDir, d.handleSystemOpenDir)

	// Desktop tray menu
	mux.HandleFunc("GET "+api.DesktopTrayMenu, d.handleTrayMenu)

	// Workspace operations
	mux.HandleFunc("POST "+api.WorkspaceUpdate, d.handleWorkspaceUpdate)

	// Multi-workspace CRUD + activate (desktop-only — handlers return 404 in server mode).
	mux.HandleFunc("GET "+api.Workspaces, d.handleListWorkspaces)
	mux.HandleFunc("POST "+api.Workspaces, d.handleCreateWorkspace)
	mux.HandleFunc("GET /api/v1/workspaces/{id}", d.handleGetWorkspace)
	mux.HandleFunc("PUT /api/v1/workspaces/{id}", d.handleUpdateWorkspace)
	mux.HandleFunc("DELETE /api/v1/workspaces/{id}", d.handleDeleteWorkspace)
	mux.HandleFunc("POST /api/v1/workspaces/{id}/activate", d.handleActivateWorkspace)
	mux.HandleFunc("GET /api/v1/workspaces/{id}/config", d.handleGetWorkspaceConfig)
	mux.HandleFunc("PUT /api/v1/workspaces/{id}/config", d.handlePutWorkspaceConfig)
	mux.HandleFunc("POST /api/v1/workspaces/{id}/config/reset", d.handleResetWorkspaceConfig)

	// Git operations
	mux.HandleFunc("POST "+api.GitSkipPull, d.handleGitSkipPull)

	// Namespaces
	mux.HandleFunc("GET "+api.Namespaces, d.handleListNamespaces)
	mux.HandleFunc("POST "+api.Namespaces, d.handleCreateNamespace)
	mux.HandleFunc("DELETE /api/v1/namespaces/{id}", d.handleDeleteNamespace)
	mux.HandleFunc("POST /api/v1/namespaces/{id}/activate", d.handleActivateNamespace)
	mux.HandleFunc("POST /api/v1/namespaces/deactivate", d.handleDeactivateNamespace)
	mux.HandleFunc("GET "+api.NamespaceEdit, d.handleGetNamespaceEdit)
	mux.HandleFunc("PUT "+api.NamespaceEdit, d.handlePutNamespaceEdit)
	mux.HandleFunc("GET "+api.QuickStarts, d.handleGetQuickStarts)

	// Bundles
	mux.HandleFunc("GET "+api.Bundles, d.handleListBundles)
	mux.HandleFunc("POST /api/v1/bundles/{repoId}/pull", d.handleBundleRepoPull)

	// Secrets
	mux.HandleFunc("GET "+api.Secrets, d.handleListSecrets)
	mux.HandleFunc("POST "+api.Secrets, d.handleCreateSecret)
	mux.HandleFunc("PUT /api/v1/secrets/{id}", d.handleUpdateSecret)
	mux.HandleFunc("DELETE /api/v1/secrets/{id}", d.handleDeleteSecret)
	mux.HandleFunc("GET /api/v1/secrets/{id}/test", d.handleTestSecret)

	// Registry auth bindings (host → secret) for reusable registry credentials
	mux.HandleFunc("GET "+api.RegistryBindingsMissing, d.handleMissingRegistryAuth)
	mux.HandleFunc("GET "+api.RegistryBindings, d.handleListRegistryBindings)
	mux.HandleFunc("POST "+api.RegistryBindings, d.handleSetRegistryBinding)

	// Secrets encryption management
	mux.HandleFunc("POST "+api.SecretsUnlock, d.handleUnlockSecrets)
	mux.HandleFunc("POST "+api.SecretsSetupPassword, d.handleSetupPassword)
	mux.HandleFunc("POST "+api.SecretsReset, d.handleResetSecrets)

	// Licenses (enterprise license management)
	mux.HandleFunc("GET /api/v1/licenses", d.handleListLicenses)
	mux.HandleFunc("POST /api/v1/licenses", d.handleCreateLicense)
	mux.HandleFunc("DELETE /api/v1/licenses/{id}", d.handleDeleteLicense)
	mux.HandleFunc("GET "+api.LicensesStatus, d.handleLicenseStatus)

	// Migration (master password for Kotlin encrypted secrets)
	mux.HandleFunc("GET "+api.MigrationStatus, d.handleGetMigrationStatus)
	mux.HandleFunc("POST "+api.MigrationMasterPassword, d.handleSubmitMasterPassword)

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
		mux.HandleFunc("POST /desktop/save-download", d.handleDesktopSaveDownload)
		mux.HandleFunc("POST /desktop/open-downloads", d.handleDesktopOpenDownloads)
	}
	// Update routes only when the service exists (desktop + linux); otherwise the
	// paths are unregistered and the Web UI's update check 404s and stays quiet.
	if d.updateSvc != nil {
		mux.HandleFunc("GET "+api.DesktopUpdateStatus, d.handleUpdateStatus)
		mux.HandleFunc("POST "+api.DesktopUpdateCheck, d.handleUpdateCheck)
		mux.HandleFunc("GET "+api.DesktopUpdateChangelog, d.handleUpdateChangelog)
		mux.HandleFunc("POST "+api.DesktopUpdateApply, d.handleUpdateApply)
	}

	// Browser auth handshake (token → session cookie) for the opt-in API
	// token auth. Public by design — it is the door into the token-protected
	// API; with api_auth disabled it just redirects to /. See apiauth.go.
	mux.HandleFunc("GET "+api.AuthSession, d.handleAuthSession)

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

// activeNsKey returns the (workspaceID, namespaceID) of the active namespace.
func (d *Daemon) activeNsKey() (wsID, nsID string) {
	act := d.active()
	nsID = "default"
	if act.nsConfig != nil {
		nsID = act.nsConfig.ID
	}
	return act.workspaceID, nsID
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
