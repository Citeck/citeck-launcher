package namespace

import (
	"testing"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func ownerWiringBundle() *bundle.Def {
	return &bundle.Def{Applications: map[string]bundle.AppDef{
		appdef.AppAi:         {Image: "citeck/ai:1.0.0"},
		appdef.AppRag:        {Image: "citeck/rag:1.0.0"},
		appdef.AppQdrant:     {Image: "qdrant/qdrant:v1.14.1"},
		appdef.AppSttSidecar: {Image: "citeck/stt-sidecar:1.0.0"},
	}}
}

func ownerWiringWorkspace() *bundle.WorkspaceConfig {
	return &bundle.WorkspaceConfig{Webapps: []bundle.WebappConfig{
		{ID: appdef.AppAi}, {ID: appdef.AppRag},
	}}
}

// TestCompanionGeneratorsDoNotWriteIntoTheirOwners pins the direction of the
// wiring: an app's env and dependencies are written by the code that generates
// THAT app. The sidecar and the vector store are generated without touching ai
// or rag; the owners read whether their companions exist and wire themselves.
// Before, CITECK_AI_RAG_ENABLED was set by the qdrant generator and the STT URL
// by the sidecar's, so what ai was told could only be found by reading the
// generators of other apps.
func TestCompanionGeneratorsDoNotWriteIntoTheirOwners(t *testing.T) {
	config.ResetDesktopMode()
	ctx := NewNsGenContext(basicCfg(), ownerWiringBundle())
	ctx.WorkspaceConfig = ownerWiringWorkspace()
	ai := ctx.GetOrCreateApp(appdef.AppAi)
	rag := ctx.GetOrCreateApp(appdef.AppRag)

	generateSttSidecar(ctx)
	generateQdrant(ctx)

	require.NotNil(t, ctx.Applications[appdef.AppSttSidecar], "the sidecar itself is generated")
	require.NotNil(t, ctx.Applications[appdef.AppQdrant], "the store itself is generated")
	assert.Zero(t, ai.Environments.Len(), "ai's env belongs to ai's own wiring")
	assert.Empty(t, ai.DependsOn, "ai's dependencies belong to ai's own wiring")
	assert.Zero(t, rag.Environments.Len(), "rag's env belongs to rag's own wiring")
	assert.Empty(t, rag.DependsOn, "rag's dependencies belong to rag's own wiring")
}

// TestOwnersWireThemselves is the other half: after the move the generated
// namespace carries exactly the wiring it carried before.
func TestOwnersWireThemselves(t *testing.T) {
	config.ResetDesktopMode()
	gen := func(t *testing.T, detached map[string]bool) *GenResp {
		t.Helper()
		resp, err := Generate(basicCfg(), ownerWiringBundle(), ownerWiringWorkspace(),
			SystemSecrets{JWT: "j", OIDC: "o"}, GenerateOpts{DetachedApps: detached})
		require.NoError(t, err)
		return resp
	}
	env := func(app *appdef.ApplicationDef, key string) (string, bool) {
		v, ok := app.Environments.Get(key)
		return v, ok
	}

	t.Run("everything attached", func(t *testing.T) {
		resp := gen(t, nil)
		ai := findGeneratedApp(resp, appdef.AppAi)
		rag := findGeneratedApp(resp, appdef.AppRag)
		require.NotNil(t, ai)
		require.NotNil(t, rag)

		v, ok := env(ai, "CITECK_AI_CALLRECORDING_STT_SIDECARURL")
		assert.True(t, ok)
		assert.Equal(t, "http://stt-sidecar:14080", v)
		assert.Contains(t, ai.DependsOn, appdef.AppSttSidecar)
		v, ok = env(ai, "CITECK_AI_RAG_ENABLED")
		assert.True(t, ok)
		assert.Equal(t, "true", v)

		v, ok = env(rag, "QDRANT_HOST")
		assert.True(t, ok)
		assert.Equal(t, appdef.AppQdrant, v)
		v, ok = env(rag, "QDRANT_GRPC_PORT")
		assert.True(t, ok)
		assert.Equal(t, "6334", v)
		assert.Contains(t, rag.DependsOn, appdef.AppQdrant)

		assert.True(t, resp.GatingApps[appdef.AppAi])
		assert.True(t, resp.GatingApps[appdef.AppSttSidecar])
	})

	t.Run("sidecar detached", func(t *testing.T) {
		resp := gen(t, map[string]bool{appdef.AppSttSidecar: true})
		ai := findGeneratedApp(resp, appdef.AppAi)
		require.NotNil(t, ai)
		_, ok := env(ai, "CITECK_AI_CALLRECORDING_STT_SIDECARURL")
		assert.False(t, ok, "ai must not be pointed at a sidecar that is not running")
		assert.NotContains(t, ai.DependsOn, appdef.AppSttSidecar, "nor wait for it")
		assert.NotNil(t, findGeneratedApp(resp, appdef.AppSttSidecar), "the sidecar's spec stays")
		assert.True(t, resp.GatingApps[appdef.AppSttSidecar], "re-attaching it must rewire ai")
	})

	t.Run("ai detached", func(t *testing.T) {
		resp := gen(t, map[string]bool{appdef.AppAi: true})
		ai := findGeneratedApp(resp, appdef.AppAi)
		require.NotNil(t, ai)
		_, ok := env(ai, "CITECK_AI_RAG_ENABLED")
		assert.False(t, ok, "unchanged: the flag is withheld from a detached ai")
		assert.True(t, resp.GatingApps[appdef.AppAi])
	})

	t.Run("qdrant detached", func(t *testing.T) {
		resp := gen(t, map[string]bool{appdef.AppQdrant: true})
		rag := findGeneratedApp(resp, appdef.AppRag)
		require.NotNil(t, rag)
		assert.Contains(t, rag.DependsOn, appdef.AppQdrant,
			"unchanged: rag without its store is not a smaller rag, it waits")
	})

	t.Run("no store in the bundle", func(t *testing.T) {
		bun := ownerWiringBundle()
		delete(bun.Applications, appdef.AppQdrant)
		resp, err := Generate(basicCfg(), bun, ownerWiringWorkspace(), SystemSecrets{JWT: "j", OIDC: "o"})
		require.NoError(t, err)
		ai := findGeneratedApp(resp, appdef.AppAi)
		rag := findGeneratedApp(resp, appdef.AppRag)
		require.NotNil(t, ai)
		require.NotNil(t, rag)
		_, ok := env(ai, "CITECK_AI_RAG_ENABLED")
		assert.False(t, ok, "unchanged: no store, no RAG namespace")
		_, ok = env(rag, "QDRANT_HOST")
		assert.False(t, ok)
		assert.NotContains(t, rag.DependsOn, appdef.AppQdrant)
	})
}
