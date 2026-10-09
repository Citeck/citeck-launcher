package namespace

import (
	"github.com/citeck/citeck-launcher/internal/appdef"
	"strings"

	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/deps"
)

// SeaweedDescriptor describes a SEAWEEDFS entry; other types return nil.
func SeaweedDescriptor(a bundle.AdditionalAppProps) deps.Descriptor {
	if a.Type != bundle.AppTypeSeaweedFS {
		return nil
	}
	return deps.NewSeaweedDescriptor(deps.ID(strings.TrimSpace(a.Name)), strings.TrimSpace(a.Name), a.SeaweedVolumeBase())
}

// SeaweedDependencies lists enabled stores for workspace registry installation.
func SeaweedDependencies(ws *bundle.WorkspaceConfig) []deps.Descriptor {
	var out []deps.Descriptor
	if ws != nil {
		for _, a := range ws.AdditionalApps {
			if a.IsEnabled() {
				if d := SeaweedDescriptor(a); d != nil {
					out = append(out, d)
				}
			}
		}
	}
	return out
}

// WillGenerateSeaweed is the seeding counterpart of the additional-app generator.
func WillGenerateSeaweed(bun *bundle.Def, ws *bundle.WorkspaceConfig) map[deps.ID]bool {
	out := map[deps.ID]bool{}
	if ws != nil {
		for _, a := range ws.AdditionalApps {
			if d := SeaweedDescriptor(a); d != nil {
				out[d.ID()] = a.IsEnabled() && additionalAppImage(bun, ws, a) != ""
			}
		}
	}
	return out
}

// Preserve an explicit host mapping instead of publishing the same service twice.
func addSeaweedPort(app *AppBuilder, port, fallback string) {
	for _, raw := range app.Ports {
		if spec, err := appdef.ParsePortSpec(raw); err == nil && spec.ContainerPort == port && spec.Protocol == "tcp" {
			return
		}
	}
	app.AddPort(fallback)
}
