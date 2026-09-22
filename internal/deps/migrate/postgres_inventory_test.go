package migrate

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/deps/migrate/migratetest"
)

func inventoryEnv(t *testing.T, exec migratetest.ExecFunc) *migratetest.FakeEnv {
	t.Helper()
	env := migratetest.New()
	env.Containers[SrcContainer] = appdef.ApplicationDef{Name: "postgres"}
	env.ExecFn = exec
	return env
}

func TestReadInventoryCollectsDatabasesRolesAndTableCounts(t *testing.T) {
	env := inventoryEnv(t, migratetest.PostgresExec(
		map[string]migratetest.PostgresInventory{"": migratetest.HealthyPostgres()}))
	inv, err := readInventory(context.Background(), env, SrcContainer, defaultPgCreds)
	require.NoError(t, err)
	assert.Equal(t, []string{"citeck_emodel", "citeck_keycloak"}, inv.Databases)
	assert.Equal(t, []string{"citeck_emodel", "postgres"}, inv.Roles)
	assert.Equal(t, map[string]int{"citeck_emodel": 42, "citeck_keycloak": 91}, inv.Tables)
}

// psql writes results to stdout and diagnostics to stderr; an inventory read
// off the merged streams would count a notice as a database.
func TestReadInventoryParsesStdoutOnly(t *testing.T) {
	env := inventoryEnv(t, func(_, cmdline string) (string, string, int, error) {
		out, _, code, err := migratetest.PostgresExec(
			map[string]migratetest.PostgresInventory{"": migratetest.HealthyPostgres()})("", cmdline)
		return out, "NOTICE:  something entirely unrelated\nnot_a_database", code, err
	})
	inv, err := readInventory(context.Background(), env, SrcContainer, defaultPgCreds)
	require.NoError(t, err)
	assert.Equal(t, []string{"citeck_emodel", "citeck_keycloak"}, inv.Databases)
}

func TestReadInventoryFailsOnANonZeroExit(t *testing.T) {
	env := inventoryEnv(t, func(string, string) (string, string, int, error) {
		return "", "psql: FATAL:  the database system is starting up", 2, nil
	})
	_, err := readInventory(context.Background(), env, SrcContainer, defaultPgCreds)
	require.ErrorContains(t, err, "list databases")
	require.ErrorContains(t, err, "starting up")
}

func TestReadInventoryFailsOnACountThatIsNotANumber(t *testing.T) {
	base := migratetest.PostgresExec(map[string]migratetest.PostgresInventory{"": migratetest.HealthyPostgres()})
	env := inventoryEnv(t, func(c, cmdline string) (string, string, int, error) {
		if strings.Contains(cmdline, "information_schema.tables") {
			return "many\n", "", 0, nil
		}
		return base(c, cmdline)
	})
	_, err := readInventory(context.Background(), env, SrcContainer, defaultPgCreds)
	require.ErrorContains(t, err, "not a number")
}

func TestInventoryDiffNamesEveryDifference(t *testing.T) {
	src := pgInventory{
		Databases: []string{"a", "b"},
		Roles:     []string{"citeck", "postgres"},
		Tables:    map[string]int{"a": 10, "b": 20},
	}
	assert.Empty(t, src.diff(src))

	missingDB := src
	missingDB.Databases = []string{"a"}
	assert.Contains(t, joinDiff(src.diff(missingDB)), "databases differ")

	missingRole := src
	missingRole.Roles = []string{"postgres"}
	assert.Contains(t, joinDiff(src.diff(missingRole)), "roles differ")

	fewerTables := src
	fewerTables.Tables = map[string]int{"a": 10, "b": 19}
	d := src.diff(fewerTables)
	require.Len(t, d, 1)
	assert.Contains(t, d[0], "table count differs in b: source 20, target 19")

	// A database the target does not have at all is reported once as a missing
	// database and once as a missing table count — both are true, and the
	// table line is what names the data that did not arrive.
	assert.Len(t, src.diff(pgInventory{Databases: []string{"a"}, Roles: src.Roles, Tables: map[string]int{"a": 10}}), 2)
}

// An extra table in the target fails the verify exactly like a missing one.
// The restore replays ONE script into an EMPTY cluster, so a count that came
// out higher is not "the migration went well and then some" — it means
// something else created objects in there, and the target is not the copy of
// the source this step exists to certify. Equality is the rule in both
// directions; the message names which side is which.
func TestInventoryDiffReportsAnExtraTableToo(t *testing.T) {
	src := pgInventory{Tables: map[string]int{"a": 10}}
	d := src.diff(pgInventory{Tables: map[string]int{"a": 11}})
	require.Len(t, d, 1)
	assert.Contains(t, d[0], "source 10, target 11")
}

func joinDiff(d []string) string { return strings.Join(d, "\n") }
