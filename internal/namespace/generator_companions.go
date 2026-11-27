package namespace

import (
	"log/slog"
	"slices"
	"sort"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/bundle"
)

// appliedCompanion records one companion block whose environments were applied
// to a generated webapp before exclusion/pruning so wireWorkspaceCompanionDeps
// can wire its dependsOn edges on the final surviving app set.
type appliedCompanion struct {
	appName   string
	companion string
	dependsOn []string
}

type companionPair struct {
	app       string
	companion string
}

type companionWebappTarget struct {
	ws      bundle.WebappConfig
	app     *AppBuilder
	baseEnv appdef.OrderedMap
	nsEnv   map[string]string
}

type resolvedCompanionBlock struct {
	envs    map[string]string
	missing []string
}

// wireWorkspaceCompanionEnvs applies the workspace's webapps[].companions
// environments onto generated webapps (ctx.GeneratedWebapps) before
// excludeAppsMissingSecrets and pruneAppsWithMissingDeps.
//
// Survival is computed as a greatest fixpoint over ctx.Applications:
//  1. Start with every generated app tentatively surviving.
//  2. On each iteration, rebuild every surviving target webapp's environments
//     from its pre-companion snapshot plus all valid companion blocks whose
//     companion app is currently surviving (in sorted companion-name order).
//  3. Eliminate any app that still carries an unresolved secret reference or
//     depends on a non-surviving app, and repeat until stable.
//
// Because all surviving companion blocks for a webapp are evaluated together:
//   - One or more surviving companions (including companions with acyclic
//     reverse dependsOn edges onto the webapp) that jointly override every
//     missing defaultProps secret rescue the webapp and its consumers.
//   - If any companion fails (absent, missing own secret, missing dependency,
//     or unresolved secret in its own block), it drops out, the webapp's
//     environments are rebuilt without it, and any secret it no longer covers
//     causes the webapp (and its dependents) to be excluded as usual.
//   - An absent, excluded, or pruned companion wires nothing and never removes
//     a healthy webapp.
//   - Companions bind by presence in ctx.Applications (Variant A: detached
//     companions remain wired and are not marked via MarkGatingApp).
//
// Precedence of an env key: namespace.yml webapps[<id>].environments win (and
// are skipped BEFORE template/secret resolution so an overridden key with an
// unresolved secret cannot block the rest of the companion block), then active
// companion blocks in sorted companion-name order, then workspace defaultProps.
// Duplicate ws.ID entries in ws.Webapps keep the first entry, matching
// generateWebapp.
func wireWorkspaceCompanionEnvs(ctx *NsGenContext) []appliedCompanion {
	if ctx.WorkspaceConfig == nil {
		return nil
	}
	targets := collectCompanionTargets(ctx)
	if len(targets) == 0 {
		return nil
	}

	for _, t := range targets {
		if _, self := t.ws.Companions[t.ws.ID]; self {
			slog.Warn("Workspace webapp lists itself as a companion; ignored", "app", t.ws.ID)
		}
	}

	surviving := make(map[string]bool, len(ctx.Applications))
	for name := range ctx.Applications {
		surviving[name] = true
	}
	cache := make(map[companionPair]resolvedCompanionBlock)

	for {
		applySurvivingCompanionEnvs(ctx, targets, surviving, cache)
		if !eliminateUnsurvivingApps(ctx, surviving) {
			break
		}
	}

	var applied []appliedCompanion
	for _, t := range targets {
		if !surviving[t.ws.ID] {
			continue
		}
		for _, companion := range sortedCompanionNames(t.ws.Companions) {
			if companion == t.ws.ID || !surviving[companion] {
				continue
			}
			block := cache[companionPair{app: t.ws.ID, companion: companion}]
			if len(block.missing) > 0 {
				slog.Error("Companion settings not applied: a secret they reference has no value in this namespace",
					"app", t.ws.ID, "companion", companion, "secrets", block.missing)
				continue
			}
			applied = append(applied, appliedCompanion{
				appName:   t.ws.ID,
				companion: companion,
				dependsOn: t.ws.Companions[companion].DependsOn,
			})
		}
	}
	return applied
}

func collectCompanionTargets(ctx *NsGenContext) []companionWebappTarget {
	var targets []companionWebappTarget
	seen := make(map[string]bool, len(ctx.WorkspaceConfig.Webapps))
	for _, ws := range ctx.WorkspaceConfig.Webapps {
		if seen[ws.ID] {
			continue
		}
		seen[ws.ID] = true
		if !ctx.GeneratedWebapps[ws.ID] || len(ws.Companions) == 0 {
			continue
		}
		app, ok := ctx.Applications[ws.ID]
		if !ok {
			continue
		}
		targets = append(targets, companionWebappTarget{
			ws:      ws,
			app:     app,
			baseEnv: slices.Clone(app.Environments),
			nsEnv:   ctx.Config.Webapps[ws.ID].Environments,
		})
	}
	return targets
}

// applySurvivingCompanionEnvs rebuilds each target webapp's environments from
// its pre-companion baseEnv plus every valid companion block whose companion
// is currently in surviving, in sorted companion-name order.
func applySurvivingCompanionEnvs(
	ctx *NsGenContext,
	targets []companionWebappTarget,
	surviving map[string]bool,
	cache map[companionPair]resolvedCompanionBlock,
) {
	for i := range targets {
		t := &targets[i]
		t.app.Environments = slices.Clone(t.baseEnv)
		if !surviving[t.ws.ID] {
			continue
		}
		for _, companion := range sortedCompanionNames(t.ws.Companions) {
			if companion == t.ws.ID || !surviving[companion] {
				continue
			}
			pair := companionPair{app: t.ws.ID, companion: companion}
			block, ok := cache[pair]
			if !ok {
				block = resolveCompanionEnvs(ctx, t.ws.Companions[companion], t.nsEnv)
				cache[pair] = block
			}
			if len(block.missing) > 0 {
				continue
			}
			for _, k := range sortedKeys(block.envs) {
				t.app.AddEnv(k, block.envs[k])
			}
		}
	}
}

// eliminateUnsurvivingApps removes from surviving any app that still carries
// an unresolved secret reference or depends on an app not in surviving.
func eliminateUnsurvivingApps(ctx *NsGenContext, surviving map[string]bool) bool {
	removed := false
	for name, alive := range surviving {
		if !alive {
			continue
		}
		app := ctx.Applications[name]
		if len(unresolvedSecretRefs(app, ctx.CloudConfig[name])) > 0 {
			surviving[name] = false
			removed = true
			continue
		}
		for _, dep := range app.DependsOn {
			if !surviving[dep] {
				surviving[name] = false
				removed = true
				break
			}
		}
	}
	return removed
}

// resolveCompanionEnvs resolves one companion block's non-overridden
// environment variables (skipping nsEnv keys BEFORE template/secret resolution)
// and collects any unresolved secret IDs without logging yet.
func resolveCompanionEnvs(
	ctx *NsGenContext,
	props bundle.WebappCompanionProps,
	nsEnv map[string]string,
) resolvedCompanionBlock {
	resolved := make(map[string]string, len(props.Environments))
	var missing []string
	for _, k := range sortedKeys(props.Environments) {
		if _, own := nsEnv[k]; own {
			continue
		}
		val := resolveTemplateVarsWithContext(props.Environments[k], ctx)
		for _, m := range secretRefPattern.FindAllStringSubmatch(val, -1) {
			missing = append(missing, m[1])
		}
		resolved[k] = val
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return resolvedCompanionBlock{missing: slices.Compact(missing)}
	}
	return resolvedCompanionBlock{envs: resolved}
}

// wireWorkspaceCompanionDeps adds the dependsOn edges of applied companion
// blocks onto the final surviving app set, skipping missing targets and any
// edge that would close a direct or transitive dependency cycle.
func wireWorkspaceCompanionDeps(ctx *NsGenContext, applied []appliedCompanion) {
	for _, a := range applied {
		app, ok := ctx.Applications[a.appName]
		if !ok {
			continue
		}
		if _, ok := ctx.Applications[a.companion]; !ok {
			continue
		}
		for _, dep := range a.dependsOn {
			if _, ok := ctx.Applications[dep]; !ok {
				continue
			}
			if reachesApp(ctx, dep, a.appName) {
				slog.Warn("Companion dependsOn would create a dependency cycle; skipping edge",
					"app", a.appName, "companion", a.companion, "dep", dep)
				continue
			}
			app.AddDependsOn(dep)
		}
	}
}

// reachesApp reports whether target is reachable from start along
// ctx.Applications[*].DependsOn edges (including start == target).
func reachesApp(ctx *NsGenContext, start, target string) bool {
	visited := make(map[string]bool)
	var walk func(string) bool
	walk = func(cur string) bool {
		if cur == target {
			return true
		}
		if visited[cur] {
			return false
		}
		visited[cur] = true
		b, ok := ctx.Applications[cur]
		if !ok {
			return false
		}
		return slices.ContainsFunc(b.DependsOn, walk)
	}
	return walk(start)
}

func sortedCompanionNames(m map[string]bundle.WebappCompanionProps) []string {
	names := make([]string, 0, len(m))
	for k := range m {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}
