package namespace

import (
	"maps"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/config"
)

// wsDeps is the workspace `dependencies:` section as the parser produces it.
// UnmarshalYAML always sets Images alongside Image — even for a plain string,
// which decodes to a one-rung ladder — so a fixture built by literal struct
// construction has to set both too, or it would understate what a real
// workspace config carries and hide any bug in a chain-aware reader.
func wsDeps(images map[string]string) map[string]bundle.DependencyEntry {
	out := make(map[string]bundle.DependencyEntry, len(images))
	for name, image := range images {
		out[name] = bundle.DependencyEntry{Image: image, Images: []string{image}}
	}
	return out
}

// generateWithWorkspace runs one generation against the caller's bundle AND
// workspace config. It mirrors generateCfgWithStates (same gateway webapp, same
// reason — see depsTestWorkspace) but lets the test own the workspace, which is
// the whole subject here. No pins: the gate is not what these tests are about,
// and with no pin the candidate always applies.
func generateWithWorkspace(t *testing.T, bun *bundle.Def, ws *bundle.WorkspaceConfig) *GenResp {
	t.Helper()
	return generateCfgWithWorkspace(t, depsTestConfig(), bun, ws)
}

func generateCfgWithWorkspace(t *testing.T, cfg *Config, bun *bundle.Def, ws *bundle.WorkspaceConfig) *GenResp {
	t.Helper()
	apps := map[string]bundle.AppDef{appdef.AppGateway: {Image: "citeck/gateway:1.0.0"}}
	var bundleDeps map[string]bundle.AppDef
	if bun != nil {
		maps.Copy(apps, bun.Applications)
		bundleDeps = bun.Dependencies
	}
	if ws == nil {
		ws = &bundle.WorkspaceConfig{}
	}
	ws.Webapps = []bundle.WebappConfig{{ID: appdef.AppGateway}}
	resp, err := Generate(cfg, &bundle.Def{Applications: apps, Dependencies: bundleDeps}, ws,
		SystemSecrets{JWT: "j", OIDC: "o"}, GenerateOpts{})
	require.NoError(t, err)
	return resp
}

// The reason the section exists one level up from the bundle: a workspace may
// raise a third-party version for every namespace at once, and a launcher with
// no dependency gate must not see it. Nothing else in the workspace config has
// that property — `postgres.image` is read by Kotlin 1.x and by every Go
// release up to 2.11.7.
func TestWorkspaceDependenciesSectionNamesTheImage(t *testing.T) {
	ws := &bundle.WorkspaceConfig{Dependencies: wsDeps(map[string]string{
		appdef.AppPostgres: "postgres:17.11",
	})}
	assert.Equal(t, "postgres:17.11",
		appByName(t, generateWithWorkspace(t, nil, ws), appdef.AppPostgres).Image)
}

// An id with no typed workspace block at all reaches its generator through the
// section — that is what makes the section a general mechanism rather than a
// second spelling of the seven blocks that already exist.
func TestWorkspaceDependenciesCoverAnIdWithNoTypedBlock(t *testing.T) {
	ws := &bundle.WorkspaceConfig{Dependencies: wsDeps(map[string]string{
		appdef.AppRabbitmq: "rabbitmq:4.2.9-management",
		appdef.AppMailpit:  "axllent/mailpit:v1.99.0",
	})}
	resp := generateWithWorkspace(t, nil, ws)
	assert.Equal(t, "rabbitmq:4.2.9-management", appByName(t, resp, appdef.AppRabbitmq).Image)
	assert.Equal(t, "axllent/mailpit:v1.99.0", appByName(t, resp, appdef.AppMailpit).Image)
}

// The registry rewriting every other workspace image goes through
// (additionalApps, the bundle's own section) applies here too: a stand that
// mirrors third-party images declares that once, in imageRepos.
func TestWorkspaceDependenciesResolveImageRepoPrefixes(t *testing.T) {
	ws := &bundle.WorkspaceConfig{
		ImageRepos:   []bundle.ImageRepo{{ID: "core", URL: "nexus.citeck.ru"}},
		Dependencies: wsDeps(map[string]string{appdef.AppPostgres: "core/postgres:17.11"}),
	}
	assert.Equal(t, "nexus.citeck.ru/postgres:17.11",
		appByName(t, generateWithWorkspace(t, nil, ws), appdef.AppPostgres).Image)
}

// The five-step chain, one test per step boundary. Every fixture value is
// deliberately different from the generator's own default (postgres:17.5), or
// deleting a whole lookup step would still produce the expected string.
func TestDependencyImageResolutionOrder(t *testing.T) {
	const (
		bundleSection = "postgres:17.12"
		bundleTop     = "postgres:17.9"
		wsSection     = "postgres:17.11"
		wsTypedBlock  = "postgres:17.7"
		launcherDflt  = "postgres:17.5"
	)
	both := &bundle.Def{
		Applications: map[string]bundle.AppDef{appdef.AppPostgres: {Image: bundleTop}},
		Dependencies: map[string]bundle.AppDef{appdef.AppPostgres: {Image: bundleSection}},
	}
	fullWs := func() *bundle.WorkspaceConfig {
		return &bundle.WorkspaceConfig{
			Postgres:     bundle.PostgresProps{Image: wsTypedBlock},
			Dependencies: wsDeps(map[string]string{appdef.AppPostgres: wsSection}),
		}
	}
	img := func(t *testing.T, bun *bundle.Def, ws *bundle.WorkspaceConfig) string {
		t.Helper()
		return appByName(t, generateWithWorkspace(t, bun, ws), appdef.AppPostgres).Image
	}

	// 1 beats 2: a bundle in transition carries both — the top-level entry for
	// launchers with no gate, the section for those that have one — and reading
	// the top level first would make the section unusable for its one purpose.
	t.Run("the bundle section beats the bundle top level", func(t *testing.T) {
		assert.Equal(t, bundleSection, img(t, both, fullWs()))
	})

	// 2 beats 3: NOT inverted. Every stand where a bundle and a workspace both
	// name an image runs the bundle's today, and inverting that would silently
	// change what those stands run.
	t.Run("the bundle top level beats the workspace section", func(t *testing.T) {
		topOnly := &bundle.Def{Applications: map[string]bundle.AppDef{appdef.AppPostgres: {Image: bundleTop}}}
		assert.Equal(t, bundleTop, img(t, topOnly, fullWs()))
	})

	// 3 beats 4: the section is the new, gate-aware place to name an image, so
	// a workspace that has moved an image there must not be overruled by the
	// legacy block it moved it out of.
	t.Run("the workspace section beats the legacy typed block", func(t *testing.T) {
		assert.Equal(t, wsSection, img(t, nil, fullWs()))
	})

	// 4 beats 5: unchanged. Every workspace config in the field is this one.
	t.Run("the legacy typed block still beats the launcher default", func(t *testing.T) {
		ws := &bundle.WorkspaceConfig{Postgres: bundle.PostgresProps{Image: wsTypedBlock}}
		assert.Equal(t, wsTypedBlock, img(t, nil, ws))
	})

	// 5: with neither new section present and nothing configured, the literal.
	t.Run("nothing configured ⇒ the launcher default", func(t *testing.T) {
		assert.Equal(t, launcherDflt, img(t, nil, nil))
	})
}

// Mongo follows the same bundle-first image order as other dependencies.
func TestMongoWorkspaceDependencyPrecedence(t *testing.T) {
	wsMongo := func() *bundle.WorkspaceConfig {
		return &bundle.WorkspaceConfig{Dependencies: wsDeps(map[string]string{
			appdef.AppMongodb: "mongo:4.4.29",
		})}
	}
	t.Run("the workspace section beats the literal default", func(t *testing.T) {
		assert.Equal(t, "mongo:4.4.29",
			appByName(t, generateWithWorkspace(t, nil, wsMongo()), appdef.AppMongodb).Image)
	})
	t.Run("the bundle section beats the workspace section", func(t *testing.T) {
		bun := &bundle.Def{Dependencies: map[string]bundle.AppDef{
			appdef.AppMongodb: {Image: "mongo:4.2.24"},
		}}
		assert.Equal(t, "mongo:4.2.24",
			appByName(t, generateWithWorkspace(t, bun, wsMongo()), appdef.AppMongodb).Image)
	})
	t.Run("the bundle beats namespace and workspace", func(t *testing.T) {
		cfg := depsTestConfig()
		cfg.MongoDB.Image = "mongo:4.4.18"
		bun := &bundle.Def{Dependencies: map[string]bundle.AppDef{
			appdef.AppMongodb: {Image: "mongo:4.2.24"},
		}}
		assert.Equal(t, "mongo:4.2.24",
			appByName(t, generateCfgWithWorkspace(t, cfg, bun, wsMongo()), appdef.AppMongodb).Image)
	})
	t.Run("a top-level bundle entry beats the default", func(t *testing.T) {
		bun := &bundle.Def{Applications: map[string]bundle.AppDef{
			appdef.AppMongodb: {Image: "mongo:9.9.9"},
		}}
		assert.Equal(t, "mongo:9.9.9",
			appByName(t, generateWithWorkspace(t, bun, nil), appdef.AppMongodb).Image,
			"the bundle selects the image")
	})
}

// An id this launcher does not know is kept by the parser and simply never
// asked for: it must not become a container, and it must not disturb the ones
// that are generated.
func TestWorkspaceDependenciesIgnoreAnUnknownId(t *testing.T) {
	ws := &bundle.WorkspaceConfig{Dependencies: wsDeps(map[string]string{
		"some-future-thing": "future/thing:1.0",
		appdef.AppPostgres:  "postgres:17.11",
	})}
	resp := generateWithWorkspace(t, nil, ws)
	assert.Nil(t, appByName(t, resp, "some-future-thing"),
		"an unknown dependency id must never become a container")
	assert.Equal(t, "postgres:17.11", appByName(t, resp, appdef.AppPostgres).Image)
}

// The compatibility contract: a workspace config with no `dependencies:`
// section generates the same defs it generates today, byte for byte. The
// hash input IS the deployment identity — a change here recreates every
// third-party container on every stand on upgrade.
func TestAWorkspaceWithNoDependenciesSectionGeneratesTheSameDefs(t *testing.T) {
	ws := &bundle.WorkspaceConfig{
		Postgres:  bundle.PostgresProps{Image: "postgres:17.7"},
		Zookeeper: bundle.ZookeeperProps{Image: "zookeeper:3.9.4"},
	}
	resp := generateWithWorkspace(t, nil, ws)
	for name, want := range map[string]string{
		appdef.AppPostgres:  "postgres:17.7",
		appdef.AppZookeeper: "zookeeper:3.9.4",
		appdef.AppRabbitmq:  "rabbitmq:4.1.2-management",
		appdef.AppMongodb:   "mongo:4.0.2",
	} {
		assert.Equal(t, want, appByName(t, resp, name).Image, "image of %s", name)
	}
}

// pgAdmin follows the bundle-first order, including namespace conflicts.
func TestPgAdminFollowsTheOrdinaryResolutionOrder(t *testing.T) {
	config.SetDesktopMode(true) // pgAdmin is generated in desktop mode only
	t.Cleanup(func() { config.SetDesktopMode(false) })

	pgAdminImage := func(t *testing.T, cfg *Config, bun *bundle.Def, ws *bundle.WorkspaceConfig) string {
		t.Helper()
		app := findGeneratedApp(generateCfgWithWorkspace(t, cfg, bun, ws), appdef.AppPgadmin)
		require.NotNil(t, app, "pgAdmin must be generated in desktop mode")
		return app.Image
	}
	wsWithBlock := func(deps map[string]string) *bundle.WorkspaceConfig {
		ws := &bundle.WorkspaceConfig{PgAdmin: bundle.PgAdminWsProps{Image: "dpage/pgadmin4:9.14"}}
		if deps != nil {
			ws.Dependencies = wsDeps(deps)
		}
		return ws
	}

	t.Run("the bundle now beats the typed workspace block", func(t *testing.T) {
		bun := &bundle.Def{Applications: map[string]bundle.AppDef{
			appdef.AppPgadmin: {Image: "dpage/pgadmin4:9.16"},
		}}
		assert.Equal(t, "dpage/pgadmin4:9.16", pgAdminImage(t, depsTestConfig(), bun, wsWithBlock(nil)))
	})

	t.Run("the bundle section beats the bundle top level", func(t *testing.T) {
		bun := &bundle.Def{
			Applications: map[string]bundle.AppDef{appdef.AppPgadmin: {Image: "dpage/pgadmin4:9.16"}},
			Dependencies: map[string]bundle.AppDef{appdef.AppPgadmin: {Image: "dpage/pgadmin4:9.15"}},
		}
		assert.Equal(t, "dpage/pgadmin4:9.15", pgAdminImage(t, depsTestConfig(), bun, wsWithBlock(nil)))
	})

	t.Run("the workspace section beats the typed block", func(t *testing.T) {
		ws := wsWithBlock(map[string]string{appdef.AppPgadmin: "dpage/pgadmin4:9.13"})
		assert.Equal(t, "dpage/pgadmin4:9.13", pgAdminImage(t, depsTestConfig(), nil, ws))
	})

	t.Run("the typed block still beats the launcher default", func(t *testing.T) {
		assert.Equal(t, "dpage/pgadmin4:9.14", pgAdminImage(t, depsTestConfig(), nil, wsWithBlock(nil)))
	})

	t.Run("the bundle beats the namespace config", func(t *testing.T) {
		cfg := depsTestConfig()
		cfg.PgAdmin.Image = "dpage/pgadmin4:9.12"
		bun := &bundle.Def{Dependencies: map[string]bundle.AppDef{
			appdef.AppPgadmin: {Image: "dpage/pgadmin4:9.15"},
		}}
		assert.Equal(t, "dpage/pgadmin4:9.15", pgAdminImage(t, cfg, bun, wsWithBlock(nil)))
	})
}

// The release notes for the bundle-over-config precedence promised that
// configuration still trying to override the image is "logged as a warning
// instead of silently taking effect" — but the warning covered only the
// namespace.yml layer. A workspace `dependencies:` entry the bundle outranks
// was discarded with no signal at all, which is the layer whose author is
// furthest from the stand and least likely to notice.
func TestWorkspaceDependencyImageOverriddenByTheBundleIsReported(t *testing.T) {
	ctx := &NsGenContext{WorkspaceConfig: &bundle.WorkspaceConfig{
		Dependencies: wsDeps(map[string]string{"postgres": "postgres:17.5"}),
	}}

	assert.Equal(t, "postgres:17.5",
		discardedWorkspaceImage(ctx, "postgres", "postgres:17.11"),
		"the workspace value that lost must be named")
}

// Two cases that must stay silent: the workspace names the SAME image the
// bundle does (nothing was discarded), and the workspace says nothing at all
// (the launcher's own default is not operator configuration and warning about
// it would fire on every app of every namespace).
func TestNoWarningWhenTheWorkspaceDiscardsNothing(t *testing.T) {
	same := &NsGenContext{WorkspaceConfig: &bundle.WorkspaceConfig{
		Dependencies: wsDeps(map[string]string{"postgres": "postgres:17.11"}),
	}}
	assert.Empty(t, discardedWorkspaceImage(same, "postgres", "postgres:17.11"))

	silent := &NsGenContext{WorkspaceConfig: &bundle.WorkspaceConfig{}}
	assert.Empty(t, discardedWorkspaceImage(silent, "postgres", "postgres:17.11"))
}

// …and the call site actually emits it. The helper above answers WHICH image
// was discarded; this pins that resolveAppImage says so out loud, which is the
// whole point — a pure helper nobody calls warns nobody.
func TestTheDiscardedWorkspaceImageIsLogged(t *testing.T) {
	logs := captureLogs(t)

	generateWithWorkspace(t,
		&bundle.Def{Dependencies: map[string]bundle.AppDef{"postgres": {Image: "postgres:17.11"}}},
		&bundle.WorkspaceConfig{Dependencies: wsDeps(map[string]string{"postgres": "postgres:17.5"})})

	out := logs.String()
	assert.Contains(t, out, "Bundle image overrides workspace image")
	assert.Contains(t, out, "postgres:17.5")
}
