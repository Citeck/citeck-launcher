package namespace

import (
	"testing"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func ragBundle() *bundle.Def {
	return &bundle.Def{Applications: map[string]bundle.AppDef{
		"rag":    {Image: "harbor.citeck.ru/enterprise/citeck-rag:1.2.2"},
		"qdrant": {Image: "qdrant/qdrant:v1.14.1"},
		"ai":     {Image: "harbor.citeck.ru/enterprise/ai:1.12.0"},
	}}
}

func ragWorkspace() *bundle.WorkspaceConfig {
	return &bundle.WorkspaceConfig{Webapps: []bundle.WebappConfig{
		{ID: "rag", Aliases: []string{"EcosRagApp"}},
		{ID: "ai", Aliases: []string{"EcosAiApp"}},
	}}
}

func TestQdrant_GeneratedWhenRagIsPresent(t *testing.T) {
	config.ResetDesktopMode()
	resp, err := Generate(basicCfg(), ragBundle(), ragWorkspace(), SystemSecrets{JWT: "j", OIDC: "o"})
	require.NoError(t, err)

	qdrant := findGeneratedApp(resp, appdef.AppQdrant)
	require.NotNil(t, qdrant, "qdrant генерируется вместе с rag")
	assert.Equal(t, "qdrant/qdrant:v1.14.1", qdrant.Image)
	assert.Equal(t, appdef.KindThirdParty, qdrant.Kind)

	rag := findGeneratedApp(resp, appdef.AppRag)
	require.NotNil(t, rag)
	host, ok := rag.Environments.Get("QDRANT_HOST")
	require.True(t, ok)
	assert.Equal(t, appdef.AppQdrant, host)
	port, ok := rag.Environments.Get("QDRANT_GRPC_PORT")
	require.True(t, ok)
	assert.Equal(t, "6334", port)
	assert.Contains(t, []string(rag.DependsOn), appdef.AppQdrant)

	ai := findGeneratedApp(resp, appdef.AppAi)
	require.NotNil(t, ai)
	enabled, ok := ai.Environments.Get("CITECK_AI_RAG_ENABLED")
	require.True(t, ok, "иначе ассистент не воспользуется rag: флаг в ai по умолчанию false")
	assert.Equal(t, "true", enabled)
}

func TestQdrant_AbsentWhenRagIsNotInTheBundle(t *testing.T) {
	config.ResetDesktopMode()
	bun := &bundle.Def{Applications: map[string]bundle.AppDef{
		"ai": {Image: "harbor.citeck.ru/enterprise/ai:1.12.0"},
	}}

	resp, err := Generate(basicCfg(), bun, ragWorkspace(), SystemSecrets{JWT: "j", OIDC: "o"})
	require.NoError(t, err)

	assert.Nil(t, findGeneratedApp(resp, appdef.AppRag), "community-бандл не должен приносить rag")
	assert.Nil(t, findGeneratedApp(resp, appdef.AppQdrant))

	ai := findGeneratedApp(resp, appdef.AppAi)
	require.NotNil(t, ai)
	_, ok := ai.Environments.Get("CITECK_AI_RAG_ENABLED")
	assert.False(t, ok, "без rag флаг не выставляется")
}

func TestQdrant_AbsentWhenRagIsDetached(t *testing.T) {
	config.ResetDesktopMode()
	resp, err := Generate(basicCfg(), ragBundle(), ragWorkspace(),
		SystemSecrets{JWT: "j", OIDC: "o"},
		GenerateOpts{DetachedApps: map[string]bool{appdef.AppRag: true}})
	require.NoError(t, err)

	assert.NotNil(t, findGeneratedApp(resp, appdef.AppRag), "спека rag остаётся, чтобы её можно было включить")
	assert.Nil(t, findGeneratedApp(resp, appdef.AppQdrant), "выключенный rag не тянет за собой qdrant")

	ai := findGeneratedApp(resp, appdef.AppAi)
	require.NotNil(t, ai)
	_, ok := ai.Environments.Get("CITECK_AI_RAG_ENABLED")
	assert.False(t, ok)
}

func TestQdrant_MarksRagAsGating(t *testing.T) {
	config.ResetDesktopMode()
	resp, err := Generate(basicCfg(), ragBundle(), ragWorkspace(), SystemSecrets{JWT: "j", OIDC: "o"})
	require.NoError(t, err)
	assert.True(t, resp.GatingApps[appdef.AppRag],
		"без этого Start на rag не перегенерирует неймспейс и qdrant не появится")
}
