package daemon

import (
	"fmt"
	"net/http"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/namespace"
)

// reloadPlanInputs is the product of the dry-run resolve+generate phase: the
// freshly-resolved desired app set plus the bundle-version context the plan
// DTO reports alongside the per-app verdicts.
type reloadPlanInputs struct {
	apps           []appdef.ApplicationDef
	bundleBefore   string
	bundleAfter    string
	bundleFallback bool
}

// handleReloadPlan (GET /api/v1/namespace/reload-plan) computes what a reload
// WOULD do right now, without doing any of it: it runs the same
// resolve→generate pipeline as doReloadEx Phase 1 (side-effect-free variant,
// see resolveReloadPlanInputs) and diffs the generated definitions against the
// runtime's current state via Runtime.PlanRegenerate — the exact comparison a
// real Regenerate performs, so the plan never lies.
//
// reloadMu is TryLock'd like handleReloadNamespace: the resolver shares the
// on-disk git/bundle dirs with a real reload, and interleaving the two could
// also produce a plan for a state that is being replaced mid-flight.
func (d *Daemon) handleReloadPlan(w http.ResponseWriter, r *http.Request) {
	if !d.reloadMu.TryLock() {
		writeErrorCode(w, http.StatusConflict, api.ErrCodeReloadInProgress, "reload already in progress")
		return
	}
	defer d.reloadMu.Unlock()

	act := d.active()
	if act.runtime == nil || act.nsConfig == nil || act.bundleDef == nil {
		writeErrorCode(w, http.StatusBadRequest, api.ErrCodeNotConfigured, "no namespace configured")
		return
	}

	inputs, err := d.reloadPlanInputs(act)
	if err != nil {
		writeInternalError(w, fmt.Errorf("reload plan: %w", err))
		return
	}

	entries := act.runtime.PlanRegenerate(r.Context(), inputs.apps)
	dto := api.ReloadPlanDto{
		Apps:           make([]api.ReloadPlanAppDto, 0, len(entries)),
		BundleBefore:   inputs.bundleBefore,
		BundleAfter:    inputs.bundleAfter,
		BundleFallback: inputs.bundleFallback,
	}
	for _, e := range entries {
		dto.Apps = append(dto.Apps, api.ReloadPlanAppDto{
			Name:        e.Name,
			Verdict:     e.Verdict,
			DiffAdded:   e.DiffAdded,
			DiffRemoved: e.DiffRemoved,
			SnapshotTag: e.SnapshotTag,
		})
		switch e.Verdict {
		case namespace.PlanVerdictCreate:
			dto.Summary.Create++
		case namespace.PlanVerdictRecreate:
			dto.Summary.Recreate++
		case namespace.PlanVerdictKeep:
			dto.Summary.Keep++
		case namespace.PlanVerdictRemove:
			dto.Summary.Remove++
		case namespace.PlanVerdictDetached:
			dto.Summary.Detached++
		}
	}
	// Mirror doReloadEx's bundle-fallback safety net: a fallback set smaller
	// than the live runtime would NOT be applied (preserve current runtime),
	// so the plan must say so instead of presenting the verdicts as actions.
	if inputs.bundleFallback && len(inputs.apps) < act.runtime.AppCount() {
		dto.WouldSkip = true
	}
	writeJSON(w, dto)
}

// reloadPlanInputs dispatches to the test seam when set, the production
// resolve otherwise.
func (d *Daemon) reloadPlanInputs(act activeNamespace) (*reloadPlanInputs, error) {
	if d.planInputsFn != nil {
		return d.planInputsFn(act)
	}
	return d.resolveReloadPlanInputs(act)
}

// resolveReloadPlanInputs is doReloadEx Phase 1 with every side effect
// removed. Kept in lockstep with doReloadEx so plan and reload can never
// disagree about the desired set:
//
//   - config is re-read from the store through the same choke-point;
//   - the bundle resolver is built identically (same auth, same workspace
//     repo opts, same cache fallback) and NO force-pull. Note that the normal
//     resolve pipeline may perform a throttled git pull — that is acceptable
//     and intentional: a real reload does the same pull, so skipping it here
//     would make the plan diverge from what reload would actually deploy;
//   - the generator runs with the same GenerateOpts (secret reader, detached
//     apps, edited-file overlay, extra licenses) so per-app hash inputs are
//     bit-identical to a real reload's;
//   - deliberately ABSENT vs doReloadEx: ensureProxyTLSCerts (cert
//     obtain/renewal I/O) and writeRuntimeFiles (bind-mount writes) — the
//     plan must not touch disk or remote CAs. Neither feeds the hash diff:
//     hash inputs come from Generate's in-memory file map, not from disk.
func (d *Daemon) resolveReloadPlanInputs(act activeNamespace) (*reloadPlanInputs, error) {
	nsID := act.nsConfig.ID
	nsCfg, err := d.loadNamespaceConfigFromStore(act.workspaceID, nsID)
	if err != nil {
		return nil, fmt.Errorf("reload config: %w", err)
	}

	resolver := bundle.NewResolverWithAuth(config.BundlesDataDir(act.workspaceID), makeTokenLookup(d.secretReaderFunc())).
		WithWorkspaceRepo(d.resolveActiveWorkspaceRepoOpts()).
		WithWorkspaceOverlay(workspaceConfigOverlay(d.store, act.workspaceID))
	resolveResult, bundleFallback, resolveErr := resolveBundleWithCacheFallback(
		resolver, nsCfg.BundleRef, d.store, act.workspaceID, nsID, act.workspaceConfig, "on reload plan", true)
	if resolveErr != nil {
		return nil, fmt.Errorf("resolve bundle: %w", resolveErr)
	}

	var genOpts namespace.GenerateOpts
	genOpts.SecretReader = d.nsSecretReader()
	genOpts.DetachedApps = act.runtime.ManualStoppedApps()
	fileEdits := act.runtime.FileEditsSnapshot()
	genOpts.EditedFileEdits = fileEdits
	genOpts.DiskContent = readDiskContent(act.volumesBase, fileEdits)
	genOpts.ExtraLicenses = collectExtraLicensesFrom(d.licenses)
	genResp, genErr := namespace.Generate(nsCfg, resolveResult.Bundle, resolveResult.Workspace, act.systemSecrets, genOpts)
	if genErr != nil {
		return nil, fmt.Errorf("generate namespace %q: %w", nsCfg.ID, genErr)
	}

	out := &reloadPlanInputs{apps: genResp.Applications, bundleFallback: bundleFallback}
	if act.bundleDef != nil {
		out.bundleBefore = act.bundleDef.Key.Version
	}
	if resolveResult.Bundle != nil {
		out.bundleAfter = resolveResult.Bundle.Key.Version
	}
	return out, nil
}
