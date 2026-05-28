package daemon

import (
	"fmt"
	"log/slog"
	"path/filepath"
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
	NsConfig        *namespace.Config
	BundleDef       *bundle.Def
	WorkspaceConfig *bundle.WorkspaceConfig
	Runtime         *namespace.Runtime
	AppDefs         []appdef.ApplicationDef
	CloudCfgServer  *CloudConfigServer
	SystemSecrets   namespace.SystemSecrets
	VolumesBase     string
	BundleError     string
	// ShouldStart is the persisted-status hint: true unless the previous
	// session ended in STOPPING/STOPPED (Kotlin parity — never auto-start
	// after an explicit stop). Caller may override.
	ShouldStart bool
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
	bundlesDataDir := config.DataDir()
	if config.IsDesktopMode() {
		bundlesDataDir = filepath.Join(config.HomeDir(), "ws", wsID)
	}
	resolver := bundle.NewResolverWithAuth(bundlesDataDir, makeTokenLookup(in.SecretService)).
		WithWorkspaceRepo(lookupWorkspaceRepoOpts(in.Store, in.SecretService, wsID))
	// Server mode: never auto-pull git repos (use 'citeck workspace update' for manual sync).
	// Desktop mode: auto-pull with throttling. --offline flag: skip git entirely.
	if in.Offline || !config.IsDesktopMode() {
		resolver.SetOffline(true)
	}
	wsCfg := resolver.ResolveWorkspaceOnly()

	// Load namespace config (mode-aware path)
	nsCfgPath := config.ResolveNamespaceConfigPath(wsID, nsID)
	nsCfg, err := namespace.LoadNamespaceConfig(nsCfgPath)
	if err != nil {
		slog.Warn("No namespace config found", "path", nsCfgPath, "err", err)
		return &loadedNamespace{
			WorkspaceConfig: wsCfg,
			VolumesBase:     volumesBase,
		}, nil
	}
	if nsCfg.ID == "" {
		nsCfg.ID = nsID
	}

	// Resolve bundle (reuses resolver created above for workspace config).
	var bundleError string
	resolveResult, resolveErr := resolver.Resolve(nsCfg.BundleRef)
	if resolveErr != nil {
		if in.Offline {
			return nil, fmt.Errorf("offline mode: bundle %q not available locally — use 'citeck workspace import' to provide workspace data: %w", nsCfg.BundleRef, resolveErr)
		}
		// Fallback to cached bundle from persisted state (survives bundle file deletion/move).
		// Reuse the workspace config already loaded by ResolveWorkspaceOnly above —
		// it comes from the workspace repo (or local repo dir) which is independent
		// of the bundle repo whose git pull just failed. Without this the fallback
		// strands every webapp's datasources / env / volume binding (workspace-v1.yml
		// is the source of truth for jdbc URLs, ${PG_HOST} substitution, etc.), and
		// webapps fall through to baked-in dev defaults pointing at localhost:14523.
		cachedState := namespace.LoadNsState(volumesBase, nsID)
		preservedWS := wsCfg
		if preservedWS == nil {
			preservedWS = &bundle.WorkspaceConfig{}
		}
		if cachedState != nil && cachedState.CachedBundle != nil && !cachedState.CachedBundle.IsEmpty() {
			slog.Warn("Bundle resolution failed, using cached bundle", "ref", nsCfg.BundleRef, "err", resolveErr,
				"cachedVersion", cachedState.CachedBundle.Key.Version, "cachedApps", len(cachedState.CachedBundle.Applications))
			resolveResult = &bundle.ResolveResult{Bundle: cachedState.CachedBundle, Workspace: preservedWS}
		} else {
			slog.Error("Failed to resolve bundle and no cache available — daemon starts with 0 apps", "ref", nsCfg.BundleRef, "err", resolveErr)
			bundleError = resolveErr.Error()
			resolveResult = &bundle.ResolveResult{Bundle: &bundle.EmptyDef, Workspace: preservedWS}
		}
	}
	bundleDef := resolveResult.Bundle
	wsCfg = resolveResult.Workspace

	slog.Info("Using bundle", "ref", nsCfg.BundleRef, "apps", len(bundleDef.Applications))

	// Self-signed cert: generate if TLS enabled + no cert paths + no LE
	ensureSelfSignedCert(nsCfg)

	// Let's Encrypt: obtain certificate if configured and not yet present
	_ = ensureACMECert(nsCfg, "")

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
	persistedState := namespace.LoadNsState(volumesBase, nsID)
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

	// Generate namespace
	genResp, genErr := namespace.Generate(nsCfg, bundleDef, resolveResult.Workspace, systemSecrets, genOpts)
	if genErr != nil {
		return nil, fmt.Errorf("generate namespace %q: %w", nsID, genErr)
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

	// Write the full runtime file set (embedded defaults + generator
	// modifications) to disk. Single source of truth; never overwritten
	// by a separate embed re-extract.
	writeRuntimeFiles(volumesBase, genResp.Files, editedFilesSkip)
	slog.Info("Generated namespace", "apps", len(genResp.Applications), "files", len(genResp.Files))

	appDefs := genResp.Applications
	runtime := namespace.NewRuntime(nsCfg, in.DockerClient, volumesBase)

	// Cache the successfully resolved bundle for fallback on future resolve failures
	if !bundleDef.IsEmpty() {
		runtime.SetCachedBundle(bundleDef)
	}

	// Wire registry auth and operation history into runtime
	runtime.SetRegistryAuthFunc(makeRegistryAuthFunc(wsCfg, in.SecretService))
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
		BundleDef:       bundleDef,
		WorkspaceConfig: wsCfg,
		Runtime:         runtime,
		AppDefs:         appDefs,
		CloudCfgServer:  cloudCfgSrv,
		SystemSecrets:   systemSecrets,
		VolumesBase:     volumesBase,
		BundleError:     bundleError,
		ShouldStart:     shouldStart,
	}, nil
}

// installLoadedNamespace atomically swaps a freshly-loaded namespace runtime
// into the live daemon: persists the selection in launcher_state, replaces
// d.runtime / d.nsConfig / d.bundleDef / etc., tears down the previous
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

	d.configMu.Lock()
	oldRuntime := d.runtime
	oldCloudCfgSrv := d.cloudCfgServer
	oldACME := d.acmeRenewal
	d.runtime = loaded.Runtime
	d.nsConfig = loaded.NsConfig
	d.bundleDef = loaded.BundleDef
	d.workspaceConfig = loaded.WorkspaceConfig
	d.appDefs = loaded.AppDefs
	d.cloudCfgServer = loaded.CloudCfgServer
	d.systemSecrets = loaded.SystemSecrets
	d.volumesBase = loaded.VolumesBase
	d.bundleError = loaded.BundleError
	d.acmeRenewal = nil
	d.configMu.Unlock()

	if oldRuntime != nil {
		oldRuntime.Shutdown()
	}
	if oldCloudCfgSrv != nil {
		oldCloudCfgSrv.Stop()
	}
	if oldACME != nil {
		oldACME.Stop()
	}

	if d.runtime != nil {
		d.runtime.SetEventCallback(func(evt api.EventDto) {
			d.broadcastEvent(evt)
		})
	}
	d.startACMERenewalIfConfigured()
	return nil
}
