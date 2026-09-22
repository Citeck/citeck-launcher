package migrate

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRestoreErrorsToleratesOnlyTheBootstrapRole(t *testing.T) {
	out := `psql:/citeck/depsmig/dump.sql:12: ERROR:  role "postgres" already exists
psql:/citeck/depsmig/dump.sql:40: NOTICE:  extension "plpgsql" already exists, skipping
psql:/citeck/depsmig/dump.sql:88: WARNING:  no privileges could be revoked for "public"
`
	assert.Empty(t, restoreErrors(out, defaultPgCreds))

	out += `psql:/citeck/depsmig/dump.sql:120: ERROR:  relation "foo" already exists
psql:/citeck/depsmig/dump.sql:121: ERROR:  role "citeck_emodel" already exists
`
	got := restoreErrors(out, defaultPgCreds)
	assert.Len(t, got, 2)
	assert.Contains(t, got[0], `relation "foo" already exists`)
	assert.Contains(t, got[1], `role "citeck_emodel" already exists`)
}

// psql prints its severities upper-case, so the match is case-SENSITIVE on
// purpose: a lower-case "error:" inside a dumped row value or a comment is
// data being replayed, not the server refusing a statement.
func TestRestoreErrorsMatchesUppercaseERRORonly(t *testing.T) {
	assert.Len(t, restoreErrors("ERROR: something\nerror: not an error line\n", defaultPgCreds), 1)
}

func TestRestoreErrorsTrimsAndIgnoresEmptyOutput(t *testing.T) {
	assert.Empty(t, restoreErrors("", defaultPgCreds))
	assert.Equal(t, []string{"psql:x.sql:1: ERROR:  boom"},
		restoreErrors("   psql:x.sql:1: ERROR:  boom   \n\n", defaultPgCreds))
}

// The second cluster's bootstrap objects are tolerated BY NAME — and only its
// own. Measured on a real stand: the observer's migration failed at `restore`
// with exactly these two lines, because its image creates both the role
// POSTGRES_USER names and the database POSTGRES_DB names before the dump gets
// a chance to.
func TestRestoreToleratesTheImagesOwnRoleAndDatabaseAndNothingElse(t *testing.T) {
	c := pgCreds{User: "observer", DB: "observer"}
	out := `psql:dump:1: ERROR:  role "observer" already exists
psql:dump:2: ERROR:  database "observer" already exists
`
	assert.Empty(t, restoreErrors(out, c))

	// Another database that already exists is NOT the image's doing: a fresh
	// cluster holds no such thing, so it is a real failure.
	assert.Len(t, restoreErrors(`ERROR:  database "citeck_emodel" already exists`, c), 1)
	// And the stand's own cluster must not start tolerating the observer's.
	assert.Len(t, restoreErrors(`ERROR:  role "observer" already exists`, defaultPgCreds), 1)
}
