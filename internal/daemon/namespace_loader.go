package daemon

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/docker"
	"github.com/citeck/citeck-launcher/internal/license"
	"github.com/citeck/citeck-launcher/internal/namespace"
	"github.com/citeck/citeck-launcher/internal/storage"
)

// loadNamespaceInput bundles all dependencies needed to load a namespace into
// a runnable runtime. Used by both initial daemon Start() and the live
// namespace-switch path (handleActivateNamespace) so the two routes share one
// definition of "what a loaded namespace looks like".
type loadNamespaceInput struct {
	Store         storage.Store
	SecretService *storage.SecretService
	DockerClient  *docker.Client
	DaemonCfg     config.DaemonConfig
	Licenses      *license.Service // user-added enterprise licenses; nil ok (falls back to workspace-only)
	WorkspaceID   string
	NamespaceID   string
	Offline       bool
	Desktop       bool
}

// loadedNamespace captures everything loadNamespace produces. The caller is
// responsible for wiring the result into a *Daemon (event callback, ACME
// renewal, snapshot import, runtime.Start) — those side effects depend on
// whether this is initial startup or a live switch.
type loadedNamespace struct {
	NsConfig *namespace.Config
	// DockerClient is the client the runtime was built with — scoped to THIS
	// (workspace, namespace). On a live switch/create it is freshly created
	// here; installLoadedNamespace swaps it into the active dockerClient so daemon-level
	// handlers (volumes, inspect, snapshots) also target the new namespace.
	DockerClient    *docker.Client
	BundleDef       *bundle.Def
	WorkspaceConfig *bundle.WorkspaceConfig
	Runtime         *namespace.Runtime
	AppDefs         []appdef.ApplicationDef
	CloudCfgServer  *CloudConfigServer
	SystemSecrets   namespace.SystemSecrets
	VolumesBase     string
	BundleError     string
	// WsSyncError is non-empty when the workspace's CUSTOM repo failed to sync
	// and no cached workspace config was usable (resolver.WorkspaceSyncError).
	// Surfaced by the Welcome-data endpoints as 502 WS_REPO_SYNC_FAILED.
	WsSyncError string
	// ShouldStart is the persisted-status hint: true unless the previous
	// session ended in STOPPING/STOPPED (Kotlin parity — never auto-start
	// after an explicit stop). Caller may override.
	ShouldStart bool
}

// resolveBundleWithCacheFallback resolves `ref` via the prepared resolver; when
// resolution fails (git pull error, missing bundle file) it falls back to the
// namespace's persisted cached bundle. Shared by loadNamespace (initial load /
// namespace switch) and doReloadEx (reload / force-update) so the fallback
// semantics cannot drift between the two paths.
//
// fallbackWS is substituted as the workspace config on the cache path — the
// workspace repo is independent of the bundle repo whose pull just failed, so
// the caller passes its best already-loaded workspace config (without it the
// fallback would strand every webapp's datasources / env / volume bindings).
// logLabel distinguishes the flows in the warning ("" or "on reload").
// allowCacheFallback=false (--offline startup) keeps "local data missing" a
// hard error instead of silently serving a possibly-stale cache.
//
// Returns (result, usedFallback, err): err is non-nil only when resolution
// failed AND no cache was usable — the caller decides between hard-failing
// (reload) and degrading to an empty bundle (startup wizard path).
func resolveBundleWithCacheFallback(
	resolver *bundle.Resolver, ref bundle.Ref, store storage.Store,
	wsID, nsID string, fallbackWS *bundle.WorkspaceConfig,
	logLabel string, allowCacheFallback bool,
) (*bundle.ResolveResult, bool, error) {
	res, err := resolver.Resolve(ref)
	if err == nil {
		return res, false, nil
	}
	if allowCacheFallback {
		cachedState := loadNsStateFromStore(store, wsID, nsID)
		if cachedState != nil && cachedState.CachedBundle != nil && !cachedState.CachedBundle.IsEmpty() {
			msg := "Bundle resolution failed, using cached bundle"
			if logLabel != "" {
				msg = "Bundle resolution failed " + logLabel + ", using cached bundle"
			}
			slog.Warn(msg, "ref", ref, "err", err,
				"cachedVersion", cachedState.CachedBundle.Key.Version,
				"cachedApps", len(cachedState.CachedBundle.Applications))
			return &bundle.ResolveResult{Bundle: cachedState.CachedBundle, Workspace: fallbackWS}, true, nil
		}
	}
	// Returned unwrapped on purpose: the reload caller adds its own
	// "resolve bundle:" context, and the startup caller surfaces the raw
	// resolver error verbatim as bundleError for the UI banner.
	return nil, false, err //nolint:wrapcheck // callers wrap with flow-specific context
}

// generateAndWriteRuntimeFiles runs the namespace generator and applies the
// resulting file map to disk under volumesBase. Shared tail of loadNamespace
// and doReloadEx — writeRuntimeFiles here is the SINGLE source of truth for
// bind-mount contents (the generator owns embedded defaults it copied and
// mutated plus files built from scratch); nothing else may re-extract appfiles
// over it. The `edited` set skips user-edited files so Web-UI edits survive
// reload/regenerate; writeRuntimeFiles also recovers from Docker's
// dir-in-place-of-file quirk (bind-mount source wiped out-of-band).
func generateAndWriteRuntimeFiles(
	nsCfg *namespace.Config, res *bundle.ResolveResult,
	systemSecrets namespace.SystemSecrets, genOpts namespace.GenerateOpts,
	volumesBase string, edited map[string]bool,
) (*namespace.GenResp, error) {
	genResp, err := namespace.Generate(nsCfg, res.Bundle, res.Workspace, systemSecrets, genOpts)
	if err != nil {
		return nil, fmt.Errorf("generate namespace %q: %w", nsCfg.ID, err)
	}
	writeRuntimeFiles(volumesBase, genResp.Files, edited)
	return genResp, nil
}

// dockerClientScoped reports whether dc is non-nil and already scoped to exactly
// (workspace, namespace). loadNamespace reuses an injected client only when this
// holds; otherwise it rebuilds — which is what makes a mis-scoped client
// impossible to install regardless of the caller.
func dockerClientScoped(dc *docker.Client, workspace, ns string) bool {
	return dc != nil && dc.Workspace() == workspace && dc.Namespace() == ns
}

// loadNamespace builds the full set of namespace-scoped state for a given
// (workspace, namespace) pair: config + bundle resolution + secrets + generator
// pass + Runtime construction + persisted-state restore + (desktop) cloud
// config server. Does NOT start the runtime — caller decides based on
// ShouldStart and external policy (e.g. user-initiated switch never
// auto-starts).
//
// Returns (nil, nil) when no namespace.yml exists at the resolved path. The
// caller treats that as "no namespace configured" — daemon still boots into
// the wizard.
//
//nolint:gocyclo,nestif // mirrors the original Start() block: bundle resolve fallback, secret resolve, generator, state restore — splitting further would scatter state
func loadNamespace(in loadNamespaceInput) (*loadedNamespace, error) {
	wsID := in.WorkspaceID
	nsID := in.NamespaceID
	volumesBase := config.ResolveVolumesBase(wsID, nsID)

	// Resolve workspace config first — needed by wizard even without a namespace.
	resolver := bundle.NewResolverWithAuth(config.BundlesDataDir(wsID), makeTokenLookup(in.SecretService)).
		WithWorkspaceRepo(lookupWorkspaceRepoOpts(in.Store, in.SecretService, wsID))
	// Server mode: never auto-pull git repos (use 'citeck workspace update' for manual sync).
	// Desktop mode: auto-pull with throttling. --offline flag: skip git entirely.
	if in.Offline || !config.IsDesktopMode() {
		resolver.SetOffline(true)
	}
	wsCfg := resolver.ResolveWorkspaceOnly()
	// Record (never fail on) a custom-workspace-repo sync failure: startup
	// resilience is preserved — the daemon still boots — but the Welcome-data
	// endpoints surface it as 502 WS_REPO_SYNC_FAILED instead of silently
	// serving the built-in fallback workspace (the empty config above).
	wsSyncError := workspaceSyncErrorString(resolver)

	// Load namespace config from the store (desktop: DB row; server: file).
	raw, ok, cfgErr := in.Store.LoadNamespaceConfig(wsID, nsID)
	if cfgErr != nil || !ok {
		slog.Warn("No namespace config found", "ws", wsID, "ns", nsID, "err", cfgErr)
		return &loadedNamespace{
			WorkspaceConfig: wsCfg,
			VolumesBase:     volumesBase,
			WsSyncError:     wsSyncError,
		}, nil
	}
	nsCfg, err := namespace.ParseNamespaceConfig([]byte(raw))
	if err != nil {
		slog.Warn("Invalid namespace config", "ws", wsID, "ns", nsID, "err", err)
		return &loadedNamespace{
			WorkspaceConfig: wsCfg,
			VolumesBase:     volumesBase,
			WsSyncError:     wsSyncError,
		}, nil
	}
	if nsCfg.ID == "" {
		nsCfg.ID = nsID
	}

	// Resolve bundle (reuses resolver created above for workspace config).
	// Reuse the workspace config already loaded by ResolveWorkspaceOnly above
	// as the cache-fallback workspace — it comes from the workspace repo (or
	// local repo dir) which is independent of the bundle repo whose git pull
	// just failed (workspace-v1.yml is the source of truth for jdbc URLs,
	// ${PG_HOST} substitution, etc.). Offline mode keeps "local data missing"
	// a hard error — no cache fallback.
	var bundleError string
	preservedWS := wsCfg
	if preservedWS == nil {
		preservedWS = &bundle.WorkspaceConfig{}
	}
	resolveResult, _, resolveErr := resolveBundleWithCacheFallback(
		resolver, nsCfg.BundleRef, in.Store, wsID, nsID, preservedWS, "", !in.Offline)
	if resolveErr != nil {
		if in.Offline {
			return nil, fmt.Errorf("offline mode: bundle %q not available locally — use 'citeck workspace import' to provide workspace data: %w", nsCfg.BundleRef, resolveErr)
		}
		slog.Error("Failed to resolve bundle and no cache available — daemon starts with 0 apps", "ref", nsCfg.BundleRef, "err", resolveErr)
		bundleError = resolveErr.Error()
		resolveResult = &bundle.ResolveResult{Bundle: &bundle.EmptyDef, Workspace: preservedWS}
	}
	bundleDef := resolveResult.Bundle
	wsCfg = resolveResult.Workspace
	// Resolve() re-ran the workspace sync — refresh the recorded error so the
	// surfaced state reflects the freshest pass (a recovered repo clears it).
	wsSyncError = workspaceSyncErrorString(resolver)

	slog.Info("Using bundle", "ref", nsCfg.BundleRef, "apps", len(bundleDef.Applications))

	// Certs (self-signed when TLS is on without LE; Let's Encrypt obtain when
	// configured). The acme.Client is discarded here — startup arms renewal
	// later via startACMERenewalIfConfigured.
	_ = ensureProxyTLSCerts(nsCfg, "")

	// Appfiles are not extracted here. The namespace generator owns the
	// full file set: it seeds ctx.Files from the embedded resources,
	// mutates some (proxy lua scheme/secret, realm JSON, etc.), appends
	// others, and returns the final map in genResp.Files. That map is
	// written to disk below via writeRuntimeFiles, which is the ONLY
	// path that touches bind-mount source files. This avoids the
	// double-write bug where an embed re-extract would revert a
	// generator-customized file back to its default content.

	// Resolve system secrets (JWT, OIDC) — migrate from plain files or generate
	// new. The IsLocked() gate that used to wrap this call was wrong: system
	// secrets live in launcher_state PLAIN (`_sys_<id>` keys), independent of
	// SecretService's encrypted user-secret store. Skipping resolution while
	// the SecretService is locked produced empty JWT env vars baked into the
	// generated webapp containers — webapps then hung in "Запуск" because
	// JWT authenticator initialization failed with an empty secret. The
	// fallbacks to svc.GetSecret (older installs) and plain-file (pre-Store
	// launcher) are tolerant of locked / missing state — they just fall
	// through to generate().
	systemSecrets, sysErr := resolveSystemSecrets(in.Store, in.SecretService, in.Desktop)
	if sysErr != nil {
		return nil, fmt.Errorf("resolve system secrets: %w", sysErr)
	}

	// Load persisted state for detached apps and status recovery.
	// Detached apps must be known BEFORE Generate() so the generator can
	// exclude them from proxy upstreams and compute DependsOnDetachedApps.
	persistedState := loadNsStateFromStore(in.Store, wsID, nsID)
	var genOpts namespace.GenerateOpts
	genOpts.SecretReader = &secretReaderAdapter{svc: in.SecretService}
	// User-added licenses: locked SecretService yields nil and the generator
	// falls back to workspace-only licenses — never aborts startup.
	if in.Licenses != nil {
		genOpts.ExtraLicenses = collectExtraLicensesFrom(in.Licenses)
	} else {
		genOpts.ExtraLicenses = collectExtraLicensesFrom(license.NewService(in.SecretService))
	}
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

	// Overlay user-edited disk content into the hash input so a UI-edited
	// file from a prior session forces container recreate on the first
	// regenerate after daemon restart. Without this, VolumesContentHash is
	// computed against embedded defaults and existing edits are never
	// reflected in the running container.
	if persistedState != nil && len(persistedState.EditedFiles) > 0 {
		genOpts.EditedFileOverlay = readEditedFileOverlay(volumesBase, persistedState.EditedFiles)
	}

	// Build the edited-file skip set from persisted state BEFORE the
	// initial writeRuntimeFiles so user edits made in a previous session
	// are not overwritten by the freshly generated defaults.
	var editedFilesSkip map[string]bool
	if persistedState != nil && len(persistedState.EditedFiles) > 0 {
		editedFilesSkip = make(map[string]bool, len(persistedState.EditedFiles))
		for _, p := range persistedState.EditedFiles {
			editedFilesSkip[p] = true
		}
	}

	// Generate the namespace and write the full runtime file set (embedded
	// defaults + generator modifications) to disk. Single source of truth;
	// never overwritten by a separate embed re-extract.
	genResp, genErr := generateAndWriteRuntimeFiles(nsCfg, resolveResult, systemSecrets, genOpts, volumesBase, editedFilesSkip)
	if genErr != nil {
		return nil, genErr
	}
	slog.Info("Generated namespace", "apps", len(genResp.Applications), "files", len(genResp.Files))

	appDefs := genResp.Applications
	// Bind the runtime to a Docker client scoped to THIS namespace. The live
	// switch/create paths pass a nil DockerClient so a fresh one is built here:
	// reusing the previously-active namespace's client (the bug) made the new
	// runtime adopt the OLD namespace's containers and emit its network name
	// (header showed nsB while containers were nsA). Startup passes its
	// already-built client. No error return follows, so no leak on the happy path.
	// Derive the Docker client from the namespace being loaded — never trust an
	// injected one blindly. An injected client is REUSED only if it is already
	// scoped to exactly (dockerWorkspace, nsID); otherwise it is rebuilt. This is
	// the single choke-point that makes the whole "client scoped to a stale/other
	// namespace" bug class structurally impossible, no matter which caller (start,
	// create, namespace switch, workspace switch) hands us what.
	dockerWorkspace := ""
	if config.IsDesktopMode() {
		dockerWorkspace = wsID
	}
	dc := in.DockerClient
	if !dockerClientScoped(dc, dockerWorkspace, nsID) {
		if dc != nil {
			slog.Error("loadNamespace: injected docker client scoped to wrong target; rebuilding",
				"wantWs", dockerWorkspace, "wantNs", nsID, "gotWs", dc.Workspace(), "gotNs", dc.Namespace())
		}
		var dcErr error
		dc, dcErr = docker.NewClient(dockerWorkspace, nsID)
		if dcErr != nil {
			return nil, fmt.Errorf("create docker client for namespace %q: %w", nsID, dcErr)
		}
	}
	runtime := namespace.NewRuntime(nsCfg, dc, volumesBase)
	runtime.SetStatePersister(nsStatePersister{store: in.Store, wsID: wsID, nsID: nsID})

	// Cache the successfully resolved bundle for fallback on future resolve failures
	if !bundleDef.IsEmpty() {
		runtime.SetCachedBundle(bundleDef)
	}

	// Wire registry auth and operation history into runtime. Registry bindings
	// (host → secret id) take precedence over the legacy scope heuristics so a
	// reused credential resolves without re-entry; a locked/missing store
	// degrades to no bindings (scope fallback still applies).
	registryBindings, _ := in.Store.ListRegistryBindings(wsID)
	runtime.SetRegistryAuthFunc(makeRegistryAuthFunc(wsCfg, in.SecretService, registryBindings))
	runtime.SetHistory(namespace.NewOperationHistory(config.LogDir()))

	// Apply daemon.yml overrides for reconciler and pull concurrency
	if in.DaemonCfg.Reconciler.IntervalSeconds > 0 || in.DaemonCfg.Reconciler.LivenessPeriodMs > 0 || in.DaemonCfg.Reconciler.LivenessEnabled != nil {
		rcfg := namespace.DefaultReconcilerConfig()
		if in.DaemonCfg.Reconciler.IntervalSeconds > 0 {
			rcfg.IntervalSeconds = in.DaemonCfg.Reconciler.IntervalSeconds
		}
		if in.DaemonCfg.Reconciler.LivenessPeriodMs > 0 {
			rcfg.LivenessPeriod = time.Duration(in.DaemonCfg.Reconciler.LivenessPeriodMs) * time.Millisecond
		}
		if in.DaemonCfg.Reconciler.LivenessEnabled != nil {
			rcfg.LivenessEnabled = *in.DaemonCfg.Reconciler.LivenessEnabled
		}
		runtime.SetReconcilerConfig(rcfg)
	}
	if in.DaemonCfg.Docker.PullConcurrency > 0 {
		runtime.SetPullConcurrency(in.DaemonCfg.Docker.PullConcurrency)
	}
	if in.DaemonCfg.Docker.StopTimeout > 0 {
		runtime.SetDefaultStopTimeout(in.DaemonCfg.Docker.StopTimeout)
	}

	// Restore persisted state: manual stopped apps, edited apps, locked apps.
	if persistedState != nil {
		if len(persistedState.ManualStoppedApps) > 0 {
			stopped := make(map[string]bool)
			for _, name := range persistedState.ManualStoppedApps {
				stopped[name] = true
			}
			runtime.SetManualStoppedApps(stopped)
		}
		runtime.RestoreEditedApps(persistedState.EditedApps, persistedState.EditedLockedApps)
		runtime.RestoreEditedFiles(persistedState.EditedFiles)
		runtime.RestoreRestartState(persistedState.RestartEvents, persistedState.RestartCounts)
	} else if len(genOpts.DetachedApps) > 0 {
		// First start with template detached apps — apply to runtime
		runtime.SetManualStoppedApps(genOpts.DetachedApps)
	}
	// Wire DependsOnDetachedApps so RestartApp can trigger regen for dependency apps
	runtime.SetDependsOnDetachedApps(genResp.DependsOnDetachedApps)

	// Start CloudConfigServer only in desktop mode — server-mode webapps
	// have SPRING_CLOUD_CONFIG_ENABLED=false and don't connect to it.
	var cloudCfgSrv *CloudConfigServer
	if config.IsDesktopMode() {
		cloudCfgSrv = NewCloudConfigServer()
		cloudCfgSrv.UpdateConfig(genResp.CloudConfig, systemSecrets.JWT)
		if startErr := cloudCfgSrv.Start(); startErr != nil {
			slog.Warn("CloudConfigServer failed to start", "err", startErr)
		}
	}

	// Status recovery hint: caller chooses whether to act on it.
	// - RUNNING / STARTING / STALLED → ShouldStart=true (re-adopt detached containers).
	// - STOPPING → user-initiated stop was interrupted; finish by staying stopped.
	// - STOPPED → user explicitly stopped the namespace; do not auto-start
	//   on next daemon launch (Kotlin parity — the original launcher never
	//   auto-started; user had to click Start each time).
	shouldStart := true
	if persistedState != nil {
		switch persistedState.Status {
		case namespace.NsStatusStopping, namespace.NsStatusStopped:
			slog.Info("Previous namespace status was stopped — not auto-starting", "status", persistedState.Status)
			shouldStart = false
		}
	}

	return &loadedNamespace{
		NsConfig:        nsCfg,
		DockerClient:    dc,
		BundleDef:       bundleDef,
		WorkspaceConfig: wsCfg,
		Runtime:         runtime,
		AppDefs:         appDefs,
		CloudCfgServer:  cloudCfgSrv,
		SystemSecrets:   systemSecrets,
		VolumesBase:     volumesBase,
		BundleError:     bundleError,
		WsSyncError:     wsSyncError,
		ShouldStart:     shouldStart,
	}, nil
}

// workspaceSyncErrorString flattens the resolver's WorkspaceSyncError into the
// string carried by loadedNamespace / activeNamespace ("" when healthy).
func workspaceSyncErrorString(resolver *bundle.Resolver) string {
	if err := resolver.WorkspaceSyncError(); err != nil {
		return err.Error()
	}
	return ""
}

// installLoadedNamespace atomically swaps a freshly-loaded namespace runtime
// into the live daemon: persists the selection in launcher_state, swaps in a
// new activeNamespace built from the loaded state, tears down the previous
// runtime + cloud-config + ACME renewal AFTER the swap (so a slow Shutdown
// doesn't block the API response), wires the SSE event callback on the new
// runtime, and re-arms ACME renewal if Let's Encrypt is configured for the
// new namespace.
//
// Used by handleActivateNamespace (live namespace switch) and
// handleCreateNamespace (auto-activate after first-time create in desktop
// mode). Callers MUST hold d.reloadMu to serialize against concurrent
// reload/start operations.
func (d *Daemon) installLoadedNamespace(loaded *loadedNamespace, wsID, nsID string) error {
	if loaded == nil || loaded.NsConfig == nil {
		return fmt.Errorf("installLoadedNamespace: nil namespace config")
	}

	// Persist the selection so the next daemon start loads it too.
	state, _ := d.store.GetState()
	if state == nil {
		state = &storage.LauncherState{WorkspaceID: wsID}
	}
	if state.SelectedNs == nil {
		state.SelectedNs = make(map[string]string, 1)
	}
	state.SelectedNs[wsID] = nsID
	if err := d.store.SetState(*state); err != nil {
		// Cleanup the freshly-built runtime we won't be installing.
		if loaded.Runtime != nil {
			loaded.Runtime.Shutdown()
		}
		if loaded.CloudCfgServer != nil {
			loaded.CloudCfgServer.Stop()
		}
		return fmt.Errorf("persist namespace selection: %w", err)
	}

	// Build the complete replacement activeNamespace and swap the pointer in
	// one shot — readers holding an old d.active() snapshot keep a consistent
	// (stale) view; nobody can observe a half-installed namespace.
	d.configMu.Lock()
	old := d.activeLocked()
	next := &activeNamespace{
		workspaceID:     old.workspaceID, // namespace switch stays within the workspace
		dockerClient:    old.dockerClient,
		runtime:         loaded.Runtime,
		nsConfig:        loaded.NsConfig,
		bundleDef:       loaded.BundleDef,
		workspaceConfig: loaded.WorkspaceConfig,
		appDefs:         loaded.AppDefs,
		cloudCfgServer:  loaded.CloudCfgServer,
		systemSecrets:   loaded.SystemSecrets,
		volumesBase:     loaded.VolumesBase,
		bundleError:     loaded.BundleError,
		wsSyncError:     loaded.WsSyncError,
		acmeRenewal:     nil,
	}
	// Swap the daemon-level Docker client to the one the new runtime was built
	// with (scoped to the new namespace). Without this, volumes/inspect/snapshot
	// handlers keep querying the PREVIOUS namespace's containers/network.
	var oldDocker *docker.Client
	if loaded.DockerClient != nil && loaded.DockerClient != old.dockerClient {
		oldDocker = old.dockerClient
		next.dockerClient = loaded.DockerClient
	}
	// Invariant guard at the single swap choke-point: the active client MUST be
	// scoped to the active namespace. loadNamespace already guarantees this, so a
	// trip here means a new code path bypassed it — fail loud rather than silently
	// emit another namespace's container/network names.
	if next.dockerClient != nil && next.nsConfig != nil && next.dockerClient.Namespace() != next.nsConfig.ID {
		slog.Error("BUG: active docker client mis-scoped after namespace install",
			"clientNs", next.dockerClient.Namespace(), "activeNs", next.nsConfig.ID)
	}
	d.activeNs = next
	d.configMu.Unlock()

	if old.runtime != nil {
		old.runtime.Shutdown()
	}
	if oldDocker != nil {
		_ = oldDocker.Close()
	}
	if old.cloudCfgServer != nil {
		old.cloudCfgServer.Stop()
	}
	if old.acmeRenewal != nil {
		old.acmeRenewal.Stop()
	}

	if next.runtime != nil {
		next.runtime.SetEventCallback(func(evt api.EventDto) {
			d.broadcastEvent(evt)
		})
	}
	d.startACMERenewalIfConfigured()
	return nil
}

// clearActiveNamespaceLocked swaps in a fresh activeNamespace that resets the
// FULL namespace-scoped field set (runtime, nsConfig, bundleDef, appDefs,
// cloudCfgServer, systemSecrets, volumesBase, acmeRenewal, bundleError) and
// returns the OLD struct so the caller shuts down its runtime / cloud-config
// server / ACME renewal OUTSIDE the lock. Caller must hold configMu for
// writing.
//
// Single definition for the deactivate / delete-active / workspace-switch
// teardown paths — the field set drifted when it was copy-pasted (workspace
// switch forgot bundleError, leaving a stale boot-time bundle-error banner);
// the whole-struct swap now makes that drift impossible. Workspace-scoped
// fields (workspaceID, dockerClient, workspaceConfig) are intentionally
// CARRIED OVER into the fresh struct: only SwitchWorkspace replaces those,
// and it does so explicitly next to this call.
func (d *Daemon) clearActiveNamespaceLocked() *activeNamespace {
	old := d.activeLocked()
	d.activeNs = &activeNamespace{
		workspaceID:     old.workspaceID,
		dockerClient:    old.dockerClient,
		workspaceConfig: old.workspaceConfig,
		// wsSyncError is workspace-scoped (it describes the workspace repo, not
		// the namespace) — carried over with workspaceConfig; SwitchWorkspace
		// replaces it explicitly next to this call.
		wsSyncError: old.wsSyncError,
	}
	return old
}
