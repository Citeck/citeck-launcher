package appdef

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// StringSet must preserve insertion order (LinkedHashSet parity) and dedup.
func TestStringSet_OrderAndDedup(t *testing.T) {
	s := NewStringSet("zookeeper", "postgres", "rabbitmq", "postgres")
	assert.Equal(t, StringSet{"zookeeper", "postgres", "rabbitmq"}, s)
	s.Add("zookeeper") // already present — no-op
	s.Add("keycloak")  // new — appended at the end
	assert.Equal(t, StringSet{"zookeeper", "postgres", "rabbitmq", "keycloak"}, s)
	assert.True(t, s.Has("postgres"))
	assert.False(t, s.Has("absent"))
	assert.False(t, StringSet(nil).Has("x")) // safe on nil
}

// Marshal emits an ordered list in both JSON and YAML (1.x Set<String> form).
func TestStringSet_MarshalsAsOrderedList(t *testing.T) {
	s := NewStringSet("keycloak", "postgres")

	j, err := json.Marshal(s)
	require.NoError(t, err)
	assert.JSONEq(t, `["keycloak","postgres"]`, string(j))

	y, err := yaml.Marshal(s)
	require.NoError(t, err)
	assert.Equal(t, "- keycloak\n- postgres\n", string(y))
}

// Unmarshal accepts the list form (order preserved).
func TestStringSet_UnmarshalList(t *testing.T) {
	var sj StringSet
	require.NoError(t, json.Unmarshal([]byte(`["keycloak","postgres"]`), &sj))
	assert.Equal(t, StringSet{"keycloak", "postgres"}, sj)

	var sy StringSet
	require.NoError(t, yaml.Unmarshal([]byte("- keycloak\n- postgres\n"), &sy))
	assert.Equal(t, StringSet{"keycloak", "postgres"}, sy)
}

// Unmarshal stays backward-compatible with the legacy {name: true} object form
// 2.x persisted: truthy keys only, taken sorted (a map has no order).
func TestStringSet_UnmarshalLegacyMap(t *testing.T) {
	var sj StringSet
	require.NoError(t, json.Unmarshal([]byte(`{"zookeeper":true,"postgres":true,"rabbitmq":false}`), &sj))
	assert.Equal(t, StringSet{"postgres", "zookeeper"}, sj)

	var sy StringSet
	require.NoError(t, yaml.Unmarshal([]byte("zookeeper: true\npostgres: true\nrabbitmq: false\n"), &sy))
	assert.Equal(t, StringSet{"postgres", "zookeeper"}, sy)
}

// An empty/absent dependsOn must be omitted (omitempty), and a populated one
// round-trips through the ApplicationDef YAML the config editor uses.
func TestStringSet_ApplicationDefRoundTrip(t *testing.T) {
	empty, err := yaml.Marshal(ApplicationDef{Name: "x", Image: "img:1"})
	require.NoError(t, err)
	assert.NotContains(t, string(empty), "dependsOn")

	def := ApplicationDef{Name: "gateway", Image: "img:1", DependsOn: NewStringSet("keycloak", "postgres")}
	data, err := yaml.Marshal(def)
	require.NoError(t, err)
	assert.Contains(t, string(data), "dependsOn:\n    - keycloak\n    - postgres\n")

	var back ApplicationDef
	require.NoError(t, yaml.Unmarshal(data, &back))
	assert.Equal(t, def.DependsOn, back.DependsOn)
}
