package h2migrate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/citeck/citeck-launcher/internal/appdef"
)

// TestApplicationDefCompat_KotlinFixture loads a hand-crafted Kotlin-shaped
// ApplicationDef JSON and asserts every field round-trips into the Go struct
// without loss. The fixture covers the three known shape mismatches
// (kind enum, dependsOn array, AppInitAction polymorphic) plus nested
// initContainers / probes / startup conditions.
func TestApplicationDefCompat_KotlinFixture(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "applicationdef_v1.json"))
	require.NoError(t, err)

	got, err := decodeKotlinApplicationDef(data)
	require.NoError(t, err)

	assert.Equal(t, "eapps", got.Name)
	assert.Equal(t, "citeck/ecos-apps:1.0.0", got.Image)
	assert.Equal(t, "-Xms256m -Xmx1024m", got.Environments["JAVA_OPTS"])
	assert.Equal(t, "prod", got.Environments["SPRING_PROFILES_ACTIVE"])
	assert.Equal(t, []string{"sh", "-c", "exec /entrypoint.sh"}, got.Cmd)
	assert.Equal(t, []string{"8080:8080"}, got.Ports)
	assert.Equal(t, []string{"./eapps/conf:/opt/citeck/conf:ro"}, got.Volumes)
	assert.Equal(t, "abc123", got.VolumesContentHash)

	// kind enum: CITECK_CORE -> KindCiteckCore (0)
	assert.Equal(t, appdef.KindCiteckCore, got.Kind)

	// dependsOn: array -> ordered StringSet (insertion order preserved)
	assert.Equal(t, appdef.StringSet{"postgres", "rabbitmq"}, got.DependsOn)

	// initActions: {type, command} -> Exec list wrapped with sh -c
	require.Len(t, got.InitActions, 1)
	assert.Equal(t, []string{"sh", "-c", "echo hello && /bin/true"}, got.InitActions[0].Exec)

	// startupConditions: nested probe preserved
	require.Len(t, got.StartupConditions, 1)
	require.NotNil(t, got.StartupConditions[0].Probe)
	require.NotNil(t, got.StartupConditions[0].Probe.HTTP)
	assert.Equal(t, "/actuator/health", got.StartupConditions[0].Probe.HTTP.Path)
	assert.Equal(t, 8080, got.StartupConditions[0].Probe.HTTP.Port)

	// livenessProbe: exec preserved
	require.NotNil(t, got.LivenessProbe)
	require.NotNil(t, got.LivenessProbe.Exec)
	assert.Equal(t, []string{"/bin/sh", "-c", "curl -f http://localhost:8080/actuator/health"}, got.LivenessProbe.Exec.Command)
	assert.Equal(t, 3, got.LivenessProbe.FailureThreshold)

	// resources preserved
	require.NotNil(t, got.Resources)
	assert.Equal(t, "1024m", got.Resources.Limits.Memory)
	assert.Equal(t, "128m", got.ShmSize)

	// initContainers: kind enum + cmd preserved
	require.Len(t, got.InitContainers, 1)
	assert.Equal(t, "busybox:1.36", got.InitContainers[0].Image)
	assert.Equal(t, appdef.KindThirdParty, got.InitContainers[0].Kind)
	assert.Equal(t, []string{"sh", "-c", "exec /init.sh"}, got.InitContainers[0].Cmd)
	assert.Equal(t, []string{"./shared:/data"}, got.InitContainers[0].Volumes)
}

// TestApplicationDefCompat_EmptyKind exercises the fallback for missing /
// unknown kind values (Kotlin Builder default = THIRD_PARTY).
func TestApplicationDefCompat_EmptyKind(t *testing.T) {
	raw, _ := json.Marshal(map[string]any{
		"name":  "x",
		"image": "img",
	})
	got, err := decodeKotlinApplicationDef(raw)
	require.NoError(t, err)
	assert.Equal(t, appdef.KindThirdParty, got.Kind)
}

// TestApplicationDefCompat_AllKindValues asserts that every Kotlin enum name
// maps to the matching Go constant.
func TestApplicationDefCompat_AllKindValues(t *testing.T) {
	cases := map[string]appdef.ApplicationKind{
		"CITECK_CORE":           appdef.KindCiteckCore,
		"CITECK_CORE_EXTENSION": appdef.KindCiteckCoreExtension,
		"CITECK_ADDITIONAL":     appdef.KindCiteckAdditional,
		"THIRD_PARTY":           appdef.KindThirdParty,
		"":                      appdef.KindThirdParty,
		"NONSENSE":              appdef.KindThirdParty,
	}
	for in, want := range cases {
		t.Run(in, func(t *testing.T) {
			raw, _ := json.Marshal(map[string]any{"name": "n", "kind": in})
			got, err := decodeKotlinApplicationDef(raw)
			require.NoError(t, err)
			assert.Equal(t, want, got.Kind)
		})
	}
}
