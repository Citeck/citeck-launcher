package daemon

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/citeck/citeck-launcher/internal/deps"
)

// The field case (2026-09-24, desktop 2.12.2): a namespace ran on postgres
// 17.5 and rabbitmq 4.1.2, its volumes were deleted, and the bundle offered
// 18.6 / 4.2.9. The pins still named the old versions, so the dashboard showed
// "upgrade available" and the migration then refused because the volume it
// would read did not exist. With no data there is nothing to hold back.
func TestDatalessPinsFindThePinsWhoseDataIsGone(t *testing.T) {
	pins := map[deps.ID]deps.DependencyState{
		deps.Postgres: {Image: "postgres:17.5"},
		deps.RabbitMQ: {Image: "rabbitmq:4.1.2-management"},
		deps.Keycloak: {Image: "keycloak/keycloak:26.4"},
	}
	got := datalessPins(context.Background(), pins, fakeProbe{}, nil, false)
	assert.Equal(t, map[deps.ID]bool{deps.Postgres: true, deps.RabbitMQ: true, deps.Keycloak: true}, got,
		"no container and no volume: nothing to protect — keycloak's data is the postgres database")
}

func TestDatalessPinsKeepEveryPinThatMayStillHaveData(t *testing.T) {
	pins := map[deps.ID]deps.DependencyState{
		deps.Postgres: {Image: "postgres:17.5"},
		deps.RabbitMQ: {Image: "rabbitmq:4.1.2-management"},
	}
	cases := map[string]fakeProbe{
		"the volume exists (even an empty one is data)": {volumes: map[string]map[string]string{
			"postgres2": {}, "rabbitmq2": {},
		}},
		"a container exists": {containers: map[string]string{
			"postgres": "postgres:17.5", "rabbitmq": "rabbitmq:4.1.2-management",
		}},
		"the volume check failed (I could not ask is not there is nothing)": {volumeErr: errors.New("docker down")},
		"the container check failed": {containerErr: errors.New("docker down")},
	}
	for name, p := range cases {
		t.Run(name, func(t *testing.T) {
			assert.Empty(t, datalessPins(context.Background(), pins, p, nil, false))
		})
	}
}

// An open migration journal means temp containers and half-built volumes may
// be on the host: nothing about the data can be concluded from its volumes.
func TestDatalessPinsReleaseNothingWhileAMigrationIsOpen(t *testing.T) {
	pins := map[deps.ID]deps.DependencyState{deps.Postgres: {Image: "postgres:17.5"}}
	assert.Empty(t, datalessPins(context.Background(), pins, fakeProbe{}, nil, true))
}

// The volume asked about is the pinned GENERATION's: after a migration the
// retained older volume may still be on disk, and it is not this pin's data.
func TestDatalessPinsAskAboutThePinnedGeneration(t *testing.T) {
	var asked []string
	p := fakeProbe{
		volumes:      map[string]map[string]string{"postgres2": {"PG_VERSION": "17\n"}},
		askedVolumes: &asked,
	}
	pins := map[deps.ID]deps.DependencyState{deps.Postgres: {Image: "postgres:18.6", VolumeGen: 2}}
	assert.Equal(t, map[deps.ID]bool{deps.Postgres: true}, datalessPins(context.Background(), pins, p, nil, false),
		"postgres3 is gone; the retained postgres2 does not make the pin's data exist")
	assert.Equal(t, []string{"postgres3"}, asked)
}

// Keycloak's state lives in the postgres database, so postgres data keeps its
// pin even though keycloak has no volume and no container of its own.
func TestDatalessPinsJudgeKeycloakByThePostgresData(t *testing.T) {
	pins := map[deps.ID]deps.DependencyState{
		deps.Postgres: {Image: "postgres:17.5"},
		deps.Keycloak: {Image: "keycloak/keycloak:26.4"},
	}
	p := fakeProbe{volumes: map[string]map[string]string{"postgres2": {"PG_VERSION": "17\n"}}}
	assert.Empty(t, datalessPins(context.Background(), pins, p, nil, false))
}

// A dependency this namespace does not have is not asked about at all.
func TestDatalessPinsSkipAbsentDependencies(t *testing.T) {
	var apps []string
	pins := map[deps.ID]deps.DependencyState{deps.MongoDB: {Image: "mongo:4.4"}}
	got := datalessPins(context.Background(), pins, fakeProbe{askedApps: &apps}, map[deps.ID]bool{deps.MongoDB: false}, false)
	assert.Empty(t, got)
	assert.Empty(t, apps)
}
