package namespace

import (
	"testing"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/deps"
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
	require.NotNil(t, qdrant, "qdrant is generated alongside rag")
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
	require.True(t, ok, "without it the assistant never uses rag: ai ships with the flag false")
	assert.Equal(t, "true", enabled)
}

// TestQdrant_AbsentWhenTheBundleCarriesNoImage is what keeps the store off
// community stands, and the condition is the IMAGE, not rag: no community
// bundle names a qdrant image, above the `dependencies:` section or inside it.
func TestQdrant_AbsentWhenTheBundleCarriesNoImage(t *testing.T) {
	config.ResetDesktopMode()
	bun := &bundle.Def{Applications: map[string]bundle.AppDef{
		"ai": {Image: "harbor.citeck.ru/enterprise/ai:1.12.0"},
	}}

	resp, err := Generate(basicCfg(), bun, ragWorkspace(), SystemSecrets{JWT: "j", OIDC: "o"})
	require.NoError(t, err)

	assert.Nil(t, findGeneratedApp(resp, appdef.AppRag), "a community bundle must not bring rag")
	assert.Nil(t, findGeneratedApp(resp, appdef.AppQdrant))

	ai := findGeneratedApp(resp, appdef.AppAi)
	require.NotNil(t, ai)
	_, ok := ai.Environments.Get("CITECK_AI_RAG_ENABLED")
	assert.False(t, ok, "no rag, no flag")
}

// TestQdrant_GeneratedWithoutRagWhenTheBundleCarriesTheImage: a qdrant image
// and no EcosRagApp anywhere. The store is not private to rag — the launcher
// offers it like any other app, and whatever is pointed at it next needs only a
// dependsOn.
//
// No public bundle has this shape today: running the real parser over all 18
// files of the public workspace shows every bundle that names qdrant also names
// EcosRagApp. That is what makes this a test rather than a field report — the
// case is reachable only through a bundle nobody has written yet, and it is the
// one the rule exists for.
func TestQdrant_GeneratedWithoutRagWhenTheBundleCarriesTheImage(t *testing.T) {
	config.ResetDesktopMode()
	bun := ragBundle()
	delete(bun.Applications, "rag")

	resp, err := Generate(basicCfg(), bun, ragWorkspace(), SystemSecrets{JWT: "j", OIDC: "o"})
	require.NoError(t, err)

	qdrant := findGeneratedApp(resp, appdef.AppQdrant)
	require.NotNil(t, qdrant, "the bundle's image is the only condition for generating the store")
	assert.Equal(t, "qdrant/qdrant:v1.14.1", qdrant.Image)
	assert.Nil(t, findGeneratedApp(resp, appdef.AppRag))

	ai := findGeneratedApp(resp, appdef.AppAi)
	require.NotNil(t, ai)
	_, ok := ai.Environments.Get("CITECK_AI_RAG_ENABLED")
	assert.False(t, ok, "a store without rag does not make this a RAG namespace")
	assert.False(t, resp.GatingApps[appdef.AppRag],
		"nothing to regenerate for an app that is not in the namespace")
}

// TestQdrant_RagRagFlagFollowsPresenceNotDetachState: CITECK_AI_RAG_ENABLED
// says whether this namespace HAS rag, not whether rag is running right now.
// Stopping rag is how it is run from an IDE instead, and flipping the flag on
// that toggle would rewrite — and therefore recreate — the ai container every
// time.
func TestQdrant_RagFlagFollowsPresenceNotDetachState(t *testing.T) {
	config.ResetDesktopMode()
	resp, err := Generate(basicCfg(), ragBundle(), ragWorkspace(),
		SystemSecrets{JWT: "j", OIDC: "o"},
		GenerateOpts{DetachedApps: map[string]bool{appdef.AppRag: true}})
	require.NoError(t, err)

	assert.NotNil(t, findGeneratedApp(resp, appdef.AppRag), "rag's spec stays so it can be switched back on")

	ai := findGeneratedApp(resp, appdef.AppAi)
	require.NotNil(t, ai)
	enabled, ok := ai.Environments.Get("CITECK_AI_RAG_ENABLED")
	require.True(t, ok, "a namespace with rag stays a RAG namespace across rag's detach")
	assert.Equal(t, "true", enabled)
}

// TestQdrant_RagIsNotGating states what is left after the auto-detach verdict
// was removed: NOTHING in the generated set reads rag's detach state any more —
// the store's spec, its image, its volume and rag's own wiring are identical
// whether rag is attached or not — so toggling rag must not cost a full
// namespace regeneration. rag was gating only because the verdict (whether the
// store was held down) was computed from it. If a future change makes some
// generated value depend on rag being detached, this test is the reminder that
// MarkGatingApp has to come back with it.
func TestQdrant_RagIsNotGating(t *testing.T) {
	config.ResetDesktopMode()
	for name, detached := range map[string]map[string]bool{
		"attached": nil,
		"detached": {appdef.AppRag: true},
	} {
		t.Run(name, func(t *testing.T) {
			resp, err := Generate(basicCfg(), ragBundle(), ragWorkspace(),
				SystemSecrets{JWT: "j", OIDC: "o"},
				GenerateOpts{DetachedApps: detached})
			require.NoError(t, err)
			assert.False(t, resp.GatingApps[appdef.AppRag],
				"rag's detach state changes nothing in the generation")
		})
	}
}

// TestQdrant_DetachedQdrant_RagKeepsHardDependency pins the decision from
// review round 1: unlike generateSttSidecar (which drops the AI->stt-sidecar
// wiring when stt-sidecar is detached), rag's dependency on qdrant is NOT
// conditioned on qdrant's own detached state. AI works fine without STT; rag
// without qdrant is not a smaller rag, it's a rag that looks RUNNING and
// silently can't search or index anything. So a detached qdrant must still
// leave rag's QDRANT_HOST/QDRANT_GRPC_PORT env vars and DependsOn(qdrant) in
// place — a rag STARTED while qdrant is detached parks in DEPS_WAITING rather
// than starting broken. (This is a generation-time contract: it does not stop a
// rag that is already RUNNING when qdrant is stopped — StopApp never cascades
// to dependents. See generator_qdrant.go.) If a future change "unifies" this
// with the stt pattern by
// adding a `!ctx.DetachedApps[appdef.AppQdrant]` guard around the rag wiring,
// this test must fail.
func TestQdrant_DetachedQdrant_RagKeepsHardDependency(t *testing.T) {
	config.ResetDesktopMode()
	resp, err := Generate(basicCfg(), ragBundle(), ragWorkspace(),
		SystemSecrets{JWT: "j", OIDC: "o"},
		GenerateOpts{DetachedApps: map[string]bool{appdef.AppQdrant: true}})
	require.NoError(t, err)

	require.NotNil(t, findGeneratedApp(resp, appdef.AppQdrant),
		"qdrant's spec stays even when detached, like stt-sidecar's")

	rag := findGeneratedApp(resp, appdef.AppRag)
	require.NotNil(t, rag)
	host, ok := rag.Environments.Get("QDRANT_HOST")
	require.True(t, ok, "a detached qdrant must not drop rag's dependency, or rag starts silently broken")
	assert.Equal(t, appdef.AppQdrant, host)
	_, ok = rag.Environments.Get("QDRANT_GRPC_PORT")
	require.True(t, ok)
	assert.Contains(t, []string(rag.DependsOn), appdef.AppQdrant,
		"without dependsOn rag never parks in DEPS_WAITING and starts with no vector store")
}

// TestQdrant_ContainerShapeIsPinned covers what the rest of this file leaves to
// chance. Each assertion here guards something whose loss is silent:
//
//   - the volume: without it every container recreate (any hash change, any
//     liveness restart) discards the vector index, which needs an OpenAI key and
//     a full reindex to rebuild;
//   - the published HTTP port: runtime_app.go probes the published host port and
//     only falls back to the container IP, which is unroutable under Docker
//     Desktop — an unpublished qdrant never leaves STARTING there and rag waits
//     on it forever;
//   - the probe itself: without it qdrant is "running" the moment the container
//     exists and rag starts against a store that has not opened its socket;
//   - the memory limit: an unbounded qdrant is the one app on the stand with no
//     ceiling at all.
//
// Desktop mode, because server mode strips every non-proxy publish (see
// Generate) and the port assertion would pass vacuously.
func TestQdrant_ContainerShapeIsPinned(t *testing.T) {
	config.SetDesktopMode(true)
	defer config.ResetDesktopMode()

	resp, err := Generate(basicCfg(), ragBundle(), ragWorkspace(), SystemSecrets{JWT: "j", OIDC: "o"})
	require.NoError(t, err)

	qdrant := findGeneratedApp(resp, appdef.AppQdrant)
	require.NotNil(t, qdrant)

	assert.Contains(t, qdrant.Volumes, "qdrant2:/qdrant/storage",
		"without the volume every recreate throws the vector index away")
	assert.Contains(t, qdrant.Ports, "6333:6333",
		"the HTTP probe has no route to the container on a desktop stand unless this port is published")

	require.Len(t, qdrant.StartupConditions, 1)
	probe := qdrant.StartupConditions[0].Probe
	require.NotNil(t, probe)
	require.NotNil(t, probe.HTTP)
	assert.Equal(t, "/healthz", probe.HTTP.Path)
	assert.Equal(t, 6333, probe.HTTP.Port)

	require.NotNil(t, qdrant.Resources)
	assert.Equal(t, "1g", qdrant.Resources.Limits.Memory)
}

// TestQdrant_WorkspaceConfigReachesBothSides is the `qdrant:` section of the
// workspace config — the only path that reads bundle.QdrantProps. The gRPC port
// is the trap: it used to be told to rag alone, so a configured 7334 made rag
// dial a port qdrant had never opened, and both apps still reported RUNNING.
func TestQdrant_WorkspaceConfigReachesBothSides(t *testing.T) {
	config.ResetDesktopMode()
	ws := ragWorkspace()
	ws.Qdrant = &bundle.QdrantProps{GrpcPort: 7334, MemoryLimit: "2g"}

	resp, err := Generate(basicCfg(), ragBundle(), ws, SystemSecrets{JWT: "j", OIDC: "o"})
	require.NoError(t, err)

	qdrant := findGeneratedApp(resp, appdef.AppQdrant)
	require.NotNil(t, qdrant)
	grpc, ok := qdrant.Environments.Get("QDRANT__SERVICE__GRPC_PORT")
	require.True(t, ok, "the container has to hear about the configured port too, not just rag")
	assert.Equal(t, "7334", grpc)
	require.NotNil(t, qdrant.Resources)
	assert.Equal(t, "2g", qdrant.Resources.Limits.Memory)

	rag := findGeneratedApp(resp, appdef.AppRag)
	require.NotNil(t, rag)
	ragPort, ok := rag.Environments.Get("QDRANT_GRPC_PORT")
	require.True(t, ok)
	assert.Equal(t, "7334", ragPort, "both sides must name the same port or the store is unreachable")
}

// TestQdrant_ImageComesFromTheBundleDependenciesSection: a bundle may declare a
// third-party image in its `dependencies:` section instead of at the top level
// (that section is invisible to launchers without the dependency gate). qdrant
// is exactly that class of image, so resolveAppImage has to read it — otherwise
// such a bundle yields "Bundle has no qdrant image" and a rag with no store.
func TestQdrant_ImageComesFromTheBundleDependenciesSection(t *testing.T) {
	config.ResetDesktopMode()
	bun := &bundle.Def{
		Applications: map[string]bundle.AppDef{
			"rag": {Image: "harbor.citeck.ru/enterprise/citeck-rag:1.2.2"},
		},
		Dependencies: map[string]bundle.AppDef{
			"qdrant": {Image: "qdrant/qdrant:v1.14.1"},
		},
	}

	resp, err := Generate(basicCfg(), bun, ragWorkspace(), SystemSecrets{JWT: "j", OIDC: "o"})
	require.NoError(t, err)

	qdrant := findGeneratedApp(resp, appdef.AppQdrant)
	require.NotNil(t, qdrant, "a bundle that hides qdrant in `dependencies:` must still get a store")
	assert.Equal(t, "qdrant/qdrant:v1.14.1", qdrant.Image)
}

// TestQdrant_GoesThroughTheDependencyGate is the registration of qdrant as an
// infra DEPENDENCY rather than a plain generated app, and both halves matter.
//
// The IMAGE goes through resolveDependencyImage, so a bundle that raises the
// minor on an existing namespace is HELD BACK and reported instead of being
// applied to a vector index the new version may not read — Qdrant's storage
// compatibility spans exactly one minor.
//
// The VOLUME comes from resolveDependencyVolume, which is what renamed it from
// "qdrant_storage" to "qdrant2": a volume outside the generation counter cannot
// be migrated at all, because a copy upgrade works by building the next
// generation beside the current one. Nothing in the field paid for the rename —
// RAG has never been released — and a dev stand pays a re-indexing.
func TestQdrant_GoesThroughTheDependencyGate(t *testing.T) {
	config.ResetDesktopMode()
	cfg := basicCfg()
	resp, err := Generate(cfg, ragBundle(), ragWorkspace(), SystemSecrets{JWT: "j", OIDC: "o"})
	require.NoError(t, err)

	qdrant := findGeneratedApp(resp, appdef.AppQdrant)
	require.NotNil(t, qdrant)
	assert.Contains(t, qdrant.Volumes, "qdrant2:/qdrant/storage",
		"generation 1 of the counter, like postgres2 and rabbitmq2")
	require.Contains(t, resp.Dependencies, deps.Qdrant,
		"qdrant must be reported as a dependency, or nothing seeds or pins it")
}

// A namespace already pinned to v1.14.1 does NOT follow a bundle offering
// v1.15.5: the minor is where Qdrant's format break sits, so the move is held
// back and reported as an upgrade the launcher can perform.
func TestQdrant_APinnedNamespaceHoldsBackAMinorBump(t *testing.T) {
	config.ResetDesktopMode()
	bun := ragBundle()
	bun.Applications["qdrant"] = bundle.AppDef{Image: "qdrant/qdrant:v1.15.5"}

	cfg := basicCfg()
	resp, err := Generate(cfg, bun, ragWorkspace(), SystemSecrets{JWT: "j", OIDC: "o"},
		GenerateOpts{DependencyStates: map[deps.ID]deps.DependencyState{
			deps.Qdrant: {Image: "qdrant/qdrant:v1.14.1"}}})
	require.NoError(t, err)

	qdrant := findGeneratedApp(resp, appdef.AppQdrant)
	require.NotNil(t, qdrant)
	assert.Equal(t, "qdrant/qdrant:v1.14.1", qdrant.Image,
		"the pin decides what runs, not the bundle")

	var found bool
	for _, up := range resp.DependencyUpgrades {
		if up.ID == deps.Qdrant {
			found = true
			assert.Equal(t, "qdrant/qdrant:v1.15.5", up.To)
			assert.True(t, up.Migratable, "this launcher ships a qdrant migrator")
			assert.False(t, up.VendorBlocked, "one minor forward is what the vendor supports")
		}
	}
	assert.True(t, found, "a held-back qdrant must be REPORTED, or the operator never learns of it")
}

// A patch bump inside one minor is not a data move, so it applies silently —
// the same rule every other dependency follows.
func TestQdrant_APatchBumpApplies(t *testing.T) {
	config.ResetDesktopMode()
	bun := ragBundle()
	bun.Applications["qdrant"] = bundle.AppDef{Image: "qdrant/qdrant:v1.14.3"}

	resp, err := Generate(basicCfg(), bun, ragWorkspace(), SystemSecrets{JWT: "j", OIDC: "o"},
		GenerateOpts{DependencyStates: map[deps.ID]deps.DependencyState{
			deps.Qdrant: {Image: "qdrant/qdrant:v1.14.1"}}})
	require.NoError(t, err)

	qdrant := findGeneratedApp(resp, appdef.AppQdrant)
	require.NotNil(t, qdrant)
	assert.Equal(t, "qdrant/qdrant:v1.14.3", qdrant.Image)
	assert.Empty(t, resp.DependencyUpgrades)
}

// A namespace that has completed one migration mounts the NEXT generation.
func TestQdrant_VolumeFollowsTheGenerationCounter(t *testing.T) {
	config.ResetDesktopMode()
	resp, err := Generate(basicCfg(), ragBundle(), ragWorkspace(), SystemSecrets{JWT: "j", OIDC: "o"},
		GenerateOpts{DependencyStates: map[deps.ID]deps.DependencyState{
			deps.Qdrant: {Image: "qdrant/qdrant:v1.14.1", VolumeGen: 2}}})
	require.NoError(t, err)

	qdrant := findGeneratedApp(resp, appdef.AppQdrant)
	require.NotNil(t, qdrant)
	assert.Contains(t, qdrant.Volumes, "qdrant3:/qdrant/storage")
}

// TestQdrant_IsGeneratedIdenticallyWhateverRagIsDoing pins the rule that
// replaces "rag detached → no qdrant at all". Taking the vector store away with
// rag broke the workflow the CloudConfigServer exists for — "stop in launcher,
// debug locally": stopping rag to run it from an IDE also removed the store it
// would talk to. The spec stays in the namespace, and — since the auto-detach
// verdict was removed — it is BYTE-IDENTICAL either way: the store is an
// ordinary app that starts with the namespace, and a stand that should not run
// one stops it (or lists it in the template's detachedApps) like any other.
func TestQdrant_IsGeneratedIdenticallyWhateverRagIsDoing(t *testing.T) {
	config.ResetDesktopMode()
	attached, err := Generate(basicCfg(), ragBundle(), ragWorkspace(), SystemSecrets{JWT: "j", OIDC: "o"})
	require.NoError(t, err)
	detached, err := Generate(basicCfg(), ragBundle(), ragWorkspace(),
		SystemSecrets{JWT: "j", OIDC: "o"},
		GenerateOpts{DetachedApps: map[string]bool{appdef.AppRag: true}})
	require.NoError(t, err)

	withRagDetached := findGeneratedApp(detached, appdef.AppQdrant)
	require.NotNil(t, withRagDetached,
		"qdrant's spec stays, or a locally run rag has nothing to reach")
	require.NotNil(t, findGeneratedApp(attached, appdef.AppQdrant))
	assert.Equal(t, findGeneratedApp(attached, appdef.AppQdrant).GetHashInput(),
		withRagDetached.GetHashInput(),
		"a store that differed with its consumer's detach state would recreate "+
			"the container on every start and stop of rag")
}

// TestQdrant_PublishesGrpcPortForLocalDebugging: citeck-rag reaches qdrant over
// gRPC (spring.ai.vectorstore.qdrant.port = ${QDRANT_GRPC_PORT:6334}), so a rag
// run outside the launcher needs 6334 on the host — 6333 is published for the
// HTTP probe and carries no gRPC. Postgres has published 14523 for this exact
// reason since the beginning.
func TestQdrant_PublishesGrpcPortForLocalDebugging(t *testing.T) {
	t.Setenv("CITECK_DESKTOP", "true")
	config.ResetDesktopMode()
	defer config.ResetDesktopMode()
	resp, err := Generate(basicCfg(), ragBundle(), ragWorkspace(), SystemSecrets{JWT: "j", OIDC: "o"})
	require.NoError(t, err)

	qdrant := findGeneratedApp(resp, appdef.AppQdrant)
	require.NotNil(t, qdrant)
	assert.Contains(t, qdrant.Ports, "6334:6334",
		"without the published gRPC port a locally run rag cannot find the store")
}

// TestQdrant_DoesNotFollowItsConsumers is what is left of the consumer list now
// that nothing is "held": the store is generated from the BUNDLE's image alone,
// with every consumer detached, and with no consumer in the namespace at all.
// qdrantConsumers decides one thing only — whether the "bundle names no qdrant
// image" error is worth logging — so the store's own shape must not follow it.
func TestQdrant_DoesNotFollowItsConsumers(t *testing.T) {
	config.ResetDesktopMode()
	// Swapping the package-level list is safe because nothing in this package
	// calls t.Parallel; if that ever changes, this test has to grow its own
	// context instead.
	restore := qdrantConsumers
	qdrantConsumers = []string{appdef.AppRag, appdef.AppAi}
	defer func() { qdrantConsumers = restore }()

	resp, err := Generate(basicCfg(), ragBundle(), ragWorkspace(),
		SystemSecrets{JWT: "j", OIDC: "o"},
		GenerateOpts{DetachedApps: map[string]bool{appdef.AppRag: true, appdef.AppAi: true}})
	require.NoError(t, err)
	assert.NotNil(t, findGeneratedApp(resp, appdef.AppQdrant),
		"the bundle's image is the only condition; a detached consumer is not one")
}
