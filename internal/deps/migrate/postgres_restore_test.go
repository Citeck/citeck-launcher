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
	assert.Empty(t, restoreErrors(out))

	out += `psql:/citeck/depsmig/dump.sql:120: ERROR:  relation "foo" already exists
psql:/citeck/depsmig/dump.sql:121: ERROR:  role "citeck_emodel" already exists
`
	got := restoreErrors(out)
	assert.Len(t, got, 2)
	assert.Contains(t, got[0], `relation "foo" already exists`)
	assert.Contains(t, got[1], `role "citeck_emodel" already exists`)
}

// psql prints its severities upper-case, so the match is case-SENSITIVE on
// purpose: a lower-case "error:" inside a dumped row value or a comment is
// data being replayed, not the server refusing a statement.
func TestRestoreErrorsMatchesUppercaseERRORonly(t *testing.T) {
	assert.Len(t, restoreErrors("ERROR: something\nerror: not an error line\n"), 1)
}

func TestRestoreErrorsTrimsAndIgnoresEmptyOutput(t *testing.T) {
	assert.Empty(t, restoreErrors(""))
	assert.Equal(t, []string{"psql:x.sql:1: ERROR:  boom"},
		restoreErrors("   psql:x.sql:1: ERROR:  boom   \n\n"))
}
