package bundle

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidatePostgresDatabaseName(t *testing.T) {
	for _, ok := range []string{"litellm", "a", "gateway_spend2", strings.Repeat("a", 63)} {
		assert.NoError(t, ValidatePostgresDatabaseName(ok), ok)
	}
	for _, bad := range []string{
		"", "LiteLLM", "1db", "_db", "lite-llm", "db name", "db;rm -rf /", "db'", strings.Repeat("a", 64),
		"postgres", "template0", "template1", "citeck_emodel", "citeck_keycloak", "pg_monitor",
	} {
		assert.Error(t, ValidatePostgresDatabaseName(bad), bad)
	}
}

func TestParseWorkspacePostgresDatabases(t *testing.T) {
	raw := []byte(`
additionalApps:
  - name: litellm
    postgresDatabases: [litellm]
    environments:
      DATABASE_URL: postgresql://litellm:litellm@${PG_HOST}:${PG_PORT}/litellm
  - name: observer-postgres
    type: POSTGRES
`)
	cfg := parseWorkspaceConfig(raw, "workspace-v2.yml", nil)
	require.NotNil(t, cfg)
	require.Len(t, cfg.AdditionalApps, 2)
	assert.Equal(t, []string{"litellm"}, cfg.AdditionalApps[0].PostgresDatabases)
}

func TestParseWorkspaceInvalidDatabaseCostsOnlyItsEntry(t *testing.T) {
	raw := []byte(`
additionalApps:
  - name: litellm
    postgresDatabases: [citeck_emodel]
  - name: observer-postgres
    type: POSTGRES
  - name: edi-sim
    image: example/edi-sim:1.0.0
`)
	cfg := parseWorkspaceConfig(raw, "workspace-v2.yml", nil)
	require.NotNil(t, cfg)
	var names []string
	for _, a := range cfg.AdditionalApps {
		names = append(names, a.Name)
	}
	assert.Equal(t, []string{"observer-postgres", "edi-sim"}, names,
		"the entry with the platform's database is dropped, the rest of the section stays")
}
