package namespace

import (
	"encoding/json"
	"testing"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Exercise the full generator: workspace defaults used to overwrite a webapp
// after its bundle image had already been assigned, while each helper app had
// its own conflicting order. Checking just the resolver would miss that bug.
func TestGeneratedImagesRespectBundleAndExplicitEdit(t *testing.T) {
	wasDesktop := config.IsDesktopMode()
	config.SetDesktopMode(true)
	t.Cleanup(func() { config.SetDesktopMode(wasDesktop) })

	for _, source := range []string{"applications", "dependencies", "fallback", "edit"} {
		t.Run(source, func(t *testing.T) {
			cfg := depsTestConfig()
			cfg.Proxy.Image = "namespace/proxy:1"
			cfg.PgAdmin.Image = "namespace/pgadmin:1"
			cfg.MongoDB.Image = "namespace/mongo:1"
			cfg.Observer.Enabled = true
			cfg.Observer.Image = "namespace/observer:1"
			cfg.Webapps = map[string]WebappProps{
				appdef.AppAi: {Image: "namespace/ai:1"},
			}
			ws := &bundle.WorkspaceConfig{
				DefaultWebappProps: bundle.WebappDefaultProps{Image: "workspace/global:1"},
				Webapps: []bundle.WebappConfig{
					{ID: appdef.AppGateway},
					{ID: appdef.AppAi, DefaultProps: bundle.WebappDefaultProps{Image: "workspace/ai:1"}},
				},
				PgAdmin:    bundle.PgAdminWsProps{Image: "workspace/pgadmin:1"},
				SttSidecar: &bundle.SttSidecarProps{Image: "workspace/stt:1"},
			}
			bun := &bundle.Def{Applications: map[string]bundle.AppDef{
				appdef.AppGateway: {Image: "citeck/gateway:1"},
				appdef.AppAi:      {},
			}, Dependencies: map[string]bundle.AppDef{}}
			fallbacks := map[string]string{
				appdef.AppAi:         "namespace/ai:1",
				appdef.AppProxy:      "namespace/proxy:1",
				appdef.AppPgadmin:    "namespace/pgadmin:1",
				appdef.AppMongodb:    "namespace/mongo:1",
				appdef.AppObserver:   "namespace/observer:1",
				appdef.AppSttSidecar: "workspace/stt:1",
			}
			for name := range fallbacks {
				if source != "fallback" {
					bun.Applications[name] = bundle.AppDef{Image: "bundle/" + name + ":1"}
				}
				if source == "dependencies" {
					bun.Dependencies[name] = bundle.AppDef{Image: "section/" + name + ":1"}
				}
			}
			opts := GenerateOpts{}
			if source == "edit" {
				opts.EditedAppPatches = map[string]json.RawMessage{
					appdef.AppPgadmin: json.RawMessage(`{"image":"edited/pgadmin:1"}`),
				}
			}
			resp, err := Generate(cfg, bun, ws, SystemSecrets{JWT: "j", OIDC: "o"}, opts)
			require.NoError(t, err)
			for name, fallback := range fallbacks {
				want := "bundle/" + name + ":1"
				switch source {
				case "dependencies":
					want = "section/" + name + ":1"
				case "fallback":
					want = fallback
				case "edit":
					if name == appdef.AppPgadmin {
						want = "edited/pgadmin:1"
					}
				}
				app := findAppByName(resp.Applications, name)
				require.NotNil(t, app, name)
				assert.Equal(t, want, app.Image, name)
			}
		})
	}
}
