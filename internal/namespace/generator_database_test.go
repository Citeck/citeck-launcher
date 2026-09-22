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

// The point of the whole file: a database nothing in Go names. A workspace
// declares it, and it gets a container, a registered dependency, a pinned
// image, a generation-counted volume — and therefore a migration and a
// rollback — with no launcher release.
func TestAWorkspaceCanDeclareADatabaseNoGoCodeKnows(t *testing.T) {
	config.ResetDesktopMode()
	t.Cleanup(deps.ResetExtraDependencies)

	ws := observerWorkspace()
	ws.Databases = []bundle.DatabaseProps{{
		ID:          "billing-postgres",
		Image:       "postgres:17.5",
		User:        "billing",
		Port:        14777,
		MemoryLimit: "256m",
		Settings:    map[string]string{"work_mem": "8MB", "shared_buffers": "64MB"},
	}}
	// The registry learns it exactly as the daemon does before generating.
	for _, s := range DatabaseSpecs(ws) {
		if s.ID == "billing-postgres" {
			deps.SetExtraDependencies([]deps.Descriptor{s.Descriptor()})
		}
	}

	resp, err := Generate(basicCfg(), &bundle.Def{}, ws, SystemSecrets{JWT: "j", OIDC: "o"})
	require.NoError(t, err)

	db := findGeneratedApp(resp, "billing-postgres")
	require.NotNil(t, db, "an unconditional declaration is generated wherever the workspace is used")
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

// A declaration may say which app it exists for; without that app the namespace
// pays nothing for it.
func TestADeclaredDatabaseFollowsItsOwner(t *testing.T) {
	config.ResetDesktopMode()
	t.Cleanup(deps.ResetExtraDependencies)

	ws := observerWorkspace()
	ws.Databases = []bundle.DatabaseProps{{ID: "billing-postgres", RequiredBy: "billing"}}
	deps.SetExtraDependencies([]deps.Descriptor{DatabaseSpecs(ws)[1].Descriptor()})

	noOwner, err := Generate(basicCfg(), &bundle.Def{}, ws, SystemSecrets{JWT: "j", OIDC: "o"})
	require.NoError(t, err)
	assert.Nil(t, findGeneratedApp(noOwner, "billing-postgres"))
	assert.False(t, WillGenerateDatabases(basicCfg(), &bundle.Def{}, ws)["billing-postgres"],
		"and the seeding must agree, or an absent cluster is probed on every load")

	withOwner := &bundle.Def{Applications: map[string]bundle.AppDef{"billing": {Image: "citeck/billing:1.0"}}}
	resp, err := Generate(basicCfg(), withOwner, ws, SystemSecrets{JWT: "j", OIDC: "o"})
	require.NoError(t, err)
	assert.NotNil(t, findGeneratedApp(resp, "billing-postgres"))
	assert.True(t, WillGenerateDatabases(basicCfg(), withOwner, ws)["billing-postgres"])
}

// The observer's database is the same mechanism with the launcher's own
// defaults, so a workspace can retune it without the launcher knowing — and
// what it changes must reach BOTH halves, or the service and its database
// disagree about the password.
func TestTheWorkspaceCanOverrideABuiltInDatabaseFieldByField(t *testing.T) {
	config.ResetDesktopMode()
	ws := observerWorkspace()
	ws.Databases = []bundle.DatabaseProps{{
		ID: appdef.AppObsPostgres, User: "obs2", MemoryLimit: "1g",
	}}

	resp, err := Generate(basicCfg(), observerBundle(), ws, SystemSecrets{JWT: "j", OIDC: "o"})
	require.NoError(t, err)

	db := findGeneratedApp(resp, appdef.AppObsPostgres)
	require.NotNil(t, db)
	user, _ := db.Environments.Get("POSTGRES_USER")
	assert.Equal(t, "obs2", user)
	assert.Equal(t, "1g", db.Resources.Limits.Memory)
	name, _ := db.Environments.Get("POSTGRES_DB")
	assert.Equal(t, "observer", name, "an untouched field keeps the launcher's default")

	obs := findGeneratedApp(resp, appdef.AppObserver)
	require.NotNil(t, obs)
	obsUser, _ := obs.Environments.Get("DATABASE_USER")
	assert.Equal(t, "obs2", obsUser,
		"the service reads the same declaration, or it cannot log in to its own database")
}

// Defaults: the smallest usable declaration is a bare id.
func TestABareIdIsAUsableDeclaration(t *testing.T) {
	specs := DatabaseSpecs(&bundle.WorkspaceConfig{Databases: []bundle.DatabaseProps{{ID: "x-postgres"}}})
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
