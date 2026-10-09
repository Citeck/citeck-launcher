package snapshot

import (
	"github.com/citeck/citeck-launcher/internal/deps"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestSnapshotPinsFollowExportedGenerations(t *testing.T) {
	pins := map[deps.ID]deps.DependencyState{
		deps.Postgres: {Image: "postgres:17.5", VolumeGen: 2, PrevImage: "postgres:16.9", PrevVolumeGen: 1},
		deps.Keycloak: {Image: "keycloak/keycloak:26.4.5"},
		deps.RabbitMQ: {Image: "rabbitmq:4.1.2-management", VolumeGen: 1},
	}
	activeOnly := DependencyStatesForVolumes([]VolumeSnapshotMeta{{Name: "postgres3"}}, pins)
	require.Len(t, activeOnly, 2)
	require.Equal(t, 2, activeOnly[deps.Postgres].Gen())
	require.Empty(t, activeOnly[deps.Postgres].PrevImage, "rollback needs its retained data")
	require.Contains(t, activeOnly, deps.Keycloak)
	both := DependencyStatesForVolumes([]VolumeSnapshotMeta{{Name: "postgres3"}, {Name: "postgres2"}}, pins)
	require.Equal(t, pins[deps.Postgres], both[deps.Postgres])
	oldOnly := DependencyStatesForVolumes([]VolumeSnapshotMeta{{Name: "postgres2"}}, pins)
	require.Empty(t, oldOnly, "the source pin must not describe an unexported generation")
}
