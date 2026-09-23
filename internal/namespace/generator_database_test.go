package namespace

import (
	"testing"

	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/deps"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// pgEntry is an additionalApps entry of type POSTGRES, built the way the YAML
// parser builds one (the typed payload plus the name/type the list is indexed
// by), so these tests exercise the same value generation sees in the field.
func pgEntry(p bundle.PostgresAppProps) bundle.AdditionalAppProps {
	p.Type = bundle.AppTypePostgres
	return bundle.AdditionalAppProps{Name: p.Name, Type: bundle.AppTypePostgres, Postgres: &p}
}

// The point of the whole file: a database nothing in Go names. A workspace
// declares it, and it gets a container, a registered dependency, a pinned
// image, a generation-counted volume — and therefore a migration and a
// rollback — with no launcher release.
func TestAWorkspaceCanDeclareADatabaseNoGoCodeKnows(t *testing.T) {
	config.ResetDesktopMode()
	t.Cleanup(deps.ResetExtraDependencies)

	ws := observerWorkspace()
	ws.AdditionalApps = []bundle.AdditionalAppProps{pgEntry(bundle.PostgresAppProps{
		Name:        "billing-postgres",
		Image:       "postgres:17.5",
		User:        "billing",
		Port:        14777,
		MemoryLimit: "256m",
		Settings:    map[string]string{"work_mem": "8MB", "shared_buffers": "64MB"},
	})}
	// The registry learns it exactly as the daemon does before generating.
	for _, s := range DatabaseSpecs(ws) {
		if s.ID == "billing-postgres" {
			deps.SetExtraDependencies([]deps.Descriptor{s.Descriptor()})
		}
	}

	resp, err := Generate(basicCfg(), &bundle.Def{}, ws, SystemSecrets{JWT: "j", OIDC: "o"})
	require.NoError(t, err)

	db := findGeneratedApp(resp, "billing-postgres")
	require.NotNil(t, db, "an entry that names its own image runs wherever the workspace is used")
	assert.Equal(t, "postgres:17.5", db.Image)
	user, _ := db.Environments.Get("POSTGRES_USER")
	assert.Equal(t, "billing", user)
	pass, _ := db.Environments.Get("POSTGRES_PASSWORD")
	assert.Equal(t, "billing", pass,
		"a workspace config in git is no place for a credential: the password follows the user")
	name, _ := db.Environments.Get("POSTGRES_DB")
	assert.Equal(t, "billing", name, "POSTGRES_DB defaults to the user, as the image itself does")
	assert.Contains(t, db.Volumes, "billing-postgres2:/var/lib/postgresql/data",
		"the volume comes from the generation counter, so the cluster can be migrated")
	assert.Equal(t, []string{"-c", "shared_buffers=64MB", "-c", "work_mem=8MB"}, db.Cmd,
		"settings are applied in KEY ORDER, or the deployment hash would follow map iteration")
	assert.Equal(t, "256m", db.Resources.Limits.Memory)

	require.Contains(t, resp.Dependencies, deps.ID("billing-postgres"),
		"a declared cluster must be reported as a dependency, or nothing pins or migrates it")
}

// The entry describes HOW a cluster is configured; the BUNDLE decides whether
// it exists — the same rule as qdrant and the observer. This is what replaced
// the old requiredBy switch: a release that does not ship the service does not
// ship its database, with nothing to keep in sync.
func TestADatabaseExistsWhereTheBundleNamesItsImage(t *testing.T) {
	config.ResetDesktopMode()
	t.Cleanup(deps.ResetExtraDependencies)

	ws := observerWorkspace()
	ws.AdditionalApps = []bundle.AdditionalAppProps{pgEntry(bundle.PostgresAppProps{
		Name: "billing-postgres", MemoryLimit: "256m",
	})}
	for _, s := range DatabaseSpecs(ws) {
		if s.ID == "billing-postgres" {
			deps.SetExtraDependencies([]deps.Descriptor{s.Descriptor()})
		}
	}

	noImage, err := Generate(basicCfg(), &bundle.Def{}, ws, SystemSecrets{JWT: "j", OIDC: "o"})
	require.NoError(t, err)
	assert.Nil(t, findGeneratedApp(noImage, "billing-postgres"),
		"settings alone must not conjure a database onto every stand using this workspace")
	assert.False(t, WillGenerateDatabases(basicCfg(), &bundle.Def{}, ws)["billing-postgres"],
		"and the seeding must agree, or an absent cluster is probed on every load")

	withImage := &bundle.Def{Dependencies: map[string]bundle.AppDef{
		"billing-postgres": {Image: "postgres:18.6"},
	}}
	resp, err := Generate(basicCfg(), withImage, ws, SystemSecrets{JWT: "j", OIDC: "o"})
	require.NoError(t, err)
	db := findGeneratedApp(resp, "billing-postgres")
	require.NotNil(t, db)
	assert.Equal(t, "postgres:18.6", db.Image, "the bundle entry outranks anything the workspace names")
	assert.Equal(t, "256m", db.Resources.Limits.Memory, "and the workspace entry still configures it")
	assert.True(t, WillGenerateDatabases(basicCfg(), withImage, ws)["billing-postgres"])
}

// A declared cluster the release does not ship is not generated, however
// complete its declaration: the bundle naming its image is what says it exists.
func TestADeclaredDatabaseNeedsTheBundleToo(t *testing.T) {
	config.ResetDesktopMode()

	resp, err := Generate(basicCfg(), &bundle.Def{}, observerWorkspace(), SystemSecrets{JWT: "j", OIDC: "o"},
		observerSecrets())
	require.NoError(t, err)
	assert.Nil(t, findGeneratedApp(resp, observerDB))
	assert.False(t, WillGenerateDatabases(basicCfg(), &bundle.Def{}, observerWorkspace())[observerDB])
}

// Defaults: the smallest usable entry is a name and a type.
func TestABareNameIsAUsableEntry(t *testing.T) {
	specs := DatabaseSpecs(&bundle.WorkspaceConfig{
		AdditionalApps: []bundle.AdditionalAppProps{pgEntry(bundle.PostgresAppProps{Name: "x-postgres"})},
	})
	var got DatabaseSpec
	for _, s := range specs {
		if s.ID == "x-postgres" {
			got = s
		}
	}
	assert.Equal(t, "postgres", got.User)
	assert.Equal(t, "postgres", got.DB)
	assert.Equal(t, "postgres", got.Password)
	assert.Equal(t, "x-postgres", got.VolumeBase)
	assert.Equal(t, dbDefaultMemoryLimit, got.MemoryLimit)
	assert.Zero(t, got.Port, "a database nobody debugs locally publishes nothing")
}
