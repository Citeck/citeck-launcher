package namespace

import (
	"log/slog"
	"slices"
	"sort"

	"github.com/citeck/citeck-launcher/internal/bundle"
)

// wireWorkspaceCompanions applies the workspace's webapps[].companions: the env
// and dependencies a webapp receives only while a named companion app is part
// of the namespace. It is the configuration-side counterpart of wireAi and
// wireRag — the workspace, not the launcher, knows the property names a
// service reads, so wiring ai and rag to an optional gateway needs no code that
// names the gateway.
//
// It runs on the FINAL generated set: after excludeAppsMissingSecrets and
// pruneAppsWithMissingDeps. Earlier, a companion that is about to be removed
// (its own secret has no value, its database was not generated) would already
// sit in the webapp's dependsOn, and the prune would take the webapp down with
// it. Applied afterwards, a companion that did not make it into the namespace
// leaves the webapp exactly as it is without the entry.
//
// Precedence of an env key: the namespace's own webapps[<id>].environments win
// (namespace.yml is the operator's file), then the companion block, then the
// workspace defaultProps. A `citeck edit` patch still overlays all of them.
//
// A block that references a `${secret:<id>}` with no value in this namespace is
// skipped WHOLE and logged. The webapp keeps running on its own configuration
// instead of being excluded: losing ai because a gateway key is missing would
// turn an optional integration into an outage.
func wireWorkspaceCompanions(ctx *NsGenContext) {
	if ctx.WorkspaceConfig == nil {
		return
	}
	for _, ws := range ctx.WorkspaceConfig.Webapps {
		if len(ws.Companions) == 0 {
			continue
		}
		app, ok := ctx.Applications[ws.ID]
		if !ok {
			continue
		}
		nsEnv := ctx.Config.Webapps[ws.ID].Environments
		for _, companion := range sortedCompanionNames(ws.Companions) {
			if companion == ws.ID {
				slog.Warn("Workspace webapp lists itself as a companion; ignored", "app", ws.ID)
				continue
			}
			if _, present := ctx.Applications[companion]; !present {
				continue
			}
			applyCompanion(ctx, app, ws.ID, companion, ws.Companions[companion], nsEnv)
		}
	}
}

// applyCompanion writes one companion block into its webapp, or nothing at all
// when the block references a secret this namespace has no value for.
func applyCompanion(ctx *NsGenContext, app *AppBuilder, appName, companion string,
	props bundle.WebappCompanionProps, nsEnv map[string]string,
) {
	resolved := make(map[string]string, len(props.Environments))
	var missing []string
	for k, v := range props.Environments {
		val := resolveTemplateVarsWithContext(v, ctx)
		for _, m := range secretRefPattern.FindAllStringSubmatch(val, -1) {
			missing = append(missing, m[1])
		}
		resolved[k] = val
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		slog.Error("Companion settings not applied: a secret they reference has no value in this namespace",
			"app", appName, "companion", companion, "secrets", slices.Compact(missing))
		return
	}
	for _, k := range sortedKeys(resolved) {
		if _, own := nsEnv[k]; own {
			continue
		}
		app.AddEnv(k, resolved[k])
	}
	for _, dep := range props.DependsOn {
		if dep == appName {
			continue
		}
		if _, ok := ctx.Applications[dep]; ok {
			app.AddDependsOn(dep)
		}
	}
}

func sortedCompanionNames(m map[string]bundle.WebappCompanionProps) []string {
	names := make([]string, 0, len(m))
	for k := range m {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}
