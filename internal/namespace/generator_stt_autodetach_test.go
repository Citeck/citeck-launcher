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

// TestSttSidecar_GeneratedButAutoDetachedWhenAiIsDetached mirrors the rag→qdrant
// rule: a detached ai no longer deletes its sidecar. The spec stays so the
// sidecar can be started on its own — an ai run from an IDE still needs speech
// recognition on localhost — and the generator names it AUTO-DETACHED so the
// runtime never starts it by itself.
func TestSttSidecar_GeneratedButAutoDetachedWhenAiIsDetached(t *testing.T) {
	config.ResetDesktopMode()
	resp, err := Generate(basicCfg(), aiSttBundle(), aiSttWorkspace(),
		SystemSecrets{JWT: "j", OIDC: "o"},
		GenerateOpts{DetachedApps: map[string]bool{appdef.AppAi: true}})
	require.NoError(t, err)

	require.NotNil(t, findGeneratedApp(resp, appdef.AppSttSidecar),
		"спека сайдкара остаётся, иначе локальному ai не к чему подключаться")
	assert.True(t, resp.AutoDetachedApps[appdef.AppSttSidecar],
		"иначе сайдкар стартанёт сам на каждом стенде с выключенным ai")
}

// TestSttSidecar_NotAutoDetachedWhenAiIsAttached is the other half.
func TestSttSidecar_NotAutoDetachedWhenAiIsAttached(t *testing.T) {
	config.ResetDesktopMode()
	resp, err := Generate(basicCfg(), aiSttBundle(), aiSttWorkspace(), SystemSecrets{JWT: "j", OIDC: "o"})
	require.NoError(t, err)
	require.NotNil(t, findGeneratedApp(resp, appdef.AppSttSidecar))
	assert.False(t, resp.AutoDetachedApps[appdef.AppSttSidecar])
}
