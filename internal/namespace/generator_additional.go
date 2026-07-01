package namespace

import (
	"log/slog"
	"strings"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/bundle"
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
		if !def.IsEnabled() {
			continue
		}
		name := strings.TrimSpace(def.Name)
		if name == "" || strings.TrimSpace(def.Image) == "" {
			// Defensive: validation rejects these at workspace-config load; skip
			// rather than emit a broken app.
			continue
		}

		// Collision guard: by this point ctx.Applications already holds every built-in
		// app (infra, keycloak, bundle webapps, sidecars). A name matching one would
		// make GetOrCreateApp return that app's builder and silently overwrite its
		// image/kind/env — corrupting a real platform container. The static
		// reservedAppNames check in ValidateAdditionalApps cannot see bundle-loaded
		// webapp IDs (edi, integrations, enterprise apps …), so guard here where the
		// full app set is known: skip (never overwrite) and log loudly.
		if _, exists := ctx.Applications[name]; exists {
			slog.Error("additionalApps entry collides with a built-in app; skipping to avoid overwriting it", "name", name)
			continue
		}

		app := ctx.GetOrCreateApp(name)
		app.Image = ctx.WorkspaceConfig.ResolveImageRef(def.Image)
		app.Kind = additionalAppKind(def.Kind)
		app.NetworkAliases = append(app.NetworkAliases, def.NetworkAliases...)
		app.Cmd = resolveTemplateVarsSlice(def.Cmd)
		app.ShmSize = def.ShmSize
		app.Resources = def.Resources
		app.LivenessProbe = def.LivenessProbe
		app.StartupConditions = def.StartupConditions
		app.StopTimeout = def.StopTimeout
		app.InitContainers = resolveInitContainers(def.InitContainers, ctx.WorkspaceConfig)
		app.InitActions = resolveInitActions(def.InitActions)

		// Env in deterministic order, with template-var resolution (${ZK_HOST} etc.).
		for _, k := range sortedKeys(def.Environments) {
			app.AddEnv(k, resolveTemplateVars(def.Environments[k]))
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
	}
}

// resolveTemplateVarsSlice resolves ${VAR} in every element of a string slice,
// returning nil for an empty input so an unset cmd stays nil (not []string{}).
func resolveTemplateVarsSlice(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, len(in))
	for i, s := range in {
		out[i] = resolveTemplateVars(s)
	}
	return out
}

// resolveInitActions resolves ${VAR} in each init action's exec args.
func resolveInitActions(in []appdef.AppInitAction) []appdef.AppInitAction {
	if len(in) == 0 {
		return nil
	}
	out := make([]appdef.AppInitAction, len(in))
	for i, a := range in {
		out[i] = appdef.AppInitAction{Exec: resolveTemplateVarsSlice(a.Exec)}
	}
	return out
}

// resolveInitContainers resolves ${VAR} in each init container's environments
// and cmd (preserving env key order) and rewrites the image through the
// workspace imageRepos (so an init container can use "core/foo:1.1" too).
// Volumes and kind pass through verbatim — they aren't template-resolved
// anywhere else either.
func resolveInitContainers(in []appdef.InitContainerDef, wsCfg *bundle.WorkspaceConfig) []appdef.InitContainerDef {
	if len(in) == 0 {
		return nil
	}
	out := make([]appdef.InitContainerDef, len(in))
	for i, ic := range in {
		resolved := ic
		resolved.Image = wsCfg.ResolveImageRef(ic.Image)
		resolved.Cmd = resolveTemplateVarsSlice(ic.Cmd)
		if ic.Environments.Len() > 0 {
			var env appdef.OrderedMap
			for _, e := range ic.Environments {
				env.Set(e.Key, resolveTemplateVars(e.Value))
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
