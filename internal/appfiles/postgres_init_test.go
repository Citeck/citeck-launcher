package appfiles

import (
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Every database the launcher creates is copied from template0. The default,
// template1, is refused by PostgreSQL once its recorded collation version
// differs from the image's C library — which is every cluster an older image
// initialized (measured on a live stand: data at glibc 2.36 under a Debian 13
// postgres:17.5 with 2.41, where "CREATE DATABASE x" failed and
// "CREATE DATABASE x TEMPLATE template0" created the database with the same
// encoding and locale). Without it no new webapp database can be created on
// such a stand.
func TestPostgresInitCreatesEveryDatabaseFromTemplate0(t *testing.T) {
	all, err := GetFiles()
	require.NoError(t, err)
	script, ok := all["postgres/init_db_and_user.sh"]
	require.True(t, ok, "the init script is embedded")

	creates := regexp.MustCompile(`(?i)CREATE DATABASE[^;]*;`).FindAllString(string(script), -1)
	require.NotEmpty(t, creates, "the script creates databases")
	for _, stmt := range creates {
		assert.Regexp(t, `(?i)TEMPLATE\s+template0`, stmt)
	}
}
