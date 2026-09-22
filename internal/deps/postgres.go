package deps

import "strconv"

// PostgresLegacyImage is what launchers before the pin ran (Kotlin 1.x and
// Go 2.x both defaulted to 17.x; only the major matters for the layout).
const PostgresLegacyImage = "postgres:17"

// PostgresLayout is how a PostgreSQL major's data is mounted INSIDE its
// volume. Which volume that is, is not a version question at all: it is the
// pin's generation (see VolumeName), which advances only when a migration
// completes. What the IMAGE dictates — and all this type carries — is the
// mount path, whether PGDATA has to be set, and where inside the volume a
// cluster announces its major.
type PostgresLayout struct {
	MountPath string // container path the volume is mounted at
	PGData    string // explicit PGDATA env; "" = image default
	// PGVersionRel is the path of PG_VERSION relative to the volume root — how
	// the launcher reads the data's major without starting a server.
	PGVersionRel string
}

// postgresLayoutRule is one entry of the layout TABLE below: the first major
// laid out this way, and the layout as a function of the major that runs it
// (the 18+ layout's PG_VERSION path carries the major, so it cannot be a
// constant).
type postgresLayoutRule struct {
	sinceMajor int
	// probeMajors are the majors whose data a pin-seeding probe can FIND under
	// this layout, newest first. A probe has no pin and no running container to
	// ask, so it can only look for PG_VERSION at a path it can spell out: for a
	// layout whose path carries the major that means one entry per major the
	// launcher knows about, while a layout whose path does not (the legacy one)
	// needs a single entry for all of them.
	probeMajors []int
	layout      func(major int) PostgresLayout
}

// postgresLayouts is everything the launcher knows about where PostgreSQL data
// lives INSIDE a volume, NEWEST FIRST. It is the ONE place a new layout is
// declared: both PostgresLayoutFor (what the generator mounts) and
// KnownPostgresDataPaths (what the pin-seeding probe looks for) are derived
// from it.
//
// Up to 17 the launcher mounted the data directory itself with an explicit
// PGDATA; from 18 the official image recommends mounting the PARENT
// (/var/lib/postgresql) and defaulting PGDATA to
// /var/lib/postgresql/<major>/docker, which is also what a future in-place
// pg_upgrade needs.
//
// WHEN 19 SHIPS: if it keeps the 18 layout, prepend 19 to that rule's
// probeMajors — without it, a namespace with pin-less 19 data on disk answers
// no probe and is seeded as legacy. If it moves the data again, add a rule
// above it; PostgresLayoutFor and the probe both follow from the table.
var postgresLayouts = []postgresLayoutRule{
	{
		sinceMajor:  18,
		probeMajors: []int{18},
		layout: func(major int) PostgresLayout {
			return PostgresLayout{
				MountPath:    "/var/lib/postgresql",
				PGVersionRel: strconv.Itoa(major) + "/docker/PG_VERSION",
			}
		},
	},
	{
		// The oldest rule matches every major below the one above it, so its
		// sinceMajor is 0 and PostgresLayoutFor always finds an answer. Its
		// PG_VERSION path carries no major, so ONE probe entry covers them all.
		sinceMajor:  0,
		probeMajors: []int{17},
		layout: func(int) PostgresLayout {
			return PostgresLayout{
				MountPath:    "/var/lib/postgresql/data",
				PGData:       "/var/lib/postgresql/data",
				PGVersionRel: "PG_VERSION",
			}
		},
	},
}

// PostgresLayoutFor returns the layout for a data major: the newest rule the
// major reaches. A major below every rule (0, a negative from an unparsed tag)
// gets the oldest layout — guessing the newest one would start a brand-new
// empty cluster beside data the launcher failed to identify.
func PostgresLayoutFor(major int) PostgresLayout {
	for _, r := range postgresLayouts {
		if major >= r.sinceMajor {
			return r.layout(major)
		}
	}
	oldest := postgresLayouts[len(postgresLayouts)-1]
	return oldest.layout(oldest.sinceMajor)
}

// KnownPostgresDataPaths returns every PG_VERSION path, relative to a volume
// root, that a pin-seeding probe should try — NEWEST FIRST, so the newest
// cluster in a volume is the one that answers.
//
// It answers "where inside a volume does a cluster announce itself", which is
// the whole of the layout question a probe can ask: WHICH volume to look in is
// the generation walk's question and has no version in it.
//
// It exists so that the probe seeding a pin from the data itself (the daemon's
// postgresPinFromData, which has no pin and no container to ask) does not have
// to name majors of its own: reading the order from here makes teaching it
// about a new layout — or about a new major inside an existing one — an edit
// to postgresLayouts and nowhere else.
func KnownPostgresDataPaths() []string {
	out := make([]string, 0, len(postgresLayouts))
	for _, r := range postgresLayouts {
		for _, major := range r.probeMajors {
			out = append(out, r.layout(major).PGVersionRel)
		}
	}
	return out
}

// postgresDescriptor describes ONE PostgreSQL cluster. A namespace can run more
// than one — the stand's own database and the observer's — and they share every
// rule (what a breaking move is, which layout a major uses, how a pin is seeded
// from PG_VERSION, which migration plan applies) while differing only in id,
// container name and volume stem. So the type carries those three as FIELDS
// rather than being a singleton: a second cluster is a registry entry, not a
// second copy of the rules.
//
// Everything that has to recognize "a PostgreSQL cluster, whichever one" asks
// IsPostgresFamily instead of comparing against the Postgres id.
type postgresDescriptor struct {
	id         ID
	appName    string
	volumeBase string
}

// IsPostgresFamily reports whether a dependency is a PostgreSQL cluster — the
// question the seeding probe, the migration plan and the rollback's version
// marker actually ask. They used to ask `d.ID() != Postgres`, which was the
// same thing only while there was exactly one.
func IsPostgresFamily(d Descriptor) bool {
	_, ok := d.(postgresDescriptor)
	return ok
}

func (d postgresDescriptor) ID() ID             { return d.id }
func (d postgresDescriptor) AppName() string    { return d.appName }
func (d postgresDescriptor) VolumeBase() string { return d.volumeBase }
func (postgresDescriptor) ParseVersion(image string) (Version, bool) {
	return ParseImageVersion(image)
}
func (postgresDescriptor) IsBreaking(from, to Version) bool { return majorBreaking(from, to) }
func (postgresDescriptor) UpgradeSupport(from, to Version) VendorSupport {
	return forwardOnlySupport(from, to)
}
func (postgresDescriptor) Migratable() bool    { return true }
func (postgresDescriptor) LegacyImage() string { return PostgresLegacyImage }
