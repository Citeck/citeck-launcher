// Package deps describes the infrastructure dependencies the launcher runs
// (PostgreSQL, RabbitMQ, ZooKeeper, Keycloak, MongoDB): how to read a version
// out of an image tag, which version changes are significant enough that the
// launcher must NOT apply them silently to an existing namespace, and which of
// them the launcher can migrate itself. It is pure — no Docker, no I/O — so
// the generator can consult it and every rule is unit-testable.
package deps

// ID names a dependency. It is the key of the per-namespace pin map and the
// value the API and CLI use, so it must stay stable.
type ID string

// Registered dependency ids.
const (
	Postgres  ID = "postgres"
	RabbitMQ  ID = "rabbitmq"
	Zookeeper ID = "zookeeper"
	Keycloak  ID = "keycloak"
	MongoDB   ID = "mongodb"
)

// Descriptor is what the launcher knows about one dependency.
type Descriptor interface {
	ID() ID
	// AppName is the generated app (container) name — appdef.App* constant.
	AppName() string
	// ParseVersion reads the version out of an image reference; ok=false for
	// an unknown tag.
	ParseVersion(image string) (Version, bool)
	// IsBreaking reports whether moving the DATA from one version to the other
	// needs a migration rather than a plain container recreate.
	IsBreaking(from, to Version) bool
	// Migratable reports whether THIS launcher has a migration plan for it.
	Migratable() bool
	// LegacyImage is what every launcher before the pin existed ran, used to
	// seed a pin when nothing better (container, data file) is available.
	LegacyImage() string
}

// registry is fixed and ordered: the order is the display order everywhere.
var registry = []Descriptor{
	postgresDescriptor{},
	rabbitDescriptor{},
	zookeeperDescriptor{},
	keycloakDescriptor{},
	mongoDescriptor{},
}

// All returns every registered descriptor in display order.
func All() []Descriptor {
	out := make([]Descriptor, len(registry))
	copy(out, registry)
	return out
}

// Lookup finds a descriptor by id.
func Lookup(id ID) (Descriptor, bool) {
	for _, d := range registry {
		if d.ID() == id {
			return d, true
		}
	}
	return nil, false
}

// ByApp finds the descriptor for a generated app name.
func ByApp(app string) (Descriptor, bool) {
	for _, d := range registry {
		if d.AppName() == app {
			return d, true
		}
	}
	return nil, false
}

// Breaking answers the generator's question for two image references.
// An unparsable tag on either side is breaking: keeping the pin and reporting
// is the safe direction, the alternative is a silent swap onto data we do not
// understand.
func Breaking(d Descriptor, pinned, candidate string) bool {
	from, okFrom := d.ParseVersion(pinned)
	to, okTo := d.ParseVersion(candidate)
	if !okFrom || !okTo {
		return true
	}
	return d.IsBreaking(from, to)
}

// majorBreaking / minorBreaking are the two rule shapes v1 uses.
func majorBreaking(from, to Version) bool { return from.Major != to.Major }
func minorBreaking(from, to Version) bool {
	return from.Major != to.Major || from.Minor != to.Minor
}
