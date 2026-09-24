package namespace

import (
	"maps"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/deps"
)

// A pin whose data is provably gone (the Volumes page deleted it) holds
// nothing: the bundle image applies, nothing is offered as an upgrade, and the
// generation is kept, so the next start creates a fresh volume where the pin
// says the data lives instead of mounting an older one a migration retained.
func TestADatalessPinLetsTheBundleImageApply(t *testing.T) {
	bun := &bundle.Def{Applications: map[string]bundle.AppDef{
		appdef.AppPostgres: {Image: "postgres:18.6"},
		appdef.AppRabbitmq: {Image: "rabbitmq:4.2.9-management"},
	}}
	states := map[deps.ID]deps.DependencyState{
		deps.Postgres: {Image: "postgres:17.5", VolumeGen: 2},
		deps.RabbitMQ: {Image: "rabbitmq:4.1.2-management"},
	}
	resp, err := Generate(depsTestConfig(), withGateway(bun), depsTestWorkspace(),
		SystemSecrets{JWT: "j", OIDC: "o"}, GenerateOpts{
			DependencyStates:     states,
			DatalessDependencies: map[deps.ID]bool{deps.Postgres: true, deps.RabbitMQ: true},
		})
	require.NoError(t, err)

	pg := appByName(t, resp, appdef.AppPostgres)
	assert.Equal(t, "postgres:18.6", pg.Image)
	assert.Contains(t, pg.Volumes, "postgres3:/var/lib/postgresql",
		"the generation stays: the fresh cluster goes where the pin says the data lives")
	assert.Equal(t, "rabbitmq:4.2.9-management", appByName(t, resp, appdef.AppRabbitmq).Image)
	assert.Nil(t, upgradeFor(t, resp, deps.Postgres), "there is no data to migrate")
	assert.Nil(t, upgradeFor(t, resp, deps.RabbitMQ), "there is no data to migrate")
	assert.Equal(t, DependencyGen{Effective: "postgres:18.6", Candidate: "postgres:18.6"}, resp.Dependencies[deps.Postgres])
}

// Only the dependencies named dataless are released: the rest stay held.
func TestADatalessMarkReleasesOnlyItsOwnDependency(t *testing.T) {
	bun := &bundle.Def{Applications: map[string]bundle.AppDef{
		appdef.AppPostgres: {Image: "postgres:18.6"},
		appdef.AppRabbitmq: {Image: "rabbitmq:4.2.9-management"},
	}}
	resp, err := Generate(depsTestConfig(), withGateway(bun), depsTestWorkspace(),
		SystemSecrets{JWT: "j", OIDC: "o"}, GenerateOpts{
			DependencyStates: statesOf(map[deps.ID]string{
				deps.Postgres: "postgres:17.5", deps.RabbitMQ: "rabbitmq:4.1.2-management",
			}),
			DatalessDependencies: map[deps.ID]bool{deps.RabbitMQ: true},
		})
	require.NoError(t, err)
	assert.Equal(t, "postgres:17.5", appByName(t, resp, appdef.AppPostgres).Image)
	assert.NotNil(t, upgradeFor(t, resp, deps.Postgres), "postgres still has its data and is still held")
	assert.Equal(t, "rabbitmq:4.2.9-management", appByName(t, resp, appdef.AppRabbitmq).Image)
}

func withGateway(bun *bundle.Def) *bundle.Def {
	apps := map[string]bundle.AppDef{appdef.AppGateway: {Image: "citeck/gateway:1.0.0"}}
	maps.Copy(apps, bun.Applications)
	return &bundle.Def{Applications: apps, Dependencies: bun.Dependencies}
}
