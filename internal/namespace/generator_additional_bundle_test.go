package namespace

import (
	"testing"

	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func genAdditional(t *testing.T, bun *bundle.Def, ws *bundle.WorkspaceConfig, secrets map[string]string) *GenResp {
	t.Helper()
	config.ResetDesktopMode()
	if bun == nil {
		bun = &bundle.Def{}
	}
	resp, err := Generate(basicCfg(), bun, ws, SystemSecrets{JWT: "JWT", OIDC: "o", CiteckSA: "SA"},
		GenerateOpts{NamespaceSecrets: secrets})
	require.NoError(t, err)
	return resp
}

// A raw entry with no image takes the one the BUNDLE names for it, from either
// section — the same rule a typed entry follows: whether a service exists is
// the release's answer, and the workspace only says how it is configured.
func TestARawEntryWithoutAnImageFollowsTheBundle(t *testing.T) {
	ws := wsWithApps([]bundle.AdditionalAppProps{{Name: "svc"}})

	t.Run("applications", func(t *testing.T) {
		resp := genAdditional(t, &bundle.Def{Applications: map[string]bundle.AppDef{"svc": {Image: "example/svc:2"}}}, ws, nil)
		app := findGeneratedApp(resp, "svc")
		require.NotNil(t, app)
		assert.Equal(t, "example/svc:2", app.Image)
	})
	t.Run("dependencies", func(t *testing.T) {
		resp := genAdditional(t, &bundle.Def{Dependencies: map[string]bundle.AppDef{"svc": {Image: "example/svc:3"}}}, ws, nil)
		app := findGeneratedApp(resp, "svc")
		require.NotNil(t, app)
		assert.Equal(t, "example/svc:3", app.Image)
	})
	t.Run("not named", func(t *testing.T) {
		resp := genAdditional(t, nil, ws, nil)
		assert.Nil(t, findGeneratedApp(resp, "svc"), "a release that does not ship it does not get it")
	})
	// An image the entry names itself is a DEFAULT, exactly like the image of
	// a typed entry or a built-in service: the bundle, when it names one, wins.
	own := wsWithApps([]bundle.AdditionalAppProps{{
		Name:         "svc",
		Image:        "example/svc:own",
		Environments: map[string]string{"MARK": "additional"},
	}})
	t.Run("own image, bundle names none", func(t *testing.T) {
		resp := genAdditional(t, nil, own, nil)
		app := findGeneratedApp(resp, "svc")
		require.NotNil(t, app)
		assert.Equal(t, "example/svc:own", app.Image, "the entry's own image is the fallback")
	})
	t.Run("own image, bundle dependencies", func(t *testing.T) {
		resp := genAdditional(t, &bundle.Def{Dependencies: map[string]bundle.AppDef{"svc": {Image: "example/svc:3"}}}, own, nil)
		app := findGeneratedApp(resp, "svc")
		require.NotNil(t, app)
		assert.Equal(t, "example/svc:3", app.Image, "the bundle overrides the entry's image")
	})
	// The same holds above `dependencies:`, where the bundle's webapps live: the
	// entry claims the name, so the service is generated from its declaration
	// (never as a webapp as well) and runs the image the release names.
	t.Run("own image, bundle applications", func(t *testing.T) {
		resp := genAdditional(t, &bundle.Def{Applications: map[string]bundle.AppDef{"svc": {Image: "example/svc:2"}}}, own, nil)
		app := findGeneratedApp(resp, "svc")
		require.NotNil(t, app)
		assert.Equal(t, "example/svc:2", app.Image, "the bundle overrides the entry's image")
		v, _ := app.Environments.Get("MARK")
		assert.Equal(t, "additional", v, "generated from the declaration, not as a webapp")
		assert.Empty(t, app.NetworkAliases, "no webapp wiring on it")
	})
}

// The bundle naming an image for a service the workspace declares as an
// additional app (with or without an image of its own) does not make it a
// webapp too — not even with an empty
// `webapps:` list, which admits everything the bundle carries.
func TestABundleAppClaimedByAnAdditionalAppIsNotAWebapp(t *testing.T) {
	bun := &bundle.Def{Applications: map[string]bundle.AppDef{"svc": {Image: "example/svc:2"}}}
	resp := genAdditional(t, bun, wsWithApps([]bundle.AdditionalAppProps{{
		Name:         "svc",
		Environments: map[string]string{"MARK": "additional"},
	}}), nil)
	app := findGeneratedApp(resp, "svc")
	require.NotNil(t, app)
	v, _ := app.Environments.Get("MARK")
	assert.Equal(t, "additional", v, "generated from the declaration, not as a webapp")
	assert.Empty(t, app.NetworkAliases, "no webapp wiring on it")
}

// cloudConfig is what CloudConfigServer hands a service run from an IDE. Its
// strings take the same substitution as env; other values pass through.
func TestARawEntryCarriesItsCloudConfig(t *testing.T) {
	ws := wsWithApps([]bundle.AdditionalAppProps{{
		Name:  "svc",
		Image: "example/svc:1",
		CloudConfig: map[string]any{
			"auth.jwt_secret":   "${JWT_SECRET}",
			"database.password": "${secret:db}",
			"server.port":       17016,
			"monitor.enabled":   true,
			"nested":            map[string]any{"hosts": []any{"${ZK_HOST}:2181"}},
		},
	}})
	resp := genAdditional(t, nil, ws, map[string]string{"db": "pw"})
	require.NotNil(t, findGeneratedApp(resp, "svc"))
	cc := resp.CloudConfig["svc"]
	require.NotNil(t, cc)
	assert.Equal(t, "JWT", cc["auth.jwt_secret"])
	assert.Equal(t, "pw", cc["database.password"])
	assert.Equal(t, 17016, cc["server.port"])
	assert.Equal(t, true, cc["monitor.enabled"])
	assert.Equal(t, map[string]any{"hosts": []any{ZKHost + ":2181"}}, cc["nested"])

	resp = genAdditional(t, nil, ws, nil)
	assert.Nil(t, findGeneratedApp(resp, "svc"), "a missing secret in cloudConfig counts too")
	assert.NotContains(t, resp.CloudConfig, "svc")
}
