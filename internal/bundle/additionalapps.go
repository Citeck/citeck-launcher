package bundle

import (
	"fmt"
	"strings"

	"github.com/citeck/citeck-launcher/internal/appdef"
)

// AdditionalAppProps declares a custom container to run in a namespace alongside
// the built-in Citeck/infra apps, with no dedicated launcher generator. It lives
// in the workspace config (workspace-v1.yml `additionalApps:`) so a service is
// defined once and distributed to every namespace that uses the workspace — applied
// live on each generation, exactly like `webapps:`.
type AdditionalAppProps struct {
	// Name is the container/app name (unique; must not collide with a built-in app).
	Name string `yaml:"name" json:"name"`
	// Enabled defaults to true; set false to keep the definition but not deploy it.
	Enabled *bool `yaml:"enabled,omitempty" json:"enabled,omitempty"`
	// Image is the full Docker image reference to run (registry/repo:tag, or a
	// locally-present tag), or a bundle-style "<repoId>/path:tag" resolved through
	// the workspace imageRepos. Pulled like any other app; registry auth comes from
	// the workspace imageRepos by host match.
	Image string `yaml:"image" json:"image"`
	// Kind classifies the app (CITECK_CORE / CITECK_CORE_EXTENSION / CITECK_ADDITIONAL
	// / THIRD_PARTY); empty defaults to THIRD_PARTY.
	Kind              string                    `yaml:"kind,omitempty" json:"kind,omitempty"`
	NetworkAliases    []string                  `yaml:"networkAliases,omitempty" json:"networkAliases,omitempty"`
	Environments      map[string]string         `yaml:"environments,omitempty" json:"environments,omitempty"`
	Cmd               []string                  `yaml:"cmd,omitempty" json:"cmd,omitempty"`
	Ports             []string                  `yaml:"ports,omitempty" json:"ports,omitempty"`
	Volumes           []string                  `yaml:"volumes,omitempty" json:"volumes,omitempty"`
	DependsOn         []string                  `yaml:"dependsOn,omitempty" json:"dependsOn,omitempty"`
	StartupConditions []appdef.StartupCondition `yaml:"startupConditions,omitempty" json:"startupConditions,omitempty"`
	LivenessProbe     *appdef.AppProbeDef       `yaml:"livenessProbe,omitempty" json:"livenessProbe,omitempty"`
	Resources         *appdef.AppResourcesDef   `yaml:"resources,omitempty" json:"resources,omitempty"`
	ShmSize           string                    `yaml:"shmSize,omitempty" json:"shmSize,omitempty"`
	// InitContainers run to completion before the main container starts (a
	// wait-for, a schema migration, a fixture loader). Each is a full
	// InitContainerDef (image + env + volumes + cmd); ${VAR} is resolved in env
	// and cmd just like the main container.
	InitContainers []appdef.InitContainerDef `yaml:"initContainers,omitempty" json:"initContainers,omitempty"`
	// InitActions are exec commands run inside the container right after it is
	// created (e.g. createbucket, a one-off CLI call). ${VAR} is resolved in args.
	InitActions []appdef.AppInitAction `yaml:"initActions,omitempty" json:"initActions,omitempty"`
	// StopTimeout is the per-app graceful-stop budget in seconds (SIGTERM→SIGKILL
	// window); 0 falls back to the daemon default.
	StopTimeout int `yaml:"stopTimeout,omitempty" json:"stopTimeout,omitempty"`
}

// IsEnabled reports whether the additional app should be deployed (default true).
func (a AdditionalAppProps) IsEnabled() bool {
	return a.Enabled == nil || *a.Enabled
}

// reservedAppNames are the built-in infra/core container names an additional app
// must not reuse (reusing one would override that built-in app's definition). This
// is the static fast-reject list; collisions with bundle-loaded webapp ids (edi,
// integrations, …) are caught at generation time where the full app set is known.
var reservedAppNames = map[string]bool{
	appdef.AppProxy: true, appdef.AppGateway: true, appdef.AppEapps: true,
	appdef.AppEmodel: true, appdef.AppUiserv: true, appdef.AppHistory: true,
	appdef.AppNotifications: true, appdef.AppTransformations: true, appdef.AppEproc: true,
	appdef.AppPostgres: true, appdef.AppZookeeper: true, appdef.AppRabbitmq: true,
	appdef.AppMongodb: true, appdef.AppMailpit: true, appdef.AppKeycloak: true,
	appdef.AppPgadmin: true, appdef.AppOnlyoffice: true, appdef.AppAlfresco: true,
	appdef.AppAlfPostgres: true, appdef.AppAlfSolr: true, appdef.AppObserver: true,
	appdef.AppObsPostgres: true, appdef.AppContent: true, appdef.AppAi: true,
	appdef.AppSttSidecar: true,
}

// ValidateAdditionalApps checks each additional app has a name + image, names are
// unique, and they do not collide with a reserved built-in container name.
func ValidateAdditionalApps(apps []AdditionalAppProps) error {
	seen := make(map[string]bool, len(apps))
	for i, a := range apps {
		name := strings.TrimSpace(a.Name)
		if name == "" {
			return fmt.Errorf("additionalApps[%d]: name is required", i)
		}
		if strings.TrimSpace(a.Image) == "" {
			return fmt.Errorf("additionalApps[%q]: image is required", name)
		}
		if reservedAppNames[name] {
			return fmt.Errorf("additionalApps[%q]: name collides with a built-in app; choose another", name)
		}
		if seen[name] {
			return fmt.Errorf("additionalApps[%q]: duplicate name", name)
		}
		seen[name] = true
		for j, ic := range a.InitContainers {
			if strings.TrimSpace(ic.Image) == "" {
				return fmt.Errorf("additionalApps[%q].initContainers[%d]: image is required", name, j)
			}
		}
		if a.StopTimeout < 0 {
			return fmt.Errorf("additionalApps[%q]: stopTimeout must be >= 0", name)
		}
	}
	return nil
}
