package daemon

import (
	"log/slog"
	"time"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/namespace"
)

// Unlocking a stopped stand must repair definitions generated with a locked
// vault, while preserving the operator's decision to keep the stand stopped.
// Use the already resolved workspace/bundle: unlocking is not a Git update.
func (d *Daemon) refreshStoppedNamespaceSecrets() {
	act := d.active()
	if act.nsConfig == nil {
		return
	}
	wsID, nsID := act.workspaceID, act.nsConfig.ID
	if d.longOp.TryLock(longOpRequest) {
		if d.reloadMu.TryLock() {
			d.refreshStoppedNamespaceSecretsLocked(wsID, nsID)
			d.reloadMu.Unlock()
			d.longOp.Unlock()
			return
		}
		d.longOp.Unlock()
	}
	// An unlock is a one-shot notification. Do not lose it behind a reload
	// that captured the locked vault and is about to commit stale definitions.
	d.bgWg.Go(func() {
		d.longOp.Lock(longOpRequest)
		defer d.longOp.Unlock()
		d.reloadMu.Lock()
		defer d.reloadMu.Unlock()
		if d.bgCtx != nil && d.bgCtx.Err() != nil {
			return
		}
		d.refreshStoppedNamespaceSecretsLocked(wsID, nsID)
	})
}

// Both generation locks are held in the same order as the lifecycle handlers.
func (d *Daemon) refreshStoppedNamespaceSecretsLocked(wsID, nsID string) {
	act := d.active()
	if act.workspaceID != wsID || act.nsConfig == nil || act.nsConfig.ID != nsID {
		return // A switch loads its own generation with the now-readable vault.
	}
	if act.runtime == nil || act.nsConfig == nil || act.bundleDef == nil ||
		act.runtime.Status() != namespace.NsStatusStopped || act.runtime.SnapshotImportPending() || act.runtime.MigrationJournal() != nil ||
		!namespaceDeclaresSecrets(act.workspaceConfig) {
		return
	}
	opts := namespace.GenerateOpts{
		DependencyStates: act.runtime.DependencyStates(),
		SecretReader:     d.nsSecretReader(),
		NamespaceSecrets: namespaceSecretsFor(d.secretService, act.workspaceID, act.nsConfig.ID, act.workspaceConfig, nsSecretsSeed),
		DetachedApps:     act.runtime.ManualStoppedApps(),
		EditedAppPatches: act.runtime.AppPatchesSnapshot(),
		EditedFileEdits:  act.runtime.FileEditsSnapshot(),
		ExtraLicenses:    collectExtraLicensesFrom(d.licenses),
	}
	opts.DatalessDependencies = datalessPins(d.bgCtx, opts.DependencyStates,
		dockerDependencyProbe{dc: depsDockerOf(act.dockerClient), volumesBase: act.volumesBase},
		namespaceDependencies(act.nsConfig, act.bundleDef, act.workspaceConfig), false)
	opts.DiskContent = readDiskContent(act.volumesBase, opts.EditedFileEdits)
	res := &bundle.ResolveResult{Bundle: act.bundleDef, Workspace: act.workspaceConfig}
	gen, err := generateAndWriteRuntimeFiles(act.nsConfig, res, act.systemSecrets, opts, act.volumesBase, nil)
	if err != nil {
		slog.Warn("Could not refresh stopped namespace after unlocking secrets", "err", err)
		return
	}
	if !act.runtime.RefreshStoppedApps(gen.Applications) {
		return
	}
	act.runtime.SetLastGenFiles(gen.BaselineFiles)
	act.runtime.SetGeneratedDefs(gen.BaselineApplications)
	act.runtime.SetCustomLinks(gen.CustomLinks)
	act.runtime.SetDependsOnDetachedApps(gen.DependsOnDetachedApps)
	act.runtime.SetGatingApps(gen.GatingApps)
	d.configMu.Lock()
	a := d.activeLocked()
	a.appDefs = gen.Applications
	a.dependencies = gen.Dependencies
	a.dependencyUpgrades = gen.DependencyUpgrades
	a.contentStorageProblem = gen.ContentStorageProblem
	d.configMu.Unlock()
	if act.cloudCfgServer != nil {
		act.cloudCfgServer.UpdateConfig(gen.CloudConfig, act.systemSecrets.JWT)
	}
	d.broadcastEvent(api.EventDto{Type: "namespace_reloaded", Timestamp: time.Now().UnixMilli(), NamespaceID: act.nsConfig.ID})
}
