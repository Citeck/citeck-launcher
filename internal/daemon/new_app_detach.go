package daemon

import (
	"context"
	"log/slog"

	"github.com/citeck/citeck-launcher/internal/deps"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/namespace"
	"github.com/citeck/citeck-launcher/internal/storage"
)

// This file answers one question, on the load path and on the reload path
// alike: a bundle release has just added an app to this namespace — should it
// start, or should it arrive detached?
//
// The rule itself is namespace.DecideNewAppDetach (pure, and where the three
// traps are documented). What lives here is the wiring: what "already seen"
// means, where the template's detachedApps come from, and how fresh that list
// has to be.

// templateDetachedApps returns the detachedApps list of the workspace template
// the namespace was created from. Empty for a namespace with no template, or
// one whose template the workspace no longer declares — in both cases there is
// simply nobody to ask, and a new app starts as it always has.
func templateDetachedApps(wsCfg *bundle.WorkspaceConfig, templateID string) []string {
	if wsCfg == nil || templateID == "" {
		return nil
	}
	for _, tmpl := range wsCfg.NamespaceTemplates {
		if tmpl.ID == templateID {
			return tmpl.DetachedApps
		}
	}
	return nil
}

// appNames pulls the generated app names out of a generation's app defs.
func appNames(apps []appdef.ApplicationDef) []string {
	out := make([]string, 0, len(apps))
	for i := range apps {
		out = append(out, apps[i].Name)
	}
	return out
}

// hasUnseenApp reports whether this generation produced an app the namespace
// has not produced before. It is the cheap gate in front of the expensive part
// (a forced git pull, a Docker probe per candidate): on every ordinary load and
// reload — which is all of them but the one after a bundle adds an app — it
// answers false and nothing else runs.
func hasUnseenApp(known, generated []string) bool {
	if len(generated) == 0 {
		return false
	}
	seen := make(map[string]bool, len(known))
	for _, name := range known {
		seen[name] = true
	}
	if len(seen) == 0 {
		for _, name := range namespace.BaselineKnownApps() {
			seen[name] = true
		}
	}
	for _, name := range generated {
		if !seen[name] {
			return true
		}
	}
	return false
}

// freshTemplateDetachedApps re-reads the template list from a FORCE-PULLED
// workspace config.
//
// It exists because the workspace repo and the bundle repo are two repos with
// two independent PullPeriod throttles (1h by default): an ordinary Update &
// Start can pick up a bundle release that introduces an app while holding an
// hour-old workspace-v1.yml that does not list it in detachedApps yet — and the
// mistake would be sticky, since the app is recorded as seen and never asked
// about again. Paying one git fetch for it is cheap because it happens only
// when the generated set actually grew.
//
// Server mode never auto-pulls (the operator drives git with `citeck workspace
// update`), exactly as resolveActiveWorkspaceConfig has it, so there the on-disk
// config IS the answer and counts as fresh — git there is the operator's to
// drive, not ours.
//
// A sync FAILURE is reported as stale (fresh=false) rather than swallowed. The
// caller then defers any candidate this list does not name instead of recording
// it as known: the one load where the workspace repo would not sync is exactly
// the load where a template entry may be missing, and recording the app would
// settle the question forever on the reading that may be wrong — the app starts
// once, is never asked about again, and `hasUnseenApp` closes the gate behind
// it. The error is read with WorkspacePullError, and neither WorkspaceSyncError
// nor WorkspaceSyncErrorAny would do: both answer "this resolve ended up with
// nothing usable", so a pull that FAILED over a good cached clone — the case
// this arm is entirely about — is silent in both, because resolveWorkspace
// returns at its priority-2 load before it ever records into wsSyncErr.
// It is a SEAM (templateDetachLookup) rather than a direct call so the decision
// is testable without a git clone — the same reason d.wsCfgResolveFn exists.
func forcePullTemplateLookup(store storage.Store, secretSvc secretReader, wsID string) templateDetachLookup {
	return func(templateID string, fallback *bundle.WorkspaceConfig) (detached []string, fresh bool) {
		return freshTemplateDetachedApps(store, secretSvc, wsID, templateID, fallback)
	}
}

// templateDetachLookup answers "what does the template detach?", given the
// config already in hand as a fallback. fresh=false means the list could not be
// refreshed and may predate the bundle being decided about.
type templateDetachLookup func(templateID string, fallback *bundle.WorkspaceConfig) (detached []string, fresh bool)

func freshTemplateDetachedApps(store storage.Store, secretSvc secretReader, wsID, templateID string,
	fallback *bundle.WorkspaceConfig,
) (detached []string, fresh bool) {
	if templateID == "" {
		// Nothing to ask and nothing to be stale about.
		return nil, true
	}
	resolver := bundle.NewResolverWithAuth(config.BundlesDataDir(wsID), makeTokenLookup(secretSvc)).
		WithWorkspaceRepo(lookupWorkspaceRepoOpts(store, secretSvc, wsID)).
		WithWorkspaceOverlay(workspaceConfigOverlay(store, wsID)).
		WithForcePull()
	if !config.IsDesktopMode() {
		resolver.SetOffline(true)
	}
	wsCfg := resolver.ResolveWorkspaceOnly()
	if err := resolver.WorkspacePullError(); err != nil || wsCfg == nil {
		slog.Warn("New apps appeared, but the workspace config could not be refreshed; "+
			"apps its cached template does not name are left undecided until it syncs",
			"ws", wsID, "template", templateID, "err", err)
		return templateDetachedApps(fallback, templateID), false
	}
	return templateDetachedApps(wsCfg, templateID), true
}

// runtimePresence is the reload path's probe: the runtime's own evidence first,
// Docker as the answer for everything else.
//
// A runtime ENTRY on its own is deliberately NOT evidence. The generator emits
// an app def for every app in the namespace, and doStart/doRegenerate seed
// r.apps from that set — so a candidate that was DEFERRED on the load path
// (Docker unreachable, nothing decided, nothing recorded) is in the app table
// by the time the next reload runs, and reading the table as "present" would
// answer the deferred question with the one fact that cannot settle it. A
// CONTAINER is what says this stand has really been running the app; the
// runtime short-circuits only when it holds that container's id, and every
// other case asks Docker, which is the same evidence the load path uses.
func runtimePresence(ctx context.Context, rt *namespace.Runtime, dc depsDocker) func(string) namespace.AppPresence {
	fromDocker := dockerPresence(ctx, dc)
	return func(app string) namespace.AppPresence {
		if rt != nil {
			if a := rt.FindApp(app); a != nil && a.ContainerID != "" {
				return namespace.AppPresencePresent
			}
		}
		return fromDocker(app)
	}
}

// dockerPresence answers AppPresence by asking Docker, which is what the LOAD
// path has: the runtime is built after the generation, so its app table is
// empty and cannot tell a genuinely new app from one this stand has been
// running for a year. A container that exists is that proof.
//
// A failed inspect is AppPresenceUnknown and not "absent": Docker being down is
// not evidence that an app was never there, and the caller records nothing for
// it, so the next load asks again.
func dockerPresence(ctx context.Context, dc depsDocker) func(string) namespace.AppPresence {
	return func(app string) namespace.AppPresence {
		if dc == nil {
			return namespace.AppPresenceUnknown
		}
		if _, err := dc.InspectContainer(ctx, dc.ContainerName(app)); err != nil {
			if isNotFoundErr(err) {
				return namespace.AppPresenceAbsent
			}
			return namespace.AppPresenceUnknown
		}
		return namespace.AppPresencePresent
	}
}

// decideNewAppDetach is the whole pass: gate, refresh, decide, report. Both
// call sites do the same three things with the answer — merge Detach into the
// operator's detach set, regenerate when it is non-empty, and install Known on
// the runtime.
func decideNewAppDetach(known []string, generated []appdef.ApplicationDef, nsCfg *namespace.Config,
	wsCfg *bundle.WorkspaceConfig, templates templateDetachLookup,
	presence func(string) namespace.AppPresence,
) namespace.NewAppDetachResult {
	names := appNames(generated)
	if !hasUnseenApp(known, names) {
		return namespace.NewAppDetachResult{Known: known}
	}
	templateID := ""
	if nsCfg != nil {
		templateID = nsCfg.Template
	}
	detachedByTemplate, fresh := templateDetachedApps(wsCfg, templateID), true
	if templates != nil {
		detachedByTemplate, fresh = templates(templateID, wsCfg)
	}
	res := namespace.DecideNewAppDetach(namespace.NewAppDetachInput{
		Known:            known,
		Generated:        names,
		TemplateDetached: detachedByTemplate,
		TemplateStale:    !fresh,
		Presence:         presence,
	})
	if len(res.Detach) > 0 {
		slog.Info("New apps arrived with this bundle and the workspace template has them detached; "+
			"they will not start by themselves",
			"apps", res.Detach, "template", templateID)
	}
	if len(res.Deferred) > 0 {
		slog.Warn("Could not tell whether these new apps already exist on this host; "+
			"leaving them alone and asking again on the next load",
			"apps", res.Deferred)
	}
	return res
}

// knownAppsOf reads the persisted known-app set. Absent (a state file written
// before the field existed, or no state at all) yields nil, which
// DecideNewAppDetach reads as "use the frozen baseline".
func knownAppsOf(state *namespace.NsPersistedState) []string {
	if state == nil {
		return nil
	}
	return state.KnownApps
}

// installWorkspaceDependencies registers the PostgreSQL clusters the ACTIVE
// workspace declares (`databases:` in workspace-v1.yml) with the dependency
// registry, so that a database added by configuration gets the same pin, volume
// generation, migration and rollback a built-in one has.
//
// It must run BEFORE anything reads the registry for this namespace — the pin
// seeding and the generator both do — and it REPLACES the previous set, because
// a workspace switch must not leave the previous workspace's clusters behind.
// deps.SetExtraDependencies drops any id that is already built in, so declaring
// `postgres` (or the observer's database) here cannot point the pin, the probe
// or the migration at somebody else's data.
func installWorkspaceDependencies(wsCfg *bundle.WorkspaceConfig) {
	specs := namespace.DatabaseSpecs(wsCfg)
	ds := make([]deps.Descriptor, 0, len(specs))
	for _, s := range specs {
		ds = append(ds, s.Descriptor())
	}
	deps.SetExtraDependencies(ds)
}
