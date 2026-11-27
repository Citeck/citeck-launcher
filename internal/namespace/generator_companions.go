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

// wireWorkspaceCompanionEnvs applies the workspace's webapps[].companions
// environments onto generated webapps (ctx.GeneratedWebapps) before
// excludeAppsMissingSecrets and pruneAppsWithMissingDeps.
//
// Only companions whose own secrets and transitive dependsOn chain survive
// (companionSurvives) are wired:
//   - A surviving companion overriding a defaultProps env key that held an
//     unresolved ${secret:...} rescues the webapp (and its consumers) from
//     exclusion.
//   - An absent, secret-missing, or pruned companion wires nothing and never
//     removes the webapp.
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

	checked := make(map[companionPair]bool)
	active := make(map[companionPair]map[string]string)
	for {
		progressed := false
		for i := range targets {
			if activateCompanionsForTarget(ctx, &targets[i], checked, active) {
				progressed = true
			}
		}
		if !progressed {
			break
		}
	}

	var applied []appliedCompanion
	for _, t := range targets {
		for _, companion := range sortedCompanionNames(t.ws.Companions) {
			if _, ok := active[companionPair{app: t.ws.ID, companion: companion}]; ok {
				applied = append(applied, appliedCompanion{
					appName:   t.ws.ID,
					companion: companion,
					dependsOn: t.ws.Companions[companion].DependsOn,
				})
			}
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

func activateCompanionsForTarget(
	ctx *NsGenContext,
	t *companionWebappTarget,
	checked map[companionPair]bool,
	active map[companionPair]map[string]string,
) bool {
	changed := false
	names := sortedCompanionNames(t.ws.Companions)
	for _, companion := range names {
		pair := companionPair{app: t.ws.ID, companion: companion}
		if checked[pair] {
			continue
		}
		if companion == t.ws.ID {
			slog.Warn("Workspace webapp lists itself as a companion; ignored", "app", t.ws.ID)
			checked[pair] = true
			continue
		}
		if !companionSurvives(ctx, companion) {
			continue
		}
		checked[pair] = true
		resolved, ok := resolveCompanionEnvs(ctx, t.ws.ID, companion, t.ws.Companions[companion], t.nsEnv)
		if !ok {
			continue
		}
		active[pair] = resolved
		changed = true
	}
	if !changed {
		return false
	}
	// Rebuild from the pre-companion snapshot in sorted companion order so
	// precedence stays deterministic even if a chained rescue activates an
	// alphabetically earlier companion in a later fixpoint round.
	t.app.Environments = slices.Clone(t.baseEnv)
	for _, companion := range names {
		resolved, ok := active[companionPair{app: t.ws.ID, companion: companion}]
		if !ok {
			continue
		}
		for _, k := range sortedKeys(resolved) {
			t.app.AddEnv(k, resolved[k])
		}
	}
	return true
}

// resolveCompanionEnvs resolves one companion block's non-overridden
// environment variables, returning (nil, false) and logging an error when any
// remaining key references a secret this namespace has no value for.
func resolveCompanionEnvs(
	ctx *NsGenContext,
	appName, companion string,
	props bundle.WebappCompanionProps,
	nsEnv map[string]string,
) (map[string]string, bool) {
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
		slog.Error("Companion settings not applied: a secret they reference has no value in this namespace",
			"app", appName, "companion", companion, "secrets", slices.Compact(missing))
		return nil, false
	}
	return resolved, true
}

// companionSurvives reports whether companion and every app in its transitive
// dependsOn closure exist in ctx.Applications and carry no unresolved secret
// references (i.e. the companion will survive excludeAppsMissingSecrets and
// pruneAppsWithMissingDeps).
func companionSurvives(ctx *NsGenContext, companion string) bool {
	visited := make(map[string]bool)
	var walk func(string) bool
	walk = func(name string) bool {
		if visited[name] {
			return true
		}
		visited[name] = true
		b, ok := ctx.Applications[name]
		if !ok {
			return false
		}
		if len(unresolvedSecretRefs(b, ctx.CloudConfig[name])) > 0 {
			return false
		}
		for _, dep := range b.DependsOn {
			if !walk(dep) {
				return false
			}
		}
		return true
	}
	return walk(companion)
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
		for _, next := range b.DependsOn {
			if walk(next) {
				return true
			}
		}
		return false
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
