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

// TestQdrant_MarksRagAsGating_EvenWhenRagIsDetached closes a gap the reviewer
// found by mutation: TestQdrant_MarksRagAsGating only exercises the
// rag-attached branch, where MarkGatingApp could be moved after the
// `if ctx.DetachedApps[appdef.AppRag] { return }` early exit without any test
// noticing (the early return still lets the not-detached test pass). If that
// happened, a Start on a detached rag would never regenerate the namespace
// and qdrant would never appear. This test pins gating specifically for the
// detached case.
func TestQdrant_MarksRagAsGating_EvenWhenRagIsDetached(t *testing.T) {
	config.ResetDesktopMode()
	resp, err := Generate(basicCfg(), ragBundle(), ragWorkspace(),
		SystemSecrets{JWT: "j", OIDC: "o"},
		GenerateOpts{DetachedApps: map[string]bool{appdef.AppRag: true}})
	require.NoError(t, err)
	assert.True(t, resp.GatingApps[appdef.AppRag],
		"rag detached всё равно должен остаться gating — иначе Start на нём не перегенерирует неймспейс")
}

// TestQdrant_DetachedQdrant_RagKeepsHardDependency pins the decision from
// review round 1: unlike generateSttSidecar (which drops the AI->stt-sidecar
// wiring when stt-sidecar is detached), rag's dependency on qdrant is NOT
// conditioned on qdrant's own detached state. AI works fine without STT; rag
// without qdrant is not a smaller rag, it's a rag that looks RUNNING and
// silently can't search or index anything. So a detached qdrant must still
// leave rag's QDRANT_HOST/QDRANT_GRPC_PORT env vars and DependsOn(qdrant) in
// place — rag ends up parked in DEPS_WAITING (task 3 behavior) rather than
// starting broken. If a future change "unifies" this with the stt pattern by
// adding a `!ctx.DetachedApps[appdef.AppQdrant]` guard around the rag wiring,
// this test must fail.
func TestQdrant_DetachedQdrant_RagKeepsHardDependency(t *testing.T) {
	config.ResetDesktopMode()
	resp, err := Generate(basicCfg(), ragBundle(), ragWorkspace(),
		SystemSecrets{JWT: "j", OIDC: "o"},
		GenerateOpts{DetachedApps: map[string]bool{appdef.AppQdrant: true}})
	require.NoError(t, err)

	require.NotNil(t, findGeneratedApp(resp, appdef.AppQdrant),
		"спека qdrant остаётся даже detached, как у stt-sidecar")

	rag := findGeneratedApp(resp, appdef.AppRag)
	require.NotNil(t, rag)
	host, ok := rag.Environments.Get("QDRANT_HOST")
	require.True(t, ok, "detached qdrant не должен вырезать зависимость rag — иначе rag стартует молча сломанным")
	assert.Equal(t, appdef.AppQdrant, host)
	_, ok = rag.Environments.Get("QDRANT_GRPC_PORT")
	require.True(t, ok)
	assert.Contains(t, []string(rag.DependsOn), appdef.AppQdrant,
		"без dependsOn rag не встанет в DEPS_WAITING, а стартует без векторной БД")
}
