package namespace

import (
	"strings"

	"github.com/citeck/citeck-launcher/internal/appdef"
)

// generateAdditionalApps materializes the namespace config's AdditionalApps into
// ApplicationDefs. This is the generic, config-driven path: any custom container can
// be added by configuration alone, without a dedicated per-service generator. Each
// enabled entry becomes an app with its image, env (with ${VAR} template resolution),
// ports, volumes, dependencies, probes and resources. Kind defaults to THIRD_PARTY.
//
// In server mode the shared port-stripping pass in Generate makes these apps internal
// to the Docker network (reachable by Name/NetworkAliases) — so a service like the EDI
// simulator self-registers in ZooKeeper and is discovered by the platform by name.
func generateAdditionalApps(ctx *NsGenContext) {
	for _, def := range ctx.Config.AdditionalApps {
		if !def.IsEnabled() {
			continue
		}
		name := strings.TrimSpace(def.Name)
		if name == "" || strings.TrimSpace(def.Image) == "" {
			// Defensive: validation rejects these earlier; skip rather than emit a broken app.
			continue
		}

		app := ctx.GetOrCreateApp(name)
		app.Image = def.Image
		app.Kind = additionalAppKind(def.Kind)
		app.NetworkAliases = append(app.NetworkAliases, def.NetworkAliases...)
		app.Cmd = def.Cmd
		app.ShmSize = def.ShmSize
		app.Resources = def.Resources
		app.LivenessProbe = def.LivenessProbe
		app.StartupConditions = def.StartupConditions

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

// additionalAppKind resolves the configured kind string, defaulting to THIRD_PARTY.
func additionalAppKind(kind string) appdef.ApplicationKind {
	if strings.TrimSpace(kind) == "" {
		return appdef.KindThirdParty
	}
	return appdef.ParseApplicationKind(kind)
}
