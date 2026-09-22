package namespace

import (
	"testing"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func aiSttBundle() *bundle.Def {
	return &bundle.Def{Applications: map[string]bundle.AppDef{
		appdef.AppAi:         {Image: "citeck/ai:1.0.0"},
		appdef.AppSttSidecar: {Image: "citeck/stt-sidecar:1.0.0"},
	}}
}

func aiSttWorkspace() *bundle.WorkspaceConfig {
	return &bundle.WorkspaceConfig{Webapps: []bundle.WebappConfig{{ID: appdef.AppAi}}}
}

// TestSttSidecar_SpecStaysWhenAiIsDetached mirrors the rag→qdrant rule: a
// detached ai no longer deletes its sidecar, because stopping ai in the
// launcher is how it gets run from an IDE and the sidecar is exactly what the
// locally run ai still has to reach. Since the auto-detach verdict was removed
// the sidecar is an ORDINARY app either way — it starts with the namespace, and
// a stand that should not pay its 2g stops it (the shipped workspace template
// lists it in detachedApps next to ai).
func TestSttSidecar_SpecStaysWhenAiIsDetached(t *testing.T) {
	config.ResetDesktopMode()
	resp, err := Generate(basicCfg(), aiSttBundle(), aiSttWorkspace(),
		SystemSecrets{JWT: "j", OIDC: "o"},
		GenerateOpts{DetachedApps: map[string]bool{appdef.AppAi: true}})
	require.NoError(t, err)

	require.NotNil(t, findGeneratedApp(resp, appdef.AppSttSidecar),
		"the sidecar's spec stays, or a locally run ai has nothing to reach")
}

// TestSttSidecar_AiStaysGating is the half of the old wiring that is still
// load-bearing without the verdict: ai's detach state DOES change the
// generation — the proxy drops its AI upstream (generator_proxy.go) and
// generateQdrant withholds CITECK_AI_RAG_ENABLED — so toggling ai must
// regenerate the namespace.
func TestSttSidecar_AiStaysGating(t *testing.T) {
	config.ResetDesktopMode()
	for name, detached := range map[string]map[string]bool{
		"attached": nil,
		"detached": {appdef.AppAi: true},
	} {
		t.Run(name, func(t *testing.T) {
			resp, err := Generate(basicCfg(), aiSttBundle(), aiSttWorkspace(),
				SystemSecrets{JWT: "j", OIDC: "o"},
				GenerateOpts{DetachedApps: detached})
			require.NoError(t, err)
			assert.True(t, resp.GatingApps[appdef.AppAi],
				"a proxy upstream and the RAG flag both follow ai's detach state")
		})
	}
}
