package namespace

import (
	"log/slog"
	"strings"

	"github.com/citeck/citeck-launcher/internal/appdef"
)

// generateAdditionalApps materializes the workspace config's AdditionalApps into
// ApplicationDefs. This is the generic, config-driven path: any custom container can
// be added by configuration alone, without a dedicated per-service generator. The
// apps are declared once in the workspace config (workspace-v1.yml) and applied to
// every namespace that uses the workspace, live on each generation — like webapps.
// Each enabled entry becomes an app spanning every container-level ApplicationDef knob
// — image, env, cmd, ports, volumes, dependencies, init containers, init actions,
// probes, resources, shmSize and stopTimeout — with ${VAR} template resolution in
// every string the user supplies (env, cmd, init-action exec, init-container env/cmd).
// Kind defaults to THIRD_PARTY.
//
// In server mode the shared port-stripping pass in Generate makes these apps internal
// to the Docker network (reachable by Name/NetworkAliases) — so a service like the EDI
// simulator self-registers in ZooKeeper and is discovered by the platform by name.
func generateAdditionalApps(ctx *NsGenContext) {
	if ctx.WorkspaceConfig == nil {
		return
	}
	for _, def := range ctx.WorkspaceConfig.AdditionalApps {
		// A TYPED entry (POSTGRES, QDRANT) has its own generator and is never a
		// raw container. Skipped explicitly: with the image no longer required,
		// it would otherwise reach the collision guard below and be refused as a
		// clash with the very cluster its own generator emitted.
		if !def.IsEnabled() || def.IsTyped() {
			continue
		}
		name := strings.TrimSpace(def.Name)
		if name == "" {
			// Defensive: validation rejects these at workspace-config load; skip
			// rather than emit a broken app.
			continue
		}
		// The image the BUNDLE names for the entry wins, and the entry's own
		// image is only the default — the same precedence a typed entry and a
		// built-in service take. An entry without an image runs only where the
		// bundle names one: the release decides that the service exists, the
		// workspace how it is configured.
		image := resolveAppImage(ctx, name, "", ctx.WorkspaceConfig.ResolveImageRef(def.Image))
		if image == "" {
			slog.Debug("additionalApps entry names no image and the bundle names none; not generated", "name", name)
			continue
		}

		// Collision guard: by this point ctx.Applications already holds every built-in
		// app (infra, keycloak, bundle webapps, sidecars). A name matching one would
		// make GetOrCreateApp return that app's builder and silently overwrite its
		// image/kind/env — corrupting a real platform container. The static
		// reservedAppNames check in ValidateAdditionalApps cannot see bundle-loaded
		// webapp IDs (edi, integrations, enterprise apps …), so guard here where the
		// full app set is known: skip (never overwrite) and log loudly.
		//
		// isBuiltInApp also covers the proxy and onlyoffice, whose generators run
		// AFTER this one: they have no builder yet, so an entry naming one would
		// SEED it instead of overwriting it, and the built-in generator would then
		// leave every field it does not set (aliases, cmd, shmSize, init
		// containers, stray env) on the real container.
		if isBuiltInApp(ctx, name) {
			slog.Error("additionalApps entry collides with a built-in app; skipping to avoid overwriting it", "name", name)
			continue
		}

		app := ctx.GetOrCreateApp(name)
		app.Image = image
		app.Kind = additionalAppKind(def.Kind)
		app.NetworkAliases = append(app.NetworkAliases, def.NetworkAliases...)
		app.Cmd = resolveTemplateVarsSlice(def.Cmd, ctx)
		app.ShmSize = def.ShmSize
		app.Resources = def.Resources
		app.LivenessProbe = def.LivenessProbe
		app.StartupConditions = def.StartupConditions
		app.StopTimeout = def.StopTimeout
		app.InitContainers = resolveInitContainers(def.InitContainers, ctx)
		app.InitActions = resolveInitActions(def.InitActions, ctx)

		// Env in deterministic order, context-aware resolution: infra hosts/ports
		// (${ZK_HOST} …) plus platform secrets / web URL (${JWT_SECRET}, ${WEB_URL},
		// ${RMQ_USER}/${RMQ_PASSWORD}, ${OIDC_SECRET}, ${KK_*}, ${ADMIN_PASSWORD}).
		for _, k := range sortedKeys(def.Environments) {
			app.AddEnv(k, resolveTemplateVarsWithContext(def.Environments[k], ctx))
		}
		for _, p := range def.Ports {
			app.AddPort(p)
		}
		for _, v := range def.Volumes {
			app.AddVolume(v)
		}
		for _, d := range def.DependsOn {
			app.AddDependsOn(d)
		}
		if len(def.CloudConfig) > 0 {
			ctx.CloudConfig[name] = resolveCloudConfig(def.CloudConfig, ctx)
		}
	}
}

// resolveCloudConfig applies the env substitution to every string in a cloud
// config, at any depth, and returns a copy; other values pass through.
func resolveCloudConfig(in map[string]any, ctx *NsGenContext) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = resolveCloudConfigValue(v, ctx)
	}
	return out
}

func resolveCloudConfigValue(v any, ctx *NsGenContext) any {
	switch t := v.(type) {
	case string:
		return resolveTemplateVarsWithContext(t, ctx)
	case map[string]any:
		return resolveCloudConfig(t, ctx)
	case []any:
		out := make([]any, len(t))
		for i, x := range t {
			out[i] = resolveCloudConfigValue(x, ctx)
		}
		return out
	}
	return v
}

// resolveTemplateVarsSlice resolves ${VAR} (context-aware) in every element of a
// string slice, returning nil for an empty input so an unset cmd stays nil.
func resolveTemplateVarsSlice(in []string, ctx *NsGenContext) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, len(in))
	for i, s := range in {
		out[i] = resolveTemplateVarsWithContext(s, ctx)
	}
	return out
}

// resolveInitActions resolves ${VAR} in each init action's exec args.
func resolveInitActions(in []appdef.AppInitAction, ctx *NsGenContext) []appdef.AppInitAction {
	if len(in) == 0 {
		return nil
	}
	out := make([]appdef.AppInitAction, len(in))
	for i, a := range in {
		out[i] = appdef.AppInitAction{Exec: resolveTemplateVarsSlice(a.Exec, ctx)}
	}
	return out
}

// resolveInitContainers resolves ${VAR} (context-aware) in each init container's
// environments and cmd (preserving env key order) and rewrites the image through
// the workspace imageRepos (so an init container can use "core/foo:1.1" too).
// Volumes and kind pass through verbatim — they aren't template-resolved
// anywhere else either.
func resolveInitContainers(in []appdef.InitContainerDef, ctx *NsGenContext) []appdef.InitContainerDef {
	if len(in) == 0 {
		return nil
	}
	out := make([]appdef.InitContainerDef, len(in))
	for i, ic := range in {
		resolved := ic
		resolved.Image = ctx.WorkspaceConfig.ResolveImageRef(ic.Image)
		resolved.Cmd = resolveTemplateVarsSlice(ic.Cmd, ctx)
		if ic.Environments.Len() > 0 {
			var env appdef.OrderedMap
			for _, e := range ic.Environments {
				env.Set(e.Key, resolveTemplateVarsWithContext(e.Value, ctx))
			}
			resolved.Environments = env
		}
		out[i] = resolved
	}
	return out
}

// additionalAppKind resolves the configured kind string, defaulting to THIRD_PARTY.
func additionalAppKind(kind string) appdef.ApplicationKind {
	if strings.TrimSpace(kind) == "" {
		return appdef.KindThirdParty
	}
	return appdef.ParseApplicationKind(kind)
}
