package deps

import (
	"strconv"

	"github.com/citeck/citeck-launcher/internal/appdef"
)

// PostgresVolumeLegacy is the volume every namespace up to PostgreSQL 17 uses;
// PostgresVolumeV18 is where 18+ data lives. Two names because the layouts
// differ (see PostgresLayoutFor) and because a migration must build the new
// cluster NEXT TO the old data, never on top of it.
const (
	PostgresVolumeLegacy = "postgres2"
	PostgresVolumeV18    = "postgres3"
	// PostgresLegacyImage is what launchers before the pin ran (Kotlin 1.x and
	// Go 2.x both defaulted to 17.x; only the major matters for the layout).
	PostgresLegacyImage = "postgres:17"
)

// PostgresLayout is how a PostgreSQL major's data is mounted.
type PostgresLayout struct {
	Volume    string // plain volume name in the app def
	MountPath string // container path the volume is mounted at
	PGData    string // explicit PGDATA env; "" = image default
	// PGVersionRel is the path of PG_VERSION relative to the volume root — how
	// the launcher reads the data's major without starting a server.
	PGVersionRel string
}

// PostgresLayoutFor returns the layout for a data major. Up to 17 the launcher
// mounted the data directory itself with an explicit PGDATA; from 18 the
// official image recommends mounting the PARENT (/var/lib/postgresql) and
// defaulting PGDATA to /var/lib/postgresql/<major>/docker, which is also what
// a future in-place pg_upgrade needs.
func PostgresLayoutFor(major int) PostgresLayout {
	if major < 18 {
		return PostgresLayout{
			Volume:       PostgresVolumeLegacy,
			MountPath:    "/var/lib/postgresql/data",
			PGData:       "/var/lib/postgresql/data",
			PGVersionRel: "PG_VERSION",
		}
	}
	return PostgresLayout{
		Volume:       PostgresVolumeV18,
		MountPath:    "/var/lib/postgresql",
		PGVersionRel: strconv.Itoa(major) + "/docker/PG_VERSION",
	}
}

type postgresDescriptor struct{}

func (postgresDescriptor) ID() ID          { return Postgres }
func (postgresDescriptor) AppName() string { return appdef.AppPostgres }
func (postgresDescriptor) ParseVersion(image string) (Version, bool) {
	return ParseImageVersion(image)
}
func (postgresDescriptor) IsBreaking(from, to Version) bool { return majorBreaking(from, to) }
func (postgresDescriptor) Migratable() bool                 { return true }
func (postgresDescriptor) LegacyImage() string              { return PostgresLegacyImage }
