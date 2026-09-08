package migratetest

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/citeck/citeck-launcher/internal/appdef"
)

// The scripted PostgreSQL is the only "server" the plan tests ever talk to, so
// what it answers on which stream is pinned here: a plan that read the wrong
// stream would otherwise pass against a fake that happily returns both.
func TestScriptedPostgresAnswersOnTheRealStreams(t *testing.T) {
	ctx := context.Background()
	f := New()
	f.Containers["pg-src"] = appdef.ApplicationDef{Name: "postgres"}
	f.ExecFn = PostgresExec(map[string]PostgresInventory{"": HealthyPostgres()})

	exec := func(cmd ...string) (string, string, int) {
		t.Helper()
		stdout, stderr, code, err := f.Exec(ctx, "pg-src", cmd)
		require.NoError(t, err)
		return stdout, stderr, code
	}

	out, errOut, code := exec("pg_isready", "-h", "127.0.0.1", "-U", "postgres")
	assert.Contains(t, out, "accepting connections")
	assert.Empty(t, errOut)
	assert.Zero(t, code)

	out, _, _ = exec("psql", "-h", "127.0.0.1", "-U", "postgres", "-d", "postgres", "-q", "-At", "-c", "select 1")
	assert.Equal(t, "1", out)

	out, _, _ = exec("psql", "-d", "postgres", "-c", "SELECT datname FROM pg_database WHERE datistemplate = false")
	assert.Equal(t, "citeck_emodel\nciteck_keycloak\n", out)

	out, _, _ = exec("psql", "-d", "postgres", "-c", "SELECT rolname FROM pg_roles")
	assert.Equal(t, "citeck_emodel\npostgres\n", out)

	out, _, _ = exec("psql", "-d", "citeck_keycloak", "-c", "SELECT count(*) FROM information_schema.tables")
	assert.Equal(t, "91\n", out, "the count follows the -d database")

	// The restore's errors arrive on stderr with an exit code of 0 — the exact
	// shape that makes scanning stderr necessary.
	out, errOut, code = exec("psql", "-U", "postgres", "-f", "/citeck/depsmig/dump.sql")
	assert.Empty(t, out)
	assert.Equal(t, ToleratedRestoreError, errOut)
	assert.Zero(t, code)
}

func TestScriptedPostgresCanDifferPerContainer(t *testing.T) {
	ctx := context.Background()
	f := New()
	f.Containers["pg-src"] = appdef.ApplicationDef{}
	f.Containers["pg-dst"] = appdef.ApplicationDef{}
	short := HealthyPostgres()
	short.Tables = map[string]int{"citeck_emodel": 41, "citeck_keycloak": 91}
	f.ExecFn = PostgresExec(map[string]PostgresInventory{"": HealthyPostgres(), "pg-dst": short})

	cmd := []string{"psql", "-d", "citeck_emodel", "-c", "SELECT count(*) FROM information_schema.tables"}
	src, _, _, err := f.Exec(ctx, "pg-src", cmd)
	require.NoError(t, err)
	dst, _, _, err := f.Exec(ctx, "pg-dst", cmd)
	require.NoError(t, err)
	assert.Equal(t, "42\n", src)
	assert.Equal(t, "41\n", dst)
}
