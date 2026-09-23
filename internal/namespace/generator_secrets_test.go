package namespace

import (
	"testing"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func secretUsingApp(name, env string, deps ...string) bundle.AdditionalAppProps {
	return bundle.AdditionalAppProps{
		Name:         name,
		Image:        "example/" + name + ":1",
		Environments: map[string]string{"PASSWORD": env},
		DependsOn:    deps,
	}
}

func generateWithSecrets(t *testing.T, ws *bundle.WorkspaceConfig, secrets map[string]string) *GenResp {
	t.Helper()
	config.ResetDesktopMode()
	resp, err := Generate(basicCfg(), &bundle.Def{}, ws, SystemSecrets{JWT: "j", OIDC: "o"},
		GenerateOpts{NamespaceSecrets: secrets})
	require.NoError(t, err)
	return resp
}

// A reference names the namespace's OWN value — the daemon hands the generator
// what the namespace has stored, the workspace entry being only the default
// that was stored the first time.
func TestSecretReferenceResolvesToTheNamespaceValue(t *testing.T) {
	ws := wsWithApps([]bundle.AdditionalAppProps{
		secretUsingApp("billing", "pre-${secret:billing-db}-post"),
	})
	resp := generateWithSecrets(t, ws, map[string]string{"billing-db": "s3cr3t"})

	app := findGeneratedApp(resp, "billing")
	require.NotNil(t, app)
	v, _ := app.Environments.Get("PASSWORD")
	assert.Equal(t, "pre-s3cr3t-post", v)
}

// A service whose secret did not arrive is NOT started: running it with an
// empty password is how a database gets initialized with one, and every app
// that depends on it goes with it through the ordinary dependency prune.
func TestAnAppWhoseSecretIsMissingIsNotGenerated(t *testing.T) {
	ws := wsWithApps([]bundle.AdditionalAppProps{
		secretUsingApp("billing-db", "${secret:billing-db}"),
		secretUsingApp("billing", "plain", "billing-db"),
		secretUsingApp("unrelated", "plain"),
	})
	resp := generateWithSecrets(t, ws, map[string]string{"other": "x"})

	assert.Nil(t, findGeneratedApp(resp, "billing-db"), "no secret, no container")
	assert.Nil(t, findGeneratedApp(resp, "billing"), "its dependents go with it")
	assert.NotNil(t, findGeneratedApp(resp, "unrelated"), "the rest of the stand is untouched")
}

// The check covers every place a reference is resolved, not just env.
func TestAMissingSecretInCmdOrAnInitContainerAlsoExcludesTheApp(t *testing.T) {
	cmd := secretUsingApp("by-cmd", "plain")
	cmd.Cmd = []string{"run", "--password=${secret:nope}"}
	initC := secretUsingApp("by-init", "plain")
	initC.InitContainers = []appdef.InitContainerDef{{
		Image:        "busybox",
		Environments: appdef.OrderedMap{{Key: "P", Value: "${secret:nope}"}},
	}}
	resp := generateWithSecrets(t, wsWithApps([]bundle.AdditionalAppProps{cmd, initC}), nil)

	assert.Nil(t, findGeneratedApp(resp, "by-cmd"))
	assert.Nil(t, findGeneratedApp(resp, "by-init"))
}

// An explicitly empty value is a value: the operator wrote it on purpose.
func TestAnExplicitlyEmptySecretIsSubstituted(t *testing.T) {
	ws := wsWithApps([]bundle.AdditionalAppProps{secretUsingApp("billing", "[${secret:empty}]")})
	resp := generateWithSecrets(t, ws, map[string]string{"empty": ""})

	app := findGeneratedApp(resp, "billing")
	require.NotNil(t, app)
	v, _ := app.Environments.Get("PASSWORD")
	assert.Equal(t, "[]", v)
}

// Webapp env goes through the same substitution as additionalApps env.
func TestSecretReferenceInWebappEnv(t *testing.T) {
	bun := &bundle.Def{Applications: map[string]bundle.AppDef{"billing": {Image: "example/billing:1"}}}
	ws := &bundle.WorkspaceConfig{Webapps: []bundle.WebappConfig{{
		ID: "billing",
		DefaultProps: bundle.WebappDefaultProps{
			Environments: map[string]string{"BILLING_KEY": "${secret:billing-key}"},
		},
	}}}
	config.ResetDesktopMode()
	resp, err := Generate(basicCfg(), bun, ws, SystemSecrets{JWT: "j", OIDC: "o"},
		GenerateOpts{NamespaceSecrets: map[string]string{"billing-key": "k"}})
	require.NoError(t, err)
	app := findGeneratedApp(resp, "billing")
	require.NotNil(t, app)
	v, _ := app.Environments.Get("BILLING_KEY")
	assert.Equal(t, "k", v)

	resp, err = Generate(basicCfg(), bun, ws, SystemSecrets{JWT: "j", OIDC: "o"})
	require.NoError(t, err)
	assert.Nil(t, findGeneratedApp(resp, "billing"), "a webapp is held to the same rule")
}

// A declared cluster's password is a reference to the namespace's own value;
// without one the cluster is not generated, and neither is what depends on it.
func TestADeclaredDatabasePasswordComesFromTheNamespaceSecret(t *testing.T) {
	bun := &bundle.Def{Dependencies: map[string]bundle.AppDef{"billing-postgres": {Image: "postgres:17.5"}}}
	ws := &bundle.WorkspaceConfig{AdditionalApps: []bundle.AdditionalAppProps{
		{Name: "billing-postgres", Type: bundle.AppTypePostgres, Postgres: &bundle.PostgresAppProps{
			Name: "billing-postgres", Type: bundle.AppTypePostgres, User: "billing", Password: "${secret:billing-db}",
		}},
		secretUsingApp("billing", "plain", "billing-postgres"),
	}}
	config.ResetDesktopMode()

	resp, err := Generate(basicCfg(), bun, ws, SystemSecrets{JWT: "j", OIDC: "o"},
		GenerateOpts{NamespaceSecrets: map[string]string{"billing-db": "s3cr3t"}})
	require.NoError(t, err)
	db := findGeneratedApp(resp, "billing-postgres")
	require.NotNil(t, db)
	v, _ := db.Environments.Get("POSTGRES_PASSWORD")
	assert.Equal(t, "s3cr3t", v)
	require.NotNil(t, findGeneratedApp(resp, "billing"))

	resp, err = Generate(basicCfg(), bun, ws, SystemSecrets{JWT: "j", OIDC: "o"})
	require.NoError(t, err)
	assert.Nil(t, findGeneratedApp(resp, "billing-postgres"), "never initialized without its password")
	assert.Nil(t, findGeneratedApp(resp, "billing"), "and its service goes with it")
}
